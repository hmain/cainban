// Command infra is the AWS CDK (Go) app for cainban's serverless stack.
//
// It provisions the DynamoDB task table and grants table, three ARM64
// provided.al2023 Lambdas (the stateless MCP handler, the connect API, and the
// Cognito pre-token trigger), two API Gateway v2 HTTP APIs (MCP + connect) each
// fronted by a managed Cognito JWT authorizer (Authorization: Bearer <jwt>, no
// SigV4) — the connect callback route is authorizer-exempt (HMAC state auth) —
// a Cognito user pool, a placeholder GitHub App secret, least-privilege IAM per
// function, reserved-concurrency caps, and explicit CloudWatch log groups.
//
// Deploy target: AWS profile aws-test-hamin, account 528757808822, region
// eu-north-1. See infra/README.md for the exact commands.
//
// Go CDK is used (not TypeScript) so the whole repo stays single-language: the
// Lambda handler and the infrastructure are both Go, sharing one toolchain.
package main

import (
	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/jsii-runtime-go"
)

func main() {
	defer jsii.Close()

	app := awscdk.NewApp(nil)

	NewCainbanStack(app, "CainbanPhase2Stack", &CainbanStackProps{
		StackProps: awscdk.StackProps{
			// Region is fixed to the dev target. Account is pinned to the
			// aws-test-hamin dev account so `cdk diff`/`deploy` resolve a
			// concrete environment from code (credentials still come from the
			// --profile at deploy time; this is only the target identity).
			Env: &awscdk.Environment{
				Account: jsii.String("528757808822"),
				Region:  jsii.String("eu-north-1"),
			},
			Description: jsii.String("cainban: DynamoDB + grants table + arm64 Lambdas (MCP/connect/pre-token) + Cognito auth + GitHub App connect; HTTP APIs fronted by a managed Cognito JWT authorizer (Bearer JWT, no SigV4); connect callback authorizer-exempt (HMAC state)"),
		},
	})

	app.Synth(nil)
}
