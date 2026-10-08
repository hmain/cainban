package mcp

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
)

// WellKnownClientConfigPath is cainban's own (non-RFC) MCP client auto-config
// discovery path. Unlike the RFC 9728 / RFC 8414 documents it is NOT a standard
// discovery endpoint — it is a convenience document that returns EVERYTHING an
// MCP client (or an AI agent setting one up) needs to configure itself in one
// fetch: the MCP URL, the public OAuth client id, the resolved authorize/token
// endpoints, AND pre-computed, copy-paste config snippets for each supported
// client (Kiro Crew, Claude Code, Kiro IDE, Cursor, VS Code).
//
// # Why this exists
//
// Setting cainban up by hand is error-prone: there are three similar-looking
// API Gateway URLs (MCP API, Connect API, Cognito Hosted UI) that users confuse,
// the OAuth client id lives only in CloudFormation outputs / the connect page,
// and Kiro Crew additionally needs its exfil-gate allowlist extended with the
// authorize host. This endpoint collapses all of that into one GET: the connect
// page renders from it (one source of truth, no client-side snippet drift), and
// an agent can fetch it and write its own config without the user hand-editing
// JSON. It is PUBLIC (no token) because configuration must be discoverable
// before the client holds any credential — exactly like the RFC discovery docs.
//
// Nothing here is a secret: the client id is a public PKCE client identifier,
// the URLs are public endpoints, and authorization is always the token's
// validated `repos` claim regardless of what this document says.
const WellKnownClientConfigPath = "/.well-known/mcp-client-config"

// MCPOAuthRedirectURIHint is the loopback OAuth callback cainban's Cognito MCP
// CLI client registers. A client whose runtime picks a random callback port
// (Kiro) must pin THIS value or Cognito rejects the exact-match redirect with
// `redirect_mismatch`. It is advertised as a hint so a config writer can set
// oauth.redirectUri without the user knowing it.
const MCPOAuthRedirectURIHint = "http://127.0.0.1:3334/oauth/callback"

// ClientConfigConfig carries the values the client-config document is built
// from. All are DERIVED from the MCP Lambda's environment by the caller
// (cmd/cainban-lambda) — nothing here is hardcoded:
//   - MCPURL is the canonical MCP API URL (CAINBAN_MCP_RESOURCE).
//   - CLIClientID is the public Cognito MCP CLI app client id
//     (CAINBAN_MCP_CLI_CLIENT_ID — a new env the CDK stack sets from the
//     McpCliClient's UserPoolClientId).
//   - HostedUIDomain is the Cognito Hosted UI base URL
//     (CAINBAN_HOSTED_UI_DOMAIN), used to build the authorize/token endpoints
//     and the exfil-gate authorize-host entry.
type ClientConfigConfig struct {
	MCPURL         string
	CLIClientID    string
	HostedUIDomain string
}

// mcpServerEntry is the shape of a single MCP server entry both Kiro Crew's
// on-disk mcp.json and the Kiro/Cursor/VS Code config files use. Field order
// mirrors what the setup guide documents.
type mcpServerEntry struct {
	Type     string            `json:"type,omitempty"`
	URL      string            `json:"url"`
	ClientID string            `json:"client_id,omitempty"`
	OAuth    *oauthEntry       `json:"oauth,omitempty"`
	Headers  map[string]string `json:"headers,omitempty"`
}

type oauthEntry struct {
	ClientID    string `json:"clientId"`
	RedirectURI string `json:"redirectUri"`
}

// mcpServersDoc wraps a server map as {"mcpServers": {...}} — the universal
// envelope every file-based MCP client uses.
type mcpServersDoc struct {
	MCPServers map[string]mcpServerEntry `json:"mcpServers"`
}

// exfilGateDoc is Kiro Crew's ~/.kiro/crew/oauth_endpoints.json shape: the
// append-only allowlist of authorize endpoints its exfiltration gate permits a
// redirect to. The connect flow must add cainban's Cognito authorize host or
// the Authorize banner fails closed with "URL contained ... exfiltration
// pattern".
type exfilGateDoc struct {
	AdditionalAuthorizationEndpoints []authEndpoint `json:"additional_authorization_endpoints"`
}

type authEndpoint struct {
	Host string `json:"host"`
	Path string `json:"path"`
}

// clientConfigDoc is the full document this endpoint returns.
type clientConfigDoc struct {
	// Flat, machine-readable fields a config writer reads directly.
	MCPURL                string `json:"mcp_url"`
	ClientID              string `json:"client_id"`
	AuthorizationEndpoint string `json:"authorization_endpoint,omitempty"`
	TokenEndpoint         string `json:"token_endpoint,omitempty"`
	RedirectURIHint       string `json:"redirect_uri_hint"`

	// Pre-computed, copy-paste config blocks per client. A config writer can
	// pick its format and write it verbatim; the connect page renders these.
	Configs clientConfigs `json:"configs"`
}

