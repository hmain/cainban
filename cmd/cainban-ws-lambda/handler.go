package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/aws/aws-lambda-go/events"

	"github.com/hmain/cainban/src/systems/auth"
	"github.com/hmain/cainban/src/systems/mcp/wsconn"
)

// Route keys API Gateway delivers on a WebSocket API. $connect/$disconnect/
// $default are the predefined routes; "auth" is the custom route selected by
// the route selection expression ($request.body.action == "auth").
const (
	routeConnect    = "$connect"
	routeDisconnect = "$disconnect"
	routeDefault    = "$default"
	routeAuth       = "auth"
)

// inboundMessage is a frame the client sends. `action` is the route selector
// (the WebSocket API's route selection expression is $request.body.action); the
// custom `auth` route carries a token + the owner/repo being watched, and a
// ping is answered on $default.
type inboundMessage struct {
	Action string `json:"action,omitempty"`
	Type   string `json:"type,omitempty"`
	Token  string `json:"token,omitempty"`
	Repo   string `json:"repo,omitempty"`
}

// repo returns the normalized owner/repo the auth frame names, or an error when
// it is missing or structurally invalid.
func (m inboundMessage) repo() (string, error) {
	return auth.NormalizeRepo(strings.TrimSpace(m.Repo))
}

// store and broadcaster are the collaborators the handler needs. Defined as an
// interface-bearing struct so main wires the real DynamoDB/API-GW clients and
// tests wire fakes.
type handler struct {
	conns    connStore
	sender   frameSender
	resolver tokenResolver
}

// tokenResolver validates a bearer token carried in a synthesized request and
// authorizes the target repo. It is satisfied by *auth.Resolver (Resolve) in
// production and a fake in tests, so the handler logic is testable without RSA
// signing machinery.
type tokenResolver interface {
	Resolve(req *http.Request, argRepo string) (*auth.Tenant, error)
}

// connStore is the subset of wsconn.Store the handler calls (fakeable in tests).
type connStore interface {
	Add(ctx context.Context, connectionID, repo string) error
	Authenticate(ctx context.Context, connectionID, repo, tenantSub string) error
	Remove(ctx context.Context, connectionID string) error
}

// frameSender posts one frame to a single connection (fakeable in tests). It is
// satisfied by *wsconn.Broadcaster.Send.
type frameSender interface {
	Send(ctx context.Context, connectionID string, frame interface{}) error
}

// handle is the single dispatch point: it routes a WebSocket proxy event to the
// per-route handler and maps the outcome to an API Gateway response. A non-2xx
// status on $connect REJECTS the handshake (the socket never opens); on other
// routes it is informational (the socket stays open).
func (h *handler) handle(ctx context.Context, req events.APIGatewayWebsocketProxyRequest) (events.APIGatewayProxyResponse, error) {
	rc := req.RequestContext
	switch rc.RouteKey {
	case routeConnect:
		return h.onConnect(ctx, req)
	case routeDisconnect:
		return h.onDisconnect(ctx, rc.ConnectionID)
	case routeAuth:
		return h.onAuth(ctx, rc.ConnectionID, req.Body)
	case routeDefault:
		return h.onDefault(ctx, rc.ConnectionID, req.Body)
	default:
		return ok(), nil
	}
}

// onConnect validates the owner/repo path params and stores the (as-yet
// unauthenticated) connection. The repo is a HINT here: the data store is only
// opened after the auth frame proves the caller may watch it, so storing the
// repo now just lets the later broadcast find this socket. A structurally
// invalid path REJECTS the handshake (non-2xx) rather than leaving a junk row.
func (h *handler) onConnect(ctx context.Context, req events.APIGatewayWebsocketProxyRequest) (events.APIGatewayProxyResponse, error) {
	repo, err := repoFromConnect(req)
	if err != nil {
		log.Printf("ws $connect rejected: %v", err)
		return events.APIGatewayProxyResponse{StatusCode: http.StatusBadRequest, Body: "invalid path"}, nil
	}
	if err := h.conns.Add(ctx, req.RequestContext.ConnectionID, repo); err != nil {
		log.Printf("ws $connect store error: %v", err)
		return events.APIGatewayProxyResponse{StatusCode: http.StatusInternalServerError}, nil
	}
	return ok(), nil
}

