package main

import (
	"context"
	"net/http"
	"testing"

	"github.com/aws/aws-lambda-go/events"

	"github.com/hmain/cainban/src/systems/auth"
	"github.com/hmain/cainban/src/systems/store"
)

// fakeResolver resolves a fixed tenant for an authorized (token, repo) pair and
// errors otherwise, standing in for the signature-first auth.Resolver.
type fakeResolver struct {
	okToken string
	okRepo  string
	tenant  *auth.Tenant
}

func (f *fakeResolver) Resolve(req *http.Request, argRepo string) (*auth.Tenant, error) {
	if req.Header.Get("Authorization") != "Bearer "+f.okToken || argRepo != f.okRepo {
		return nil, errUnauthorized
	}
	return f.tenant, nil
}

type stringError string

func (e stringError) Error() string { return string(e) }

const errUnauthorized = stringError("unauthorized")

// fakeStore is an in-memory WSConnectionStore keyed by prefix, shared across
// opens so a $connect then a $disconnect are observable in one test.
type fakeStore struct {
	byPrefix map[string]map[string]string // prefix -> {connId: repo}
	lookup   map[string]string            // connId -> prefix
}

func newFakeStore() *fakeStore {
	return &fakeStore{byPrefix: map[string]map[string]string{}, lookup: map[string]string{}}
}

func (f *fakeStore) opener() connStoreOpener {
	return func(_ context.Context, prefix string) (store.WSConnectionStore, func() error, error) {
		return &fakeStoreScope{parent: f, prefix: prefix}, func() error { return nil }, nil
	}
}

type fakeStoreScope struct {
	parent *fakeStore
	prefix string
}

func (s *fakeStoreScope) PutWSConnection(_ context.Context, id, repo string) error {
	if s.parent.byPrefix[s.prefix] == nil {
		s.parent.byPrefix[s.prefix] = map[string]string{}
	}
	s.parent.byPrefix[s.prefix][id] = repo
	s.parent.lookup[id] = s.prefix
	return nil
}

func (s *fakeStoreScope) ListWSConnectionIDs(_ context.Context) ([]string, error) {
	out := []string{}
	for id := range s.parent.byPrefix[s.prefix] {
		out = append(out, id)
	}
	return out, nil
}

func (s *fakeStoreScope) DeleteWSConnection(_ context.Context, id string) error {
	prefix, ok := s.parent.lookup[id]
	delete(s.parent.lookup, id)
	if ok {
		if b := s.parent.byPrefix[prefix]; b != nil {
			delete(b, id)
		}
	}
	return nil
}

func (s *fakeStoreScope) DeleteWSConnectionInScope(_ context.Context, id string) error {
	delete(s.parent.byPrefix[s.prefix], id)
	delete(s.parent.lookup, id)
	return nil
}

func wsEvent(route, connID string, qs map[string]string, body string) events.APIGatewayWebsocketProxyRequest {
	req := events.APIGatewayWebsocketProxyRequest{Body: body, QueryStringParameters: qs}
	req.RequestContext.RouteKey = route
	req.RequestContext.ConnectionID = connID
	return req
}

func newTestHandler(r tenantResolver, st *fakeStore) *handler {
	return &handler{resolver: r, open: st.opener()}
}

func TestConnect_AuthorizedRegisters(t *testing.T) {
	st := newFakeStore()
	r := &fakeResolver{
		okToken: "good",
		okRepo:  "acme/widgets",
		tenant:  &auth.Tenant{PartitionPrefix: "REPO#acme/widgets#"},
	}
	h := newTestHandler(r, st)

	resp := h.handle(context.Background(), wsEvent(routeConnect, "c1",
		map[string]string{"token": "good", "repo": "acme/widgets"}, ""))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("connect want 200, got %d (%s)", resp.StatusCode, resp.Body)
	}
	if st.byPrefix["REPO#acme/widgets#"]["c1"] != "acme/widgets" {
		t.Fatalf("connection not registered under repo prefix: %+v", st.byPrefix)
	}
}

