package auth

import (
	"net/http"
	"testing"
	"time"
)

// fixedNow is the reference clock for token expiry math in these tests.
var fixedNow = time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)

// validClaims is a well-formed claim set for the trusted issuer/audience.
func validClaims() tokenClaims {
	return tokenClaims{
		Issuer:      "https://issuer.test/pool",
		Subject:     "user-1",
		Audience:    "test-audience",
		Expiry:      fixedNow.Add(time.Hour).Unix(),
		IssuedAt:    fixedNow.Add(-time.Minute).Unix(),
		Repos:       []string{"acme/repo-a"},
		DefaultRepo: "acme/repo-a",
	}
}

func newValidator(t *testing.T, ts *testSigner) *Validator {
	t.Helper()
	v, err := NewValidator(ts.baseConfig(fixedNow))
	if err != nil {
		t.Fatalf("NewValidator: %v", err)
	}
	return v
}

// --- signature-first validation --------------------------------------------

func TestValidate_ValidToken(t *testing.T) {
	ts := newTestSigner(t, "kid-1")
	v := newValidator(t, ts)
	id, err := v.Validate(ts.sign(t, "RS256", validClaims()))
	if err != nil {
		t.Fatalf("expected valid token, got %v", err)
	}
	if id.Subject != "user-1" {
		t.Errorf("subject = %q, want user-1", id.Subject)
	}
	if !id.authorizes("acme/repo-a") {
		t.Errorf("expected repo-a authorized")
	}
	if id.authorizes("acme/repo-b") {
		t.Errorf("repo-b must NOT be authorized")
	}
}

func TestValidate_RejectsUnsignedAlgNone(t *testing.T) {
	ts := newTestSigner(t, "kid-1")
	v := newValidator(t, ts)
	// alg=none is the classic signature-stripping downgrade; even though the
	// claims are otherwise perfect it must be rejected BEFORE any claim is
	// trusted.
	_, err := v.Validate(ts.unsignedToken(t, validClaims()))
	if err == nil {
		t.Fatal("expected alg=none token to be rejected")
	}
	if HTTPStatus(err) != 401 {
		t.Errorf("status = %d, want 401", HTTPStatus(err))
	}
}

func TestValidate_RejectsTamperedPayload(t *testing.T) {
	ts := newTestSigner(t, "kid-1")
	v := newValidator(t, ts)
	// A token whose payload was swapped after signing (forged repos claim) must
	// fail the signature check — proving claims are never trusted pre-signature.
	_, err := v.Validate(ts.tamperedToken(t, validClaims()))
	if err == nil {
		t.Fatal("expected tampered token to be rejected")
	}
	if HTTPStatus(err) != 401 {
		t.Errorf("status = %d, want 401", HTTPStatus(err))
	}
}

func TestValidate_RejectsWrongKey(t *testing.T) {
	signer := newTestSigner(t, "kid-1")
	attacker := newTestSigner(t, "kid-1") // same kid, different key
	// Validator trusts only the legitimate signer's JWKS.
	v := newValidator(t, signer)
	// Token signed by the attacker's key must fail signature verification.
	_, err := v.Validate(attacker.sign(t, "RS256", validClaims()))
	if err == nil {
		t.Fatal("expected token signed with untrusted key to be rejected")
	}
	if HTTPStatus(err) != 401 {
		t.Errorf("status = %d, want 401", HTTPStatus(err))
	}
}

func TestValidate_RejectsExpired(t *testing.T) {
	ts := newTestSigner(t, "kid-1")
	v := newValidator(t, ts)
	c := validClaims()
	c.Expiry = fixedNow.Add(-time.Hour).Unix()
	_, err := v.Validate(ts.sign(t, "RS256", c))
	if err == nil || HTTPStatus(err) != 401 {
		t.Fatalf("expected 401 for expired token, got %v", err)
	}
}

func TestValidate_RejectsWrongIssuer(t *testing.T) {
	ts := newTestSigner(t, "kid-1")
	v := newValidator(t, ts)
	c := validClaims()
	c.Issuer = "https://evil.test/pool"
	_, err := v.Validate(ts.sign(t, "RS256", c))
	if err == nil || HTTPStatus(err) != 401 {
		t.Fatalf("expected 401 for wrong issuer, got %v", err)
	}
}

func TestValidate_RejectsWrongAudience(t *testing.T) {
	ts := newTestSigner(t, "kid-1")
	v := newValidator(t, ts)
	c := validClaims()
	c.Audience = "some-other-client"
	_, err := v.Validate(ts.sign(t, "RS256", c))
	if err == nil || HTTPStatus(err) != 401 {
		t.Fatalf("expected 401 for wrong audience, got %v", err)
	}
}

func TestValidate_RejectsMissingToken(t *testing.T) {
	ts := newTestSigner(t, "kid-1")
	v := newValidator(t, ts)
	_, err := v.Validate("")
	if err == nil || HTTPStatus(err) != 401 {
		t.Fatalf("expected 401 for empty token, got %v", err)
	}
}

// --- Resolver authorization + tenant isolation -----------------------------

func newResolver(t *testing.T, ts *testSigner) *Resolver {
	return NewResolver(newValidator(t, ts))
}

