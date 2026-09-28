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
	"github.com/hmain/cainban/src/systems/grants"
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
	lister    *fakeLister
	crypter   *fakeCrypter
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
	lister := &fakeLister{}
	cr := &fakeCrypter{}
	store := newFakeStore()
	h, err := NewHandler(Config{
		Auth:    validator,
		OAuth:   oauth,
		Verify:  verify,
		Lister:  lister,
		Grants:  store,
		Crypter: cr,
		State:   state,
		AppSlug: "cainban-connect",
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	return &testEnv{signer: key, validator: validator, handler: h, oauth: oauth, verify: verify, lister: lister, crypter: cr, store: store, state: state}
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

// app-info returns the App install URL built from the configured slug, with a
// signed state, and needs a JWT but NOT a linked identity.
func TestAppInfo_ReturnsInstallURL(t *testing.T) {
	e := newEnv(t)
	r := httptest.NewRequest("GET", "/connect/app-info", nil)
	r.Header.Set("Authorization", "Bearer "+e.token(t, subA))
	w := httptest.NewRecorder()
	e.handler.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("app-info status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	var body struct {
		InstallURL string `json:"install_url"`
		AppSlug    string `json:"app_slug"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("app-info body not JSON: %v (%s)", err, w.Body.String())
	}
	// newEnv configures the handler with a test app slug; the URL must point at
	// github.com/apps/<slug>/installations/new and carry a state.
	if !strings.Contains(body.InstallURL, "/apps/") ||
		!strings.Contains(body.InstallURL, "/installations/new") ||
		!strings.Contains(body.InstallURL, "state=") {
		t.Fatalf("unexpected install_url: %s", body.InstallURL)
	}
}

// app-info requires a JWT (401 without one).
func TestAppInfo_Unauth401(t *testing.T) {
	e := newEnv(t)
	if w := e.do(t, "GET", "/connect/app-info", "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("app-info no-token status = %d, want 401", w.Code)
	}
}

// --- fakes -----------------------------------------------------------------

type fakeOAuth struct {
	login       string
	exchangeErr error
	authorized  int
	exchanged   int
	// tokens returned by ExchangeCodeTokens (refresh persisted by the callback).
	exchangeRefresh string
	// refresh behavior for RefreshUserToken.
	refreshAccess  string
	refreshRefresh string
	refreshErr     error
	refreshed      int
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
func (f *fakeOAuth) ExchangeCodeTokens(_ context.Context, _ string) (string, github.UserTokens, error) {
	f.exchanged++
	if f.exchangeErr != nil {
		return "", github.UserTokens{}, f.exchangeErr
	}
	return f.login, github.UserTokens{AccessToken: "usr-access", RefreshToken: f.exchangeRefresh}, nil
}
func (f *fakeOAuth) RefreshUserToken(_ context.Context, _ string) (github.UserTokens, error) {
	f.refreshed++
	if f.refreshErr != nil {
		return github.UserTokens{}, f.refreshErr
	}
	return github.UserTokens{AccessToken: f.refreshAccess, RefreshToken: f.refreshRefresh}, nil
}

// fakeLister is an in-memory InstallationLister.
type fakeLister struct {
	installs    []github.Installation
	installsErr error
	repos       []string
	reposErr    error
	instCalls   int
	repoCalls   int
}

func (f *fakeLister) UserInstallations(_ context.Context, _ string) ([]github.Installation, error) {
	f.instCalls++
	return f.installs, f.installsErr
}
func (f *fakeLister) InstallationRepositories(_ context.Context, _ string, _ int64) ([]string, error) {
	f.repoCalls++
	return f.repos, f.reposErr
}

// fakeCrypter is a reversible crypter that XOR-obfuscates the bytes (with a
// marker prefix) so the ciphertext does NOT contain the plaintext as a
// substring — matching the leak-proofing a real crypter guarantees — and
// round-trips deterministically.
type fakeCrypter struct {
	encErr error
	decErr error
}

const fakeCryptKey = 0x5a

func (c *fakeCrypter) Encrypt(_ context.Context, plaintext []byte, _ map[string]string) ([]byte, error) {
	if c.encErr != nil {
		return nil, c.encErr
	}
	out := append([]byte("enc:"), make([]byte, len(plaintext))...)
	for i, b := range plaintext {
		out[len("enc:")+i] = b ^ fakeCryptKey
	}
	return out, nil
}
func (c *fakeCrypter) Decrypt(_ context.Context, ciphertext []byte, _ map[string]string) ([]byte, error) {
	if c.decErr != nil {
		return nil, c.decErr
	}
	body := ciphertext[len("enc:"):]
	out := make([]byte, len(body))
	for i, b := range body {
		out[i] = b ^ fakeCryptKey
	}
	return out, nil
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
	refresh    map[string][]byte          // subject -> encrypted refresh token
	putErr     error
	getIDErr   error
	listErr    error
}

func newFakeStore() *fakeStore {
	return &fakeStore{grants: map[string]map[string]bool{}, identities: map[string]string{}, refresh: map[string][]byte{}}
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
	if s.listErr != nil {
		return nil, s.listErr
	}
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
func (s *fakeStore) PutIdentityWithRefresh(ctx context.Context, crypter grants.Crypter, subject, login, refreshToken string) error {
	if s.putErr != nil {
		return s.putErr
	}
	ct, err := crypter.Encrypt(ctx, []byte(refreshToken), map[string]string{"subject": subject})
	if err != nil {
		return err
	}
	s.identities[subject] = login
	s.refresh[subject] = ct
	return nil
}
func (s *fakeStore) GetRefreshToken(ctx context.Context, crypter grants.Crypter, subject string) (string, error) {
	ct, ok := s.refresh[subject]
	if !ok || len(ct) == 0 {
		return "", nil
	}
	pt, err := crypter.Decrypt(ctx, ct, map[string]string{"subject": subject})
	if err != nil {
		return "", err
	}
	return string(pt), nil
}

// --- tests -----------------------------------------------------------------

const (
	subA = "sub-alice"
	subB = "sub-bob"
)

// Every JWT-GUARDED route rejects an unauthenticated request with 401. The
// OAuth callback is deliberately NOT in this set: it is a browser redirect that
// carries no JWT and authenticates from the signed state instead (see
// TestCallback_* below), so it is exempt from the JWT choke point.
func TestUnauth_401_EveryRoute(t *testing.T) {
	e := newEnv(t)
	cases := []struct {
		method, path, body string
	}{
		{"GET", "/connect/github/start", ""},
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

// start with Accept: application/json -> 200 JSON {authorize_url}, no redirect
// (the SPA path: a fetch()+bearer that then navigates the window itself).
func TestStart_JSON_ReturnsAuthorizeURL(t *testing.T) {
	e := newEnv(t)
	r := httptest.NewRequest("GET", "/connect/github/start", nil)
	r.Header.Set("Authorization", "Bearer "+e.token(t, subA))
	r.Header.Set("Accept", "application/json")
	w := httptest.NewRecorder()
	e.handler.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("start(JSON) status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	var body struct {
		AuthorizeURL string `json:"authorize_url"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("start(JSON) body not JSON: %v (%s)", err, w.Body.String())
	}
	if !strings.Contains(body.AuthorizeURL, "state=") {
		t.Fatalf("authorize_url has no state: %s", body.AuthorizeURL)
	}
	if w.Header().Get("Location") != "" {
		t.Errorf("start(JSON) must not set a Location redirect header")
	}
}

// callback with a bad/forged state -> rejected, no exchange, no identity write.
// No JWT is sent: the callback does not use one.
func TestCallback_BadState_Rejected(t *testing.T) {
	e := newEnv(t)
	w := e.do(t, "GET", "/connect/github/callback?code=c&state=forged", "", "")
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

// A TAMPERED state (valid structure, broken HMAC) is rejected: the callback
// authenticates from the state's signature alone, so forging the bound sub by
// editing the payload must fail the HMAC check. No exchange, no write.
func TestCallback_TamperedState_Rejected(t *testing.T) {
	e := newEnv(t)
	good, _ := e.state.Issue(subA)
	// Flip the last character of the signature segment to break the HMAC while
	// keeping the token structurally valid (payload.sig).
	tampered := good[:len(good)-1]
	if good[len(good)-1] == 'A' {
		tampered += "B"
	} else {
		tampered += "A"
	}
	w := e.do(t, "GET", "/connect/github/callback?code=c&state="+tampered, "", "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("tampered-state callback status = %d, want 400", w.Code)
	}
	if e.oauth.exchanged != 0 {
		t.Error("code must not be exchanged on a tampered state")
	}
	if _, ok := e.store.identities[subA]; ok {
		t.Error("no identity must be persisted on a tampered state")
	}
}

// callback ok with a valid signed state and NO JWT -> identity persisted for
// the sub the state is bound to. This is the core of the browser-redirect fix:
// the callback carries no Cognito token, only the signed state.
func TestCallback_OK_NoJWT_PersistsIdentity(t *testing.T) {
	e := newEnv(t)
	stateForA, _ := e.state.Issue(subA)
	w := e.do(t, "GET", "/connect/github/callback?code=c&state="+stateForA, "", "")
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

// --- Option A: /connect/available-repos ------------------------------------

// availableReposResp mirrors the endpoint's JSON so tests can assert on it.
type availableReposResp struct {
	Repos []struct {
		FullName       string `json:"full_name"`
		AlreadyGranted bool   `json:"already_granted"`
	} `json:"repos"`
	GitHubLogin    string `json:"github_login"`
	NoInstallation bool   `json:"no_installation"`
}

// linkWithRefresh links subA with a stored (encrypted) refresh token, the state
// the endpoint requires, so a happy-path test starts from a realistic item.
func (e *testEnv) linkWithRefresh(t *testing.T, sub, login, refresh string) {
	t.Helper()
	if err := e.store.PutIdentityWithRefresh(context.Background(), e.crypter, sub, login, refresh); err != nil {
		t.Fatalf("seed refresh: %v", err)
	}
}

// Happy path: a linked user with a stored refresh token gets the paginated
// installation repos, with already-granted flags. The refresh is rotated and
// the NEW refresh token persisted; the OLD one is gone.
func TestAvailableRepos_Happy_PaginatedAndFlagged(t *testing.T) {
	e := newEnv(t)
	e.linkWithRefresh(t, subA, "octocat", "refresh-old")
	e.oauth.refreshAccess = "fresh-access"
	e.oauth.refreshRefresh = "refresh-new"
	e.lister.installs = []github.Installation{{ID: 42, Account: "acme", AppID: 0}}
	// Simulate a paginated result already merged by the lister (the lister owns
	// pagination; the handler consumes the merged slice).
	e.lister.repos = []string{"acme/a", "acme/b", "acme/c"}
	e.store.grants[subA] = map[string]bool{"acme/b": true} // already granted

	w := e.do(t, "GET", "/connect/available-repos", e.token(t, subA), "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	var out availableReposResp
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Repos) != 3 {
		t.Fatalf("repos = %d, want 3 (%v)", len(out.Repos), out.Repos)
	}
	if out.GitHubLogin != "octocat" {
		t.Errorf("github_login = %q, want octocat", out.GitHubLogin)
	}
	granted := map[string]bool{}
	for _, r := range out.Repos {
		granted[r.FullName] = r.AlreadyGranted
	}
	if !granted["acme/b"] {
		t.Error("acme/b should be flagged already_granted")
	}
	if granted["acme/a"] || granted["acme/c"] {
		t.Error("acme/a and acme/c must not be flagged already_granted")
	}
	// Rotated refresh token persisted: the stored token now decrypts to the NEW
	// value, not the old one.
	got, err := e.store.GetRefreshToken(context.Background(), e.crypter, subA)
	if err != nil {
		t.Fatalf("read rotated refresh: %v", err)
	}
	if got != "refresh-new" {
		t.Errorf("stored refresh = %q, want refresh-new (rotation not persisted)", got)
	}
}

// No linked identity -> 409, and no refresh/list/verify work is attempted.
func TestAvailableRepos_NoIdentity_409(t *testing.T) {
	e := newEnv(t)
	w := e.do(t, "GET", "/connect/available-repos", e.token(t, subA), "")
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body=%s)", w.Code, w.Body.String())
	}
	if e.oauth.refreshed != 0 || e.lister.instCalls != 0 {
		t.Error("no refresh/installation call may run without a linked identity")
	}
}

// Linked identity but NO stored refresh token (an older link) -> 409 re-link,
// no refresh attempted.
func TestAvailableRepos_LinkedButNoRefresh_409(t *testing.T) {
	e := newEnv(t)
	e.store.identities[subA] = "octocat" // linked, but no refresh token stored
	w := e.do(t, "GET", "/connect/available-repos", e.token(t, subA), "")
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body=%s)", w.Code, w.Body.String())
	}
	if e.oauth.refreshed != 0 {
		t.Error("no refresh may run without a stored refresh token")
	}
}

