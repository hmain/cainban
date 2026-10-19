// Command cainban-ws-lambda is the AWS Lambda entrypoint for cainban's
// real-time WebSocket endpoint WHEN served by an API Gateway WebSocket API.
//
// The MCP HTTP API (cmd/cainban-lambda) cannot carry WebSockets — API Gateway
// HTTP APIs do not proxy raw upgrades — so real-time board updates run on a
// SEPARATE API Gateway WebSocket API fronted by THIS function. API Gateway owns
// the sockets and invokes this Lambda once per event:
//
//	$connect    — a browser opened wss://<host>/<stage>/{owner}/{repo}
//	$disconnect — the socket closed
//	auth        — a custom route ({ "action":"auth", "token":..., "repo":... })
//	$default    — any other client frame (heartbeat ping)
//
// There are no live connections in process memory (the Lambda is cold between
// events); connections live in a DynamoDB table (src/systems/mcp/wsconn) keyed
// by connectionId, and the MCP Lambda pushes activity to a repo's sockets via
// the API Gateway Management API using that table's repo GSI.
//
// Auth is the SAME signature-first JWT validation + repo-scoped authorization as
// the MCP path (src/systems/auth): the browser WebSocket API cannot set an
// Authorization header, so the token arrives in the first `auth` frame, is
// validated here, and only then is the connection marked authenticated and
// eligible to receive broadcasts. An un-authenticated socket never receives a
// repo's data.
//
// Build (arm64, provided.al2023, pure Go):
//
//	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -tags lambda.norpc \
//	    -o bootstrap ./cmd/cainban-ws-lambda
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/apigatewaymanagementapi"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"

	"github.com/hmain/cainban/src/systems/auth"
	"github.com/hmain/cainban/src/systems/mcp/wsconn"
)

// Env vars the CDK stack sets.
const (
	// envConnTable is the DynamoDB connections table name.
	envConnTable = "CAINBAN_WS_CONN_TABLE"
	// envConnRegion is the region for the DynamoDB + management-API clients.
	envConnRegion = "CAINBAN_WS_CONN_REGION"
	// envWSCallbackURL is the API Gateway Management API callback endpoint
	// (https://<api-id>.execute-api.<region>.amazonaws.com/<stage>) used to post
	// frames back to a connection. The CDK stack derives it from the WebSocket
	// API + stage.
	envWSCallbackURL = "CAINBAN_WS_CALLBACK_URL"
)

func main() {
	ctx := context.Background()

	table := strings.TrimSpace(os.Getenv(envConnTable))
	if table == "" {
		log.Fatalf("cainban-ws-lambda: %s must be set", envConnTable)
	}
	region := strings.TrimSpace(os.Getenv(envConnRegion))
	if region == "" {
		region = strings.TrimSpace(os.Getenv("AWS_REGION"))
	}
	callbackURL := strings.TrimSpace(os.Getenv(envWSCallbackURL))
	if callbackURL == "" {
		log.Fatalf("cainban-ws-lambda: %s must be set", envWSCallbackURL)
	}

	resolver, err := buildResolver()
	if err != nil {
		log.Fatalf("cainban-ws-lambda: auth configuration error: %v", err)
	}

	var optFns []func(*awsconfig.LoadOptions) error
	if region != "" {
		optFns = append(optFns, awsconfig.WithRegion(region))
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, optFns...)
	if err != nil {
		log.Fatalf("cainban-ws-lambda: load AWS config: %v", err)
	}

	ddb := dynamodb.NewFromConfig(cfg)
	store := wsconn.New(ddb, table)

	// The management-API client's endpoint is the per-API+stage callback URL,
	// NOT the default service endpoint.
	mgmt := apigatewaymanagementapi.NewFromConfig(cfg, func(o *apigatewaymanagementapi.Options) {
		o.BaseEndpoint = &callbackURL
	})
	broadcaster := wsconn.NewBroadcaster(store, mgmt)

	h := &handler{conns: store, sender: broadcaster, resolver: resolver}

	lambda.Start(func(ctx context.Context, req events.APIGatewayWebsocketProxyRequest) (events.APIGatewayProxyResponse, error) {
		return h.handle(ctx, req)
	})
}

// buildResolver constructs the signature-first auth resolver from env — the
// SAME CAINBAN_AUTH_* configuration the MCP Lambda uses, so a token minted for
// the MCP path authenticates the WebSocket path with no second credential.
func buildResolver() (*auth.Resolver, error) {
	issuer := strings.TrimSpace(os.Getenv("CAINBAN_AUTH_ISSUER"))
	audiences := splitAudiences(os.Getenv("CAINBAN_AUTH_AUDIENCE"))
	if issuer == "" || len(audiences) == 0 {
		return nil, fmt.Errorf("both CAINBAN_AUTH_ISSUER and CAINBAN_AUTH_AUDIENCE must be set")
	}
	jwksURL := strings.TrimSpace(os.Getenv("CAINBAN_AUTH_JWKS_URL"))
	if jwksURL == "" {
		jwksURL = strings.TrimRight(issuer, "/") + "/.well-known/jwks.json"
	}
	validator, err := auth.NewValidator(auth.Config{
		Issuer:    issuer,
		Audiences: audiences,
		Keys:      auth.NewJWKSCache(jwksURL),
	})
	if err != nil {
		return nil, err
	}
	return auth.NewResolver(validator), nil
}

// splitAudiences parses the comma-separated CAINBAN_AUTH_AUDIENCE into accepted
// app-client-id audiences (trimmed, non-empty), mirroring the MCP Lambda so one
// Cognito pool with several app clients validates on both paths.
func splitAudiences(raw string) []string {
	out := []string{}
	for _, p := range strings.Split(raw, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
