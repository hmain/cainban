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
// document, picks the right MCP URL / client id / authorize host, and emits
// per-client config snippets a config writer can use verbatim.

const testCLIClientID = "testmcpcliclientid123"

func testClientCfg() ClientConfigConfig {
	return ClientConfigConfig{
		MCPURL:         testMcpAPIOrigin,
		CLIClientID:    testCLIClientID,
		HostedUIDomain: testHostedUI,
	}
}

// TestClientConfig_PublicOKValidJSON proves the endpoint is public (no auth) and
// returns a valid document carrying the flat machine-readable fields.
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
	if doc.MCPURL != testMcpAPIOrigin {
		t.Errorf("mcp_url = %q, want %q", doc.MCPURL, testMcpAPIOrigin)
	}
	if doc.ClientID != testCLIClientID {
		t.Errorf("client_id = %q, want %q", doc.ClientID, testCLIClientID)
	}
	if want := testHostedUI + "/oauth2/authorize"; doc.AuthorizationEndpoint != want {
		t.Errorf("authorization_endpoint = %q, want %q", doc.AuthorizationEndpoint, want)
	}
	if want := testHostedUI + "/oauth2/token"; doc.TokenEndpoint != want {
		t.Errorf("token_endpoint = %q, want %q", doc.TokenEndpoint, want)
	}
	if doc.RedirectURIHint != MCPOAuthRedirectURIHint {
		t.Errorf("redirect_uri_hint = %q, want %q", doc.RedirectURIHint, MCPOAuthRedirectURIHint)
	}
}

// TestClientConfig_KirocrewSnippet proves the Kiro Crew on-disk config snippet
// is complete: type=http, the url, snake_case client_id, AND the nested oauth
// block with the pinned redirect — the exact shape ~/.kiro/crew/mcp.json needs.
func TestClientConfig_KirocrewSnippet(t *testing.T) {
	doc := buildClientConfigDoc(testClientCfg())

	entry, ok := doc.Configs.Kirocrew.MCPServers["cainban"]
	if !ok {
		t.Fatalf("kirocrew config missing cainban server entry")
	}
	if entry.Type != "http" {
		t.Errorf("kirocrew type = %q, want http", entry.Type)
	}
	if entry.URL != testMcpAPIOrigin {
		t.Errorf("kirocrew url = %q, want %q", entry.URL, testMcpAPIOrigin)
	}
	if entry.ClientID != testCLIClientID {
		t.Errorf("kirocrew client_id = %q, want %q", entry.ClientID, testCLIClientID)
	}
	if entry.OAuth == nil {
		t.Fatalf("kirocrew oauth block missing")
	}
	if entry.OAuth.ClientID != testCLIClientID {
		t.Errorf("kirocrew oauth.clientId = %q, want %q", entry.OAuth.ClientID, testCLIClientID)
	}
	if entry.OAuth.RedirectURI != MCPOAuthRedirectURIHint {
		t.Errorf("kirocrew oauth.redirectUri = %q, want %q", entry.OAuth.RedirectURI, MCPOAuthRedirectURIHint)
	}
}

// TestClientConfig_DashboardSnippet proves the dashboard paste-box snippet omits
// `type` and the snake_case `client_id` (that surface rejects both with
// "unknown spec key") and carries the camelCase oauth.clientId instead.
func TestClientConfig_DashboardSnippet(t *testing.T) {
	doc := buildClientConfigDoc(testClientCfg())

	entry, ok := doc.Configs.KirocrewDashboard.MCPServers["cainban"]
	if !ok {
		t.Fatalf("kirocrew_dashboard config missing cainban server entry")
	}
	if entry.Type != "" {
		t.Errorf("dashboard type = %q, want empty (paste box rejects type)", entry.Type)
	}
	if entry.ClientID != "" {
		t.Errorf("dashboard client_id = %q, want empty (paste box rejects client_id)", entry.ClientID)
	}
	if entry.OAuth == nil || entry.OAuth.ClientID != testCLIClientID {
		t.Errorf("dashboard oauth.clientId must be %q", testCLIClientID)
	}

	// Marshal and confirm neither forbidden key appears in the JSON.
	b, _ := json.Marshal(doc.Configs.KirocrewDashboard)
	if strings.Contains(string(b), `"type"`) {
		t.Errorf("dashboard snippet must not contain a type key: %s", b)
	}
	if strings.Contains(string(b), `"client_id"`) {
		t.Errorf("dashboard snippet must not contain a client_id key: %s", b)
	}
}

