package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"

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
	// envGitHubOAuthSecretName is the Secrets Manager secret name holding the
	// GitHub OAuth credentials (client_id, client_secret) for the device flow.
	// Optional: when unset the GitHub auth endpoints are not mounted.
	envGitHubOAuthSecretName = "CAINBAN_GITHUB_OAUTH_SECRET_NAME"
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

// buildResolver constructs the GitHub-only auth resolver from env. cainban no
// longer uses Cognito: identity comes from the user's GitHub token (validated
// via GET /user by the resolver's GitHubValidator) and authorization comes from
// the GitHub App installation token (the RepoVerifier). The App secret is
// REQUIRED — there is no fail-open path and no user-token authorization
// fallback.
func buildResolver() (*auth.Resolver, error) {
	v := appVerifierFromEnv(context.Background())
	if v == nil {
		return nil, fmt.Errorf("%s must be set: GitHub App authorization is required (no unauthenticated or user-token path)", envGitHubAppSecretName)
	}
	// Validator is nil: GitHub-only. The resolver validates the GitHub bearer
	// token for identity; a non-GitHub token is rejected (no Cognito path).
	return auth.NewResolverWithApp(nil, v), nil
}

// envGitHubAppSecretName is the Secrets Manager secret holding the GitHub App
// credentials (app_id, private_key, client_id, client_secret) used for App-based
// authorization. Optional: when unset, App authorization is not wired and the
// resolver falls back to its default path.
const envGitHubAppSecretName = "CAINBAN_GITHUB_APP_SECRET_NAME"

// appVerifierFromEnv loads the GitHub App secret named by
// CAINBAN_GITHUB_APP_SECRET_NAME and builds the App RepoVerifier. Returns nil
// when the env var is unset (App authz not configured). A set-but-broken secret
// is fatal (panics at cold start) rather than silently serving a weaker authz
// path — an auth downgrade must never be silent.
func appVerifierFromEnv(ctx context.Context) auth.RepoVerifier {
	secretName := strings.TrimSpace(os.Getenv(envGitHubAppSecretName))
	if secretName == "" {
		return nil
	}
	secret, err := loadJSONSecret(ctx, secretName)
	if err != nil {
		panic(fmt.Sprintf("cainban-lambda: cannot load GitHub App secret %q: %v", secretName, err))
	}
	v, err := appVerifierFromSecret(secret)
	if err != nil {
		panic(fmt.Sprintf("cainban-lambda: invalid GitHub App secret %q: %v", secretName, err))
	}
	return v
}

// ensureDynamoBackend forces CAINBAN_BACKEND=dynamodb for this process. Returns
// the previous value (for completeness / testability).
func ensureDynamoBackend() string {
	prev := os.Getenv(store.EnvBackend)
	_ = os.Setenv(store.EnvBackend, store.BackendDynamoDB)
	return prev
}

// githubAuthConfig loads the GitHub OAuth credentials from Secrets Manager for
// the device flow endpoints. Returns an empty config if the secret name is not
// set (graceful degradation — the endpoints simply won't be mounted).
func githubAuthConfig(ctx context.Context) mcp.GitHubAuthConfig {
	secretName := strings.TrimSpace(os.Getenv(envGitHubOAuthSecretName))
	if secretName == "" {
		return mcp.GitHubAuthConfig{}
	}
	secret, err := loadJSONSecret(ctx, secretName)
	if err != nil {
		// Log but don't fail — the endpoints just won't be available.
		fmt.Fprintf(os.Stderr, "cainban-lambda: failed to load GitHub OAuth secret %q: %v\n", secretName, err)
		return mcp.GitHubAuthConfig{}
	}
	return mcp.GitHubAuthConfig{
		ClientID:     secret["client_id"],
		ClientSecret: secret["client_secret"],
	}
}

// loadJSONSecret loads a JSON object secret from Secrets Manager and returns it
// as a map. Returns an error if the secret cannot be loaded or parsed.
func loadJSONSecret(ctx context.Context, secretName string) (map[string]string, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}
	client := secretsmanager.NewFromConfig(cfg)
	out, err := client.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
		SecretId: &secretName,
	})
	if err != nil {
		return nil, fmt.Errorf("get secret: %w", err)
	}
	if out.SecretString == nil || *out.SecretString == "" {
		return nil, fmt.Errorf("secret %q has no SecretString", secretName)
	}
	var result map[string]string
	if err := json.Unmarshal([]byte(*out.SecretString), &result); err != nil {
		return nil, fmt.Errorf("parse secret JSON: %w", err)
	}
	return result, nil
}
