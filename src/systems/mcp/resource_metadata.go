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

// MCPResourceScopes are the OAuth scopes cainban advertises for its MCP
// resource, surfaced both in the protected-resource metadata (scopes_supported)
// and in the WWW-Authenticate challenge on a 401.
//
// These MUST be scopes that actually EXIST on the Cognito authorization server
// and are enabled on the MCP CLI app client (openid/email/profile) — because a
// spec-compliant MCP client (Claude Code) reads scopes_supported and REQUESTS
// them at Cognito's /oauth2/authorize. Advertising a scope Cognito does not know
// (a custom "cainban:tasks" with no Cognito resource server behind it) makes
// Cognito reject the ENTIRE authorize request with `invalid_scope`, blocking the
// whole flow. The in-Lambda authorization decision is still the validated
// `repos` claim, NOT a scope string — so these three OIDC scopes are all we need
// to advertise, and none is a false promise.
var MCPResourceScopes = []string{"openid", "email", "profile"}

// MCPResourceChallengeScope is the single scope named in the WWW-Authenticate
// `scope` parameter on a 401. `openid` is always valid on the Cognito clients,
// so a client re-requesting it can never trip `invalid_scope`.
const MCPResourceChallengeScope = "openid"

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
	// AuthorizationServer is the issuer URL a spec-compliant MCP client runs
	// RFC 8414 discovery against to find the authorization endpoints.
	//
	// It is DELIBERATELY the MCP API origin (McpApiUrl origin), NOT the raw
	// Cognito issuer. RFC 8414 §3 derives the metadata URL from the issuer by
	// INSERTING the well-known segment after the host and BEFORE any path:
	// issuer "https://h" (no path) => "https://h/.well-known/oauth-authorization-server".
	// The Cognito issuer carries a path ("https://cognito-idp.<r>.amazonaws.com/<poolId>"),
	// so discovery on it would resolve to
	// ".../.well-known/oauth-authorization-server/<poolId>" — Cognito serves no
	// such document, and Cognito's own OIDC metadata reports
	// code_challenge_methods_supported:null (no PKCE advertised), which a
	// spec-compliant client refuses. By advertising the path-less MCP API origin
	// here, discovery resolves to <origin>/.well-known/oauth-authorization-server
	// = cainban's RFC 8414 shim (AuthServerMetadataConfig), which mirrors
	// Cognito's endpoints AND advertises S256 PKCE. cainban is still only the
	// resource server: the shim points at Cognito's real authorize/token/jwks;
	// the client runs the flow against Cognito. See docs/mcp-oauth-setup.md for
	// the resolved discovery URL.
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
		ScopesSupported:        MCPResourceScopes,
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
// RFC 9728 protected-resource metadata AND the RFC 8414 authorization-server
// metadata shim publicly at their well-known paths, and routes EVERY other
// request through authed (HandlerWithAuth). This is what lets the well-known
// paths BYPASS AuthMiddleware while no other route does — the well-known
// branches are matched before, and never fall through to, the authenticated
// handler.
//
// resourceCfg / authServerCfg are derived from env by the entrypoint (McpApiUrl
// origin + CAINBAN_AUTH_ISSUER + CAINBAN_HOSTED_UI_DOMAIN). authed is the
// fully-authenticated MCP handler (Server.HandlerWithAuth).
func PublicMux(resourceCfg ResourceMetadataConfig, authServerCfg AuthServerMetadataConfig, authed http.Handler) http.Handler {
	metadata := NewResourceMetadataHandler(resourceCfg)
	authServerMeta := NewAuthServerMetadataHandler(authServerCfg)
	mux := http.NewServeMux()
	// Exact-match the well-known paths so they are served publicly; the metadata
	// handlers themselves enforce GET/HEAD.
	mux.Handle(WellKnownProtectedResourcePath, metadata)
	// RFC 8414 authorization-server metadata shim — PUBLIC. This is the document
	// that advertises code_challenge_methods_supported:["S256"] (which Cognito's
	// own discovery omits), unblocking a spec-compliant MCP client.
	mux.Handle(WellKnownAuthServerPath, authServerMeta)
	// Everything else (root, /{proxy+}, tool calls) goes through the JWT auth +
	// tenant-resolution gate. "/" is the ServeMux catch-all.
	//
	// EXCEPT the CORS preflight: a browser sends an unauthenticated OPTIONS
	// request before the real POST, with no Authorization header. It must get a
	// 2xx or the browser blocks the real call. The API Gateway managed CORS layer
	// attaches the Access-Control-* response headers; we just need a 2xx body-less
	// reply here (reached via the no-auth OPTIONS routes in infra/stack.go, so
	// the authorizer does not 401 the preflight first). We answer OPTIONS before
	// delegating to the authed handler so a preflight never needs a token.
	mux.Handle("/", corsPreflightOr(authed))
	return mux
}

// corsPreflightOr returns a handler that answers an OPTIONS preflight with 204
// (no body) and delegates every other method to next. The managed API Gateway
// CORS layer adds the Access-Control-* headers to the response; this only
// needs to supply the 2xx status a browser preflight requires. It never runs
// auth, which is correct — a CORS preflight carries no credentials by design.
func corsPreflightOr(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
