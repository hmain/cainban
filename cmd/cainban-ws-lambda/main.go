// Command cainban-ws-lambda is the AWS Lambda entrypoint for cainban's API
// Gateway WebSocket API — the serverless real-time push transport.
//
// # Why this exists
//
// cainban's in-process WebSocket endpoint (/ws/{owner}/{repo}, src/systems/mcp/
// websocket.go) pushes task-activity events to browsers over a long-lived
// connection the server process holds. That works on a long-running server but
// NOT on Lambda: an API Gateway v2 HTTP API does not proxy raw WebSocket
// upgrades, and a Lambda has no persistent process to hold the connection.
//
// An API Gateway WebSocket API solves both: API Gateway terminates the
// WebSocket and invokes this Lambda on three route keys — $connect,
// $disconnect, $default. This function does NOT stream; it only manages the
// connection registry in DynamoDB (store.WSConnectionStore). The actual push
// happens from the MCP Lambda's broadcaster (src/systems/mcp/wsconn), which
// lists a repo's connections and calls the API Gateway Management API
// (PostToConnection) when a tool mutates a task.
//
// # Auth (identical trust model to the in-process WS path)
//
// The browser WebSocket API cannot set request headers, so the bearer token and
// target repo arrive as $connect QUERY-STRING parameters: wss://<host>/<stage>
// ?repo=<owner/repo>&token=<jwt>. The token is validated SIGNATURE-FIRST through
// the SAME auth.Resolver the MCP/HTTP path uses, and the repo is authorized
// against the token's signed `repos` claim BEFORE any connection row is stored.
// A token that does not grant the target repo is rejected at $connect with a
// non-2xx, so the connection never joins any repo's fan-out set — tenant
// isolation is structural, exactly as on the MCP path.
//
// Build (arm64, provided.al2023, pure Go — no SQLite/CGO):
//
//	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -tags lambda.norpc \
//	    -o bootstrap ./cmd/cainban-ws-lambda
package main

import (
	"context"
	"log"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"

	"github.com/hmain/cainban/src/systems/store"
)

func main() {
	// Force the DynamoDB backend: Lambda has no durable local disk and this
	// binary is built CGO-off (no SQLite).
	_ = ensureDynamoBackend()

	// Build the signature-first auth resolver from env (same Cognito pool as the
	// MCP Lambda). A misconfiguration is fatal at cold start rather than serving
	// an endpoint that would accept any connection.
	resolver, err := buildResolver()
	if err != nil {
		log.Fatalf("cainban-ws-lambda: auth configuration error: %v", err)
	}

	h := &handler{
		resolver: resolver,
		open:     store.OpenWSConnectionStoreForTenant,
	}

	lambda.Start(func(ctx context.Context, req events.APIGatewayWebsocketProxyRequest) (events.APIGatewayProxyResponse, error) {
		return h.handle(ctx, req), nil
	})
}
