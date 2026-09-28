// Command cainban-connect is the AWS Lambda entrypoint for cainban's Phase 4
// human GitHub-connect API. It serves the /connect/* routes (start, callback,
// repo POST/DELETE, repos list) behind signature-first Cognito JWT validation,
// verifies repo access server-side against the GitHub App, and writes/reads the
// resulting grants in the grants table.
//
// It is a SEPARATE Lambda from the MCP server (cmd/cainban-lambda): the connect
// flow has a different IAM surface (Secrets Manager read + grants-table
// read/WRITE, and NO access to the board/task data table) and a different job
// (human OAuth + verification), so keeping it its own function keeps each
// function's blast radius and IAM minimal. Both are provided.al2023 + arm64,
// pure Go (CGO off — no SQLite here).
//
// Build (arm64, provided.al2023, pure Go):
//
//	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -tags lambda.norpc \
//	    -o bootstrap ./cmd/cainban-connect
//
// Runtime config (env vars set by the CDK stack):
//
//	CAINBAN_AUTH_ISSUER        Cognito issuer URL (JWT iss)
//	CAINBAN_AUTH_AUDIENCE      Cognito app client id (JWT aud)
//	CAINBAN_AUTH_JWKS_URL      optional JWKS override (else <issuer>/.well-known/jwks.json)
//	CAINBAN_GITHUB_APP_SECRET  Secrets Manager secret name (App id/key/client id+secret)
//	CAINBAN_GRANTS_TABLE       grants DynamoDB table name
//	CAINBAN_GRANTS_REGION      grants table region (else AWS_REGION)
//	CAINBAN_CONNECT_REDIRECT_URI  optional App callback URL echoed to GitHub's OAuth
//	CAINBAN_CONNECT_SUCCESS_URL   optional browser redirect after a successful link
//
// NO secret value ever lives in code, the repo, or CDK — the App credentials
// and the HMAC state key are derived at runtime from the Secrets Manager secret.
package main

import (
	"context"
	"log"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/awslabs/aws-lambda-go-api-proxy/httpadapter"
)

func main() {
	handler, err := buildHandler(context.Background())
	if err != nil {
		// Fail at cold start rather than serving a misconfigured / fail-open
		// endpoint.
		log.Fatalf("cainban-connect: startup error: %v", err)
	}

	adapter := httpadapter.NewV2(handler)

	// Fronted by an API Gateway v2 HTTP API (payload format 2.0), NOT a Function
	// URL. Every /connect route is protected by the managed Cognito JWT
	// authorizer EXCEPT GET /connect/github/callback, which is authorizer-exempt
	// (a browser redirect from GitHub cannot carry a Cognito JWT; it is
	// authenticated by the HMAC-signed, sub-bound state param). An HTTP API event
	// is already an events.APIGatewayV2HTTPRequest, so the httpadapter consumes
	// it directly — no Function URL conversion. RawPath carries the full route
	// path (e.g. /connect/github/callback), which the handler routes on.
	lambda.Start(func(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
		resp, err := adapter.ProxyWithContext(ctx, req)
		if err != nil {
			return events.APIGatewayV2HTTPResponse{StatusCode: 502}, err
		}
		return resp, nil
	})
}
