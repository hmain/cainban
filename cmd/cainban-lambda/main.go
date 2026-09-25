// Command cainban-lambda is the AWS Lambda entrypoint for cainban's MCP server.
//
// It serves the SAME stateless Streamable-HTTP handler the local `cainban mcp
// --http` command serves (mcp.Server.Handler), adapted to Lambda via a Function
// URL. A Function URL delivers a payload-format-2.0 event, which the
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
// NOTE: the Function URL is currently unauthenticated (auth NONE) — that is a
// TEMPORARY Phase 2 state for a private dev endpoint. Phase 3 adds auth-gated,
// repo-scoped tenancy. Do not expose this to untrusted callers.
package main

import (
	"context"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/awslabs/aws-lambda-go-api-proxy/httpadapter"

	"github.com/hmain/cainban/src/systems/mcp"
)

func main() {
	// Force the DynamoDB backend: this binary has no SQLite (built CGO-off) and
	// Lambda has no durable local disk.
	_ = ensureDynamoBackend()

	server := mcp.NewStateless()
	adapter := httpadapter.NewV2(server.Handler())

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
