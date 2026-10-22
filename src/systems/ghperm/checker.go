package ghperm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// DefaultTTL is the default cache TTL for DENIAL decisions if none is
// specified. Denials are safe to cache longer (they only slow re-granting).
const DefaultTTL = 5 * time.Minute

// DefaultGrantTTL is the default cache TTL for WRITE-capable (grant) decisions.
// It is deliberately short so a GitHub access revocation takes effect quickly:
// a grant kept for the full DefaultTTL would keep authorizing writes for that
// long after the forge revoked access (RFC M3). Bounds the revocation window.
const DefaultGrantTTL = 30 * time.Second

// PermissionChecker answers "can this user write to this repo?" using GitHub
// as the source of truth, with caching to avoid per-request API latency.
type PermissionChecker struct {
	client   GitHubClient
	cache    PermissionCache
	ttl      time.Duration // denial TTL
	grantTTL time.Duration // write-grant TTL (short, bounds revocation window)
	now      func() time.Time
}

// CheckerConfig configures a PermissionChecker.
type CheckerConfig struct {
	Client   GitHubClient
	Cache    PermissionCache
	TTL      time.Duration    // denial TTL (default: 5 minutes)
	GrantTTL time.Duration    // write-grant TTL (default: 30 seconds)
	Now      func() time.Time // default: time.Now
}

// NewChecker creates a PermissionChecker.
func NewChecker(cfg CheckerConfig) (*PermissionChecker, error) {
	if cfg.Client == nil {
		return nil, errors.New("ghperm: client is required")
	}
	if cfg.Cache == nil {
		return nil, errors.New("ghperm: cache is required")
	}
	ttl := cfg.TTL
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	grantTTL := cfg.GrantTTL
	if grantTTL <= 0 {
		grantTTL = DefaultGrantTTL
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &PermissionChecker{
		client:   cfg.Client,
		cache:    cfg.Cache,
		ttl:      ttl,
		grantTTL: grantTTL,
		now:      now,
	}, nil
}

// CanWrite checks if the GitHub user can write to the repo.
// It checks the cache first, then GitHub API on cache miss.
// Returns (true, nil) if authorized, (false, nil) if denied,
// (false, error) on failure (caller MUST treat error as deny).
func (c *PermissionChecker) CanWrite(ctx context.Context, githubUser, githubToken, repo string) (bool, error) {
	perm, err := c.GetPermission(ctx, githubUser, githubToken, repo)
	if err != nil {
		return false, err // fail closed: error = deny
	}
	return perm.CanWrite(), nil
}

// GetPermission returns the user's permission level (cached or fresh).
// Useful when the caller needs the specific level, not just write access.
func (c *PermissionChecker) GetPermission(ctx context.Context, githubUser, githubToken, repo string) (Permission, error) {
	// Validate inputs
	if strings.TrimSpace(githubUser) == "" {
		return PermissionNone, errors.New("ghperm: empty github user")
	}
	if strings.TrimSpace(githubToken) == "" {
		return PermissionNone, errors.New("ghperm: empty github token")
	}

	// Normalize repo
	normalized, err := normalizeRepo(repo)
	if err != nil {
		return PermissionNone, fmt.Errorf("ghperm: invalid repo: %w", err)
	}

	// The token fingerprint is part of the cache identity so a different-scope
	// token for the same login cannot reuse this token's decision (RFC M3).
	tokenFP := TokenFingerprint(githubToken)

	// Check cache
	if entry := c.cache.Get(ctx, githubUser, tokenFP, normalized); entry != nil {
		return entry.Permission, nil
	}

	// Cache miss: call GitHub API
	owner, repoName, _ := splitRepo(normalized) // already validated by normalizeRepo

	perm, err := c.client.GetRepoPermission(ctx, githubToken, owner, repoName)
	if err != nil {
		// Do NOT cache errors - fail closed, retry on next request
		return PermissionNone, fmt.Errorf("ghperm: github api: %w", err)
	}

	// Cache result. Write-capable GRANTS get the short grantTTL so a revocation
	// takes effect quickly; DENIALS (and read-only) get the longer ttl (they are
	// cached to prevent enumeration and are safe to hold — they only slow a
	// later re-grant) (RFC M3).
	ttl := c.ttl
	if perm.CanWrite() {
		ttl = c.grantTTL
	}
	c.cache.Set(ctx, githubUser, tokenFP, normalized, perm, ttl)

	return perm, nil
}

// InvalidatePermission removes any cached decision for a user+token+repo. It is
// the wired revocation hook (RFC M3): call it when access is known to have
// changed (e.g. a disconnect/webhook) so the next request re-checks GitHub
// instead of waiting out the TTL.
func (c *PermissionChecker) InvalidatePermission(ctx context.Context, githubUser, githubToken, repo string) {
	normalized, err := normalizeRepo(repo)
	if err != nil {
		return
	}
	c.cache.Delete(ctx, githubUser, TokenFingerprint(githubToken), normalized)
}

// normalizeRepo validates and normalizes a repo string.
// Returns "owner/repo" with whitespace trimmed.
func normalizeRepo(repo string) (string, error) {
	repo = strings.TrimSpace(repo)
	if repo == "" {
		return "", errors.New("empty repo")
	}
	parts := strings.SplitN(repo, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", errors.New("repo must be in owner/repo format")
	}
	return strings.TrimSpace(parts[0]) + "/" + strings.TrimSpace(parts[1]), nil
}

// splitRepo splits a normalized repo into owner and repo name.
// Assumes repo has already been validated by normalizeRepo.
func splitRepo(repo string) (owner, repoName string, ok bool) {
	parts := strings.SplitN(repo, "/", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}
