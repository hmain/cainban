package connect

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/hmain/cainban/src/systems/auth"
	"github.com/hmain/cainban/src/systems/github"
	"github.com/hmain/cainban/src/systems/grants"
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
// authorize URL, exchange a code for the connecting user's GitHub login (plus,
// for Option A, the user tokens including the refresh token), and refresh a
// stored refresh token into a fresh short-lived access token at list time.
// Satisfied by *github.OAuth; a fake implements it in tests.
type OAuthLeg interface {
	AuthorizeURL(state string) string
	ExchangeCode(ctx context.Context, code string) (login string, err error)
	ExchangeCodeTokens(ctx context.Context, code string) (login string, tokens github.UserTokens, err error)
	RefreshUserToken(ctx context.Context, refreshToken string) (github.UserTokens, error)
}

// InstallationLister is the GitHub App surface used to list the repos the
// signed-in user can access through the App installation (Option A). Both calls
// use the USER's short-lived access token (minted from the stored refresh
// token), so the result is scoped to what THIS user can see. Satisfied by
// *github.Client; a fake implements it in tests.
type InstallationLister interface {
	UserInstallations(ctx context.Context, userToken string) ([]github.Installation, error)
	InstallationRepositories(ctx context.Context, userToken string, installationID int64) ([]string, error)
	// AppSlug returns THIS App's slug (GET /app, App-JWT auth) so the connect
	// API can build the install URL with no operator-supplied slug env var.
	AppSlug(ctx context.Context) (string, error)
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
	// PutIdentityWithRefresh persists the login AND the encrypted refresh token
	// (Option A). GetRefreshToken reads + decrypts it (returns "" when none).
	PutIdentityWithRefresh(ctx context.Context, crypter grants.Crypter, subject, githubLogin, refreshToken string) error
	GetRefreshToken(ctx context.Context, crypter grants.Crypter, subject string) (string, error)
	// GetDefaultRepo / SetDefaultRepo read and write the subject's default repo
	// (the grants table META item). The connect API sets the default to the
	// FIRST repo a subject grants, so a single-repo user's MCP config can omit
	// the X-Cainban-Repo header (the token's default_repo claim names the repo).
	GetDefaultRepo(ctx context.Context, subject string) (string, error)
	SetDefaultRepo(ctx context.Context, subject, repo string) error
}

// Handler serves the /connect/* routes. It holds only interfaces, so it is
// fully unit-testable with fakes and never touches the network or AWS in tests.
type Handler struct {
	auth    Authenticator
	oauth   OAuthLeg
	verify  Verifier
	lister  InstallationLister
	grants  GrantStore
	crypter grants.Crypter
	state   *StateSigner
	// appSlug / appID identify THIS GitHub App so available-repos picks the
	// user's installation of OUR App when the user can see several installations.
	// Either may be empty/zero; when both are unset and the user has exactly one
	// installation, that one is used.
	appSlug string
	appID   int64
	// cachedSlug memoizes the slug resolved from GET /app (an App's slug never
	// changes); slugMu guards it across concurrent requests.
	cachedSlug string
	slugMu     sync.Mutex
	// successRedirect is where the callback sends the browser after a
	// successful identity link (optional; empty => a 200 confirmation instead).
	successRedirect string
}

// Config wires a Handler's dependencies. All are required except
// SuccessRedirect, AppSlug and AppID.
type Config struct {
	Auth            Authenticator
	OAuth           OAuthLeg
	Verify          Verifier
	Lister          InstallationLister
	Grants          GrantStore
	Crypter         grants.Crypter
	State           *StateSigner
	AppSlug         string
	AppID           int64
	SuccessRedirect string
}