// Refresh failure -> fail closed (non-200), NEVER an empty list, no repo listed.
func TestAvailableRepos_RefreshFailure_FailsClosed(t *testing.T) {
	e := newEnv(t)
	e.linkWithRefresh(t, subA, "octocat", "refresh-old")
	e.oauth.refreshErr = errors.New("invalid_grant")
	w := e.do(t, "GET", "/connect/available-repos", e.token(t, subA), "")
	if w.Code == http.StatusOK {
		t.Fatalf("a refresh failure must not return 200 (body=%s)", w.Body.String())
	}
	if e.lister.instCalls != 0 {
		t.Error("installations must not be listed after a refresh failure")
	}
	// The body must not be an empty repo list masquerading as success.
	if strings.Contains(w.Body.String(), `"repos"`) {
		t.Error("a failed refresh must not return a repos list")
	}
}

// A decrypt error on the stored refresh token fails closed (never lists).
func TestAvailableRepos_DecryptError_FailsClosed(t *testing.T) {
	e := newEnv(t)
	e.linkWithRefresh(t, subA, "octocat", "refresh-old")
	e.crypter.decErr = errors.New("kms denied")
	w := e.do(t, "GET", "/connect/available-repos", e.token(t, subA), "")
	if w.Code == http.StatusOK {
		t.Fatalf("a decrypt error must not return 200 (body=%s)", w.Body.String())
	}
	if e.oauth.refreshed != 0 {
		t.Error("no refresh may run when the stored token cannot be decrypted")
	}
}

