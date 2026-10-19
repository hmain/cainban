package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hmain/cainban/src/systems/auth"
	"github.com/hmain/cainban/src/systems/store"
)

var wtNow = time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)

const wtSecret = "test-widget-secret-0123456789"

// --- token generation + validation ----------------------------------------

func TestWidgetToken_MintAndValidate(t *testing.T) {
	secret := []byte(wtSecret)
	token, exp, err := mintWidgetToken(secret, "tok-1", "acme/repo-a", 30*24*time.Hour, wtNow)
	if err != nil {
		t.Fatalf("mintWidgetToken: %v", err)
	}
	if !strings.HasPrefix(token, widgetTokenPrefix) {
		t.Errorf("token missing %q prefix: %q", widgetTokenPrefix, token)
	}
	if exp != wtNow.Add(30*24*time.Hour).Unix() {
		t.Errorf("exp = %d, want %d", exp, wtNow.Add(30*24*time.Hour).Unix())
	}

	payload, err := validateWidgetToken(secret, token, wtNow)
	if err != nil {
		t.Fatalf("validateWidgetToken: %v", err)
	}
	if payload.ID != "tok-1" {
		t.Errorf("id = %q, want tok-1", payload.ID)
	}
	if payload.Repo != "acme/repo-a" {
		t.Errorf("repo = %q, want acme/repo-a", payload.Repo)
	}
}

func TestWidgetToken_WrongSecretRejected(t *testing.T) {
	token, _, err := mintWidgetToken([]byte(wtSecret), "tok-1", "acme/repo-a", time.Hour, wtNow)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if _, err := validateWidgetToken([]byte("a-different-secret"), token, wtNow); err == nil {
		t.Error("a token signed with another secret must not validate")
	}
}

func TestWidgetToken_TamperedPayloadRejected(t *testing.T) {
	token, _, err := mintWidgetToken([]byte(wtSecret), "tok-1", "acme/repo-a", time.Hour, wtNow)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	// Flip a byte in the payload segment; the HMAC must no longer match.
	body := strings.TrimPrefix(token, widgetTokenPrefix)
	parts := strings.SplitN(body, ".", 2)
	tampered := widgetTokenPrefix + flipFirstByte(parts[0]) + "." + parts[1]
	if _, err := validateWidgetToken([]byte(wtSecret), tampered, wtNow); err == nil {
		t.Error("a tampered payload must not validate")
	}
}

func flipFirstByte(s string) string {
	if s == "" {
		return s
	}
	b := []byte(s)
	if b[0] == 'A' {
		b[0] = 'B'
	} else {
		b[0] = 'A'
	}
	return string(b)
}

func TestWidgetToken_MalformedRejected(t *testing.T) {
	for _, tok := range []string{"wt_", "wt_onlyonepart", "not-a-widget-token", "wt_a.b.c"} {
		if _, err := validateWidgetToken([]byte(wtSecret), tok, wtNow); err == nil {
			t.Errorf("malformed token %q must not validate", tok)
		}
	}
}

func TestWidgetToken_EmptySecretRefuses(t *testing.T) {
	if _, _, err := mintWidgetToken(nil, "tok", "acme/repo", time.Hour, wtNow); err == nil {
		t.Error("minting with an empty secret must error")
	}
	token, _, _ := mintWidgetToken([]byte(wtSecret), "tok", "acme/repo", time.Hour, wtNow)
	if _, err := validateWidgetToken(nil, token, wtNow); err == nil {
		t.Error("validating with an empty secret must error")
	}
}

// --- expiry ----------------------------------------------------------------

func TestWidgetToken_Expiry(t *testing.T) {
	secret := []byte(wtSecret)
	token, _, err := mintWidgetToken(secret, "tok-1", "acme/repo-a", time.Hour, wtNow)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	// Valid just before expiry.
	if _, err := validateWidgetToken(secret, token, wtNow.Add(59*time.Minute)); err != nil {
		t.Errorf("token should still be valid before expiry: %v", err)
	}
	// Expired one second past.
	if _, err := validateWidgetToken(secret, token, wtNow.Add(time.Hour+time.Second)); err == nil {
		t.Error("expired token must not validate")
	}
}

// --- read-only tool allowlist ----------------------------------------------

func TestWidgetToken_ReadOnlyAllowlist(t *testing.T) {
	allowed := []string{"list_tasks", "get_task", "list_activity"}
	for _, tool := range allowed {
		if !widgetToolAllowed(tool) {
			t.Errorf("tool %q should be allowed for a widget token", tool)
		}
	}
	denied := []string{"create_task", "update_task", "update_task_status", "update_task_priority", "delete_task", "restore_task", "link_tasks", "unlink_tasks", "create_widget_token", "revoke_widget_token"}
	for _, tool := range denied {
		if widgetToolAllowed(tool) {
			t.Errorf("tool %q must NOT be allowed for a widget token", tool)
		}
	}
}