// NewHandler builds a Handler, rejecting a nil required dependency so a
// misconfiguration is a cold-start failure, never a fail-open endpoint.
func NewHandler(cfg Config) (*Handler, error) {
	if cfg.Auth == nil || cfg.OAuth == nil || cfg.Verify == nil || cfg.Grants == nil || cfg.State == nil {
		return nil, errors.New("connect: Auth, OAuth, Verify, Grants and State are all required")
	}
	if cfg.Lister == nil {
		return nil, errors.New("connect: Lister is required (available-repos listing)")
	}
	if cfg.Crypter == nil {
		return nil, errors.New("connect: Crypter is required (encrypted refresh-token storage)")
	}
	return &Handler{
		auth:            cfg.Auth,
		oauth:           cfg.OAuth,
		verify:          cfg.Verify,
		lister:          cfg.Lister,
		grants:          cfg.Grants,
		crypter:         cfg.Crypter,
		state:           cfg.State,
		appSlug:         cfg.AppSlug,
		appID:           cfg.AppID,
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
		// The callback is a browser redirect from GitHub and CANNOT carry the
		// Cognito JWT, so it is NOT behind methodGuard/authenticate. It
		// authenticates from the HMAC-signed, sub-bound state instead (see
		// handleCallback). This matches the API Gateway route, which exempts
		// this one path from the JWT authorizer.
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		h.handleCallback(w, r)
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
	case "/connect/available-repos":
		h.methodGuard(w, r, http.MethodGet, h.handleAvailableRepos)
	case "/connect/app-info":
		h.methodGuard(w, r, http.MethodGet, h.handleAppInfo)
	default:
		writeError(w, http.StatusNotFound, "not found")
	}
}

// handleAppInfo (GET /connect/app-info) returns metadata the SPA needs to guide
// the user through installing the GitHub App: the App's install URL. When the
// available-repos list is empty (App not installed on any account the user can
// access), the SPA sends the user here to install, then back.
//
// The install URL is github.com/apps/<slug>/installations/new with a signed,
// sub-bound `state` so the post-install return can be tied back to this user
// (GitHub echoes state to the App's Setup URL). The App slug is public (it is
// in the App's own URL), so this carries no secret. It needs only a valid JWT,
// NOT a linked GitHub identity — the user installs the App precisely because
// they have nothing linked/available yet.
func (h *Handler) handleAppInfo(w http.ResponseWriter, r *http.Request, id *auth.Identity) {
	// Resolve the App slug: prefer a runtime GET /app (self-configuring, no env
	// var needed), fall back to a configured slug, so a GitHub blip still yields
	// a usable button when the operator set one. A resolved slug is cached (it
	// never changes for an App).
	slug := h.resolveAppSlug(r.Context())
	if slug == "" {
		// Cannot build an install URL. Report empty so the SPA keeps the manual
		// fallback rather than a broken link.
		writeJSON(w, http.StatusOK, map[string]any{"install_url": "", "app_slug": ""})
		return
	}
	state, err := h.state.Issue(id.Subject)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not build install URL")
		return
	}
	installURL := "https://github.com/apps/" + url.PathEscape(slug) +
		"/installations/new?state=" + url.QueryEscape(state)
	writeJSON(w, http.StatusOK, map[string]any{
		"install_url": installURL,
		"app_slug":    slug,
	})
}

// resolveAppSlug returns THIS App's slug, preferring a live GET /app (cached
// after first success) and falling back to the configured appSlug. Empty only
// when both the fetch fails AND no slug was configured.
func (h *Handler) resolveAppSlug(ctx context.Context) string {
	h.slugMu.Lock()
	cached := h.cachedSlug
	h.slugMu.Unlock()
	if cached != "" {
		return cached
	}
	if h.lister != nil {
		if s, err := h.lister.AppSlug(ctx); err == nil && strings.TrimSpace(s) != "" {
			h.slugMu.Lock()
			h.cachedSlug = s
			h.slugMu.Unlock()
			return s
		}
	}
	return strings.TrimSpace(h.appSlug)
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
// and hands back GitHub's authorize URL.
//
// A browser SPA cannot set the Authorization header on a plain navigation, so
// it calls this endpoint with fetch()+bearer — but it then CANNOT follow a 302
// into github.com (a cross-origin auth page has no CORS headers, and a manual
// redirect hides the Location). So when the client asks for JSON
// (Accept: application/json), we return {"authorize_url": "..."} and let the SPA
// navigate the top-level window there itself. For a non-JSON client (a direct
// browser hit) we keep the plain 302 redirect.
func (h *Handler) handleStart(w http.ResponseWriter, r *http.Request, id *auth.Identity) {
	state, err := h.state.Issue(id.Subject)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start connect flow")
		return
	}
	authorizeURL := h.oauth.AuthorizeURL(state)
	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		writeJSON(w, http.StatusOK, map[string]any{"authorize_url": authorizeURL})
		return
	}
	http.Redirect(w, r, authorizeURL, http.StatusFound)
}

