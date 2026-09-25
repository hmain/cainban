package github

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client is the mockable GitHub App API surface VerifyRepoAccess is written
// against. Every method is ONE documented GitHub REST call. Tests either inject
// a fake Doer into the real httpClient (exercising request-building + decoding)
// or mock this interface directly (exercising VerifyRepoAccess's decision
// logic) — either way no live GitHub call is made.
type Client interface {
	// InstallationToken exchanges the App JWT for a short-lived installation
	// token via POST /app/installations/{installation_id}/access_tokens.
	InstallationToken(ctx context.Context, installationID int64) (string, error)

	// RepoInstallationID reports the installation id that covers a repo via
	// GET /repos/{owner}/{repo}/installation (authenticated with the App JWT).
	// A 404 means the App is NOT installed on that repo — returned as
	// ErrNotInstalled so the caller can map it to "no coverage" (deny) rather
	// than a hard error.
	RepoInstallationID(ctx context.Context, owner, repo string) (int64, error)

	// IsOrgMember reports whether login is a member of org via
	// GET /orgs/{org}/members/{login} using the installation token. GitHub
	// returns 204 (member) / 404 (not a member) / 302 (requester not a member,
	// treated as not-a-member for our purposes).
	IsOrgMember(ctx context.Context, instToken, org, login string) (bool, error)

	// CollaboratorPermission reports login's permission level on owner/repo via
	// GET /repos/{owner}/{repo}/collaborators/{login}/permission using the
	// installation token. Returns the permission string ("admin","write",
	// "read","none"); a 404 (not a collaborator) returns ("none", nil).
	CollaboratorPermission(ctx context.Context, instToken, owner, repo, login string) (string, error)
}

// ErrNotInstalled signals the App is not installed on the target repo (a 404
// from GET /repos/{owner}/{repo}/installation). It is a NEGATIVE result, not a
// transport failure — VerifyRepoAccess maps it to (false, nil) (deny), while a
// genuine transport/5xx error stays a non-nil error (fail closed).
var ErrNotInstalled = errors.New("github: app not installed on repo")

// httpClient is the production Client: it mints an App JWT per App-authenticated
// call and performs real REST requests through a Doer.
type httpClient struct {
	doer  Doer
	appID int64
	key   *rsa.PrivateKey
	now   func() time.Time // injectable clock for deterministic JWT tests
}

// NewClient builds a production Client from an AppConfig. The RSA private key
// is parsed once here so a malformed key is caught at construction, not on the
// first call. doer is the HTTP transport; pass a real *http.Client (with a
// timeout) in production, a fake in tests. A nil doer defaults to
// http.DefaultClient — but production should pass a client with a timeout.
func NewClient(cfg AppConfig, doer Doer) (Client, error) {
	if cfg.AppID <= 0 {
		return nil, errors.New("github: AppConfig.AppID must be positive")
	}
	key, err := parseRSAPrivateKey(cfg.PrivateKeyPEM)
	if err != nil {
		return nil, err
	}
	if doer == nil {
		doer = &http.Client{Timeout: 10 * time.Second}
	}
	return &httpClient{doer: doer, appID: cfg.AppID, key: key, now: time.Now}, nil
}

// appJWT mints a fresh short-lived App JWT for an App-authenticated request.
func (c *httpClient) appJWT() (string, error) {
	return mintAppJWT(c.appID, c.key, c.now())
}

// do performs a request with the standard GitHub headers and returns the
// response. The caller owns closing the body.
func (c *httpClient) do(ctx context.Context, method, url, bearer string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return nil, fmt.Errorf("github: build request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Authorization", "Bearer "+bearer)
	return c.doer.Do(req)
}

