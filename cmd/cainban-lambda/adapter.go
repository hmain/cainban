package main

import (
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/hmain/cainban/src/systems/auth"
	"github.com/hmain/cainban/src/systems/mcp"
	"github.com/hmain/cainban/src/systems/store"
)

// Auth env vars the CDK stack sets from the Cognito user pool. The JWKS URL is
// derived from the issuer when not set explicitly (Cognito's standard path).
const (
	envAuthIssuer   = "CAINBAN_AUTH_ISSUER"   // e.g. https://cognito-idp.<region>.amazonaws.com/<poolId>
	envAuthAudience = "CAINBAN_AUTH_AUDIENCE" // Cognito app client id
	envAuthJWKSURL  = "CAINBAN_AUTH_JWKS_URL" // optional override; else <issuer>/.well-known/jwks.json
	// envMcpResource is the MCP API's canonical URL (McpApiUrl). The CDK stack
	// sets it from the HTTP API's own Url() output. It is the RFC 8707 resource
	// indicator and the `resource` field of the RFC 9728 protected-resource
	// metadata document, and the base for the WWW-Authenticate resource_metadata
	// pointer. Optional: when unset the discovery document/challenge degrade
	// gracefully (empty resource / legacy realm challenge) rather than failing.
	envMcpResource = "CAINBAN_MCP_RESOURCE"
	// envHostedUIDomain is the Cognito Hosted UI base URL (HostedUiDomain stack
	// output), e.g. https://cainban-emawiant-<acct>.auth.<region>.amazoncognito.com.
	// The CDK stack sets it; the RFC 8414 AS-metadata shim builds the
	// authorize/token endpoints from it (Cognito's authorize/token live under
	// the Hosted UI domain, NOT under the issuer host). Never hardcoded.
	envHostedUIDomain = "CAINBAN_HOSTED_UI_DOMAIN"
	// envMcpCliClientID is the public Cognito MCP CLI app client id
	// (McpCliClientId stack output). The CDK stack sets it; the client-config
	// auto-config document advertises it so an agent/connect-page can write a
	// complete MCP config without the user looking the id up. Public PKCE client
	// identifier — not a secret. Optional: when unset the document's client_id
	// is empty and the Claude command/snippets degrade gracefully.
	envMcpCliClientID = "CAINBAN_MCP_CLI_CLIENT_ID"
)

// resourceMetadataConfig builds the RFC 9728 protected-resource metadata config
// from env: the canonical MCP URL (CAINBAN_MCP_RESOURCE, trailing slash trimmed)
// as the resource, and the MCP API ORIGIN as the authorization server the client
// runs RFC 8414 discovery against.
//
// authorization_servers is DELIBERATELY the MCP API origin (scheme://host,
// path stripped), NOT the raw Cognito issuer. RFC 8414 inserts the well-known
// segment after the host and before any path, so discovery on the path-less
// origin resolves to <origin>/.well-known/oauth-authorization-server —
// cainban's RFC 8414 shim, which advertises S256 PKCE (Cognito's own discovery
// reports code_challenge_methods_supported:null and a spec-compliant client
// refuses that). See mcp.ResourceMetadataConfig.AuthorizationServer and
// docs/mcp-oauth-setup.md for the resolved discovery URL.
func resourceMetadataConfig() mcp.ResourceMetadataConfig {
	return mcp.ResourceMetadataConfig{
		Resource:            strings.TrimRight(strings.TrimSpace(os.Getenv(envMcpResource)), "/"),
		AuthorizationServer: mcpAPIOrigin(),
	}
}

// authServerMetadataConfig builds the RFC 8414 authorization-server metadata
// shim config from env. The advertised `issuer` MUST equal the base URL the
// client discovered on (the MCP API origin), so it is mcpAPIOrigin(); the JWKS
// stays Cognito's (tokens are Cognito-signed) via CAINBAN_AUTH_ISSUER; the
// authorize/token endpoints are built from CAINBAN_HOSTED_UI_DOMAIN. All env,
// never hardcoded.
func authServerMetadataConfig() mcp.AuthServerMetadataConfig {
	return mcp.AuthServerMetadataConfig{
		Issuer:         mcpAPIOrigin(),
		CognitoIssuer:  strings.TrimSpace(os.Getenv(envAuthIssuer)),
		HostedUIDomain: strings.TrimRight(strings.TrimSpace(os.Getenv(envHostedUIDomain)), "/"),
	}
}

