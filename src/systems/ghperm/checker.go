package ghperm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// DefaultTTL is the default cache TTL if none is specified.
const DefaultTTL = 5 * time.Minute

// PermissionChecker answers "can this user write to this repo?" using GitHub
// as the source of truth, with caching to avoid per-request API latency.
type PermissionChecker struct {
	client GitHubClient
	cache  PermissionCache
	ttl    time.Duration
	now    func() time.Time
}

// CheckerConfig configures a PermissionChecker.
type CheckerConfig struct {
	Client GitHubClient
	Cache  PermissionCache
	TTL    time.Duration    // default: 5 minutes
	Now    func() time.Time // default: time.Now
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
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &PermissionChecker{
		client: cfg.Client,
		cache:  cfg.Cache,
		ttl:    ttl,
		now:    now,
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

	// Check cache
	if entry := c.cache.Get(ctx, githubUser, normalized); entry != nil {
		return entry.Permission, nil
	}

	// Cache miss: call GitHub API
	owner, repoName, _ := splitRepo(normalized) // already validated by normalizeRepo

	perm, err := c.client.GetRepoPermission(ctx, githubToken, owner, repoName)
	if err != nil {
		// Do NOT cache errors - fail closed, retry on next request
		return PermissionNone, fmt.Errorf("ghperm: github api: %w", err)
	}

	// Cache result (including denials to prevent enumeration)
	c.cache.Set(ctx, githubUser, normalized, perm, c.ttl)

	return perm, nil
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