// drain reads and closes a response body so a connection can be reused, and
// returns the bytes for optional decoding/error context.
func drain(resp *http.Response) []byte {
	if resp == nil || resp.Body == nil {
		return nil
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	_ = resp.Body.Close()
	return b
}

// InstallationToken: POST /app/installations/{id}/access_tokens (App JWT auth).
func (c *httpClient) InstallationToken(ctx context.Context, installationID int64) (string, error) {
	if installationID <= 0 {
		return "", errors.New("github: installation id must be positive")
	}
	jwt, err := c.appJWT()
	if err != nil {
		return "", err
	}
	url := fmt.Sprintf("%s/app/installations/%d/access_tokens", apiBase, installationID)
	// This is a POST with no body; reuse do() but with POST.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return "", fmt.Errorf("github: build installation-token request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Authorization", "Bearer "+jwt)
	resp, err := c.doer.Do(req)
	if err != nil {
		return "", fmt.Errorf("github: installation-token exchange: %w", err)
	}
	body := drain(resp)
	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("github: installation-token exchange: unexpected status %d: %s", resp.StatusCode, snippet(body))
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("github: decode installation-token response: %w", err)
	}
	if out.Token == "" {
		return "", errors.New("github: installation-token response had empty token")
	}
	return out.Token, nil
}

// RepoInstallationID: GET /repos/{owner}/{repo}/installation (App JWT auth).
func (c *httpClient) RepoInstallationID(ctx context.Context, owner, repo string) (int64, error) {
	jwt, err := c.appJWT()
	if err != nil {
		return 0, err
	}
	url := fmt.Sprintf("%s/repos/%s/%s/installation", apiBase, owner, repo)
	resp, err := c.do(ctx, http.MethodGet, url, jwt)
	if err != nil {
		return 0, fmt.Errorf("github: repo installation lookup: %w", err)
	}
	body := drain(resp)
	switch resp.StatusCode {
	case http.StatusOK:
		var out struct {
			ID int64 `json:"id"`
		}
		if err := json.Unmarshal(body, &out); err != nil {
			return 0, fmt.Errorf("github: decode repo installation: %w", err)
		}
		if out.ID <= 0 {
			return 0, errors.New("github: repo installation response had no id")
		}
		return out.ID, nil
	case http.StatusNotFound:
		// App not installed on this repo — a negative result, not a failure.
		return 0, ErrNotInstalled
	default:
		return 0, fmt.Errorf("github: repo installation lookup: unexpected status %d: %s", resp.StatusCode, snippet(body))
	}
}

// IsOrgMember: GET /orgs/{org}/members/{login} (installation token auth).
// 204 => member; 404 => not a member; 302 => requester cannot see membership
// (treated as not-a-member). Any other status is a fail-closed error.
func (c *httpClient) IsOrgMember(ctx context.Context, instToken, org, login string) (bool, error) {
	url := fmt.Sprintf("%s/orgs/%s/members/%s", apiBase, org, login)
	resp, err := c.do(ctx, http.MethodGet, url, instToken)
	if err != nil {
		return false, fmt.Errorf("github: org membership check: %w", err)
	}
	body := drain(resp)
	switch resp.StatusCode {
	case http.StatusNoContent:
		return true, nil
	case http.StatusNotFound, http.StatusFound:
		return false, nil
	default:
		return false, fmt.Errorf("github: org membership check: unexpected status %d: %s", resp.StatusCode, snippet(body))
	}
}

// CollaboratorPermission: GET /repos/{owner}/{repo}/collaborators/{login}/permission
// (installation token auth). 200 => decode permission; 404 => not a
// collaborator ("none"); other => fail-closed error.
func (c *httpClient) CollaboratorPermission(ctx context.Context, instToken, owner, repo, login string) (string, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/collaborators/%s/permission", apiBase, owner, repo, login)
	resp, err := c.do(ctx, http.MethodGet, url, instToken)
	if err != nil {
		return "", fmt.Errorf("github: collaborator permission check: %w", err)
	}
	body := drain(resp)
	switch resp.StatusCode {
	case http.StatusOK:
		var out struct {
			Permission string `json:"permission"`
		}
		if err := json.Unmarshal(body, &out); err != nil {
			return "", fmt.Errorf("github: decode collaborator permission: %w", err)
		}
		if out.Permission == "" {
			return "none", nil
		}
		return out.Permission, nil
	case http.StatusNotFound:
		return "none", nil
	default:
		return "", fmt.Errorf("github: collaborator permission check: unexpected status %d: %s", resp.StatusCode, snippet(body))
	}
}

// snippet trims a response body for inclusion in an error message without
// dumping a full page or leaking large payloads into logs.
func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}
