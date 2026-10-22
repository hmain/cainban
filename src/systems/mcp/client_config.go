package mcp

import (
	"encoding/json"
	"net/http"
	"strings"
)

// WellKnownClientConfigPath is cainban's own (non-RFC) MCP client auto-config
// discovery path. It is a convenience document that returns what an MCP client
// (or an AI agent setting one up) needs to configure itself in one fetch: the
// MCP URL plus pre-computed, copy-paste server snippets for each supported
// client (Kiro Crew, Claude Code, Kiro IDE, Cursor, VS Code).
//
// # Why this exists
//
// Setting cainban up by hand is error-prone: the MCP API URL lives only in
// CloudFormation outputs / the connect page. This endpoint collapses that into
// one GET: the connect page renders from it (one source of truth, no
// client-side snippet drift), and an agent can fetch it and write its own
// config without hand-editing JSON. It is PUBLIC (no token) because
// configuration must be discoverable before the client holds any credential.
//
// # GitHub-only note
//
// Under the GitHub-only auth model there is no OAuth authorization server:
// clients authenticate with a GitHub token (device flow or a fine-grained PAT)
// supplied out of band as the bearer. This document therefore advertises NO
// authorize/token endpoints and NO OAuth client id — only the MCP URL and the
// server snippets. Nothing here is a secret.
const WellKnownClientConfigPath = "/.well-known/mcp-client-config"

// ClientConfigConfig carries the values the client-config document is built
// from. DERIVED from the MCP Lambda's environment by the caller
// (cmd/cainban-lambda) — nothing here is hardcoded:
//   - MCPURL is the canonical MCP API URL (CAINBAN_MCP_RESOURCE).
type ClientConfigConfig struct {
	MCPURL string
}

// stdioServerEntry is the shape of a stdio MCP server entry for local agents.
// Unlike HTTP entries, it has command/args/env instead of url/oauth.
type stdioServerEntry struct {
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
}

// stdioServersDoc wraps a stdio server map.
type stdioServersDoc struct {
	MCPServers map[string]stdioServerEntry `json:"mcpServers"`
}

// mcpServerEntry is the shape of a single MCP server entry both Kiro Crew's
// on-disk mcp.json and the Kiro/Cursor/VS Code config files use. Under
// GitHub-only auth there is no OAuth block — the client supplies its GitHub
// token as the bearer out of band.
type mcpServerEntry struct {
	Type    string            `json:"type,omitempty"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
}

// mcpServersDoc wraps a server map as {"mcpServers": {...}} — the universal
// envelope every file-based MCP client uses.
type mcpServersDoc struct {
	MCPServers map[string]mcpServerEntry `json:"mcpServers"`
}

// clientConfigDoc is the full document this endpoint returns.
type clientConfigDoc struct {
	// Flat, machine-readable field a config writer reads directly.
	MCPURL string `json:"mcp_url"`

	// Pre-computed, copy-paste config blocks per client. A config writer can
	// pick its format and write it verbatim; the connect page renders these.
	Configs clientConfigs `json:"configs"`
}

// clientConfigs holds the ready-to-write config for each supported client.
type clientConfigs struct {
	// Kirocrew is the ~/.kiro/crew/mcp.json entry (type + url).
	Kirocrew mcpServersDoc `json:"kirocrew"`
	// KirocrewDashboard is the Add-Custom-Server paste-box form (url only).
	KirocrewDashboard mcpServersDoc `json:"kirocrew_dashboard"`
	// KiroIDE / Cursor / VSCode share the file-based shape, identical content,
	// kept as separate keys so the connect page can label them.
	KiroIDE mcpServersDoc `json:"kiro_ide"`
	Cursor  mcpServersDoc `json:"cursor"`
	VSCode  mcpServersDoc `json:"vscode"`
	// ClaudeCodeCommand is the one-line `claude mcp add` command.
	ClaudeCodeCommand string `json:"claude_code_command"`
	// Stdio is the local stdio transport config with env-based credentials.
	// Per MCP spec: "STDIO transport SHOULD NOT follow [OAuth] and instead
	// retrieve credentials from the environment."
	Stdio stdioServersDoc `json:"stdio"`
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

	// The file-based server entry (Kiro Crew on-disk, Kiro IDE, Cursor, VS Code).
	// GitHub-only: just the HTTP URL — the client sends its GitHub token as the
	// bearer; there is no OAuth block to pre-fill.
	fileEntry := mcpServerEntry{
		Type: "http",
		URL:  mcpURL,
	}
	fileDoc := func() mcpServersDoc {
		return mcpServersDoc{MCPServers: map[string]mcpServerEntry{"cainban": fileEntry}}
	}

	// The dashboard paste-box entry: url only (that surface rejects `type`).
	dashEntry := mcpServerEntry{URL: mcpURL}

	claudeCmd := ""
	if mcpURL != "" {
		claudeCmd = "claude mcp add --transport http cainban " + mcpURL
	}

	// Stdio config for local agents (env-based credentials). The command
	// placeholder "cainban" assumes it's in PATH; users can replace it with the
	// full path to their cainban binary.
	stdioDoc := stdioServersDoc{
		MCPServers: map[string]stdioServerEntry{
			"cainban": {
				Command: "cainban",
				Args:    []string{"mcp"},
				Env: map[string]string{
					"CAINBAN_REPO":  "${CAINBAN_REPO}",
					"CAINBAN_ACTOR": "${CAINBAN_ACTOR}",
				},
			},
		},
	}

	return clientConfigDoc{
		MCPURL: mcpURL,
		Configs: clientConfigs{
			Kirocrew:          fileDoc(),
			KirocrewDashboard: mcpServersDoc{MCPServers: map[string]mcpServerEntry{"cainban": dashEntry}},
			KiroIDE:           fileDoc(),
			Cursor:            fileDoc(),
			VSCode:            fileDoc(),
			ClaudeCodeCommand: claudeCmd,
			Stdio:             stdioDoc,
		},
	}
}
