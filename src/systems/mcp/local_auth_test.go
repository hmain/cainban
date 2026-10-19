package mcp

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hmain/cainban/src/systems/auth"
)

// TestLocalIdentityFromEnv verifies that when CAINBAN_REPO is set, the stdio
// server resolves a local tenant scoped to that repo.
func TestLocalIdentityFromEnv(t *testing.T) {
	// Set up env
	os.Setenv("CAINBAN_REPO", "hmain/cainban")
	os.Setenv("CAINBAN_ACTOR", "test@example.com")
	defer func() {
		os.Unsetenv("CAINBAN_REPO")
		os.Unsetenv("CAINBAN_ACTOR")
	}()

	tenant, err := localTenantFromEnv()
	if err != nil {
		t.Fatalf("localTenantFromEnv() error: %v", err)
	}
	if tenant == nil {
		t.Fatal("localTenantFromEnv() returned nil tenant")
	}

	if tenant.Repo != "hmain/cainban" {
		t.Errorf("tenant.Repo = %q, want %q", tenant.Repo, "hmain/cainban")
	}
	if tenant.Actor != "test@example.com" {
		t.Errorf("tenant.Actor = %q, want %q", tenant.Actor, "test@example.com")
	}
	if tenant.PartitionPrefix != auth.PartitionPrefixFor("hmain/cainban") {
		t.Errorf("tenant.PartitionPrefix = %q, want %q", tenant.PartitionPrefix, auth.PartitionPrefixFor("hmain/cainban"))
	}
	if len(tenant.AuthorizedRepos) != 1 || tenant.AuthorizedRepos[0] != "hmain/cainban" {
		t.Errorf("tenant.AuthorizedRepos = %v, want [hmain/cainban]", tenant.AuthorizedRepos)
	}
	if tenant.Unscoped {
		t.Error("tenant.Unscoped = true, want false")
	}
}

// TestLocalIdentityActorFallback verifies that when CAINBAN_ACTOR is not set,
// the actor falls back to a sensible default.
func TestLocalIdentityActorFallback(t *testing.T) {
	os.Setenv("CAINBAN_REPO", "owner/repo")
	os.Unsetenv("CAINBAN_ACTOR")
	defer os.Unsetenv("CAINBAN_REPO")

	tenant, err := localTenantFromEnv()
	if err != nil {
		t.Fatalf("localTenantFromEnv() error: %v", err)
	}

	// Actor should fall back to "local" or similar when not set
	if tenant.Actor == "" {
		t.Error("tenant.Actor should not be empty when CAINBAN_ACTOR is unset")
	}
}

// TestLocalIdentityMissingRepo verifies that when no CAINBAN_REPO is set,
// localTenantFromEnv returns nil (single-tenant mode, existing behavior).
func TestLocalIdentityMissingRepo(t *testing.T) {
	os.Unsetenv("CAINBAN_REPO")
	os.Unsetenv("CAINBAN_ACTOR")
	os.Unsetenv("CAINBAN_TOKEN")

	tenant, err := localTenantFromEnv()
	if err != nil {
		t.Fatalf("localTenantFromEnv() error: %v", err)
	}
	if tenant != nil {
		t.Errorf("localTenantFromEnv() = %+v, want nil (single-tenant mode)", tenant)
	}
}

// TestLocalIdentityInvalidRepo verifies that an invalid repo format is rejected.
func TestLocalIdentityInvalidRepo(t *testing.T) {
	testCases := []string{
		"",
		"noslash",
		"too/many/slashes",
		"/leading",
		"trailing/",
	}

	for _, repo := range testCases {
		os.Setenv("CAINBAN_REPO", repo)
		tenant, err := localTenantFromEnv()
		os.Unsetenv("CAINBAN_REPO")

		// Empty string should return nil (single-tenant mode), not error
		if repo == "" {
			if err != nil {
				t.Errorf("CAINBAN_REPO=%q: got error %v, want nil", repo, err)
			}
			if tenant != nil {
				t.Errorf("CAINBAN_REPO=%q: got tenant %+v, want nil", repo, tenant)
			}
			continue
		}

		// Invalid non-empty repos should error
		if err == nil {
			t.Errorf("CAINBAN_REPO=%q: expected error, got tenant %+v", repo, tenant)
		}
	}
}

// TestLocalTenantContextRoundTrip verifies that a local tenant can be stored in
// and retrieved from context.
func TestLocalTenantContextRoundTrip(t *testing.T) {
	original := &auth.Tenant{
		Repo:            "hmain/cainban",
		PartitionPrefix: auth.PartitionPrefixFor("hmain/cainban"),
		Subject:         "local",
		Actor:           "test@example.com",
		AuthorizedRepos: []string{"hmain/cainban"},
		Unscoped:        false,
	}

	ctx := withTenant(context.Background(), original)
	retrieved, ok := tenantFromContext(ctx)

	if !ok {
		t.Fatal("tenantFromContext returned ok=false")
	}
	if retrieved.Repo != original.Repo {
		t.Errorf("retrieved.Repo = %q, want %q", retrieved.Repo, original.Repo)
	}
	if retrieved.Actor != original.Actor {
		t.Errorf("retrieved.Actor = %q, want %q", retrieved.Actor, original.Actor)
	}
}

