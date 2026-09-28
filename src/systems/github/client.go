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

	// UserInstallations lists the GitHub App installations the user (identified
	// by their own user access token) can see, via GET /user/installations. It
	// is authenticated with the USER's token (Bearer), NOT the App JWT — so the
	// result is scoped to what THIS user can access, which is exactly Option A's
	// requirement. Returns each installation's id and the owning account login.
	UserInstallations(ctx context.Context, userToken string) ([]Installation, error)

	// InstallationRepositories lists the repositories the user can access within
	// one installation, via GET /user/installations/{id}/repositories, following
	// pagination until exhausted. Authenticated with the USER's token. Returns
	// each repo's full_name ("owner/repo").
	InstallationRepositories(ctx context.Context, userToken string, installationID int64) ([]string, error)

	// AppSlug returns THIS GitHub App's slug (the name in its public URL) via
	// GET /app, authenticated with the App JWT. It lets the connect API build
	// the install URL (github.com/apps/<slug>/installations/new) with no
	// operator-supplied slug env var — self-configuring. The slug is public.
	AppSlug(ctx context.Context) (string, error)
}

// ErrNotInstalled signals the App is not installed on the target repo (a 404
// from GET /repos/{owner}/{repo}/installation). It is a NEGATIVE result, not a
// transport failure — VerifyRepoAccess maps it to (false, nil) (deny), while a
// genuine transport/5xx error stays a non-nil error (fail closed).
var ErrNotInstalled = errors.New("github: app not installed on repo")

// Installation is one GitHub App installation the connecting user can see (from
// GET /user/installations). ID is the installation id used to list its repos;
// Account is the owning account's login (org or user) and AppID/AppSlug let the
// caller pick the installation belonging to THIS App when a user can see
// several.
type Installation struct {
	ID      int64
	Account string
	AppID   int64
	AppSlug string
}

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

// UserInstallations: GET /user/installations (USER token auth). Lists the App
// installations the user can access. GitHub wraps the array in an
// {total_count, installations:[...]} envelope. FAIL CLOSED: a non-2xx or decode
// error returns (nil, err). This endpoint is not paginated in practice for a
// single user, but we still read only the installations array.
func (c *httpClient) UserInstallations(ctx context.Context, userToken string) ([]Installation, error) {
	if strings.TrimSpace(userToken) == "" {
		return nil, errors.New("github: user installations: empty user token")
	}
	url := apiBase + "/user/installations?per_page=100"
	resp, err := c.do(ctx, http.MethodGet, url, userToken)
	if err != nil {
		return nil, fmt.Errorf("github: user installations: %w", err)
	}
	body := drain(resp)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github: user installations: unexpected status %d: %s", resp.StatusCode, snippet(body))
	}
	var out struct {
		Installations []struct {
			ID      int64  `json:"id"`
			AppID   int64  `json:"app_id"`
			AppSlug string `json:"app_slug"`
			Account struct {
				Login string `json:"login"`
			} `json:"account"`
		} `json:"installations"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("github: decode user installations: %w", err)
	}
	installs := make([]Installation, 0, len(out.Installations))
	for _, in := range out.Installations {
		installs = append(installs, Installation{
			ID:      in.ID,
			Account: in.Account.Login,
			AppID:   in.AppID,
			AppSlug: in.AppSlug,
		})
	}
	return installs, nil
}

// InstallationRepositories: GET /user/installations/{id}/repositories (USER
// token auth), following pagination. GitHub wraps the array in a
// {total_count, repositories:[...]} envelope and paginates via ?page. We
// request per_page=100 and advance the page until a page returns fewer than
// per_page repositories (or none). FAIL CLOSED: any non-2xx or decode error
// returns (nil, err) — the caller must never treat an error as "no repos".
func (c *httpClient) InstallationRepositories(ctx context.Context, userToken string, installationID int64) ([]string, error) {
	if strings.TrimSpace(userToken) == "" {
		return nil, errors.New("github: installation repositories: empty user token")
	}
	if installationID <= 0 {
		return nil, errors.New("github: installation repositories: installation id must be positive")
	}
	const perPage = 100
	var repos []string
	for page := 1; ; page++ {
		url := fmt.Sprintf("%s/user/installations/%d/repositories?per_page=%d&page=%d", apiBase, installationID, perPage, page)
		resp, err := c.do(ctx, http.MethodGet, url, userToken)
		if err != nil {
			return nil, fmt.Errorf("github: installation repositories: %w", err)
		}
		body := drain(resp)
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("github: installation repositories: unexpected status %d: %s", resp.StatusCode, snippet(body))
		}
		var out struct {
			Repositories []struct {
				FullName string `json:"full_name"`
			} `json:"repositories"`
		}
		if err := json.Unmarshal(body, &out); err != nil {
			return nil, fmt.Errorf("github: decode installation repositories: %w", err)
		}
		for _, r := range out.Repositories {
			if fn := strings.TrimSpace(r.FullName); fn != "" {
				repos = append(repos, fn)
			}
		}
		// Stop when the page is not full: no more pages to fetch. This is a safe
		// terminator (a full last page triggers one extra empty request, which
		// also terminates) and avoids parsing the Link header.
		if len(out.Repositories) < perPage {
			break
		}
	}
	return repos, nil
}

// AppSlug: GET /app (App JWT auth) -> the App's `slug`. The slug is public
// (it appears in the App's own URL), so this exposes no secret. A transport or
// non-200 is returned as an error so the caller can fall back / fail closed.
func (c *httpClient) AppSlug(ctx context.Context) (string, error) {
	jwt, err := c.appJWT()
	if err != nil {
		return "", err
	}
	url := fmt.Sprintf("%s/app", apiBase)
	resp, err := c.do(ctx, http.MethodGet, url, jwt)
	if err != nil {
		return "", fmt.Errorf("github: app: %w", err)
	}
	body := drain(resp)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github: app: unexpected status %d: %s", resp.StatusCode, snippet(body))
	}
	var out struct {
		Slug string `json:"slug"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("github: decode app: %w", err)
	}
	if strings.TrimSpace(out.Slug) == "" {
		return "", errors.New("github: app: empty slug")
	}
	return out.Slug, nil
}

// dumping a full page or leaking large payloads into logs.
func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}
