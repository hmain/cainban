package auth

import (
	"context"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/hmain/cainban/src/systems/ghperm"
)

// repoKeys returns the authorized repo set as a slice for diagnostic logging.
func repoKeys(m map[string]struct{}) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}

// HeaderTargetRepo is the request header a client uses to name the repo it wants
// to act on (repo IDENTITY). It is UNTRUSTED: it only selects which repo, and is
// authorized against the validated token claim before any store is opened. An
// MCP tool arg can override it per call (see ResolveTarget), but neither is ever
// trusted for AUTHORIZATION.
const HeaderTargetRepo = "X-Cainban-Repo"

// UseGitHubPermissions controls whether authorization uses live GitHub API
// permission checks (true) or the legacy repos claim (false). Set via
// CAINBAN_USE_GITHUB_PERMISSIONS=true environment variable.
var UseGitHubPermissions = os.Getenv("CAINBAN_USE_GITHUB_PERMISSIONS") == "true"

// Resolver is the single request-time entry point: it validates the bearer
// token signature-first and resolves the authorized tenant. The transport calls
// Resolve once per request BEFORE opening any store.
type Resolver struct {
	validator   *Validator
	permChecker *ghperm.PermissionChecker // nil when UseGitHubPermissions is false
}

// NewResolver wraps a Validator.
func NewResolver(v *Validator) *Resolver {
	return &Resolver{validator: v}
}

// NewResolverWithGitHub creates a Resolver that uses GitHub for authorization.
// The permChecker is used when UseGitHubPermissions is true.
func NewResolverWithGitHub(v *Validator, permChecker *ghperm.PermissionChecker) *Resolver {
	return &Resolver{validator: v, permChecker: permChecker}
}

// Resolve is the full auth pipeline for one request:
//
//  1. Extract the bearer token from the Authorization header.
//  2. Validate it SIGNATURE-FIRST (Validator.Validate) — a bad/missing token is
//     a 401 here and returns before any repo/claim is considered.
//  3. Determine the TARGET repo (identity): the explicit argRepo (an MCP tool
//     arg) if non-empty, else the X-Cainban-Repo header, else the token's
//     default_repo claim.
//  4. AUTHORIZE the target: either via GitHub API (when UseGitHubPermissions is
//     true) or via the validated `repos` claim. A target the caller cannot
//     access is a 403.
//  5. Return a Tenant carrying the isolating partition prefix.
//
// When the caller names NO repo (no arg, no header) AND the token carries no
// default_repo, the request is authenticated but UNSCOPED: it returns a Tenant
// with Unscoped=true and empty Repo/PartitionPrefix. This lets the MCP handshake
// (initialize, tools/list, ping, notifications) succeed for a token that simply
// has no default repo, instead of a spurious 403 before the client can even list
// tools. An unscoped tenant MUST NOT open a data store — the store-opening path
// (resolveTaskSystem) fails closed on it. A repo that IS named but not granted is
// still a 403; only the absence of any target becomes unscoped.
//
// argRepo is the per-call target from an MCP tool argument; pass "" when the
// caller relies on the header/default. Both header and arg are UNTRUSTED for
// authorization — only membership in the validated claim (or GitHub API check)
// grants access.
func (r *Resolver) Resolve(req *http.Request, argRepo string) (*Tenant, error) {
	token, err := bearerToken(req)
	if err != nil {
		return nil, err // 401, no identity, no store
	}

	// Signature-first: nothing below runs unless this passes.
	identity, err := r.validator.Validate(token)
	if err != nil {
		return nil, err // 401
	}

	target, err := r.resolveTarget(identity, req, argRepo)
	if err != nil {
		log.Printf("cainban auth: 403 resolveTarget failed (sub=%q default_repo=%q repos=%v): %v", identity.Subject, identity.DefaultRepo, repoKeys(identity.Repos), err)
		return nil, err // 403 (an explicitly named target was invalid)
	}

	// No repo named and no default_repo: authenticated but unscoped. Valid only
	// for non-tenant handshake operations; the store path fails closed on it.
	if target == "" {
		return &Tenant{Subject: identity.Subject, Actor: actorFor(identity), AuthorizedRepos: identity.sortedRepos(), Unscoped: true}, nil
	}

	// Authorize the target repo
	if err := r.authorize(req.Context(), identity, target); err != nil {
		return nil, err // 401 or 403
	}

	return &Tenant{
		Repo:            target,
		PartitionPrefix: PartitionPrefixFor(target),
		Subject:         identity.Subject,
		Actor:           actorFor(identity),
		AuthorizedRepos: identity.sortedRepos(),
	}, nil
}