// The plaintext refresh/access token must NEVER appear in the response body
// (token-never-logged / never-leaked invariant, checked at the HTTP boundary).
func TestAvailableRepos_TokenNeverLeaksToResponse(t *testing.T) {
	e := newEnv(t)
	e.linkWithRefresh(t, subA, "octocat", "refresh-old")
	e.oauth.refreshAccess = "fresh-access-SECRET"
	e.oauth.refreshRefresh = "refresh-new-SECRET"
	e.lister.installs = []github.Installation{{ID: 42, Account: "acme"}}
	e.lister.repos = []string{"acme/a"}
	w := e.do(t, "GET", "/connect/available-repos", e.token(t, subA), "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, secret := range []string{"fresh-access-SECRET", "refresh-new-SECRET", "refresh-old"} {
		if strings.Contains(body, secret) {
			t.Fatalf("token material leaked into response body: %q", secret)
		}
	}
	// And the stored ciphertext is not the plaintext.
	if raw, ok := e.store.refresh[subA]; ok && strings.Contains(string(raw), "refresh-new-SECRET") {
		t.Fatal("stored refresh token is not encrypted (plaintext present)")
	}
}

// Ambiguous installations (several, none identifiable) -> fail closed to
// no_installation rather than guessing.
func TestAvailableRepos_AmbiguousInstallations_NoGuess(t *testing.T) {
	e := newEnv(t)
	e.linkWithRefresh(t, subA, "octocat", "refresh-old")
	e.oauth.refreshAccess = "fresh-access"
	e.oauth.refreshRefresh = "refresh-new"
	e.lister.installs = []github.Installation{{ID: 1, AppID: 111}, {ID: 2, AppID: 222}}
	w := e.do(t, "GET", "/connect/available-repos", e.token(t, subA), "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	var out availableReposResp
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !out.NoInstallation || len(out.Repos) != 0 {
		t.Fatalf("ambiguous installs must yield no_installation + empty list, got %+v", out)
	}
	if e.lister.repoCalls != 0 {
		t.Error("must not list repos when the installation cannot be identified")
	}
}

// The callback persists the ENCRYPTED refresh token when GitHub returns one.
func TestCallback_PersistsEncryptedRefresh(t *testing.T) {
	e := newEnv(t)
	e.oauth.exchangeRefresh = "refresh-from-callback"
	stateForA, _ := e.state.Issue(subA)
	w := e.do(t, "GET", "/connect/github/callback?code=c&state="+stateForA, "", "")
	if w.Code != http.StatusOK {
		t.Fatalf("callback status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	if e.store.identities[subA] != "octocat" {
		t.Errorf("identity = %q, want octocat", e.store.identities[subA])
	}
	// A ciphertext is stored, and it is not the plaintext.
	raw, ok := e.store.refresh[subA]
	if !ok || len(raw) == 0 {
		t.Fatal("no encrypted refresh token persisted on callback")
	}
	if strings.Contains(string(raw), "refresh-from-callback") {
		t.Fatal("refresh token stored in plaintext")
	}
	got, err := e.store.GetRefreshToken(context.Background(), e.crypter, subA)
	if err != nil {
		t.Fatalf("read back refresh: %v", err)
	}
	if got != "refresh-from-callback" {
		t.Errorf("decrypted refresh = %q, want refresh-from-callback", got)
	}
}