// TestLocalTenantIntegration is an integration test that verifies the full
// stdio path: env → tenant → tool call with correct repo scoping.
func TestLocalTenantIntegration(t *testing.T) {
	os.Setenv("CAINBAN_REPO", "hmain/cainban")
	os.Setenv("CAINBAN_ACTOR", "integration-test")
	os.Setenv("CAINBAN_BACKEND", "sqlite") // Force SQLite for test
	defer func() {
		os.Unsetenv("CAINBAN_REPO")
		os.Unsetenv("CAINBAN_ACTOR")
		os.Unsetenv("CAINBAN_BACKEND")
	}()

	// Create server
	server := NewStateless()

	// Get local tenant from env
	tenant, err := localTenantFromEnv()
	if err != nil {
		t.Fatalf("localTenantFromEnv() error: %v", err)
	}
	if tenant == nil {
		t.Fatal("expected non-nil tenant")
	}

	// Create context with tenant
	ctx := withTenant(context.Background(), tenant)

	// Resolve task system — should succeed with the local tenant
	ts, closer, err := server.resolveTaskSystemForRepo(ctx, "", "")
	if err != nil {
		t.Fatalf("resolveTaskSystemForRepo() error: %v", err)
	}
	defer closer()

	// The task system should be usable
	if ts == nil {
		t.Fatal("resolveTaskSystemForRepo() returned nil task system")
	}

	// For SQLite, the partition prefix is ignored, so we just verify it opens
	// without error. The real test is that we got here without auth errors.
}


// =============================================================================
// Phase 2: CAINBAN_TOKEN tests
// =============================================================================

// TestLocalTokenValidation verifies that a valid CAINBAN_TOKEN sets the tenant
// with repos from the token's claims.
func TestLocalTokenValidation(t *testing.T) {
	// Generate a test signing key
	key := []byte("test-secret-key-32-bytes-long!!!")

	// Create a valid token with repos claim
	token, err := MintLocalToken(key, "test-user@example.com", []string{"hmain/cainban", "hmain/other"}, time.Hour)
	if err != nil {
		t.Fatalf("MintLocalToken() error: %v", err)
	}

	// Set up env
	os.Setenv("CAINBAN_TOKEN", token)
	os.Setenv("CAINBAN_TOKEN_KEY", string(key))
	defer func() {
		os.Unsetenv("CAINBAN_TOKEN")
		os.Unsetenv("CAINBAN_TOKEN_KEY")
	}()

	tenant, err := localTenantFromEnv()
	if err != nil {
		t.Fatalf("localTenantFromEnv() error: %v", err)
	}
	if tenant == nil {
		t.Fatal("localTenantFromEnv() returned nil tenant")
	}

	// Subject should be from token
	if tenant.Subject != "test-user@example.com" {
		t.Errorf("tenant.Subject = %q, want %q", tenant.Subject, "test-user@example.com")
	}

	// Actor should also be from token subject
	if tenant.Actor != "test-user@example.com" {
		t.Errorf("tenant.Actor = %q, want %q", tenant.Actor, "test-user@example.com")
	}

	// AuthorizedRepos should have both repos from token
	if len(tenant.AuthorizedRepos) != 2 {
		t.Fatalf("len(tenant.AuthorizedRepos) = %d, want 2", len(tenant.AuthorizedRepos))
	}
	wantRepos := map[string]bool{"hmain/cainban": true, "hmain/other": true}
	for _, repo := range tenant.AuthorizedRepos {
		if !wantRepos[repo] {
			t.Errorf("unexpected repo %q in AuthorizedRepos", repo)
		}
	}

	// First repo should be set as the primary Repo
	if tenant.Repo != "hmain/cainban" {
		t.Errorf("tenant.Repo = %q, want %q", tenant.Repo, "hmain/cainban")
	}
}

// TestLocalTokenExpired verifies that an expired token is rejected.
func TestLocalTokenExpired(t *testing.T) {
	key := []byte("test-secret-key-32-bytes-long!!!")

	// Create an expired token (negative duration)
	token, err := MintLocalToken(key, "test-user", []string{"hmain/cainban"}, -time.Hour)
	if err != nil {
		t.Fatalf("MintLocalToken() error: %v", err)
	}

	os.Setenv("CAINBAN_TOKEN", token)
	os.Setenv("CAINBAN_TOKEN_KEY", string(key))
	defer func() {
		os.Unsetenv("CAINBAN_TOKEN")
		os.Unsetenv("CAINBAN_TOKEN_KEY")
	}()

	_, err = localTenantFromEnv()
	if err == nil {
		t.Fatal("expected error for expired token, got nil")
	}
	if !strings.Contains(err.Error(), "expired") {
		t.Errorf("error should mention 'expired', got: %v", err)
	}
}

