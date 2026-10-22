package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"

	"github.com/hmain/cainban/src/systems/auth"
	"github.com/hmain/cainban/src/systems/mcp"
	"github.com/hmain/cainban/src/systems/store"
)

const (
	// envMcpResource is the MCP API's canonical URL (McpApiUrl). The CDK stack
	// sets it from the HTTP API's own Url() output. It is the RFC 8707 resource
	// indicator and the `resource` field of the RFC 9728 protected-resource
	// metadata document, and the base for the WWW-Authenticate resource_metadata
	// pointer. Optional: when unset the discovery document/challenge degrade
	// gracefully (empty resource / legacy realm challenge) rather than failing.
	envMcpResource = "CAINBAN_MCP_RESOURCE"
	// envGitHubOAuthSecretName is the Secrets Manager secret name holding the
	// GitHub OAuth credentials (client_id, client_secret) for the device flow.
	// Optional: when unset the GitHub auth endpoints are not mounted.
	envGitHubOAuthSecretName = "CAINBAN_GITHUB_OAUTH_SECRET_NAME"
)

// resourceMetadataConfig builds the RFC 9728 protected-resource metadata config
// from env: the canonical MCP URL (CAINBAN_MCP_RESOURCE, trailing slash trimmed)
// as the resource. The authorization-server field is left empty under the
// GitHub-only model — there is no OAuth authorization server to discover (the
// RFC 8414 Cognito shim was removed); the document and the 401 challenge still
// carry the `resource` indicator.
func resourceMetadataConfig() mcp.ResourceMetadataConfig {
	return mcp.ResourceMetadataConfig{
		Resource: strings.TrimRight(strings.TrimSpace(os.Getenv(envMcpResource)), "/"),
	}
}

// clientConfigConfig builds the cainban MCP client auto-config document config
// from env: just the canonical MCP URL (CAINBAN_MCP_RESOURCE). Under GitHub-only
// there is no OAuth client id / authorize-token endpoint to advertise.
func clientConfigConfig() mcp.ClientConfigConfig {
	return mcp.ClientConfigConfig{
		MCPURL: strings.TrimRight(strings.TrimSpace(os.Getenv(envMcpResource)), "/"),
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
