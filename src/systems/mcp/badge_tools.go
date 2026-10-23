package mcp

import (
	"context"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hmain/cainban/src/systems/auth"
)

// CreateBadgeURLArgs are the arguments for the create_badge_url tool.
type CreateBadgeURLArgs struct {
	// Repo is the "owner/repo" to create a badge URL for. Must be one of the
	// repos your token authorizes. Omit to use the connection's default repo.
	Repo string `json:"repo,omitempty" jsonschema:"optional owner/repo; must be authorized by your token. Omit to use the default repo."`
	// ExpiresIn is the duration until the URL expires (e.g. "30d", "7d", "1y").
	// Default is 30 days; maximum is 1 year. Use "0" for no expiry.
	ExpiresIn string `json:"expires_in,omitempty" jsonschema:"duration until expiry (e.g. '30d', '7d', '1y', or '0' for no expiry). Default: 30d, max: 1y."`
	// Style is the badge style (flat, flat-square, plastic). Default: flat.
	Style string `json:"style,omitempty" jsonschema:"badge style: flat, flat-square, or plastic. Default: flat."`
	// Label is the left-side text. Default: cainban.
	Label string `json:"label,omitempty" jsonschema:"left-side label text. Default: cainban."`
	// Color is the right-side fill color (name like 'green' or hex like '#4cd07d').
	Color string `json:"color,omitempty" jsonschema:"right-side fill color (name or hex). Default: blue."`
}

// CreateBadgeURLResult is the result of the create_badge_url tool.
type CreateBadgeURLResult struct {
	URL       string `json:"url"`
	ExpiresAt string `json:"expires_at,omitempty"`
	Markdown  string `json:"markdown"`
}

// handleCreateBadgeURL generates a signed badge URL. The caller must be
// authorized for the repo (via their token). This is a read-only tool: it does
// not persist anything, it simply computes a signed URL that the public badge
// endpoint will accept.
func (s *Server) handleCreateBadgeURL(ctx context.Context, _ *mcp.CallToolRequest, args CreateBadgeURLArgs) (*mcp.CallToolResult, *CreateBadgeURLResult, error) {
	// Scope the badge to the repo this connection is ALREADY authorized for.
	//
	// Under the GitHub-App authorization model a user token carries no `repos`
	// claim; authorization is a live, per-request App check the resolver ran
	// BEFORE this handler, and its result is the tenant's scoped repo
	// (repoFromCtx). So the only repo we may mint a badge for is that one —
	// re-checking an override against the (now-empty) AuthorizedRepos claim
	// would wrongly reject every GitHub-token caller.
	//
	// A caller that names a DIFFERENT repo is rejected: the resolver has not
	// authorized this connection for it. To badge another repo, point the
	// connection at it (the X-Cainban-Repo header or a default_repo) and mint
	// again. Local CLI/stdio has no tenant and no scope restriction.
	scoped := repoFromCtx(ctx)
	repo := scoped
	if args.Repo != "" {
		norm, err := auth.NormalizeRepo(args.Repo)
		if err != nil {
			return toolError("invalid repo %q: %v", args.Repo, err), nil, nil
		}
		if scoped != "" && norm != scoped {
			return toolError(
				"badges can only be created for this connection's repo (%q); reconnect scoped to %q (set the %s header or a default_repo) to badge it",
				scoped, norm, auth.HeaderTargetRepo,
			), nil, nil
		}
		repo = norm
	}
	if repo == "" {
		return toolError("no repo specified and no default repo on this connection; pass the repo argument"), nil, nil
	}

	// Check that badge signing is configured.
	if badgeSecret() == "" {
		return toolError("badge signing is not configured (CAINBAN_BADGE_SECRET not set)"), nil, nil
	}

	// Parse expiry duration.
	var expiry int64
	var expiresAtStr string
	expiresIn := args.ExpiresIn
	if expiresIn == "" {
		expiresIn = "30d" // default
	}
	if expiresIn != "0" {
		dur, err := parseDuration(expiresIn)
		if err != nil {
			return toolError("invalid expires_in: %v", err), nil, nil
		}
		// Max 1 year.
		maxDur := 365 * 24 * time.Hour
		if dur > maxDur {
			return toolError("expires_in exceeds maximum of 1 year"), nil, nil
		}
		expiresAt := time.Now().Add(dur)
		expiry = expiresAt.Unix()
		expiresAtStr = expiresAt.UTC().Format(time.RFC3339)
	}

	// Generate the signature.
	sig := SignBadgeURL(repo, expiry)
	if sig == "" {
		return toolError("failed to generate badge signature"), nil, nil
	}

	// Build the URL with query params.
	// Base URL will be provided by the client; we return a relative path.
	path := fmt.Sprintf("/badge/%s.svg?sig=%s", repo, sig)
	if expiry > 0 {
		path += fmt.Sprintf("&exp=%d", expiry)
	}
	if args.Style != "" && args.Style != "flat" {
		path += fmt.Sprintf("&style=%s", args.Style)
	}
	if args.Label != "" && args.Label != "cainban" {
		path += fmt.Sprintf("&label=%s", args.Label)
	}
	if args.Color != "" {
		path += fmt.Sprintf("&color=%s", args.Color)
	}

	// Build markdown for README embedding.
	// The user will need to prepend their base URL.
	markdown := fmt.Sprintf("![cainban](%s)", path)

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: fmt.Sprintf(
				"Signed badge URL created.\n\nPath: %s\n\nTo embed in your README, prepend your cainban API URL:\n\n```markdown\n![cainban](https://YOUR_API_URL%s)\n```\n\nExpires: %s",
				path,
				path,
				expiresAtStr,
			)},
		},
	}, &CreateBadgeURLResult{
		URL:       path,
		ExpiresAt: expiresAtStr,
		Markdown:  markdown,
	}, nil
}

// parseDuration parses a duration string like "30d", "7d", "1y", "24h".
func parseDuration(s string) (time.Duration, error) {
	if len(s) < 2 {
		return 0, fmt.Errorf("invalid duration %q", s)
	}
	unit := s[len(s)-1]
	numStr := s[:len(s)-1]
	var num int
	if _, err := fmt.Sscanf(numStr, "%d", &num); err != nil {
		return 0, fmt.Errorf("invalid duration %q: %w", s, err)
	}
	if num <= 0 {
		return 0, fmt.Errorf("duration must be positive: %q", s)
	}

	switch unit {
	case 'h':
		return time.Duration(num) * time.Hour, nil
	case 'd':
		return time.Duration(num) * 24 * time.Hour, nil
	case 'w':
		return time.Duration(num) * 7 * 24 * time.Hour, nil
	case 'y':
		return time.Duration(num) * 365 * 24 * time.Hour, nil
	default:
		return 0, fmt.Errorf("unknown duration unit %q (use h, d, w, or y)", string(unit))
	}
}