func TestWidgetTenant_IsReadOnlyAndScoped(t *testing.T) {
	payload := &widgetTokenPayload{ID: "tok-1", Repo: "acme/repo-a", Exp: wtNow.Add(time.Hour).Unix()}
	tn := widgetTenant(payload)
	if !tn.ReadOnly {
		t.Error("widget tenant must be read-only")
	}
	if tn.PartitionPrefix != "REPO#acme/repo-a#" {
		t.Errorf("prefix = %q, want REPO#acme/repo-a#", tn.PartitionPrefix)
	}
	if len(tn.AuthorizedRepos) != 1 || tn.AuthorizedRepos[0] != "acme/repo-a" {
		t.Errorf("authorized repos = %v, want [acme/repo-a]", tn.AuthorizedRepos)
	}
}

// --- read-only enforcement in write handlers -------------------------------

func TestReadOnlyToolDenied_BlocksWrites(t *testing.T) {
	roCtx := withTenant(context.Background(), &auth.Tenant{
		Repo: "acme/repo-a", PartitionPrefix: "REPO#acme/repo-a#", ReadOnly: true,
	})
	if readOnlyToolDenied(roCtx) == nil {
		t.Error("read-only tenant must be denied a write")
	}

	rwCtx := withTenant(context.Background(), &auth.Tenant{
		Repo: "acme/repo-a", PartitionPrefix: "REPO#acme/repo-a#", ReadOnly: false,
	})
	if readOnlyToolDenied(rwCtx) != nil {
		t.Error("a full-access tenant must be allowed a write")
	}

	// Local (no tenant) path is not read-only.
	if readOnlyToolDenied(context.Background()) != nil {
		t.Error("local no-tenant path must not be treated as read-only")
	}
}

func TestHandleCreateTask_ReadOnlyDenied(t *testing.T) {
	s := NewStateless()
	roCtx := withTenant(context.Background(), &auth.Tenant{
		Repo: "acme/repo-a", PartitionPrefix: "REPO#acme/repo-a#", ReadOnly: true,
	})
	res, _, err := s.handleCreateTask(roCtx, nil, CreateTaskArgs{Title: "nope"})
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if res == nil || !res.IsError {
		t.Fatal("create_task with a read-only tenant must return an isError result")
	}
}

// --- middleware: widget-token path end to end ------------------------------

// wtDeps builds widgetAuthDeps backed by an in-memory token store seeded with
// the given metadata, for middleware tests that must not touch env/AWS.
func wtDeps(t *testing.T, seed ...store.WidgetTokenMeta) (widgetAuthDeps, *memStore) {
	t.Helper()
	ms := &memStore{byPrefix: map[string]map[string]store.WidgetTokenMeta{}}
	for _, m := range seed {
		ms.put(auth.PartitionPrefixFor(m.Repo), m)
	}
	deps := widgetAuthDeps{
		secret: []byte(wtSecret),
		now:    func() time.Time { return wtNow },
		openStore: func(prefix string) (store.WidgetTokenStore, func() error, error) {
			return ms.scoped(prefix), func() error { return nil }, nil
		},
	}
	return deps, ms
}

func rpcBody(method, tool string) []byte {
	b, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  method,
		"params":  map[string]any{"name": tool, "arguments": map[string]any{}},
	})
	return b
}

func widgetRequest(token string, body []byte) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "https://cainban.test/mcp", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	return req
}

func TestMiddleware_WidgetToken_AllowsReadTool(t *testing.T) {
	token, exp, _ := mintWidgetToken([]byte(wtSecret), "tok-1", "acme/repo-a", time.Hour, wtNow)
	deps, _ := wtDeps(t, store.WidgetTokenMeta{TokenID: "tok-1", Repo: "acme/repo-a", ExpiresAt: time.Unix(exp, 0)})

	spy := &spyHandler{}
	m := newMWSigner(t)
	h := authMiddlewareWithWidgetDeps(m.resolver(t), "", deps, spy)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, widgetRequest(token, rpcBody("tools/call", "list_tasks")))

	if !spy.ran {
		t.Fatal("a read tool must reach the handler")
	}
	if spy.tenant == nil || !spy.tenant.ReadOnly {
		t.Fatalf("handler must see a read-only tenant, got %+v", spy.tenant)
	}
	if spy.tenant.PartitionPrefix != "REPO#acme/repo-a#" {
		t.Errorf("prefix = %q, want REPO#acme/repo-a#", spy.tenant.PartitionPrefix)
	}
}

func TestMiddleware_WidgetToken_BlocksWriteTool(t *testing.T) {
	token, exp, _ := mintWidgetToken([]byte(wtSecret), "tok-1", "acme/repo-a", time.Hour, wtNow)
	deps, _ := wtDeps(t, store.WidgetTokenMeta{TokenID: "tok-1", Repo: "acme/repo-a", ExpiresAt: time.Unix(exp, 0)})

	spy := &spyHandler{}
	m := newMWSigner(t)
	h := authMiddlewareWithWidgetDeps(m.resolver(t), "", deps, spy)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, widgetRequest(token, rpcBody("tools/call", "create_task")))

	if spy.ran {
		t.Error("a write tool must NOT reach the handler on a widget token")
	}
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestMiddleware_WidgetToken_HandshakeAllowed(t *testing.T) {
	token, exp, _ := mintWidgetToken([]byte(wtSecret), "tok-1", "acme/repo-a", time.Hour, wtNow)
	deps, _ := wtDeps(t, store.WidgetTokenMeta{TokenID: "tok-1", Repo: "acme/repo-a", ExpiresAt: time.Unix(exp, 0)})

	spy := &spyHandler{}
	m := newMWSigner(t)
	h := authMiddlewareWithWidgetDeps(m.resolver(t), "", deps, spy)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, widgetRequest(token, rpcBody("tools/list", "")))

	if !spy.ran {
		t.Error("the handshake/list method must reach the handler for a widget token")
	}
}

