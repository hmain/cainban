package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// These tests cover MCP OAuth step (a): the RFC 9728 protected-resource metadata
// endpoint is PUBLIC and returns a valid document, and a protected route's 401
// carries the RFC 9728 WWW-Authenticate challenge pointing at that document.

const (
	testMcpResource = "https://mcp.example.test/prod"
	testAuthServer  = "https://cognito-idp.eu-north-1.amazonaws.com/eu-north-1_TESTPOOL"
)

func testResourceCfg() ResourceMetadataConfig {
	return ResourceMetadataConfig{
		Resource:            testMcpResource,
		AuthorizationServer: testAuthServer,
	}
}

// TestResourceMetadata_PublicOKValidJSON proves GET /.well-known/oauth-protected-resource
// returns 200 with valid JSON (resource + authorization_servers) and requires
// NO authentication — the request carries no Authorization header.
func TestResourceMetadata_PublicOKValidJSON(t *testing.T) {
	h := NewResourceMetadataHandler(testResourceCfg())

	req := httptest.NewRequest(http.MethodGet, WellKnownProtectedResourcePath, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req) // no Authorization header set

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (must be public, no auth)", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var doc protectedResourceMetadata
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("body is not valid JSON: %v (body=%q)", err, rec.Body.String())
	}
	if doc.Resource != testMcpResource {
		t.Errorf("resource = %q, want %q", doc.Resource, testMcpResource)
	}
	if len(doc.AuthorizationServers) != 1 || doc.AuthorizationServers[0] != testAuthServer {
		t.Errorf("authorization_servers = %v, want [%q]", doc.AuthorizationServers, testAuthServer)
	}
	if len(doc.ScopesSupported) != 3 ||
		doc.ScopesSupported[0] != "openid" ||
		doc.ScopesSupported[1] != "email" ||
		doc.ScopesSupported[2] != "profile" {
		t.Errorf("scopes_supported = %v, want [openid email profile]", doc.ScopesSupported)
	}
	// Regression guard: never advertise a scope Cognito does not know, or the
	// authorize step fails with invalid_scope.
	for _, s := range doc.ScopesSupported {
		if s == "cainban:tasks" {
			t.Errorf("scopes_supported must not advertise the non-existent Cognito scope %q", s)
		}
	}
	if len(doc.BearerMethodsSupported) != 1 || doc.BearerMethodsSupported[0] != "header" {
		t.Errorf("bearer_methods_supported = %v, want [\"header\"]", doc.BearerMethodsSupported)
	}
}

