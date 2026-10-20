// Package mcp provides the MCP HTTP handlers and transport.
package mcp

import (
	"encoding/json"
	"net/http"

	"github.com/hmain/cainban/src/systems/github"
)

// GitHubAuthConfig carries the GitHub OAuth credentials for the device flow.
type GitHubAuthConfig struct {
	ClientID     string
	ClientSecret string
}

// GitHubDeviceStartRequest is the request body for POST /auth/github/device.
type GitHubDeviceStartRequest struct {
	// Scope is optional; defaults to "user:email read:user repo" for cainban.
	Scope string `json:"scope,omitempty"`
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

		// Default scope for cainban: user identity + repo access.
		scope := req.Scope
		if scope == "" {
			scope = "user:email read:user repo"
		}

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
		json.NewEncoder(w).Encode(resp)
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
			if dae, ok := err.(*github.DeviceAuthError); ok {
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
				json.NewEncoder(w).Encode(resp)
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
		json.NewEncoder(w).Encode(resp)
	})
}
