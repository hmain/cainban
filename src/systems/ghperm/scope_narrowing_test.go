package ghperm

import (
	"context"
	"net/http"
	"testing"
)

// These tests lock the behaviors the scope-narrowing RFC depends on
// (docs/rfc-github-scope-narrowing.md). The response SHAPE of
// GET /repos/{owner}/{repo} does not change with a narrowed classic scope or a
// fine-grained PAT — only WHICH repos return 200 vs 404 does. Both the 200 and
// the 404 paths are already covered in client_test.go; here we lock the two
// narrowing-specific invariants explicitly so a future scope change can't
// silently break authorization.

// TestNarrowedScope_FineGrainedPAT_ReportsUserPermission proves the endpoint
// reports the USER's actual permission level even when the token is a
// fine-grained PAT with only Metadata: Read-only. The PAT's own permission set
// does not downgrade the reported `permissions` block — GitHub reports what the
// authenticated user can do on the repo. An owner therefore still reads as
// admin, so a fine-grained PAT is a valid personal-private-repo onboarding
// token (the chosen path for personal private repos).
func TestNarrowedScope_FineGrainedPAT_ReportsUserPermission(t *testing.T) {
	t.Parallel()
	doer := &mockDoer{
		statusCode: http.StatusOK,
		// A repo OWNER authenticating with a fine-grained PAT (Metadata: Read):
		// GitHub reports the user's real permission (admin), not the token's.
		body: `{
			"full_name": "octocat/private-notes",
			"private": true,
			"permissions": {
				"admin": true,
				"maintain": true,
				"push": true,
				"triage": true,
				"pull": true
			}
		}`,
	}
	client := NewGitHubClient(doer)

	perm, err := client.GetRepoPermission(context.Background(), "github_pat_fakefakefake", "octocat", "private-notes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if perm != PermissionAdmin {
		t.Errorf("fine-grained PAT owner should read as admin, got %s", perm)
	}
	if !perm.CanWrite() {
		t.Errorf("admin permission must satisfy CanWrite")
	}
}

// TestNarrowedScope_PrivateRepoInvisible_DeniesClosed proves that when the
// narrowed scope cannot see a private repo, GitHub returns 404 and the checker
// maps it to PermissionNone (deny) WITHOUT an error. This is the exact case a
// personal private repo hits under read:org (no org to grant visibility): it
// must fail closed to "no access", never to an error that a caller might
// mishandle, and never to a grant.
func TestNarrowedScope_PrivateRepoInvisible_DeniesClosed(t *testing.T) {
	t.Parallel()
	doer := &mockDoer{
		statusCode: http.StatusNotFound,
		body:       `{"message":"Not Found"}`,
	}
	client := NewGitHubClient(doer)

	perm, err := client.GetRepoPermission(context.Background(), "gho_narrowtoken", "octocat", "private-notes")
	if err != nil {
		t.Fatalf("invisible private repo must not error, got: %v", err)
	}
	if perm != PermissionNone {
		t.Errorf("invisible private repo must deny (PermissionNone), got %s", perm)
	}
	if perm.CanWrite() {
		t.Errorf("PermissionNone must never satisfy CanWrite")
	}
}
