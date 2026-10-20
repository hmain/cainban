package github

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestStartDeviceFlow_Success(t *testing.T) {
	rt := &oauthRoute{
		responses: map[string]*http.Response{
			"login/device/code": {
				StatusCode: 200,
				Body:       io.NopCloser(strings.NewReader(`{"device_code":"dc123","user_code":"ABCD-1234","verification_uri":"https://github.com/login/device","expires_in":900,"interval":5}`)),
			},
		},
	}
	o := NewOAuth(OAuthConfig{ClientID: "test-client"}, rt.doer())

	dcr, err := o.StartDeviceFlow(context.Background(), "user:email read:user repo")
	if err != nil {
		t.Fatalf("StartDeviceFlow failed: %v", err)
	}
	if dcr.DeviceCode != "dc123" {
		t.Errorf("DeviceCode = %q, want %q", dcr.DeviceCode, "dc123")
	}
	if dcr.UserCode != "ABCD-1234" {
		t.Errorf("UserCode = %q, want %q", dcr.UserCode, "ABCD-1234")
	}
	if dcr.VerificationURI != "https://github.com/login/device" {
		t.Errorf("VerificationURI = %q, want %q", dcr.VerificationURI, "https://github.com/login/device")
	}
	if dcr.ExpiresIn != 900 {
		t.Errorf("ExpiresIn = %d, want %d", dcr.ExpiresIn, 900)
	}
	if dcr.Interval != 5 {
		t.Errorf("Interval = %d, want %d", dcr.Interval, 5)
	}
}

func TestStartDeviceFlow_MissingClientID(t *testing.T) {
	o := NewOAuth(OAuthConfig{}, nil)
	_, err := o.StartDeviceFlow(context.Background(), "")
	if err == nil || !strings.Contains(err.Error(), "client id not configured") {
		t.Errorf("expected client id error, got: %v", err)
	}
}

func TestPollDeviceToken_Success(t *testing.T) {
	rt := &oauthRoute{
		responses: map[string]*http.Response{
			"access_token": {
				StatusCode: 200,
				Body:       io.NopCloser(strings.NewReader(`{"access_token":"gho_abc123","token_type":"bearer","scope":"user:email read:user repo"}`)),
			},
		},
	}
	o := NewOAuth(OAuthConfig{ClientID: "test-client"}, rt.doer())

	tokens, err := o.PollDeviceToken(context.Background(), "dc123")
	if err != nil {
		t.Fatalf("PollDeviceToken failed: %v", err)
	}
	if tokens.AccessToken != "gho_abc123" {
		t.Errorf("AccessToken = %q, want %q", tokens.AccessToken, "gho_abc123")
	}
}

func TestPollDeviceToken_AuthorizationPending(t *testing.T) {
	rt := &oauthRoute{
		responses: map[string]*http.Response{
			"access_token": {
				StatusCode: 200,
				Body:       io.NopCloser(strings.NewReader(`{"error":"authorization_pending","error_description":"The user has not yet entered the code"}`)),
			},
		},
	}
	o := NewOAuth(OAuthConfig{ClientID: "test-client"}, rt.doer())

	_, err := o.PollDeviceToken(context.Background(), "dc123")
	var dae *DeviceAuthError
	if !errors.As(err, &dae) {
		t.Fatalf("expected DeviceAuthError, got: %T %v", err, err)
	}
	if !dae.IsAuthorizationPending() {
		t.Errorf("expected IsAuthorizationPending, got code=%q", dae.Code)
	}
}

func TestPollDeviceToken_SlowDown(t *testing.T) {
	rt := &oauthRoute{
		responses: map[string]*http.Response{
			"access_token": {
				StatusCode: 200,
				Body:       io.NopCloser(strings.NewReader(`{"error":"slow_down","error_description":"Too many requests"}`)),
			},
		},
	}
	o := NewOAuth(OAuthConfig{ClientID: "test-client"}, rt.doer())

	_, err := o.PollDeviceToken(context.Background(), "dc123")
	var dae *DeviceAuthError
	if !errors.As(err, &dae) {
		t.Fatalf("expected DeviceAuthError, got: %T %v", err, err)
	}
	if !dae.IsSlowDown() {
		t.Errorf("expected IsSlowDown, got code=%q", dae.Code)
	}
}

func TestPollDeviceToken_Expired(t *testing.T) {
	rt := &oauthRoute{
		responses: map[string]*http.Response{
			"access_token": {
				StatusCode: 200,
				Body:       io.NopCloser(strings.NewReader(`{"error":"expired_token","error_description":"The device code has expired"}`)),
			},
		},
	}
	o := NewOAuth(OAuthConfig{ClientID: "test-client"}, rt.doer())

	_, err := o.PollDeviceToken(context.Background(), "dc123")
	var dae *DeviceAuthError
	if !errors.As(err, &dae) {
		t.Fatalf("expected DeviceAuthError, got: %T %v", err, err)
	}
	if !dae.IsExpired() {
		t.Errorf("expected IsExpired, got code=%q", dae.Code)
	}
}

func TestPollDeviceToken_AccessDenied(t *testing.T) {
	rt := &oauthRoute{
		responses: map[string]*http.Response{
			"access_token": {
				StatusCode: 200,
				Body:       io.NopCloser(strings.NewReader(`{"error":"access_denied","error_description":"The user denied the request"}`)),
			},
		},
	}
	o := NewOAuth(OAuthConfig{ClientID: "test-client"}, rt.doer())

	_, err := o.PollDeviceToken(context.Background(), "dc123")
	var dae *DeviceAuthError
	if !errors.As(err, &dae) {
		t.Fatalf("expected DeviceAuthError, got: %T %v", err, err)
	}
	if !dae.IsAccessDenied() {
		t.Errorf("expected IsAccessDenied, got code=%q", dae.Code)
	}
}

func TestPollDeviceToken_EmptyDeviceCode(t *testing.T) {
	o := NewOAuth(OAuthConfig{ClientID: "test-client"}, nil)
	_, err := o.PollDeviceToken(context.Background(), "")
	if err == nil || !strings.Contains(err.Error(), "empty device code") {
		t.Errorf("expected empty device code error, got: %v", err)
	}
}
