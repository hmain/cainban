package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/hmain/cainban/src/systems/auth"
	"github.com/hmain/cainban/src/systems/store"
)

// This file extends the Phase 3 auth gate (tenant.go's AuthMiddleware) with the
// WIDGET-TOKEN path: a request whose bearer token is a `wt_`-prefixed widget
// token is authenticated by this server's own HMAC secret (not the IdP JWKS),
// resolves to a READ-ONLY, single-repo tenant, and is additionally constrained
// so it can invoke only the read tools. A normal user JWT is unaffected and
// still flows through the resolver unchanged.
//
// Routing happens by prefix: isWidgetToken(bearer) decides the path BEFORE any
// JWT work. A widget token that fails HMAC/expiry/revocation validation is a
// 401 here, exactly like a bad JWT, and never reaches a store.

// widgetAuthDeps are the inputs the widget-token path needs: the HMAC secret to
// validate the token signature, a clock for expiry (injectable for tests), and
// a factory that opens the repo-scoped metadata store used for the revocation
// check. Keeping the store factory a field (rather than calling the package
// function directly) lets tests inject an in-memory store without env/AWS.
type widgetAuthDeps struct {
	secret []byte
	now    func() time.Time
	// openStore opens a WidgetTokenStore scoped to a partition prefix. Defaults
	// to store.OpenWidgetTokenStoreForTenant.
	openStore func(prefix string) (store.WidgetTokenStore, func() error, error)
}

func defaultWidgetAuthDeps() widgetAuthDeps {
	return widgetAuthDeps{
		secret: widgetSecret(),
		now:    func() time.Time { return time.Now().UTC() },
		openStore: func(prefix string) (store.WidgetTokenStore, func() error, error) {
			return store.OpenWidgetTokenStoreForTenant(context.TODO(), prefix)
		},
	}
}

// AuthMiddlewareWithWidget is AuthMiddlewareWithChallenge plus the widget-token
// path. It is the entrypoint the Lambda uses so the public endpoint accepts
// BOTH user JWTs (full access) and `wt_` widget tokens (read-only, one repo).
//
// A widget token is handled entirely here: validated HMAC-first, checked for
// expiry and revocation, turned into a read-only tenant, and gated so only the
// read tools run. Everything else (a missing token, a user JWT) falls through
// to the existing resolver path unchanged.
func AuthMiddlewareWithWidget(resolver *auth.Resolver, resourceMetadataURL string, next http.Handler) http.Handler {
	return authMiddlewareWithWidgetDeps(resolver, resourceMetadataURL, defaultWidgetAuthDeps(), next)
}

// authMiddlewareWithWidgetDeps is the test seam: identical behavior with
// injectable widget-auth dependencies.
func authMiddlewareWithWidgetDeps(resolver *auth.Resolver, resourceMetadataURL string, deps widgetAuthDeps, next http.Handler) http.Handler {
	jwtPath := AuthMiddlewareWithChallenge(resolver, resourceMetadataURL, next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bearer := bearerFromRequest(r)
		if !isWidgetToken(bearer) {
			// Not a widget token — the normal JWT gate handles it (including
			// the missing/empty case, which it 401s).
			jwtPath.ServeHTTP(w, r)
			return
		}
		serveWidgetToken(w, r, bearer, deps, resourceMetadataURL, next)
	})
}

