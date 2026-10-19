package main

import (
	"context"
	"log"
	"net/http"
	"strings"

	"github.com/aws/aws-lambda-go/events"

	"github.com/hmain/cainban/src/systems/auth"
	"github.com/hmain/cainban/src/systems/store"
)

// tenantResolver is the slice of auth.Resolver the $connect path needs. An
// interface (rather than the concrete *auth.Resolver) keeps the handler
// testable with a fake; *auth.Resolver satisfies it directly in production.
type tenantResolver interface {
	Resolve(req *http.Request, argRepo string) (*auth.Tenant, error)
}

// Route keys API Gateway invokes this Lambda on. $default handles every data
// frame a client sends after connecting; cainban's push is one-directional
// (server -> client) so $default only answers an application-level ping to keep
// the frontend's heartbeat happy and otherwise ignores client frames.
const (
	routeConnect    = "$connect"
	routeDisconnect = "$disconnect"
	routeDefault    = "$default"
)

// connStoreOpener matches store.OpenWSConnectionStoreForTenant so production
// passes that function and tests pass a fake.
type connStoreOpener func(ctx context.Context, partitionPrefix string) (store.WSConnectionStore, func() error, error)

// handler holds the dependencies shared across invocations (one cold start).
type handler struct {
	resolver tenantResolver
	open     connStoreOpener
}

// handle dispatches one API Gateway WebSocket event by route key.
func (h *handler) handle(ctx context.Context, req events.APIGatewayWebsocketProxyRequest) events.APIGatewayProxyResponse {
	switch req.RequestContext.RouteKey {
	case routeConnect:
		return h.handleConnect(ctx, req)
	case routeDisconnect:
		return h.handleDisconnect(ctx, req)
	case routeDefault:
		return h.handleDefault(ctx, req)
	default:
		// Unknown route: accept so API Gateway does not retry, but do nothing.
		return ok()
	}
}

// handleConnect authenticates the connection and, on success, registers it in
// the repo's fan-out partition. Auth is signature-first through the shared
// resolver and the repo is authorized against the token's signed claim BEFORE
// any row is written — a token that does not grant the repo is a 403 and the
// connection is rejected, so it never joins the repo's broadcast set.
func (h *handler) handleConnect(ctx context.Context, req events.APIGatewayWebsocketProxyRequest) events.APIGatewayProxyResponse {
	token := strings.TrimSpace(req.QueryStringParameters["token"])
	rawRepo := strings.TrimSpace(req.QueryStringParameters["repo"])
	if token == "" || rawRepo == "" {
		return deny(http.StatusUnauthorized, "missing token or repo query parameter")
	}

	repo, err := auth.NormalizeRepo(rawRepo)
	if err != nil {
		return deny(http.StatusBadRequest, "invalid repo")
	}

	// Reuse the HTTP auth pipeline verbatim by synthesizing a request carrying
	// the bearer token and the target repo, exactly like the in-process WS path
	// (mcp.authenticateWS). Keeps WS and MCP auth on ONE code path.
	areq, err := http.NewRequestWithContext(ctx, http.MethodGet, "/ws", nil)
	if err != nil {
		return deny(http.StatusInternalServerError, "internal error")
	}
	areq.Header.Set("Authorization", "Bearer "+token)
	areq.Header.Set(auth.HeaderTargetRepo, repo)

	tenant, err := h.resolver.Resolve(areq, repo)
	if err != nil || tenant == nil || tenant.Unscoped {
		return deny(http.StatusForbidden, "not authorized for repo")
	}

	connStore, closeFn, err := h.open(ctx, tenant.PartitionPrefix)
	if err != nil {
		log.Printf("cainban-ws: open connection store: %v", err)
		return deny(http.StatusInternalServerError, "internal error")
	}
	defer func() { _ = closeFn() }()

	if err := connStore.PutWSConnection(ctx, req.RequestContext.ConnectionID, repo); err != nil {
		log.Printf("cainban-ws: register connection %s for %s: %v", req.RequestContext.ConnectionID, repo, err)
		return deny(http.StatusInternalServerError, "internal error")
	}
	return ok()
}

// handleDisconnect removes the connection from its repo's fan-out set. The
// $disconnect event carries only the connectionId (no repo/tenant), so the
// store opens with an empty prefix and resolves the repo from the stored
// reverse-lookup row. A missing connection is not an error (idempotent).
func (h *handler) handleDisconnect(ctx context.Context, req events.APIGatewayWebsocketProxyRequest) events.APIGatewayProxyResponse {
	connStore, closeFn, err := h.open(ctx, "")
	if err != nil {
		log.Printf("cainban-ws: open connection store: %v", err)
		// Return 200 anyway: a $disconnect retry cannot succeed if the store is
		// unreachable, and the TTL will reap the row.
		return ok()
	}
	defer func() { _ = closeFn() }()

	if err := connStore.DeleteWSConnection(ctx, req.RequestContext.ConnectionID); err != nil {
		log.Printf("cainban-ws: deregister connection %s: %v", req.RequestContext.ConnectionID, err)
	}
	return ok()
}

// handleDefault answers an application-level ping so the frontend heartbeat
// (web/src/board/wsAdapter.ts) stays alive, and ignores every other client
// frame — cainban's push is server-to-client only, so a client never drives
// state over this socket. The actual push comes from the MCP Lambda's
// broadcaster, not from here.
func (h *handler) handleDefault(_ context.Context, req events.APIGatewayWebsocketProxyRequest) events.APIGatewayProxyResponse {
	if strings.Contains(req.Body, `"ping"`) {
		// API Gateway sends the response body back to the client on $default.
		return events.APIGatewayProxyResponse{StatusCode: http.StatusOK, Body: `{"type":"pong"}`}
	}
	return ok()
}

func ok() events.APIGatewayProxyResponse {
	return events.APIGatewayProxyResponse{StatusCode: http.StatusOK}
}

func deny(code int, msg string) events.APIGatewayProxyResponse {
	return events.APIGatewayProxyResponse{StatusCode: code, Body: msg}
}
