// Package mcp provides the MCP HTTP handlers and transport.
package mcp

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/hmain/cainban/src/systems/github"
)

// GitHubAuthConfig carries the GitHub OAuth credentials for the device flow.
type GitHubAuthConfig struct {
	ClientID     string
	ClientSecret string
}

// GitHubDeviceStartRequest is the request body for POST /auth/github/device.
type GitHubDeviceStartRequest struct {
	// Scope is optional; defaults to defaultDeviceScope for cainban.
	Scope string `json:"scope,omitempty"`
}

// defaultDeviceScope is the narrowed default OAuth scope cainban requests in the
// device flow. It deliberately OMITS the broad classic `repo` scope: cainban
// only performs a READ-ONLY permission check (ghperm.PermissionChecker calls
// GET /repos/{owner}/{repo} and reads the `permissions` object), so it needs
// identity (read:user, user:email) plus org read (read:org) to see org private
// repos — never write access to repository contents. Personal private repos are
// handled out-of-band with a fine-grained PAT (Metadata: Read-only); see
// docs/rfc-github-scope-narrowing.md. Changing this to re-add `repo` is a
// security regression guarded by TestDefaultDeviceScopeNeverHasRepo and a
// pre-commit grep hook.
const defaultDeviceScope = "read:user user:email read:org"

// resolveDeviceScope returns the scope to request for a device flow: the
// caller's explicit scope when non-empty (trimmed), else the narrowed
// defaultDeviceScope. It is a pure function so the scope policy is unit-tested
// without a live GitHub call.
func resolveDeviceScope(requested string) string {
	if s := strings.TrimSpace(requested); s != "" {
		return s
	}
	return defaultDeviceScope
}

// GitHubDeviceStartResponse is returned when initiating the device flow.
type GitHubDeviceStartResponse struct {
	// UserCode is the code the user enters at VerificationURI.
	UserCode string `json:"user_code"`
	// VerificationURI is where the user goes to enter the code.
	VerificationURI string `json:"verification_uri"`
	// VerificationURIComplete is the full URL with code pre-filled (optional).
	VerificationURIComplete string `json:"verification_uri_complete,omitempty"`
	// DeviceCode is the code the client uses to poll for a token.
	// The client should store this securely and not show it to the user.
	DeviceCode string `json:"device_code"`
	// ExpiresIn is how long the device_code is valid (seconds).
	ExpiresIn int `json:"expires_in"`
	// Interval is the minimum seconds between poll requests.
	Interval int `json:"interval"`
}

// GitHubDevicePollRequest is the request body for POST /auth/github/device/poll.
type GitHubDevicePollRequest struct {
	// DeviceCode is the device_code from the start response.
	DeviceCode string `json:"device_code"`
}

// GitHubDevicePollResponse is returned when polling succeeds.
type GitHubDevicePollResponse struct {
	// AccessToken is the GitHub access token. Use this for API requests.
	AccessToken string `json:"access_token"`
	// TokenType is always "bearer".
	TokenType string `json:"token_type"`
	// Scope is the granted scopes.
	Scope string `json:"scope,omitempty"`
}

// GitHubDevicePollPendingResponse indicates the user hasn't completed auth yet.
type GitHubDevicePollPendingResponse struct {
	// Error is "authorization_pending" when the user hasn't completed auth.
	Error string `json:"error"`
	// ErrorDescription provides more detail.
	ErrorDescription string `json:"error_description,omitempty"`
	// RetryAfter is the number of seconds to wait before polling again.
	RetryAfter int `json:"retry_after,omitempty"`
}

// NewGitHubDeviceStartHandler returns an http.Handler for POST /auth/github/device.
// It initiates the GitHub device flow and returns the user_code and verification_uri.
func NewGitHubDeviceStartHandler(cfg GitHubAuthConfig) http.Handler {
	oauth := github.NewOAuth(github.OAuthConfig{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
	}, nil)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Parse optional request body for scope override.
		var req GitHubDeviceStartRequest
		if r.Body != nil && r.ContentLength > 0 {
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, "invalid request body", http.StatusBadRequest)
				return
			}
		}

		// Narrowed scope: identity + org read, NEVER the broad `repo` scope.
		// cainban only does a read-only permission check; see
		// resolveDeviceScope / docs/rfc-github-scope-narrowing.md. The token is
		// stored only on the user's machine in their MCP client config, never on
		// cainban servers.
		scope := resolveDeviceScope(req.Scope)

		dcr, err := oauth.StartDeviceFlow(r.Context(), scope)
		if err != nil {
			http.Error(w, "failed to start device flow: "+err.Error(), http.StatusInternalServerError)
			return
		}

		resp := GitHubDeviceStartResponse{
			UserCode:                dcr.UserCode,
			VerificationURI:         dcr.VerificationURI,
			VerificationURIComplete: dcr.VerificationURIComplete,
			DeviceCode:              dcr.DeviceCode,
			ExpiresIn:               dcr.ExpiresIn,
			Interval:                dcr.Interval,
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(resp)
	})
}

// NewGitHubDevicePollHandler returns an http.Handler for POST /auth/github/device/poll.
// It polls for a token after the user has been given the user_code.
func NewGitHubDevicePollHandler(cfg GitHubAuthConfig) http.Handler {
	oauth := github.NewOAuth(github.OAuthConfig{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
	}, nil)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req GitHubDevicePollRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if req.DeviceCode == "" {
			http.Error(w, "device_code is required", http.StatusBadRequest)
			return
		}

		tokens, err := oauth.PollDeviceToken(r.Context(), req.DeviceCode)
		if err != nil {
			// Check if it's a device auth error (pending, slow_down, expired, denied).
			// errors.As (not a bare type assertion) so a wrapped error is still matched.
			var dae *github.DeviceAuthError
			if errors.As(err, &dae) {
				resp := GitHubDevicePollPendingResponse{
					Error:            dae.Code,
					ErrorDescription: dae.Description,
				}
				if dae.IsSlowDown() {
					resp.RetryAfter = 10 // Tell client to slow down
				}
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Cache-Control", "no-store")
				// Return 200 with error in body (GitHub's pattern).
				_ = json.NewEncoder(w).Encode(resp)
				return
			}
			// Other error (network, etc.).
			http.Error(w, "failed to poll for token: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// Success! Return the access token.
		resp := GitHubDevicePollResponse{
			AccessToken: tokens.AccessToken,
			TokenType:   "bearer",
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		// #nosec G117 -- this is the OAuth device-flow poll response: returning
		// the access token to the client that initiated THIS device flow is the
		// protocol's purpose. Sent once, over no-store, to its own initiator.
		_ = json.NewEncoder(w).Encode(resp)
	})
}
