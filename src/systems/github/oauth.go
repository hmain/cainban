package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// oauthAuthorizeBase and oauthTokenURL are the GitHub *user*-OAuth endpoints
// used by the P4.3 connect leg. They are package vars (not consts) so a test
// can point them at a fake server / fake Doer without a live call, mirroring
// apiBase. P4.3 targets github.com only.
var (
	// oauthAuthorizeBase is where the browser is sent to authorize the App on
	// behalf of the user (GET, human-interactive). cainban only BUILDS this URL
	// (it never calls it server-side); the browser follows the redirect.
	oauthAuthorizeBase = "https://github.com/login/oauth/authorize"
	// oauthTokenURL is the server-to-server code<->user-token exchange (POST).
	oauthTokenURL = "https://github.com/login/oauth/access_token"
)

// OAuthConfig is the App's OAuth client credentials for the user-identification
// leg. ClientID/ClientSecret come from the Secrets Manager secret (never code):
// they are the same values github.AppConfig carries. RedirectURI is the App's
// configured callback URL (the connect Lambda's /connect/github/callback), used
// only to satisfy GitHub's optional redirect_uri echo — it is NOT trusted for
// authorization.
type OAuthConfig struct {
	ClientID     string
	ClientSecret string
	// RedirectURI is optional; when set it is passed through to GitHub so the
	// authorize + token calls agree on the callback. Leave empty to rely on the
	// App's registered default callback.
	RedirectURI string
}

// OAuth performs the user-OAuth leg: it builds the authorize URL a browser is
// redirected to, and exchanges the returned code for the connecting user's
// GitHub *login*. It is expressed against the Doer seam so tests inject a fake
// transport and NO live GitHub call is made. It deliberately does NOT persist
// anything and holds no long-lived state — the connect API owns persistence and
// the anti-CSRF state.
//
// SECURITY: the login returned here is the identity GitHub itself vouches for
// after the user authorized the App — it is NOT a client-supplied "I am X"
// claim. The connect API ties a grant to THIS login, and authorization is still
// decided separately by VerifyRepoAccess.
type OAuth struct {
	cfg  OAuthConfig
	doer Doer
}

// NewOAuth builds an OAuth leg from the App's OAuth credentials and an HTTP
// transport. A nil doer defaults to a timeout-bounded *http.Client; production
// should pass one with a timeout. ClientID/ClientSecret are required for the
// exchange (AuthorizeURL only needs ClientID).
func NewOAuth(cfg OAuthConfig, doer Doer) *OAuth {
	if doer == nil {
		doer = &http.Client{Timeout: 10 * time.Second}
	}
	return &OAuth{cfg: cfg, doer: doer}
}

// AuthorizeURL builds the GitHub authorize URL the browser is redirected to at
// GET /connect/github/start. state is the caller's opaque anti-CSRF value (tied
// to the Cognito sub by the connect API); it is echoed back to the callback and
// MUST be validated there. No user scope is requested beyond the default
// identity read the App's "Request user authorization" flow provides — the
// connect leg only needs to learn WHO the user is (their login).
func (o *OAuth) AuthorizeURL(state string) string {
	q := url.Values{}
	q.Set("client_id", o.cfg.ClientID)
	q.Set("state", state)
	if strings.TrimSpace(o.cfg.RedirectURI) != "" {
		q.Set("redirect_uri", o.cfg.RedirectURI)
	}
	// allow_signup=false: this is a connect flow for an existing account, not a
	// signup funnel.
	q.Set("allow_signup", "false")
	return oauthAuthorizeBase + "?" + q.Encode()
}

// ExchangeCode swaps an OAuth authorization code for the connecting user's
// GitHub login. It performs two server-side calls, both through the Doer seam:
//
//  1. POST https://github.com/login/oauth/access_token (client_id, client_secret,
//     code[, redirect_uri]) -> a short-lived USER access token.
//  2. GET  https://api.github.com/user (Bearer <user token>) -> the user's login.
//
// FAIL CLOSED: any transport error, non-2xx, GitHub-reported OAuth error, or
// empty login returns ("", err). An empty/blank code is rejected before any
// call. The caller treats a non-nil error / empty login as "no verified
// identity" and must NOT persist or grant anything.
func (o *OAuth) ExchangeCode(ctx context.Context, code string) (string, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return "", errors.New("github: oauth: empty authorization code")
	}
	if strings.TrimSpace(o.cfg.ClientID) == "" || strings.TrimSpace(o.cfg.ClientSecret) == "" {
		return "", errors.New("github: oauth: client id/secret not configured")
	}

	token, err := o.exchangeToken(ctx, code)
	if err != nil {
		return "", err
	}
	return o.fetchLogin(ctx, token)
}

// exchangeToken performs the code->user-token POST.
func (o *OAuth) exchangeToken(ctx context.Context, code string) (string, error) {
	form := url.Values{}
	form.Set("client_id", o.cfg.ClientID)
	form.Set("client_secret", o.cfg.ClientSecret)
	form.Set("code", code)
	if strings.TrimSpace(o.cfg.RedirectURI) != "" {
		form.Set("redirect_uri", o.cfg.RedirectURI)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, oauthTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("github: oauth: build token request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := o.doer.Do(req)
	if err != nil {
		return "", fmt.Errorf("github: oauth: token exchange: %w", err)
	}
	body := drain(resp)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github: oauth: token exchange: unexpected status %d: %s", resp.StatusCode, snippet(body))
	}
	// The JSON body carries EITHER an access_token OR an error (GitHub returns
	// 200 with an `error` field for a bad/expired code).
	var out struct {
		AccessToken      string `json:"access_token"`
		TokenType        string `json:"token_type"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("github: oauth: decode token response: %w", err)
	}
	if out.Error != "" {
		return "", fmt.Errorf("github: oauth: token exchange error %q: %s", out.Error, out.ErrorDescription)
	}
	if strings.TrimSpace(out.AccessToken) == "" {
		return "", errors.New("github: oauth: token response had empty access_token")
	}
	return out.AccessToken, nil
}

// fetchLogin reads GET /user with the user token and returns the login.
func (o *OAuth) fetchLogin(ctx context.Context, userToken string) (string, error) {
	url := apiBase + "/user"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("github: oauth: build user request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Authorization", "Bearer "+userToken)

	resp, err := o.doer.Do(req)
	if err != nil {
		return "", fmt.Errorf("github: oauth: fetch user: %w", err)
	}
	body := drain(resp)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github: oauth: fetch user: unexpected status %d: %s", resp.StatusCode, snippet(body))
	}
	var out struct {
		Login string `json:"login"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("github: oauth: decode user response: %w", err)
	}
	if strings.TrimSpace(out.Login) == "" {
		return "", errors.New("github: oauth: user response had empty login")
	}
	return out.Login, nil
}
