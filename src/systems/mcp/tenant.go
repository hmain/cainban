package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/hmain/cainban/src/systems/auth"
)

// tenantCtxKey is the private context key under which a request's resolved,
// AUTHORIZED tenant is carried from the auth middleware to the tool handlers.
type tenantCtxKey struct{}

// withTenant returns a context carrying the resolved tenant.
func withTenant(ctx context.Context, t *auth.Tenant) context.Context {
	return context.WithValue(ctx, tenantCtxKey{}, t)
}

// tenantFromContext returns the resolved tenant, if any. The stdio/local CLI
// path never sets it (single-tenant, empty prefix); the authenticated HTTP path
// always sets it before a handler runs.
func tenantFromContext(ctx context.Context) (*auth.Tenant, bool) {
	t, ok := ctx.Value(tenantCtxKey{}).(*auth.Tenant)
	return t, ok
}

// AuthMiddleware wraps an http.Handler (the MCP Streamable-HTTP handler) with
// signature-first JWT auth + repo-scoped tenant resolution. It is the gate that
// replaces the Phase 2 unauthenticated endpoint: EVERY request must carry a
// valid bearer token authorizing the target repo, or it is rejected here BEFORE
// the MCP handler (and therefore any store) runs.
//
// On success the resolved tenant is injected into the request context, where
// resolveTaskSystem reads it to build a DynamoDB store scoped to that tenant's
// partition prefix. On failure it writes a JSON-RPC-shaped error with the right
// HTTP status (401 unauthenticated, 403 forbidden) and never calls next.
//
// This form emits the legacy `WWW-Authenticate: Bearer realm="cainban"` on 401.
// Use AuthMiddlewareWithChallenge to additionally advertise the RFC 9728
// protected-resource metadata URL (the MCP-OAuth discovery pointer).
func AuthMiddleware(resolver *auth.Resolver, next http.Handler) http.Handler {
	return AuthMiddlewareWithChallenge(resolver, "", next)
}

// AuthMiddlewareWithChallenge is AuthMiddleware plus the RFC 9728 discovery
// pointer: when resourceMetadataURL is non-empty, a 401 carries
//
//	WWW-Authenticate: Bearer resource_metadata="<url>", scope="openid"
//
// which is what tells a spec-compliant MCP client (MCP authorization spec
// 2026-07-28) WHERE to discover cainban's authorization server. resourceMetadataURL
// is the absolute URL of the protected-resource metadata document
// (<McpApiUrl>/.well-known/oauth-protected-resource), derived from env by the
// entrypoint — never from the request.
func AuthMiddlewareWithChallenge(resolver *auth.Resolver, resourceMetadataURL string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The target repo comes from the header at the transport edge; an MCP
		// tool arg can further scope per call, but the header is the request's
		// default target. Authorization is decided against the validated claim
		// inside Resolve, not from this header.
		tenant, err := resolver.Resolve(r, "")
		if err != nil {
			writeAuthError(w, err, resourceMetadataURL)
			return
		}
		ctx := withTenant(r.Context(), tenant)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// writeAuthError writes a minimal JSON error body with the auth-appropriate
// status. The message is intentionally generic (no token internals leak).
//
// On a 401 it sets the WWW-Authenticate challenge. When resourceMetadataURL is
// non-empty it emits the RFC 9728 MCP-OAuth form
//
//	Bearer resource_metadata="<url>", scope="openid"
//
// so a spec-compliant MCP client can discover the authorization server; when it
// is empty it falls back to the legacy `Bearer realm="cainban"` challenge.
func writeAuthError(w http.ResponseWriter, err error, resourceMetadataURL string) {
	status := auth.HTTPStatus(err)
	w.Header().Set("Content-Type", "application/json")
	if status == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", bearerChallenge(resourceMetadataURL))
	}
	w.WriteHeader(status)
	msg := "unauthorized"
	if status == http.StatusForbidden {
		msg = "forbidden"
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"code":    status,
			"message": msg,
		},
	})
}

// bearerChallenge builds the 401 WWW-Authenticate header value. With a
// resource-metadata URL it is the RFC 9728 MCP-OAuth challenge pointing a client
// at the discovery document; without one it is the legacy realm challenge.
func bearerChallenge(resourceMetadataURL string) string {
	if u := strings.TrimSpace(resourceMetadataURL); u != "" {
		// resource_metadata is a URL; scope is the resource's scope hint. Both
		// are quoted-string auth-param values per RFC 7235. The URL is
		// constructed from trusted env (never the request), so it needs no
		// escaping beyond the surrounding quotes.
		return `Bearer resource_metadata="` + u + `", scope="` + MCPResourceChallengeScope + `"`
	}
	return `Bearer realm="cainban"`
}