// onDisconnect removes the connection row. Idempotent and best-effort: a missing
// row is not an error, and a store error is logged but still returns 200 (there
// is nothing the client can do, and the TTL backstop will reap any leftover).
func (h *handler) onDisconnect(ctx context.Context, connectionID string) (events.APIGatewayProxyResponse, error) {
	if err := h.conns.Remove(ctx, connectionID); err != nil {
		log.Printf("ws $disconnect store error: %v", err)
	}
	return ok(), nil
}

// onAuth validates the auth frame's JWT through the SAME signature-first
// resolver the MCP/HTTP path uses, authorizes the repo it carries, and — only
// on success — marks the connection authenticated with the resolved tenant
// subject. A failure sends an error frame and leaves the connection
// unauthenticated (so it receives no broadcasts); it does NOT close the socket
// here (API Gateway closes it when the client gives up, or the TTL reaps it).
func (h *handler) onAuth(ctx context.Context, connectionID, body string) (events.APIGatewayProxyResponse, error) {
	var msg inboundMessage
	if err := json.Unmarshal([]byte(body), &msg); err != nil {
		h.sendError(ctx, connectionID, "malformed auth frame")
		return ok(), nil
	}
	// The repo to authorize comes from the auth frame itself (the client echoes
	// the owner/repo it connected to in a `repo` field).
	repo, rerr := msg.repo()
	if rerr != nil {
		h.sendError(ctx, connectionID, "auth frame missing repo")
		return ok(), nil
	}
	if strings.TrimSpace(msg.Token) == "" {
		h.sendError(ctx, connectionID, "auth frame missing token")
		return ok(), nil
	}

	tenant, err := resolveToken(ctx, h.resolver, msg.Token, repo)
	if err != nil {
		h.sendError(ctx, connectionID, fmt.Sprintf("not authorized for %s", repo))
		return ok(), nil
	}

	if err := h.conns.Authenticate(ctx, connectionID, tenant.Repo, tenant.Subject); err != nil {
		// Most likely the socket already closed (condition failed). Nothing to
		// recover; the row is gone or will be reaped.
		log.Printf("ws auth store error: %v", err)
		return ok(), nil
	}
	_ = h.sender.Send(ctx, connectionID, map[string]any{"type": "authenticated", "repo": tenant.Repo})
	return ok(), nil
}

// onDefault handles any client frame that is not the custom auth route — in
// practice a heartbeat ping, answered with a pong so the client can measure
// liveness exactly as on the in-process hub path.
func (h *handler) onDefault(ctx context.Context, connectionID, body string) (events.APIGatewayProxyResponse, error) {
	var msg inboundMessage
	if err := json.Unmarshal([]byte(body), &msg); err == nil {
		if msg.Type == "ping" || msg.Action == "ping" {
			_ = h.sender.Send(ctx, connectionID, map[string]any{"type": "pong"})
		}
	}
	return ok(), nil
}

func (h *handler) sendError(ctx context.Context, connectionID, message string) {
	_ = h.sender.Send(ctx, connectionID, map[string]any{"type": "error", "message": message})
}

// resolveToken validates a raw bearer token and authorizes the target repo via
// the shared resolver, reusing the HTTP auth pipeline verbatim by synthesizing a
// request that carries the token and the target repo header — identical to the
// in-process WS handler's authenticateWS, so WS-on-Lambda and WS-on-server auth
// can never drift.
func resolveToken(ctx context.Context, resolver tokenResolver, token, repo string) (*auth.Tenant, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "/ws/"+repo, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set(auth.HeaderTargetRepo, repo)
	tenant, err := resolver.Resolve(req, repo)
	if err != nil {
		return nil, err
	}
	if tenant == nil || tenant.Unscoped {
		return nil, fmt.Errorf("token does not authorize repo %s", repo)
	}
	return tenant, nil
}

// ok is the standard 200 response.
func ok() events.APIGatewayProxyResponse {
	return events.APIGatewayProxyResponse{StatusCode: http.StatusOK}
}

// compile-time assurance the concrete types satisfy the handler interfaces.
var _ connStore = (*wsconn.Store)(nil)
