package mcp

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hmain/cainban/src/systems/auth"
)

// This test exercises the Phase 3 auth gate at the HTTP boundary: it proves the
// middleware rejects unauthenticated (401) and unauthorized (403) requests
// BEFORE the wrapped handler runs, and on a valid+authorized request injects
// the resolved tenant into the request context that the tool handlers read.

var mwNow = time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)

var b64u = base64.RawURLEncoding

type mwSigner struct {
	kid  string
	key  *rsa.PrivateKey
	keys auth.KeySource
}

func newMWSigner(t *testing.T) *mwSigner {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	return &mwSigner{
		kid:  "mw-kid",
		key:  key,
		keys: auth.NewStaticKeySource(map[string]*rsa.PublicKey{"mw-kid": &key.PublicKey}),
	}
}

func (m *mwSigner) token(t *testing.T, repos []string) string {
	t.Helper()
	header := map[string]string{"alg": "RS256", "kid": m.kid, "typ": "JWT"}
	claims := map[string]any{
		"iss":   "https://issuer.test/pool",
		"sub":   "user-mw",
		"aud":   "test-audience",
		"exp":   mwNow.Add(time.Hour).Unix(),
		"repos": repos,
	}
	hb, _ := json.Marshal(header)
	cb, _ := json.Marshal(claims)
	signingInput := b64u.EncodeToString(hb) + "." + b64u.EncodeToString(cb)
	d := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, m.key, crypto.SHA256, d[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return signingInput + "." + b64u.EncodeToString(sig)
}

func (m *mwSigner) resolver(t *testing.T) *auth.Resolver {
	t.Helper()
	v, err := auth.NewValidator(auth.Config{
		Issuer:   "https://issuer.test/pool",
		Audience: "test-audience",
		Keys:     m.keys,
		Now:      func() time.Time { return mwNow },
	})
	if err != nil {
		t.Fatalf("NewValidator: %v", err)
	}
	return auth.NewResolver(v)
}

// spyHandler records whether it ran and what tenant it saw.
type spyHandler struct {
	ran    bool
	tenant *auth.Tenant
}

func (s *spyHandler) ServeHTTP(_ http.ResponseWriter, r *http.Request) {
	s.ran = true
	if t, ok := tenantFromContext(r.Context()); ok {
		s.tenant = t
	}
}

func mwRequest(token, repo string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "https://cainban.test/mcp", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if repo != "" {
		req.Header.Set(auth.HeaderTargetRepo, repo)
	}
	return req
}

func TestAuthMiddleware_MissingToken401(t *testing.T) {
	m := newMWSigner(t)
	spy := &spyHandler{}
	h := AuthMiddleware(m.resolver(t), spy)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, mwRequest("", "acme/repo-a"))

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
	if spy.ran {
		t.Error("handler must NOT run on a missing token")
	}
}

func TestAuthMiddleware_InvalidToken401(t *testing.T) {
	m := newMWSigner(t)
	spy := &spyHandler{}
	h := AuthMiddleware(m.resolver(t), spy)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, mwRequest("not.a.jwt", "acme/repo-a"))

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
	if spy.ran {
		t.Error("handler must NOT run on an invalid token")
	}
}

func TestAuthMiddleware_UnauthorizedRepo403(t *testing.T) {
	m := newMWSigner(t)
	spy := &spyHandler{}
	h := AuthMiddleware(m.resolver(t), spy)

	// Token grants repo-a only; request targets repo-b.
	tok := m.token(t, []string{"acme/repo-a"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, mwRequest(tok, "acme/repo-b"))

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
	if spy.ran {
		t.Error("handler must NOT run when the repo is unauthorized")
	}
}

func TestAuthMiddleware_ValidInjectsTenant(t *testing.T) {
	m := newMWSigner(t)
	spy := &spyHandler{}
	h := AuthMiddleware(m.resolver(t), spy)

	tok := m.token(t, []string{"acme/repo-a"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, mwRequest(tok, "acme/repo-a"))

	if !spy.ran {
		t.Fatal("handler should run on a valid+authorized request")
	}
	if spy.tenant == nil {
		t.Fatal("tenant must be injected into the handler context")
	}
	if spy.tenant.PartitionPrefix != "REPO#acme/repo-a#" {
		t.Errorf("prefix = %q, want REPO#acme/repo-a#", spy.tenant.PartitionPrefix)
	}
	if spy.tenant.Subject != "user-mw" {
		t.Errorf("subject = %q, want user-mw", spy.tenant.Subject)
	}
}
