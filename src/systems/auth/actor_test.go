package auth

import (
	"net/http"
	"testing"
	"time"
)

// newEmailTestResolver builds a Resolver over a test signer with a fixed clock.
func newEmailTestResolver(t *testing.T) (*Resolver, *testSigner, time.Time) {
	t.Helper()
	ts := newTestSigner(t, "kid-actor")
	now := time.Unix(1_700_000_000, 0).UTC()
	v, err := NewValidator(ts.baseConfig(now))
	if err != nil {
		t.Fatalf("NewValidator: %v", err)
	}
	return NewResolver(v), ts, now
}

func requestWithToken(t *testing.T, tok string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, "http://cainban.test/mcp", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	return req
}

// TestActor_PrefersEmail proves the `email` claim is decoded and the resolved
// tenant's Actor is the email when present.
func TestActor_PrefersEmail(t *testing.T) {
	r, ts, now := newEmailTestResolver(t)
	tok := ts.sign(t, "RS256", tokenClaims{
		Issuer:      "https://issuer.test/pool",
		Subject:     "sub-123",
		Email:       "dev@example.com",
		Audience:    "test-audience",
		Expiry:      now.Add(time.Hour).Unix(),
		Repos:       []string{"acme/widgets"},
		DefaultRepo: "acme/widgets",
	})

	tenant, err := r.Resolve(requestWithToken(t, tok), "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if tenant.Actor != "dev@example.com" {
		t.Fatalf("expected Actor to prefer email, got %q", tenant.Actor)
	}
	if tenant.Subject != "sub-123" {
		t.Fatalf("expected subject preserved, got %q", tenant.Subject)
	}
}

// TestActor_FallsBackToSub proves that with no email claim the Actor falls back
// to the opaque subject.
func TestActor_FallsBackToSub(t *testing.T) {
	r, ts, now := newEmailTestResolver(t)
	tok := ts.sign(t, "RS256", tokenClaims{
		Issuer:      "https://issuer.test/pool",
		Subject:     "sub-456",
		Audience:    "test-audience",
		Expiry:      now.Add(time.Hour).Unix(),
		Repos:       []string{"acme/widgets"},
		DefaultRepo: "acme/widgets",
	})

	tenant, err := r.Resolve(requestWithToken(t, tok), "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if tenant.Actor != "sub-456" {
		t.Fatalf("expected Actor to fall back to subject, got %q", tenant.Actor)
	}
}

// TestActor_UnscopedTenantStillCarriesActor proves an authenticated request that
// names no repo (unscoped) still resolves an Actor for audit.
func TestActor_UnscopedTenantStillCarriesActor(t *testing.T) {
	r, ts, now := newEmailTestResolver(t)
	tok := ts.sign(t, "RS256", tokenClaims{
		Issuer:   "https://issuer.test/pool",
		Subject:  "sub-789",
		Email:    "ops@example.com",
		Audience: "test-audience",
		Expiry:   now.Add(time.Hour).Unix(),
		// No repos, no default_repo -> unscoped.
	})

	tenant, err := r.Resolve(requestWithToken(t, tok), "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !tenant.Unscoped {
		t.Fatalf("expected an unscoped tenant, got scoped repo=%q", tenant.Repo)
	}
	if tenant.Actor != "ops@example.com" {
		t.Fatalf("expected unscoped tenant to still carry Actor, got %q", tenant.Actor)
	}
}
