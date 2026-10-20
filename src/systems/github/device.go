// Package github provides GitHub API and OAuth functionality.
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

// deviceCodeURL is the GitHub device authorization endpoint.
var deviceCodeURL = "https://github.com/login/device/code"

// DeviceCodeResponse is returned when initiating the device flow.
// The client shows verification_uri and user_code to the user, then polls
// the token endpoint with device_code until the user completes authorization.
type DeviceCodeResponse struct {
	// DeviceCode is the code the client uses to poll for a token.
	// Never show this to the user.
	DeviceCode string `json:"device_code"`
	// UserCode is the code the user enters at VerificationURI.
	UserCode string `json:"user_code"`
	// VerificationURI is where the user goes to enter the code (github.com/login/device).
	VerificationURI string `json:"verification_uri"`
	// VerificationURIComplete is the full URL with code pre-filled (optional).
	VerificationURIComplete string `json:"verification_uri_complete,omitempty"`
	// ExpiresIn is how long the device_code is valid (seconds).
	ExpiresIn int `json:"expires_in"`
	// Interval is the minimum seconds between poll requests.
	Interval int `json:"interval"`
}

// DeviceAuthError represents an error from the device flow.
type DeviceAuthError struct {
	Code        string // authorization_pending, slow_down, expired_token, access_denied, etc.
	Description string
}

func (e *DeviceAuthError) Error() string {
	if e.Description != "" {
		return fmt.Sprintf("github device auth: %s: %s", e.Code, e.Description)
	}
	return fmt.Sprintf("github device auth: %s", e.Code)
}

// IsAuthorizationPending returns true if the user hasn't completed auth yet.
func (e *DeviceAuthError) IsAuthorizationPending() bool {
	return e.Code == "authorization_pending"
}

// IsSlowDown returns true if the client is polling too fast.
func (e *DeviceAuthError) IsSlowDown() bool {
	return e.Code == "slow_down"
}

// IsExpired returns true if the device code has expired.
func (e *DeviceAuthError) IsExpired() bool {
	return e.Code == "expired_token"
}

// IsAccessDenied returns true if the user denied the request.
func (e *DeviceAuthError) IsAccessDenied() bool {
	return e.Code == "access_denied"
}

// StartDeviceFlow initiates the GitHub device authorization flow.
// Returns the device code response containing the user_code and verification_uri
// that the client should display to the user.
//
// The scope parameter specifies what permissions to request. For cainban,
// use "user:email read:user repo" to enable repo permission checks.
func (o *OAuth) StartDeviceFlow(ctx context.Context, scope string) (*DeviceCodeResponse, error) {
	if strings.TrimSpace(o.cfg.ClientID) == "" {
		return nil, errors.New("github: device flow: client id not configured")
	}

	form := url.Values{}
	form.Set("client_id", o.cfg.ClientID)
	if strings.TrimSpace(scope) != "" {
		form.Set("scope", scope)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, deviceCodeURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("github: device flow: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := o.doer.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github: device flow: request: %w", err)
	}
	body := drain(resp)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github: device flow: unexpected status %d: %s", resp.StatusCode, snippet(body))
	}

	var out DeviceCodeResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("github: device flow: decode response: %w", err)
	}
	if out.DeviceCode == "" || out.UserCode == "" {
		return nil, errors.New("github: device flow: response missing device_code or user_code")
	}
	// Default interval if not provided (GitHub typically returns 5).
	if out.Interval == 0 {
		out.Interval = 5
	}
	return &out, nil
}

// PollDeviceToken polls for a token after the user has been given the user_code.
// Returns the token when the user completes authorization.
//
// If the user hasn't completed auth yet, returns a *DeviceAuthError with
// IsAuthorizationPending() == true. The client should wait `interval` seconds
// and try again.
//
// If the user denied the request or the code expired, returns a *DeviceAuthError
// with IsAccessDenied() or IsExpired() == true. The client should stop polling.
func (o *OAuth) PollDeviceToken(ctx context.Context, deviceCode string) (*UserTokens, error) {
	if strings.TrimSpace(deviceCode) == "" {
		return nil, errors.New("github: device flow: empty device code")
	}
	if strings.TrimSpace(o.cfg.ClientID) == "" {
		return nil, errors.New("github: device flow: client id not configured")
	}

	form := url.Values{}
	form.Set("client_id", o.cfg.ClientID)
	form.Set("device_code", deviceCode)
	form.Set("grant_type", "urn:ietf:params:oauth:grant-type:device_code")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, oauthTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("github: device flow: build token request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := o.doer.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github: device flow: token request: %w", err)
	}
	body := drain(resp)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github: device flow: unexpected status %d: %s", resp.StatusCode, snippet(body))
	}

	// Parse the response. GitHub returns 200 with either an access_token or an error.
	var out struct {
		AccessToken           string `json:"access_token"`
		TokenType             string `json:"token_type"`
		RefreshToken          string `json:"refresh_token"`
		ExpiresIn             int    `json:"expires_in"`
		RefreshTokenExpiresIn int    `json:"refresh_token_expires_in"`
		Error                 string `json:"error"`
		ErrorDescription      string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("github: device flow: decode token response: %w", err)
	}

	// Check for error response (authorization_pending, slow_down, expired_token, access_denied).
	if out.Error != "" {
		return nil, &DeviceAuthError{Code: out.Error, Description: out.ErrorDescription}
	}

	// Success: we got an access token.
	if strings.TrimSpace(out.AccessToken) == "" {
		return nil, errors.New("github: device flow: token response had empty access_token")
	}
	return &UserTokens{
		AccessToken:           out.AccessToken,
		RefreshToken:          out.RefreshToken,
		ExpiresIn:             out.ExpiresIn,
		RefreshTokenExpiresIn: out.RefreshTokenExpiresIn,
	}, nil
}

// DeviceFlow is a high-level helper that runs the complete device flow:
// 1. Starts the flow and returns the user instructions
// 2. Polls until the user completes auth (or timeout/denial)
//
// The onStart callback receives the device code response so the caller can
// display the user_code and verification_uri to the user.
//
// This is a BLOCKING call that runs until success, timeout, or denial.
// For non-blocking use, call StartDeviceFlow and PollDeviceToken directly.
func (o *OAuth) DeviceFlow(ctx context.Context, scope string, onStart func(*DeviceCodeResponse)) (*UserTokens, error) {
	dcr, err := o.StartDeviceFlow(ctx, scope)
	if err != nil {
		return nil, err
	}
	if onStart != nil {
		onStart(dcr)
	}

	interval := time.Duration(dcr.Interval) * time.Second
	deadline := time.Now().Add(time.Duration(dcr.ExpiresIn) * time.Second)

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(interval):
		}

		if time.Now().After(deadline) {
			return nil, &DeviceAuthError{Code: "expired_token", Description: "device code expired"}
		}

		tokens, err := o.PollDeviceToken(ctx, dcr.DeviceCode)
		if err == nil {
			return tokens, nil
		}

		var dae *DeviceAuthError
		if errors.As(err, &dae) {
			if dae.IsAuthorizationPending() {
				continue // Keep polling
			}
			if dae.IsSlowDown() {
				interval += 5 * time.Second // GitHub asks us to slow down
				continue
			}
			// Expired, denied, or other terminal error
			return nil, err
		}
		// Unexpected error
		return nil, err
	}
}
