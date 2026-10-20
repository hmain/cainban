package ghperm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const apiBase = "https://api.github.com"

// Doer is an HTTP client interface (matches *http.Client).
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

// GitHubClient is the subset of GitHub API needed for permission checks.
// It uses the user's own GitHub token (not an App JWT).
type GitHubClient interface {
	// GetRepoPermission returns the user's permission level on a repo.
	// Uses the user's own GitHub token for authentication.
	// Returns PermissionNone if user has no access (not an error).
	// Returns error only on transport/API failures (fail closed).
	GetRepoPermission(ctx context.Context, userToken, owner, repo string) (Permission, error)
}

// httpGitHubClient implements GitHubClient using the GitHub REST API.
type httpGitHubClient struct {
	doer Doer
}

// NewGitHubClient creates a GitHubClient with the given HTTP client.
// If doer is nil, a default http.Client with 10s timeout is used.
func NewGitHubClient(doer Doer) GitHubClient {
	if doer == nil {
		doer = &http.Client{Timeout: 10 * time.Second}
	}
	return &httpGitHubClient{doer: doer}
}

// GetRepoPermission calls GET /repos/{owner}/{repo} with the user's token.
// The response includes a "permissions" object showing the user's access level.
// Returns PermissionNone for 404 (no access), error for other non-2xx.
func (c *httpGitHubClient) GetRepoPermission(ctx context.Context, userToken, owner, repo string) (Permission, error) {
	if strings.TrimSpace(userToken) == "" {
		return PermissionNone, errors.New("ghperm: empty user token")
	}

	url := fmt.Sprintf("%s/repos/%s/%s", apiBase, owner, repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return PermissionNone, fmt.Errorf("ghperm: build request: %w", err)
	}

	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Authorization", "Bearer "+userToken)

	resp, err := c.doer.Do(req)
	if err != nil {
		return PermissionNone, fmt.Errorf("ghperm: http request: %w", err)
	}
	body := drain(resp)

	switch resp.StatusCode {
	case http.StatusOK:
		return parsePermissions(body)
	case http.StatusNotFound:
		// No access to repo (private and not a collaborator, or doesn't exist)
		return PermissionNone, nil
	default:
		return PermissionNone, fmt.Errorf("ghperm: unexpected status %d: %s", resp.StatusCode, snippet(body))
	}
}

// repoResponse is the subset of GET /repos/{owner}/{repo} we care about.
type repoResponse struct {
	Permissions struct {
		Admin    bool `json:"admin"`
		Maintain bool `json:"maintain"`
		Push     bool `json:"push"`
		Triage   bool `json:"triage"`
		Pull     bool `json:"pull"`
	} `json:"permissions"`
}

// parsePermissions extracts the permission level from a repo response.
// Order matters: check from highest to lowest permission.
func parsePermissions(body []byte) (Permission, error) {
	var resp repoResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return PermissionNone, fmt.Errorf("ghperm: decode response: %w", err)
	}

	p := resp.Permissions
	switch {
	case p.Admin:
		return PermissionAdmin, nil
	case p.Maintain:
		return PermissionMaintain, nil
	case p.Push:
		return PermissionWrite, nil
	case p.Triage:
		return PermissionTriage, nil
	case p.Pull:
		return PermissionRead, nil
	default:
		return PermissionNone, nil
	}
}

// drain reads and closes a response body so the connection can be reused.
func drain(resp *http.Response) []byte {
	if resp == nil || resp.Body == nil {
		return nil
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	_ = resp.Body.Close()
	return b
}

// snippet truncates a byte slice for error messages.
func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}