func TestMiddleware_WidgetToken_RevokedRejected(t *testing.T) {
	token, exp, _ := mintWidgetToken([]byte(wtSecret), "tok-1", "acme/repo-a", time.Hour, wtNow)
	deps, _ := wtDeps(t, store.WidgetTokenMeta{TokenID: "tok-1", Repo: "acme/repo-a", ExpiresAt: time.Unix(exp, 0), Revoked: true})

	spy := &spyHandler{}
	m := newMWSigner(t)
	h := authMiddlewareWithWidgetDeps(m.resolver(t), "", deps, spy)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, widgetRequest(token, rpcBody("tools/call", "list_tasks")))

	if spy.ran {
		t.Error("a revoked widget token must NOT reach the handler")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestMiddleware_WidgetToken_ExpiredRejected(t *testing.T) {
	token, exp, _ := mintWidgetToken([]byte(wtSecret), "tok-1", "acme/repo-a", time.Hour, wtNow)
	// Clock is well past expiry.
	deps, _ := wtDeps(t, store.WidgetTokenMeta{TokenID: "tok-1", Repo: "acme/repo-a", ExpiresAt: time.Unix(exp, 0)})
	deps.now = func() time.Time { return wtNow.Add(2 * time.Hour) }

	spy := &spyHandler{}
	m := newMWSigner(t)
	h := authMiddlewareWithWidgetDeps(m.resolver(t), "", deps, spy)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, widgetRequest(token, rpcBody("tools/call", "list_tasks")))

	if spy.ran {
		t.Error("an expired widget token must NOT reach the handler")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestMiddleware_UserJWT_Unaffected(t *testing.T) {
	// A normal (non-wt_) JWT still flows through the resolver path and reaches
	// the handler with a full-access tenant.
	deps, _ := wtDeps(t)
	m := newMWSigner(t)
	spy := &spyHandler{}
	h := authMiddlewareWithWidgetDeps(m.resolver(t), "", deps, spy)

	tok := m.token(t, []string{"acme/repo-a"})
	req := httptest.NewRequest(http.MethodPost, "https://cainban.test/mcp", bytes.NewReader(rpcBody("tools/call", "create_task")))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set(auth.HeaderTargetRepo, "acme/repo-a")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if !spy.ran {
		t.Fatal("a valid user JWT must reach the handler")
	}
	if spy.tenant == nil || spy.tenant.ReadOnly {
		t.Errorf("user JWT tenant must be full-access (ReadOnly=false), got %+v", spy.tenant)
	}
}

// --- tiny in-memory token store for middleware tests -----------------------

type memStore struct {
	byPrefix map[string]map[string]store.WidgetTokenMeta
}

func (m *memStore) put(prefix string, meta store.WidgetTokenMeta) {
	b, ok := m.byPrefix[prefix]
	if !ok {
		b = map[string]store.WidgetTokenMeta{}
		m.byPrefix[prefix] = b
	}
	b[meta.TokenID] = meta
}

func (m *memStore) scoped(prefix string) store.WidgetTokenStore {
	return &memScope{parent: m, prefix: prefix}
}

type memScope struct {
	parent *memStore
	prefix string
}

func (m *memScope) bucket() map[string]store.WidgetTokenMeta {
	b, ok := m.parent.byPrefix[m.prefix]
	if !ok {
		b = map[string]store.WidgetTokenMeta{}
		m.parent.byPrefix[m.prefix] = b
	}
	return b
}

func (m *memScope) PutWidgetToken(_ context.Context, meta store.WidgetTokenMeta) error {
	m.bucket()[meta.TokenID] = meta
	return nil
}

func (m *memScope) GetWidgetToken(_ context.Context, id string) (*store.WidgetTokenMeta, error) {
	meta, ok := m.bucket()[id]
	if !ok {
		return nil, nil
	}
	return &meta, nil
}

func (m *memScope) ListWidgetTokens(_ context.Context) ([]store.WidgetTokenMeta, error) {
	b := m.bucket()
	out := make([]store.WidgetTokenMeta, 0, len(b))
	for _, meta := range b {
		out = append(out, meta)
	}
	return out, nil
}

func (m *memScope) RevokeWidgetToken(_ context.Context, id string) (bool, error) {
	b := m.bucket()
	meta, ok := b[id]
	if !ok {
		return false, nil
	}
	meta.Revoked = true
	b[id] = meta
	return true, nil
}
