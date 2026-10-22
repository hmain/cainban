// Package ghperm provides GitHub-native permission checking with caching.
// It replaces the grants table by checking permissions via the GitHub API
// and caching results for low-latency authorization decisions.
package ghperm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"
)

// Permission represents a GitHub permission level for a user on a repo.
type Permission string

const (
	PermissionNone     Permission = "none"
	PermissionRead     Permission = "read"
	PermissionTriage   Permission = "triage"
	PermissionWrite    Permission = "write"
	PermissionMaintain Permission = "maintain"
	PermissionAdmin    Permission = "admin"
)

// CanWrite reports whether this permission grants write access.
func (p Permission) CanWrite() bool {
	switch p {
	case PermissionWrite, PermissionMaintain, PermissionAdmin:
		return true
	default:
		return false
	}
}

// CacheEntry holds a cached permission with expiry.
type CacheEntry struct {
	Permission Permission
	ExpiresAt  time.Time
}

// PermissionCache stores GitHub permission lookups with TTL.
// Implementations must be safe for concurrent use.
//
// tokenFP is a fingerprint of the GitHub token that produced the decision (see
// TokenFingerprint). It is part of the cache identity so two tokens for the
// same login that differ in SCOPE (e.g. a broad PAT vs a fine-grained token
// with no access to this repo) never share a cached decision (RFC M3).
type PermissionCache interface {
	// Get returns the cached permission for user+repo+tokenFP, or nil if not cached/expired.
	Get(ctx context.Context, githubUser, tokenFP, repo string) *CacheEntry

	// Set stores a permission with the given TTL.
	Set(ctx context.Context, githubUser, tokenFP, repo string, perm Permission, ttl time.Duration)

	// Delete removes a cache entry (used on explicit revocation signal).
	Delete(ctx context.Context, githubUser, tokenFP, repo string)
}

// TokenFingerprint returns a short, non-reversible fingerprint of a GitHub
// token for use in cache keys. It is a SHA-256 hash truncated to 128 bits and
// hex-encoded — enough to distinguish tokens without ever storing or logging
// token material. The empty token maps to a fixed empty-marker so a missing
// token cannot collide with a real one.
func TokenFingerprint(token string) string {
	if token == "" {
		return "notoken"
	}
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:16])
}

// MemoryCache is a thread-safe in-memory PermissionCache.
// Suitable for Lambda (per-invocation) and single-instance servers.
// For multi-instance deployments, use DynamoDBCache or RedisCache.
type MemoryCache struct {
	mu      sync.RWMutex
	entries map[string]CacheEntry
	now     func() time.Time
}

// NewMemoryCache creates a MemoryCache with an injectable clock.
// If now is nil, time.Now is used.
func NewMemoryCache(now func() time.Time) *MemoryCache {
	if now == nil {
		now = time.Now
	}
	return &MemoryCache{
		entries: make(map[string]CacheEntry),
		now:     now,
	}
}

// Get returns the cached permission for user+tokenFP+repo, or nil if not cached or expired.
func (m *MemoryCache) Get(ctx context.Context, githubUser, tokenFP, repo string) *CacheEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	key := cacheKey(githubUser, tokenFP, repo)
	entry, ok := m.entries[key]
	if !ok {
		return nil
	}
	if m.now().After(entry.ExpiresAt) {
		return nil // expired
	}
	return &entry
}

// Set stores a permission with the given TTL.
func (m *MemoryCache) Set(ctx context.Context, githubUser, tokenFP, repo string, perm Permission, ttl time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := cacheKey(githubUser, tokenFP, repo)
	m.entries[key] = CacheEntry{
		Permission: perm,
		ExpiresAt:  m.now().Add(ttl),
	}
}

// Delete removes a cache entry.
func (m *MemoryCache) Delete(ctx context.Context, githubUser, tokenFP, repo string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.entries, cacheKey(githubUser, tokenFP, repo))
}

// cacheKey builds the cache key for a user+tokenFP+repo triple. The token
// fingerprint is included so two different-scope tokens for the same login do
// not share a decision (RFC M3).
func cacheKey(githubUser, tokenFP, repo string) string {
	return "ghperm:" + githubUser + ":" + tokenFP + ":" + repo
}
