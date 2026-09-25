// Package github is cainban's GitHub App integration (Phase 4, step 2). It
// exists for ONE job in the connect flow: to VERIFY, server-side, that a
// principal (a human, or later an agent) genuinely has access to an
// "owner/repo" BEFORE a grant is ever written for it. It never trusts a
// client-supplied "I have access" claim — the answer is always the result of a
// real GitHub API call made with the App's own credentials.
//
// # Why a GitHub App (not an OAuth App)
//
// Phase 4 locked a GitHub App: fine-grained per-repo permissions, ORG installs
// authorized by an org admin, and a webhook-ready surface for a later sync
// phase. Concretely, an App authenticates in two hops:
//
//  1. App JWT: a short-lived RS256 JWT signed with the App's PRIVATE KEY,
//     asserting the App id (`iss`). It authenticates the App ITSELF (not any
//     user), and is used only against the App-level endpoints below.
//  2. Installation token: exchanging the App JWT at
//     POST /app/installations/{installation_id}/access_tokens yields a
//     short-lived INSTALLATION token scoped to what the org/user granted the
//     App on install. Repo-coverage checks are made with this token.
//
// The connecting principal's OWN identity (their GitHub login, obtained via the
// P4.3 OAuth leg) is what decides membership/collaborator entitlement. P4.2
// accepts that identity as a parameter (see Principal) — P4.3 wires the OAuth
// leg that fills it.
//
// # The mockable seam
//
// Every outbound GitHub call this package makes goes through the Doer interface
// (a one-method http.Client shape). Production uses a real *http.Client; tests
// inject an in-memory fake that returns canned responses, so NO LIVE GITHUB
// CALL is ever made in code paths exercised by unit tests or CI. The Client is
// the only thing that talks to GitHub; VerifyRepoAccess is expressed purely in
// terms of Client method calls, so a test can also mock the Client interface
// directly.
//
// # Fail-closed contract (non-negotiable)
//
// On ANY error from a GitHub call (network, non-2xx, decode), VerifyRepoAccess
// returns (false, err). The caller (the P4.3 connect API) treats a non-nil
// error as "DENY" — an error must NEVER read as "access granted". Access is
// granted only when every required check returned an affirmative 2xx.
//
// It is PURE GO (no CGO): App-JWT minting uses crypto/rsa + crypto/sha256 +
// encoding/pem from the standard library (no third-party JWT dependency), so
// the go directive does not move and the CI Go pin is unchanged.
package github

import (
	"net/http"
	"time"
)

// apiBase is the GitHub REST API root. It is a package var (not a const) so a
// test's fake Doer sees the same value and, if ever needed, a GitHub Enterprise
// base could be injected — but P4.2 targets github.com only.
var apiBase = "https://api.github.com"

// Doer is the minimal HTTP surface the Client depends on — exactly
// http.Client.Do. Depending on this one-method interface (rather than a
// concrete *http.Client) is the mock seam for the token/exchange calls: tests
// inject a fake Doer that returns canned *http.Response values with no network.
type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Principal is the connecting GitHub identity whose entitlement to a repo is
// being verified. In P4.2 it is supplied by the caller; P4.3 fills Login from
// the user's OAuth leg. It is deliberately a value the SERVER learned from
// GitHub (the OAuth'd identity), never a raw client claim of "I am X".
type Principal struct {
	// Login is the principal's GitHub login (username). Required — an empty
	// login cannot be verified and VerifyRepoAccess denies it.
	Login string
}

// AppConfig is the GitHub App's credentials, loaded at runtime from Secrets
// Manager (see package secrets). None of these values ever live in code, the
// repo, or CDK — the CDK only creates a PLACEHOLDER secret the operator fills.
type AppConfig struct {
	// AppID is the numeric GitHub App id (the `iss` of the App JWT).
	AppID int64
	// PrivateKeyPEM is the App's RSA private key in PEM form (PKCS#1 or PKCS#8).
	PrivateKeyPEM []byte
	// ClientID / ClientSecret are the App's OAuth client credentials, used by
	// the P4.3 OAuth leg (not by P4.2's verification calls). Carried here so the
	// single secret holds everything the App needs.
	ClientID     string
	ClientSecret string
}

// jwtTTL is the App-JWT lifetime. GitHub rejects an App JWT with more than 10
// minutes of validity; a short TTL bounds replay. We also back-date `iat` by a
// small skew to tolerate clock drift between us and GitHub.
const (
	jwtTTL       = 9 * time.Minute
	jwtClockSkew = 30 * time.Second
)
