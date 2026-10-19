package mcp

import (
	"fmt"
	"os"
	"strings"

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
//
// This implements Mode 2 (local identity from env) first; Mode 1 (scoped token)
// is added in Phase 2.
func localTenantFromEnv() (*auth.Tenant, error) {
	// Phase 2 will check CAINBAN_TOKEN here first
	// For now, skip to Mode 2

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
