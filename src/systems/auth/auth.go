// Package auth is cainban's Phase 3 authentication + repo-scoped authorization
// layer for the HTTP/Lambda path. It turns a bearer JWT into a validated caller
// identity and the ONE repo tenant that caller is authorized to touch.
//
// # Security ordering (non-negotiable)
//
// The whole point of this package is the ORDER in which checks run. For every
// request:
//
//  1. The JWT SIGNATURE is verified against the IdP's JWKS (RS256), and the
//     token's issuer, audience and expiry are validated. Only a token that
//     passes ALL of these is trusted at all.
//  2. ONLY AFTER (1) passes are claims read. The caller's authorized repos come
//     from a VALIDATED claim (a signed list of repos the token grants), never
//     from a header, the request body, or an unsigned source.
//  3. The repo the request TARGETS (identity: which repo) may be supplied by
//     the client (an MCP tool arg or a claim). The AUTHORIZATION (may this
//     caller touch that repo) is decided by intersecting the target against the
//     validated claim from (2). A target the claim does not grant is denied.
//
// A missing/invalid/expired/unsigned token is a 401 and NO tenant is resolved
// (so no store is ever opened). A valid token that does not grant the requested
// repo is a 403. No claim-based decision is ever made before signature
// validation — that would be a spoofing hole.
//
// # Identity vs authorization (where each comes from)
//
//   - Repo IDENTITY (which repo a request addresses): the client-supplied
//     target — an owner/repo pair passed per request (MCP tool arg / header),
//     falling back to a default_repo claim when the client sends none. This is
//     UNTRUSTED on its own: it only names a repo, it does not grant access.
//   - Repo AUTHORIZATION (may this caller touch that repo): the validated
//     `repos` claim inside the signature-checked JWT. Access is granted iff the
//     target repo is a member of that claim. This is the only source of truth
//     for authorization; the client-supplied target is never trusted alone.
package auth

import (
	"errors"
	"fmt"
	"strings"
)

// ErrorKind classifies an auth failure so the transport can map it to the right
// HTTP status without string matching.
type ErrorKind int

const (
	// KindUnauthenticated => HTTP 401. The token is missing, malformed, has a
	// bad signature, wrong issuer/audience, or is expired. No tenant resolved.
	KindUnauthenticated ErrorKind = iota
	// KindForbidden => HTTP 403. The token is valid but does not authorize the
	// requested repo.
	KindForbidden
)

// Error carries an ErrorKind so callers can select 401 vs 403 precisely.
type Error struct {
	Kind    ErrorKind
	Message string
	// Err is an optional wrapped cause (never surfaced to the client verbatim).
	Err error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Err)
	}
	return e.Message
}

func (e *Error) Unwrap() error { return e.Err }

// unauthenticated builds a 401 auth error.
func unauthenticated(msg string, cause error) *Error {
	return &Error{Kind: KindUnauthenticated, Message: msg, Err: cause}
}

// forbidden builds a 403 auth error.
func forbidden(msg string) *Error {
	return &Error{Kind: KindForbidden, Message: msg}
}

// HTTPStatus returns the HTTP status code for an error: 401 for an auth error
// of kind Unauthenticated, 403 for Forbidden, and 401 as the safe default for
// any non-auth error (fail closed — never fall through to a store).
func HTTPStatus(err error) int {
	var ae *Error
	if errors.As(err, &ae) {
		if ae.Kind == KindForbidden {
			return 403
		}
		return 401
	}
	return 401
}

// Identity is the validated caller. It is produced ONLY after signature
// validation has passed, so every field here is trustworthy.
type Identity struct {
	// Subject is the IdP `sub` claim — a stable per-user id.
	Subject string
	// Repos is the set of repos this token authorizes, from the validated
	// `repos` claim. Membership in this set is the authorization decision.
	Repos map[string]struct{}
	// DefaultRepo is the optional `default_repo` claim, used as the target when
	// the client supplies none. It still must be a member of Repos to be usable.
	DefaultRepo string
}

// authorizes reports whether the validated token grants the given repo.
func (id *Identity) authorizes(repo string) bool {
	_, ok := id.Repos[repo]
	return ok
}

// Tenant is the resolved, AUTHORIZED tenant for a request: the repo the caller
// targeted AND is allowed to touch, plus the DynamoDB partition prefix that
// isolates it. PartitionPrefix is what gets passed to dynamo.NewWithPrefix.
type Tenant struct {
	// Repo is the canonical "owner/repo" the request is scoped to.
	Repo string
	// PartitionPrefix is "REPO#<owner>/<repo>#". Every DynamoDB key for this
	// request is built under this prefix, so a request for repo A can never
	// address items under repo B's prefix — isolation is structural.
	PartitionPrefix string
	// Subject is the validated caller id (for logging/audit).
	Subject string
}

// PartitionPrefixFor returns the DynamoDB partition prefix for a repo. It is the
// single definition of how a repo maps to its partition, shared by production
// resolution and tests so they cannot drift.
func PartitionPrefixFor(repo string) string {
	return fmt.Sprintf("REPO#%s#", repo)
}

// NormalizeRepo canonicalizes an "owner/repo" identifier: trims spaces and a
// leading "REPO#"/trailing "#" if a caller passed the raw prefix form, and
// lowercases nothing (repo names are case-sensitive on the forge). It returns
// an error for an empty or structurally invalid value so a bad target cannot
// silently collapse two repos into one partition.
func NormalizeRepo(raw string) (string, error) {
	r := strings.TrimSpace(raw)
	r = strings.TrimPrefix(r, "REPO#")
	r = strings.TrimSuffix(r, "#")
	r = strings.TrimSpace(r)
	if r == "" {
		return "", fmt.Errorf("empty repo identifier")
	}
	// Must be exactly owner/repo: one slash, both sides non-empty, no path
	// traversal or control characters that could break the key namespace.
	parts := strings.Split(r, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", fmt.Errorf("repo %q must be in owner/repo form", raw)
	}
	if strings.ContainsAny(r, "\x00#") || strings.Contains(r, "..") {
		return "", fmt.Errorf("repo %q contains invalid characters", raw)
	}
	return r, nil
}
