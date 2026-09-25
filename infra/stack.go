package main

import (
	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/aws-cdk-go/awscdk/v2/awscognito"
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

	// --- Cognito user pool (identity provider) ---------------------------
	//
	// A self-contained Cognito user pool issues the JWTs the Lambda validates
	// (signature-first) on every request. Chosen over wiring an existing IdP
	// because it is self-contained and frugal for this dev account (no cost at
	// rest, generous free tier) and needs no external federation to stand up.
	// It is SWAPPABLE: the Lambda only needs an OIDC issuer + audience + JWKS
	// URL (CAINBAN_AUTH_* env vars), so pointing at an existing IdP later is a
	// config change, not a code change.
	//
	// Repo AUTHORIZATION is carried in a custom `repos` claim (a space/JSON
	// list of owner/repo the user may touch) plus an optional `default_repo`.
	// These are populated by the pool (e.g. a pre-token-generation trigger or
	// group mapping) — the Lambda never trusts a repo the token does not grant.
	userPool := awscognito.NewUserPool(stack, jsii.String("UserPool"), &awscognito.UserPoolProps{
		UserPoolName:      jsii.String("cainban-users"),
		SelfSignUpEnabled: jsii.Bool(false),
		SignInAliases: &awscognito.SignInAliases{
			Email: jsii.Bool(true),
		},
		RemovalPolicy: awscdk.RemovalPolicy_RETAIN,
		StandardAttributes: &awscognito.StandardAttributes{
			Email: &awscognito.StandardAttribute{Required: jsii.Bool(true), Mutable: jsii.Bool(true)},
		},
		CustomAttributes: &map[string]awscognito.ICustomAttribute{
			// Repos the user is authorized for; surfaced into the access/ID
			// token as a validated claim consumed by the Lambda's authorizer.
			"repos":        awscognito.NewStringAttribute(&awscognito.StringAttributeProps{Mutable: jsii.Bool(true)}),
			"default_repo": awscognito.NewStringAttribute(&awscognito.StringAttributeProps{Mutable: jsii.Bool(true)}),
		},
	})

	userPoolClient := userPool.AddClient(jsii.String("McpClient"), &awscognito.UserPoolClientOptions{
		UserPoolClientName: jsii.String("cainban-mcp-client"),
		AuthFlows: &awscognito.AuthFlow{
			UserPassword: jsii.Bool(true),
			UserSrp:      jsii.Bool(true),
		},
		GenerateSecret: jsii.Bool(false),
	})

	// --- Pre-token-generation trigger -----------------------------------
	//
	// Cognito stores a user's repo grants in the custom:repos / custom:default_repo
	// attributes, but it does NOT surface custom attributes as the TOP-LEVEL
	// `repos` / `default_repo` claims the auth validator (src/systems/auth)
	// authorizes against — its tokens carry them as `custom:repos` (a string).
	// This Lambda runs during token generation, reads those custom attributes,
	// and returns claimsToAddOrOverride mapping them onto the top-level `repos`
	// (a JSON-array-encoded string) + `default_repo` claims the validator reads.
	// Without it, no user's grants ever reach the validated claim and every
	// request 403s. It is a pure reflector of the user's stored attributes and
	// never invents a grant; it needs no permissions beyond basic CloudWatch
	// Logs (it reads only the attributes Cognito hands it in the event).
	//
	// Build the bootstrap into ../.build/pretoken (see `make pretoken`), mirroring
	// the MCP Lambda's ../.build/lambda asset. Same runtime/arch: provided.al2023
	// + arm64 + pure-Go bootstrap.
	preTokenFn := awslambda.NewFunction(stack, jsii.String("PreTokenFunction"), &awslambda.FunctionProps{
		FunctionName: jsii.String("cainban-pretoken"),
		Runtime:      awslambda.Runtime_PROVIDED_AL2023(),
		Architecture: awslambda.Architecture_ARM_64(),
		Handler:      jsii.String("bootstrap"),
		MemorySize:   jsii.Number(128),
		Timeout:      awscdk.Duration_Seconds(jsii.Number(5)),
		Code:         awslambda.Code_FromAsset(jsii.String("../.build/pretoken"), nil),
	})

	// Attach as the pool's PreTokenGeneration trigger. LambdaVersion V1_0 emits
	// the overrides as top-level ID-token claims via ClaimsToAddOrOverride
	// (map[string]string) — the string-valued shape the validator's reposClaim
	// decoder consumes. AddTrigger also grants Cognito permission to invoke the
	// function (a resource-based policy), so no manual permission is needed.
	userPool.AddTrigger(
		awscognito.UserPoolOperation_PRE_TOKEN_GENERATION(),
		preTokenFn,
		awscognito.LambdaVersion_V1_0,
	)

	// Explicit CloudWatch log group for the trigger (controlled retention, and a
	// stack-owned resource rather than the implicit /aws/lambda/<name> group).
	awslogs.NewLogGroup(stack, jsii.String("PreTokenLogGroup"), &awslogs.LogGroupProps{
		LogGroupName:  jsii.String("/aws/lambda/cainban-pretoken"),
		Retention:     awslogs.RetentionDays_ONE_MONTH,
		RemovalPolicy: awscdk.RemovalPolicy_DESTROY,
	})

	// Issuer for a Cognito user pool: the standard cognito-idp URL.
	issuer := awscdk.Fn_Sub(jsii.String("https://cognito-idp.${AWS::Region}.amazonaws.com/${PoolId}"),
		&map[string]*string{"PoolId": userPool.UserPoolId()})

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
			// Phase 3 auth config: signature-first JWT validation against this
			// Cognito user pool. The JWKS URL is derived from the issuer in the
			// handler when unset; audience is the app client id.
			"CAINBAN_AUTH_ISSUER":   issuer,
			"CAINBAN_AUTH_AUDIENCE": userPoolClient.UserPoolClientId(),
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

	// --- Function URL (AUTHENTICATED — Phase 3) --------------------------
	//
	// Phase 3 REPLACES the Phase 2 AuthType NONE. Two layers now gate the
	// endpoint:
	//   1. EDGE: AuthType AWS_IAM — the Function URL rejects any request that is
	//      not SigV4-signed by an allowed principal, so there is no anonymous
	//      reachability (the "no open endpoint remains" requirement).
	//   2. APPLICATION: the Lambda validates a Cognito JWT signature-first and
	//      resolves the repo-scoped tenant before any store access (identity +
	//      authorization). Bearer JWT is carried in the Authorization header.
	//
	// Alternative considered: an API Gateway HTTP API with a Cognito JWT
	// authorizer offloads signature validation to the managed authorizer. It was
	// NOT chosen because (a) the signature-first ordering is the security core
	// of this phase and must be unit-testable locally with a mock JWKS — an
	// in-Lambda validator is; a managed authorizer is not — and (b) it adds an
	// API Gateway surface + stage for no functional gain over the Function URL
	// the Phase 2 transport already targets. The in-Lambda validator keeps the
	// IdP swappable via env vars alone.
	fnURL := fn.AddFunctionUrl(&awslambda.FunctionUrlOptions{
		AuthType: awslambda.FunctionUrlAuthType_AWS_IAM,
		Cors: &awslambda.FunctionUrlCorsOptions{
			AllowedOrigins: jsii.Strings("*"),
			AllowedMethods: &[]awslambda.HttpMethod{awslambda.HttpMethod_ALL},
			// Authorization carries the bearer JWT; the SigV4 headers are needed
			// for the AWS_IAM edge; X-Cainban-Repo names the target repo.
			AllowedHeaders: jsii.Strings(
				"content-type", "mcp-session-id", "mcp-protocol-version",
				"authorization", "x-cainban-repo",
				"x-amz-date", "x-amz-security-token", "x-amz-content-sha256",
			),
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
		Description: jsii.String("MCP Streamable-HTTP endpoint — AWS_IAM edge auth + in-Lambda Cognito JWT (Phase 3, authenticated)"),
	})
	awscdk.NewCfnOutput(stack, jsii.String("TableName"), &awscdk.CfnOutputProps{
		Value:       table.TableName(),
		Description: jsii.String("DynamoDB table backing cainban"),
	})
	awscdk.NewCfnOutput(stack, jsii.String("UserPoolId"), &awscdk.CfnOutputProps{
		Value:       userPool.UserPoolId(),
		Description: jsii.String("Cognito user pool issuing MCP JWTs"),
	})
	awscdk.NewCfnOutput(stack, jsii.String("UserPoolClientId"), &awscdk.CfnOutputProps{
		Value:       userPoolClient.UserPoolClientId(),
		Description: jsii.String("Cognito app client id (JWT audience)"),
	})

	return stack
}