// handleCallback (GET /connect/github/callback?code&state) is the GitHub OAuth
// redirect target. It is a plain browser navigation and carries NO Cognito JWT,
// so — unlike every other /connect route — it does not call authenticate().
// Instead it authenticates ENTIRELY from the state param:
//
//   - the state is HMAC-verified and unexpired (VerifyAndExtractSub), so it is
//     unforgeable and could only have been minted by handleStart;
//   - handleStart required a valid Cognito JWT and bound the state to that
//     validated sub, so the sub extracted here traces back to a real
//     authenticated user — the signed state IS the proof of identity.
//
// It then exchanges the code for the user's GitHub login and persists that
// login for the state-bound sub. It never trusts a client-supplied login or
// subject.
func (h *Handler) handleCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	code := strings.TrimSpace(q.Get("code"))
	state := strings.TrimSpace(q.Get("state"))
	if code == "" || state == "" {
		writeError(w, http.StatusBadRequest, "missing code or state")
		return
	}
	// Authenticate from the signed state: verify HMAC + expiry and extract the
	// sub it is bound to. Any failure rejects the callback.
	subject, err := h.state.VerifyAndExtractSub(state)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid state")
		return
	}
	// Exchange the code for the user's GitHub login AND user tokens (server-side,
	// via GitHub). Option A needs the REFRESH token persisted so a fresh access
	// token can be minted at list time.
	login, tokens, err := h.oauth.ExchangeCodeTokens(r.Context(), code)
	if err != nil || strings.TrimSpace(login) == "" {
		// Fail closed: no identity learned => nothing persisted.
		writeError(w, http.StatusBadGateway, "could not complete GitHub authorization")
		return
	}
	// Persist the linked identity for the state-bound sub only. When GitHub
	// returned a refresh token (App has "Expire user authorization tokens"
	// enabled), store it ENCRYPTED alongside the login; otherwise persist the
	// login alone. The plaintext refresh token never touches DynamoDB.
	if strings.TrimSpace(tokens.RefreshToken) != "" {
		err = h.grants.PutIdentityWithRefresh(r.Context(), h.crypter, subject, login, tokens.RefreshToken)
	} else {
		err = h.grants.PutIdentity(r.Context(), subject, login)
	}
	if err != nil {
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
	// Make the first granted repo the subject's default, so a single-repo user's
	// MCP config can omit the X-Cainban-Repo header (the token's default_repo
	// claim then names the repo). Best-effort relative to the grant — the grant
	// already landed and must not be undone by a default-set failure.
	h.setDefaultIfUnset(r.Context(), id.Subject, canon)
	writeJSON(w, http.StatusOK, map[string]any{"granted": canon})
}

// setDefaultIfUnset sets the subject's default repo to canon ONLY when they have
// no default yet. It is best-effort: every failure path is logged and swallowed,
// because the grant has already succeeded and a missing default degrades to
// header-required behavior, never a failed grant. It never overrides a default a
// multi-repo user already chose (the FIRST repo connected stays the default).
func (h *Handler) setDefaultIfUnset(ctx context.Context, subject, canon string) {
	current, err := h.grants.GetDefaultRepo(ctx, subject)
	if err != nil {
		log.Printf("connect: could not read default repo for sub=%q, leaving unset: %v", subject, err)
		return
	}
	if strings.TrimSpace(current) != "" {
		return // already has a default; never clobber it
	}
	if err := h.grants.SetDefaultRepo(ctx, subject, canon); err != nil {
		log.Printf("connect: could not set default repo for sub=%q (grant still succeeded): %v", subject, err)
	}
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
	// If the removed repo was this subject's default, the META default_repo now
	// points at a repo they can no longer access — the pre-token trigger would
	// mint a stale default_repo claim. Re-point it to a remaining grant, or
	// clear it when none remain. Per-user only: this touches just THIS subject's
	// grants partition, never another user's and never any board data. Best-
	// effort, mirroring setDefaultIfUnset: the grant removal already succeeded
	// and must not be undone by a default-cleanup failure.
	h.repointDefaultAfterRemoval(r.Context(), id.Subject, canon)
	writeJSON(w, http.StatusOK, map[string]any{"revoked": canon})
}