// authorize checks whether the identity is permitted to access the target repo.
// When UseGitHubPermissions is true, it uses the GitHub API (via PermissionChecker).
// Otherwise, it checks the legacy repos claim.
func (r *Resolver) authorize(ctx context.Context, identity *Identity, target string) error {
	if UseGitHubPermissions {
		return r.authorizeViaGitHub(ctx, identity, target)
	}
	return r.authorizeViaReposClaim(identity, target)
}

// authorizeViaGitHub checks GitHub API for write permission.
func (r *Resolver) authorizeViaGitHub(ctx context.Context, identity *Identity, target string) error {
	if r.permChecker == nil {
		log.Printf("cainban auth: GitHub permissions enabled but no PermissionChecker configured")
		return unauthenticated("authorization system misconfigured", nil)
	}
	if identity.GitHubUser == "" {
		log.Printf("cainban auth: 401 missing github_user claim (sub=%q)", identity.Subject)
		return unauthenticated("missing GitHub identity", nil)
	}
	if identity.GitHubToken == "" {
		log.Printf("cainban auth: 401 missing github_token claim (sub=%q)", identity.Subject)
		return unauthenticated("missing GitHub token", nil)
	}

	canWrite, err := r.permChecker.CanWrite(ctx, identity.GitHubUser, identity.GitHubToken, target)
	if err != nil {
		// Fail closed: any error denies access
		log.Printf("cainban auth: 401 GitHub permission check failed (sub=%q user=%q repo=%q): %v",
			identity.Subject, identity.GitHubUser, target, err)
		return unauthenticated("permission check failed", err)
	}
	if !canWrite {
		log.Printf("cainban auth: 403 GitHub denies write access (sub=%q user=%q repo=%q)",
			identity.Subject, identity.GitHubUser, target)
		return forbidden("caller not authorized for repo " + target)
	}
	return nil
}

// authorizeViaReposClaim checks the legacy repos claim from the JWT.
func (r *Resolver) authorizeViaReposClaim(identity *Identity, target string) error {
	if !identity.authorizes(target) {
		log.Printf("cainban auth: 403 not authorized for target=%q (sub=%q repos=%v)",
			target, identity.Subject, repoKeys(identity.Repos))
		return forbidden("caller not authorized for repo " + target)
	}
	return nil
}

// actorFor returns the human-readable caller id for audit: email when the
// token carried one, else the opaque subject. Never empty for a valid token.
func actorFor(id *Identity) string {
	if id.Email != "" {
		return id.Email
	}
	return id.Subject
}

// resolveTarget picks the repo IDENTITY (which repo), preferring an explicit MCP
// tool arg, then the request header, then the token's default_repo claim. It
// normalizes the value but does NOT authorize it (that is Resolve's job).
//
// A caller that supplies NOTHING and has no default_repo gets an empty string
// (not an error): Resolve turns that into an unscoped, authenticated tenant so
// the handshake can proceed. A value that IS supplied but is structurally
// invalid is still an error (403) — a malformed target must never be silently
// downgraded to unscoped, which would hide a client bug behind a working
// handshake.
func (r *Resolver) resolveTarget(id *Identity, req *http.Request, argRepo string) (string, error) {
	raw := strings.TrimSpace(argRepo)
	if raw == "" && req != nil {
		raw = strings.TrimSpace(req.Header.Get(HeaderTargetRepo))
	}
	if raw == "" {
		raw = strings.TrimSpace(id.DefaultRepo)
	}
	if raw == "" {
		return "", nil // nothing named anywhere -> unscoped (handled by Resolve)
	}
	norm, err := NormalizeRepo(raw)
	if err != nil {
		return "", forbidden("invalid target repo: " + err.Error())
	}
	return norm, nil
}

// bearerToken extracts the token from the Authorization: Bearer <token> header.
// A missing or malformed header is a 401.
func bearerToken(req *http.Request) (string, error) {
	if req == nil {
		return "", unauthenticated("no request", nil)
	}
	h := req.Header.Get("Authorization")
	if h == "" {
		return "", unauthenticated("missing Authorization header", nil)
	}
	const prefix = "Bearer "
	if len(h) <= len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return "", unauthenticated("Authorization header is not a Bearer token", nil)
	}
	return strings.TrimSpace(h[len(prefix):]), nil
}
