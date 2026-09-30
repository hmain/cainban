package auth

import (
	"log"
	"net/http"
	"strings"
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

// Resolver is the single request-time entry point: it validates the bearer
// token signature-first and resolves the authorized tenant. The transport calls
// Resolve once per request BEFORE opening any store.
type Resolver struct {
	validator *Validator
}

// NewResolver wraps a Validator.
func NewResolver(v *Validator) *Resolver {
	return &Resolver{validator: v}
}

// Resolve is the full auth pipeline for one request:
//
//  1. Extract the bearer token from the Authorization header.
//  2. Validate it SIGNATURE-FIRST (Validator.Validate) — a bad/missing token is
//     a 401 here and returns before any repo/claim is considered.
//  3. Determine the TARGET repo (identity): the explicit argRepo (an MCP tool
//     arg) if non-empty, else the X-Cainban-Repo header, else the token's
//     default_repo claim.
//  4. AUTHORIZE the target against the validated `repos` claim. A target the
//     token does not grant is a 403.
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
// authorization — only membership in the validated claim grants access.
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
		return &Tenant{Subject: identity.Subject, Unscoped: true}, nil
	}

	if !identity.authorizes(target) {
		log.Printf("cainban auth: 403 not authorized for target=%q (sub=%q repos=%v)", target, identity.Subject, repoKeys(identity.Repos))
		return nil, forbidden("caller not authorized for repo " + target)
	}

	return &Tenant{
		Repo:            target,
		PartitionPrefix: PartitionPrefixFor(target),
		Subject:         identity.Subject,
	}, nil
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
