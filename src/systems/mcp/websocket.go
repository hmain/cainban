package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/hmain/cainban/src/systems/auth"
	"github.com/hmain/cainban/src/systems/task"
)

// WebSocket real-time board updates (Phase 4).
//
// This file adds a WebSocket endpoint (/ws/{owner}/{repo}) that pushes
// task-activity events to connected browsers in real time, replacing the SPA's
// delta-polling of list_activity. The wire protocol is defined by the frontend
// adapter (web/src/board/wsAdapter.ts); keep the two in lock-step:
//
//   - Connect: wss://<host>/ws/<owner>/<repo>
//   - Auth:     client -> { "type": "auth", "token": "<jwt>" } (first frame)
//   - Snapshot: server -> { "type": "snapshot", "tasks": [...], "cursor": "..." }
//   - Event:    server -> { "type": "event", "event": <ActivityEvent> }
//   - Heartbeat: server -> { "type": "ping" }; client -> { "type": "pong" }
//   - Error:    server -> { "type": "error", "message": "..." }
//
// # Authorization (identical trust model to the MCP path)
//
// The token arrives in the first WS frame, not an Authorization header (the
// browser WebSocket API cannot set custom headers). It is validated
// SIGNATURE-FIRST through the SAME auth.Resolver the HTTP handler uses: we
// synthesize an *http.Request carrying the bearer token and the repo from the
// URL path, then call resolver.Resolve. A token that does not grant the target
// repo closes the socket with a policy error BEFORE the connection joins any
// hub or any store is read — tenant isolation is structural, exactly as on the
// MCP path.
//
// # Memory safety
//
// Every connection owns exactly two goroutines (a read pump and the caller's
// serve goroutine running the write loop). Both exit when the shared per-client
// context is canceled, which happens on client close, read/write error, failed
// heartbeat, or server shutdown — so there is no goroutine or connection leak.
// The hub holds only live clients and prunes on unregister.

const (
	// wsPingInterval is how often the server sends an application-level ping.
	// The frontend treats a 60s pong gap as a dead connection, so 30s leaves a
	// comfortable margin.
	wsPingInterval = 30 * time.Second

	// wsWriteTimeout bounds a single frame write so a stuck/slow client cannot
	// block the hub's broadcast or wedge a goroutine indefinitely.
	wsWriteTimeout = 10 * time.Second

	// wsSendBuffer is the per-client outbound queue depth. A client that falls
	// this far behind is dropped rather than allowed to back-pressure the hub.
	wsSendBuffer = 32

	// wsMaxMessageBytes caps an inbound frame (auth token / pong). Inbound
	// frames are tiny; this just bounds a hostile client.
	wsMaxMessageBytes = 64 * 1024
)

// wsMessage is the envelope exchanged in both directions. Only the fields
// relevant to a given type are populated; omitempty keeps the wire form minimal
// and matching the frontend's WSMessage shape.
type wsMessage struct {
	Type    string      `json:"type"`
	Token   string      `json:"token,omitempty"`
	Event   *wsActivity `json:"event,omitempty"`
	Tasks   []wsTask    `json:"tasks,omitempty"`
	Cursor  string      `json:"cursor,omitempty"`
	Message string      `json:"message,omitempty"`
}

// wsActivity is the serialized activity event pushed to clients. Field names
// match web/src/board/wsAdapter.ts WSActivityEvent exactly (PascalCase).
type wsActivity struct {
	BoardTaskID int    `json:"BoardTaskID"`
	Action      string `json:"Action"`
	Actor       string `json:"Actor"`
	Detail      string `json:"Detail"`
	Timestamp   string `json:"Timestamp"`
}

// wsTask is the serialized task in a snapshot. Field names match the frontend's
// WSTask shape (PascalCase).
type wsTask struct {
	BoardTaskID int    `json:"BoardTaskID"`
	Title       string `json:"Title"`
	Status      string `json:"Status"`
	Priority    int    `json:"Priority"`
	Description string `json:"Description,omitempty"`
}

// activityToWS converts an internal event to its wire form.
func activityToWS(ev task.ActivityEvent) wsActivity {
	return wsActivity{
		BoardTaskID: ev.BoardTaskID,
		Action:      string(ev.Action),
		Actor:       ev.Actor,
		Detail:      ev.Detail,
		Timestamp:   ev.Timestamp.Format(time.RFC3339),
	}
}

// Hub fans out activity events to the WebSocket clients watching a repo.
//
// Clients are indexed by repo key (the normalized "owner/repo"), so a broadcast
// for repo A never reaches a client watching repo B — the hub enforces the same
// tenant isolation at the fan-out layer that the store enforces at the data
// layer. The hub is safe for concurrent use.
type Hub struct {
	mu      sync.RWMutex
	clients map[string]map[*wsClient]struct{} // repoKey -> set of clients
}