// clientConfigConfig builds the cainban MCP client auto-config document config
// from env: the canonical MCP URL (CAINBAN_MCP_RESOURCE), the public MCP CLI
// client id (CAINBAN_MCP_CLI_CLIENT_ID), and the Cognito Hosted UI domain
// (CAINBAN_HOSTED_UI_DOMAIN) the authorize/token endpoints + exfil-gate host are
// built from. All env, never hardcoded; each degrades gracefully when unset.
func clientConfigConfig() mcp.ClientConfigConfig {
	return mcp.ClientConfigConfig{
		MCPURL:         strings.TrimRight(strings.TrimSpace(os.Getenv(envMcpResource)), "/"),
		CLIClientID:    strings.TrimSpace(os.Getenv(envMcpCliClientID)),
		HostedUIDomain: strings.TrimRight(strings.TrimSpace(os.Getenv(envHostedUIDomain)), "/"),
	}
}

// mcpAPIOrigin returns the scheme://host origin of the MCP API (CAINBAN_MCP_RESOURCE),
// with any path/query/trailing slash stripped. This is the path-less issuer the
// RFC 9728 doc advertises and the RFC 8414 shim's `issuer`, so RFC 8414
// discovery resolves to <origin>/.well-known/oauth-authorization-server. Returns
// "" when CAINBAN_MCP_RESOURCE is unset or unparseable.
func mcpAPIOrigin() string {
	raw := strings.TrimSpace(os.Getenv(envMcpResource))
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// resourceMetadataURL is the absolute URL of the protected-resource metadata
// document (<McpApiUrl>/.well-known/oauth-protected-resource), used as the
// resource_metadata pointer in the 401 WWW-Authenticate challenge. Empty when
// CAINBAN_MCP_RESOURCE is unset (the challenge then falls back to the legacy
// realm form).
func resourceMetadataURL() string {
	base := strings.TrimRight(strings.TrimSpace(os.Getenv(envMcpResource)), "/")
	if base == "" {
		return ""
	}
	return base + mcp.WellKnownProtectedResourcePath
}

// buildResolver constructs the signature-first auth resolver from env. It fails
// (rather than serving an unauthenticated endpoint) if issuer/audience are
// missing — there is no fail-open path.
func buildResolver() (*auth.Resolver, error) {
	issuer := strings.TrimSpace(os.Getenv(envAuthIssuer))
	audiences := splitAudiences(os.Getenv(envAuthAudience))
	if issuer == "" || len(audiences) == 0 {
		return nil, fmt.Errorf("both %s and %s must be set (no unauthenticated endpoint)", envAuthIssuer, envAuthAudience)
	}
	jwksURL := strings.TrimSpace(os.Getenv(envAuthJWKSURL))
	if jwksURL == "" {
		jwksURL = strings.TrimRight(issuer, "/") + "/.well-known/jwks.json"
	}

	validator, err := auth.NewValidator(auth.Config{
		Issuer:    issuer,
		Audiences: audiences,
		Keys:      auth.NewJWKSCache(jwksURL),
	})
	if err != nil {
		return nil, err
	}
	return auth.NewResolver(validator), nil
}

// ensureDynamoBackend forces CAINBAN_BACKEND=dynamodb for this process. Returns
// the previous value (for completeness / testability).
func ensureDynamoBackend() string {
	prev := os.Getenv(store.EnvBackend)
	_ = os.Setenv(store.EnvBackend, store.BackendDynamoDB)
	return prev
}

// splitAudiences parses a comma-separated CAINBAN_AUTH_AUDIENCE into a list of
// accepted app-client-id audiences (trimmed, non-empty). This lets one Cognito
// pool that issues tokens to several app clients — the machine client AND the
// browser SPA client — be validated by one authorizer/Lambda. A single value
// (no commas) yields a one-element list, preserving the prior behavior.
func splitAudiences(raw string) []string {
	out := []string{}
	for _, p := range strings.Split(raw, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
