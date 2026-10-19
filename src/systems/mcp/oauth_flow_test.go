package mcp

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// TestOAuthFlowServerStartsOnConfiguredPort verifies the server starts and
// listens on the expected callback port.
func TestOAuthFlowServerStartsOnConfiguredPort(t *testing.T) {
	// Find an available port
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to find free port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	cfg := OAuthFlowConfig{
		CallbackPort:     port,
		CallbackPath:     "/oauth/callback",
		AuthorizeURL:     "https://example.com/authorize",
		TokenURL:         "https://example.com/token",
		ClientID:         "test-client",
		SkipBrowserOpen:  true, // Don't open browser in tests
		ShutdownTimeout:  100 * time.Millisecond,
		PageServeTimeout: 100 * time.Millisecond,
	}

	flow := NewOAuthFlow(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Start in goroutine since it blocks until callback
	errCh := make(chan error, 1)
	go func() {
		_, err := flow.Run(ctx)
		errCh <- err
	}()

	// Wait for server to start
	time.Sleep(50 * time.Millisecond)

	// Verify server is listening
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/health", port))
	if err != nil {
		t.Fatalf("server not listening: %v", err)
	}
	resp.Body.Close()

	cancel() // Stop the flow
	<-errCh
}

// TestOAuthFlowCallbackServesSuccessPage verifies the callback endpoint serves
// the styled success page after receiving the code.
func TestOAuthFlowCallbackServesSuccessPage(t *testing.T) {
	// Mock token server
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_token":"test-token","token_type":"Bearer","expires_in":3600}`))
	}))
	defer tokenServer.Close()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to find free port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	cfg := OAuthFlowConfig{
		CallbackPort:     port,
		CallbackPath:     "/oauth/callback",
		AuthorizeURL:     "https://example.com/authorize",
		TokenURL:         tokenServer.URL,
		ClientID:         "test-client",
		SkipBrowserOpen:  true,
		ShutdownTimeout:  500 * time.Millisecond,
		PageServeTimeout: 500 * time.Millisecond,
	}

	flow := NewOAuthFlow(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Start flow in background
	resultCh := make(chan *OAuthFlowResult, 1)
	errCh := make(chan error, 1)
	go func() {
		result, err := flow.Run(ctx)
		resultCh <- result
		errCh <- err
	}()

	// Wait for server to start
	time.Sleep(100 * time.Millisecond)

	// Simulate OAuth callback with code
	callbackURL := fmt.Sprintf("http://127.0.0.1:%d/oauth/callback?code=test-code&state=%s",
		port, flow.State())
	resp, err := http.Get(callbackURL)
	if err != nil {
		t.Fatalf("callback request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("callback status = %d, want 200", resp.StatusCode)
	}

	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}

	// Wait for flow to complete
	select {
	case result := <-resultCh:
		if result == nil {
			t.Error("expected non-nil result")
		} else if result.AccessToken != "test-token" {
			t.Errorf("access_token = %q, want test-token", result.AccessToken)
		}
	case <-time.After(2 * time.Second):
		t.Error("flow did not complete in time")
	}
}

// TestOAuthFlowRejectsInvalidState verifies state mismatch is rejected.
func TestOAuthFlowRejectsInvalidState(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to find free port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	cfg := OAuthFlowConfig{
		CallbackPort:     port,
		CallbackPath:     "/oauth/callback",
		AuthorizeURL:     "https://example.com/authorize",
		TokenURL:         "https://example.com/token",
		ClientID:         "test-client",
		SkipBrowserOpen:  true,
		ShutdownTimeout:  100 * time.Millisecond,
		PageServeTimeout: 100 * time.Millisecond,
	}

	flow := NewOAuthFlow(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	go func() {
		flow.Run(ctx)
	}()

	// Wait for server to start
	time.Sleep(50 * time.Millisecond)

	// Simulate callback with WRONG state
	callbackURL := fmt.Sprintf("http://127.0.0.1:%d/oauth/callback?code=test-code&state=wrong-state", port)
	resp, err := http.Get(callbackURL)
	if err != nil {
		t.Fatalf("callback request failed: %v", err)
	}
	resp.Body.Close()

	// Should reject with 400
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for invalid state", resp.StatusCode)
	}
}

