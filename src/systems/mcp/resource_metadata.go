package mcp

import (
	"encoding/json"
	"net/http"
	"strings"
)

// WellKnownProtectedResourcePath is the RFC 9728 Protected Resource Metadata
// discovery path. It MUST be reachable WITHOUT authentication so a spec-
// compliant MCP client can discover cainban's authorization server before it
// holds any token (MCP authorization spec 2026-07-28, step (a)).
const WellKnownProtectedResourcePath = "/.well-known/oauth-protected-resource"

// MCPResourceScope is the OAuth scope hint cainban advertises for its MCP
// resource, surfaced both in the protected-resource metadata (scopes_supported)
// and in the WWW-Authenticate challenge on a 401. It is a hint for clients; the
// in-Lambda authorization decision is still the validated `repos` claim, not a
// scope string.
const MCPResourceScope = "cainban:tasks"

// ResourceMetadataConfig carries the values the RFC 9728 document is built from.
// Both are DERIVED from the MCP Lambda's existing environment (McpApiUrl and
// CAINBAN_AUTH_ISSUER) by the caller (cmd/cainban-lambda) — nothing here is
// hardcoded.
type ResourceMetadataConfig struct {
	// Resource is the MCP API's canonical URL — the resource identifier a
	// client sends as the RFC 8707 `resource` indicator. Per RFC 8707 canonical-
	// URI guidance it carries NO trailing slash; NewResourceMetadataHandler
	// trims one defensively.
	Resource string
	// AuthorizationServer is the issuer URL of cainban's authorization server
	// (the Cognito user pool), derived from CAINBAN_AUTH_ISSUER. A spec-
	// compliant MCP client fetches THIS issuer's own metadata
	// (/.well-known/openid-configuration) to run the PKCE authorization-code
	// flow — cainban itself implements none of that (it is only the resource
	// server).
	AuthorizationServer string
}

// protectedResourceMetadata is the RFC 9728 Protected Resource Metadata
// document. Field order/tags follow the RFC's JSON member names exactly.
type protectedResourceMetadata struct {
	Resource               string   `json:"resource"`
	AuthorizationServers   []string `json:"authorization_servers"`
	ScopesSupported        []string `json:"scopes_supported"`
	BearerMethodsSupported []string `json:"bearer_methods_supported"`
}

// NewResourceMetadataHandler returns an http.Handler serving the RFC 9728
// Protected Resource Metadata document as JSON. It is PUBLIC (no auth) — the
// caller mounts it on the well-known path ahead of the authenticated handler so
// discovery works before a client has a token.
//
// The values come from cfg (derived from env by the entrypoint), never from the
// request, so a client cannot influence what resource/authorization server the
// document advertises.
func NewResourceMetadataHandler(cfg ResourceMetadataConfig) http.Handler {
	// Canonicalize the resource per RFC 8707: no trailing slash.
	resource := strings.TrimRight(strings.TrimSpace(cfg.Resource), "/")
	authServer := strings.TrimSpace(cfg.AuthorizationServer)

	doc := protectedResourceMetadata{
		Resource:               resource,
		AuthorizationServers:   []string{authServer},
		ScopesSupported:        []string{MCPResourceScope},
		BearerMethodsSupported: []string{"header"},
	}
	// Marshal once at construction — the document is static per process.
	body, _ := json.Marshal(doc)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Discovery is a GET (HEAD allowed); anything else is not allowed.
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		// Discovery metadata is public and stable; allow any origin so a browser
		// MCP client can read it, and permit short caching.
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodHead {
			return
		}
		_, _ = w.Write(body)
	})
}

// PublicMux returns the top-level HTTP handler for the MCP Lambda: it serves the
// RFC 9728 protected-resource metadata publicly at WellKnownProtectedResourcePath
// and routes EVERY other request through authed (HandlerWithAuth). This is what
// lets the well-known path BYPASS AuthMiddleware while no other route does — the
// well-known branch is matched before, and never falls through to, the
// authenticated handler.
//
// resourceCfg is derived from env by the entrypoint (McpApiUrl +
// CAINBAN_AUTH_ISSUER). authed is the fully-authenticated MCP handler
// (Server.HandlerWithAuth).
func PublicMux(resourceCfg ResourceMetadataConfig, authed http.Handler) http.Handler {
	metadata := NewResourceMetadataHandler(resourceCfg)
	mux := http.NewServeMux()
	// Exact-match the well-known path so it is served publicly; the metadata
	// handler itself enforces GET/HEAD.
	mux.Handle(WellKnownProtectedResourcePath, metadata)
	// Everything else (root, /{proxy+}, tool calls) goes through the JWT auth +
	// tenant-resolution gate. "/" is the ServeMux catch-all.
	mux.Handle("/", authed)
	return mux
}
