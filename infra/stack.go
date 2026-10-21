package main

import (
	"strings"

	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsapigatewayv2"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsapigatewayv2authorizers"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsapigatewayv2integrations"
	"github.com/aws/aws-cdk-go/awscdk/v2/awscognito"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsdynamodb"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsiam"
	"github.com/aws/aws-cdk-go/awscdk/v2/awslambda"
	"github.com/aws/aws-cdk-go/awscdk/v2/awslogs"
	"github.com/aws/aws-cdk-go/awscdk/v2/awssecretsmanager"
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

	// --- Secrets Manager: Signing secrets ---------------------------------
	//
	// Badge and widget signing secrets (HMAC-SHA256). Created out of band:
	//   aws secretsmanager create-secret --name cainban/badge-secret --secret-string "$(openssl rand -hex 32)"
	//   aws secretsmanager create-secret --name cainban/widget-secret --secret-string "$(openssl rand -hex 32)"
	// RETAIN so cdk destroy doesn't delete them.
	badgeSecret := awssecretsmanager.Secret_FromSecretNameV2(stack, jsii.String("BadgeSecret"), jsii.String("cainban/badge-secret"))
	widgetSecret := awssecretsmanager.Secret_FromSecretNameV2(stack, jsii.String("WidgetSecret"), jsii.String("cainban/widget-secret"))

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

	// --- GitHub-only authentication (direct token) ------------------------
	//
	// Cognito does NOT natively support GitHub as a federated identity provider
	// (valid ProviderTypes are: Facebook, Google, LoginWithAmazon, OIDC, SAML,
	// SignInWithApple — GitHub is not in this list and the API rejects it).
	//
	// Instead of federation, cainban accepts GitHub access tokens DIRECTLY:
	//   - Client obtains a GitHub token (via OAuth Device Flow or web flow)
	//   - Client sends `Authorization: Bearer <github_token>` to the MCP API
	//   - Lambda validates the token with GET /user (confirms token is valid)
	//   - Lambda uses the same token for repo permission checks (ghperm)
	//
	// This is SIMPLER than federation because:
	//   - No Cognito federation complexity (which doesn't work with GitHub anyway)
	//   - User's GitHub token is directly available for permission checks
	//   - No token translation or claim mapping needed
	//   - Works with any client that can do GitHub OAuth
	//
	// The Lambda validates GitHub tokens by calling the GitHub API; invalid/expired
	// tokens return 401. Repo authorization is checked per-request via ghperm
	// (CAINBAN_USE_GITHUB_PERMISSIONS=true).
	//
	// GitHub OAuth credentials (for the device flow endpoint) are stored in
	// Secrets Manager (cainban/github-oauth). Create it out of band:
	//   aws secretsmanager create-secret --name cainban/github-oauth \
	//     --secret-string '{"client_id":"<OAuth App client id>","client_secret":"<secret>"}'

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

	// GitHub OAuth credentials for the device flow endpoint (Lambda reads these
	// to initiate device auth and exchange codes). Created by operator.
	githubOauthSecret := awssecretsmanager.Secret_FromSecretNameV2(stack, jsii.String("GitHubOauthSecret"), jsii.String("cainban/github-oauth"))

	// With direct GitHub tokens, app clients use COGNITO as the only provider
	// (for any local Cognito users), OR accept GitHub tokens directly at the API.
	// The Hosted UI shows Cognito login only; GitHub auth bypasses it entirely.
	cognitoSupported := []awscognito.UserPoolClientIdentityProvider{
		awscognito.UserPoolClientIdentityProvider_COGNITO(),
	}

	// --- Hosted UI domain (free Cognito prefix domain) ------------------
	//
	// A free Cognito-hosted prefix domain (no custom-domain cost, no ACM cert).
	// The prefix must be globally unique across the region; a deterministic
	// account-derived suffix avoids collisions without needing a real tenant.
	// The Hosted UI is where signInWithRedirect sends the browser for Cognito
	// native login (username/password). GitHub auth bypasses the Hosted UI
	// entirely — users auth directly with GitHub and send the token to the API.
	hostedDomain := userPool.AddDomain(jsii.String("HostedUiDomain"), &awscognito.UserPoolDomainOptions{
		CognitoDomain: &awscognito.CognitoDomainOptions{
			// Deterministic, globally-unique-per-region prefix derived from the
			// account id so it does not collide with another AWS account's pool.
			// Keep the old prefix (with emawiant) to avoid domain replacement.
			DomainPrefix: awscdk.Fn_Join(jsii.String("-"), &[]*string{
				jsii.String("cainban-emawiant"),
				stack.Account(),
			}),
		},
	})

	// The Hosted UI base URL and the exact GitHub redirect (reply) URI.
	hostedUiBaseURL := hostedDomain.BaseUrl(nil)
	_ = awscdk.Fn_Join(jsii.String(""), &[]*string{hostedUiBaseURL, jsii.String("/oauth2/idpresponse")})

	// --- SPA app client (public, PKCE) ----------------------------------
	//
	// A SEPARATE public client for the browser SPA — the machine McpClient is
	// left untouched. No secret is generated (public PKCE client). It uses the
	// authorization-code grant with openid/email/profile. With direct GitHub
	// auth, this client supports Cognito native login only; GitHub users bypass
	// Cognito entirely and send their GitHub token directly to the API.
	// Callback/logout URLs come from context with a localhost dev default +
	// a placeholder for the Amplify URL (filled after first Amplify deploy —
	// see web/README.md).
	spaCallbacks := splitCsv(ctxOr("spaCallbackUrls", "http://localhost:5173/,https://localhost/"))
	spaLogouts := splitCsv(ctxOr("spaLogoutUrls", "http://localhost:5173/,https://localhost/"))

	spaClient := userPool.AddClient(jsii.String("SpaClient"), &awscognito.UserPoolClientOptions{
		UserPoolClientName: jsii.String("cainban-spa-client"),
		// Public SPA client: PKCE, no secret.
		GenerateSecret: jsii.Bool(false),
		// Token lifetimes. Cognito defaults access/ID tokens to 1h; that made
		// cainban re-prompt roughly hourly because the cached grant stored only
		// a relative `expires_in` (no absolute expiry) and the client did not
		// refresh. Raise access/ID to the Cognito max (24h) so day-to-day use
		// does not churn, and make the 30d refresh window explicit (not an
		// implicit default) so it is not silently changed. Lifetime only — no
		// change to flows, scopes, audiences, or the pre-token trigger.
		AccessTokenValidity:  awscdk.Duration_Hours(jsii.Number(24)),
		IdTokenValidity:      awscdk.Duration_Hours(jsii.Number(24)),
		RefreshTokenValidity: awscdk.Duration_Days(jsii.Number(30)),
		OAuth: &awscognito.OAuthSettings{
			Flows: &awscognito.OAuthFlows{
				AuthorizationCodeGrant: jsii.Bool(true),
			},
			Scopes: &[]awscognito.OAuthScope{
				awscognito.OAuthScope_OPENID(),
				awscognito.OAuthScope_EMAIL(),
				awscognito.OAuthScope_PROFILE(),
			},
			CallbackUrls: &spaCallbacks,
			LogoutUrls:   &spaLogouts,
		},
		SupportedIdentityProviders: &cognitoSupported,
	})

	// --- MCP CLI app client (public, PKCE) — Claude Code ----------------
	//
	// A DEDICATED public PKCE client for CLI MCP clients (Claude Code), separate
	// from both the machine McpClient (USER_PASSWORD/SRP) and the browser SPA
	// client — so the CLI's loopback callback URLs never mingle with the SPA's
	// hosted callbacks. No secret (public PKCE client). Authorization-code grant,
	// openid/email/profile, Cognito native login. GitHub users bypass Cognito
	// entirely — they use GitHub Device Flow and send the GitHub token directly.
	//
	// Claude Code's OAuth callback is a LOOPBACK redirect the CLI opens on the
	// user's machine: http://localhost:<PORT>/callback (and the 127.0.0.1
	// literal). Claude Code picks an ephemeral port at flow time, so the exact
	// port is not known in advance; there is a documented open issue where Claude
	// Code sends a ported loopback while some providers reject anything but the
	// registered value. Cognito requires each callback URL to be registered
	// exactly (it does NOT honor RFC 8252 port-agnostic loopback matching), so we
	// register the ports Claude Code commonly uses plus a portless loopback, on
	// both localhost and 127.0.0.1. Operators can add more via
	// -c mcpCliCallbackUrls=... (CSV) if a build uses a different port. The exact
	// resolved behavior is documented in docs/mcp-oauth-setup.md.
	//
	// Kiro (kiro-cli / the Kiro Crew dashboard Authorize banner) uses a DIFFERENT
	// loopback path from Claude Code: its callback listener serves /oauth/callback
	// (Claude Code uses /callback). With oauth.redirectUri unset Kiro picks a
	// random port, which Cognito rejects (redirect_mismatch) — so cainban's setup
	// pins oauth.redirectUri=http://127.0.0.1:3334/oauth/callback, and that exact
	// pair MUST stay registered here or a flagless deploy reverts it and breaks the
	// Kiro Authorize flow. See docs/rfc-mcp-oauth-callback-pinning.md.
	mcpCliCallbacks := splitCsv(ctxOr("mcpCliCallbackUrls", strings.Join([]string{
		"http://localhost:3118/callback",
		"http://127.0.0.1:3118/callback",
		"http://localhost:3334/oauth/callback",
		"http://127.0.0.1:3334/oauth/callback",
		"http://localhost:41842/callback",
		"http://127.0.0.1:41842/callback",
		"http://localhost/callback",
		"http://127.0.0.1/callback",
	}, ",")))
	mcpCliLogouts := splitCsv(ctxOr("mcpCliLogoutUrls", strings.Join([]string{
		"http://localhost:3118/callback",
		"http://127.0.0.1:3118/callback",
	}, ",")))

	mcpCliClient := userPool.AddClient(jsii.String("McpCliClient"), &awscognito.UserPoolClientOptions{
		UserPoolClientName: jsii.String("cainban-mcp-cli"),
		// Public PKCE client: no secret. Claude Code / Kiro perform PKCE + the
		// code exchange + refresh themselves; cainban only validates the token.
		GenerateSecret: jsii.Bool(false),
		// Token lifetimes — see the SpaClient note above. 1h access tokens made
		// the Kiro/kiro-cli loopback flow re-prompt hourly on a headless gateway
		// (no browser to pop, no consent banner, grant goes stale). 24h access/ID
		// + explicit 30d refresh stops the churn; the client still refreshes via
		// the advertised token_endpoint (grant_types_supported includes
		// refresh_token). Lifetime only — audiences/routes/trigger unchanged.
		AccessTokenValidity:  awscdk.Duration_Hours(jsii.Number(24)),
		IdTokenValidity:      awscdk.Duration_Hours(jsii.Number(24)),
		RefreshTokenValidity: awscdk.Duration_Days(jsii.Number(30)),
		OAuth: &awscognito.OAuthSettings{
			Flows: &awscognito.OAuthFlows{
				AuthorizationCodeGrant: jsii.Bool(true),
			},
			Scopes: &[]awscognito.OAuthScope{
				awscognito.OAuthScope_OPENID(),
				awscognito.OAuthScope_EMAIL(),
				awscognito.OAuthScope_PROFILE(),
			},
			CallbackUrls: &mcpCliCallbacks,
			LogoutUrls:   &mcpCliLogouts,
		},
		SupportedIdentityProviders: &cognitoSupported,
	})

	// Both app clients issue tokens against this pool: the machine/CLI MCP
	// client AND the browser SPA client. The JWT authorizers and the in-Lambda
	// validators must accept EITHER `aud`, so the accepted-audience value is the
	// comma-separated pair. (CAINBAN_AUTH_AUDIENCE is parsed as a CSV by the
	// Lambdas; the API GW authorizers take the two ids as a list directly.)
	authAudiencesCsv := awscdk.Fn_Join(jsii.String(","), &[]*string{
		userPoolClient.UserPoolClientId(),
		spaClient.UserPoolClientId(),
		mcpCliClient.UserPoolClientId(),
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
	// NOTE: With GitHub-native permissions enabled (CAINBAN_USE_GITHUB_PERMISSIONS=true),
	// authorization is checked at runtime via the GitHub API (see src/systems/ghperm).
	// The pre-token trigger still surfaces the GitHub identity (github_user, github_token)
	// for the resolver, but repo authorization is no longer stored in custom:repos.
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
		// Frugality: cap concurrency so a burst of token issuance (or abuse)
		// cannot fan out Lambda cost. This is a fast per-request trigger; 5 is
		// ample at dev scale.
		ReservedConcurrentExecutions: jsii.Number(5),
		// No DynamoDB access needed — the trigger only reflects Cognito
		// attributes into token claims. GitHub-native permissions are checked
		// at request time by the resolver, not at token generation.
		Environment: &map[string]*string{},
		Code:        awslambda.Code_FromAsset(jsii.String("../.build/pretoken"), nil),
	})

	// Attach as the pool's PreTokenGeneration trigger. LambdaVersion V2_0 lets
	// the handler write the overrides to BOTH the ID token AND the ACCESS token
	// (ClaimsAndScopeOverrideDetails.{IDTokenGeneration,AccessTokenGeneration}).
	// The MCP API's bearer is the ACCESS token — a spec-compliant MCP client
	// (Claude Code) sends the access token to the resource server — so the
	// `repos` claim MUST be on the access token or the validator sees no grants
	// and returns 403. V1_0 could only write ID-token claims, which is why the
	// access token carried no repos claim. Values remain the string-valued shape
	// the validator's reposClaim decoder consumes. AddTrigger also grants Cognito
	// permission to invoke the function (a resource-based policy).
	userPool.AddTrigger(
		awscognito.UserPoolOperation_PRE_TOKEN_GENERATION_CONFIG(),
		preTokenFn,
		awscognito.LambdaVersion_V2_0,
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
		// Frugality AND abuse guard: cap concurrency on the hot MCP path so a
		// runaway agent loop or traffic spike cannot scale out Lambda +
		// on-demand DynamoDB cost unbounded. This cap is LOAD-BEARING now that
		// the MCP data routes use HttpNoneAuthorizer and enforce auth in the
		// Lambda (so the 401 can carry the RFC 9728 resource_metadata challenge):
		// UNAUTHENTICATED requests now reach this function instead of being
		// absorbed for free by the managed JWT authorizer, so an anonymous flood
		// consumes invocations. 10 bounds that blast radius (cost cannot exceed
		// 10 concurrent) while staying generous for a dev-scale shared board.
		// Do NOT remove this cap when revisiting the authorizer model.
		ReservedConcurrentExecutions: jsii.Number(10),
		Environment: &map[string]*string{
			"CAINBAN_BACKEND":    jsii.String("dynamodb"),
			"CAINBAN_DDB_TABLE":  table.TableName(),
			"CAINBAN_DDB_REGION": stack.Region(),
			// Phase 3 auth config: signature-first JWT validation against this
			// Cognito user pool. The JWKS URL is derived from the issuer in the
			// handler when unset; audience is the app client id.
			"CAINBAN_AUTH_ISSUER":   issuer,
			"CAINBAN_AUTH_AUDIENCE": authAudiencesCsv,
			// GitHub-native permissions: check GitHub API for repo access at
			// runtime (cached 5 minutes) using the user's own GitHub token.
			// See src/systems/ghperm and docs/rfc-github-permission-cache.md.
			"CAINBAN_USE_GITHUB_PERMISSIONS": jsii.String("true"),
			// Badge and widget signing secrets (HMAC-SHA256). Lambda reads them
			// from Secrets Manager at startup; the name is passed via env var.
			"CAINBAN_BADGE_SECRET_NAME":  badgeSecret.SecretName(),
			"CAINBAN_WIDGET_SECRET_NAME": widgetSecret.SecretName(),
			// GitHub OAuth secret for device flow authentication.
			"CAINBAN_GITHUB_OAUTH_SECRET_NAME": githubOauthSecret.SecretName(),
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

	// Grant read access to badge and widget signing secrets.
	badgeSecret.GrantRead(fn.Role(), nil)
	widgetSecret.GrantRead(fn.Role(), nil)

	// Grant read access to GitHub OAuth secret for device flow.
	githubOauthSecret.GrantRead(fn.Role(), nil)

	// --- API Gateway v2 HTTP API + Cognito JWT authorizer (MCP) ----------
	//
	// REPLACES the Phase 3 Function URL (AuthType AWS_IAM). A live smoke-test
	// found a HEADER COLLISION: with an AWS_IAM Function URL the caller's SigV4
	// signature occupies the Authorization header, but the in-Lambda validator
	// ALSO reads the Cognito JWT from Authorization — one request cannot carry
	// both, so a correctly-signed request with a valid ID token still 401s.
	//
	// The fix is an HTTP API (apigatewayv2 — cheaper than REST, native JWT
	// authorizer, no VPC/NAT) fronted by a MANAGED Cognito JWT authorizer. The
	// client now sends ONLY `Authorization: Bearer <jwt>` (no SigV4), so:
	//   1. EDGE: the JWT authorizer validates the token's signature/iss/aud/exp
	//      against the Cognito pool's JWKS before the request reaches the Lambda.
	//   2. APPLICATION: the in-Lambda validator (HandlerWithAuth) re-validates
	//      signature-first from the SAME header and resolves the repo-scoped
	//      tenant. It stays unit-testable with a mock JWKS (the reason the Phase
	//      3 comment gave for rejecting API GW), so it is kept as defense in
	//      depth AND to carry the repo/tenant resolution the authorizer does not.
	//
	// This updates the Phase 3 decision (recorded above) that rejected API GW:
	// the header collision makes AWS_IAM + in-Lambda JWT unworkable on one
	// header, so signature validation moves to the managed authorizer while the
	// in-Lambda validator continues to authorize the tenant.
	//
	// Issuer + audience are DERIVED from the stack's own Cognito constructs
	// (userPool / userPoolClient) — never a hardcoded foreign pool. Identity
	// source is the standard Authorization header.
	mcpAuthorizer := awsapigatewayv2authorizers.NewHttpJwtAuthorizer(
		jsii.String("McpJwtAuthorizer"),
		issuer,
		&awsapigatewayv2authorizers.HttpJwtAuthorizerProps{
			AuthorizerName: jsii.String("cainban-mcp-jwt"),
			JwtAudience:    &[]*string{userPoolClient.UserPoolClientId(), spaClient.UserPoolClientId(), mcpCliClient.UserPoolClientId()},
			IdentitySource: jsii.Strings("$request.header.Authorization"),
		},
	)

	mcpIntegration := awsapigatewayv2integrations.NewHttpLambdaIntegration(
		jsii.String("McpIntegration"),
		fn,
		// Payload format 2.0 is the HTTP API default and is exactly what the
		// Lambda's httpadapter.NewV2 consumes (events.APIGatewayV2HTTPRequest).
		&awsapigatewayv2integrations.HttpLambdaIntegrationProps{
			PayloadFormatVersion: awsapigatewayv2.PayloadFormatVersion_VERSION_2_0(),
		},
	)

	// CORS allowed origins (RFC M6): derived from the SPA callback URLs in
	// context (spaCallbackUrls) rather than a wildcard "*". Each callback URL is
	// reduced to its scheme+host origin. The data plane is already gated by
	// in-Lambda auth, but a wildcard origin is needless attack surface — scope it
	// to the known SPA/connect origins. Falls back to the localhost+prod origins
	// if context is unset so `cdk synth` still works.
	corsOrigins := deriveCorsOrigins(ctxOr("spaCallbackUrls", "http://localhost:5173/,https://cainban.awiant.tech/"))

	mcpAPI := awsapigatewayv2.NewHttpApi(stack, jsii.String("McpHttpApi"), &awsapigatewayv2.HttpApiProps{
		ApiName:     jsii.String("cainban-mcp"),
		Description: jsii.String("MCP Streamable-HTTP endpoint — HTTP API; auth enforced IN the Lambda so the 401 carries the RFC 9728 resource_metadata challenge (Authorization: Bearer <jwt>, no SigV4)"),
		// DefaultAuthorizer stays as a FAIL-SAFE: any route added later without an
		// explicit authorizer is JWT-protected by default rather than open. The
		// MCP data routes below deliberately OVERRIDE it with HttpNoneAuthorizer
		// and let the Lambda enforce auth, so that the Lambda (not the managed
		// authorizer) produces the 401 — only the Lambda's challenge carries
		// `resource_metadata`, which is what makes a spec-compliant client show
		// an auth page. See the data-route comment below.
		DefaultAuthorizer: mcpAuthorizer,
		CorsPreflight: &awsapigatewayv2.CorsPreflightOptions{
			AllowOrigins: corsOrigins,
			AllowMethods: &[]awsapigatewayv2.CorsHttpMethod{awsapigatewayv2.CorsHttpMethod_ANY},
			// Authorization carries the bearer JWT; X-Cainban-Repo names the
			// target repo; the MCP session/protocol headers are used by the
			// Streamable-HTTP transport. No SigV4 headers are needed any more.
			AllowHeaders: jsii.Strings(
				"content-type", "mcp-session-id", "mcp-protocol-version",
				"authorization", "x-cainban-repo",
			),
		},
	})

	// Route every MCP request (root + any sub-path) to the MCP Lambda.
	//
	// These data routes use HttpNoneAuthorizer so the request reaches the Lambda,
	// which enforces auth itself (HandlerWithAuthChallenge: signature-first JWT
	// validation + repo-scoped tenant resolution — a missing/invalid/expired
	// token is a 401, an ungranted repo a 403, before any store runs). We move
	// enforcement OFF the managed JWT authorizer for one specific reason: a
	// spec-compliant MCP client bootstraps OAuth from the 401's
	// `WWW-Authenticate: Bearer resource_metadata="<url>"` challenge (RFC 9728 /
	// MCP auth spec), and the managed authorizer emits only a BARE `Bearer` with
	// no resource_metadata — so the client never discovers the authorization
	// server and never shows an auth page. The Lambda's challenge DOES carry
	// resource_metadata (CAINBAN_MCP_RESOURCE → the protected-resource doc URL),
	// so letting the Lambda produce the 401 is what makes the auth page appear.
	// HTTP APIs (apigatewayv2) cannot customize an authorizer's 401 headers
	// (no REST-style Gateway Responses), so in-Lambda enforcement is the fix.
	// The `verify-deploy.sh` smoke check asserts the 401 carries resource_metadata
	// so this cannot silently regress.
	mcpAPI.AddRoutes(&awsapigatewayv2.AddRoutesOptions{
		Path:        jsii.String("/"),
		Methods:     &[]awsapigatewayv2.HttpMethod{awsapigatewayv2.HttpMethod_ANY},
		Integration: mcpIntegration,
		Authorizer:  awsapigatewayv2.NewHttpNoneAuthorizer(),
	})
	mcpAPI.AddRoutes(&awsapigatewayv2.AddRoutesOptions{
		Path:        jsii.String("/{proxy+}"),
		Methods:     &[]awsapigatewayv2.HttpMethod{awsapigatewayv2.HttpMethod_ANY},
		Integration: mcpIntegration,
		Authorizer:  awsapigatewayv2.NewHttpNoneAuthorizer(),
	})

	// CORS preflight. Explicit OPTIONS routes with HttpNoneAuthorizer so the
	// managed CorsPreflight layer answers the browser's preflight (204) instead
	// of any authorizer. The data routes above are already HttpNoneAuthorizer
	// (auth is enforced in the Lambda), so these explicit OPTIONS routes keep the
	// preflight behaviour unambiguous and independent of the API default. The
	// authed data methods (POST/GET on these paths) still require a valid JWT —
	// now enforced by the in-Lambda validator rather than the managed authorizer.
	mcpAPI.AddRoutes(&awsapigatewayv2.AddRoutesOptions{
		Path:        jsii.String("/"),
		Methods:     &[]awsapigatewayv2.HttpMethod{awsapigatewayv2.HttpMethod_OPTIONS},
		Integration: mcpIntegration,
		Authorizer:  awsapigatewayv2.NewHttpNoneAuthorizer(),
	})
	mcpAPI.AddRoutes(&awsapigatewayv2.AddRoutesOptions{
		Path:        jsii.String("/{proxy+}"),
		Methods:     &[]awsapigatewayv2.HttpMethod{awsapigatewayv2.HttpMethod_OPTIONS},
		Integration: mcpIntegration,
		Authorizer:  awsapigatewayv2.NewHttpNoneAuthorizer(),
	})

	// --- RFC 9728 protected-resource metadata route (MCP OAuth step a) ---
	//
	// GET /.well-known/oauth-protected-resource is PUBLIC — a spec-compliant MCP
	// client (MCP authorization spec 2026-07-28) must be able to discover
	// cainban's authorization server BEFORE it holds any token. HttpNoneAuthorizer
	// explicitly removes the default JWT authorizer for this ONE route (mirroring
	// the connect /connect/github/callback exemption pattern); every other MCP
	// route above stays behind the managed Cognito JWT authorizer. The document
	// itself is served by the MCP Lambda (mcp.PublicMux), which routes only this
	// exact path publicly and everything else through the authed handler — so a
	// tool call can never reach an unauthenticated code path.
	mcpAPI.AddRoutes(&awsapigatewayv2.AddRoutesOptions{
		Path:        jsii.String("/.well-known/oauth-protected-resource"),
		Methods:     &[]awsapigatewayv2.HttpMethod{awsapigatewayv2.HttpMethod_GET},
		Integration: mcpIntegration,
		Authorizer:  awsapigatewayv2.NewHttpNoneAuthorizer(),
	})

	// --- RFC 8414 authorization-server metadata shim route (MCP OAuth a') ---
	//
	// GET /.well-known/oauth-authorization-server is PUBLIC — the RFC 8414
	// discovery document a spec-compliant MCP client (Claude Code) fetches to
	// find the authorization endpoints AND the PKCE capability. Cognito's own
	// OIDC discovery reports code_challenge_methods_supported:null (no PKCE
	// advertised), which makes such a client refuse the flow; this shim mirrors
	// Cognito's real endpoints but adds code_challenge_methods_supported:["S256"].
	// It is NOT a proxy (no token minting, no secret). Same HttpNoneAuthorizer
	// exemption pattern as the protected-resource route above; every other MCP
	// route stays behind the managed Cognito JWT authorizer.
	//
	// The RFC 9728 protected-resource doc advertises the MCP API ORIGIN as its
	// authorization server, so RFC 8414 discovery resolves to
	// <origin>/.well-known/oauth-authorization-server = THIS route (see
	// docs/mcp-oauth-setup.md for the discovery math + resolved URL).
	mcpAPI.AddRoutes(&awsapigatewayv2.AddRoutesOptions{
		Path:        jsii.String("/.well-known/oauth-authorization-server"),
		Methods:     &[]awsapigatewayv2.HttpMethod{awsapigatewayv2.HttpMethod_GET},
		Integration: mcpIntegration,
		Authorizer:  awsapigatewayv2.NewHttpNoneAuthorizer(),
	})

	// --- cainban MCP client auto-config route (not an RFC; cainban's own) ---
	//
	// GET /.well-known/mcp-client-config is PUBLIC — it returns the MCP URL, the
	// public OAuth client id, the resolved authorize/token endpoints, and
	// pre-computed per-client config snippets (Kiro Crew, Claude Code, Kiro IDE,
	// Cursor, VS Code) so an agent or the connect page can configure itself in
	// one fetch instead of hand-assembling JSON. Nothing in it is a secret (the
	// client id is a public PKCE identifier; authorization is always the token's
	// repos claim). Same HttpNoneAuthorizer exemption as the discovery routes;
	// every other MCP route stays behind the managed Cognito JWT authorizer. The
	// document is served by the MCP Lambda (mcp.PublicMux) which routes only this
	// exact path publicly.
	mcpAPI.AddRoutes(&awsapigatewayv2.AddRoutesOptions{
		Path:        jsii.String("/.well-known/mcp-client-config"),
		Methods:     &[]awsapigatewayv2.HttpMethod{awsapigatewayv2.HttpMethod_GET},
		Integration: mcpIntegration,
		Authorizer:  awsapigatewayv2.NewHttpNoneAuthorizer(),
	})
	// URL. mcpAPI is created after the function, so the URL is added as env here
	// (a lazily-resolved CDK token) rather than at function construction. This is
	// the `resource` field of the metadata doc, the RFC 8707 resource indicator,
	// and the base of the WWW-Authenticate resource_metadata pointer. The
	// authorization server in the doc is the MCP API origin (derived by the
	// Lambda from CAINBAN_MCP_RESOURCE), so RFC 8414 discovery resolves to the
	// shim above. The shim's authorize/token endpoints come from the Hosted UI
	// domain env below; its jwks_uri from CAINBAN_AUTH_ISSUER (set above) — never
	// hardcoded.
	fn.AddEnvironment(jsii.String("CAINBAN_MCP_RESOURCE"), mcpAPI.Url(), nil)

	// The Cognito Hosted UI base URL — where Cognito's authorize/token endpoints
	// live (NOT under the issuer host). The RFC 8414 AS-metadata shim builds its
	// authorization_endpoint/token_endpoint from this. Sourced from the same
	// UserPoolDomain construct the HostedUiDomain output uses — never hardcoded.
	fn.AddEnvironment(jsii.String("CAINBAN_HOSTED_UI_DOMAIN"), hostedUiBaseURL, nil)

	// The public Cognito MCP CLI app client id (McpCliClientId output). The
	// client-config auto-config document (mcp.NewClientConfigHandler) advertises
	// it so an agent/connect-page can write a complete MCP config — including the
	// OAuth client id — without the user looking it up. It is a public PKCE
	// client identifier, not a secret, and authorizes nothing on its own (every
	// request is still gated by the token's validated repos claim). Sourced from
	// the McpCliClient construct the McpCliClientId output uses — never hardcoded.
	fn.AddEnvironment(jsii.String("CAINBAN_MCP_CLI_CLIENT_ID"), mcpCliClient.UserPoolClientId(), nil)

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

	// --- Real-time WebSocket API -----------------------------------------
	//
	// The in-process /ws/{owner}/{repo} endpoint (src/systems/mcp/websocket.go)
	// cannot run on Lambda: an API Gateway v2 HTTP API does not proxy raw
	// WebSocket upgrades, and a Lambda has no persistent process to hold the
	// live connection. An API Gateway WebSocket API is the serverless transport:
	// API Gateway terminates the socket and invokes a dedicated WS Lambda on the
	// three route keys ($connect/$disconnect/$default). That Lambda only manages
	// the connection REGISTRY in the "cainban" table (store.WSConnectionStore);
	// the actual real-time PUSH happens from the MCP Lambda's broadcaster
	// (src/systems/mcp/wsconn), which lists a repo's connections and posts the
	// activity frame via the API Gateway Management API when a tool mutates data.
	//
	// Auth: the browser WebSocket API cannot set headers, so the bearer token +
	// target repo arrive as $connect query-string params and are validated
	// signature-first by the SAME Cognito resolver the MCP path uses, authorized
	// against the token's signed repos claim BEFORE any connection row is stored
	// (handler.go handleConnect). A token that does not grant the repo is
	// rejected at $connect — tenant isolation is structural.
	//
	// provided.al2023 + arm64 + pure-Go bootstrap, same as the other functions.
	// Built into ../.build/ws (see `make ws`). The connection rows live in the
	// EXISTING "cainban" table (co-located per repo partition), so no new table.
	wsFn := awslambda.NewFunction(stack, jsii.String("WsFunction"), &awslambda.FunctionProps{
		FunctionName: jsii.String("cainban-ws"),
		Runtime:      awslambda.Runtime_PROVIDED_AL2023(),
		Architecture: awslambda.Architecture_ARM_64(),
		Handler:      jsii.String("bootstrap"),
		MemorySize:   jsii.Number(128),
		Timeout:      awscdk.Duration_Seconds(jsii.Number(10)),
		// Frugality + abuse guard: $connect/$disconnect are tiny, fast registry
		// writes. Cap concurrency so a connection flood cannot fan out Lambda +
		// on-demand DynamoDB cost unbounded. 10 is ample at dev scale and bounds
		// the blast radius exactly like the MCP function's cap.
		ReservedConcurrentExecutions: jsii.Number(10),
		Environment: &map[string]*string{
			"CAINBAN_BACKEND":    jsii.String("dynamodb"),
			"CAINBAN_DDB_TABLE":  table.TableName(),
			"CAINBAN_DDB_REGION": stack.Region(),
			// Same Cognito pool as the MCP Lambda — $connect validates the same
			// tokens the MCP endpoint accepts (signature-first, repos claim).
			"CAINBAN_AUTH_ISSUER":   issuer,
			"CAINBAN_AUTH_AUDIENCE": authAudiencesCsv,
		},
		Code: awslambda.Code_FromAsset(jsii.String("../.build/ws"), nil),
	})

	// Least-privilege DynamoDB for the WS Lambda: it registers/lists/removes
	// connection rows only — PutItem (register the two rows), GetItem (resolve a
	// connection's repo on $disconnect), Query (not used by the WS Lambda itself
	// but by the broadcaster; kept off here), DeleteItem (deregister). Grant
	// exactly the actions the connection store issues on this table, scoped to
	// the table ARN alone. No Query is granted to the WS Lambda (it never lists
	// a repo's connections — only the broadcaster does, on the MCP role).
	wsFn.AddToRolePolicy(awsiam.NewPolicyStatement(&awsiam.PolicyStatementProps{
		Effect: awsiam.Effect_ALLOW,
		Actions: jsii.Strings(
			"dynamodb:GetItem",
			"dynamodb:PutItem",
			"dynamodb:DeleteItem",
		),
		Resources: &[]*string{table.TableArn()},
	}))

	wsConnectIntegration := awsapigatewayv2integrations.NewWebSocketLambdaIntegration(
		jsii.String("WsConnectIntegration"), wsFn, nil)
	wsDisconnectIntegration := awsapigatewayv2integrations.NewWebSocketLambdaIntegration(
		jsii.String("WsDisconnectIntegration"), wsFn, nil)
	wsDefaultIntegration := awsapigatewayv2integrations.NewWebSocketLambdaIntegration(
		jsii.String("WsDefaultIntegration"), wsFn, nil)

	// The WebSocket API with the three required route keys wired to the WS
	// Lambda. Auth is enforced INSIDE the $connect handler (query-string token,
	// signature-first) rather than via an API Gateway WebSocket authorizer: the
	// browser cannot send an Authorization header on the upgrade, so a Lambda/
	// request authorizer reading the token from the query string would be the
	// only option, and doing the SAME validation in the connect handler keeps WS
	// and MCP auth on one code path (the resolver) with no second construct.
	wsAPI := awsapigatewayv2.NewWebSocketApi(stack, jsii.String("WsApi"), &awsapigatewayv2.WebSocketApiProps{
		ApiName:     jsii.String("cainban-ws"),
		Description: jsii.String("Real-time task-activity push — API Gateway WebSocket API. $connect authenticates via query-string token+repo (signature-first, repo-scoped); the MCP Lambda pushes frames via the Management API."),
		// cainban's protocol routes everything the client sends to $default
		// (the client only ever sends an auth-less ping); server->client push
		// is driven by the broadcaster, not by client messages.
		ConnectRouteOptions: &awsapigatewayv2.WebSocketRouteOptions{
			Integration: wsConnectIntegration,
		},
		DisconnectRouteOptions: &awsapigatewayv2.WebSocketRouteOptions{
			Integration: wsDisconnectIntegration,
		},
		DefaultRouteOptions: &awsapigatewayv2.WebSocketRouteOptions{
			Integration: wsDefaultIntegration,
			// $default returns a pong frame to the client's ping, so the route
			// must be allowed to send a response.
			ReturnResponse: jsii.Bool(true),
		},
	})

	// A single auto-deploying stage. The stage URL is the wss:// endpoint the
	// browser connects to; the stage CALLBACK URL (https://) is what the
	// Management API client posts to — that is the value the MCP Lambda's
	// broadcaster needs as CAINBAN_WS_API_ENDPOINT.
	wsStage := awsapigatewayv2.NewWebSocketStage(stack, jsii.String("WsStage"), &awsapigatewayv2.WebSocketStageProps{
		WebSocketApi: wsAPI,
		StageName:    jsii.String("prod"),
		AutoDeploy:   jsii.Bool(true),
	})

	// Let the MCP Lambda PUSH to connected clients: grant execute-api:ManageConnections
	// on this WebSocket API/stage so the broadcaster's PostToConnection calls are
	// authorized. GrantManageConnections scopes the IAM action to this API's ARN
	// alone — the MCP role cannot manage any other API's connections.
	wsAPI.GrantManageConnections(fn)

	// Tell the MCP Lambda where to push. Set as a lazily-resolved CDK token
	// AFTER the stage exists (the function was created earlier), mirroring how
	// CAINBAN_MCP_RESOURCE is added post-API. When this env is present the MCP
	// Lambda installs the API-Gateway-WebSocket broadcaster (cmd/cainban-lambda
	// main.go); when absent it keeps the in-process-hub default (a no-op on
	// Lambda). The CALLBACK url (https://.../<stage>) is the Management API
	// endpoint, NOT the wss:// client url.
	fn.AddEnvironment(jsii.String("CAINBAN_WS_API_ENDPOINT"), wsStage.CallbackUrl(), nil)

	awslogs.NewLogGroup(stack, jsii.String("WsLogGroup"), &awslogs.LogGroupProps{
		LogGroupName:  jsii.String("/aws/lambda/cainban-ws"),
		Retention:     awslogs.RetentionDays_ONE_MONTH,
		RemovalPolicy: awscdk.RemovalPolicy_DESTROY,
	})

	// --- Outputs ----------------------------------------------------------
	awscdk.NewCfnOutput(stack, jsii.String("McpApiUrl"), &awscdk.CfnOutputProps{
		Value:       mcpAPI.Url(),
		Description: jsii.String("MCP Streamable-HTTP endpoint — API Gateway v2 HTTP API + managed Cognito JWT authorizer. Client sends Authorization: Bearer <jwt> (no SigV4)."),
	})
	awscdk.NewCfnOutput(stack, jsii.String("McpProtectedResourceMetadataUrl"), &awscdk.CfnOutputProps{
		Value: awscdk.Fn_Join(jsii.String(""), &[]*string{
			mcpAPI.Url(),
			jsii.String(".well-known/oauth-protected-resource"),
		}),
		Description: jsii.String("RFC 9728 Protected Resource Metadata (MCP OAuth step a) — PUBLIC (AuthorizationType NONE), returns {resource, authorization_servers, scopes_supported, bearer_methods_supported}. mcpAPI.Url() ends with '/', so this is <McpApiUrl>.well-known/oauth-protected-resource."),
	})
	awscdk.NewCfnOutput(stack, jsii.String("WsApiUrl"), &awscdk.CfnOutputProps{
		Value:       wsStage.Url(),
		Description: jsii.String("Real-time WebSocket endpoint the browser connects to (wss://). Connect with ?repo=<owner/repo>&token=<jwt>; the token is validated signature-first at $connect and authorized against its repos claim. The SPA uses this as VITE_WS_URL."),
	})
	awscdk.NewCfnOutput(stack, jsii.String("WsManagementEndpoint"), &awscdk.CfnOutputProps{
		Value:       wsStage.CallbackUrl(),
		Description: jsii.String("API Gateway Management API callback endpoint (https://) the MCP Lambda posts real-time frames to. Set on the MCP Lambda as CAINBAN_WS_API_ENDPOINT; not a client URL."),
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

	// --- Federation / SPA outputs ----------------------------------------
	awscdk.NewCfnOutput(stack, jsii.String("HostedUiDomain"), &awscdk.CfnOutputProps{
		Value:       hostedUiBaseURL,
		Description: jsii.String("Cognito Hosted UI base URL (free prefix domain). The SPA's signInWithRedirect targets this; also the OAuth base for the SPA client."),
	})
	awscdk.NewCfnOutput(stack, jsii.String("GitHubDeviceFlowUri"), &awscdk.CfnOutputProps{
		Value:       awscdk.Fn_Join(jsii.String(""), &[]*string{mcpAPI.Url(), jsii.String("auth/github/device")}),
		Description: jsii.String("GitHub Device Flow endpoint: POST to initiate device auth, then poll for token. CLI clients use this instead of Cognito."),
	})
	awscdk.NewCfnOutput(stack, jsii.String("SpaClientId"), &awscdk.CfnOutputProps{
		Value:       spaClient.UserPoolClientId(),
		Description: jsii.String("Cognito app client id for the browser SPA (public, PKCE, authorization-code grant, GitHub federated). Use as VITE_USER_POOL_CLIENT_ID."),
	})
	awscdk.NewCfnOutput(stack, jsii.String("McpCliClientId"), &awscdk.CfnOutputProps{
		Value:       mcpCliClient.UserPoolClientId(),
		Description: jsii.String("Cognito app client id for CLI MCP clients (Claude Code): public, PKCE, authorization-code grant, no secret, Cognito login, loopback callbacks. For GitHub auth, use the Device Flow endpoint instead."),
	})
	awscdk.NewCfnOutput(stack, jsii.String("SpaOauthScopes"), &awscdk.CfnOutputProps{
		Value:       jsii.String("openid email profile"),
		Description: jsii.String("OAuth scopes granted to the SPA client."),
	})

	// --- Amplify Hosting service role ------------------------------------
	//
	// The IAM role AWS Amplify Hosting assumes to run builds for the connect-page
	// SPA (web/): write CloudWatch build logs, manage the app's build/deploy
	// artifacts. It is defined here (rather than auto-created in the console) so
	// the role is IaC — versioned, reviewable, and consistent with the rest of
	// the stack. It does NOT connect the GitHub repo (that is a one-time console
	// OAuth) and grants nothing to the SPA itself; it is only the build-time
	// service identity. (RFC H1/C1) The AWS-managed AdministratorAccess-Amplify
	// policy is near-admin; this role only needs to write its build logs. We
	// attach a SCOPED inline policy instead: CloudWatch Logs for the Amplify
	// build log groups only. Amplify manages its own build/artifact storage
	// under the service-linked role it creates per app, so the custom service
	// role does not need S3/CloudFront/Lambda/IAM breadth.
	//
	// Attach it to the Amplify app after deploy (console: App settings → General
	// → Service role → select cainban-amplify-service-role; or
	// `aws amplify update-app --app-id <id> --iam-service-role-arn <this output>`).
	//
	// NOTE: if a future Amplify build step needs an additional action, add that
	// specific action here rather than reverting to the managed admin policy.
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
						// Scoped to the Amplify build log groups in this account/region.
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
		Description: jsii.String("IAM service role ARN for AWS Amplify Hosting — attach to the Amplify app (App settings → General → Service role, or aws amplify update-app --iam-service-role-arn)."),
	})

	return stack
}

// splitCsv turns a comma-separated context string into a []*string suitable for
// Cognito callback/logout URL lists. Empty entries are dropped and surrounding
// whitespace trimmed, so "a, b ,," yields ["a","b"].
func splitCsv(csv string) []*string {
	out := []*string{}
	for _, part := range strings.Split(csv, ",") {
		p := strings.TrimSpace(part)
		if p == "" {
			continue
		}
		out = append(out, jsii.String(p))
	}
	return out
}

// deriveCorsOrigins turns a comma-separated list of SPA callback URLs (the
// spaCallbackUrls context value, e.g. "http://localhost:5173/,https://app.example/")
// into the set of scheme+host origins for the API Gateway CORS AllowOrigins
// (RFC M6). It strips any path/query and a trailing slash so each entry is a
// bare origin ("http://localhost:5173", "https://app.example"), de-duplicates,
// and preserves order. If nothing usable is parsed it falls back to "*" so a
// misconfigured context never breaks the preflight entirely (the data plane is
// still auth-gated in-Lambda).
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
