// Command infra is the AWS CDK (Go) app for cainban's Phase 2 serverless stack.
//
// It provisions a single DynamoDB table, an ARM64 provided.al2023 Lambda
// serving the stateless MCP handler (cmd/cainban-lambda), an unauthenticated
// Lambda Function URL (TEMPORARY — Phase 3 adds auth), least-privilege IAM
// scoped to the table, and an explicit CloudWatch log group.
//
// Deploy target (config only — this app is NOT deployed by the Phase 2 PR):
// AWS profile aws-test-hamin, region eu-north-1. See infra/README.md for the
// exact (un-run) commands.
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
			// Region is fixed to the Phase 2 dev target. Account is resolved
			// from the CLI credentials at deploy time (CDK_DEFAULT_ACCOUNT),
			// so `cdk synth` works with no account configured.
			Env: &awscdk.Environment{
				Region: jsii.String("eu-north-1"),
			},
			Description: jsii.String("cainban Phase 2: DynamoDB + arm64 Lambda + Function URL (unauthenticated, dev only)"),
		},
	})

	app.Synth(nil)
}
