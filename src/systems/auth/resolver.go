package auth

import (
	"net/http"
	"strings"
)

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
		return nil, err // 403 (no repo the caller may touch)
	}

	if !identity.authorizes(target) {
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
// normalizes the value but does NOT authorize it (that is Resolve's job). A
// caller that supplies nothing and has no default_repo gets a 403 (there is no
// repo to scope to) rather than silently touching some other tenant.
func (r *Resolver) resolveTarget(id *Identity, req *http.Request, argRepo string) (string, error) {
	raw := strings.TrimSpace(argRepo)
	if raw == "" && req != nil {
		raw = strings.TrimSpace(req.Header.Get(HeaderTargetRepo))
	}
	if raw == "" {
		raw = id.DefaultRepo
	}
	if strings.TrimSpace(raw) == "" {
		return "", forbidden("no target repo supplied and token has no default_repo")
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
