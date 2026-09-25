package main

import (
	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsdynamodb"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsiam"
	"github.com/aws/aws-cdk-go/awscdk/v2/awslambda"
	"github.com/aws/aws-cdk-go/awscdk/v2/awslogs"
	"github.com/aws/constructs-go/constructs/v10"
	"github.com/aws/jsii-runtime-go"
)

// CainbanStackProps configures the cainban Phase 2 stack.
type CainbanStackProps struct {
	awscdk.StackProps
}

// NewCainbanStack builds the Phase 2 serverless stack.
func NewCainbanStack(scope constructs.Construct, id string, props *CainbanStackProps) awscdk.Stack {
	var sprops awscdk.StackProps
	if props != nil {
		sprops = props.StackProps
	}
	stack := awscdk.NewStack(scope, &id, &sprops)

	// --- DynamoDB single table -------------------------------------------
	//
	// One table holds boards, the atomic per-board counter, tasks and links,
	// keyed by (PK, SK). PAY_PER_REQUEST (on-demand): no capacity to manage and
	// zero cost when idle, right for a dev/low-traffic MCP board. RETAIN on
	// delete so a `cdk destroy` never silently drops task data.
	table := awsdynamodb.NewTable(stack, jsii.String("Table"), &awsdynamodb.TableProps{
		TableName: jsii.String("cainban"),
		PartitionKey: &awsdynamodb.Attribute{
			Name: jsii.String("PK"),
			Type: awsdynamodb.AttributeType_STRING,
		},
		SortKey: &awsdynamodb.Attribute{
			Name: jsii.String("SK"),
			Type: awsdynamodb.AttributeType_STRING,
		},
		BillingMode:   awsdynamodb.BillingMode_PAY_PER_REQUEST,
		RemovalPolicy: awscdk.RemovalPolicy_RETAIN,
		PointInTimeRecoverySpecification: &awsdynamodb.PointInTimeRecoverySpecification{
			PointInTimeRecoveryEnabled: jsii.Bool(true),
		},
	})

	// --- Lambda function --------------------------------------------------
	//
	// provided.al2023 + arm64 running the Go bootstrap built from
	// cmd/cainban-lambda. Code is bundled by shelling out to the Go toolchain
	// (CGO off) inside the CDK asset staging so `cdk synth`/`deploy` produce a
	// fresh binary. The DynamoDB backend is selected via env vars.
	fn := awslambda.NewFunction(stack, jsii.String("McpFunction"), &awslambda.FunctionProps{
		FunctionName: jsii.String("cainban-mcp"),
		Runtime:      awslambda.Runtime_PROVIDED_AL2023(),
		Architecture: awslambda.Architecture_ARM_64(),
		Handler:      jsii.String("bootstrap"),
		MemorySize:   jsii.Number(256),
		Timeout:      awscdk.Duration_Seconds(jsii.Number(30)),
		Environment: &map[string]*string{
			"CAINBAN_BACKEND":    jsii.String("dynamodb"),
			"CAINBAN_DDB_TABLE":  table.TableName(),
			"CAINBAN_DDB_REGION": stack.Region(),
		},
		// Build the Go binary into the asset directory. The command runs in a
		// local shell (no Docker) via TryBundle returning false is not used;
		// instead we rely on a pre-built asset path. See infra/README.md: the
		// build step (make lambda) must run before `cdk synth`/`deploy`.
		Code: awslambda.Code_FromAsset(jsii.String("../.build/lambda"), nil),
	})

	// Least-privilege: grant ONLY the DynamoDB actions the store actually
	// calls (dynamo.go uses GetItem, PutItem, UpdateItem, DeleteItem, Query),
	// scoped to this table's ARN. GrantReadWriteData is deliberately NOT used
	// because it also grants Scan, BatchWrite, and stream read actions the code
	// never issues.
	fn.AddToRolePolicy(awsiam.NewPolicyStatement(&awsiam.PolicyStatementProps{
		Effect: awsiam.Effect_ALLOW,
		Actions: jsii.Strings(
			"dynamodb:GetItem",
			"dynamodb:PutItem",
			"dynamodb:UpdateItem",
			"dynamodb:DeleteItem",
			"dynamodb:Query",
		),
		Resources: &[]*string{table.TableArn()},
	}))

	// --- Function URL (UNAUTHENTICATED — TEMPORARY) -----------------------
	//
	// Phase 2 exposes the MCP endpoint with NO auth so a developer can smoke
	// test it. This is NOT for production and NOT multi-tenant: Phase 3 adds
	// auth-gated, repo-scoped access. CORS is owned by the URL config (not the
	// handler) to avoid duplicate Access-Control-Allow-Origin headers.
	fnURL := fn.AddFunctionUrl(&awslambda.FunctionUrlOptions{
		AuthType: awslambda.FunctionUrlAuthType_NONE,
		Cors: &awslambda.FunctionUrlCorsOptions{
			AllowedOrigins: jsii.Strings("*"),
			AllowedMethods: &[]awslambda.HttpMethod{awslambda.HttpMethod_ALL},
			AllowedHeaders: jsii.Strings("content-type", "mcp-session-id", "mcp-protocol-version"),
		},
	})

	// --- Explicit CloudWatch log group -----------------------------------
	//
	// Created explicitly (rather than the implicit /aws/lambda/<name> group) so
	// retention is controlled and the group is a stack resource. One month is
	// plenty for a dev endpoint.
	awslogs.NewLogGroup(stack, jsii.String("McpLogGroup"), &awslogs.LogGroupProps{
		LogGroupName:  jsii.String("/aws/lambda/cainban-mcp"),
		Retention:     awslogs.RetentionDays_ONE_MONTH,
		RemovalPolicy: awscdk.RemovalPolicy_DESTROY,
	})

	// --- Outputs ----------------------------------------------------------
	awscdk.NewCfnOutput(stack, jsii.String("FunctionUrl"), &awscdk.CfnOutputProps{
		Value:       fnURL.Url(),
		Description: jsii.String("Public (UNAUTHENTICATED) MCP Streamable-HTTP endpoint — dev only, Phase 3 adds auth"),
	})
	awscdk.NewCfnOutput(stack, jsii.String("TableName"), &awscdk.CfnOutputProps{
		Value:       table.TableName(),
		Description: jsii.String("DynamoDB table backing cainban"),
	})

	return stack
}