// serveWidgetToken validates a widget token and, on success, serves the request
// through next with a read-only, repo-scoped tenant — but only if the called
// tool is in the read-only allowlist. Any validation failure is a 401; a write
// tool is rejected before next runs.
func serveWidgetToken(w http.ResponseWriter, r *http.Request, bearer string, deps widgetAuthDeps, resourceMetadataURL string, next http.Handler) {
	if len(deps.secret) == 0 {
		// Feature disabled: treat the widget token as unauthenticated.
		writeWidgetAuthError(w, http.StatusUnauthorized, resourceMetadataURL)
		return
	}
	now := time.Now().UTC()
	if deps.now != nil {
		now = deps.now()
	}
	payload, err := validateWidgetToken(deps.secret, bearer, now)
	if err != nil {
		writeWidgetAuthError(w, http.StatusUnauthorized, resourceMetadataURL)
		return
	}

	// Revocation check against the repo-scoped metadata store.
	prefix := auth.PartitionPrefixFor(payload.Repo)
	tokenStore, closeFn, serr := deps.openStore(prefix)
	if serr != nil {
		// Fail closed: if we cannot verify revocation, do not accept the token.
		writeWidgetAuthError(w, http.StatusUnauthorized, resourceMetadataURL)
		return
	}
	meta, gerr := tokenStore.GetWidgetToken(r.Context(), payload.ID)
	if closeFn != nil {
		_ = closeFn()
	}
	if gerr != nil {
		writeWidgetAuthError(w, http.StatusUnauthorized, resourceMetadataURL)
		return
	}
	if meta != nil && meta.Revoked {
		writeWidgetAuthError(w, http.StatusUnauthorized, resourceMetadataURL)
		return
	}

	// Enforce the read-only tool allowlist. Parse the JSON-RPC body to find the
	// tool name; a tools/call for a write tool is forbidden (403).
	tool, body, perr := peekToolName(r)
	if perr == nil && tool != "" && !widgetToolAllowed(tool) {
		writeWidgetAuthError(w, http.StatusForbidden, resourceMetadataURL)
		return
	}
	// Restore the body for the downstream handler (peekToolName drained it).
	if body != nil {
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.ContentLength = int64(len(body))
	}

	tenant := widgetTenant(payload)
	ctx := withTenant(r.Context(), tenant)
	next.ServeHTTP(w, r.WithContext(ctx))
}

// peekToolName reads the JSON-RPC request body and extracts the tool name when
// the method is "tools/call". It returns the tool name (empty for non-tool
// methods like initialize/tools/list), the raw body (so the caller can restore
// it), and an error only if the body could not be read. A body that is not
// JSON-RPC tools/call yields ("", body, nil) so the request proceeds — the
// handshake and listing methods must work for a widget token too.
func peekToolName(r *http.Request) (string, []byte, error) {
	if r.Body == nil {
		return "", nil, nil
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxRPCBodyBytes))
	_ = r.Body.Close()
	if err != nil {
		return "", body, err
	}
	type rpcCall struct {
		Method string `json:"method"`
		Params struct {
			Name string `json:"name"`
		} `json:"params"`
	}

	// A JSON-RPC BATCH is a top-level array. A single-object unmarshal fails on
	// it, which previously let a batch sail past the read-only gate (RFC L1):
	// the handler backstop still blocked the write, but the edge gate's stated
	// contract ("a write tool is rejected before next runs") did not hold. We
	// now inspect each element; the FIRST tools/call naming a tool is returned
	// so the caller's allowlist check can reject a batched write at the edge.
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		var batch []rpcCall
		if jerr := json.Unmarshal(trimmed, &batch); jerr != nil {
			// Malformed batch: don't guess — let the SDK handler reject it. The
			// handler-layer read-only backstop still protects writes.
			return "", body, nil
		}
		// Prefer surfacing a NON-allowlisted (write) tool so a batch mixing
		// reads and a write is still rejected at the edge; fall back to the
		// first tools/call tool name otherwise.
		firstTool := ""
		for _, c := range batch {
			if c.Method != "tools/call" || c.Params.Name == "" {
				continue
			}
			if !widgetToolAllowed(c.Params.Name) {
				return c.Params.Name, body, nil
			}
			if firstTool == "" {
				firstTool = c.Params.Name
			}
		}
		return firstTool, body, nil
	}

	var rpc rpcCall
	if jerr := json.Unmarshal(body, &rpc); jerr != nil {
		// Not parseable as a single JSON-RPC object. Let the SDK handler deal
		// with it; the handler-layer backstop still protects writes.
		return "", body, nil
	}
	if rpc.Method != "tools/call" {
		return "", body, nil
	}
	return rpc.Params.Name, body, nil
}

// maxRPCBodyBytes caps how much of a request body the read-only gate will read
// to find the tool name. A tools/call envelope is tiny; this bounds a hostile
// body without affecting legitimate calls.
const maxRPCBodyBytes = 1 << 20 // 1 MiB

// bearerFromRequest extracts the raw bearer token from the Authorization header,
// or "" when absent/malformed. Unlike the resolver's bearerToken it never
// errors — the caller only needs to route on the prefix.
func bearerFromRequest(r *http.Request) string {
	h := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(h) <= len(prefix) {
		return ""
	}
	if !strings.EqualFold(h[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(h[len(prefix):])
}

// writeWidgetAuthError writes a minimal JSON error body with the given status
// for the widget-token path, mirroring the JWT path's writeAuthError shape
// (including the 401 WWW-Authenticate challenge) without needing an auth.Error.
func writeWidgetAuthError(w http.ResponseWriter, status int, resourceMetadataURL string) {
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
