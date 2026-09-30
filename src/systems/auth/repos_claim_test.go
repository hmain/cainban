package auth

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

// --- reposClaim.UnmarshalJSON: every on-the-wire shape -----------------------

// TestReposClaim_Shapes covers the five shapes the `repos` claim can take on the
// wire. The native array is what the self-signed test tokens use; the string
// shapes are what a real Cognito pre-token-generation trigger emits (Cognito
// claim-override values are always strings). All must decode to the same set.
func TestReposClaim_Shapes(t *testing.T) {
	cases := []struct {
		name string
		// raw is the raw JSON value of the "repos" field (what appears after the
		// colon in the token payload).
		raw  string
		want []string
	}{
		{"native array", `["acme/repo-a","acme/repo-b"]`, []string{"acme/repo-a", "acme/repo-b"}},
		{"json-array-encoded string (trigger output)", `"[\"acme/repo-a\",\"acme/repo-b\"]"`, []string{"acme/repo-a", "acme/repo-b"}},
		{"space-delimited string", `"acme/repo-a acme/repo-b"`, []string{"acme/repo-a", "acme/repo-b"}},
		{"comma-delimited string", `"acme/repo-a,acme/repo-b"`, []string{"acme/repo-a", "acme/repo-b"}},
		{"comma+space-delimited string", `"acme/repo-a, acme/repo-b"`, []string{"acme/repo-a", "acme/repo-b"}},
		{"single native", `["acme/repo-a"]`, []string{"acme/repo-a"}},
		{"single string", `"acme/repo-a"`, []string{"acme/repo-a"}},
		{"empty array", `[]`, nil},
		{"empty string", `""`, nil},
		{"whitespace-only string", `"   "`, nil},
		{"null", `null`, nil},
		{"garbage string (no grants, not an error)", `"not-a-repo but tokens"`, []string{"not-a-repo", "but", "tokens"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var rc reposClaim
			if err := json.Unmarshal([]byte(tc.raw), &rc); err != nil {
				t.Fatalf("UnmarshalJSON(%s) unexpected error: %v", tc.raw, err)
			}
			if len(rc) != len(tc.want) {
				t.Fatalf("UnmarshalJSON(%s) = %#v, want %#v", tc.raw, []string(rc), tc.want)
			}
			for i := range tc.want {
				if rc[i] != tc.want[i] {
					t.Errorf("UnmarshalJSON(%s)[%d] = %q, want %q", tc.raw, i, rc[i], tc.want[i])
				}
			}
		})
	}
}

// TestReposClaim_AbsentField proves an ABSENT repos field decodes to no grants
// (the field is simply not present in the payload object).
func TestReposClaim_AbsentField(t *testing.T) {
	type payload struct {
		Repos reposClaim `json:"repos"`
	}
	var p payload
	if err := json.Unmarshal([]byte(`{"sub":"user-1"}`), &p); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(p.Repos) != 0 {
		t.Errorf("absent repos = %#v, want empty", []string(p.Repos))
	}
}

// TestReposClaim_InTokenClaims proves the WHOLE claims struct decodes each shape
// through the same json.Unmarshal path Validate uses (not just the field type in
// isolation), so a real token payload is parsed correctly.
func TestReposClaim_InTokenClaims(t *testing.T) {
	// JSON-array-encoded string is the shape the trigger emits.
	payload := `{"iss":"i","sub":"s","repos":"[\"acme/repo-a\",\"acme/repo-b\"]","default_repo":"acme/repo-a"}`
	var c claims
	if err := json.Unmarshal([]byte(payload), &c); err != nil {
		t.Fatalf("unmarshal claims: %v", err)
	}
	if len(c.Repos) != 2 || c.Repos[0] != "acme/repo-a" || c.Repos[1] != "acme/repo-b" {
		t.Fatalf("repos = %#v, want [acme/repo-a acme/repo-b]", []string(c.Repos))
	}
	if c.DefaultRepo != "acme/repo-a" {
		t.Errorf("default_repo = %q, want acme/repo-a", c.DefaultRepo)
	}
}

// --- end-to-end: trigger output shape flows through the resolver -------------

