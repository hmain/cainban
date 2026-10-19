package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/hmain/cainban/src/systems/auth"
	"github.com/hmain/cainban/src/systems/task"
)

// These tests pin the Phase 4 WebSocket contract the frontend adapter
// (web/src/board/wsAdapter.ts) depends on: path parsing, hub fan-out with
// strict per-repo isolation, token-first authorization on connect, the initial
// snapshot, live event push, and ping/pong heartbeat.

func TestWSRepoFromPath(t *testing.T) {
	cases := []struct {
		path    string
		want    string
		wantErr bool
	}{
		{"/ws/acme/repo-a", "acme/repo-a", false},
		{"/ws/acme/repo-a/", "acme/repo-a", false},
		{"/ws/octo/Hello-World", "octo/Hello-World", false},
		{"/ws/acme", "", true},            // missing repo
		{"/ws/acme/repo/extra", "", true}, // too many segments
		{"/ws/", "", true},                // empty
		{"/mcp", "", true},                // not a /ws/ path
		{"/ws/acme/..", "", true},         // path traversal rejected by NormalizeRepo
	}
	for _, c := range cases {
		got, err := wsRepoFromPath(c.path)
		if c.wantErr {
			if err == nil {
				t.Errorf("wsRepoFromPath(%q) = %q, want error", c.path, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("wsRepoFromPath(%q) unexpected error: %v", c.path, err)
			continue
		}
		if got != c.want {
			t.Errorf("wsRepoFromPath(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}

func TestHubRegisterUnregisterPrune(t *testing.T) {
	h := NewHub()
	c1 := &wsClient{repoKey: "acme/repo-a", send: make(chan wsMessage, wsSendBuffer)}
	c2 := &wsClient{repoKey: "acme/repo-a", send: make(chan wsMessage, wsSendBuffer)}
	c3 := &wsClient{repoKey: "acme/repo-b", send: make(chan wsMessage, wsSendBuffer)}

	h.register(c1)
	h.register(c2)
	h.register(c3)

	if got := h.clientCount("acme/repo-a"); got != 2 {
		t.Errorf("repo-a count = %d, want 2", got)
	}
	if got := h.clientCount("acme/repo-b"); got != 1 {
		t.Errorf("repo-b count = %d, want 1", got)
	}

	h.unregister(c1)
	if got := h.clientCount("acme/repo-a"); got != 1 {
		t.Errorf("after unregister repo-a count = %d, want 1", got)
	}

	// Removing the last client must prune the bucket entirely (no leak).
	h.unregister(c2)
	if got := h.clientCount("acme/repo-a"); got != 0 {
		t.Errorf("after prune repo-a count = %d, want 0", got)
	}
	h.mu.RLock()
	_, present := h.clients["acme/repo-a"]
	h.mu.RUnlock()
	if present {
		t.Error("empty repo bucket must be pruned from the hub map")
	}
}

func TestHubBroadcastIsolation(t *testing.T) {
	h := NewHub()
	a := &wsClient{repoKey: "acme/repo-a", send: make(chan wsMessage, wsSendBuffer)}
	b := &wsClient{repoKey: "acme/repo-b", send: make(chan wsMessage, wsSendBuffer)}
	h.register(a)
	h.register(b)

	ev := task.ActivityEvent{BoardTaskID: 7, Action: task.ActivityCreated, Detail: "hello", Timestamp: time.Unix(0, 0).UTC()}
	h.Broadcast("acme/repo-a", ev)

	select {
	case msg := <-a.send:
		if msg.Type != "event" || msg.Event == nil || msg.Event.BoardTaskID != 7 {
			t.Errorf("repo-a got unexpected message: %+v", msg)
		}
	default:
		t.Error("repo-a client should have received the broadcast")
	}

	select {
	case msg := <-b.send:
		t.Errorf("repo-b client must NOT receive repo-a's event, got %+v", msg)
	default:
		// correct: isolated
	}
}

func TestHubBroadcastDropsSlowClient(t *testing.T) {
	h := NewHub()
	// Buffer of 1, pre-filled, so the next send cannot enqueue.
	slow := &wsClient{repoKey: "r/r", send: make(chan wsMessage, 1)}
	slow.send <- wsMessage{Type: "snapshot"}
	h.register(slow)

	// Must not block or panic; the frame is simply dropped.
	done := make(chan struct{})
	go func() {
		h.Broadcast("r/r", task.ActivityEvent{BoardTaskID: 1})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Broadcast blocked on a full client buffer")
	}
}

// fakeSnapshot is a snapshotProvider that returns a fixed snapshot.
type fakeSnapshot struct {
	tasks  []wsTask
	cursor string
}

func (f fakeSnapshot) wsSnapshot(_ context.Context, _ *auth.Tenant) ([]wsTask, string, error) {
	return f.tasks, f.cursor, nil
}

// dialWS connects a test client to the handler's /ws/{repo} endpoint.
func dialWS(t *testing.T, serverURL, repo string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(serverURL, "http") + "/ws/" + repo
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return websocket.Dial(ctx, wsURL, nil)
}

func readMsg(t *testing.T, conn *websocket.Conn) wsMessage {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var m wsMessage
	if err := readWSMessage(ctx, conn, &m); err != nil {
		t.Fatalf("read: %v", err)
	}
	return m
}

func writeMsg(t *testing.T, conn *websocket.Conn, m wsMessage) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := writeWSMessage(ctx, conn, m); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestWebSocketHandler_AuthSnapshotAndBroadcast(t *testing.T) {
	m := newMWSigner(t)
	hub := NewHub()
	snap := fakeSnapshot{
		tasks:  []wsTask{{BoardTaskID: 1, Title: "T1", Status: "todo", Priority: 0}},
		cursor: "2026-09-25T12:00:00Z",
	}
	srv := httptest.NewServer(hub.WebSocketHandler(m.resolver(t), snap))
	defer srv.Close()

	conn, _, err := dialWS(t, srv.URL, "acme/repo-a")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()

	// Authenticate with a token that grants the target repo.
	tok := m.token(t, []string{"acme/repo-a"})
	writeMsg(t, conn, wsMessage{Type: "auth", Token: tok})

	// First server frame is the initial snapshot.
	got := readMsg(t, conn)
	if got.Type != "snapshot" {
		t.Fatalf("first frame type = %q, want snapshot", got.Type)
	}
	if len(got.Tasks) != 1 || got.Tasks[0].Title != "T1" {
		t.Fatalf("snapshot tasks = %+v, want one task T1", got.Tasks)
	}
	if got.Cursor != snap.cursor {
		t.Errorf("snapshot cursor = %q, want %q", got.Cursor, snap.cursor)
	}

	// Wait until the client is registered, then broadcast a live event.
	deadline := time.Now().Add(2 * time.Second)
	for hub.clientCount("acme/repo-a") == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if hub.clientCount("acme/repo-a") != 1 {
		t.Fatalf("client not registered with hub")
	}

	hub.Broadcast("acme/repo-a", task.ActivityEvent{
		BoardTaskID: 2,
		Action:      task.ActivityStatusChanged,
		Detail:      "todo -> doing",
		Timestamp:   time.Date(2026, 9, 25, 12, 1, 0, 0, time.UTC),
	})

	ev := readMsg(t, conn)
	if ev.Type != "event" || ev.Event == nil {
		t.Fatalf("expected event frame, got %+v", ev)
	}
	if ev.Event.BoardTaskID != 2 || ev.Event.Action != "status_changed" || ev.Event.Detail != "todo -> doing" {
		t.Errorf("event = %+v, want BoardTaskID 2 status_changed todo->doing", ev.Event)
	}
}

func TestWebSocketHandler_PingPong(t *testing.T) {
	m := newMWSigner(t)
	hub := NewHub()
	srv := httptest.NewServer(hub.WebSocketHandler(m.resolver(t), fakeSnapshot{}))
	defer srv.Close()

	conn, _, err := dialWS(t, srv.URL, "acme/repo-a")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()

	writeMsg(t, conn, wsMessage{Type: "auth", Token: m.token(t, []string{"acme/repo-a"})})
	// Snapshot frame first (empty snapshot still sends an envelope).
	_ = readMsg(t, conn)

	// A client-initiated ping must be answered with a pong.
	writeMsg(t, conn, wsMessage{Type: "ping"})
	got := readMsg(t, conn)
	if got.Type != "pong" {
		t.Errorf("response to client ping = %q, want pong", got.Type)
	}
}

func TestWebSocketHandler_UnauthorizedRepoClosed(t *testing.T) {
	m := newMWSigner(t)
	hub := NewHub()
	srv := httptest.NewServer(hub.WebSocketHandler(m.resolver(t), fakeSnapshot{}))
	defer srv.Close()

	conn, _, err := dialWS(t, srv.URL, "acme/repo-b")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close(websocket.StatusPolicyViolation, "") }()

	// Token grants repo-a only; we connected to repo-b.
	writeMsg(t, conn, wsMessage{Type: "auth", Token: m.token(t, []string{"acme/repo-a"})})

	// Server sends an error frame then closes with a policy violation.
	got := readMsg(t, conn)
	if got.Type != "error" {
		t.Fatalf("expected error frame for unauthorized repo, got %+v", got)
	}

	// The connection must be closed and never join the hub.
	if hub.clientCount("acme/repo-b") != 0 {
		t.Errorf("unauthorized client must not register with the hub")
	}
}

func TestWebSocketHandler_InvalidTokenClosed(t *testing.T) {
	m := newMWSigner(t)
	hub := NewHub()
	srv := httptest.NewServer(hub.WebSocketHandler(m.resolver(t), fakeSnapshot{}))
	defer srv.Close()

	conn, _, err := dialWS(t, srv.URL, "acme/repo-a")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close(websocket.StatusPolicyViolation, "") }()

	writeMsg(t, conn, wsMessage{Type: "auth", Token: "not.a.jwt"})

	got := readMsg(t, conn)
	if got.Type != "error" {
		t.Fatalf("expected error frame for invalid token, got %+v", got)
	}
	if hub.clientCount("acme/repo-a") != 0 {
		t.Errorf("invalid-token client must not register with the hub")
	}
}

func TestWebSocketHandler_FirstFrameMustBeAuth(t *testing.T) {
	m := newMWSigner(t)
	hub := NewHub()
	srv := httptest.NewServer(hub.WebSocketHandler(m.resolver(t), fakeSnapshot{}))
	defer srv.Close()

	conn, _, err := dialWS(t, srv.URL, "acme/repo-a")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close(websocket.StatusPolicyViolation, "") }()

	// Send a non-auth frame first.
	writeMsg(t, conn, wsMessage{Type: "pong"})

	got := readMsg(t, conn)
	if got.Type != "error" {
		t.Fatalf("expected error frame when first frame is not auth, got %+v", got)
	}
}
