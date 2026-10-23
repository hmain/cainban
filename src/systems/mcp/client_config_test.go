package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Tests for the cainban MCP client auto-config endpoint
// (/.well-known/mcp-client-config): it is PUBLIC, returns a complete, valid
// document carrying the MCP URL and per-client server snippets. Under
// GitHub-only auth there are no OAuth fields (no client id, authorize/token
// endpoints, redirect hint, or exfil-gate) — the client supplies its GitHub
// token as the bearer out of band.

const testMcpURL = "https://abc123.execute-api.eu-north-1.amazonaws.com"

func testClientCfg() ClientConfigConfig {
	return ClientConfigConfig{MCPURL: testMcpURL}
}

// TestClientConfig_PublicOKValidJSON proves the endpoint is public (no auth) and
// returns a valid document carrying the MCP URL.
func TestClientConfig_PublicOKValidJSON(t *testing.T) {
	h := NewClientConfigHandler(testClientCfg())

	req := httptest.NewRequest(http.MethodGet, WellKnownClientConfigPath, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req) // no Authorization header

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (must be public, no auth)", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	if ao := rec.Header().Get("Access-Control-Allow-Origin"); ao != "*" {
		t.Errorf("Access-Control-Allow-Origin = %q, want * (browser connect page fetches it)", ao)
	}

	var doc clientConfigDoc
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("body is not valid JSON: %v (body=%q)", err, rec.Body.String())
	}
	if doc.MCPURL != testMcpURL {
		t.Errorf("mcp_url = %q, want %q", doc.MCPURL, testMcpURL)
	}
}

// TestClientConfig_KirocrewSnippet proves the Kiro Crew on-disk config snippet
// is the GitHub-only shape: url only with NO oauth block (the client sends its
// GitHub token as the bearer). Kiro infers HTTP transport from `url` and
// REJECTS a `type` field, so the Kiro Crew / Kiro IDE entries must NOT carry
// one.
func TestClientConfig_KirocrewSnippet(t *testing.T) {
	doc := buildClientConfigDoc(testClientCfg())

	for _, which := range []struct {
		name string
		doc  mcpServersDoc
	}{
		{"kirocrew", doc.Configs.Kirocrew},
		{"kiro_ide", doc.Configs.KiroIDE},
	} {
		entry, ok := which.doc.MCPServers["cainban"]
		if !ok {
			t.Fatalf("%s config missing cainban server entry", which.name)
		}
		if entry.Type != "" {
			t.Errorf("%s type = %q, want empty (Kiro rejects a type field)", which.name, entry.Type)
		}
		if entry.URL != testMcpURL {
			t.Errorf("%s url = %q, want %q", which.name, entry.URL, testMcpURL)
		}
		// Belt and suspenders: the marshaled Kiro snippet must have no `type` key.
		b, _ := json.Marshal(which.doc)
		if strings.Contains(string(b), `"type"`) {
			t.Errorf("%s snippet must not contain a type key: %s", which.name, b)
		}
	}
}

// TestClientConfig_CursorVSCodeHaveType proves Cursor and VS Code DO carry
// `"type": "http"` — unlike Kiro, those clients expect it.
func TestClientConfig_CursorVSCodeHaveType(t *testing.T) {
	doc := buildClientConfigDoc(testClientCfg())
	for _, which := range []struct {
		name string
		doc  mcpServersDoc
	}{
		{"cursor", doc.Configs.Cursor},
		{"vscode", doc.Configs.VSCode},
	} {
		entry := which.doc.MCPServers["cainban"]
		if entry.Type != "http" {
			t.Errorf("%s type = %q, want http", which.name, entry.Type)
		}
		if entry.URL != testMcpURL {
			t.Errorf("%s url = %q, want %q", which.name, entry.URL, testMcpURL)
		}
	}
}

// TestClientConfig_DashboardSnippet proves the dashboard paste-box snippet omits
// `type` (that surface rejects it) and carries just the url.
func TestClientConfig_DashboardSnippet(t *testing.T) {
	doc := buildClientConfigDoc(testClientCfg())

	entry, ok := doc.Configs.KirocrewDashboard.MCPServers["cainban"]
	if !ok {
		t.Fatalf("kirocrew_dashboard config missing cainban server entry")
	}
	if entry.Type != "" {
		t.Errorf("dashboard type = %q, want empty (paste box rejects type)", entry.Type)
	}
	if entry.URL != testMcpURL {
		t.Errorf("dashboard url = %q, want %q", entry.URL, testMcpURL)
	}

	// Marshal and confirm the forbidden `type` key does not appear.
	b, _ := json.Marshal(doc.Configs.KirocrewDashboard)
	if strings.Contains(string(b), `"type"`) {
		t.Errorf("dashboard snippet must not contain a type key: %s", b)
	}
}

// TestClientConfig_ClaudeCommand proves the one-line claude command is complete
// (GitHub-only: no --client-id).
func TestClientConfig_ClaudeCommand(t *testing.T) {
	doc := buildClientConfigDoc(testClientCfg())
	want := "claude mcp add --transport http cainban " + testMcpURL
	if doc.Configs.ClaudeCodeCommand != want {
		t.Errorf("claude_code_command = %q, want %q", doc.Configs.ClaudeCodeCommand, want)
	}
}

// TestClientConfig_TrimsTrailingSlash proves the MCP URL is canonicalized (no
// trailing slash) even when env carries one — mcpAPI.Url() ends with a slash.
func TestClientConfig_TrimsTrailingSlash(t *testing.T) {
	doc := buildClientConfigDoc(ClientConfigConfig{MCPURL: testMcpURL + "/"})
	if doc.MCPURL != testMcpURL {
		t.Errorf("mcp_url = %q, want %q (trailing slash trimmed)", doc.MCPURL, testMcpURL)
	}
	if entry := doc.Configs.Kirocrew.MCPServers["cainban"]; entry.URL != testMcpURL {
		t.Errorf("kirocrew url = %q, want %q (trailing slash trimmed)", entry.URL, testMcpURL)
	}
}

// TestClientConfig_MethodNotAllowed proves a non-GET/HEAD method is 405.
func TestClientConfig_MethodNotAllowed(t *testing.T) {
	h := NewClientConfigHandler(testClientCfg())
	req := httptest.NewRequest(http.MethodPost, WellKnownClientConfigPath, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405 for POST", rec.Code)
	}
}
