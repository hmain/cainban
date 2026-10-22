package ghperm

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestMemoryCache_GetSet(t *testing.T) {
	t.Parallel()
	now := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	cache := NewMemoryCache(clock)
	ctx := context.Background()

	// Set a permission
	cache.Set(ctx, "octocat", "tok", "acme/frontend", PermissionWrite, 5*time.Minute)

	// Get should return it
	entry := cache.Get(ctx, "octocat", "tok", "acme/frontend")
	if entry == nil {
		t.Fatal("expected entry, got nil")
	}
	if entry.Permission != PermissionWrite {
		t.Errorf("expected PermissionWrite, got %s", entry.Permission)
	}
	expectedExpiry := now.Add(5 * time.Minute)
	if !entry.ExpiresAt.Equal(expectedExpiry) {
		t.Errorf("expected ExpiresAt %v, got %v", expectedExpiry, entry.ExpiresAt)
	}
}

func TestMemoryCache_GetMiss(t *testing.T) {
	t.Parallel()
	cache := NewMemoryCache(nil)
	ctx := context.Background()

	// Get non-existent entry
	entry := cache.Get(ctx, "octocat", "tok", "acme/frontend")
	if entry != nil {
		t.Errorf("expected nil, got %+v", entry)
	}
}

func TestMemoryCache_Expiry(t *testing.T) {
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

	// Set with 5 minute TTL
	cache.Set(ctx, "octocat", "tok", "acme/frontend", PermissionWrite, 5*time.Minute)

	// Still valid after 4 minutes
	mu.Lock()
	now = now.Add(4 * time.Minute)
	mu.Unlock()
	if entry := cache.Get(ctx, "octocat", "tok", "acme/frontend"); entry == nil {
		t.Error("expected entry to still be valid")
	}

	// Expired after 6 minutes
	mu.Lock()
	now = now.Add(2 * time.Minute) // now 6 min from original
	mu.Unlock()
	if entry := cache.Get(ctx, "octocat", "tok", "acme/frontend"); entry != nil {
		t.Errorf("expected nil (expired), got %+v", entry)
	}
}

func TestMemoryCache_Delete(t *testing.T) {
	t.Parallel()
	cache := NewMemoryCache(nil)
	ctx := context.Background()

	// Set then delete
	cache.Set(ctx, "octocat", "tok", "acme/frontend", PermissionWrite, 5*time.Minute)
	cache.Delete(ctx, "octocat", "tok", "acme/frontend")

	// Should be gone
	if entry := cache.Get(ctx, "octocat", "tok", "acme/frontend"); entry != nil {
		t.Errorf("expected nil after delete, got %+v", entry)
	}
}

func TestMemoryCache_DeleteNonExistent(t *testing.T) {
	t.Parallel()
	cache := NewMemoryCache(nil)
	ctx := context.Background()

	// Delete non-existent should not panic
	cache.Delete(ctx, "octocat", "tok", "acme/frontend")
}

func TestMemoryCache_Overwrite(t *testing.T) {
	t.Parallel()
	cache := NewMemoryCache(nil)
	ctx := context.Background()

	// Set then overwrite
	cache.Set(ctx, "octocat", "tok", "acme/frontend", PermissionRead, 5*time.Minute)
	cache.Set(ctx, "octocat", "tok", "acme/frontend", PermissionAdmin, 10*time.Minute)

	entry := cache.Get(ctx, "octocat", "tok", "acme/frontend")
	if entry == nil {
		t.Fatal("expected entry")
	}
	if entry.Permission != PermissionAdmin {
		t.Errorf("expected PermissionAdmin, got %s", entry.Permission)
	}
}