// TestOAuthFlowGeneratesAuthorizeURL verifies the authorize URL is correctly
// constructed with all required OAuth parameters.
func TestOAuthFlowGeneratesAuthorizeURL(t *testing.T) {
	cfg := OAuthFlowConfig{
		CallbackPort:    3334,
		CallbackPath:    "/oauth/callback",
		AuthorizeURL:    "https://auth.example.com/authorize",
		TokenURL:        "https://auth.example.com/token",
		ClientID:        "my-client-id",
		Scopes:          []string{"openid", "email"},
		SkipBrowserOpen: true,
	}

	flow := NewOAuthFlow(cfg)
	authURL := flow.AuthorizeURL()

	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("failed to parse authorize URL: %v", err)
	}

	// Check base URL
	if parsed.Scheme != "https" || parsed.Host != "auth.example.com" || parsed.Path != "/authorize" {
		t.Errorf("unexpected base URL: %s", authURL)
	}

	// Check required parameters
	q := parsed.Query()
	if q.Get("response_type") != "code" {
		t.Errorf("response_type = %q, want code", q.Get("response_type"))
	}
	if q.Get("client_id") != "my-client-id" {
		t.Errorf("client_id = %q, want my-client-id", q.Get("client_id"))
	}
	if q.Get("redirect_uri") != "http://127.0.0.1:3334/oauth/callback" {
		t.Errorf("redirect_uri = %q, want http://127.0.0.1:3334/oauth/callback", q.Get("redirect_uri"))
	}
	if q.Get("scope") != "openid email" {
		t.Errorf("scope = %q, want 'openid email'", q.Get("scope"))
	}
	if q.Get("state") == "" {
		t.Error("state should not be empty")
	}

	// PKCE parameters
	if q.Get("code_challenge") == "" {
		t.Error("code_challenge should not be empty (PKCE)")
	}
	if q.Get("code_challenge_method") != "S256" {
		t.Errorf("code_challenge_method = %q, want S256", q.Get("code_challenge_method"))
	}
}

// TestOAuthFlowHandlesOAuthError verifies OAuth error responses are handled.
func TestOAuthFlowHandlesOAuthError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to find free port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	cfg := OAuthFlowConfig{
		CallbackPort:     port,
		CallbackPath:     "/oauth/callback",
		AuthorizeURL:     "https://example.com/authorize",
		TokenURL:         "https://example.com/token",
		ClientID:         "test-client",
		SkipBrowserOpen:  true,
		ShutdownTimeout:  100 * time.Millisecond,
		PageServeTimeout: 100 * time.Millisecond,
	}

	flow := NewOAuthFlow(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		_, err := flow.Run(ctx)
		errCh <- err
	}()

	// Wait for server to start
	time.Sleep(50 * time.Millisecond)

	// Simulate OAuth error callback
	callbackURL := fmt.Sprintf("http://127.0.0.1:%d/oauth/callback?error=access_denied&error_description=User+denied+access", port)
	resp, err := http.Get(callbackURL)
	if err != nil {
		t.Fatalf("callback request failed: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for OAuth error", resp.StatusCode)
	}

	// Flow should complete with error
	select {
	case flowErr := <-errCh:
		if flowErr == nil {
			t.Error("expected error from flow")
		}
		if !strings.Contains(flowErr.Error(), "access_denied") {
			t.Errorf("error = %v, want to contain 'access_denied'", flowErr)
		}
	case <-time.After(2 * time.Second):
		t.Error("flow did not complete in time")
	}
}

// TestOAuthFlowHealthEndpoint verifies /health returns 200 when server is up.
func TestOAuthFlowHealthEndpoint(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to find free port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	cfg := OAuthFlowConfig{
		CallbackPort:     port,
		CallbackPath:     "/oauth/callback",
		AuthorizeURL:     "https://example.com/authorize",
		TokenURL:         "https://example.com/token",
		ClientID:         "test-client",
		SkipBrowserOpen:  true,
		ShutdownTimeout:  100 * time.Millisecond,
		PageServeTimeout: 100 * time.Millisecond,
	}

	flow := NewOAuthFlow(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	go func() {
		flow.Run(ctx)
	}()

	// Wait for server to start
	time.Sleep(50 * time.Millisecond)

	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/health", port))
	if err != nil {
		t.Fatalf("health check failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("health status = %d, want 200", resp.StatusCode)
	}
}