// TestLocalTokenBadSignature verifies that a token with wrong signature is rejected.
func TestLocalTokenBadSignature(t *testing.T) {
	correctKey := []byte("correct-key-32-bytes-long!!!!!!!")
	wrongKey := []byte("wrong-key-32-bytes-long-here!!!!")

	// Create token with one key
	token, err := MintLocalToken(correctKey, "test-user", []string{"hmain/cainban"}, time.Hour)
	if err != nil {
		t.Fatalf("MintLocalToken() error: %v", err)
	}

	// Try to validate with different key
	os.Setenv("CAINBAN_TOKEN", token)
	os.Setenv("CAINBAN_TOKEN_KEY", string(wrongKey))
	defer func() {
		os.Unsetenv("CAINBAN_TOKEN")
		os.Unsetenv("CAINBAN_TOKEN_KEY")
	}()

	_, err = localTenantFromEnv()
	if err == nil {
		t.Fatal("expected error for bad signature, got nil")
	}
	if !strings.Contains(err.Error(), "signature") && !strings.Contains(err.Error(), "invalid") {
		t.Errorf("error should mention signature issue, got: %v", err)
	}
}

// TestLocalTokenMissingRepos verifies that a token without repos claim is rejected.
func TestLocalTokenMissingRepos(t *testing.T) {
	key := []byte("test-secret-key-32-bytes-long!!!")

	// Create token with empty repos
	token, err := MintLocalToken(key, "test-user", []string{}, time.Hour)
	if err != nil {
		t.Fatalf("MintLocalToken() error: %v", err)
	}

	os.Setenv("CAINBAN_TOKEN", token)
	os.Setenv("CAINBAN_TOKEN_KEY", string(key))
	defer func() {
		os.Unsetenv("CAINBAN_TOKEN")
		os.Unsetenv("CAINBAN_TOKEN_KEY")
	}()

	_, err = localTenantFromEnv()
	if err == nil {
		t.Fatal("expected error for token with no repos, got nil")
	}
	if !strings.Contains(err.Error(), "repos") {
		t.Errorf("error should mention 'repos', got: %v", err)
	}
}

// TestLocalTokenPrecedenceOverRepo verifies that CAINBAN_TOKEN takes precedence
// over CAINBAN_REPO when both are set.
func TestLocalTokenPrecedenceOverRepo(t *testing.T) {
	key := []byte("test-secret-key-32-bytes-long!!!")

	token, err := MintLocalToken(key, "token-user", []string{"token/repo"}, time.Hour)
	if err != nil {
		t.Fatalf("MintLocalToken() error: %v", err)
	}

	// Set both TOKEN and REPO
	os.Setenv("CAINBAN_TOKEN", token)
	os.Setenv("CAINBAN_TOKEN_KEY", string(key))
	os.Setenv("CAINBAN_REPO", "env/repo")
	os.Setenv("CAINBAN_ACTOR", "env-actor")
	defer func() {
		os.Unsetenv("CAINBAN_TOKEN")
		os.Unsetenv("CAINBAN_TOKEN_KEY")
		os.Unsetenv("CAINBAN_REPO")
		os.Unsetenv("CAINBAN_ACTOR")
	}()

	tenant, err := localTenantFromEnv()
	if err != nil {
		t.Fatalf("localTenantFromEnv() error: %v", err)
	}

	// Token should win
	if tenant.Repo != "token/repo" {
		t.Errorf("tenant.Repo = %q, want %q (token should take precedence)", tenant.Repo, "token/repo")
	}
	if tenant.Actor != "token-user" {
		t.Errorf("tenant.Actor = %q, want %q (token should take precedence)", tenant.Actor, "token-user")
	}
}

// TestLocalTokenMissingKey verifies that a token without a signing key is rejected.
func TestLocalTokenMissingKey(t *testing.T) {
	key := []byte("test-secret-key-32-bytes-long!!!")
	token, _ := MintLocalToken(key, "test-user", []string{"hmain/cainban"}, time.Hour)

	os.Setenv("CAINBAN_TOKEN", token)
	os.Unsetenv("CAINBAN_TOKEN_KEY") // No key set
	defer os.Unsetenv("CAINBAN_TOKEN")

	_, err := localTenantFromEnv()
	if err == nil {
		t.Fatal("expected error when CAINBAN_TOKEN_KEY is not set, got nil")
	}
}
