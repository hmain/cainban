package auth

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/hmain/cainban/src/systems/ghperm"
)

// mockGitHubClientForResolver is a test double for ghperm.GitHubClient
// that allows controlling permission responses.
type mockGitHubClientForResolver struct {
	mu       sync.Mutex
	canWrite bool
	err      error
	calls    []string // records "owner/repo" for each call
}

func (m *mockGitHubClientForResolver) GetRepoPermission(ctx context.Context, userToken, owner, repo string) (ghperm.Permission, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, owner+"/"+repo)
	if m.err != nil {
		return ghperm.PermissionNone, m.err
	}
	if m.canWrite {
		return ghperm.PermissionWrite, nil
	}
	return ghperm.PermissionNone, nil
}

func (m *mockGitHubClientForResolver) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.calls)
}

// gitHubClaims returns claims with GitHub identity fields.
func gitHubClaims() tokenClaims {
	return tokenClaims{
		Issuer:      "https://issuer.test/pool",
		Subject:     "user-1",
		Audience:    "test-audience",
		Expiry:      fixedNow.Add(time.Hour).Unix(),
		IssuedAt:    fixedNow.Add(-time.Minute).Unix(),
		GitHubUser:  "octocat",
		GitHubToken: "ghp_fake_token_12345",
		DefaultRepo: "acme/repo-a",
	}
}

// newGitHubResolver builds a Resolver with GitHub permission checking.
func newGitHubResolver(t *testing.T, ts *testSigner, mockClient *mockGitHubClientForResolver) *Resolver {
	t.Helper()
	v := newValidator(t, ts)
	cache := ghperm.NewMemoryCache(nil)
	checker, err := ghperm.NewChecker(ghperm.CheckerConfig{
		Client: mockClient,
		Cache:  cache,
		TTL:    5 * time.Minute,
	})
	if err != nil {
		t.Fatalf("NewChecker: %v", err)
	}
	return NewResolverWithGitHub(v, checker)
}

// TestGitHubAuth_ValidTokenAuthorized tests that a valid token with GitHub
// credentials is authorized when GitHub grants write access.
func TestGitHubAuth_ValidTokenAuthorized(t *testing.T) {
	// Enable GitHub permissions for this test
	oldFlag := UseGitHubPermissions
	UseGitHubPermissions = true
	defer func() { UseGitHubPermissions = oldFlag }()

	ts := newTestSigner(t, "kid-1")
	mock := &mockGitHubClientForResolver{canWrite: true}
	r := newGitHubResolver(t, ts, mock)
	tok := ts.sign(t, "RS256", gitHubClaims())

	tenant, err := r.Resolve(bearerReq(t, tok, "acme/repo-a"), "")
	if err != nil {
		t.Fatalf("expected authorized, got %v", err)
	}
	if tenant.Repo != "acme/repo-a" {
		t.Errorf("repo = %q, want acme/repo-a", tenant.Repo)
	}
	if mock.callCount() != 1 {
		t.Errorf("expected 1 GitHub API call, got %d", mock.callCount())
	}
}

// TestGitHubAuth_ValidTokenDenied tests that a valid token is denied when
// GitHub denies write access.
func TestGitHubAuth_ValidTokenDenied(t *testing.T) {
	oldFlag := UseGitHubPermissions
	UseGitHubPermissions = true
	defer func() { UseGitHubPermissions = oldFlag }()

	ts := newTestSigner(t, "kid-1")
	mock := &mockGitHubClientForResolver{canWrite: false}
	r := newGitHubResolver(t, ts, mock)
	tok := ts.sign(t, "RS256", gitHubClaims())

	_, err := r.Resolve(bearerReq(t, tok, "acme/repo-a"), "")
	if err == nil {
		t.Fatal("expected 403 for GitHub denial")
	}
	if HTTPStatus(err) != 403 {
		t.Errorf("status = %d, want 403", HTTPStatus(err))
	}
}

// TestGitHubAuth_GitHubError_FailClosed tests that GitHub API errors result
// in a 401 (fail closed).
func TestGitHubAuth_GitHubError_FailClosed(t *testing.T) {
	oldFlag := UseGitHubPermissions
	UseGitHubPermissions = true
	defer func() { UseGitHubPermissions = oldFlag }()

	ts := newTestSigner(t, "kid-1")
	mock := &mockGitHubClientForResolver{err: errors.New("rate limited")}
	r := newGitHubResolver(t, ts, mock)
	tok := ts.sign(t, "RS256", gitHubClaims())

	_, err := r.Resolve(bearerReq(t, tok, "acme/repo-a"), "")
	if err == nil {
		t.Fatal("expected error for GitHub failure")
	}
	// Fail closed means any error denies access (401)
	if HTTPStatus(err) != 401 {
		t.Errorf("status = %d, want 401 (fail closed)", HTTPStatus(err))
	}
}

// TestGitHubAuth_MissingGitHubUser tests that a token without github_user
// claim is rejected.
func TestGitHubAuth_MissingGitHubUser(t *testing.T) {
	oldFlag := UseGitHubPermissions
	UseGitHubPermissions = true
	defer func() { UseGitHubPermissions = oldFlag }()

	ts := newTestSigner(t, "kid-1")
	mock := &mockGitHubClientForResolver{canWrite: true}
	r := newGitHubResolver(t, ts, mock)

	c := gitHubClaims()
	c.GitHubUser = "" // Missing
	tok := ts.sign(t, "RS256", c)

	_, err := r.Resolve(bearerReq(t, tok, "acme/repo-a"), "")
	if err == nil {
		t.Fatal("expected error for missing github_user")
	}
	if HTTPStatus(err) != 401 {
		t.Errorf("status = %d, want 401", HTTPStatus(err))
	}
}