// NewHub creates an empty hub.
func NewHub() *Hub {
	return &Hub{clients: make(map[string]map[*wsClient]struct{})}
}

// register adds a client to its repo bucket.
func (h *Hub) register(c *wsClient) {
	h.mu.Lock()
	defer h.mu.Unlock()
	set := h.clients[c.repoKey]
	if set == nil {
		set = make(map[*wsClient]struct{})
		h.clients[c.repoKey] = set
	}
	set[c] = struct{}{}
}

// unregister removes a client and prunes an empty repo bucket so the hub never
// retains memory for a repo with no live watchers.
func (h *Hub) unregister(c *wsClient) {
	h.mu.Lock()
	defer h.mu.Unlock()
	set := h.clients[c.repoKey]
	if set == nil {
		return
	}
	delete(set, c)
	if len(set) == 0 {
		delete(h.clients, c.repoKey)
	}
}

// clientCount reports how many clients currently watch a repo. Used by tests
// and diagnostics.
func (h *Hub) clientCount(repoKey string) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients[repoKey])
}

// Broadcast delivers an activity event to every client watching repoKey. A
// client whose send buffer is full is skipped (dropped frame) rather than
// allowed to block the hub; its own read/ping loop will eventually detect the
// stall and close. Broadcast never blocks on a slow client.
func (h *Hub) Broadcast(repoKey string, ev task.ActivityEvent) {
	msg := wsMessage{Type: "event", Event: ptrActivity(activityToWS(ev))}
	h.mu.RLock()
	// Snapshot the client set under the read lock, then send outside it so a
	// slow send can never hold the hub lock.
	targets := make([]*wsClient, 0, len(h.clients[repoKey]))
	for c := range h.clients[repoKey] {
		targets = append(targets, c)
	}
	h.mu.RUnlock()
	for _, c := range targets {
		c.trySend(msg)
	}
}

func ptrActivity(a wsActivity) *wsActivity { return &a }

// wsClient is one live WebSocket connection bound to an authorized repo.
type wsClient struct {
	conn    *websocket.Conn
	repoKey string
	send    chan wsMessage
}

// trySend enqueues a message without blocking. Returns false if the buffer is
// full (the client is too slow and the frame is dropped).
func (c *wsClient) trySend(msg wsMessage) bool {
	select {
	case c.send <- msg:
		return true
	default:
		return false
	}
}

// wsRepoFromPath extracts "owner/repo" from a /ws/{owner}/{repo} path. It
// validates the owner/repo shape via auth.NormalizeRepo so a malformed path is
// rejected before any auth work. Extra path segments or missing parts are an
// error.
func wsRepoFromPath(urlPath string) (string, error) {
	trimmed := strings.TrimPrefix(urlPath, "/ws/")
	if trimmed == urlPath { // prefix was absent
		return "", fmt.Errorf("not a /ws/ path")
	}
	trimmed = strings.Trim(trimmed, "/")
	parts := strings.Split(trimmed, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", fmt.Errorf("path must be /ws/{owner}/{repo}")
	}
	return auth.NormalizeRepo(parts[0] + "/" + parts[1])
}

// snapshotProvider loads the current task list for a repo so a freshly
// connected client can render the board without a round-trip to the REST/MCP
// path. It is satisfied by the Server (resolveTaskSystemForRepo + List).
type snapshotProvider interface {
	wsSnapshot(ctx context.Context, tenant *auth.Tenant) ([]wsTask, string, error)
}

// WebSocketHandler returns the /ws/{owner}/{repo} upgrade handler. resolver is
// the SAME signature-first validator used by the MCP path; snap loads the
// initial board snapshot. The returned handler:
//
//  1. parses + validates the repo from the URL path,
//  2. upgrades to WebSocket,
//  3. reads the first frame, requires it to be {type:"auth", token:...},
//  4. validates the token and authorizes the repo through resolver.Resolve,
//  5. sends the initial snapshot,
//  6. registers with the hub and runs the read/write/heartbeat loops until the
//     connection ends.
//
// A failure at any step closes the socket with an explanatory error frame and a
// WebSocket close code; no partial state is left in the hub.
func (h *Hub) WebSocketHandler(resolver *auth.Resolver, snap snapshotProvider) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		repo, err := wsRepoFromPath(r.URL.Path)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			// The browser connects cross-origin from the SPA; the request is
			// already gated by token auth below, so origin is not the security
			// boundary. InsecureSkipVerify disables coder/websocket's built-in
			// same-origin check (which would otherwise reject the SPA).
			InsecureSkipVerify: true,
		})
		if err != nil {
			return // Accept already wrote the HTTP error
		}
		conn.SetReadLimit(wsMaxMessageBytes)

		// Per-connection context: canceled on any terminal condition, which
		// unwinds both goroutines. Never leaks.
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()

		tenant, authErr := authenticateWS(ctx, conn, resolver, repo)
		if authErr != nil {
			_ = writeWSMessage(ctx, conn, wsMessage{Type: "error", Message: authErr.Error()})
			_ = conn.Close(websocket.StatusPolicyViolation, "auth failed")
			return
		}

		client := &wsClient{conn: conn, repoKey: repo, send: make(chan wsMessage, wsSendBuffer)}

		// Initial snapshot (best-effort: a snapshot failure still allows the
		// client to receive live events rather than tearing down the socket).
		if tasks, cursor, serr := snap.wsSnapshot(ctx, tenant); serr == nil {
			client.trySend(wsMessage{Type: "snapshot", Tasks: tasks, Cursor: cursor})
		}

		h.register(client)
		defer h.unregister(client)

		// Read pump: consumes client frames (pong / client-initiated ping) and
		// cancels the context on read error/close, unwinding the write loop.
		go readPump(ctx, cancel, conn)

		// Write loop (this goroutine): drains the send queue and emits periodic
		// pings. Returns when the context is canceled.
		writeLoop(ctx, conn, client.send)

		_ = conn.Close(websocket.StatusNormalClosure, "")
	})
}

