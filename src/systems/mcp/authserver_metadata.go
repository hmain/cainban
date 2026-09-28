package mcp

import (
	"encoding/json"
	"net/http"
	"strings"
)

// WellKnownAuthServerPath is the RFC 8414 Authorization Server Metadata
// discovery path. Like the RFC 9728 protected-resource path it MUST be
// reachable WITHOUT authentication so a spec-compliant MCP client (Claude Code)
// can discover the authorization endpoints — and, crucially, the PKCE
// capability — before it holds any token.
//
// # Why cainban serves this at all (the PKCE-advertisement gap)
//
// Cognito's own OIDC discovery document
// (<issuer>/.well-known/openid-configuration) reports
// `code_challenge_methods_supported: null` — it does NOT advertise PKCE, even
// though Cognito fully supports S256. Per the MCP authorization spec a
// spec-compliant client MUST refuse to proceed when that field is absent, so
// Claude Code will not run the flow against Cognito's raw metadata.
//
// This shim is a thin RFC 8414 Authorization Server Metadata document that
// MIRRORS Cognito's real endpoints (authorize/token on the Hosted UI domain,
// jwks on the issuer) but ADDS `code_challenge_methods_supported: ["S256"]`
// (plus the other required advertisement fields). It is NOT an OAuth proxy: it
// mints no tokens, holds no secret, and terminates no flow — the client still
// runs PKCE + the code exchange + refresh directly against Cognito's Hosted UI.
// It only lets the client SEE that PKCE is available. The DCR/CIMD proxy
// (Phase 5 step b) is intentionally NOT built here; see
// docs/phase5-mcp-oauth.md.
const WellKnownAuthServerPath = "/.well-known/oauth-authorization-server"

// AuthServerMetadataConfig carries the values the RFC 8414 document is built
// from. All are DERIVED from the MCP Lambda's environment by the caller
// (cmd/cainban-lambda) — nothing here is hardcoded:
//   - Issuer from CAINBAN_AUTH_ISSUER (the Cognito issuer),
//   - HostedUIDomain from CAINBAN_HOSTED_UI_DOMAIN (the Cognito Hosted UI base
//     URL, set in CDK from the UserPoolDomain), which is where Cognito's
//     authorize/token endpoints actually live (they are NOT under the issuer
//     host).
type AuthServerMetadataConfig struct {
	// Issuer is the value the metadata document advertises as `issuer`. Per RFC
	// 8414 §3.3 the client verifies that this EQUALS the issuer it performed
	// discovery on, so it MUST be the base URL whose
	// /.well-known/oauth-authorization-server resolves to THIS shim — i.e. the
	// MCP API origin (see NewAuthServerMetadataHandler and the discovery-math
	// note in docs/mcp-oauth-setup.md), NOT the Cognito issuer. The Cognito
	// issuer is only used to build jwks_uri.
	Issuer string
	// CognitoIssuer is the real Cognito issuer (CAINBAN_AUTH_ISSUER). Used ONLY
	// to derive jwks_uri (<CognitoIssuer>/.well-known/jwks.json); the tokens
	// cainban validates are still signed by Cognito, so the JWKS must be
	// Cognito's.
	CognitoIssuer string
	// HostedUIDomain is the Cognito Hosted UI base URL (CAINBAN_HOSTED_UI_DOMAIN),
	// e.g. https://cainban-emawiant-<acct>.auth.<region>.amazoncognito.com. The
	// authorize/token endpoints are built from it. No trailing slash is required
	// (one is trimmed defensively).
	HostedUIDomain string
}

// authServerMetadata is the RFC 8414 Authorization Server Metadata document.
// Field order/tags follow the RFC's JSON member names exactly. Only the members
// an MCP client needs to run the Cognito PKCE authorization-code flow are
// populated; everything else is deliberately omitted (this is a discovery shim,
// not a full AS advertisement).
type authServerMetadata struct {
	Issuer                            string   `json:"issuer"`
	AuthorizationEndpoint             string   `json:"authorization_endpoint"`
	TokenEndpoint                     string   `json:"token_endpoint"`
	JwksURI                           string   `json:"jwks_uri"`
	ResponseTypesSupported            []string `json:"response_types_supported"`
	GrantTypesSupported               []string `json:"grant_types_supported"`
	CodeChallengeMethodsSupported     []string `json:"code_challenge_methods_supported"`
	ScopesSupported                   []string `json:"scopes_supported"`
	TokenEndpointAuthMethodsSupported []string `json:"token_endpoint_auth_methods_supported"`
}

// NewAuthServerMetadataHandler returns an http.Handler serving the RFC 8414
// Authorization Server Metadata document as JSON. It is PUBLIC (no auth) — the
// caller mounts it on WellKnownAuthServerPath ahead of the authenticated
// handler so discovery works before a client has a token.
//
// The values come from cfg (derived from env by the entrypoint), never from the
// request, so a client cannot influence what endpoints the document advertises.
//
// The KEY reason this shim exists is the `code_challenge_methods_supported:
// ["S256"]` member: Cognito's own discovery reports it as null, which makes a
// spec-compliant MCP client refuse the flow. Mirroring Cognito's endpoints while
// asserting the S256 PKCE capability Cognito actually has unblocks the client.
func NewAuthServerMetadataHandler(cfg AuthServerMetadataConfig) http.Handler {
	issuer := strings.TrimRight(strings.TrimSpace(cfg.Issuer), "/")
	cognitoIssuer := strings.TrimRight(strings.TrimSpace(cfg.CognitoIssuer), "/")
	hostedUI := strings.TrimRight(strings.TrimSpace(cfg.HostedUIDomain), "/")

	doc := authServerMetadata{
		Issuer:                 issuer,
		AuthorizationEndpoint:  hostedUI + "/oauth2/authorize",
		TokenEndpoint:          hostedUI + "/oauth2/token",
		JwksURI:                cognitoIssuer + "/.well-known/jwks.json",
		ResponseTypesSupported: []string{"code"},
		GrantTypesSupported:    []string{"authorization_code", "refresh_token"},
		// THE KEY ADDITION — Cognito supports S256 PKCE but does not advertise
		// it; a spec-compliant MCP client requires this field to proceed.
		CodeChallengeMethodsSupported: []string{"S256"},
		ScopesSupported:               []string{"openid", "email", "profile"},
		// Public PKCE clients authenticate with no client secret.
		TokenEndpointAuthMethodsSupported: []string{"none"},
	}
	body, _ := json.Marshal(doc)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
