// Command cainban-lambda is the AWS Lambda entrypoint for cainban's MCP server.
//
// It serves the SAME stateless Streamable-HTTP handler the local `cainban mcp
// --http` command serves, wrapped in Phase 3 with signature-first JWT auth +
// repo-scoped tenancy (mcp.Server.HandlerWithAuth), adapted to Lambda behind an
// API Gateway v2 HTTP API. The HTTP API delivers a payload-format-2.0 event
// (events.APIGatewayV2HTTPRequest), which the aws-lambda-go-api-proxy
// httpadapter (NewV2) turns into a net/http request the handler already
// understands. One *mcp.Server is built per request inside the handler (via
// getServer), so the function is safe for concurrent invocations.
//
// Storage: this binary forces the DynamoDB backend (CAINBAN_BACKEND=dynamodb is
// set at process start regardless of the environment) because Lambda has no
// local disk for SQLite and the SQLite driver needs CGO, which this binary is
// built without (CGO_ENABLED=0). Table name + region come from env vars the CDK
// stack sets (CAINBAN_DDB_TABLE, CAINBAN_DDB_REGION / AWS_REGION).
//
// Build (arm64, provided.al2023, pure Go):
//
//	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -tags lambda.norpc \
//	    -o bootstrap ./cmd/cainban-lambda
//
// NOTE: The transport is an API Gateway v2 HTTP API fronted by a managed
// Cognito JWT authorizer (issuer/audience from the stack's own Cognito user
// pool + app client). The authorizer validates the token at the edge; the
// Lambda still performs signature-first JWT validation (Cognito JWKS) and
// repo-scoped tenant resolution on EVERY request via mcp.HandlerWithAuth
// (defense in depth). The client now sends `Authorization: Bearer <jwt>` with
// NO SigV4, so the token reaches the header the in-Lambda validator always
// expected (the Function URL AWS_IAM SigV4 header collision is gone). Issuer,
// audience and JWKS URL come from env vars the CDK stack sets from the pool.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/awslabs/aws-lambda-go-api-proxy/httpadapter"

	"github.com/hmain/cainban/src/systems/mcp"
	"github.com/hmain/cainban/src/systems/mcp/wsconn"
)

// envWebSocketAPIEndpoint is the API Gateway WebSocket API's per-stage callback
// URL (e.g. https://<apiId>.execute-api.<region>.amazonaws.com/<stage>). The
// CDK stack sets it from the WebSocket stage URL. When present, this Lambda
// installs the API-Gateway-WebSocket broadcaster so a task mutation pushes a
// real-time frame to every connected browser; when absent (e.g. a deployment
// without the WebSocket API), the server keeps its in-process hub default and
// simply has no external fan-out (the serverless MCP path has no persistent WS
// server, so no client is connected to the hub — the push is a no-op, exactly
// as before).
const envWebSocketAPIEndpoint = "CAINBAN_WS_API_ENDPOINT"

func main() {
	// Force the DynamoDB backend: this binary has no SQLite (built CGO-off) and
	// Lambda has no durable local disk.
	_ = ensureDynamoBackend()

	// Build the signature-first auth resolver from env (Cognito user pool). A
	// misconfiguration is fatal at cold start rather than silently serving an
	// unauthenticated or fail-open endpoint.
	resolver, err := buildResolver()
	if err != nil {
		log.Fatalf("cainban-lambda: auth configuration error: %v", err)
	}

	server := mcp.NewStateless()

	// Install the API Gateway WebSocket broadcaster when the WebSocket API
	// endpoint is configured. This is the serverless replacement for the
	// in-process hub's fan-out: on a mutation, broadcastActivity routes through
	// the broadcaster, which lists the repo's persisted connections and posts
	// the activity frame to each via the API Gateway Management API. A
	// misconfigured AWS config is fatal at cold start (a half-wired broadcaster
	// that silently drops every push would be worse than failing loudly).
	if endpoint := strings.TrimSpace(os.Getenv(envWebSocketAPIEndpoint)); endpoint != "" {
		cfg, cfgErr := awsconfig.LoadDefaultConfig(context.Background())
		if cfgErr != nil {
			log.Fatalf("cainban-lambda: load AWS config for WebSocket broadcaster: %v", cfgErr)
		}
		server.SetBroadcaster(wsconn.New(cfg, endpoint))
		log.Printf("cainban-lambda: API Gateway WebSocket broadcaster enabled (%s)", endpoint)
	}

	// Top-level handler: the RFC 9728 protected-resource metadata document is
	// served PUBLICLY at /.well-known/oauth-protected-resource (MCP OAuth
	// discovery must work before a client has any token), while EVERY other
	// route goes through signature-first JWT auth + repo-scoped tenant
	// resolution. On a 401, the authed path advertises the metadata URL in the
	// WWW-Authenticate challenge. Under GitHub-only auth there is no OAuth
	// authorization server to discover; the document and challenge carry only the
	// `resource` indicator, DERIVED from env (CAINBAN_MCP_RESOURCE), never
	// hardcoded.
	authed := server.HandlerWithAuthChallenge(resolver, resourceMetadataURL())
	// Public, unauthenticated dynamic status badge endpoint
	// (GET /badge/{owner}/{repo}.svg). It is wired onto the PublicMux alongside
	// the well-known discovery routes and BEFORE the authed MCP routes, so it
	// never passes through the JWT authorizer. It exposes only per-status task
	// counts, nothing else.
	badge := http.HandlerFunc(server.HandleBadge)
	// GitHub OAuth config for device flow endpoints. Loaded from Secrets Manager
	// at cold start; empty config if not configured (graceful degradation).
	githubAuth := githubAuthConfig(context.Background())
	if githubAuth.ClientID != "" {
		log.Printf("cainban-lambda: GitHub device flow enabled")
	}
	handler := mcp.PublicMux(resourceMetadataConfig(), clientConfigConfig(), githubAuth, badge, authed)
	adapter := httpadapter.NewV2(handler)

	// This Lambda is fronted by an API Gateway v2 HTTP API (payload format 2.0),
	// NOT a Function URL. The managed Cognito JWT authorizer validates the token
	// signature at the edge; the in-Lambda validator (HandlerWithAuth) then
	// re-validates signature-first AND resolves the repo-scoped tenant from the
	// same `Authorization: Bearer <jwt>` header (no SigV4 now, so no header
	// collision). An HTTP API event is already an events.APIGatewayV2HTTPRequest,
	// so the httpadapter consumes it directly — no Function URL conversion.
	lambda.Start(func(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
		resp, err := adapter.ProxyWithContext(ctx, req)
		if err != nil {
			return events.APIGatewayV2HTTPResponse{StatusCode: 502}, err
		}
		return resp, nil
	})
}
