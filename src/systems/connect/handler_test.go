package connect

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hmain/cainban/src/systems/auth"
	"github.com/hmain/cainban/src/systems/github"
)

// --- self-signed Cognito JWT (mirrors the auth package test key) -----------

const (
	testKID      = "test-key-1"
	testIssuer   = "https://issuer.test/pool"
	testAudience = "test-audience"
)

type testEnv struct {
	signer    *rsa.PrivateKey
	validator *auth.Validator
	handler   *Handler
	oauth     *fakeOAuth
	verify    *fakeVerifier
	store     *fakeStore
	state     *StateSigner
}

// newEnv builds a fully-wired handler backed by fakes + a real signature-first
// validator trusting a self-signed key. No network, no AWS.
func newEnv(t *testing.T) *testEnv {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("gen key: %v", err)
	}
	keys := auth.NewStaticKeySource(map[string]*rsa.PublicKey{testKID: &key.PublicKey})
	validator, err := auth.NewValidator(auth.Config{
		Issuer:   testIssuer,
		Audience: testAudience,
		Keys:     keys,
	})
	if err != nil {
		t.Fatalf("NewValidator: %v", err)
	}
	state := newTestSigner(t)
	oauth := &fakeOAuth{login: "octocat"}
	verify := &fakeVerifier{}
	store := newFakeStore()
	h, err := NewHandler(Config{
		Auth:   validator,
		OAuth:  oauth,
		Verify: verify,
		Grants: store,
		State:  state,
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	return &testEnv{signer: key, validator: validator, handler: h, oauth: oauth, verify: verify, store: store, state: state}
}

// token mints a signed JWT for sub, valid now.
func (e *testEnv) token(t *testing.T, sub string) string {
	t.Helper()
	now := time.Now()
	payload := map[string]any{
		"iss": testIssuer,
		"aud": testAudience,
		"sub": sub,
		"exp": now.Add(time.Hour).Unix(),
		"iat": now.Unix(),
	}
	pb, _ := json.Marshal(payload)
	header := map[string]string{"alg": "RS256", "kid": testKID, "typ": "JWT"}
	hb, _ := json.Marshal(header)
	b64 := base64.RawURLEncoding
	signingInput := b64.EncodeToString(hb) + "." + b64.EncodeToString(pb)
	digest := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, e.signer, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return signingInput + "." + b64.EncodeToString(sig)
}

func (e *testEnv) do(t *testing.T, method, target, bearer, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
	} else {
		r = httptest.NewRequest(method, target, nil)
	}
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	}
	w := httptest.NewRecorder()
	e.handler.ServeHTTP(w, r)
	return w
}

// --- fakes -----------------------------------------------------------------

type fakeOAuth struct {
	login       string
	exchangeErr error
	authorized  int
	exchanged   int
}

func (f *fakeOAuth) AuthorizeURL(state string) string {
	f.authorized++
	return "https://github.test/login/oauth/authorize?state=" + state
}
func (f *fakeOAuth) ExchangeCode(_ context.Context, _ string) (string, error) {
	f.exchanged++
	if f.exchangeErr != nil {
		return "", f.exchangeErr
	}
	return f.login, nil
}

type fakeVerifier struct {
	allow bool
	err   error
	calls int
}

func (f *fakeVerifier) VerifyRepoAccess(_ context.Context, _, _ string, _ github.Principal) (bool, error) {
	f.calls++
	return f.allow, f.err
}

// fakeStore is an in-memory GrantStore keyed by subject.
type fakeStore struct {
	grants     map[string]map[string]bool // subject -> repo -> present
	identities map[string]string          // subject -> github login
	putErr     error
	getIDErr   error
}

func newFakeStore() *fakeStore {
	return &fakeStore{grants: map[string]map[string]bool{}, identities: map[string]string{}}
}

func (s *fakeStore) PutGrant(_ context.Context, subject, repo string) error {
	if s.putErr != nil {
		return s.putErr
	}
	if s.grants[subject] == nil {
		s.grants[subject] = map[string]bool{}
	}
	s.grants[subject][repo] = true
	return nil
}
func (s *fakeStore) DeleteGrant(_ context.Context, subject, repo string) error {
	if s.grants[subject] != nil {
		delete(s.grants[subject], repo)
	}
	return nil
}
func (s *fakeStore) ListReposForSubject(_ context.Context, subject string) ([]string, error) {
	var out []string
	for r, ok := range s.grants[subject] {
		if ok {
			out = append(out, r)
		}
	}
	return out, nil
}
func (s *fakeStore) PutIdentity(_ context.Context, subject, login string) error {
	s.identities[subject] = login
	return nil
}
func (s *fakeStore) GetIdentity(_ context.Context, subject string) (string, error) {
	if s.getIDErr != nil {
		return "", s.getIDErr
	}
	return s.identities[subject], nil
}

// --- tests -----------------------------------------------------------------