func bearerReq(t *testing.T, token, headerRepo string) *http.Request {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, "https://cainban.test/mcp", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if headerRepo != "" {
		req.Header.Set(HeaderTargetRepo, headerRepo)
	}
	return req
}

// Scenario (a): valid token for repo A -> tenant resolves to repo A's prefix.
func TestResolve_ValidTokenAuthorizedRepo(t *testing.T) {
	ts := newTestSigner(t, "kid-1")
	r := newResolver(t, ts)
	tok := ts.sign(t, "RS256", validClaims())

	tenant, err := r.Resolve(bearerReq(t, tok, "acme/repo-a"), "")
	if err != nil {
		t.Fatalf("expected authorized, got %v", err)
	}
	if tenant.Repo != "acme/repo-a" {
		t.Errorf("repo = %q, want acme/repo-a", tenant.Repo)
	}
	if tenant.PartitionPrefix != "REPO#acme/repo-a#" {
		t.Errorf("prefix = %q, want REPO#acme/repo-a#", tenant.PartitionPrefix)
	}
}

// Scenario (b): valid token WITHOUT repo A access -> 403.
func TestResolve_ValidTokenUnauthorizedRepo(t *testing.T) {
	ts := newTestSigner(t, "kid-1")
	r := newResolver(t, ts)
	c := validClaims()
	c.Repos = []string{"acme/repo-a"} // no repo-b
	c.DefaultRepo = "acme/repo-a"
	tok := ts.sign(t, "RS256", c)

	// Target repo-b, which the validated claim does not grant.
	_, err := r.Resolve(bearerReq(t, tok, "acme/repo-b"), "")
	if err == nil {
		t.Fatal("expected 403 for unauthorized repo")
	}
	if HTTPStatus(err) != 403 {
		t.Errorf("status = %d, want 403", HTTPStatus(err))
	}
}

// Scenario (c): invalid/expired/unsigned token -> 401 and NO tenant.
func TestResolve_InvalidTokenNoTenant(t *testing.T) {
	ts := newTestSigner(t, "kid-1")
	r := newResolver(t, ts)

	// Unsigned (alg=none) token targeting repo-a.
	tenant, err := r.Resolve(bearerReq(t, ts.unsignedToken(t, validClaims()), "acme/repo-a"), "")
	if err == nil {
		t.Fatal("expected 401 for unsigned token")
	}
	if HTTPStatus(err) != 401 {
		t.Errorf("status = %d, want 401", HTTPStatus(err))
	}
	if tenant != nil {
		t.Error("no tenant must be resolved for an invalid token")
	}
}

// The client-supplied target (header/arg) can only ever NARROW to a repo the
// token grants; it cannot escalate. An arg overrides the header but is still
// authorized against the claim.
func TestResolve_ArgOverridesHeaderButStillAuthorized(t *testing.T) {
	ts := newTestSigner(t, "kid-1")
	r := newResolver(t, ts)
	c := validClaims()
	c.Repos = []string{"acme/repo-a", "acme/repo-c"}
	tok := ts.sign(t, "RS256", c)

	// Header says repo-a, arg says repo-c (both granted) -> arg wins.
	tenant, err := r.Resolve(bearerReq(t, tok, "acme/repo-a"), "acme/repo-c")
	if err != nil {
		t.Fatalf("expected authorized, got %v", err)
	}
	if tenant.Repo != "acme/repo-c" {
		t.Errorf("arg should win: repo = %q, want acme/repo-c", tenant.Repo)
	}

	// Arg targets a repo NOT granted -> 403 even though header repo is granted.
	_, err = r.Resolve(bearerReq(t, tok, "acme/repo-a"), "acme/repo-x")
	if HTTPStatus(err) != 403 {
		t.Errorf("unauthorized arg: status = %d, want 403", HTTPStatus(err))
	}
}

// Isolation at the tenant layer: two distinct authorized repos map to two
// distinct partition prefixes, so no request can address the other's data.
func TestResolve_TenantPrefixIsolation(t *testing.T) {
	ts := newTestSigner(t, "kid-1")
	r := newResolver(t, ts)
	c := validClaims()
	c.Repos = []string{"acme/repo-a", "acme/repo-b"}
	tok := ts.sign(t, "RS256", c)

	ta, _ := r.Resolve(bearerReq(t, tok, "acme/repo-a"), "")
	tb, _ := r.Resolve(bearerReq(t, tok, "acme/repo-b"), "")
	if ta.PartitionPrefix == tb.PartitionPrefix {
		t.Fatalf("repo-a and repo-b must have distinct prefixes, both = %q", ta.PartitionPrefix)
	}
}

func TestNormalizeRepo(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"acme/repo-a", "acme/repo-a", false},
		{" acme/repo-a ", "acme/repo-a", false},
		{"REPO#acme/repo-a#", "acme/repo-a", false},
		{"", "", true},
		{"noslash", "", true},
		{"a/b/c", "", true},
		{"/b", "", true},
		{"a/", "", true},
		{"a/../b", "", true},
		{"a/b#c", "", true},
	}
	for _, tc := range cases {
		got, err := NormalizeRepo(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("NormalizeRepo(%q) expected error", tc.in)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("NormalizeRepo(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
		}
	}
}