// stringReposClaims mirrors tokenClaims but encodes `repos` as a STRING, exactly
// as the Cognito pre-token-generation trigger emits it (a JSON-array-encoded
// string). This lets us mint a self-signed token in the trigger's real output
// shape and drive it through the full signature-first resolver.
type stringReposClaims struct {
	Issuer      string `json:"iss,omitempty"`
	Subject     string `json:"sub,omitempty"`
	Audience    string `json:"aud,omitempty"`
	Expiry      int64  `json:"exp,omitempty"`
	NotBefore   int64  `json:"nbf,omitempty"`
	IssuedAt    int64  `json:"iat,omitempty"`
	Repos       string `json:"repos,omitempty"`        // JSON-array-encoded STRING
	DefaultRepo string `json:"default_repo,omitempty"` // string
}

// signStringRepos signs a token whose payload carries `repos` as a string, using
// the test signer's key (so the signature is valid). It marshals this alternate
// claim shape and signs it with RS256 via the signer's shared helper.
func signStringRepos(t *testing.T, ts *testSigner, c stringReposClaims) string {
	t.Helper()
	pb, _ := json.Marshal(c)
	return ts.signPayload(t, "RS256", pb)
}

// TestResolve_TriggerStringReposClaim is the end-to-end proof required by the
// brief: a self-signed token whose `repos` claim is a JSON-array-ENCODED STRING
// (as the pre-token trigger produces) authorizes exactly the granted repos
// through the real Resolver, and 403s any repo it does not grant.
func TestResolve_TriggerStringReposClaim(t *testing.T) {
	ts := newTestSigner(t, "kid-1")
	r := newResolver(t, ts)

	// Trigger output: repos as a JSON-array-encoded string, default_repo a string.
	reposJSON, _ := json.Marshal([]string{"acme/repo-a", "acme/repo-c"})
	tok := signStringRepos(t, ts, stringReposClaims{
		Issuer:      "https://issuer.test/pool",
		Subject:     "user-1",
		Audience:    "test-audience",
		Expiry:      fixedNow.Add(time.Hour).Unix(),
		IssuedAt:    fixedNow.Add(-time.Minute).Unix(),
		Repos:       string(reposJSON), // "[\"acme/repo-a\",\"acme/repo-c\"]"
		DefaultRepo: "acme/repo-a",
	})

	// Granted repo -> authorized, correct partition prefix.
	tenant, err := r.Resolve(bearerReq(t, tok, "acme/repo-a"), "")
	if err != nil {
		t.Fatalf("expected authorized for granted repo, got %v", err)
	}
	if tenant.Repo != "acme/repo-a" || tenant.PartitionPrefix != "REPO#acme/repo-a#" {
		t.Errorf("tenant = %+v, want repo acme/repo-a", tenant)
	}

	// Second granted repo (proves the string was actually split into a set).
	if _, err := r.Resolve(bearerReq(t, tok, "acme/repo-c"), ""); err != nil {
		t.Errorf("expected authorized for second granted repo, got %v", err)
	}

	// Non-granted repo -> 403.
	_, err = r.Resolve(bearerReq(t, tok, "acme/repo-b"), "")
	if HTTPStatus(err) != 403 {
		t.Errorf("unauthorized repo: status = %d, want 403", HTTPStatus(err))
	}

	// Default repo used when no target supplied -> authorized (it is granted).
	if _, err := r.Resolve(noTargetReq(t, tok), ""); err != nil {
		t.Errorf("expected default_repo to authorize when no target supplied, got %v", err)
	}
}

// TestResolve_TriggerSpaceDelimitedReposClaim proves the space-delimited string
// shape (a plausible alternate the trigger or an operator could produce) also
// flows through the resolver.
func TestResolve_TriggerSpaceDelimitedReposClaim(t *testing.T) {
	ts := newTestSigner(t, "kid-1")
	r := newResolver(t, ts)
	tok := signStringRepos(t, ts, stringReposClaims{
		Issuer:   "https://issuer.test/pool",
		Subject:  "user-1",
		Audience: "test-audience",
		Expiry:   fixedNow.Add(time.Hour).Unix(),
		IssuedAt: fixedNow.Add(-time.Minute).Unix(),
		Repos:    "acme/repo-a acme/repo-c",
	})
	if _, err := r.Resolve(bearerReq(t, tok, "acme/repo-c"), ""); err != nil {
		t.Errorf("expected authorized (space-delimited), got %v", err)
	}
	if _, err := r.Resolve(bearerReq(t, tok, "acme/repo-b"), ""); HTTPStatus(err) != 403 {
		t.Errorf("space-delimited unauthorized: status = %d, want 403", HTTPStatus(err))
	}
}