// TestGitHubAuth_MissingGitHubToken tests that a token without github_token
// claim is rejected.
func TestGitHubAuth_MissingGitHubToken(t *testing.T) {
	oldFlag := UseGitHubPermissions
	UseGitHubPermissions = true
	defer func() { UseGitHubPermissions = oldFlag }()

	ts := newTestSigner(t, "kid-1")
	mock := &mockGitHubClientForResolver{canWrite: true}
	r := newGitHubResolver(t, ts, mock)

	c := gitHubClaims()
	c.GitHubToken = "" // Missing
	tok := ts.sign(t, "RS256", c)

	_, err := r.Resolve(bearerReq(t, tok, "acme/repo-a"), "")
	if err == nil {
		t.Fatal("expected error for missing github_token")
	}
	if HTTPStatus(err) != 401 {
		t.Errorf("status = %d, want 401", HTTPStatus(err))
	}
}

// TestGitHubAuth_Unscoped_NoGitHubCall tests that unscoped requests (no repo)
// don't call GitHub.
func TestGitHubAuth_Unscoped_NoGitHubCall(t *testing.T) {
	oldFlag := UseGitHubPermissions
	UseGitHubPermissions = true
	defer func() { UseGitHubPermissions = oldFlag }()

	ts := newTestSigner(t, "kid-1")
	mock := &mockGitHubClientForResolver{canWrite: true}
	r := newGitHubResolver(t, ts, mock)

	c := gitHubClaims()
	c.DefaultRepo = "" // No default
	tok := ts.sign(t, "RS256", c)

	tenant, err := r.Resolve(bearerReq(t, tok, ""), "") // No repo specified
	if err != nil {
		t.Fatalf("expected unscoped success, got %v", err)
	}
	if !tenant.Unscoped {
		t.Error("expected Unscoped=true")
	}
	if mock.callCount() != 0 {
		t.Errorf("expected 0 GitHub calls for unscoped, got %d", mock.callCount())
	}
}

// TestGitHubAuth_FlagDisabled_UsesReposClaim tests that when the flag is
// disabled, the legacy repos claim is used.
func TestGitHubAuth_FlagDisabled_UsesReposClaim(t *testing.T) {
	// Ensure flag is disabled
	oldFlag := UseGitHubPermissions
	UseGitHubPermissions = false
	defer func() { UseGitHubPermissions = oldFlag }()

	ts := newTestSigner(t, "kid-1")
	mock := &mockGitHubClientForResolver{canWrite: false} // Would deny if called
	r := newGitHubResolver(t, ts, mock)

	// Token with repos claim (legacy mode)
	c := tokenClaims{
		Issuer:      "https://issuer.test/pool",
		Subject:     "user-1",
		Audience:    "test-audience",
		Expiry:      fixedNow.Add(time.Hour).Unix(),
		IssuedAt:    fixedNow.Add(-time.Minute).Unix(),
		Repos:       []string{"acme/repo-a"},
		DefaultRepo: "acme/repo-a",
	}
	tok := ts.sign(t, "RS256", c)

	tenant, err := r.Resolve(bearerReq(t, tok, "acme/repo-a"), "")
	if err != nil {
		t.Fatalf("expected authorized via repos claim, got %v", err)
	}
	if tenant.Repo != "acme/repo-a" {
		t.Errorf("repo = %q, want acme/repo-a", tenant.Repo)
	}
	// GitHub should NOT be called when flag is disabled
	if mock.callCount() != 0 {
		t.Errorf("expected 0 GitHub calls when flag disabled, got %d", mock.callCount())
	}
}

// TestGitHubAuth_NoPermChecker_FailClosed tests that if GitHub permissions
// are enabled but no PermissionChecker is configured, requests fail closed.
func TestGitHubAuth_NoPermChecker_FailClosed(t *testing.T) {
	oldFlag := UseGitHubPermissions
	UseGitHubPermissions = true
	defer func() { UseGitHubPermissions = oldFlag }()

	ts := newTestSigner(t, "kid-1")
	v := newValidator(t, ts)
	// Resolver without PermissionChecker
	r := NewResolver(v)
	tok := ts.sign(t, "RS256", gitHubClaims())

	_, err := r.Resolve(bearerReq(t, tok, "acme/repo-a"), "")
	if err == nil {
		t.Fatal("expected error when PermissionChecker is nil")
	}
	if HTTPStatus(err) != 401 {
		t.Errorf("status = %d, want 401", HTTPStatus(err))
	}
}

// TestGitHubAuth_CacheHit_NoSecondCall tests that cached permissions don't
// trigger additional GitHub API calls.
func TestGitHubAuth_CacheHit_NoSecondCall(t *testing.T) {
	oldFlag := UseGitHubPermissions
	UseGitHubPermissions = true
	defer func() { UseGitHubPermissions = oldFlag }()

	ts := newTestSigner(t, "kid-1")
	mock := &mockGitHubClientForResolver{canWrite: true}
	r := newGitHubResolver(t, ts, mock)
	tok := ts.sign(t, "RS256", gitHubClaims())

	// First call
	_, err := r.Resolve(bearerReq(t, tok, "acme/repo-a"), "")
	if err != nil {
		t.Fatalf("first call failed: %v", err)
	}
	if mock.callCount() != 1 {
		t.Fatalf("expected 1 call after first request, got %d", mock.callCount())
	}

	// Second call - should hit cache
	_, err = r.Resolve(bearerReq(t, tok, "acme/repo-a"), "")
	if err != nil {
		t.Fatalf("second call failed: %v", err)
	}
	if mock.callCount() != 1 {
		t.Errorf("expected still 1 call (cached), got %d", mock.callCount())
	}
}
