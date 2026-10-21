package ghperm

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// mockGitHubClient is a test double for GitHubClient
type mockGitHubClient struct {
	mu         sync.Mutex
	permission Permission
	byToken    map[string]Permission // optional per-token responses
	err        error
	calls      []string // records "owner/repo" for each call
}

// byToken, when non-nil, maps a user token to the permission the mock returns,
// so a test can give two different-scope tokens different answers.
func (m *mockGitHubClient) GetRepoPermission(ctx context.Context, userToken, owner, repo string) (Permission, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, owner+"/"+repo)
	if m.byToken != nil {
		if p, ok := m.byToken[userToken]; ok {
			return p, m.err
		}
	}
	return m.permission, m.err
}

func (m *mockGitHubClient) GetCollaboratorPermission(ctx context.Context, appToken, owner, repo, username string) (Permission, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, username+"@"+owner+"/"+repo)
	return m.permission, m.err
}

func (m *mockGitHubClient) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.calls)
}

func TestChecker_CacheHit(t *testing.T) {
	t.Parallel()
	now := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	cache := NewMemoryCache(func() time.Time { return now })
	ctx := context.Background()

	// Pre-populate cache
	cache.Set(ctx, "octocat", TokenFingerprint("fake-token"), "acme/frontend", PermissionWrite, 5*time.Minute)

	mockClient := &mockGitHubClient{permission: PermissionNone} // would deny if called
	checker, err := NewChecker(CheckerConfig{
		Client: mockClient,
		Cache:  cache,
		TTL:    5 * time.Minute,
		Now:    func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewChecker: %v", err)
	}

	// Should use cache, not call GitHub
	canWrite, err := checker.CanWrite(ctx, "octocat", "fake-token", "acme/frontend")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !canWrite {
		t.Error("expected canWrite=true from cache")
	}
	if mockClient.callCount() != 0 {
		t.Errorf("expected 0 GitHub calls, got %d", mockClient.callCount())
	}
}

func TestChecker_CacheMiss_GitHubGrantsWrite(t *testing.T) {
	t.Parallel()
	now := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	cache := NewMemoryCache(func() time.Time { return now })
	ctx := context.Background()

	mockClient := &mockGitHubClient{permission: PermissionWrite}
	checker, err := NewChecker(CheckerConfig{
		Client: mockClient,
		Cache:  cache,
		TTL:    5 * time.Minute,
		Now:    func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewChecker: %v", err)
	}

	// Cache is empty, should call GitHub
	canWrite, err := checker.CanWrite(ctx, "octocat", "fake-token", "acme/frontend")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !canWrite {
		t.Error("expected canWrite=true from GitHub")
	}
	if mockClient.callCount() != 1 {
		t.Errorf("expected 1 GitHub call, got %d", mockClient.callCount())
	}

	// Should be cached now
	entry := cache.Get(ctx, "octocat", TokenFingerprint("fake-token"), "acme/frontend")
	if entry == nil {
		t.Fatal("expected cached entry")
	}
	if entry.Permission != PermissionWrite {
		t.Errorf("expected cached PermissionWrite, got %s", entry.Permission)
	}
}

func TestChecker_CacheMiss_GitHubDenies(t *testing.T) {
	t.Parallel()
	now := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	cache := NewMemoryCache(func() time.Time { return now })
	ctx := context.Background()

	mockClient := &mockGitHubClient{permission: PermissionNone}
	checker, err := NewChecker(CheckerConfig{
		Client: mockClient,
		Cache:  cache,
		TTL:    5 * time.Minute,
		Now:    func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewChecker: %v", err)
	}

	canWrite, err := checker.CanWrite(ctx, "octocat", "fake-token", "acme/frontend")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if canWrite {
		t.Error("expected canWrite=false for PermissionNone")
	}

	// Denial should also be cached (prevents enumeration)
	entry := cache.Get(ctx, "octocat", TokenFingerprint("fake-token"), "acme/frontend")
	if entry == nil {
		t.Fatal("expected denial to be cached")
	}
	if entry.Permission != PermissionNone {
		t.Errorf("expected cached PermissionNone, got %s", entry.Permission)
	}
}

func TestChecker_GitHubError_FailClosed(t *testing.T) {
	t.Parallel()
	cache := NewMemoryCache(nil)
	ctx := context.Background()

	mockClient := &mockGitHubClient{err: errors.New("rate limited")}
	checker, err := NewChecker(CheckerConfig{
		Client: mockClient,
		Cache:  cache,
		TTL:    5 * time.Minute,
	})
	if err != nil {
		t.Fatalf("NewChecker: %v", err)
	}

	canWrite, err := checker.CanWrite(ctx, "octocat", "fake-token", "acme/frontend")
	if err == nil {
		t.Fatal("expected error for GitHub failure")
	}
	if canWrite {
		t.Error("expected canWrite=false on error (fail closed)")
	}

	// Error should NOT be cached
	entry := cache.Get(ctx, "octocat", TokenFingerprint("fake-token"), "acme/frontend")
	if entry != nil {
		t.Error("errors should not be cached")
	}
}

func TestChecker_InvalidRepo_Error(t *testing.T) {
	t.Parallel()
	cache := NewMemoryCache(nil)
	ctx := context.Background()

	mockClient := &mockGitHubClient{permission: PermissionAdmin}
	checker, err := NewChecker(CheckerConfig{
		Client: mockClient,
		Cache:  cache,
		TTL:    5 * time.Minute,
	})
	if err != nil {
		t.Fatalf("NewChecker: %v", err)
	}

	// Invalid repo format
	_, err = checker.CanWrite(ctx, "octocat", "fake-token", "invalid-no-slash")
	if err == nil {
		t.Fatal("expected error for invalid repo")
	}

	// GitHub should not be called for invalid input
	if mockClient.callCount() != 0 {
		t.Errorf("expected 0 GitHub calls for invalid repo, got %d", mockClient.callCount())
	}
}

func TestChecker_NormalizesRepo(t *testing.T) {
	t.Parallel()
	cache := NewMemoryCache(nil)
	ctx := context.Background()

	mockClient := &mockGitHubClient{permission: PermissionWrite}
	checker, err := NewChecker(CheckerConfig{
		Client: mockClient,
		Cache:  cache,
		TTL:    5 * time.Minute,
	})
	if err != nil {
		t.Fatalf("NewChecker: %v", err)
	}

	// Repo with whitespace
	_, err = checker.CanWrite(ctx, "octocat", "fake-token", "  acme/frontend  ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify normalized in cache
	entry := cache.Get(ctx, "octocat", TokenFingerprint("fake-token"), "acme/frontend")
	if entry == nil {
		t.Error("expected entry under normalized key")
	}
}

func TestChecker_GetPermission_AllLevels(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		ghPerm   Permission
		canWrite bool
	}{
		{"admin", PermissionAdmin, true},
		{"maintain", PermissionMaintain, true},
		{"write", PermissionWrite, true},
		{"triage", PermissionTriage, false},
		{"read", PermissionRead, false},
		{"none", PermissionNone, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cache := NewMemoryCache(nil)
			ctx := context.Background()

			mockClient := &mockGitHubClient{permission: tc.ghPerm}
			checker, _ := NewChecker(CheckerConfig{
				Client: mockClient,
				Cache:  cache,
				TTL:    5 * time.Minute,
			})

			perm, err := checker.GetPermission(ctx, "user", "token", "owner/repo")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if perm != tc.ghPerm {
				t.Errorf("expected %s, got %s", tc.ghPerm, perm)
			}

			canWrite, err := checker.CanWrite(ctx, "user", "token", "owner/repo")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if canWrite != tc.canWrite {
				t.Errorf("expected canWrite=%v, got %v", tc.canWrite, canWrite)
			}
		})
	}
}

func TestChecker_CacheExpiry_RefetchesFromGitHub(t *testing.T) {
	t.Parallel()
	now := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	mu := sync.Mutex{}
	clock := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	}
	cache := NewMemoryCache(clock)
	ctx := context.Background()

	mockClient := &mockGitHubClient{permission: PermissionWrite}
	checker, _ := NewChecker(CheckerConfig{
		Client: mockClient,
		Cache:  cache,
		TTL:    5 * time.Minute,
		Now:    clock,
	})

	// First call populates cache
	_, _ = checker.CanWrite(ctx, "user", "token", "owner/repo")
	if mockClient.callCount() != 1 {
		t.Errorf("expected 1 call, got %d", mockClient.callCount())
	}

	// Second call uses cache
	_, _ = checker.CanWrite(ctx, "user", "token", "owner/repo")
	if mockClient.callCount() != 1 {
		t.Errorf("expected still 1 call (cached), got %d", mockClient.callCount())
	}

	// Advance time past TTL
	mu.Lock()
	now = now.Add(6 * time.Minute)
	mu.Unlock()

	// Third call should refetch
	_, _ = checker.CanWrite(ctx, "user", "token", "owner/repo")
	if mockClient.callCount() != 2 {
		t.Errorf("expected 2 calls (cache expired), got %d", mockClient.callCount())
	}
}

func TestChecker_DifferentUsers_SeparateCacheEntries(t *testing.T) {
	t.Parallel()
	cache := NewMemoryCache(nil)
	ctx := context.Background()

	callCount := 0
	mockClient := &mockGitHubClient{}

	// Alternate between write and read permissions
	originalGetPerm := mockClient.permission
	mockClient.permission = PermissionWrite

	checker, _ := NewChecker(CheckerConfig{
		Client: mockClient,
		Cache:  cache,
		TTL:    5 * time.Minute,
	})

	// Alice checks
	_, _ = checker.CanWrite(ctx, "alice", "token-a", "owner/repo")
	callCount++

	// Change mock response
	mockClient.mu.Lock()
	mockClient.permission = PermissionRead
	mockClient.mu.Unlock()

	// Bob checks (different user, should call GitHub)
	_, _ = checker.CanWrite(ctx, "bob", "token-b", "owner/repo")
	callCount++

	if mockClient.callCount() != callCount {
		t.Errorf("expected %d calls (separate users), got %d", callCount, mockClient.callCount())
	}

	// Restore for clarity
	mockClient.permission = originalGetPerm
}

func TestNewChecker_Validation(t *testing.T) {
	t.Parallel()

	// Nil client
	_, err := NewChecker(CheckerConfig{
		Client: nil,
		Cache:  NewMemoryCache(nil),
		TTL:    5 * time.Minute,
	})
	if err == nil {
		t.Error("expected error for nil client")
	}

	// Nil cache
	_, err = NewChecker(CheckerConfig{
		Client: &mockGitHubClient{},
		Cache:  nil,
		TTL:    5 * time.Minute,
	})
	if err == nil {
		t.Error("expected error for nil cache")
	}

	// Zero TTL should use default
	checker, err := NewChecker(CheckerConfig{
		Client: &mockGitHubClient{},
		Cache:  NewMemoryCache(nil),
		TTL:    0,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if checker.ttl != DefaultTTL {
		t.Errorf("expected default TTL %v, got %v", DefaultTTL, checker.ttl)
	}
}

func TestChecker_EmptyGitHubUser(t *testing.T) {
	t.Parallel()
	cache := NewMemoryCache(nil)
	ctx := context.Background()

	mockClient := &mockGitHubClient{permission: PermissionWrite}
	checker, _ := NewChecker(CheckerConfig{
		Client: mockClient,
		Cache:  cache,
		TTL:    5 * time.Minute,
	})

	_, err := checker.CanWrite(ctx, "", "token", "owner/repo")
	if err == nil {
		t.Error("expected error for empty github user")
	}
}

func TestChecker_EmptyToken(t *testing.T) {
	t.Parallel()
	cache := NewMemoryCache(nil)
	ctx := context.Background()

	mockClient := &mockGitHubClient{permission: PermissionWrite}
	checker, _ := NewChecker(CheckerConfig{
		Client: mockClient,
		Cache:  cache,
		TTL:    5 * time.Minute,
	})

	_, err := checker.CanWrite(ctx, "user", "", "owner/repo")
	if err == nil {
		t.Error("expected error for empty token")
	}
}

// TestChecker_DifferentTokenDoesNotReuseDecision is the RFC M3 guard against the
// token-blind cache key: two tokens for the SAME login that differ in scope must
// NOT share a cached decision. Token A has write; token B (a narrower token with
// no access) must trigger its own GitHub check and get its own answer, not reuse
// A's cached write.
func TestChecker_DifferentTokenDoesNotReuseDecision(t *testing.T) {
	t.Parallel()
	now := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	cache := NewMemoryCache(func() time.Time { return now })
	ctx := context.Background()

	mockClient := &mockGitHubClient{byToken: map[string]Permission{
		"token-A-write": PermissionWrite,
		"token-B-none":  PermissionNone,
	}}
	checker, err := NewChecker(CheckerConfig{Client: mockClient, Cache: cache, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("NewChecker: %v", err)
	}

	// Token A: write, cached.
	okA, err := checker.CanWrite(ctx, "octocat", "token-A-write", "acme/frontend")
	if err != nil || !okA {
		t.Fatalf("token A expected write, got ok=%v err=%v", okA, err)
	}
	// Token B (same user, different scope): MUST NOT reuse A's decision.
	okB, err := checker.CanWrite(ctx, "octocat", "token-B-none", "acme/frontend")
	if err != nil {
		t.Fatalf("token B unexpected error: %v", err)
	}
	if okB {
		t.Fatalf("token B reused token A's cached write — token-blind cache key (RFC M3)")
	}
	// Both tokens caused a GitHub call (no cross-token cache reuse).
	if mockClient.callCount() != 2 {
		t.Errorf("expected 2 GitHub calls (one per distinct token), got %d", mockClient.callCount())
	}
}

// TestChecker_GrantTTLShorterThanDenialTTL is the RFC M3 guard for the
// revocation window: a write GRANT is cached only for the short grantTTL (so a
// revocation takes effect quickly), while a DENIAL is cached for the longer ttl.
func TestChecker_GrantTTLShorterThanDenialTTL(t *testing.T) {
	t.Parallel()
	base := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	clock := base
	cache := NewMemoryCache(func() time.Time { return clock })
	ctx := context.Background()

	mockClient := &mockGitHubClient{permission: PermissionWrite}
	checker, err := NewChecker(CheckerConfig{
		Client:   mockClient,
		Cache:    cache,
		TTL:      5 * time.Minute,  // denial TTL
		GrantTTL: 30 * time.Second, // grant TTL (short)
		Now:      func() time.Time { return clock },
	})
	if err != nil {
		t.Fatalf("NewChecker: %v", err)
	}

	// First check: GitHub grants write, cached under the short grantTTL.
	if ok, _ := checker.CanWrite(ctx, "octocat", "tok", "acme/frontend"); !ok {
		t.Fatal("expected initial write grant")
	}
	if mockClient.callCount() != 1 {
		t.Fatalf("expected 1 GitHub call, got %d", mockClient.callCount())
	}

	// 20s later (< grantTTL): still cached, no new call.
	clock = base.Add(20 * time.Second)
	if ok, _ := checker.CanWrite(ctx, "octocat", "tok", "acme/frontend"); !ok {
		t.Fatal("within grantTTL the grant should still be cached")
	}
	if mockClient.callCount() != 1 {
		t.Errorf("within grantTTL expected no new GitHub call, got %d", mockClient.callCount())
	}

	// 45s later (> grantTTL, < denial TTL): grant expired, GitHub re-checked.
	// Now GitHub denies (access revoked) — the short grant TTL is what let this
	// revocation take effect well before the 5m denial TTL would have.
	clock = base.Add(45 * time.Second)
	mockClient.mu.Lock()
	mockClient.permission = PermissionNone
	mockClient.mu.Unlock()
	if ok, _ := checker.CanWrite(ctx, "octocat", "tok", "acme/frontend"); ok {
		t.Fatal("after grantTTL expiry the revoked access must be re-checked and denied")
	}
	if mockClient.callCount() != 2 {
		t.Errorf("expected a 2nd GitHub call after grantTTL expiry, got %d", mockClient.callCount())
	}
}