func TestConnect_RejectsBadToken(t *testing.T) {
	st := newFakeStore()
	r := &fakeResolver{okToken: "good", okRepo: "acme/widgets", tenant: &auth.Tenant{PartitionPrefix: "REPO#acme/widgets#"}}
	h := newTestHandler(r, st)

	resp := h.handle(context.Background(), wsEvent(routeConnect, "c1",
		map[string]string{"token": "bad", "repo": "acme/widgets"}, ""))
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("bad token want 403, got %d", resp.StatusCode)
	}
	if len(st.lookup) != 0 {
		t.Fatalf("no connection must be registered on auth failure, got %+v", st.lookup)
	}
}

func TestConnect_RejectsUnscopedTenant(t *testing.T) {
	st := newFakeStore()
	r := &fakeResolver{okToken: "good", okRepo: "acme/widgets", tenant: &auth.Tenant{Unscoped: true}}
	h := newTestHandler(r, st)

	resp := h.handle(context.Background(), wsEvent(routeConnect, "c1",
		map[string]string{"token": "good", "repo": "acme/widgets"}, ""))
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("unscoped tenant want 403, got %d", resp.StatusCode)
	}
}

func TestConnect_MissingParams(t *testing.T) {
	st := newFakeStore()
	h := newTestHandler(&fakeResolver{}, st)

	resp := h.handle(context.Background(), wsEvent(routeConnect, "c1", map[string]string{}, ""))
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("missing params want 401, got %d", resp.StatusCode)
	}
}

func TestConnect_InvalidRepo(t *testing.T) {
	st := newFakeStore()
	h := newTestHandler(&fakeResolver{}, st)

	resp := h.handle(context.Background(), wsEvent(routeConnect, "c1",
		map[string]string{"token": "good", "repo": "not-a-repo"}, ""))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid repo want 400, got %d", resp.StatusCode)
	}
}

func TestDisconnect_Deregisters(t *testing.T) {
	st := newFakeStore()
	r := &fakeResolver{okToken: "good", okRepo: "acme/widgets", tenant: &auth.Tenant{PartitionPrefix: "REPO#acme/widgets#"}}
	h := newTestHandler(r, st)

	// Connect, then disconnect.
	_ = h.handle(context.Background(), wsEvent(routeConnect, "c1",
		map[string]string{"token": "good", "repo": "acme/widgets"}, ""))
	resp := h.handle(context.Background(), wsEvent(routeDisconnect, "c1", nil, ""))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("disconnect want 200, got %d", resp.StatusCode)
	}
	if len(st.byPrefix["REPO#acme/widgets#"]) != 0 {
		t.Fatalf("connection must be removed on disconnect, got %+v", st.byPrefix)
	}
}

func TestDefault_AnswersPing(t *testing.T) {
	h := newTestHandler(&fakeResolver{}, newFakeStore())
	resp := h.handle(context.Background(), wsEvent(routeDefault, "c1", nil, `{"type":"ping"}`))
	if resp.StatusCode != http.StatusOK || resp.Body != `{"type":"pong"}` {
		t.Fatalf("ping want pong body, got %d %q", resp.StatusCode, resp.Body)
	}
}

func TestDefault_IgnoresOtherFrames(t *testing.T) {
	h := newTestHandler(&fakeResolver{}, newFakeStore())
	resp := h.handle(context.Background(), wsEvent(routeDefault, "c1", nil, `{"type":"pong"}`))
	if resp.StatusCode != http.StatusOK || resp.Body != "" {
		t.Fatalf("non-ping frame want empty 200, got %d %q", resp.StatusCode, resp.Body)
	}
}

// Compile-time assertion that the real resolver satisfies the interface the
// handler depends on (so production wiring in main.go type-checks).
var _ tenantResolver = (*auth.Resolver)(nil)
