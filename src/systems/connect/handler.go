package connect

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/hmain/cainban/src/systems/auth"
	"github.com/hmain/cainban/src/systems/github"
)

// Authenticator validates a bearer JWT signature-first and returns the caller
// identity. It is satisfied by *auth.Validator (its Validate method); declaring
// it as an interface lets tests inject a self-signed validator with no network.
// The connect API needs only the validated Subject — it does NOT use the repo
// claim (a connect grant is authorized by GitHub, not by an existing claim), so
// this is the token's authenticity, not its authorization.
type Authenticator interface {
	Validate(rawToken string) (*auth.Identity, error)
}

// OAuthLeg is the GitHub user-OAuth surface the handler needs: build the
// authorize URL, and exchange a code for the connecting user's GitHub login.
// Satisfied by *github.OAuth; a fake implements it in tests.
type OAuthLeg interface {
	AuthorizeURL(state string) string
	ExchangeCode(ctx context.Context, code string) (login string, err error)
}

// Verifier answers "does this GitHub login genuinely have access to
// owner/repo?" server-side. Satisfied by *github.Verifier; a mock implements it
// in tests. A non-nil error MUST be treated as DENY (fail closed).
type Verifier interface {
	VerifyRepoAccess(ctx context.Context, owner, repo string, principal github.Principal) (bool, error)
}

// GrantStore is the subset of the grants store the connect API writes/reads.
// Satisfied by *grants.Store; an in-memory fake implements it in tests. Every
// method is scoped to a subject, and the handler ALWAYS passes the VALIDATED
// sub — never a client-supplied subject.
type GrantStore interface {
	PutGrant(ctx context.Context, subject, repo string) error
	DeleteGrant(ctx context.Context, subject, repo string) error
	ListReposForSubject(ctx context.Context, subject string) ([]string, error)
	PutIdentity(ctx context.Context, subject, githubLogin string) error
	GetIdentity(ctx context.Context, subject string) (string, error)
}

// Handler serves the /connect/* routes. It holds only interfaces, so it is
// fully unit-testable with fakes and never touches the network or AWS in tests.
type Handler struct {
	auth   Authenticator
	oauth  OAuthLeg
	verify Verifier
	grants GrantStore
	state  *StateSigner
	// successRedirect is where the callback sends the browser after a
	// successful identity link (optional; empty => a 200 confirmation instead).
	successRedirect string
}

// Config wires a Handler's dependencies. All are required except
// SuccessRedirect.
type Config struct {
	Auth            Authenticator
	OAuth           OAuthLeg
	Verify          Verifier
	Grants          GrantStore
	State           *StateSigner
	SuccessRedirect string
}

// NewHandler builds a Handler, rejecting a nil required dependency so a
// misconfiguration is a cold-start failure, never a fail-open endpoint.
func NewHandler(cfg Config) (*Handler, error) {
	if cfg.Auth == nil || cfg.OAuth == nil || cfg.Verify == nil || cfg.Grants == nil || cfg.State == nil {
		return nil, errors.New("connect: Auth, OAuth, Verify, Grants and State are all required")
	}
	return &Handler{
		auth:            cfg.Auth,
		oauth:           cfg.OAuth,
		verify:          cfg.Verify,
		grants:          cfg.Grants,
		state:           cfg.State,
		successRedirect: cfg.SuccessRedirect,
	}, nil
}

// ServeHTTP routes the /connect/* endpoints. Method+path are matched
// explicitly; anything else is 404/405. Every handler first authenticates
// signature-first (see authenticate) before doing anything else.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimRight(r.URL.Path, "/")
	switch path {
	case "/connect/github/start":
		h.methodGuard(w, r, http.MethodGet, h.handleStart)
	case "/connect/github/callback":
		h.methodGuard(w, r, http.MethodGet, h.handleCallback)
	case "/connect/repo":
		switch r.Method {
		case http.MethodPost:
			h.handleRepoPost(w, r)
		case http.MethodDelete:
			h.handleRepoDelete(w, r)
		default:
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	case "/connect/repos":
		h.methodGuard(w, r, http.MethodGet, h.handleReposList)
	default:
		writeError(w, http.StatusNotFound, "not found")
	}
}

