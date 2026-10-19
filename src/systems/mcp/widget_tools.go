package mcp

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/hmain/cainban/src/systems/auth"
	"github.com/hmain/cainban/src/systems/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// widgetSecret reads the HMAC signing secret for widget tokens from the
// environment. An empty secret disables the widget-token feature: no token can
// be minted (create_widget_token errors) and no widget token is accepted at the
// auth edge.
func widgetSecret() []byte {
	return []byte(strings.TrimSpace(os.Getenv(EnvWidgetSecret)))
}

// resolveWidgetTokenStore opens the per-request widget-token metadata store for
// the request's AUTHORIZED tenant. It fails closed on an unscoped/local tenant
// exactly like resolveTaskSystem: widget tokens are always repo-scoped, so a
// request with no resolved repo cannot address a token namespace.
func (s *Server) resolveWidgetTokenStore(ctx context.Context) (store.WidgetTokenStore, func(), string, error) {
	prefix, err := partitionPrefixForCall(ctx, "")
	if err != nil {
		return nil, nil, "", err
	}
	if prefix == "" {
		return nil, nil, "", fmt.Errorf("widget tokens require a repo scope; set the %s header or a default_repo claim", auth.HeaderTargetRepo)
	}
	ts, closer, err := store.OpenWidgetTokenStoreForTenant(context.Background(), prefix)
	if err != nil {
		return nil, nil, "", err
	}
	return ts, func() { _ = closer() }, repoFromCtx(ctx), nil
}

// --- create_widget_token ---------------------------------------------------

type CreateWidgetTokenArgs struct {
	Repo       string `json:"repo,omitempty" jsonschema:"optional owner/repo to mint a token for; must be one of the repos your token authorizes. Omit to use the connection's default repo."`
	ExpiryDays int    `json:"expiry_days,omitempty" jsonschema:"days until the token expires (default 30, max 365)"`
}

type createWidgetTokenResult struct {
	Token     string    `json:"token"`
	TokenID   string    `json:"token_id"`
	Repo      string    `json:"repo"`
	ExpiresAt time.Time `json:"expires_at"`
}

// handleCreateWidgetToken mints a read-only, repo-scoped widget token. The repo
// is authorized against the caller's signed claim (partitionPrefixForCall) BEFORE
// a token is minted, so a caller can only create tokens for repos they already
// have. The token string is returned ONCE here (it is never stored); only its
// metadata is persisted for later listing/revocation.
func (s *Server) handleCreateWidgetToken(ctx context.Context, _ *mcp.CallToolRequest, args CreateWidgetTokenArgs) (*mcp.CallToolResult, *createWidgetTokenResult, error) {
	if isReadOnlyTenant(ctx) {
		return toolError("this is a read-only token; it cannot create widget tokens"), nil, nil
	}
	secret := widgetSecret()
	if len(secret) == 0 {
		return toolError("widget tokens are not enabled on this server (no %s configured)", EnvWidgetSecret), nil, nil
	}

	// Authorize the target repo against the signed claim. An unauthorized or
	// invalid repo fails here before anything is minted or stored.
	prefix, err := partitionPrefixForCall(ctx, args.Repo)
	if err != nil {
		return toolError("%s", err.Error()), nil, nil
	}
	if prefix == "" {
		return toolError("widget tokens require a repo scope; set the %s header, pass repo, or set a default_repo claim", auth.HeaderTargetRepo), nil, nil
	}
	repo := repoFromCtx(ctx)
	if strings.TrimSpace(args.Repo) != "" {
		if norm, nerr := auth.NormalizeRepo(args.Repo); nerr == nil {
			repo = norm
		}
	}

	days := args.ExpiryDays
	if days <= 0 {
		days = defaultWidgetExpiryDays
	}
	if days > maxWidgetExpiryDays {
		return toolError("expiry_days %d exceeds the maximum of %d", days, maxWidgetExpiryDays), nil, nil
	}

	id, err := newWidgetTokenID()
	if err != nil {
		return nil, nil, err
	}
	now := time.Now().UTC()
	ttl := time.Duration(days) * 24 * time.Hour
	token, exp, err := mintWidgetToken(secret, id, repo, ttl, now)
	if err != nil {
		return nil, nil, err
	}
	expiresAt := time.Unix(exp, 0).UTC()

	tokenStore, closeFn, _, err := s.resolveWidgetTokenStore(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer closeFn()

	meta := store.WidgetTokenMeta{
		TokenID:   id,
		Repo:      repo,
		CreatedBy: actorFromCtx(ctx),
		CreatedAt: now,
		ExpiresAt: expiresAt,
		Revoked:   false,
	}
	if err := tokenStore.PutWidgetToken(ctx, meta); err != nil {
		return nil, nil, fmt.Errorf("failed to store widget token metadata: %w", err)
	}

	res := &createWidgetTokenResult{Token: token, TokenID: id, Repo: repo, ExpiresAt: expiresAt}
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: fmt.Sprintf(
				"Created read-only widget token for %s (id %s, expires %s).\nToken (shown once — store it now):\n%s",
				repo, id, expiresAt.Format(time.RFC3339), token)},
		},
	}, res, nil
}

