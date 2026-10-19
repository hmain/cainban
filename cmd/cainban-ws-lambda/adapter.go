package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/hmain/cainban/src/systems/auth"
	"github.com/hmain/cainban/src/systems/store"
)

// Auth env vars the CDK stack sets from the SAME Cognito user pool as the MCP
// Lambda, so a WebSocket $connect validates the exact tokens the MCP endpoint
// accepts. Mirrors cmd/cainban-lambda/adapter.go (kept as its own copy because
// package main cannot be imported across command dirs).
const (
	envAuthIssuer   = "CAINBAN_AUTH_ISSUER"   // e.g. https://cognito-idp.<region>.amazonaws.com/<poolId>
	envAuthAudience = "CAINBAN_AUTH_AUDIENCE" // comma-separated Cognito app client ids
	envAuthJWKSURL  = "CAINBAN_AUTH_JWKS_URL" // optional override; else <issuer>/.well-known/jwks.json
)

// buildResolver constructs the signature-first auth resolver from env. It fails
// (rather than accepting any connection) if issuer/audience are missing — there
// is no fail-open path.
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
// accepted app-client-id audiences (trimmed, non-empty), so one Cognito pool
// issuing tokens to several app clients (machine, SPA, CLI) is validated by one
// resolver. A single value yields a one-element list.
func splitAudiences(raw string) []string {
	out := []string{}
	for _, p := range strings.Split(raw, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
