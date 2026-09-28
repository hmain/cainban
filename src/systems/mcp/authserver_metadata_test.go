package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// These tests cover MCP OAuth step (a'): the RFC 8414 authorization-server
// metadata SHIM is PUBLIC, returns a valid document mirroring Cognito's
// endpoints, and — the whole point — advertises code_challenge_methods_supported
// ["S256"], which Cognito's own discovery omits and a spec-compliant MCP client
// (Claude Code) requires.

const (
	testHostedUI      = "https://cainban-emawiant-123456789012.auth.eu-north-1.amazoncognito.com"
	testMcpAPIOrigin  = "https://abc123.execute-api.eu-north-1.amazonaws.com"
	testCognitoIssuer = testAuthServer // reuse the resource-metadata test issuer
)

func testAuthServerCfg() AuthServerMetadataConfig {
	return AuthServerMetadataConfig{
		Issuer:         testMcpAPIOrigin,
		CognitoIssuer:  testCognitoIssuer,
		HostedUIDomain: testHostedUI,
	}
}

// TestAuthServerMetadata_PublicOKAdvertisesS256 is the load-bearing test: GET
// /.well-known/oauth-authorization-server returns 200 valid JSON, UNAUTHENTICATED,
// with code_challenge_methods_supported:["S256"] and the mirrored Cognito
// endpoints. This is exactly the advertisement Cognito's own metadata is missing.
func TestAuthServerMetadata_PublicOKAdvertisesS256(t *testing.T) {
	h := NewAuthServerMetadataHandler(testAuthServerCfg())

	req := httptest.NewRequest(http.MethodGet, WellKnownAuthServerPath, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req) // no Authorization header set

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (must be public, no auth)", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var doc authServerMetadata
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("body is not valid JSON: %v (body=%q)", err, rec.Body.String())
	}

	// THE KEY ASSERTION: S256 PKCE is advertised.
	if len(doc.CodeChallengeMethodsSupported) != 1 || doc.CodeChallengeMethodsSupported[0] != "S256" {
		t.Errorf("code_challenge_methods_supported = %v, want [\"S256\"]", doc.CodeChallengeMethodsSupported)
	}
	// issuer MUST equal the base the client discovered on (RFC 8414 §3.3).
	if doc.Issuer != testMcpAPIOrigin {
		t.Errorf("issuer = %q, want %q (the MCP API origin the client discovered on)", doc.Issuer, testMcpAPIOrigin)
	}
	// Endpoints mirror Cognito's real Hosted UI + issuer JWKS.
	if want := testHostedUI + "/oauth2/authorize"; doc.AuthorizationEndpoint != want {
		t.Errorf("authorization_endpoint = %q, want %q", doc.AuthorizationEndpoint, want)
	}
	if want := testHostedUI + "/oauth2/token"; doc.TokenEndpoint != want {
		t.Errorf("token_endpoint = %q, want %q", doc.TokenEndpoint, want)
	}
	if want := testCognitoIssuer + "/.well-known/jwks.json"; doc.JwksURI != want {
		t.Errorf("jwks_uri = %q, want %q", doc.JwksURI, want)
	}
	// Advertisement fields a spec-compliant client checks.
	if len(doc.ResponseTypesSupported) != 1 || doc.ResponseTypesSupported[0] != "code" {
		t.Errorf("response_types_supported = %v, want [\"code\"]", doc.ResponseTypesSupported)
	}
	if len(doc.TokenEndpointAuthMethodsSupported) != 1 || doc.TokenEndpointAuthMethodsSupported[0] != "none" {
		t.Errorf("token_endpoint_auth_methods_supported = %v, want [\"none\"]", doc.TokenEndpointAuthMethodsSupported)
	}
	if len(doc.GrantTypesSupported) == 0 {
		t.Errorf("grant_types_supported = %v, want non-empty", doc.GrantTypesSupported)
	}
	if len(doc.ScopesSupported) == 0 {
		t.Errorf("scopes_supported = %v, want non-empty", doc.ScopesSupported)
	}
}

// TestAuthServerMetadata_TrimsTrailingSlashes proves the endpoints are built
// cleanly even when the env values carry trailing slashes.
func TestAuthServerMetadata_TrimsTrailingSlashes(t *testing.T) {
	h := NewAuthServerMetadataHandler(AuthServerMetadataConfig{
		Issuer:         testMcpAPIOrigin + "/",
		CognitoIssuer:  testCognitoIssuer + "/",
		HostedUIDomain: testHostedUI + "/",
	})
	req := httptest.NewRequest(http.MethodGet, WellKnownAuthServerPath, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	var doc authServerMetadata
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if doc.Issuer != testMcpAPIOrigin {
		t.Errorf("issuer = %q, want %q (trailing slash trimmed)", doc.Issuer, testMcpAPIOrigin)
	}
	if strings.Contains(doc.AuthorizationEndpoint, "//oauth2") {
		t.Errorf("authorization_endpoint = %q has a double slash", doc.AuthorizationEndpoint)
	}
	if strings.Contains(doc.JwksURI, "amazonaws.com//.well-known") {
		t.Errorf("jwks_uri = %q has a double slash", doc.JwksURI)
	}
}

// TestAuthServerMetadata_MethodNotAllowed proves a non-GET/HEAD method is 405.
func TestAuthServerMetadata_MethodNotAllowed(t *testing.T) {
	h := NewAuthServerMetadataHandler(testAuthServerCfg())
	req := httptest.NewRequest(http.MethodPost, WellKnownAuthServerPath, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405 for POST", rec.Code)
	}
}
