package ghperm

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

// mockDoer is a test double for http.Client
type mockDoer struct {
	statusCode int
	body       string
	err        error
	lastReq    *http.Request
}

func (m *mockDoer) Do(req *http.Request) (*http.Response, error) {
	m.lastReq = req
	if m.err != nil {
		return nil, m.err
	}
	return &http.Response{
		StatusCode: m.statusCode,
		Body:       io.NopCloser(strings.NewReader(m.body)),
		Header:     make(http.Header),
	}, nil
}

func TestGitHubClient_GetRepoPermission_Write(t *testing.T) {
	t.Parallel()
	doer := &mockDoer{
		statusCode: http.StatusOK,
		body: `{
			"full_name": "acme/frontend",
			"permissions": {
				"admin": false,
				"maintain": false,
				"push": true,
				"triage": true,
				"pull": true
			}
		}`,
	}
	client := NewGitHubClient(doer)
	ctx := context.Background()

	perm, err := client.GetRepoPermission(ctx, "fake-token", "acme", "frontend")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if perm != PermissionWrite {
		t.Errorf("expected PermissionWrite, got %s", perm)
	}

	// Verify request
	if doer.lastReq.URL.Path != "/repos/acme/frontend" {
		t.Errorf("unexpected path: %s", doer.lastReq.URL.Path)
	}
	if auth := doer.lastReq.Header.Get("Authorization"); auth != "Bearer fake-token" {
		t.Errorf("unexpected auth header: %s", auth)
	}
}

