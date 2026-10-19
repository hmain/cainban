package mcp

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/hmain/cainban/src/systems/auth"
)

// Environment variables for local (stdio) credential mode, per MCP spec:
// "Implementations using an STDIO transport SHOULD NOT follow [OAuth] and
// instead retrieve credentials from the environment."
const (
	// EnvLocalRepo is the repo this local server is authorized for (Mode 2).
	// Format: "owner/repo". When set, the server creates a tenant scoped to
	// this single repo.
	EnvLocalRepo = "CAINBAN_REPO"

	// EnvLocalActor is the actor identity for the activity feed. Optional;
	// defaults to "local" when not set.
	EnvLocalActor = "CAINBAN_ACTOR"

	// EnvLocalToken is a scoped token that authorizes specific repos (Mode 1).
	// Takes precedence over EnvLocalRepo when set.
	EnvLocalToken = "CAINBAN_TOKEN"

	// EnvLocalTokenKey is the HMAC-SHA256 signing key for local tokens.
	// Required when CAINBAN_TOKEN is set.
	EnvLocalTokenKey = "CAINBAN_TOKEN_KEY"
)

// localTenantFromEnv reads local credentials from the environment and returns a
// tenant if configured. Returns (nil, nil) when no local credential is set,
// indicating single-tenant mode (the existing stdio behavior). Returns an error
// for invalid configuration (e.g. malformed repo).
//
// Precedence:
//  1. CAINBAN_TOKEN — if set, validate and extract repos/identity from it.
//  2. CAINBAN_REPO — if set (and no token), use as the single authorized repo.
//  3. Neither — return nil (single-tenant mode).
func localTenantFromEnv() (*auth.Tenant, error) {
	// Mode 1: CAINBAN_TOKEN (takes precedence)
	token := strings.TrimSpace(os.Getenv(EnvLocalToken))
	if token != "" {
		keyStr := os.Getenv(EnvLocalTokenKey)
		if keyStr == "" {
			return nil, fmt.Errorf("%s is set but %s is missing", EnvLocalToken, EnvLocalTokenKey)
		}
		// Try to decode as base64 first (for env-var-friendly format)
		key, err := base64.StdEncoding.DecodeString(keyStr)
		if err != nil {
			// Fall back to raw bytes (for backward compat / file-based keys)
			key = []byte(keyStr)
		}
		return validateLocalToken(token, key)
	}

	// Mode 2: CAINBAN_REPO
	repo := strings.TrimSpace(os.Getenv(EnvLocalRepo))
	if repo == "" {
		// No local credential configured — single-tenant mode
		return nil, nil
	}

	// Validate repo format
	normalized, err := auth.NormalizeRepo(repo)
	if err != nil {
		return nil, fmt.Errorf("invalid %s: %w", EnvLocalRepo, err)
	}

	actor := strings.TrimSpace(os.Getenv(EnvLocalActor))
	if actor == "" {
		actor = "local"
	}

	return &auth.Tenant{
		Repo:            normalized,
		PartitionPrefix: auth.PartitionPrefixFor(normalized),
		Subject:         "local",
		Actor:           actor,
		AuthorizedRepos: []string{normalized},
		Unscoped:        false,
	}, nil
}

// localTenantForStdio returns the tenant for a stdio session. It reads
// credentials from the environment and, if configured, returns a tenant that
// will be injected into every tool-call context. If no local credential is set,
// it returns nil and the server runs in single-tenant mode (existing behavior).
//
// This is the entry point called by Server.Start() for the stdio path.
func localTenantForStdio() (*auth.Tenant, error) {
	return localTenantFromEnv()
}


// =============================================================================
// Local Token (Mode 1): HMAC-SHA256 signed JWT
// =============================================================================

// localTokenClaims is the payload of a local token.
type localTokenClaims struct {
	Subject string   `json:"sub"`          // identity (email or username)
	Repos   []string `json:"repos"`        // authorized repos ["owner/repo", ...]
	Expiry  int64    `json:"exp"`          // Unix timestamp
	Issuer  string   `json:"iss,omitempty"` // always "cainban-local"
}

// b64url is base64url without padding, per JWT spec.
var b64url = base64.RawURLEncoding

