// Command cainban-lambda is the AWS Lambda entrypoint for cainban's MCP server.
//
// It serves the SAME stateless Streamable-HTTP handler the local `cainban mcp
// --http` command serves, wrapped in Phase 3 with signature-first JWT auth +
// repo-scoped tenancy (mcp.Server.HandlerWithAuth), adapted to Lambda via a
// Function URL. A Function URL delivers a payload-format-2.0 event, which the
// aws-lambda-go-api-proxy httpadapter (NewV2) turns into a net/http request the
// handler already understands. One *mcp.Server is built per request inside the
// handler (via getServer), so the function is safe for concurrent invocations.
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
// NOTE: Phase 3 replaces the Phase 2 unauthenticated Function URL. The Lambda
// now performs signature-first JWT validation (Cognito JWKS) and repo-scoped
// tenant resolution on EVERY request via mcp.HandlerWithAuth; the Function URL
// AuthType is AWS_IAM at the edge (no anonymous reachability). Issuer, audience
// and JWKS URL come from env vars the CDK stack sets from the Cognito user pool.
package main

import (
	"context"
	"log"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/awslabs/aws-lambda-go-api-proxy/httpadapter"

	"github.com/hmain/cainban/src/systems/mcp"
)

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
	adapter := httpadapter.NewV2(server.HandlerWithAuth(resolver))

	lambda.Start(func(ctx context.Context, req events.LambdaFunctionURLRequest) (events.LambdaFunctionURLResponse, error) {
		// A Function URL request is payload format 2.0, structurally the same as
		// an API Gateway HTTP API v2 request the adapter consumes.
		v2 := functionURLToAPIGatewayV2(req)
		resp, err := adapter.ProxyWithContext(ctx, v2)
		if err != nil {
			return events.LambdaFunctionURLResponse{StatusCode: 502}, err
		}
		return apiGatewayV2ToFunctionURL(resp), nil
	})
}