const (
	subA = "sub-alice"
	subB = "sub-bob"
)

// Every route rejects an unauthenticated request with 401.
func TestUnauth_401_EveryRoute(t *testing.T) {
	e := newEnv(t)
	cases := []struct {
		method, path, body string
	}{
		{"GET", "/connect/github/start", ""},
		{"GET", "/connect/github/callback?code=c&state=s", ""},
		{"POST", "/connect/repo", `{"owner":"acme","repo":"widgets"}`},
		{"DELETE", "/connect/repo", `{"owner":"acme","repo":"widgets"}`},
		{"GET", "/connect/repos", ""},
	}
	for _, tc := range cases {
		// No token at all.
		if w := e.do(t, tc.method, tc.path, "", tc.body); w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s no-token: status %d, want 401", tc.method, tc.path, w.Code)
		}
		// A garbage token (bad signature).
		if w := e.do(t, tc.method, tc.path, "garbage.token.here", tc.body); w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s bad-token: status %d, want 401", tc.method, tc.path, w.Code)
		}
	}
}

// start -> 302 redirect carrying a state.
func TestStart_RedirectsWithState(t *testing.T) {
	e := newEnv(t)
	w := e.do(t, "GET", "/connect/github/start", e.token(t, subA), "")
	if w.Code != http.StatusFound {
		t.Fatalf("start status = %d, want 302", w.Code)
	}
	loc := w.Header().Get("Location")
	if !strings.Contains(loc, "state=") {
		t.Fatalf("redirect Location has no state: %s", loc)
	}
	if e.oauth.authorized != 1 {
		t.Errorf("AuthorizeURL calls = %d, want 1", e.oauth.authorized)
	}
}

// callback with a bad/forged state -> rejected, no exchange, no identity write.
func TestCallback_BadState_Rejected(t *testing.T) {
	e := newEnv(t)
	w := e.do(t, "GET", "/connect/github/callback?code=c&state=forged", e.token(t, subA), "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("bad-state callback status = %d, want 400", w.Code)
	}
	if e.oauth.exchanged != 0 {
		t.Error("code must not be exchanged on a bad state")
	}
	if _, ok := e.store.identities[subA]; ok {
		t.Error("no identity must be persisted on a bad state")
	}
}

// A state minted for subA cannot be completed by subB (anti-CSRF sub binding).
func TestCallback_StateSubMismatch_Rejected(t *testing.T) {
	e := newEnv(t)
	stateForA, _ := e.state.Issue(subA)
	w := e.do(t, "GET", "/connect/github/callback?code=c&state="+stateForA, e.token(t, subB), "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("cross-sub callback status = %d, want 400", w.Code)
	}
	if e.oauth.exchanged != 0 {
		t.Error("code must not be exchanged when state sub != caller sub")
	}
}

