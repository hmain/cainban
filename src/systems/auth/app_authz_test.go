package auth

import (
	"context"
	"errors"
	"testing"
	"time"
)

// mockVerifier is a test double for the App-backed RepoVerifier. It records the
// login/owner/repo it was asked about so a test can assert the resolver passed
// the token's GitHub login (identity) through to the verifier — never a
// client-supplied value. It deliberately uses NO github import: the resolver
// depends on the local RepoVerifier interface (a *github.Verifier is adapted in
// the Lambda wiring), which also avoids the auth<->github import cycle.
type mockVerifier struct {
	allow     bool
	err       error
	gotLogin  string
	gotOwner  string
	gotRepo   string
	callCount int
}

func (m *mockVerifier) VerifyRepoAccess(_ context.Context, owner, repo, login string) (bool, error) {
	m.callCount++
	m.gotOwner, m.gotRepo, m.gotLogin = owner, repo, login
	return m.allow, m.err
}

func newAppResolver(t *testing.T, ts *testSigner, mv *mockVerifier) *Resolver {
	t.Helper()
	v := newValidator(t, ts)
	return NewResolverWithApp(v, mv)
}

// gitHubClaims returns claims carrying a GitHub identity (github_user). After
// the GitHub-only migration, authorization uses the App verifier keyed by this
// login; the token itself is identity-only.
func gitHubClaims() tokenClaims {
	return tokenClaims{
		Issuer:      "https://issuer.test/pool",
		Subject:     "user-1",
		Audience:    "test-audience",
		Expiry:      fixedNow.Add(time.Hour).Unix(),
		IssuedAt:    fixedNow.Add(-time.Minute).Unix(),
		GitHubUser:  "octocat",
		DefaultRepo: "acme/repo-a",
	}
}

// TestAppAuth_Authorized proves a repo the App verifier grants resolves to a
// scoped tenant, and that the login passed to the verifier is the token's
// github_user (identity), not the repo arg.
func TestAppAuth_Authorized(t *testing.T) {
	oldFlag := UseGitHubPermissions
	UseGitHubPermissions = true
	defer func() { UseGitHubPermissions = oldFlag }()

	ts := newTestSigner(t, "kid-1")
	mv := &mockVerifier{allow: true}
	r := newAppResolver(t, ts, mv)
	tok := ts.sign(t, "RS256", gitHubClaims())

	tenant, err := r.Resolve(bearerReq(t, tok, "acme/repo-a"), "")
	if err != nil {
		t.Fatalf("expected authorized, got %v", err)
	}
	if tenant.Repo != "acme/repo-a" {
		t.Errorf("repo = %q, want acme/repo-a", tenant.Repo)
	}
	if mv.callCount != 1 {
		t.Errorf("expected 1 verify call, got %d", mv.callCount)
	}
	if mv.gotLogin != "octocat" {
		t.Errorf("verifier got login %q, want octocat (the token identity)", mv.gotLogin)
	}
	if mv.gotOwner != "acme" || mv.gotRepo != "repo-a" {
		t.Errorf("verifier got %s/%s, want acme/repo-a", mv.gotOwner, mv.gotRepo)
	}
}

// TestAppAuth_Denied proves a definitive deny (false, nil) from the verifier is
// a 403.
func TestAppAuth_Denied(t *testing.T) {
	oldFlag := UseGitHubPermissions
	UseGitHubPermissions = true
	defer func() { UseGitHubPermissions = oldFlag }()

	ts := newTestSigner(t, "kid-1")
	mv := &mockVerifier{allow: false}
	r := newAppResolver(t, ts, mv)
	tok := ts.sign(t, "RS256", gitHubClaims())

	_, err := r.Resolve(bearerReq(t, tok, "acme/repo-a"), "")
	if err == nil {
		t.Fatal("expected 403 for verifier denial")
	}
	if HTTPStatus(err) != 403 {
		t.Errorf("status = %d, want 403", HTTPStatus(err))
	}
}

// TestAppAuth_VerifierError_FailClosed proves any verifier error denies (401,
// fail closed) — an error must never read as "granted".
func TestAppAuth_VerifierError_FailClosed(t *testing.T) {
	oldFlag := UseGitHubPermissions
	UseGitHubPermissions = true
	defer func() { UseGitHubPermissions = oldFlag }()

	ts := newTestSigner(t, "kid-1")
	mv := &mockVerifier{err: errors.New("github 500")}
	r := newAppResolver(t, ts, mv)
	tok := ts.sign(t, "RS256", gitHubClaims())

	_, err := r.Resolve(bearerReq(t, tok, "acme/repo-a"), "")
	if err == nil {
		t.Fatal("expected error (fail closed)")
	}
	if HTTPStatus(err) != 401 {
		t.Errorf("status = %d, want 401 (fail closed)", HTTPStatus(err))
	}
}

// TestAppAuth_MissingLogin_Denied proves a token with no github_user cannot be
// verified and is denied (401) without any verifier call.
func TestAppAuth_MissingLogin_Denied(t *testing.T) {
	oldFlag := UseGitHubPermissions
	UseGitHubPermissions = true
	defer func() { UseGitHubPermissions = oldFlag }()

	ts := newTestSigner(t, "kid-1")
	mv := &mockVerifier{allow: true}
	r := newAppResolver(t, ts, mv)

	c := gitHubClaims()
	c.GitHubUser = ""
	tok := ts.sign(t, "RS256", c)

	_, err := r.Resolve(bearerReq(t, tok, "acme/repo-a"), "")
	if err == nil {
		t.Fatal("expected 401 for missing github_user")
	}
	if HTTPStatus(err) != 401 {
		t.Errorf("status = %d, want 401", HTTPStatus(err))
	}
	if mv.callCount != 0 {
		t.Errorf("verifier must not be called without an identity, got %d calls", mv.callCount)
	}
}