// clientConfigs holds the ready-to-write config for each supported client.
type clientConfigs struct {
	// Kirocrew is the ~/.kiro/crew/mcp.json entry (type + snake_case client_id +
	// nested oauth, matching the on-disk file the runtime reads).
	Kirocrew mcpServersDoc `json:"kirocrew"`
	// KirocrewExfilGate is the ~/.kiro/crew/oauth_endpoints.json entry that
	// allowlists cainban's Cognito authorize host for the exfil gate. Omitted
	// (empty slice) when the Hosted UI domain is unknown.
	KirocrewExfilGate exfilGateDoc `json:"kirocrew_exfil_gate"`
	// KirocrewDashboard is the Add-Custom-Server paste-box form: camelCase
	// clientId, NO `type` (that surface rejects both `type` and `client_id`).
	KirocrewDashboard mcpServersDoc `json:"kirocrew_dashboard"`
	// KiroIDE / Cursor / VSCode share the file-based shape (type + client_id +
	// oauth), identical content, kept as separate keys so the connect page can
	// label them without re-deriving.
	KiroIDE mcpServersDoc `json:"kiro_ide"`
	Cursor  mcpServersDoc `json:"cursor"`
	VSCode  mcpServersDoc `json:"vscode"`
	// ClaudeCodeCommand is the one-line `claude mcp add` command.
	ClaudeCodeCommand string `json:"claude_code_command"`
}

// NewClientConfigHandler returns an http.Handler serving the MCP client
// auto-config document as JSON. It is PUBLIC (no auth) — the caller mounts it on
// WellKnownClientConfigPath ahead of the authenticated handler so a client can
// discover its config before holding any token.
//
// The values come from cfg (derived from env by the entrypoint), never from the
// request, so a client cannot influence what URL/client id the document
// advertises.
func NewClientConfigHandler(cfg ClientConfigConfig) http.Handler {
	doc := buildClientConfigDoc(cfg)
	// Marshal once at construction — the document is static per process.
	body, _ := json.MarshalIndent(doc, "", "  ")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		// Public and stable; allow any origin so the browser connect page can
		// fetch it, and permit short caching.
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodHead {
			return
		}
		_, _ = w.Write(body)
	})
}

// buildClientConfigDoc assembles the document from cfg. Exposed to tests via the
// package; pure (no I/O), so the handler marshals its result once.
func buildClientConfigDoc(cfg ClientConfigConfig) clientConfigDoc {
	mcpURL := strings.TrimRight(strings.TrimSpace(cfg.MCPURL), "/")
	clientID := strings.TrimSpace(cfg.CLIClientID)
	hostedUI := strings.TrimRight(strings.TrimSpace(cfg.HostedUIDomain), "/")

	var authorizeEP, tokenEP string
	if hostedUI != "" {
		authorizeEP = hostedUI + "/oauth2/authorize"
		tokenEP = hostedUI + "/oauth2/token"
	}

	// The file-based server entry (Kiro Crew on-disk, Kiro IDE, Cursor, VS Code).
	fileEntry := mcpServerEntry{
		Type:     "http",
		URL:      mcpURL,
		ClientID: clientID,
		OAuth: &oauthEntry{
			ClientID:    clientID,
			RedirectURI: MCPOAuthRedirectURIHint,
		},
	}
	fileDoc := func() mcpServersDoc {
		return mcpServersDoc{MCPServers: map[string]mcpServerEntry{"cainban": fileEntry}}
	}

	// The dashboard paste-box entry: camelCase clientId via oauth, NO type, NO
	// snake_case client_id (that surface rejects both).
	dashEntry := mcpServerEntry{
		URL: mcpURL,
		OAuth: &oauthEntry{
			ClientID:    clientID,
			RedirectURI: MCPOAuthRedirectURIHint,
		},
	}

	// Exfil-gate entry: the Cognito authorize host, only when we know it.
	exfil := exfilGateDoc{AdditionalAuthorizationEndpoints: []authEndpoint{}}
	if host := hostOf(hostedUI); host != "" {
		exfil.AdditionalAuthorizationEndpoints = append(exfil.AdditionalAuthorizationEndpoints, authEndpoint{
			Host: host,
			Path: "/oauth2/authorize",
		})
	}

	claudeCmd := ""
	if mcpURL != "" && clientID != "" {
		claudeCmd = "claude mcp add --transport http --client-id " + clientID + " cainban " + mcpURL
	}

	return clientConfigDoc{
		MCPURL:                mcpURL,
		ClientID:              clientID,
		AuthorizationEndpoint: authorizeEP,
		TokenEndpoint:         tokenEP,
		RedirectURIHint:       MCPOAuthRedirectURIHint,
		Configs: clientConfigs{
			Kirocrew:          fileDoc(),
			KirocrewExfilGate: exfil,
			KirocrewDashboard: mcpServersDoc{MCPServers: map[string]mcpServerEntry{"cainban": dashEntry}},
			KiroIDE:           fileDoc(),
			Cursor:            fileDoc(),
			VSCode:            fileDoc(),
			ClaudeCodeCommand: claudeCmd,
		},
	}
}

// hostOf returns the host (no scheme, no path) of a URL, or "" if it cannot be
// parsed. Used to turn the Hosted UI base URL into the exfil-gate `host` entry.
func hostOf(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Host
}
