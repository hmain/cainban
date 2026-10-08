package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newClientConfigServer stands up a test server that serves the three public
// discovery documents the connect-agent flow reads, so DiscoverClientConfig can
// be exercised end to end against a real HTTP endpoint.
func newClientConfigServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle(WellKnownProtectedResourcePath, NewResourceMetadataHandler(testResourceCfg()))
	mux.Handle(WellKnownAuthServerPath, NewAuthServerMetadataHandler(testAuthServerCfg()))
	// The served client-config must advertise the SERVER's own URL as mcp_url,
	// which the test rewrites once the server is up (below).
	srv := httptest.NewServer(mux)
	// Re-register client-config now that we know the server URL, so mcp_url
	// matches the real origin a client fetched from.
	mux.Handle(WellKnownClientConfigPath, NewClientConfigHandler(ClientConfigConfig{
		MCPURL:         srv.URL,
		CLIClientID:    testCLIClientID,
		HostedUIDomain: testHostedUI,
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestDiscoverClientConfig_HappyPath proves discovery against a real MCP server
// resolves and returns the client id + endpoints.
func TestDiscoverClientConfig_HappyPath(t *testing.T) {
	srv := newClientConfigServer(t)

	got, err := DiscoverClientConfig(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("DiscoverClientConfig: %v", err)
	}
	if got.ClientID != testCLIClientID {
		t.Errorf("ClientID = %q, want %q", got.ClientID, testCLIClientID)
	}
	if got.MCPURL != srv.URL {
		t.Errorf("MCPURL = %q, want %q", got.MCPURL, srv.URL)
	}
	if want := testHostedUI + "/oauth2/authorize"; got.AuthorizationEndpoint != want {
		t.Errorf("AuthorizationEndpoint = %q, want %q", got.AuthorizationEndpoint, want)
	}
}

// TestDiscoverClientConfig_WrongURLRejected proves pointing at a server that
// does NOT serve the RFC 9728 document (the Connect-API-URL mistake) fails with
// a message that names the likely cause, rather than writing a broken config.
func TestDiscoverClientConfig_WrongURLRejected(t *testing.T) {
	// A server that 404s everything — stands in for the Connect API.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := DiscoverClientConfig(context.Background(), srv.URL)
	if err == nil {
		t.Fatalf("expected an error for a non-MCP URL, got nil")
	}
	if !strings.Contains(err.Error(), "not a cainban MCP server") {
		t.Errorf("error should name the likely cause, got: %v", err)
	}
	if !strings.Contains(err.Error(), "Connect API URL") {
		t.Errorf("error should hint at the Connect-API-URL mistake, got: %v", err)
	}
}

// TestRenderConfig_Formats proves each format renders without error and the
// file-based formats produce parseable JSON with the cainban server.
func TestRenderConfig_Formats(t *testing.T) {
	srv := newClientConfigServer(t)
	cfg, err := DiscoverClientConfig(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}

	jsonFormats := []string{"json", "kirocrew", "kirocrew-exfil-gate", "kirocrew-dashboard", "kiro-ide", "cursor", "vscode"}
	for _, f := range jsonFormats {
		out, err := cfg.RenderConfig(f)
		if err != nil {
			t.Errorf("RenderConfig(%q): %v", f, err)
			continue
		}
		var probe any
		if err := json.Unmarshal([]byte(out), &probe); err != nil {
			t.Errorf("RenderConfig(%q) is not valid JSON: %v", f, err)
		}
	}

	// claude is a shell command, not JSON.
	claude, err := cfg.RenderConfig("claude")
	if err != nil {
		t.Fatalf("RenderConfig(claude): %v", err)
	}
	if !strings.HasPrefix(claude, "claude mcp add") {
		t.Errorf("claude command = %q, want it to start with 'claude mcp add'", claude)
	}

	// An unknown format is a clear error, not a silent empty string.
	if _, err := cfg.RenderConfig("emacs"); err == nil {
		t.Errorf("expected an error for an unknown format")
	}
}