// MintLocalToken creates a signed local token (HS256 JWT) for the given subject
// and repos, valid for the given duration from now. Exported for use by the
// `cainban token` CLI command.
func MintLocalToken(key []byte, subject string, repos []string, ttl time.Duration) (string, error) {
	if len(key) == 0 {
		return "", fmt.Errorf("empty signing key")
	}
	if strings.TrimSpace(subject) == "" {
		return "", fmt.Errorf("subject is required")
	}

	// Normalize repos
	normalizedRepos := make([]string, 0, len(repos))
	for _, r := range repos {
		if norm, err := auth.NormalizeRepo(r); err == nil {
			normalizedRepos = append(normalizedRepos, norm)
		}
	}

	header := map[string]string{"alg": "HS256", "typ": "JWT"}
	headerJSON, _ := json.Marshal(header)

	claims := localTokenClaims{
		Subject: subject,
		Repos:   normalizedRepos,
		Expiry:  time.Now().Add(ttl).Unix(),
		Issuer:  "cainban-local",
	}
	claimsJSON, _ := json.Marshal(claims)

	signingInput := b64url.EncodeToString(headerJSON) + "." + b64url.EncodeToString(claimsJSON)

	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(signingInput))
	sig := mac.Sum(nil)

	return signingInput + "." + b64url.EncodeToString(sig), nil
}

// validateLocalToken validates a local token and returns the tenant.
func validateLocalToken(token string, key []byte) (*auth.Tenant, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid token format: expected 3 JWT segments")
	}
	headerSeg, payloadSeg, sigSeg := parts[0], parts[1], parts[2]

	// Verify header is HS256
	headerBytes, err := b64url.DecodeString(headerSeg)
	if err != nil {
		return nil, fmt.Errorf("invalid token header: %w", err)
	}
	var header struct {
		Alg string `json:"alg"`
		Typ string `json:"typ"`
	}
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return nil, fmt.Errorf("invalid token header: %w", err)
	}
	if header.Alg != "HS256" {
		return nil, fmt.Errorf("invalid token: unsupported algorithm %q (want HS256)", header.Alg)
	}

	// Verify signature FIRST (before trusting any claims)
	signingInput := headerSeg + "." + payloadSeg
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(signingInput))
	expectedSig := mac.Sum(nil)

	actualSig, err := b64url.DecodeString(sigSeg)
	if err != nil {
		return nil, fmt.Errorf("invalid token signature encoding: %w", err)
	}
	if !hmac.Equal(expectedSig, actualSig) {
		return nil, fmt.Errorf("invalid token: signature mismatch")
	}

	// Signature valid — now parse claims
	payloadBytes, err := b64url.DecodeString(payloadSeg)
	if err != nil {
		return nil, fmt.Errorf("invalid token payload: %w", err)
	}
	var claims localTokenClaims
	if err := json.Unmarshal(payloadBytes, &claims); err != nil {
		return nil, fmt.Errorf("invalid token payload: %w", err)
	}

	// Check expiry
	if claims.Expiry == 0 {
		return nil, fmt.Errorf("invalid token: missing exp")
	}
	if time.Now().After(time.Unix(claims.Expiry, 0)) {
		return nil, fmt.Errorf("token expired")
	}

	// Check subject
	if strings.TrimSpace(claims.Subject) == "" {
		return nil, fmt.Errorf("invalid token: missing sub")
	}

	// Check repos
	if len(claims.Repos) == 0 {
		return nil, fmt.Errorf("invalid token: no repos claim")
	}

	// Normalize repos
	normalizedRepos := make([]string, 0, len(claims.Repos))
	for _, r := range claims.Repos {
		if norm, err := auth.NormalizeRepo(r); err == nil {
			normalizedRepos = append(normalizedRepos, norm)
		}
	}
	if len(normalizedRepos) == 0 {
		return nil, fmt.Errorf("invalid token: no valid repos in claim")
	}

	// Use first repo as the primary repo
	primaryRepo := normalizedRepos[0]

	return &auth.Tenant{
		Repo:            primaryRepo,
		PartitionPrefix: auth.PartitionPrefixFor(primaryRepo),
		Subject:         claims.Subject,
		Actor:           claims.Subject,
		AuthorizedRepos: normalizedRepos,
		Unscoped:        false,
	}, nil
}