func TestMemoryCache_ConcurrentAccess(t *testing.T) {
	t.Parallel()
	cache := NewMemoryCache(nil)
	ctx := context.Background()

	// Run concurrent reads and writes
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			cache.Set(ctx, "user1", "tok", "repo1", PermissionWrite, time.Minute)
		}()
		go func() {
			defer wg.Done()
			_ = cache.Get(ctx, "user1", "tok", "repo1")
		}()
	}
	wg.Wait()
	// Test passes if no race detected (run with -race)
}

func TestMemoryCache_DifferentUsers(t *testing.T) {
	t.Parallel()
	cache := NewMemoryCache(nil)
	ctx := context.Background()

	// Same repo, different users
	cache.Set(ctx, "alice", "tok", "acme/frontend", PermissionWrite, 5*time.Minute)
	cache.Set(ctx, "bob", "tok", "acme/frontend", PermissionRead, 5*time.Minute)

	aliceEntry := cache.Get(ctx, "alice", "tok", "acme/frontend")
	bobEntry := cache.Get(ctx, "bob", "tok", "acme/frontend")

	if aliceEntry == nil || aliceEntry.Permission != PermissionWrite {
		t.Errorf("expected alice to have write, got %v", aliceEntry)
	}
	if bobEntry == nil || bobEntry.Permission != PermissionRead {
		t.Errorf("expected bob to have read, got %v", bobEntry)
	}
}

func TestMemoryCache_DifferentRepos(t *testing.T) {
	t.Parallel()
	cache := NewMemoryCache(nil)
	ctx := context.Background()

	// Same user, different repos
	cache.Set(ctx, "octocat", "tok", "acme/frontend", PermissionWrite, 5*time.Minute)
	cache.Set(ctx, "octocat", "tok", "acme/backend", PermissionAdmin, 5*time.Minute)

	frontendEntry := cache.Get(ctx, "octocat", "tok", "acme/frontend")
	backendEntry := cache.Get(ctx, "octocat", "tok", "acme/backend")

	if frontendEntry == nil || frontendEntry.Permission != PermissionWrite {
		t.Errorf("expected frontend to have write, got %v", frontendEntry)
	}
	if backendEntry == nil || backendEntry.Permission != PermissionAdmin {
		t.Errorf("expected backend to have admin, got %v", backendEntry)
	}
}

func TestCacheKey_Format(t *testing.T) {
	t.Parallel()
	tests := []struct {
		user    string
		tokenFP string
		repo    string
		want    string
	}{
		{"octocat", "fp1", "acme/frontend", "ghperm:octocat:fp1:acme/frontend"},
		{"alice", "fp2", "org/repo-name", "ghperm:alice:fp2:org/repo-name"},
		{"user-123", "fp3", "OWNER/REPO", "ghperm:user-123:fp3:OWNER/REPO"},
	}

	for _, tc := range tests {
		got := cacheKey(tc.user, tc.tokenFP, tc.repo)
		if got != tc.want {
			t.Errorf("cacheKey(%q, %q, %q) = %q, want %q", tc.user, tc.tokenFP, tc.repo, got, tc.want)
		}
	}
}

func TestPermission_CanWrite(t *testing.T) {
	t.Parallel()
	tests := []struct {
		perm Permission
		want bool
	}{
		{PermissionNone, false},
		{PermissionRead, false},
		{PermissionTriage, false},
		{PermissionWrite, true},
		{PermissionMaintain, true},
		{PermissionAdmin, true},
	}

	for _, tc := range tests {
		got := tc.perm.CanWrite()
		if got != tc.want {
			t.Errorf("Permission(%q).CanWrite() = %v, want %v", tc.perm, got, tc.want)
		}
	}
}

func TestPermission_String(t *testing.T) {
	t.Parallel()
	// Verify Permission constants are valid strings
	perms := []Permission{
		PermissionNone,
		PermissionRead,
		PermissionTriage,
		PermissionWrite,
		PermissionMaintain,
		PermissionAdmin,
	}

	for _, p := range perms {
		if string(p) == "" {
			t.Errorf("Permission constant should not be empty string")
		}
	}
}
