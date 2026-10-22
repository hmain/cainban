package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"

	"github.com/hmain/cainban/src/systems/auth"
	"github.com/hmain/cainban/src/systems/github"
	"github.com/hmain/cainban/src/systems/store"
)

// envGitHubAppSecretName is the Secrets Manager secret holding the GitHub App
// credentials used for App-based authorization at $connect. Mirrors the MCP
// Lambda (kept as its own copy because package main cannot be imported across
// command dirs).
const envGitHubAppSecretName = "CAINBAN_GITHUB_APP_SECRET_NAME"

// buildResolver constructs the GitHub-only auth resolver from env. $connect
// validates the caller's GitHub token for identity and authorizes the target
// repo via the GitHub App installation token. The App secret is REQUIRED — a
// missing/broken secret is fatal at cold start, never an endpoint that accepts
// any connection.
func buildResolver() (*auth.Resolver, error) {
	secretName := strings.TrimSpace(os.Getenv(envGitHubAppSecretName))
	if secretName == "" {
		return nil, fmt.Errorf("%s must be set: GitHub App authorization is required", envGitHubAppSecretName)
	}
	secret, err := loadJSONSecret(context.Background(), secretName)
	if err != nil {
		return nil, fmt.Errorf("load GitHub App secret %q: %w", secretName, err)
	}
	v, err := appVerifierFromSecret(secret)
	if err != nil {
		return nil, fmt.Errorf("invalid GitHub App secret %q: %w", secretName, err)
	}
	// Validator is nil: GitHub-only (identity from the GitHub bearer token).
	return auth.NewResolverWithApp(nil, v), nil
}

// appVerifier adapts a *github.Verifier to auth.RepoVerifier (login string ->
// github.Principal), breaking the auth<->github import cycle. Mirror of the MCP
// Lambda's adapter.
type appVerifier struct {
	v *github.Verifier
}

func (a *appVerifier) VerifyRepoAccess(ctx context.Context, owner, repo, login string) (bool, error) {
	return a.v.VerifyRepoAccess(ctx, owner, repo, github.Principal{Login: login})
}

// appVerifierFromSecret builds the App RepoVerifier from the cainban/github-app
// secret map. A malformed secret (non-numeric app_id, missing key) is an error
// so it fails at cold start.
func appVerifierFromSecret(secret map[string]string) (*appVerifier, error) {
	appIDRaw := strings.TrimSpace(secret["app_id"])
	if appIDRaw == "" {
		return nil, fmt.Errorf("github app secret: missing app_id")
	}
	appID, err := strconv.ParseInt(appIDRaw, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("github app secret: app_id %q is not numeric: %w", appIDRaw, err)
	}
	key := strings.TrimSpace(secret["private_key"])
	if key == "" {
		return nil, fmt.Errorf("github app secret: missing private_key")
	}
	client, err := github.NewClient(github.AppConfig{
		AppID:         appID,
		PrivateKeyPEM: []byte(key),
		ClientID:      strings.TrimSpace(secret["client_id"]),
		ClientSecret:  strings.TrimSpace(secret["client_secret"]),
	}, &http.Client{Timeout: 10 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("github app secret: build client: %w", err)
	}
	return &appVerifier{v: github.NewVerifier(client)}, nil
}

// loadJSONSecret loads a JSON object secret from Secrets Manager as a map.
func loadJSONSecret(ctx context.Context, secretName string) (map[string]string, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}
	client := secretsmanager.NewFromConfig(cfg)
	out, err := client.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{SecretId: &secretName})
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

// ensureDynamoBackend forces CAINBAN_BACKEND=dynamodb for this process. Returns
// the previous value (for completeness / testability).
func ensureDynamoBackend() string {
	prev := os.Getenv(store.EnvBackend)
	_ = os.Setenv(store.EnvBackend, store.BackendDynamoDB)
	return prev
}
