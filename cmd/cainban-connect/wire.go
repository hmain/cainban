package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"

	"github.com/hmain/cainban/src/systems/auth"
	"github.com/hmain/cainban/src/systems/connect"
	"github.com/hmain/cainban/src/systems/github"
	"github.com/hmain/cainban/src/systems/grants"
	"github.com/hmain/cainban/src/systems/secrets"
)

// Env var names the CDK stack sets. Auth vars are shared with the MCP Lambda so
// both validate against the same Cognito pool.
const (
	envAuthIssuer   = "CAINBAN_AUTH_ISSUER"
	envAuthAudience = "CAINBAN_AUTH_AUDIENCE"
	envAuthJWKSURL  = "CAINBAN_AUTH_JWKS_URL"

	envGrantsTable  = "CAINBAN_GRANTS_TABLE"
	envGrantsRegion = "CAINBAN_GRANTS_REGION"

	envRedirectURI = "CAINBAN_CONNECT_REDIRECT_URI"
	envSuccessURL  = "CAINBAN_CONNECT_SUCCESS_URL"
)

// buildHandler constructs the fully-wired connect handler from env + Secrets
// Manager. It fails (never serves fail-open) on any missing required config.
func buildHandler(ctx context.Context) (http.Handler, error) {
	// --- signature-first auth validator (same Cognito pool as the MCP Lambda) ---
	issuer := strings.TrimSpace(os.Getenv(envAuthIssuer))
	audience := strings.TrimSpace(os.Getenv(envAuthAudience))
	if issuer == "" || audience == "" {
		return nil, fmt.Errorf("both %s and %s must be set (no unauthenticated endpoint)", envAuthIssuer, envAuthAudience)
	}
	jwksURL := strings.TrimSpace(os.Getenv(envAuthJWKSURL))
	if jwksURL == "" {
		jwksURL = strings.TrimRight(issuer, "/") + "/.well-known/jwks.json"
	}
	validator, err := auth.NewValidator(auth.Config{
		Issuer:   issuer,
		Audience: audience,
		Keys:     auth.NewJWKSCache(jwksURL),
	})
	if err != nil {
		return nil, err
	}

	// --- GitHub App credentials from Secrets Manager (runtime, never code) ---
	loader, err := secrets.NewFromEnv(ctx)
	if err != nil {
		return nil, err
	}
	appCfg, err := loader.Load(ctx)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(appCfg.ClientID) == "" || strings.TrimSpace(appCfg.ClientSecret) == "" {
		return nil, fmt.Errorf("connect: GitHub App client_id/client_secret not set in secret (needed for the OAuth leg)")
	}

	// A shared HTTP client with a timeout for all outbound GitHub calls.
	httpClient := &http.Client{Timeout: 10 * time.Second}

	// --- verifier (App JWT + installation token + membership/collaborator) ---
	ghClient, err := github.NewClient(appCfg, httpClient)
	if err != nil {
		return nil, err
	}
	verifier := github.NewVerifier(ghClient)

	// --- OAuth leg (code<->login), credentials from the same secret ---
	oauth := github.NewOAuth(github.OAuthConfig{
		ClientID:     appCfg.ClientID,
		ClientSecret: appCfg.ClientSecret,
		RedirectURI:  strings.TrimSpace(os.Getenv(envRedirectURI)),
	}, httpClient)

	// --- grants store (read + write) ---
	grantsTable := strings.TrimSpace(os.Getenv(envGrantsTable))
	if grantsTable == "" {
		return nil, fmt.Errorf("%s must be set", envGrantsTable)
	}
	var optFns []func(*awsconfig.LoadOptions) error
	if region := strings.TrimSpace(os.Getenv(envGrantsRegion)); region != "" {
		optFns = append(optFns, awsconfig.WithRegion(region))
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, optFns...)
	if err != nil {
		return nil, fmt.Errorf("connect: load AWS config: %w", err)
	}
	grantStore := grants.New(dynamodb.NewFromConfig(awsCfg), grantsTable)

	// --- anti-CSRF state signer: key derived from the OAuth client secret ---
	// The client secret lives only in Secrets Manager; deriving the HMAC key
	// from it via SHA-256 keeps the state key out of code/env and rotates with
	// the secret. It is a one-way derivation, so the state key never exposes the
	// client secret.
	stateKey := sha256.Sum256([]byte("cainban-connect-state|" + appCfg.ClientSecret))
	stateSigner, err := connect.NewStateSigner(stateKey[:])
	if err != nil {
		return nil, err
	}

	return connect.NewHandler(connect.Config{
		Auth:            validator,
		OAuth:           oauth,
		Verify:          verifier,
		Grants:          grantStore,
		State:           stateSigner,
		SuccessRedirect: strings.TrimSpace(os.Getenv(envSuccessURL)),
	})
}
