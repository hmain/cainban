package main

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/aws/aws-lambda-go/events"

	"github.com/hmain/cainban/src/systems/auth"
)

// --- fakes -----------------------------------------------------------------

type fakeConns struct {
	added         map[string]string // connectionId -> repo
	authenticated map[string]string // connectionId -> repo
	authSubs      map[string]string // connectionId -> sub
	removed       map[string]bool
	addErr        error
	authErr       error
}

func newFakeConns() *fakeConns {
	return &fakeConns{
		added:         map[string]string{},
		authenticated: map[string]string{},
		authSubs:      map[string]string{},
		removed:       map[string]bool{},
	}
}

func (f *fakeConns) Add(_ context.Context, id, repo string) error {
	if f.addErr != nil {
		return f.addErr
	}
	f.added[id] = repo
	return nil
}
func (f *fakeConns) Authenticate(_ context.Context, id, repo, sub string) error {
	if f.authErr != nil {
		return f.authErr
	}
	f.authenticated[id] = repo
	f.authSubs[id] = sub
	return nil
}
func (f *fakeConns) Remove(_ context.Context, id string) error {
	f.removed[id] = true
	return nil
}

type fakeSender struct {
	frames map[string][]interface{}
}

func newFakeSender() *fakeSender { return &fakeSender{frames: map[string][]interface{}{}} }
func (f *fakeSender) Send(_ context.Context, id string, frame interface{}) error {
	f.frames[id] = append(f.frames[id], frame)
	return nil
}

// fakeResolver authorizes a fixed repo/sub for a known-good token and rejects
// everything else, mirroring auth.Resolver's contract without RSA signing.
type fakeResolver struct {
	goodToken string
	tenant    *auth.Tenant
}

func (f *fakeResolver) Resolve(req *http.Request, _ string) (*auth.Tenant, error) {
	got := req.Header.Get("Authorization")
	if got != "Bearer "+f.goodToken {
		return nil, errors.New("401")
	}
	return f.tenant, nil
}

func newHandler(c *fakeConns, s *fakeSender, r tokenResolver) *handler {
	return &handler{conns: c, sender: s, resolver: r}
}

func wsEvent(routeKey, connID, body string, query map[string]string) events.APIGatewayWebsocketProxyRequest {
	return events.APIGatewayWebsocketProxyRequest{
		QueryStringParameters: query,
		Body:                  body,
		RequestContext: events.APIGatewayWebsocketProxyRequestContext{
			RouteKey:     routeKey,
			ConnectionID: connID,
		},
	}
}

// --- $connect --------------------------------------------------------------

func TestConnectStoresRepoFromQueryParams(t *testing.T) {
	c, s := newFakeConns(), newFakeSender()
	h := newHandler(c, s, &fakeResolver{})
	resp, err := h.handle(context.Background(), wsEvent(routeConnect, "C1", "", map[string]string{"owner": "acme", "repo": "repo-a"}))
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if c.added["C1"] != "acme/repo-a" {
		t.Errorf("stored repo = %q, want acme/repo-a", c.added["C1"])
	}
}

func TestConnectRejectsMissingRepo(t *testing.T) {
	c, s := newFakeConns(), newFakeSender()
	h := newHandler(c, s, &fakeResolver{})
	resp, _ := h.handle(context.Background(), wsEvent(routeConnect, "C1", "", nil))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (reject handshake)", resp.StatusCode)
	}
	if _, ok := c.added["C1"]; ok {
		t.Error("must not store a connection with no repo")
	}
}

// --- $disconnect -----------------------------------------------------------

func TestDisconnectRemovesConnection(t *testing.T) {
	c, s := newFakeConns(), newFakeSender()
	h := newHandler(c, s, &fakeResolver{})
	resp, err := h.handle(context.Background(), wsEvent(routeDisconnect, "C1", "", nil))
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("disconnect resp=%d err=%v", resp.StatusCode, err)
	}
	if !c.removed["C1"] {
		t.Error("connection not removed on $disconnect")
	}
}

// --- auth ------------------------------------------------------------------

func TestAuthValidTokenMarksAuthenticated(t *testing.T) {
	c, s := newFakeConns(), newFakeSender()
	r := &fakeResolver{
		goodToken: "good",
		tenant:    &auth.Tenant{Repo: "acme/repo-a", Subject: "sub-123"},
	}
	h := newHandler(c, s, r)
	body := `{"action":"auth","token":"good","repo":"acme/repo-a"}`
	resp, err := h.handle(context.Background(), wsEvent(routeAuth, "C1", body, nil))
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("auth resp=%d err=%v", resp.StatusCode, err)
	}
	if c.authenticated["C1"] != "acme/repo-a" || c.authSubs["C1"] != "sub-123" {
		t.Errorf("authenticated=%q sub=%q, want acme/repo-a sub-123", c.authenticated["C1"], c.authSubs["C1"])
	}
}