// authenticateWS reads the first frame, requires an auth message, and resolves
// the authorized tenant for the URL-path repo via the shared resolver. The
// token is validated signature-first; a repo the token does not grant is a
// closed socket, never a cross-tenant read.
func authenticateWS(ctx context.Context, conn *websocket.Conn, resolver *auth.Resolver, repo string) (*auth.Tenant, error) {
	// Bound the wait for the auth frame so an idle connection cannot sit open
	// forever before authenticating.
	authCtx, cancel := context.WithTimeout(ctx, wsWriteTimeout)
	defer cancel()

	var first wsMessage
	if err := readWSMessage(authCtx, conn, &first); err != nil {
		return nil, fmt.Errorf("expected auth frame: %w", err)
	}
	if first.Type != "auth" || strings.TrimSpace(first.Token) == "" {
		return nil, fmt.Errorf("first frame must be {type:\"auth\", token:...}")
	}

	// Reuse the HTTP auth pipeline verbatim by synthesizing a request carrying
	// the bearer token and the target repo. This keeps WS and MCP auth on ONE
	// code path (signature-first validation + repos-claim authorization), so
	// the two can never drift.
	// #nosec G704 -- this request is never sent; it is a synthetic *http.Request
	// built only to feed the in-process auth resolver. No outbound call is made.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "/ws/"+repo, nil)
	if err != nil {
		return nil, fmt.Errorf("auth: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+first.Token)
	req.Header.Set(auth.HeaderTargetRepo, repo)

	tenant, err := resolver.Resolve(req, repo)
	if err != nil {
		return nil, fmt.Errorf("not authorized for %s", repo)
	}
	if tenant == nil || tenant.Unscoped {
		return nil, fmt.Errorf("token does not authorize repo %s", repo)
	}
	return tenant, nil
}

// readPump consumes inbound frames until the connection closes or errors, then
// cancels the shared context. A pong (or client-initiated ping) just resets the
// read; any read error means the peer is gone.
func readPump(ctx context.Context, cancel context.CancelFunc, conn *websocket.Conn) {
	defer cancel()
	for {
		var msg wsMessage
		if err := readWSMessage(ctx, conn, &msg); err != nil {
			return
		}
		// A client-initiated ping is answered with a pong; everything else
		// (pong, unknown) is ignored. We do not act on client control frames
		// beyond liveness.
		if msg.Type == "ping" {
			// Best-effort; a write failure will surface on the write loop.
			_ = writeWSMessage(ctx, conn, wsMessage{Type: "pong"})
		}
	}
}

// writeLoop drains the per-client send queue and emits a heartbeat ping every
// wsPingInterval. It returns when the context is canceled (peer gone, read
// error, or server shutdown), guaranteeing the goroutine exits.
func writeLoop(ctx context.Context, conn *websocket.Conn, send <-chan wsMessage) {
	ticker := time.NewTicker(wsPingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-send:
			if err := writeWSMessage(ctx, conn, msg); err != nil {
				return
			}
		case <-ticker.C:
			if err := writeWSMessage(ctx, conn, wsMessage{Type: "ping"}); err != nil {
				return
			}
		}
	}
}

// writeWSMessage marshals and writes one JSON text frame with a bounded
// deadline so a stuck peer cannot wedge the writer.
func writeWSMessage(ctx context.Context, conn *websocket.Conn, msg wsMessage) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	wctx, cancel := context.WithTimeout(ctx, wsWriteTimeout)
	defer cancel()
	return conn.Write(wctx, websocket.MessageText, data)
}

// readWSMessage reads one text frame and unmarshals it into msg.
func readWSMessage(ctx context.Context, conn *websocket.Conn, msg *wsMessage) error {
	_, data, err := conn.Read(ctx)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, msg)
}
