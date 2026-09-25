package mcp

import (
	"context"
	"encoding/json"
	"net/http"

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
func AuthMiddleware(resolver *auth.Resolver, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The target repo comes from the header at the transport edge; an MCP
		// tool arg can further scope per call, but the header is the request's
		// default target. Authorization is decided against the validated claim
		// inside Resolve, not from this header.
		tenant, err := resolver.Resolve(r, "")
		if err != nil {
			writeAuthError(w, err)
			return
		}
		ctx := withTenant(r.Context(), tenant)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// writeAuthError writes a minimal JSON error body with the auth-appropriate
// status. The message is intentionally generic (no token internals leak).
func writeAuthError(w http.ResponseWriter, err error) {
	status := auth.HTTPStatus(err)
	w.Header().Set("Content-Type", "application/json")
	if status == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", `Bearer realm="cainban"`)
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