// TestResourceMetadata_TrimsTrailingSlash proves the resource is canonicalized
// per RFC 8707 (no trailing slash) even when the env value carries one.
func TestResourceMetadata_TrimsTrailingSlash(t *testing.T) {
	h := NewResourceMetadataHandler(ResourceMetadataConfig{
		Resource:            testMcpResource + "/",
		AuthorizationServer: testAuthServer,
	})
	req := httptest.NewRequest(http.MethodGet, WellKnownProtectedResourcePath, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	var doc protectedResourceMetadata
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if doc.Resource != testMcpResource {
		t.Errorf("resource = %q, want %q (trailing slash trimmed)", doc.Resource, testMcpResource)
	}
}

// TestResourceMetadata_MethodNotAllowed proves a non-GET/HEAD method is 405.
func TestResourceMetadata_MethodNotAllowed(t *testing.T) {
	h := NewResourceMetadataHandler(testResourceCfg())
	req := httptest.NewRequest(http.MethodPost, WellKnownProtectedResourcePath, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405 for POST", rec.Code)
	}
}

// TestPublicMux_WellKnownBypassesAuth proves that through the top-level mux the
// well-known path is served publicly (200, no token) while every other route is
// gated by auth (401 without a token).
func TestPublicMux_WellKnownBypassesAuth(t *testing.T) {
	m := newMWSigner(t)
	// A protected handler that must NEVER run unauthenticated.
	protected := AuthMiddlewareWithChallenge(m.resolver(t), resourceMetaURLFor(testMcpResource), &spyHandler{})
	mux := PublicMux(testResourceCfg(), testAuthServerCfg(), protected)

	// (a) well-known: public 200. The Lambda sees the bare route path (API
	// Gateway routes by path; no stage/host prefix reaches the handler).
	wkReq := httptest.NewRequest(http.MethodGet, WellKnownProtectedResourcePath, nil)
	wkRec := httptest.NewRecorder()
	mux.ServeHTTP(wkRec, wkReq)
	if wkRec.Code != http.StatusOK {
		t.Errorf("well-known status = %d, want 200", wkRec.Code)
	}

	// (a2) the RFC 8414 AS-metadata shim is ALSO public: 200, no token.
	asReq := httptest.NewRequest(http.MethodGet, WellKnownAuthServerPath, nil)
	asRec := httptest.NewRecorder()
	mux.ServeHTTP(asRec, asReq)
	if asRec.Code != http.StatusOK {
		t.Errorf("as-metadata status = %d, want 200 (must be public, no auth)", asRec.Code)
	}

	// (b) some other route without a token: 401 (auth gate ran).
	otherReq := httptest.NewRequest(http.MethodPost, "/", nil)
	otherRec := httptest.NewRecorder()
	mux.ServeHTTP(otherRec, otherReq)
	if otherRec.Code != http.StatusUnauthorized {
		t.Errorf("protected route status = %d, want 401 (auth must gate non-well-known)", otherRec.Code)
	}

	// (c) a CORS preflight (OPTIONS) must be answered publicly with 204 — a
	// browser sends it with no Authorization header before the real POST, and a
	// non-2xx preflight makes the browser block the whole call. The auth gate
	// must NOT run on it (the spy handler never fires).
	preflightReq := httptest.NewRequest(http.MethodOptions, "/", nil)
	preflightRec := httptest.NewRecorder()
	mux.ServeHTTP(preflightRec, preflightReq)
	if preflightRec.Code != http.StatusNoContent {
		t.Errorf("CORS preflight status = %d, want 204 (preflight must bypass auth and succeed)", preflightRec.Code)
	}
}

// TestWriteAuthError_WWWAuthenticateResourceMetadata proves a 401 from a
// protected route carries the RFC 9728 WWW-Authenticate challenge with
// resource_metadata pointing at the discovery document and the scope hint.
func TestWriteAuthError_WWWAuthenticateResourceMetadata(t *testing.T) {
	m := newMWSigner(t)
	metaURL := resourceMetaURLFor(testMcpResource)
	h := AuthMiddlewareWithChallenge(m.resolver(t), metaURL, &spyHandler{})

	// No Authorization header => 401.
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	wa := rec.Header().Get("WWW-Authenticate")
	if !strings.HasPrefix(wa, "Bearer ") {
		t.Fatalf("WWW-Authenticate = %q, want a Bearer challenge", wa)
	}
	if !strings.Contains(wa, `resource_metadata="`+metaURL+`"`) {
		t.Errorf("WWW-Authenticate = %q, want it to carry resource_metadata=%q", wa, metaURL)
	}
	if !strings.Contains(wa, `scope="`+MCPResourceChallengeScope+`"`) {
		t.Errorf("WWW-Authenticate = %q, want it to carry scope=%q", wa, MCPResourceChallengeScope)
	}
}

// TestWriteAuthError_LegacyChallengeWhenNoURL proves the challenge falls back to
// the legacy realm form when no resource-metadata URL is configured (backward
// compatibility with the plain AuthMiddleware).
func TestWriteAuthError_LegacyChallengeWhenNoURL(t *testing.T) {
	m := newMWSigner(t)
	h := AuthMiddleware(m.resolver(t), &spyHandler{}) // no challenge URL

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if wa := rec.Header().Get("WWW-Authenticate"); wa != `Bearer realm="cainban"` {
		t.Errorf("WWW-Authenticate = %q, want legacy `Bearer realm=\"cainban\"`", wa)
	}
}

// resourceMetaURLFor mirrors the entrypoint's derivation of the metadata URL
// (<resource>/.well-known/oauth-protected-resource) for tests, without pulling
// in the cmd package.
func resourceMetaURLFor(resource string) string {
	return strings.TrimRight(resource, "/") + WellKnownProtectedResourcePath
}
