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
	err        error
	calls      []string // records "owner/repo" for each call
}

func (m *mockGitHubClient) GetRepoPermission(ctx context.Context, userToken, owner, repo string) (Permission, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, owner+"/"+repo)
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
	cache.Set(ctx, "octocat", "acme/frontend", PermissionWrite, 5*time.Minute)

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
	entry := cache.Get(ctx, "octocat", "acme/frontend")
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
	entry := cache.Get(ctx, "octocat", "acme/frontend")
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
	entry := cache.Get(ctx, "octocat", "acme/frontend")
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
	entry := cache.Get(ctx, "octocat", "acme/frontend")
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