// repointDefaultAfterRemoval keeps the subject's default_repo consistent after a
// grant is revoked. If the current default is NOT the removed repo it does
// nothing. If it IS, it re-points the default to the first remaining grant, or
// clears it (SetDefaultRepo with "") when the subject has no grants left. Every
// failure path is logged and swallowed, because the revoke already succeeded and
// a stale/missing default degrades to header-required MCP behavior, never a
// failed removal.
func (h *Handler) repointDefaultAfterRemoval(ctx context.Context, subject, removed string) {
	current, err := h.grants.GetDefaultRepo(ctx, subject)
	if err != nil {
		log.Printf("connect: could not read default repo for sub=%q after removal (revoke still succeeded): %v", subject, err)
		return
	}
	if strings.TrimSpace(current) == "" || current != removed {
		return // no default, or the default was a different repo — nothing to do
	}
	// The removed repo WAS the default: pick a replacement from what remains.
	remaining, err := h.grants.ListReposForSubject(ctx, subject)
	if err != nil {
		log.Printf("connect: could not list remaining repos for sub=%q to re-point default (revoke still succeeded): %v", subject, err)
		return
	}
	replacement := "" // "" clears the META item
	if len(remaining) > 0 {
		replacement = remaining[0] // ListReposForSubject returns sorted, stable
	}
	if err := h.grants.SetDefaultRepo(ctx, subject, replacement); err != nil {
		log.Printf("connect: could not re-point default repo for sub=%q after removal (revoke still succeeded): %v", subject, err)
	}
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

// availableRepo is one repo the signed-in user can access through the App
// installation, with whether they have already granted it.
type availableRepo struct {
	FullName       string `json:"full_name"`
	AlreadyGranted bool   `json:"already_granted"`
}

// handleAvailableRepos (GET /connect/available-repos) lists the repos the
// SIGNED-IN USER can access through the GitHub App installation (Option A),
// each flagged with whether it is already granted. It is Cognito-JWT-authed
// (behind the same authorizer as the other /connect routes — see methodGuard).
//
// Flow (all scoped to the validated sub):
//
//  1. Require a linked GitHub identity (IDENTITY#github). None => 409 so the SPA
//     prompts "Link GitHub first" (same contract as the repo-post path).
//  2. Read + decrypt the stored refresh token. None stored (an older link made
//     before Option A) => 409 asking the user to re-link.
//  3. Refresh it into a short-lived user access token, and persist the ROTATED
//     refresh token ATOMICALLY before using the access token (GitHub rotates
//     the refresh token on every refresh; single-use).
//  4. With the user token, list the user's installations, pick THIS App's
//     installation, and list its repositories (paginated).
//  5. Subtract/flag the repos already granted to this sub.
//
// FAIL CLOSED: any token/refresh/list error returns a NON-200 with a clear
// message the SPA renders as "couldn't load your repos — enter one manually".
// It NEVER returns an empty list that could be mistaken for "no repos", and
// NEVER fabricates a repo.
func (h *Handler) handleAvailableRepos(w http.ResponseWriter, r *http.Request, id *auth.Identity) {
	ctx := r.Context()

	// (1) Linked identity required.
	login, err := h.grants.GetIdentity(ctx, id.Subject)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read linked identity")
		return
	}
	if strings.TrimSpace(login) == "" {
		writeError(w, http.StatusConflict, "no linked GitHub identity: start /connect/github/start first")
		return
	}

	// (2) Stored (encrypted) refresh token required.
	refreshToken, err := h.grants.GetRefreshToken(ctx, h.crypter, id.Subject)
	if err != nil {
		// Decrypt/DynamoDB error — fail closed.
		writeError(w, http.StatusBadGateway, "could not load your GitHub authorization; enter a repo manually")
		return
	}
	if strings.TrimSpace(refreshToken) == "" {
		// Linked, but no refresh token on file (older link). Ask to re-link.
		writeError(w, http.StatusConflict, "GitHub authorization is incomplete: re-link your GitHub identity to list repos")
		return
	}

	// (3) Refresh -> fresh access token, and PERSIST the rotated refresh token
	// atomically BEFORE using the access token (single-use rotation).
	tokens, err := h.oauth.RefreshUserToken(ctx, refreshToken)
	if err != nil || strings.TrimSpace(tokens.AccessToken) == "" {
		writeError(w, http.StatusBadGateway, "could not refresh your GitHub authorization; enter a repo manually")
		return
	}
	if strings.TrimSpace(tokens.RefreshToken) != "" {
		// Store the new refresh token before we rely on the access token, so a
		// crash after this point never leaves a spent refresh token on file.
		if err := h.grants.PutIdentityWithRefresh(ctx, h.crypter, id.Subject, login, tokens.RefreshToken); err != nil {
			writeError(w, http.StatusInternalServerError, "could not persist rotated GitHub authorization")
			return
		}
	}

	// (4) List the user's installations and pick THIS App's installation.
	installs, err := h.lister.UserInstallations(ctx, tokens.AccessToken)
	if err != nil {
		writeError(w, http.StatusBadGateway, "could not list your GitHub installations; enter a repo manually")
		return
	}
	inst, ok := h.pickInstallation(installs)
	if !ok {
		// No installation of this App the user can see: nothing to list, but a
		// definitive "no installation" (not an error). Return an empty list with
		// a flag so the SPA shows manual entry with a clear reason rather than
		// mistaking it for "no repos".
		writeJSON(w, http.StatusOK, map[string]any{
			"repos":           []availableRepo{},
			"github_login":    login,
			"no_installation": true,
		})
		return
	}

	// (5) List the installation's repositories (paginated), scoped to the user.
	fullNames, err := h.lister.InstallationRepositories(ctx, tokens.AccessToken, inst.ID)
	if err != nil {
		writeError(w, http.StatusBadGateway, "could not list your accessible repos; enter a repo manually")
		return
	}

	// Flag already-granted repos for this sub.
	granted, err := h.grants.ListReposForSubject(ctx, id.Subject)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read your grants")
		return
	}
	grantedSet := make(map[string]bool, len(granted))
	for _, g := range granted {
		grantedSet[g] = true
	}
	repos := make([]availableRepo, 0, len(fullNames))
	for _, fn := range fullNames {
		repos = append(repos, availableRepo{FullName: fn, AlreadyGranted: grantedSet[fn]})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"repos":        repos,
		"github_login": login,
	})
}

// pickInstallation chooses THIS App's installation from the user's visible
// installations. It prefers a match on the configured app id, then app slug;
// failing both, if the user has exactly one installation it uses that one
// (single-App deployments). It returns ok=false when no installation can be
// chosen.
func (h *Handler) pickInstallation(installs []github.Installation) (github.Installation, bool) {
	if len(installs) == 0 {
		return github.Installation{}, false
	}
	if h.appID > 0 {
		for _, in := range installs {
			if in.AppID == h.appID {
				return in, true
			}
		}
	}
	if slug := strings.TrimSpace(h.appSlug); slug != "" {
		for _, in := range installs {
			if strings.EqualFold(in.AppSlug, slug) {
				return in, true
			}
		}
	}
	if len(installs) == 1 {
		return installs[0], true
	}
	// Ambiguous: several installations and no way to identify ours. Fail closed
	// (do not guess) — the SPA falls back to manual entry.
	return github.Installation{}, false
}

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