// TestResolve_EmptyReposClaimDenies proves a signature-valid token whose repos
// string is empty grants nothing (403) — an empty attribute must never be an
// invented grant.
func TestResolve_EmptyReposClaimDenies(t *testing.T) {
	ts := newTestSigner(t, "kid-1")
	r := newResolver(t, ts)
	tok := signStringRepos(t, ts, stringReposClaims{
		Issuer:   "https://issuer.test/pool",
		Subject:  "user-1",
		Audience: "test-audience",
		Expiry:   fixedNow.Add(time.Hour).Unix(),
		IssuedAt: fixedNow.Add(-time.Minute).Unix(),
		Repos:    "",
	})
	_, err := r.Resolve(bearerReq(t, tok, "acme/repo-a"), "")
	if HTTPStatus(err) != 403 {
		t.Errorf("empty repos claim: status = %d, want 403", HTTPStatus(err))
	}
}

// noTargetReq builds a bearer request with no target repo header/arg, so the
// resolver falls back to the token's default_repo claim.
func noTargetReq(t *testing.T, token string) *http.Request {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, "https://cainban.test/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	return req
}

// TestResolve_NoTargetNoDefault_Unscoped proves that a signature-valid token
// that names NO repo (no arg, no header) AND has no default_repo is NOT a 403:
// it resolves to an authenticated, UNSCOPED tenant so the MCP handshake
// (initialize, tools/list) can proceed. This is the repo-agnostic handshake fix
// — previously this returned 403 before the client could list any tools.
func TestResolve_NoTargetNoDefault_Unscoped(t *testing.T) {
	ts := newTestSigner(t, "kid-1")
	r := newResolver(t, ts)
	tok := signStringRepos(t, ts, stringReposClaims{
		Issuer:   "https://issuer.test/pool",
		Subject:  "user-1",
		Audience: "test-audience",
		Expiry:   fixedNow.Add(time.Hour).Unix(),
		IssuedAt: fixedNow.Add(-time.Minute).Unix(),
		Repos:    "acme/repo-a", // has a grant, but names no target and no default
	})
	tenant, err := r.Resolve(noTargetReq(t, tok), "")
	if err != nil {
		t.Fatalf("no target + no default_repo should be unscoped, got error: %v", err)
	}
	if !tenant.Unscoped {
		t.Errorf("tenant.Unscoped = false, want true")
	}
	if tenant.Repo != "" || tenant.PartitionPrefix != "" {
		t.Errorf("unscoped tenant must carry no repo/prefix, got repo=%q prefix=%q", tenant.Repo, tenant.PartitionPrefix)
	}
	if tenant.Subject == "" {
		t.Errorf("unscoped tenant must still carry the validated subject")
	}
}

// TestResolve_NamedUnauthorized_StillForbidden proves the unscoped path did NOT
// weaken authorization: a repo that IS named but not granted is still a 403,
// never silently downgraded to unscoped.
func TestResolve_NamedUnauthorized_StillForbidden(t *testing.T) {
	ts := newTestSigner(t, "kid-1")
	r := newResolver(t, ts)
	tok := signStringRepos(t, ts, stringReposClaims{
		Issuer:   "https://issuer.test/pool",
		Subject:  "user-1",
		Audience: "test-audience",
		Expiry:   fixedNow.Add(time.Hour).Unix(),
		IssuedAt: fixedNow.Add(-time.Minute).Unix(),
		Repos:    "acme/repo-a",
	})
	if _, err := r.Resolve(bearerReq(t, tok, "acme/repo-b"), ""); HTTPStatus(err) != 403 {
		t.Errorf("named unauthorized repo: status = %d, want 403", HTTPStatus(err))
	}
}

// TestResolve_MalformedTarget_StillForbidden proves a supplied-but-malformed
// target is a 403, not an unscoped downgrade — a client bug must not hide behind
// a working handshake.
func TestResolve_MalformedTarget_StillForbidden(t *testing.T) {
	ts := newTestSigner(t, "kid-1")
	r := newResolver(t, ts)
	tok := signStringRepos(t, ts, stringReposClaims{
		Issuer:   "https://issuer.test/pool",
		Subject:  "user-1",
		Audience: "test-audience",
		Expiry:   fixedNow.Add(time.Hour).Unix(),
		IssuedAt: fixedNow.Add(-time.Minute).Unix(),
		Repos:    "acme/repo-a",
	})
	if _, err := r.Resolve(bearerReq(t, tok, "not-a-valid-repo"), ""); HTTPStatus(err) != 403 {
		t.Errorf("malformed target: status = %d, want 403", HTTPStatus(err))
	}
}
