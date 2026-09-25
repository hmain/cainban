package github

import (
	"context"
	"errors"
	"testing"
)

// mockClient is a hand-configured Client implementation for VerifyRepoAccess
// tests. Each field is the canned result for the corresponding call, so a test
// composes a scenario declaratively. Call counts let a test assert that a
// short-circuit path did NOT make later calls (e.g. an org member is not also
// queried as a collaborator).
type mockClient struct {
	instID     int64
	instIDErr  error
	instToken  string
	instTokErr error
	member     bool
	memberErr  error
	perm       string
	permErr    error

	repoInstallCalls int
	instTokenCalls   int
	memberCalls      int
	permCalls        int
}

func (m *mockClient) RepoInstallationID(_ context.Context, _, _ string) (int64, error) {
	m.repoInstallCalls++
	return m.instID, m.instIDErr
}
func (m *mockClient) InstallationToken(_ context.Context, _ int64) (string, error) {
	m.instTokenCalls++
	if m.instToken == "" && m.instTokErr == nil {
		return "inst-tok", nil
	}
	return m.instToken, m.instTokErr
}
func (m *mockClient) IsOrgMember(_ context.Context, _, _, _ string) (bool, error) {
	m.memberCalls++
	return m.member, m.memberErr
}
func (m *mockClient) CollaboratorPermission(_ context.Context, _, _, _, _ string) (string, error) {
	m.permCalls++
	return m.perm, m.permErr
}

const (
	testOwner = "acme"
	testRepo  = "widgets"
	testLogin = "octocat"
)

func principal() Principal { return Principal{Login: testLogin} }

// (a) installation covers repo + user is an org member -> true.
func TestVerify_OrgMember_Allowed(t *testing.T) {
	m := &mockClient{instID: 42, member: true}
	ok, err := NewVerifier(m).VerifyRepoAccess(context.Background(), testOwner, testRepo, principal())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("org member should be allowed")
	}
	// Membership short-circuits: the collaborator fallback must NOT be queried.
	if m.permCalls != 0 {
		t.Errorf("collaborator permission should not be checked when org member; calls=%d", m.permCalls)
	}
}

// (b) installation does NOT cover repo -> false, no error (definitive deny).
func TestVerify_NotInstalled_Denied(t *testing.T) {
	m := &mockClient{instIDErr: ErrNotInstalled}
	ok, err := NewVerifier(m).VerifyRepoAccess(context.Background(), testOwner, testRepo, principal())
	if err != nil {
		t.Fatalf("ErrNotInstalled must be a definitive deny, not an error; got err=%v", err)
	}
	if ok {
		t.Fatal("not-installed must deny")
	}
	// Must not have tried to mint a token or check membership.
	if m.instTokenCalls != 0 || m.memberCalls != 0 || m.permCalls != 0 {
		t.Errorf("no further calls expected after not-installed; tok=%d member=%d perm=%d",
			m.instTokenCalls, m.memberCalls, m.permCalls)
	}
}

// (c) covered but user is NOT a member and NOT a collaborator -> false.
func TestVerify_CoveredButNotEntitled_Denied(t *testing.T) {
	m := &mockClient{instID: 42, member: false, perm: "none"}
	ok, err := NewVerifier(m).VerifyRepoAccess(context.Background(), testOwner, testRepo, principal())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("covered but not entitled must deny")
	}
	if m.memberCalls != 1 || m.permCalls != 1 {
		t.Errorf("expected both entitlement checks; member=%d perm=%d", m.memberCalls, m.permCalls)
	}
}

// (c') covered, not a member, but a repo collaborator with write -> true
// (outside-collaborator fallback path).
func TestVerify_CollaboratorFallback_Allowed(t *testing.T) {
	m := &mockClient{instID: 42, member: false, perm: "write"}
	ok, err := NewVerifier(m).VerifyRepoAccess(context.Background(), testOwner, testRepo, principal())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("collaborator with write should be allowed")
	}
}

// (d) a GitHub API error at each stage -> error returned, and NEVER true
// (fail closed). Table-drives the error at coverage, token, membership, and
// collaborator stages.
func TestVerify_APIError_FailsClosed(t *testing.T) {
	sentinel := errors.New("github: 500 boom")
	cases := []struct {
		name string
		m    *mockClient
	}{
		{"coverage lookup error", &mockClient{instIDErr: sentinel}},
		{"installation token error", &mockClient{instID: 42, instTokErr: sentinel}},
		{"membership check error", &mockClient{instID: 42, memberErr: sentinel}},
		{"collaborator check error", &mockClient{instID: 42, member: false, permErr: sentinel}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ok, err := NewVerifier(tc.m).VerifyRepoAccess(context.Background(), testOwner, testRepo, principal())
			if err == nil {
				t.Fatal("expected an error to be returned (fail closed)")
			}
			if ok {
				t.Fatal("access must NEVER be true on an API error")
			}
		})
	}
}

// (e) org membership path is exercised end to end: coverage -> token ->
// membership 204 -> allowed, with the exact call ordering.
func TestVerify_OrgMembershipPath_Ordering(t *testing.T) {
	m := &mockClient{instID: 7, member: true}
	ok, err := NewVerifier(m).VerifyRepoAccess(context.Background(), testOwner, testRepo, principal())
	if err != nil || !ok {
		t.Fatalf("org membership path should allow; ok=%v err=%v", ok, err)
	}
	if m.repoInstallCalls != 1 {
		t.Errorf("coverage lookup calls = %d, want 1", m.repoInstallCalls)
	}
	if m.instTokenCalls != 1 {
		t.Errorf("installation token calls = %d, want 1", m.instTokenCalls)
	}
	if m.memberCalls != 1 {
		t.Errorf("membership calls = %d, want 1", m.memberCalls)
	}
}

// An empty principal login is a definitive deny (cannot verify an identity),
// and must not make ANY GitHub call.
func TestVerify_EmptyLogin_Denied(t *testing.T) {
	m := &mockClient{instID: 42, member: true}
	ok, err := NewVerifier(m).VerifyRepoAccess(context.Background(), testOwner, testRepo, Principal{Login: ""})
	if err != nil {
		t.Fatalf("empty login should be a plain deny, not an error: %v", err)
	}
	if ok {
		t.Fatal("empty login must deny")
	}
	if m.repoInstallCalls != 0 {
		t.Errorf("no GitHub call expected for empty login; calls=%d", m.repoInstallCalls)
	}
}

// A malformed owner/repo is rejected before any GitHub call.
func TestVerify_MalformedRepo_Rejected(t *testing.T) {
	m := &mockClient{}
	ok, err := NewVerifier(m).VerifyRepoAccess(context.Background(), "acme", "bad/../repo", principal())
	if err == nil {
		t.Fatal("expected error for malformed repo")
	}
	if ok {
		t.Fatal("malformed repo must not be allowed")
	}
	if m.repoInstallCalls != 0 {
		t.Errorf("no GitHub call expected for malformed repo; calls=%d", m.repoInstallCalls)
	}
}

func TestHasReadAccess(t *testing.T) {
	for _, p := range []string{"admin", "write", "maintain", "triage", "read", "READ", " Write "} {
		if !hasReadAccess(p) {
			t.Errorf("hasReadAccess(%q) = false, want true", p)
		}
	}
	for _, p := range []string{"none", "", "bogus"} {
		if hasReadAccess(p) {
			t.Errorf("hasReadAccess(%q) = true, want false", p)
		}
	}
}