// methodGuard enforces a single allowed method for a route.
func (h *Handler) methodGuard(w http.ResponseWriter, r *http.Request, method string, next func(http.ResponseWriter, *http.Request, *auth.Identity)) {
	if r.Method != method {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	id, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	next(w, r, id)
}

// authenticate validates the bearer token SIGNATURE-FIRST. On failure it writes
// the correct 401/403 and returns ok=false; nothing else in a route runs until
// this passes. This is the single choke point that guarantees "every /connect
// route validates the Cognito JWT first".
func (h *Handler) authenticate(w http.ResponseWriter, r *http.Request) (*auth.Identity, bool) {
	token, err := bearerToken(r)
	if err != nil {
		writeError(w, auth.HTTPStatus(err), "unauthorized")
		return nil, false
	}
	id, err := h.auth.Validate(token)
	if err != nil {
		writeError(w, auth.HTTPStatus(err), "unauthorized")
		return nil, false
	}
	if id == nil || strings.TrimSpace(id.Subject) == "" {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return nil, false
	}
	return id, true
}

// handleStart (GET /connect/github/start) issues a sub-bound anti-CSRF state
// and redirects the browser to GitHub's authorize URL.
func (h *Handler) handleStart(w http.ResponseWriter, r *http.Request, id *auth.Identity) {
	state, err := h.state.Issue(id.Subject)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start connect flow")
		return
	}
	http.Redirect(w, r, h.oauth.AuthorizeURL(state), http.StatusFound)
}

// handleCallback (GET /connect/github/callback?code&state) validates the state
// (anti-CSRF + sub binding), exchanges the code for the user's GitHub login,
// and persists that login for the validated sub. It never trusts a
// client-supplied login.
func (h *Handler) handleCallback(w http.ResponseWriter, r *http.Request, id *auth.Identity) {
	q := r.URL.Query()
	code := strings.TrimSpace(q.Get("code"))
	state := strings.TrimSpace(q.Get("state"))
	if code == "" || state == "" {
		writeError(w, http.StatusBadRequest, "missing code or state")
		return
	}
	// Anti-CSRF: the state must verify AND be bound to THIS caller's sub.
	if err := h.state.Verify(state, id.Subject); err != nil {
		writeError(w, http.StatusBadRequest, "invalid state")
		return
	}
	// Exchange the code for the user's GitHub login (server-side, via GitHub).
	login, err := h.oauth.ExchangeCode(r.Context(), code)
	if err != nil || strings.TrimSpace(login) == "" {
		// Fail closed: no identity learned => nothing persisted.
		writeError(w, http.StatusBadGateway, "could not complete GitHub authorization")
		return
	}
	// Persist the linked identity for the VALIDATED sub only.
	if err := h.grants.PutIdentity(r.Context(), id.Subject, login); err != nil {
		writeError(w, http.StatusInternalServerError, "could not persist GitHub identity")
		return
	}
	if h.successRedirect != "" {
		http.Redirect(w, r, h.successRedirect, http.StatusFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"linked":       true,
		"github_login": login,
	})
}

// repoBody is the JSON body of POST/DELETE /connect/repo.
type repoBody struct {
	Owner string `json:"owner"`
	Repo  string `json:"repo"`
}