// TestClientConfig_ExfilGateHost proves the Kiro Crew exfil-gate entry names the
// Cognito authorize HOST (no scheme, no path) and the /oauth2/authorize path.
func TestClientConfig_ExfilGateHost(t *testing.T) {
	doc := buildClientConfigDoc(testClientCfg())

	eps := doc.Configs.KirocrewExfilGate.AdditionalAuthorizationEndpoints
	if len(eps) != 1 {
		t.Fatalf("exfil-gate endpoints = %d, want 1", len(eps))
	}
	wantHost := "cainban-emawiant-123456789012.auth.eu-north-1.amazoncognito.com"
	if eps[0].Host != wantHost {
		t.Errorf("exfil-gate host = %q, want %q (host only, no scheme/path)", eps[0].Host, wantHost)
	}
	if eps[0].Path != "/oauth2/authorize" {
		t.Errorf("exfil-gate path = %q, want /oauth2/authorize", eps[0].Path)
	}
}

// TestClientConfig_ClaudeCommand proves the one-line claude command is complete
// and correct.
func TestClientConfig_ClaudeCommand(t *testing.T) {
	doc := buildClientConfigDoc(testClientCfg())
	want := "claude mcp add --transport http --client-id " + testCLIClientID + " cainban " + testMcpAPIOrigin
	if doc.Configs.ClaudeCodeCommand != want {
		t.Errorf("claude_code_command = %q, want %q", doc.Configs.ClaudeCodeCommand, want)
	}
}

// TestClientConfig_TrimsTrailingSlash proves the MCP URL is canonicalized (no
// trailing slash) even when env carries one — the mcpAPI.Url() CDK output ends
// with a slash, so this is the real input.
func TestClientConfig_TrimsTrailingSlash(t *testing.T) {
	doc := buildClientConfigDoc(ClientConfigConfig{
		MCPURL:         testMcpAPIOrigin + "/",
		CLIClientID:    testCLIClientID,
		HostedUIDomain: testHostedUI,
	})
	if doc.MCPURL != testMcpAPIOrigin {
		t.Errorf("mcp_url = %q, want %q (trailing slash trimmed)", doc.MCPURL, testMcpAPIOrigin)
	}
	if entry := doc.Configs.Kirocrew.MCPServers["cainban"]; entry.URL != testMcpAPIOrigin {
		t.Errorf("kirocrew url = %q, want %q (trailing slash trimmed)", entry.URL, testMcpAPIOrigin)
	}
}

// TestClientConfig_DegradesWithoutHostedUI proves the document is still valid
// (no authorize endpoint, empty exfil-gate list, not nil) when the Hosted UI
// domain is unset — the endpoint must never panic on partial config.
func TestClientConfig_DegradesWithoutHostedUI(t *testing.T) {
	doc := buildClientConfigDoc(ClientConfigConfig{
		MCPURL:      testMcpAPIOrigin,
		CLIClientID: testCLIClientID,
		// HostedUIDomain intentionally empty.
	})
	if doc.AuthorizationEndpoint != "" {
		t.Errorf("authorization_endpoint = %q, want empty without hosted UI", doc.AuthorizationEndpoint)
	}
	if doc.Configs.KirocrewExfilGate.AdditionalAuthorizationEndpoints == nil {
		t.Errorf("exfil-gate list must be a non-nil empty slice, not null, for a stable JSON shape")
	}
	if len(doc.Configs.KirocrewExfilGate.AdditionalAuthorizationEndpoints) != 0 {
		t.Errorf("exfil-gate list should be empty without a known authorize host")
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
