package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newClientConfigServer stands up a test server that serves the public
// discovery documents the connect-agent flow reads (the RFC 9728
// protected-resource doc + the client-config doc), so DiscoverClientConfig can
// be exercised end to end against a real HTTP endpoint. The RFC 8414 auth-server
// shim was removed in the GitHub-only migration, so it is not served here.
func newClientConfigServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle(WellKnownProtectedResourcePath, NewResourceMetadataHandler(testResourceCfg()))
	srv := httptest.NewServer(mux)
	// Re-register client-config now that we know the server URL, so mcp_url
	// matches the real origin a client fetched from.
	mux.Handle(WellKnownClientConfigPath, NewClientConfigHandler(ClientConfigConfig{
		MCPURL: srv.URL,
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestDiscoverClientConfig_HappyPath proves discovery against a real MCP server
// resolves and returns the MCP URL.
func TestDiscoverClientConfig_HappyPath(t *testing.T) {
	srv := newClientConfigServer(t)

	got, err := DiscoverClientConfig(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("DiscoverClientConfig: %v", err)
	}
	if got.MCPURL != srv.URL {
		t.Errorf("MCPURL = %q, want %q", got.MCPURL, srv.URL)
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

	jsonFormats := []string{"json", "kirocrew", "kirocrew-dashboard", "kiro-ide", "cursor", "vscode"}
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

// TestRenderConfig_StdioFormat proves the stdio format emits a valid MCP client
// config for local stdio transport with env-based credentials.
func TestRenderConfig_StdioFormat(t *testing.T) {
	srv := newClientConfigServer(t)
	cfg, err := DiscoverClientConfig(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}

	out, err := cfg.RenderConfig("stdio")
	if err != nil {
		t.Fatalf("RenderConfig(stdio): %v", err)
	}

	// Must be valid JSON
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("RenderConfig(stdio) is not valid JSON: %v", err)
	}

	// Must have mcpServers.cainban
	servers, ok := doc["mcpServers"].(map[string]any)
	if !ok {
		t.Fatal("missing mcpServers key")
	}
	cainban, ok := servers["cainban"].(map[string]any)
	if !ok {
		t.Fatal("missing mcpServers.cainban")
	}

	// stdio format must have command, args, and env (NOT url or oauth)
	if _, hasCommand := cainban["command"]; !hasCommand {
		t.Error("stdio format missing 'command' field")
	}
	if _, hasArgs := cainban["args"]; !hasArgs {
		t.Error("stdio format missing 'args' field")
	}
	if _, hasEnv := cainban["env"]; !hasEnv {
		t.Error("stdio format missing 'env' field")
	}
	if _, hasURL := cainban["url"]; hasURL {
		t.Error("stdio format should NOT have 'url' field (that's HTTP)")
	}
	if _, hasOAuth := cainban["oauth"]; hasOAuth {
		t.Error("stdio format should NOT have 'oauth' field")
	}

	// args should include "mcp"
	args, ok := cainban["args"].([]any)
	if !ok || len(args) == 0 {
		t.Fatal("args should be a non-empty array")
	}
	if args[0] != "mcp" {
		t.Errorf("args[0] = %v, want 'mcp'", args[0])
	}

	// env should have CAINBAN_REPO placeholder
	env, ok := cainban["env"].(map[string]any)
	if !ok {
		t.Fatal("env should be an object")
	}
	if _, hasRepo := env["CAINBAN_REPO"]; !hasRepo {
		t.Error("env missing CAINBAN_REPO")
	}
}
