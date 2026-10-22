// Command infra is the AWS CDK (Go) app for cainban's serverless stack.
//
// It provisions the DynamoDB task table, two ARM64 provided.al2023 Lambdas (the
// stateless MCP handler and the WebSocket connection registry), an API Gateway
// v2 HTTP API (MCP) with NO managed authorizer — auth is GitHub-only and
// enforced in the Lambda (Authorization: Bearer <github_token>, no SigV4, no
// Cognito) — a WebSocket API, the GitHub App + OAuth secrets, least-privilege
// IAM per function, reserved-concurrency caps, and explicit CloudWatch log
// groups. Authorization is the GitHub App installation token.
//
// Deploy target is selected by CAINBAN_ENV:
//
//	CAINBAN_ENV=dev (default)
//	  Profile:  aws-test-hamin
//	  Account:  528757808822
//	  Region:   eu-north-1
//	  Stack:    CainbanPhase2Stack
//
//	CAINBAN_ENV=prod
//	  Profile:  elastic-mobile-aws-reseller
//	  Account:  563329104476
//	  Region:   eu-central-1
//	  Stack:    CainbanProdStack
//
// See infra/README.md for the exact commands.
//
// Go CDK is used (not TypeScript) so the whole repo stays single-language: the
// Lambda handler and the infrastructure are both Go, sharing one toolchain.
package main

import (
	"os"

	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/jsii-runtime-go"
)

// envConfig holds the per-environment deployment settings.
type envConfig struct {
	StackName string
	Account   string
	Region    string
}

var environments = map[string]envConfig{
	"dev": {
		StackName: "CainbanPhase2Stack",
		Account:   "528757808822",
		Region:    "eu-north-1",
	},
	"prod": {
		StackName: "CainbanProdStack",
		Account:   "563329104476",
		Region:    "eu-central-1",
	},
}

func main() {
	defer jsii.Close()

	env := os.Getenv("CAINBAN_ENV")
	if env == "" {
		env = "dev"
	}
	cfg, ok := environments[env]
	if !ok {
		panic("CAINBAN_ENV must be 'dev' or 'prod', got: " + env)
	}

	app := awscdk.NewApp(nil)

	NewCainbanStack(app, cfg.StackName, &CainbanStackProps{
		StackProps: awscdk.StackProps{
			Env: &awscdk.Environment{
				Account: jsii.String(cfg.Account),
				Region:  jsii.String(cfg.Region),
			},
			Description: jsii.String("cainban: DynamoDB + arm64 Lambdas (MCP + WebSocket) + GitHub-only auth (GitHub token identity, GitHub App installation-token authorization); HTTP API with in-Lambda auth (Bearer GitHub token, no SigV4, no Cognito)"),
		},
	})

	app.Synth(nil)
}
