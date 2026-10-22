package main

import (
	"strings"

	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsapigatewayv2"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsapigatewayv2integrations"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsdynamodb"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsiam"
	"github.com/aws/aws-cdk-go/awscdk/v2/awslambda"
	"github.com/aws/aws-cdk-go/awscdk/v2/awslogs"
	"github.com/aws/aws-cdk-go/awscdk/v2/awssecretsmanager"
	"github.com/aws/constructs-go/constructs/v10"
	"github.com/aws/jsii-runtime-go"
)

// CainbanStackProps configures the cainban stack.
type CainbanStackProps struct {
	awscdk.StackProps
}

// NewCainbanStack builds the GitHub-only serverless stack.
//
// AUTH MODEL (GitHub-only): cainban no longer uses Cognito. Identity is the
// user's own GitHub token (validated by the Lambda via GET /user); authorization
// is decided server-side with the GitHub App installation token (the App's repo
// permissions are read-only Metadata + Administration). There is no Cognito user
// pool, no managed JWT authorizer, and no pre-token trigger. Every MCP route is
// HttpNoneAuthorizer and the Lambda enforces auth itself, so the 401 carries the
// RFC 9728 resource_metadata challenge. See docs/rfc-github-app-authorization.md.
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

	// --- Secrets Manager: Signing secrets ---------------------------------
	//
	// Badge and widget signing secrets (HMAC-SHA256). Created out of band:
	//   aws secretsmanager create-secret --name cainban/badge-secret --secret-string "$(openssl rand -hex 32)"
	//   aws secretsmanager create-secret --name cainban/widget-secret --secret-string "$(openssl rand -hex 32)"
	// RETAIN so cdk destroy doesn't delete them.
	badgeSecret := awssecretsmanager.Secret_FromSecretNameV2(stack, jsii.String("BadgeSecret"), jsii.String("cainban/badge-secret"))
	widgetSecret := awssecretsmanager.Secret_FromSecretNameV2(stack, jsii.String("WidgetSecret"), jsii.String("cainban/widget-secret"))

	// --- GitHub-only authentication + authorization -----------------------
	//
	// IDENTITY: the client obtains a GitHub token (OAuth Device Flow or a
	// fine-grained PAT) and sends `Authorization: Bearer <github_token>`. The
	// Lambda validates it with GET /user — invalid/expired tokens are 401. The
	// token is used ONLY to establish WHO the caller is.
	//
	// AUTHORIZATION: decided server-side with the GitHub App installation token
	// (the cainban/github-app secret). A caller is authorized for owner/repo when
	// the App is installed on the repo AND the caller is an org member or a repo
	// collaborator with read+ access. The user's token is never used for the
	// permission check, so it needs no repo scope.
	//
	// Two secrets back this (both created by the operator; see
	// docs/github-app-setup.md and docs/github-oauth-setup.md):
	//   - cainban/github-app   : {app_id, private_key, client_id, client_secret}
	//   - cainban/github-oauth  : {client_id, client_secret}  (device-flow endpoint)
	githubAppSecret := awssecretsmanager.Secret_FromSecretNameV2(stack, jsii.String("GitHubAppSecret"), jsii.String("cainban/github-app"))
	githubOauthSecret := awssecretsmanager.Secret_FromSecretNameV2(stack, jsii.String("GitHubOauthSecret"), jsii.String("cainban/github-oauth"))

	// Placeholder-safe context reader: returns the -c value if present, else the
	// supplied placeholder default so `cdk synth` runs before the real values are
	// known. NEVER used for secrets — those live in Secrets Manager.
	ctxOr := func(key, def string) string {
		if v := stack.Node().TryGetContext(jsii.String(key)); v != nil {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
		return def
	}

	// --- MCP Lambda function ----------------------------------------------
	//
	// provided.al2023 + arm64 running the Go bootstrap built from
	// cmd/cainban-lambda. The DynamoDB backend is selected via env vars; auth is
	// GitHub-only (no Cognito issuer/audience).
	fn := awslambda.NewFunction(stack, jsii.String("McpFunction"), &awslambda.FunctionProps{
		FunctionName: jsii.String("cainban-mcp"),
		Runtime:      awslambda.Runtime_PROVIDED_AL2023(),
		Architecture: awslambda.Architecture_ARM_64(),
		Handler:      jsii.String("bootstrap"),
		MemorySize:   jsii.Number(256),
		Timeout:      awscdk.Duration_Seconds(jsii.Number(30)),
		// Frugality AND abuse guard: cap concurrency on the hot MCP path. This
		// cap is LOAD-BEARING — the MCP data routes use HttpNoneAuthorizer and
		// enforce auth in the Lambda (so the 401 can carry the RFC 9728
		// resource_metadata challenge), so UNAUTHENTICATED requests reach this
		// function. 10 bounds the blast radius of an anonymous flood.
		ReservedConcurrentExecutions: jsii.Number(10),
		Environment: &map[string]*string{
			"CAINBAN_BACKEND":    jsii.String("dynamodb"),
			"CAINBAN_DDB_TABLE":  table.TableName(),
			"CAINBAN_DDB_REGION": stack.Region(),
			// GitHub App authorization: the Lambda builds the App verifier from
			// this secret and authorizes every repo with the App installation
			// token. REQUIRED — buildResolver fails cold start without it.
			"CAINBAN_GITHUB_APP_SECRET_NAME": githubAppSecret.SecretName(),
			// Badge and widget signing secrets (HMAC-SHA256). Lambda reads them
			// from Secrets Manager at startup; the name is passed via env var.
			"CAINBAN_BADGE_SECRET_NAME":  badgeSecret.SecretName(),
			"CAINBAN_WIDGET_SECRET_NAME": widgetSecret.SecretName(),
			// GitHub OAuth secret for the device-flow onboarding endpoint.
			"CAINBAN_GITHUB_OAUTH_SECRET_NAME": githubOauthSecret.SecretName(),
		},
		// Build the Go binary into the asset directory first (make lambda) — see
		// infra/README.md — then `cdk synth`/`deploy`.
		Code: awslambda.Code_FromAsset(jsii.String("../.build/lambda"), nil),
	})

	// Least-privilege: grant ONLY the DynamoDB actions the store actually calls.
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

	// Grant read access to the secrets the MCP Lambda loads at startup.
	badgeSecret.GrantRead(fn.Role(), nil)
	widgetSecret.GrantRead(fn.Role(), nil)
	githubOauthSecret.GrantRead(fn.Role(), nil)
	githubAppSecret.GrantRead(fn.Role(), nil)

	// --- API Gateway v2 HTTP API (MCP) ------------------------------------
	//
	// An HTTP API (apigatewayv2 — cheaper than REST, no VPC/NAT) with NO managed
	// authorizer: auth is GitHub-only and enforced IN the Lambda (it validates
	// the GitHub token and authorizes the repo via the App). The managed Cognito
	// JWT authorizer is gone — a Cognito authorizer cannot validate a GitHub
	// token, and in-Lambda enforcement is required anyway so the 401 carries the
	// RFC 9728 `resource_metadata` challenge (HTTP APIs cannot customize an
	// authorizer's 401 headers).
	mcpIntegration := awsapigatewayv2integrations.NewHttpLambdaIntegration(
		jsii.String("McpIntegration"),
		fn,
		&awsapigatewayv2integrations.HttpLambdaIntegrationProps{
			PayloadFormatVersion: awsapigatewayv2.PayloadFormatVersion_VERSION_2_0(),
		},
	)

	// CORS allowed origins (RFC M6): derived from the SPA/connect origins in
	// context rather than a wildcard. The data plane is gated by in-Lambda auth,
	// but a wildcard origin is needless attack surface.
	corsOrigins := deriveCorsOrigins(ctxOr("spaCallbackUrls", "http://localhost:5173/,https://cainban.awiant.tech/"))

	mcpAPI := awsapigatewayv2.NewHttpApi(stack, jsii.String("McpHttpApi"), &awsapigatewayv2.HttpApiProps{
		ApiName:     jsii.String("cainban-mcp"),
		Description: jsii.String("MCP Streamable-HTTP endpoint — HTTP API; GitHub-only auth enforced IN the Lambda (Authorization: Bearer <github_token>, no SigV4, no Cognito)."),
		CorsPreflight: &awsapigatewayv2.CorsPreflightOptions{
			AllowOrigins: corsOrigins,
			AllowMethods: &[]awsapigatewayv2.CorsHttpMethod{awsapigatewayv2.CorsHttpMethod_ANY},
			AllowHeaders: jsii.Strings(
				"content-type", "mcp-session-id", "mcp-protocol-version",
				"authorization", "x-cainban-repo",
			),
		},
	})

	// Route every MCP request (root + any sub-path + preflight) to the Lambda
	// with HttpNoneAuthorizer: the request reaches the Lambda, which enforces
	// GitHub-only auth itself (HandlerWithAuthChallenge). A missing/invalid token
	// is a 401 whose WWW-Authenticate carries resource_metadata; an ungranted
	// repo is a 403 — both decided in the Lambda. verify-deploy.sh asserts the
	// 401 carries resource_metadata so this cannot silently regress.
	noneAuth := awsapigatewayv2.NewHttpNoneAuthorizer()
	for _, p := range []string{"/", "/{proxy+}"} {
		mcpAPI.AddRoutes(&awsapigatewayv2.AddRoutesOptions{
			Path:        jsii.String(p),
			Methods:     &[]awsapigatewayv2.HttpMethod{awsapigatewayv2.HttpMethod_ANY},
			Integration: mcpIntegration,
			Authorizer:  noneAuth,
		})
		mcpAPI.AddRoutes(&awsapigatewayv2.AddRoutesOptions{
			Path:        jsii.String(p),
			Methods:     &[]awsapigatewayv2.HttpMethod{awsapigatewayv2.HttpMethod_OPTIONS},
			Integration: mcpIntegration,
			Authorizer:  noneAuth,
		})
	}

	// --- RFC 9728 protected-resource metadata route (MCP OAuth step a) ---
	//
	// GET /.well-known/oauth-protected-resource is PUBLIC so a spec-compliant MCP
	// client can discover cainban's auth before holding a token. Served by the
	// MCP Lambda (mcp.PublicMux), which routes only this exact path publicly.
	mcpAPI.AddRoutes(&awsapigatewayv2.AddRoutesOptions{
		Path:        jsii.String("/.well-known/oauth-protected-resource"),
		Methods:     &[]awsapigatewayv2.HttpMethod{awsapigatewayv2.HttpMethod_GET},
		Integration: mcpIntegration,
		Authorizer:  noneAuth,
	})
	// cainban's own MCP client auto-config document — PUBLIC. Returns the MCP URL
	// and per-client config snippets so an agent/connect page can self-configure.
	mcpAPI.AddRoutes(&awsapigatewayv2.AddRoutesOptions{
		Path:        jsii.String("/.well-known/mcp-client-config"),
		Methods:     &[]awsapigatewayv2.HttpMethod{awsapigatewayv2.HttpMethod_GET},
		Integration: mcpIntegration,
		Authorizer:  noneAuth,
	})

	// The MCP API's canonical URL: the RFC 8707 resource indicator, the
	// `resource` field of the RFC 9728 metadata doc, and the base of the
	// WWW-Authenticate resource_metadata pointer. Added post-API as a
	// lazily-resolved token (the function is created before the API).
	fn.AddEnvironment(jsii.String("CAINBAN_MCP_RESOURCE"), mcpAPI.Url(), nil)

	// --- Explicit CloudWatch log group -----------------------------------
	awslogs.NewLogGroup(stack, jsii.String("McpLogGroup"), &awslogs.LogGroupProps{
		LogGroupName:  jsii.String("/aws/lambda/cainban-mcp"),
		Retention:     awslogs.RetentionDays_ONE_MONTH,
		RemovalPolicy: awscdk.RemovalPolicy_DESTROY,
	})

	// --- Real-time WebSocket API -----------------------------------------
	//
	// API Gateway terminates the socket and invokes a dedicated WS Lambda on the
	// three route keys ($connect/$disconnect/$default). That Lambda manages the
	// connection REGISTRY in the "cainban" table; the real-time PUSH happens from
	// the MCP Lambda's broadcaster via the API Gateway Management API.
	//
	// Auth: the browser WebSocket API cannot set headers, so the GitHub token +
	// target repo arrive as $connect query-string params and are validated by
	// the SAME GitHub-only resolver the MCP path uses (identity via GET /user,
	// authorization via the App installation token) BEFORE any connection row is
	// stored. A token that does not grant the repo is rejected at $connect.
	wsFn := awslambda.NewFunction(stack, jsii.String("WsFunction"), &awslambda.FunctionProps{
		FunctionName: jsii.String("cainban-ws"),
		Runtime:      awslambda.Runtime_PROVIDED_AL2023(),
		Architecture: awslambda.Architecture_ARM_64(),
		Handler:      jsii.String("bootstrap"),
		MemorySize:   jsii.Number(128),
		Timeout:      awscdk.Duration_Seconds(jsii.Number(10)),
		// Frugality + abuse guard: cap concurrency so a connection flood cannot
		// fan out Lambda + on-demand DynamoDB cost unbounded.
		ReservedConcurrentExecutions: jsii.Number(10),
		Environment: &map[string]*string{
			"CAINBAN_BACKEND":    jsii.String("dynamodb"),
			"CAINBAN_DDB_TABLE":  table.TableName(),
			"CAINBAN_DDB_REGION": stack.Region(),
			// Same GitHub App secret as the MCP Lambda — $connect authorizes the
			// same way (identity via the GitHub token, repo via the App).
			"CAINBAN_GITHUB_APP_SECRET_NAME": githubAppSecret.SecretName(),
		},
		Code: awslambda.Code_FromAsset(jsii.String("../.build/ws"), nil),
	})

	// Least-privilege DynamoDB for the WS Lambda: register/resolve/deregister
	// connection rows only.
	wsFn.AddToRolePolicy(awsiam.NewPolicyStatement(&awsiam.PolicyStatementProps{
		Effect: awsiam.Effect_ALLOW,
		Actions: jsii.Strings(
			"dynamodb:GetItem",
			"dynamodb:PutItem",
			"dynamodb:DeleteItem",
		),
		Resources: &[]*string{table.TableArn()},
	}))
	// The WS Lambda also builds the App verifier at $connect.
	githubAppSecret.GrantRead(wsFn.Role(), nil)

	wsConnectIntegration := awsapigatewayv2integrations.NewWebSocketLambdaIntegration(
		jsii.String("WsConnectIntegration"), wsFn, nil)
	wsDisconnectIntegration := awsapigatewayv2integrations.NewWebSocketLambdaIntegration(
		jsii.String("WsDisconnectIntegration"), wsFn, nil)
	wsDefaultIntegration := awsapigatewayv2integrations.NewWebSocketLambdaIntegration(
		jsii.String("WsDefaultIntegration"), wsFn, nil)

	wsAPI := awsapigatewayv2.NewWebSocketApi(stack, jsii.String("WsApi"), &awsapigatewayv2.WebSocketApiProps{
		ApiName:     jsii.String("cainban-ws"),
		Description: jsii.String("Real-time task-activity push — API Gateway WebSocket API. $connect authenticates via query-string GitHub token+repo (identity via /user, repo via the App installation token)."),
		ConnectRouteOptions: &awsapigatewayv2.WebSocketRouteOptions{
			Integration: wsConnectIntegration,
		},
		DisconnectRouteOptions: &awsapigatewayv2.WebSocketRouteOptions{
			Integration: wsDisconnectIntegration,
		},
		DefaultRouteOptions: &awsapigatewayv2.WebSocketRouteOptions{
			Integration:    wsDefaultIntegration,
			ReturnResponse: jsii.Bool(true),
		},
	})

	wsStage := awsapigatewayv2.NewWebSocketStage(stack, jsii.String("WsStage"), &awsapigatewayv2.WebSocketStageProps{
		WebSocketApi: wsAPI,
		StageName:    jsii.String("prod"),
		AutoDeploy:   jsii.Bool(true),
	})

	// Let the MCP Lambda PUSH to connected clients.
	wsAPI.GrantManageConnections(fn)
	// Tell the MCP Lambda where to push (the https:// Management API endpoint).
	fn.AddEnvironment(jsii.String("CAINBAN_WS_API_ENDPOINT"), wsStage.CallbackUrl(), nil)

	awslogs.NewLogGroup(stack, jsii.String("WsLogGroup"), &awslogs.LogGroupProps{
		LogGroupName:  jsii.String("/aws/lambda/cainban-ws"),
		Retention:     awslogs.RetentionDays_ONE_MONTH,
		RemovalPolicy: awscdk.RemovalPolicy_DESTROY,
	})

	// --- Outputs ----------------------------------------------------------
	awscdk.NewCfnOutput(stack, jsii.String("McpApiUrl"), &awscdk.CfnOutputProps{
		Value:       mcpAPI.Url(),
		Description: jsii.String("MCP Streamable-HTTP endpoint — HTTP API, GitHub-only auth enforced in the Lambda. Client sends Authorization: Bearer <github_token>."),
	})
	awscdk.NewCfnOutput(stack, jsii.String("McpProtectedResourceMetadataUrl"), &awscdk.CfnOutputProps{
		Value: awscdk.Fn_Join(jsii.String(""), &[]*string{
			mcpAPI.Url(),
			jsii.String(".well-known/oauth-protected-resource"),
		}),
		Description: jsii.String("RFC 9728 Protected Resource Metadata — PUBLIC (AuthorizationType NONE)."),
	})
	awscdk.NewCfnOutput(stack, jsii.String("WsApiUrl"), &awscdk.CfnOutputProps{
		Value:       wsStage.Url(),
		Description: jsii.String("Real-time WebSocket endpoint (wss://). Connect with ?repo=<owner/repo>&token=<github_token>; validated at $connect. The SPA uses this as VITE_WS_URL."),
	})
	awscdk.NewCfnOutput(stack, jsii.String("WsManagementEndpoint"), &awscdk.CfnOutputProps{
		Value:       wsStage.CallbackUrl(),
		Description: jsii.String("API Gateway Management API callback endpoint (https://) the MCP Lambda posts real-time frames to (CAINBAN_WS_API_ENDPOINT)."),
	})
	awscdk.NewCfnOutput(stack, jsii.String("TableName"), &awscdk.CfnOutputProps{
		Value:       table.TableName(),
		Description: jsii.String("DynamoDB table backing cainban"),
	})
	awscdk.NewCfnOutput(stack, jsii.String("GitHubDeviceFlowUri"), &awscdk.CfnOutputProps{
		Value:       awscdk.Fn_Join(jsii.String(""), &[]*string{mcpAPI.Url(), jsii.String("auth/github/device")}),
		Description: jsii.String("GitHub Device Flow endpoint: POST to initiate device auth, then poll for a token. Clients send that token as the MCP bearer."),
	})

	// --- Amplify Hosting service role ------------------------------------
	//
	// The IAM role AWS Amplify Hosting assumes to build the connect-page SPA
	// (web/). Scoped to CloudWatch Logs for the Amplify build log groups only —
	// NOT the near-admin AdministratorAccess-Amplify managed policy. Attach after
	// deploy (console App settings → General → Service role, or
	// `aws amplify update-app --iam-service-role-arn <this output>`).
	amplifyServiceRole := awsiam.NewRole(stack, jsii.String("AmplifyServiceRole"), &awsiam.RoleProps{
		RoleName:    jsii.String("cainban-amplify-service-role"),
		AssumedBy:   awsiam.NewServicePrincipal(jsii.String("amplify.amazonaws.com"), nil),
		Description: jsii.String("Service role AWS Amplify Hosting assumes to build/deploy the cainban connect-page SPA (scoped to CloudWatch Logs)."),
		InlinePolicies: &map[string]awsiam.PolicyDocument{
			"cainban-amplify-build-logs": awsiam.NewPolicyDocument(&awsiam.PolicyDocumentProps{
				Statements: &[]awsiam.PolicyStatement{
					awsiam.NewPolicyStatement(&awsiam.PolicyStatementProps{
						Effect: awsiam.Effect_ALLOW,
						Actions: jsii.Strings(
							"logs:CreateLogGroup",
							"logs:CreateLogStream",
							"logs:PutLogEvents",
							"logs:DescribeLogGroups",
							"logs:DescribeLogStreams",
						),
						Resources: jsii.Strings(
							"arn:aws:logs:*:*:log-group:/aws/amplify/*",
							"arn:aws:logs:*:*:log-group:/aws/amplify/*:log-stream:*",
						),
					}),
				},
			}),
		},
	})
	awscdk.NewCfnOutput(stack, jsii.String("AmplifyServiceRoleArn"), &awscdk.CfnOutputProps{
		Value:       amplifyServiceRole.RoleArn(),
		Description: jsii.String("IAM service role ARN for AWS Amplify Hosting — attach to the Amplify app."),
	})

	return stack
}