// callback ok -> identity persisted for the validated sub.
func TestCallback_OK_PersistsIdentity(t *testing.T) {
	e := newEnv(t)
	stateForA, _ := e.state.Issue(subA)
	w := e.do(t, "GET", "/connect/github/callback?code=c&state="+stateForA, e.token(t, subA), "")
	if w.Code != http.StatusOK {
		t.Fatalf("ok callback status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	if e.store.identities[subA] != "octocat" {
		t.Errorf("persisted identity = %q, want octocat", e.store.identities[subA])
	}
}

// callback with an OAuth exchange error -> fail closed, no identity persisted.
func TestCallback_ExchangeError_FailsClosed(t *testing.T) {
	e := newEnv(t)
	e.oauth.exchangeErr = errors.New("github down")
	stateForA, _ := e.state.Issue(subA)
	w := e.do(t, "GET", "/connect/github/callback?code=c&state="+stateForA, e.token(t, subA), "")
	if w.Code == http.StatusOK {
		t.Fatal("callback must not succeed on an exchange error")
	}
	if _, ok := e.store.identities[subA]; ok {
		t.Error("no identity must be persisted on an exchange error")
	}
}

// POST /connect/repo without a linked identity -> link-required (409), no verify.
func TestRepoPost_NoLinkedIdentity_LinkRequired(t *testing.T) {
	e := newEnv(t)
	w := e.do(t, "POST", "/connect/repo", e.token(t, subA), `{"owner":"acme","repo":"widgets"}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("no-identity POST status = %d, want 409", w.Code)
	}
	if e.verify.calls != 0 {
		t.Error("verify must not run without a linked identity")
	}
}

// POST /connect/repo verify-true -> grant written.
func TestRepoPost_VerifyTrue_GrantWritten(t *testing.T) {
	e := newEnv(t)
	e.store.identities[subA] = "octocat"
	e.verify.allow = true
	w := e.do(t, "POST", "/connect/repo", e.token(t, subA), `{"owner":"acme","repo":"widgets"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("verify-true POST status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	if !e.store.grants[subA]["acme/widgets"] {
		t.Error("grant must be written on verify-true")
	}
}

// POST /connect/repo verify-false -> 403, NO grant written.
func TestRepoPost_VerifyFalse_403_NoWrite(t *testing.T) {
	e := newEnv(t)
	e.store.identities[subA] = "octocat"
	e.verify.allow = false
	w := e.do(t, "POST", "/connect/repo", e.token(t, subA), `{"owner":"acme","repo":"widgets"}`)
	if w.Code != http.StatusForbidden {
		t.Fatalf("verify-false POST status = %d, want 403", w.Code)
	}
	if e.store.grants[subA]["acme/widgets"] {
		t.Error("no grant must be written on verify-false")
	}
}

// POST /connect/repo verify-ERROR -> fail closed (not 200), NO grant written.
func TestRepoPost_VerifyError_FailsClosed_NoWrite(t *testing.T) {
	e := newEnv(t)
	e.store.identities[subA] = "octocat"
	e.verify.err = errors.New("github 500")
	w := e.do(t, "POST", "/connect/repo", e.token(t, subA), `{"owner":"acme","repo":"widgets"}`)
	if w.Code == http.StatusOK {
		t.Fatal("a verify error must never succeed")
	}
	if e.store.grants[subA]["acme/widgets"] {
		t.Error("no grant must be written on a verify error (fail closed)")
	}
}

// A caller can never write a grant into ANOTHER subject's partition: the grant
// lands under the validated sub only.
func TestRepoPost_GrantScopedToValidatedSub(t *testing.T) {
	e := newEnv(t)
	e.store.identities[subA] = "octocat"
	e.verify.allow = true
	e.do(t, "POST", "/connect/repo", e.token(t, subA), `{"owner":"acme","repo":"widgets"}`)
	if e.store.grants[subB]["acme/widgets"] {
		t.Fatal("grant leaked into another subject's partition")
	}
	if !e.store.grants[subA]["acme/widgets"] {
		t.Fatal("grant not written under the validated sub")
	}
}

// DELETE /connect/repo removes the grant for the validated sub only.
func TestRepoDelete_Removes(t *testing.T) {
	e := newEnv(t)
	e.store.grants[subA] = map[string]bool{"acme/widgets": true}
	w := e.do(t, "DELETE", "/connect/repo", e.token(t, subA), `{"owner":"acme","repo":"widgets"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("DELETE status = %d, want 200", w.Code)
	}
	if e.store.grants[subA]["acme/widgets"] {
		t.Error("grant must be removed after DELETE")
	}
}

// GET /connect/repos lists ONLY the caller's grants.
func TestReposList_OnlyCallersGrants(t *testing.T) {
	e := newEnv(t)
	e.store.grants[subA] = map[string]bool{"acme/a": true}
	e.store.grants[subB] = map[string]bool{"acme/secret": true}
	e.store.identities[subA] = "octocat"
	w := e.do(t, "GET", "/connect/repos", e.token(t, subA), "")
	if w.Code != http.StatusOK {
		t.Fatalf("repos list status = %d, want 200", w.Code)
	}
	var out struct {
		Repos       []string `json:"repos"`
		GitHubLogin string   `json:"github_login"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Repos) != 1 || out.Repos[0] != "acme/a" {
		t.Fatalf("repos = %v, want [acme/a] (only caller's)", out.Repos)
	}
	if strings.Contains(w.Body.String(), "acme/secret") {
		t.Fatal("another subject's grant leaked into the list")
	}
	if out.GitHubLogin != "octocat" {
		t.Errorf("github_login = %q, want octocat", out.GitHubLogin)
	}
}

// A malformed owner/repo body is a 400 before any verify/write.
func TestRepoPost_MalformedBody_400(t *testing.T) {
	e := newEnv(t)
	e.store.identities[subA] = "octocat"
	for _, body := range []string{`{`, `{"owner":"","repo":"x"}`, `{"owner":"a","repo":""}`, `{"owner":"a","repo":"b/c"}`} {
		w := e.do(t, "POST", "/connect/repo", e.token(t, subA), body)
		if w.Code != http.StatusBadRequest {
			t.Errorf("malformed body %q status = %d, want 400", body, w.Code)
		}
	}
	if e.verify.calls != 0 {
		t.Error("verify must not run on a malformed body")
	}
}

// An unknown path is 404; a wrong method is 405.
func TestRoutingGuards(t *testing.T) {
	e := newEnv(t)
	if w := e.do(t, "GET", "/connect/unknown", e.token(t, subA), ""); w.Code != http.StatusNotFound {
		t.Errorf("unknown path status = %d, want 404", w.Code)
	}
	if w := e.do(t, "POST", "/connect/github/start", e.token(t, subA), ""); w.Code != http.StatusMethodNotAllowed {
		t.Errorf("wrong method status = %d, want 405", w.Code)
	}
}