func TestGitHubClient_GetRepoPermission_Admin(t *testing.T) {
	t.Parallel()
	doer := &mockDoer{
		statusCode: http.StatusOK,
		body: `{
			"full_name": "acme/frontend",
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
	ctx := context.Background()

	perm, err := client.GetRepoPermission(ctx, "fake-token", "acme", "frontend")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if perm != PermissionAdmin {
		t.Errorf("expected PermissionAdmin, got %s", perm)
	}
}

func TestGitHubClient_GetRepoPermission_Maintain(t *testing.T) {
	t.Parallel()
	doer := &mockDoer{
		statusCode: http.StatusOK,
		body: `{
			"full_name": "acme/frontend",
			"permissions": {
				"admin": false,
				"maintain": true,
				"push": true,
				"triage": true,
				"pull": true
			}
		}`,
	}
	client := NewGitHubClient(doer)
	ctx := context.Background()

	perm, err := client.GetRepoPermission(ctx, "fake-token", "acme", "frontend")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if perm != PermissionMaintain {
		t.Errorf("expected PermissionMaintain, got %s", perm)
	}
}

func TestGitHubClient_GetRepoPermission_ReadOnly(t *testing.T) {
	t.Parallel()
	doer := &mockDoer{
		statusCode: http.StatusOK,
		body: `{
			"full_name": "acme/frontend",
			"permissions": {
				"admin": false,
				"maintain": false,
				"push": false,
				"triage": false,
				"pull": true
			}
		}`,
	}
	client := NewGitHubClient(doer)
	ctx := context.Background()

	perm, err := client.GetRepoPermission(ctx, "fake-token", "acme", "frontend")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if perm != PermissionRead {
		t.Errorf("expected PermissionRead, got %s", perm)
	}
}

func TestGitHubClient_GetRepoPermission_Triage(t *testing.T) {
	t.Parallel()
	doer := &mockDoer{
		statusCode: http.StatusOK,
		body: `{
			"full_name": "acme/frontend",
			"permissions": {
				"admin": false,
				"maintain": false,
				"push": false,
				"triage": true,
				"pull": true
			}
		}`,
	}
	client := NewGitHubClient(doer)
	ctx := context.Background()

	perm, err := client.GetRepoPermission(ctx, "fake-token", "acme", "frontend")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if perm != PermissionTriage {
		t.Errorf("expected PermissionTriage, got %s", perm)
	}
}

func TestGitHubClient_GetRepoPermission_NoAccess(t *testing.T) {
	t.Parallel()
	doer := &mockDoer{
		statusCode: http.StatusNotFound,
		body:       `{"message": "Not Found"}`,
	}
	client := NewGitHubClient(doer)
	ctx := context.Background()

	perm, err := client.GetRepoPermission(ctx, "fake-token", "acme", "frontend")
	if err != nil {
		t.Fatalf("unexpected error: %v", err) // 404 = PermissionNone, not error
	}
	if perm != PermissionNone {
		t.Errorf("expected PermissionNone, got %s", perm)
	}
}

func TestGitHubClient_GetRepoPermission_RateLimit(t *testing.T) {
	t.Parallel()
	doer := &mockDoer{
		statusCode: http.StatusForbidden,
		body:       `{"message": "API rate limit exceeded"}`,
	}
	client := NewGitHubClient(doer)
	ctx := context.Background()

	_, err := client.GetRepoPermission(ctx, "fake-token", "acme", "frontend")
	if err == nil {
		t.Fatal("expected error for rate limit")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("error should mention 403: %v", err)
	}
}

func TestGitHubClient_GetRepoPermission_ServerError(t *testing.T) {
	t.Parallel()
	doer := &mockDoer{
		statusCode: http.StatusInternalServerError,
		body:       `{"message": "Internal server error"}`,
	}
	client := NewGitHubClient(doer)
	ctx := context.Background()

	_, err := client.GetRepoPermission(ctx, "fake-token", "acme", "frontend")
	if err == nil {
		t.Fatal("expected error for 500")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error should mention 500: %v", err)
	}
}

func TestGitHubClient_GetRepoPermission_TransportError(t *testing.T) {
	t.Parallel()
	doer := &mockDoer{
		err: io.ErrUnexpectedEOF,
	}
	client := NewGitHubClient(doer)
	ctx := context.Background()

	_, err := client.GetRepoPermission(ctx, "fake-token", "acme", "frontend")
	if err == nil {
		t.Fatal("expected error for transport failure")
	}
}

func TestGitHubClient_GetRepoPermission_InvalidJSON(t *testing.T) {
	t.Parallel()
	doer := &mockDoer{
		statusCode: http.StatusOK,
		body:       `not valid json`,
	}
	client := NewGitHubClient(doer)
	ctx := context.Background()

	_, err := client.GetRepoPermission(ctx, "fake-token", "acme", "frontend")
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestGitHubClient_GetRepoPermission_EmptyToken(t *testing.T) {
	t.Parallel()
	client := NewGitHubClient(&mockDoer{})
	ctx := context.Background()

	_, err := client.GetRepoPermission(ctx, "", "acme", "frontend")
	if err == nil {
		t.Fatal("expected error for empty token")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("error should mention empty: %v", err)
	}
}

func TestGitHubClient_GetRepoPermission_RequestHeaders(t *testing.T) {
	t.Parallel()
	doer := &mockDoer{
		statusCode: http.StatusOK,
		body: `{
			"permissions": {"pull": true}
		}`,
	}
	client := NewGitHubClient(doer)
	ctx := context.Background()

	_, _ = client.GetRepoPermission(ctx, "test-token", "owner", "repo")

	// Verify GitHub API headers
	if doer.lastReq.Header.Get("Accept") != "application/vnd.github+json" {
		t.Errorf("missing Accept header")
	}
	if doer.lastReq.Header.Get("X-GitHub-Api-Version") != "2022-11-28" {
		t.Errorf("missing or wrong X-GitHub-Api-Version header")
	}
}

func TestNewGitHubClient_NilDoer(t *testing.T) {
	t.Parallel()
	// Should not panic with nil doer (uses default)
	client := NewGitHubClient(nil)
	if client == nil {
		t.Error("expected non-nil client")
	}
}