// deriveCorsOrigins turns a comma-separated list of SPA callback URLs (the
// spaCallbackUrls context value, e.g. "http://localhost:5173/,https://app.example/")
// into the set of scheme+host origins for the API Gateway CORS AllowOrigins
// (RFC M6). It strips any path/query and a trailing slash so each entry is a
// bare origin, de-duplicates, and preserves order. If nothing usable is parsed
// it falls back to "*" so a misconfigured context never breaks the preflight
// entirely (the data plane is still auth-gated in-Lambda).
func deriveCorsOrigins(csv string) *[]*string {
	seen := map[string]bool{}
	var origins []*string
	for _, raw := range strings.Split(csv, ",") {
		u := strings.TrimSpace(raw)
		if u == "" {
			continue
		}
		// Reduce to scheme://host[:port] by cutting at the first '/' after the
		// scheme's "://".
		origin := u
		if i := strings.Index(u, "://"); i >= 0 {
			rest := u[i+3:]
			if j := strings.IndexByte(rest, '/'); j >= 0 {
				origin = u[:i+3+j]
			}
		}
		origin = strings.TrimRight(origin, "/")
		if origin == "" || seen[origin] {
			continue
		}
		seen[origin] = true
		origins = append(origins, jsii.String(origin))
	}
	if len(origins) == 0 {
		return jsii.Strings("*")
	}
	return &origins
}
