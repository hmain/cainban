package github

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/hmain/cainban/src/systems/auth"
)

// Verifier answers "does this principal genuinely have access to owner/repo?"
// against the GitHub App API. It is expressed ENTIRELY in terms of the Client
// interface, so it is unit-tested with a mocked Client and never touches the
// network in tests. It holds no secrets — the Client it wraps owns the App
// credentials.
type Verifier struct {
	client Client
}

// NewVerifier wraps a Client (real or mock).
func NewVerifier(client Client) *Verifier {
	return &Verifier{client: client}
}

// VerifyRepoAccess is the core P4.2 check. It returns true ONLY when BOTH hold,
// each proved by a server-side GitHub API result (never a client claim):
//
//  1. COVERAGE — the App installation covers owner/repo. Proved by
//     GET /repos/{owner}/{repo}/installation returning the installation id
//     (a 404 => not installed => deny).
//  2. ENTITLEMENT — the connecting principal is entitled to the repo. For an
//     ORG repo (the required case): the principal is an org MEMBER
//     (GET /orgs/{org}/members/{login} => 204) OR at least a repo COLLABORATOR
//     with read+ permission
//     (GET /repos/{owner}/{repo}/collaborators/{login}/permission != "none").
//     The org check is made first because org membership is the primary
//     entitlement for org repos; collaborator permission is the fallback that
//     also covers a user-owned repo and outside collaborators.
//
// The full call sequence behind the interface:
//
//  1. GET  /repos/{owner}/{repo}/installation                       (App JWT)   -> coverage / installation id
//  2. POST /app/installations/{id}/access_tokens                    (App JWT)   -> installation token
//  3. GET  /orgs/{owner}/members/{login}                            (inst tok)  -> org membership (204/404)
//  4. GET  /repos/{owner}/{repo}/collaborators/{login}/permission   (inst tok)  -> collaborator permission (fallback)
//
// FAIL CLOSED: any transport/decode/unexpected-status error from a GitHub call
// returns (false, err). The caller MUST treat a non-nil error as DENY — an
// error must never read as "access granted". A definitive negative (not
// installed, not a member and not a collaborator) returns (false, nil).
//
// owner/repo are canonicalized with auth.NormalizeRepo (the same normalization
// grants and the resolver use) so verification, the grant written, and the
// claim authorized against are all the SAME string.
func (v *Verifier) VerifyRepoAccess(ctx context.Context, owner, repo string, principal Principal) (bool, error) {
	// Canonicalize via the shared normalizer, then re-split. This rejects a
	// malformed target before any GitHub call and guarantees the owner/repo we
	// query GitHub with is exactly the string a grant would be keyed by.
	canon, err := auth.NormalizeRepo(owner + "/" + repo)
	if err != nil {
		return false, fmt.Errorf("github: verify repo access: %w", err)
	}
	parts := strings.SplitN(canon, "/", 2)
	nOwner, nRepo := parts[0], parts[1]

	login := strings.TrimSpace(principal.Login)
	if login == "" {
		// No verifiable identity => cannot prove entitlement => deny (not an
		// error: it is a definitive "no", the caller should surface a
		// link-required / re-auth path rather than a 500).
		return false, nil
	}

	// (1) COVERAGE: is the App installed on owner/repo?
	instID, err := v.client.RepoInstallationID(ctx, nOwner, nRepo)
	if err != nil {
		if errors.Is(err, ErrNotInstalled) {
			// Not installed — definitive deny, no error.
			return false, nil
		}
		return false, err // fail closed
	}

	// (2) An installation token, needed to query membership/collaborator with
	// the App's granted scope.
	instToken, err := v.client.InstallationToken(ctx, instID)
	if err != nil {
		return false, err // fail closed
	}

	// (3) ENTITLEMENT — org membership first (primary path for org repos).
	member, err := v.client.IsOrgMember(ctx, instToken, nOwner, login)
	if err != nil {
		return false, err // fail closed
	}
	if member {
		return true, nil
	}

	// (4) ENTITLEMENT fallback — repo collaborator with read+ permission. This
	// covers outside collaborators on an org repo and user-owned repos (where
	// there is no org to be a member of).
	perm, err := v.client.CollaboratorPermission(ctx, instToken, nOwner, nRepo, login)
	if err != nil {
		return false, err // fail closed
	}
	if hasReadAccess(perm) {
		return true, nil
	}

	// Installed but the principal is neither an org member nor a collaborator:
	// definitive deny.
	return false, nil
}

// hasReadAccess reports whether a collaborator permission string grants at
// least read access. GitHub returns "admin", "write", "read", or "none"; only
// "none" (or an unrecognized/empty value) is denied.
func hasReadAccess(permission string) bool {
	switch strings.ToLower(strings.TrimSpace(permission)) {
	case "admin", "write", "maintain", "triage", "read":
		return true
	default:
		return false
	}
}