func TestAuthBadTokenDoesNotAuthenticate(t *testing.T) {
	c, s := newFakeConns(), newFakeSender()
	r := &fakeResolver{goodToken: "good", tenant: &auth.Tenant{Repo: "acme/repo-a", Subject: "x"}}
	h := newHandler(c, s, r)
	body := `{"action":"auth","token":"WRONG","repo":"acme/repo-a"}`
	h.handle(context.Background(), wsEvent(routeAuth, "C1", body, nil))
	if _, ok := c.authenticated["C1"]; ok {
		t.Error("a bad token must NOT authenticate the connection")
	}
	// An error frame should have been sent back.
	if len(s.frames["C1"]) == 0 {
		t.Error("expected an error frame on failed auth")
	}
}

func TestAuthRejectsUnscopedTenant(t *testing.T) {
	c, s := newFakeConns(), newFakeSender()
	// Unscoped tenant => token authenticated but grants no repo; must be rejected.
	r := &fakeResolver{goodToken: "good", tenant: &auth.Tenant{Unscoped: true}}
	h := newHandler(c, s, r)
	body := `{"action":"auth","token":"good","repo":"acme/repo-a"}`
	h.handle(context.Background(), wsEvent(routeAuth, "C1", body, nil))
	if _, ok := c.authenticated["C1"]; ok {
		t.Error("an unscoped tenant must not authenticate a repo-scoped socket")
	}
}

func TestAuthMissingRepoRejected(t *testing.T) {
	c, s := newFakeConns(), newFakeSender()
	r := &fakeResolver{goodToken: "good", tenant: &auth.Tenant{Repo: "acme/repo-a"}}
	h := newHandler(c, s, r)
	body := `{"action":"auth","token":"good"}` // no repo
	h.handle(context.Background(), wsEvent(routeAuth, "C1", body, nil))
	if _, ok := c.authenticated["C1"]; ok {
		t.Error("auth frame with no repo must be rejected")
	}
}

func TestAuthMalformedBodyRejected(t *testing.T) {
	c, s := newFakeConns(), newFakeSender()
	h := newHandler(c, s, &fakeResolver{})
	h.handle(context.Background(), wsEvent(routeAuth, "C1", "{not json", nil))
	if _, ok := c.authenticated["C1"]; ok {
		t.Error("malformed auth frame must be rejected")
	}
}

// --- $default (ping/pong) --------------------------------------------------

func TestDefaultAnswersPingWithPong(t *testing.T) {
	c, s := newFakeConns(), newFakeSender()
	h := newHandler(c, s, &fakeResolver{})
	h.handle(context.Background(), wsEvent(routeDefault, "C1", `{"type":"ping"}`, nil))
	if len(s.frames["C1"]) != 1 {
		t.Fatalf("expected 1 pong frame, got %d", len(s.frames["C1"]))
	}
	frame, ok := s.frames["C1"][0].(map[string]any)
	if !ok || frame["type"] != "pong" {
		t.Errorf("frame = %+v, want {type:pong}", s.frames["C1"][0])
	}
}

func TestDefaultIgnoresNonPing(t *testing.T) {
	c, s := newFakeConns(), newFakeSender()
	h := newHandler(c, s, &fakeResolver{})
	h.handle(context.Background(), wsEvent(routeDefault, "C1", `{"type":"whatever"}`, nil))
	if len(s.frames["C1"]) != 0 {
		t.Errorf("non-ping must not produce a frame, got %d", len(s.frames["C1"]))
	}
}

// --- connect path parsing --------------------------------------------------

func TestRepoFromConnectVariants(t *testing.T) {
	cases := []struct {
		name  string
		query map[string]string
		path  string
		want  string
		ok    bool
	}{
		{"owner+repo params", map[string]string{"owner": "acme", "repo": "repo-a"}, "", "acme/repo-a", true},
		{"single repo param", map[string]string{"repo": "acme/repo-a"}, "", "acme/repo-a", true},
		{"path suffix", nil, "/prod/acme/repo-a", "acme/repo-a", true},
		{"nothing", nil, "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := repoFromConnect(events.APIGatewayWebsocketProxyRequest{
				QueryStringParameters: tc.query,
				Path:                  tc.path,
			})
			if tc.ok && (err != nil || got != tc.want) {
				t.Fatalf("got %q err=%v, want %q", got, err, tc.want)
			}
			if !tc.ok && err == nil {
				t.Fatalf("expected error, got %q", got)
			}
		})
	}
}