// handleRepoPost (POST /connect/repo {owner,repo}) requires a linked GitHub
// identity, verifies access server-side against that identity, and writes the
// grant ONLY on an affirmative verify. A verify error fails closed.
func (h *Handler) handleRepoPost(w http.ResponseWriter, r *http.Request) {
	id, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	owner, repo, canon, ok := decodeRepo(w, r)
	if !ok {
		return
	}
	// Require a linked identity: authorization is decided against the login the
	// sub authorized via OAuth, never a client-supplied login.
	login, err := h.grants.GetIdentity(r.Context(), id.Subject)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read linked identity")
		return
	}
	if strings.TrimSpace(login) == "" {
		writeError(w, http.StatusConflict, "no linked GitHub identity: start /connect/github/start first")
		return
	}
	// Server-side authorization. FAIL CLOSED on any error.
	allowed, err := h.verify.VerifyRepoAccess(r.Context(), owner, repo, github.Principal{Login: login})
	if err != nil {
		writeError(w, http.StatusBadGateway, "could not verify repo access")
		return
	}
	if !allowed {
		writeError(w, http.StatusForbidden, "GitHub access to repo not verified")
		return
	}
	if err := h.grants.PutGrant(r.Context(), id.Subject, canon); err != nil {
		writeError(w, http.StatusInternalServerError, "could not write grant")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"granted": canon})
}

// handleRepoDelete (DELETE /connect/repo {owner,repo}) revokes a grant for the
// validated sub. It does not require GitHub verification — revoking access is
// always safe.
func (h *Handler) handleRepoDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	_, _, canon, ok := decodeRepo(w, r)
	if !ok {
		return
	}
	if err := h.grants.DeleteGrant(r.Context(), id.Subject, canon); err != nil {
		writeError(w, http.StatusInternalServerError, "could not revoke grant")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"revoked": canon})
}

// handleReposList (GET /connect/repos) lists ONLY the caller's granted repos
// plus the linked GitHub login.
func (h *Handler) handleReposList(w http.ResponseWriter, r *http.Request, id *auth.Identity) {
	repos, err := h.grants.ListReposForSubject(r.Context(), id.Subject)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not list grants")
		return
	}
	login, err := h.grants.GetIdentity(r.Context(), id.Subject)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read linked identity")
		return
	}
	if repos == nil {
		repos = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"repos":        repos,
		"github_login": login,
	})
}

// decodeRepo reads {owner,repo} from the request body and normalizes it to a
// canonical owner/repo. It returns the raw owner, raw repo, canonical string,
// and ok=false (having written a 400) on a malformed body/repo.
func decodeRepo(w http.ResponseWriter, r *http.Request) (owner, repo, canon string, ok bool) {
	var body repoBody
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16))
	if err := dec.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return "", "", "", false
	}
	owner = strings.TrimSpace(body.Owner)
	repo = strings.TrimSpace(body.Repo)
	if owner == "" || repo == "" {
		writeError(w, http.StatusBadRequest, "owner and repo are required")
		return "", "", "", false
	}
	c, err := auth.NormalizeRepo(owner + "/" + repo)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid owner/repo")
		return "", "", "", false
	}
	return owner, repo, c, true
}

// bearerToken extracts the token from Authorization: Bearer <token>. A missing
// or malformed header is a 401 (auth.Error of kind Unauthenticated).
func bearerToken(r *http.Request) (string, error) {
	if r == nil {
		return "", &auth.Error{Kind: auth.KindUnauthenticated, Message: "no request"}
	}
	hdr := r.Header.Get("Authorization")
	if hdr == "" {
		return "", &auth.Error{Kind: auth.KindUnauthenticated, Message: "missing Authorization header"}
	}
	const prefix = "Bearer "
	if len(hdr) <= len(prefix) || !strings.EqualFold(hdr[:len(prefix)], prefix) {
		return "", &auth.Error{Kind: auth.KindUnauthenticated, Message: "not a Bearer token"}
	}
	return strings.TrimSpace(hdr[len(prefix):]), nil
}

// writeJSON writes a JSON response with a status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError writes a JSON {"error": msg} with a status code. The message is a
// generic, non-leaking string (never a raw internal error).
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// defaultNonce returns a 128-bit random nonce, base64url-encoded.
func defaultNonce() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return b64.EncodeToString(b[:]), nil
}