// --- list_widget_tokens ----------------------------------------------------

type ListWidgetTokensArgs struct {
	Repo string `json:"repo,omitempty" jsonschema:"optional owner/repo whose tokens to list; must be one of the repos your token authorizes. Omit to use the connection's default repo."`
}

type widgetTokenSummary struct {
	TokenID   string    `json:"token_id"`
	Repo      string    `json:"repo"`
	CreatedBy string    `json:"created_by,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
	Revoked   bool      `json:"revoked"`
	Expired   bool      `json:"expired"`
}

// handleListWidgetTokens lists the widget tokens for a repo the caller is
// authorized for. It shows a truncated token id, timestamps and the revoked /
// expired state so the caller can decide what to revoke. The full token string
// is never recoverable (only its metadata is stored).
func (s *Server) handleListWidgetTokens(ctx context.Context, _ *mcp.CallToolRequest, args ListWidgetTokensArgs) (*mcp.CallToolResult, []widgetTokenSummary, error) {
	// Authorize the repo (if an override was given) against the signed claim.
	if _, err := partitionPrefixForCall(ctx, args.Repo); err != nil {
		return toolError("%s", err.Error()), nil, nil
	}

	tokenStore, closeFn, repo, err := s.resolveWidgetTokenStoreForRepo(ctx, args.Repo)
	if err != nil {
		return nil, nil, err
	}
	defer closeFn()

	metas, err := tokenStore.ListWidgetTokens(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list widget tokens: %w", err)
	}

	now := time.Now().UTC()
	out := make([]widgetTokenSummary, 0, len(metas))
	for _, m := range metas {
		out = append(out, widgetTokenSummary{
			TokenID:   m.TokenID,
			Repo:      m.Repo,
			CreatedBy: m.CreatedBy,
			CreatedAt: m.CreatedAt,
			ExpiresAt: m.ExpiresAt,
			Revoked:   m.Revoked,
			Expired:   !m.ExpiresAt.IsZero() && now.After(m.ExpiresAt),
		})
	}

	if len(out) == 0 {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("No widget tokens for %s.", repo)}},
		}, out, nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Widget tokens for %s:", repo)
	for _, t := range out {
		state := "active"
		switch {
		case t.Revoked:
			state = "revoked"
		case t.Expired:
			state = "expired"
		}
		fmt.Fprintf(&sb, "\n• %s… created %s · expires %s · %s",
			truncateTokenID(t.TokenID), t.CreatedAt.Format(time.RFC3339), t.ExpiresAt.Format(time.RFC3339), state)
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: sb.String()}}}, out, nil
}

// truncateTokenID returns the first 8 chars of a token id for display, so a
// listing never surfaces the full id in logs/UI more than necessary (the full
// id is still in the structured content for revoke).
func truncateTokenID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

// --- revoke_widget_token ---------------------------------------------------

type RevokeWidgetTokenArgs struct {
	TokenID string `json:"token_id" jsonschema:"the id of the widget token to revoke (from list_widget_tokens)"`
	Repo    string `json:"repo,omitempty" jsonschema:"optional owner/repo the token belongs to; must be one of the repos your token authorizes. Omit to use the connection's default repo."`
}

// handleRevokeWidgetToken revokes a widget token by id. Authorization is
// twofold: the caller must be authorized for the repo (partitionPrefixForCall,
// so the token namespace is in scope), AND must be the repo owner OR the token's
// creator. Any authorized caller for the repo IS effectively the owner in this
// model (the signed `repos` claim is the grant), and the creator check lets a
// non-owner revoke their own token.
func (s *Server) handleRevokeWidgetToken(ctx context.Context, _ *mcp.CallToolRequest, args RevokeWidgetTokenArgs) (*mcp.CallToolResult, any, error) {
	if isReadOnlyTenant(ctx) {
		return toolError("this is a read-only token; it cannot revoke widget tokens"), nil, nil
	}
	if strings.TrimSpace(args.TokenID) == "" {
		return toolError("token_id is required; call list_widget_tokens to see ids"), nil, nil
	}
	if _, err := partitionPrefixForCall(ctx, args.Repo); err != nil {
		return toolError("%s", err.Error()), nil, nil
	}

	tokenStore, closeFn, repo, err := s.resolveWidgetTokenStoreForRepo(ctx, args.Repo)
	if err != nil {
		return nil, nil, err
	}
	defer closeFn()

	meta, err := tokenStore.GetWidgetToken(ctx, args.TokenID)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read widget token: %w", err)
	}
	if meta == nil {
		return toolError("widget token %q not found for %s", args.TokenID, repo), nil, nil
	}

	// Owner-or-creator check. Being authorized for the repo (verified above) is
	// the ownership grant in cainban's repo-scoped model; the creator check is
	// retained so the message is accurate and future non-owner grants still let
	// a creator revoke their own token.
	actor := actorFromCtx(ctx)
	isCreator := meta.CreatedBy != "" && meta.CreatedBy == actor
	isRepoOwner := isAuthorizedForRepo(ctx, meta.Repo)
	if !isRepoOwner && !isCreator {
		return toolError("not authorized to revoke token %q (must be the repo owner or the token creator)", args.TokenID), nil, nil
	}

	ok, err := tokenStore.RevokeWidgetToken(ctx, args.TokenID)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to revoke widget token: %w", err)
	}
	if !ok {
		return toolError("widget token %q not found for %s", args.TokenID, repo), nil, nil
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Revoked widget token %s for %s.", args.TokenID, repo)}},
	}, nil, nil
}

// resolveWidgetTokenStoreForRepo is resolveWidgetTokenStore with an optional
// per-call repo override, authorized identically via partitionPrefixForCall.
func (s *Server) resolveWidgetTokenStoreForRepo(ctx context.Context, argRepo string) (store.WidgetTokenStore, func(), string, error) {
	prefix, err := partitionPrefixForCall(ctx, argRepo)
	if err != nil {
		return nil, nil, "", err
	}
	if prefix == "" {
		return nil, nil, "", fmt.Errorf("widget tokens require a repo scope; set the %s header or a default_repo claim", auth.HeaderTargetRepo)
	}
	ts, closer, err := store.OpenWidgetTokenStoreForTenant(context.Background(), prefix)
	if err != nil {
		return nil, nil, "", err
	}
	repo := repoFromCtx(ctx)
	if strings.TrimSpace(argRepo) != "" {
		if norm, nerr := auth.NormalizeRepo(argRepo); nerr == nil {
			repo = norm
		}
	}
	return ts, func() { _ = closer() }, repo, nil
}

// isAuthorizedForRepo reports whether the request's tenant grants the given
// (normalized) repo — the repo-owner check for revocation.
func isAuthorizedForRepo(ctx context.Context, repo string) bool {
	t, ok := tenantFromContext(ctx)
	if !ok || t == nil {
		// Local CLI path: single local scope, treat as owner.
		return true
	}
	return tenantAuthorizes(t, repo)
}
