package main

import (
	"fmt"
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
)

// resourceMetadataConfig builds the RFC 9728 protected-resource metadata config
// from env: the canonical MCP URL (CAINBAN_MCP_RESOURCE, trailing slash trimmed)
// as the resource, and the Cognito issuer (CAINBAN_AUTH_ISSUER — the SAME env
// the validator already uses, never hardcoded) as the authorization server. The
// metadata handler canonicalizes the resource again defensively.
func resourceMetadataConfig() mcp.ResourceMetadataConfig {
	return mcp.ResourceMetadataConfig{
		Resource:            strings.TrimRight(strings.TrimSpace(os.Getenv(envMcpResource)), "/"),
		AuthorizationServer: strings.TrimSpace(os.Getenv(envAuthIssuer)),
	}
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
