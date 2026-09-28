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
	"github.com/aws/aws-cdk-go/awscdk/v2/awskms"
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

	// --- DynamoDB grants table (Phase 4) ---------------------------------
	//
	// A SEPARATE table from the "cainban" tenant-data table. Grants are keyed by
	// SUBJECT (PK=USER#<cognitoSub>, SK=GRANT#<owner>/<repo>) — a disjoint
	// keyspace and a different access path (the pre-token trigger queries by
	// user; it never touches board/task data). A separate table is chosen over
	// reusing the single "cainban" table so the pre-token Lambda's IAM can be
	// scoped to the grants table ARN ALONE (least privilege — a token-minting
	// trigger structurally cannot read task data), and so PITR/backup boundaries
	// stay clean per concern. Cost is identical: PAY_PER_REQUEST (on-demand),
	// zero at rest. Same durability posture as the main table (PITR + RETAIN).
	// See src/systems/grants for the key design and fallback/fail-closed rules.
	grantsTable := awsdynamodb.NewTable(stack, jsii.String("GrantsTable"), &awsdynamodb.TableProps{
		TableName: jsii.String("cainban-grants"),
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

	// --- Secrets Manager: GitHub App credentials (Phase 4, P4.2) ---------
	//
	// A PLACEHOLDER secret for the GitHub App's credentials — the App id, OAuth
	// client id/secret, and RSA PRIVATE KEY that the connect/verify flow (P4.3)
	// uses to call the GitHub API and prove a principal's access to owner/repo
	// before a grant is written (see src/systems/github, src/systems/secrets).
	//
	// It is created EMPTY on purpose: NO real secret value lives in code, this
	// repo, or the synthesized template. After deploy an operator fills it in
	// (see docs/github-app-setup.md) with the JSON shape the loader expects:
	//
	//	{"app_id":"…","client_id":"…","client_secret":"…","private_key":"-----BEGIN RSA PRIVATE KEY-----\n…"}
	//
	// The `generate_string_key`/template here only seeds a well-formed empty
	// JSON envelope so the secret exists with the right shape; it contains no
	// credential. RETAIN so a `cdk destroy` never drops an operator-filled key.
	//
	// P4.2 only CREATES the secret and wires least-privilege read access ready
	// for P4.3 — the connect Lambda that consumes it is built in P4.3, not here.
	// For now the read grant is attached to the existing MCP Lambda's role (the
	// function that P4.3 extends with the connect routes), scoped to THIS secret
	// ARN alone.
	githubAppSecret := awssecretsmanager.NewSecret(stack, jsii.String("GitHubAppSecret"), &awssecretsmanager.SecretProps{
		SecretName:  jsii.String("cainban/github-app"),
		Description: jsii.String("cainban GitHub App credentials (Phase 4) — PLACEHOLDER; operator fills app_id/client_id/client_secret/private_key post-deploy. No secret value in code/CDK."),
		GenerateSecretString: &awssecretsmanager.SecretStringGenerator{
			// Seed an empty JSON envelope; the generated key is a throwaway
			// field the operator overwrites. No real credential is generated.
			SecretStringTemplate: jsii.String(`{"app_id":"","client_id":"","client_secret":"","private_key":""}`),
			GenerateStringKey:    jsii.String("_placeholder"),
		},
		RemovalPolicy: awscdk.RemovalPolicy_RETAIN,
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

	// --- Entra ID (and future) OIDC federation --------------------------
	//
	// Browser users sign in through an external OIDC identity provider (the
	// customer's Microsoft Entra ID tenant) via the Cognito Hosted UI, rather
	// than with a Cognito-native password. This does NOT touch the machine
	// McpClient above (USER_PASSWORD/SRP for CLI/agent callers) — federation is
	// added purely for the browser SPA client created below.
	//
	// A federated user still becomes a normal Cognito user with a stable pool
	// `sub`, and the pre-token trigger + grants table key off that SAME `sub`
	// exactly as for a native user (see src/systems/auth: authorization is the
	// validated `repos` claim, sourced from grants keyed by the Cognito sub).
	// So federation changes only HOW a user authenticates, never how they are
	// authorized — no change to the pre-token trigger, grants, or validator.
	//
	// SaaS-pluggability: providers are declared as a slice of specs and created
	// in a loop. Onboarding a second customer's OIDC IdP (another Entra tenant,
	// Okta, etc.) is ADDING an oidcProviderSpec entry + its own placeholder
	// secret — not rewriting this block. Each spec's client secret lives in its
	// OWN Secrets Manager secret (RETAIN, placeholder), never in code/context.

	// oidcProviderSpec declares one federated OIDC identity provider. issuer and
	// clientId are non-secret and come from CDK context (placeholder-safe so
	// synth works before a real tenant is known); the client SECRET is read from
	// the named Secrets Manager secret at deploy/runtime, never from code.
	type oidcProviderSpec struct {
		// name is the Cognito provider name browser clients reference in
		// SupportedIdentityProviders (letters/digits/_ only; no spaces).
		name string
		// providerConstructID is the CDK construct id for this IdP.
		providerConstructID string
		// issuer is the OIDC issuer URL, e.g.
		// https://login.microsoftonline.com/<tenant>/v2.0 — from context.
		issuer string
		// clientId is the app (client) id registered in the external IdP — from
		// context, non-secret.
		clientId string
		// secret is the Secrets Manager secret holding {"client_id","client_secret"}
		// for this provider (placeholder, RETAIN). Its client_secret is passed to
		// the OIDC provider as the ClientSecret.
		secret awssecretsmanager.ISecret
	}

	// Placeholder-safe context reader: returns the -c value if present, else the
	// supplied placeholder default so `cdk synth` runs before the real tenant is
	// known. NEVER used for secrets — those live in Secrets Manager.
	ctxOr := func(key, def string) string {
		if v := stack.Node().TryGetContext(jsii.String(key)); v != nil {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
		return def
	}

	// Awiant's Entra OIDC placeholder secret. Created EMPTY on purpose — no real
	// secret in code/CDK/context. The operator fills client_id/client_secret
	// post-deploy (the OIDC provider reads client_secret from here). RETAIN so a
	// The Entra OIDC client credentials live in a Secrets Manager secret the
	// operator fills out of band (never in code/CDK). Because a Cognito OIDC
	// provider is REJECTED at create time if its client_secret resolves empty,
	// the secret must already hold a real value when this stack creates the
	// provider — so the stack REFERENCES an existing secret (created + filled by
	// the operator) rather than creating an empty placeholder it would then fail
	// to use on the first deploy. Create it once out of band:
	//   aws secretsmanager create-secret --name cainban/entra-oidc \
	//     --secret-string '{"client_id":"<id>","client_secret":"<secret>"}'
	// then deploy. `cdk destroy` never touches it (not stack-owned).
	entraOidcSecret := awssecretsmanager.Secret_FromSecretNameV2(stack, jsii.String("EntraOidcSecret"), jsii.String("cainban/entra-oidc"))

	// The SaaS-pluggable provider list. Wire Awiant's Entra as the first (only)
	// element today; a second customer is one more append here.
	oidcProviders := []oidcProviderSpec{
		{
			name:                "EntraAwiant",
			providerConstructID: "EntraOidcProvider",
			// Placeholder issuer/clientId keep synth working pre-tenant. Supply
			// real values at deploy: -c entraIssuer=https://login.microsoftonline.com/<tenant>/v2.0 -c entraClientId=<appId>
			issuer:   ctxOr("entraIssuer", "https://login.microsoftonline.com/00000000-0000-0000-0000-000000000000/v2.0"),
			clientId: ctxOr("entraClientId", "PLACEHOLDER_ENTRA_CLIENT_ID"),
			secret:   entraOidcSecret,
		},
	}

	// Create each provider and collect them so the SPA client can list them as
	// supported identity providers (and depend on them for correct ordering).
	oidcSupported := []awscognito.UserPoolClientIdentityProvider{}
	createdOidcProviders := []awscognito.IUserPoolIdentityProvider{}
	for _, spec := range oidcProviders {
		provider := awscognito.NewUserPoolIdentityProviderOidc(stack, jsii.String(spec.providerConstructID), &awscognito.UserPoolIdentityProviderOidcProps{
			UserPool:  userPool,
			Name:      jsii.String(spec.name),
			IssuerUrl: jsii.String(spec.issuer),
			ClientId:  jsii.String(spec.clientId),
			// The provider reads the client secret from the placeholder Secrets
			// Manager secret; the operator fills it post-deploy. Never inline.
			ClientSecret: spec.secret.SecretValueFromJson(jsii.String("client_secret")).UnsafeUnwrap(),
			Scopes:       jsii.Strings("openid", "email", "profile"),
			// GET keeps the userinfo request simple and is what Entra expects.
			AttributeRequestMethod: awscognito.OidcAttributeRequestMethod_GET,
			AttributeMapping: &awscognito.AttributeMapping{
				Email: awscognito.ProviderAttribute_Other(jsii.String("email")),
			},
		})
		createdOidcProviders = append(createdOidcProviders, provider)
		oidcSupported = append(oidcSupported, awscognito.UserPoolClientIdentityProvider_Custom(jsii.String(spec.name)))
	}

	// --- Hosted UI domain (free Cognito prefix domain) ------------------
	//
	// A free Cognito-hosted prefix domain (no custom-domain cost, no ACM cert).
	// The prefix must be globally unique across the region; a deterministic
	// account-derived suffix avoids collisions without needing a real tenant.
	// The Hosted UI is where signInWithRedirect sends the browser: it presents
	// the Entra IdP button and, on return, exchanges the code for tokens.
	hostedDomain := userPool.AddDomain(jsii.String("HostedUiDomain"), &awscognito.UserPoolDomainOptions{
		CognitoDomain: &awscognito.CognitoDomainOptions{
			// Deterministic, globally-unique-per-region prefix derived from the
			// account id so it does not collide with another AWS account's pool.
			DomainPrefix: awscdk.Fn_Join(jsii.String("-"), &[]*string{
				jsii.String("cainban-emawiant"),
				stack.Account(),
			}),
		},
	})

	// The Hosted UI base URL and the exact Entra redirect (reply) URI the
	// operator must register in the Entra app registration.
	hostedUiBaseURL := hostedDomain.BaseUrl(nil)
	entraRedirectURI := awscdk.Fn_Join(jsii.String(""), &[]*string{hostedUiBaseURL, jsii.String("/oauth2/idpresponse")})

	// --- SPA app client (public, PKCE) ----------------------------------
	//
	// A SEPARATE public client for the browser SPA — the machine McpClient is
	// left untouched. No secret is generated (public PKCE client). It uses the
	// authorization-code grant with openid/email/profile and lists the federated
	// OIDC provider(s) as supported identity providers, so the Hosted UI offers
	// the Entra sign-in. Callback/logout URLs come from context with a localhost
	// dev default + a placeholder for the Amplify URL (filled after first
	// Amplify deploy — see web/README.md).
	spaCallbacks := splitCsv(ctxOr("spaCallbackUrls", "http://localhost:5173/,https://localhost/"))
	spaLogouts := splitCsv(ctxOr("spaLogoutUrls", "http://localhost:5173/,https://localhost/"))

	spaClient := userPool.AddClient(jsii.String("SpaClient"), &awscognito.UserPoolClientOptions{
		UserPoolClientName: jsii.String("cainban-spa-client"),
		// Public SPA client: PKCE, no secret.
		GenerateSecret: jsii.Bool(false),
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
		SupportedIdentityProviders: &oidcSupported,
	})

	// The SPA client references the OIDC provider(s) by name in
	// SupportedIdentityProviders, so the providers must exist first. Add an
	// explicit construct dependency so synth/deploy order is correct.
	for _, p := range createdOidcProviders {
		spaClient.Node().AddDependency(p)
	}

	// Both app clients issue tokens against this pool: the machine/CLI MCP
	// client AND the browser SPA client. The JWT authorizers and the in-Lambda
	// validators must accept EITHER `aud`, so the accepted-audience value is the
	// comma-separated pair. (CAINBAN_AUTH_AUDIENCE is parsed as a CSV by the
	// Lambdas; the API GW authorizers take the two ids as a list directly.)
	authAudiencesCsv := awscdk.Fn_Join(jsii.String(","), &[]*string{
		userPoolClient.UserPoolClientId(),
		spaClient.UserPoolClientId(),
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
		// Frugality: cap concurrency so a burst of token issuance (or abuse)
		// cannot fan out Lambda + on-demand DynamoDB cost. This is a fast
		// per-request trigger; 5 is ample at dev scale.
		ReservedConcurrentExecutions: jsii.Number(5),
		// Phase 4: the trigger reads grants from the grants table. Name + region
		// come from env; the custom:repos attribute remains a read fallback when
		// the table has nothing for a subject (and if this env were unset the
		// trigger degrades to the attribute-only path — it never fails token
		// issuance on missing config).
		Environment: &map[string]*string{
			"CAINBAN_GRANTS_TABLE":  grantsTable.TableName(),
			"CAINBAN_GRANTS_REGION": stack.Region(),
		},
		Code: awslambda.Code_FromAsset(jsii.String("../.build/pretoken"), nil),
	})

	// Least-privilege: the pre-token trigger only READS grants — it issues
	// GetItem (default_repo META item) + Query (a subject's GRANT# items). Grant
	// ONLY those two actions, scoped to the grants table ARN alone. It gets NO
	// write actions and NO access to the "cainban" tenant-data table, so a
	// token-minting trigger structurally cannot mutate grants or read task data.
	preTokenFn.AddToRolePolicy(awsiam.NewPolicyStatement(&awsiam.PolicyStatementProps{
		Effect: awsiam.Effect_ALLOW,
		Actions: jsii.Strings(
			"dynamodb:GetItem",
			"dynamodb:Query",
		),
		Resources: &[]*string{grantsTable.TableArn()},
	}))

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
		// Frugality: cap concurrency on the hot MCP path so a runaway agent
		// loop or traffic spike cannot scale out Lambda + on-demand DynamoDB
		// cost unbounded. 10 is generous for a dev-scale shared board.
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
			// Phase 4 (P4.2): the name of the placeholder GitHub App creds
			// secret. The connect/verify flow (P4.3) loads the App credentials
			// from this secret at runtime via src/systems/secrets — never from
			// code or env. Wired now so P4.3 needs no infra change to read it.
			"CAINBAN_GITHUB_APP_SECRET": githubAppSecret.SecretName(),
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

	// Phase 4 (P4.2): least-privilege READ of the GitHub App creds secret.
	// Grant ONLY secretsmanager:GetSecretValue, scoped to THIS secret's ARN
	// alone — not "secretsmanager:*", not a wildcard resource. This is the one
	// call src/systems/secrets makes. `Secret.GrantRead` attaches exactly that
	// (GetSecretValue + DescribeSecret) on this secret ARN to the function's
	// role; no other secret is reachable. Wired onto the existing MCP Lambda now
	// so the P4.3 connect flow (hosted on this same function) can load the App
	// credentials without an infra change; the pre-token trigger is deliberately
	// NOT granted this (it never touches GitHub).
	githubAppSecret.GrantRead(fn.Role(), nil)

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
			JwtAudience:    &[]*string{userPoolClient.UserPoolClientId(), spaClient.UserPoolClientId()},
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

	mcpAPI := awsapigatewayv2.NewHttpApi(stack, jsii.String("McpHttpApi"), &awsapigatewayv2.HttpApiProps{
		ApiName:           jsii.String("cainban-mcp"),
		Description:       jsii.String("MCP Streamable-HTTP endpoint — HTTP API + managed Cognito JWT authorizer (Authorization: Bearer <jwt>, no SigV4)"),
		DefaultAuthorizer: mcpAuthorizer,
		CorsPreflight: &awsapigatewayv2.CorsPreflightOptions{
			AllowOrigins: jsii.Strings("*"),
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

	// Route every MCP request (root + any sub-path) to the MCP Lambda under the
	// default JWT authorizer. The MCP client POSTs to the API root.
	mcpAPI.AddRoutes(&awsapigatewayv2.AddRoutesOptions{
		Path:        jsii.String("/"),
		Methods:     &[]awsapigatewayv2.HttpMethod{awsapigatewayv2.HttpMethod_ANY},
		Integration: mcpIntegration,
	})
	mcpAPI.AddRoutes(&awsapigatewayv2.AddRoutesOptions{
		Path:        jsii.String("/{proxy+}"),
		Methods:     &[]awsapigatewayv2.HttpMethod{awsapigatewayv2.HttpMethod_ANY},
		Integration: mcpIntegration,
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

	// The MCP Lambda serves the RFC 9728 metadata document from its OWN canonical
	// URL. mcpAPI is created after the function, so the URL is added as env here
	// (a lazily-resolved CDK token) rather than at function construction. This is
	// the `resource` field of the metadata doc, the RFC 8707 resource indicator,
	// and the base of the WWW-Authenticate resource_metadata pointer. The
	// authorization server in the doc is the Cognito issuer, derived by the
	// Lambda from CAINBAN_AUTH_ISSUER (already set above) — never hardcoded.
	fn.AddEnvironment(jsii.String("CAINBAN_MCP_RESOURCE"), mcpAPI.Url(), nil)

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

	// --- Connect API Lambda (Phase 4, P4.3) ------------------------------
	//
	// A SEPARATE function from the MCP Lambda. The connect flow has a distinct
	// job (human OAuth + server-side repo verification + grant writes) and a
	// distinct IAM surface (Secrets Manager read + grants-table read/WRITE, and
	// crucially NO access to the "cainban" board/task data table). Keeping it
	// its own function keeps each function's blast radius and least-privilege
	// IAM minimal, and lets the connect endpoint be a browser-facing redirect
	// surface without touching the MCP data path. Same runtime/arch as the other
	// functions: provided.al2023 + arm64 + pure-Go bootstrap (CGO off — no
	// SQLite here). Built into ../.build/connect (see `make connect`/`bundles`).

	// KMS customer-managed key that encrypts the user GitHub OAuth REFRESH token
	// stored on the grants IDENTITY#github item (Option A: /connect/available-repos
	// mints a short-lived user access token from it at list time). The plaintext
	// refresh token NEVER lands in DynamoDB — only its KMS ciphertext, bound to
	// the subject via KMS EncryptionContext. Key rotation is enabled; the connect
	// Lambda is granted ONLY kms:Encrypt/kms:Decrypt on THIS key (below).
	connectRefreshKey := awskms.NewKey(stack, jsii.String("ConnectRefreshTokenKey"), &awskms.KeyProps{
		Alias:             jsii.String("cainban/connect-refresh-token"),
		Description:       jsii.String("Encrypts the stored GitHub OAuth refresh token for the cainban connect flow (Option A available-repos)."),
		EnableKeyRotation: jsii.Bool(true),
		RemovalPolicy:     awscdk.RemovalPolicy_DESTROY,
	})

	connectFn := awslambda.NewFunction(stack, jsii.String("ConnectFunction"), &awslambda.FunctionProps{
		FunctionName: jsii.String("cainban-connect"),
		Runtime:      awslambda.Runtime_PROVIDED_AL2023(),
		Architecture: awslambda.Architecture_ARM_64(),
		Handler:      jsii.String("bootstrap"),
		MemorySize:   jsii.Number(256),
		Timeout:      awscdk.Duration_Seconds(jsii.Number(30)),
		// Frugality: the connect API is low-frequency (interactive OAuth +
		// grant writes), so a small cap prevents any burst from scaling out
		// GitHub-verify calls and grants-table writes. 5 is ample.
		ReservedConcurrentExecutions: jsii.Number(5),
		Environment: &map[string]*string{
			// Same Cognito pool as the MCP Lambda — the connect API validates
			// the SAME signature-first JWT before any GitHub/secret/grant action.
			"CAINBAN_AUTH_ISSUER":   issuer,
			"CAINBAN_AUTH_AUDIENCE": authAudiencesCsv,
			// GitHub App credentials (App id, OAuth client id/secret, private
			// key) are loaded at runtime from this Secrets Manager secret —
			// never from code or env.
			"CAINBAN_GITHUB_APP_SECRET": githubAppSecret.SecretName(),
			// Grants table: the connect API READS and WRITES grants here (the
			// pre-token trigger only reads). Region for the DynamoDB client.
			"CAINBAN_GRANTS_TABLE":  grantsTable.TableName(),
			"CAINBAN_GRANTS_REGION": stack.Region(),
			// KMS key id used to encrypt/decrypt the stored refresh token
			// (Option A). Required at cold start — no plaintext-fallback path.
			"CAINBAN_CONNECT_KMS_KEY_ID": connectRefreshKey.KeyId(),
			// Optional GitHub App slug so available-repos can pick THIS App's
			// installation when a user can see several. The App id (from the
			// secret) is the primary discriminator; this is a secondary hint.
			"CAINBAN_GITHUB_APP_SLUG": jsii.String(ctxOr("githubAppSlug", "")),
			// Where the OAuth callback sends the browser after a successful
			// identity link — set to the connect-page SPA so the user lands back
			// in the app instead of seeing the raw JSON confirmation. Supplied at
			// deploy: -c connectSuccessUrl=https://<amplify-app>/ (empty => the
			// callback returns a JSON {linked:true} confirmation instead).
			"CAINBAN_CONNECT_SUCCESS_URL": jsii.String(ctxOr("connectSuccessUrl", "")),
		},
		Code: awslambda.Code_FromAsset(jsii.String("../.build/connect"), nil),
	})

	// Least-privilege DynamoDB: the connect API issues GetItem/Query (read a
	// subject's grants + linked identity) and PutItem/DeleteItem (write/revoke a
	// grant, persist the linked identity) on the GRANTS table ONLY. It is
	// deliberately NOT granted anything on the "cainban" data table — the
	// connect flow never touches board/task data. UpdateItem is not granted
	// because the grants store never issues it (item put/delete only).
	connectFn.AddToRolePolicy(awsiam.NewPolicyStatement(&awsiam.PolicyStatementProps{
		Effect: awsiam.Effect_ALLOW,
		Actions: jsii.Strings(
			"dynamodb:GetItem",
			"dynamodb:Query",
			"dynamodb:PutItem",
			"dynamodb:DeleteItem",
		),
		Resources: &[]*string{grantsTable.TableArn()},
	}))

	// Least-privilege secret read: ONLY GetSecretValue (+ DescribeSecret) on the
	// GitHub App secret ARN alone — the connect Lambda loads the App credentials
	// at runtime to mint App JWTs, exchange OAuth codes, and call the GitHub API.
	githubAppSecret.GrantRead(connectFn.Role(), nil)

	// Least-privilege KMS: grant EXACTLY kms:Encrypt + kms:Decrypt on the
	// refresh-token key ARN alone — the connect Lambda encrypts the stored
	// refresh token at link time and decrypts it at list time via direct
	// Encrypt/Decrypt (the token is well under the 4 KB direct-encrypt limit).
	// GrantEncryptDecrypt would additionally add GenerateDataKey*/ReEncrypt*,
	// which the code never issues; an explicit two-action statement keeps the
	// surface to exactly what is used. No key-management actions are granted.
	connectFn.AddToRolePolicy(awsiam.NewPolicyStatement(&awsiam.PolicyStatementProps{
		Effect: awsiam.Effect_ALLOW,
		Actions: jsii.Strings(
			"kms:Encrypt",
			"kms:Decrypt",
		),
		Resources: &[]*string{connectRefreshKey.KeyArn()},
	}))

	// --- API Gateway v2 HTTP API + Cognito JWT authorizer (Connect) ------
	//
	// REPLACES the connect Function URL (AuthType AWS_IAM) for the same reason
	// as the MCP endpoint: the SigV4 signature and the Cognito JWT both want the
	// Authorization header. The connect API now sits behind an HTTP API with a
	// managed Cognito JWT authorizer as the DEFAULT — every /connect route
	// requires `Authorization: Bearer <jwt>` (no SigV4) — with ONE exception:
	//
	//   GET /connect/github/callback is AUTHORIZER-EXEMPT. It is a browser
	//   redirect from GitHub's OAuth (GitHub -> this callback URL) and CANNOT
	//   carry a Cognito JWT. Its authenticity comes from the HMAC-signed,
	//   sub-bound, expiring `state` param (see src/systems/connect/state.go +
	//   handler.go handleCallback): the state is verified with a key derived
	//   from the App's OAuth client secret and is bound to the sub that started
	//   the flow. Putting the JWT authorizer on the callback would break the
	//   browser redirect (GitHub sends no bearer token), so that one route uses
	//   HttpNoneAuthorizer while every other route inherits the JWT authorizer.
	//
	// Issuer + audience are derived from the SAME Cognito constructs as the MCP
	// authorizer (userPool / userPoolClient) — never hardcoded.
	connectAuthorizer := awsapigatewayv2authorizers.NewHttpJwtAuthorizer(
		jsii.String("ConnectJwtAuthorizer"),
		issuer,
		&awsapigatewayv2authorizers.HttpJwtAuthorizerProps{
			AuthorizerName: jsii.String("cainban-connect-jwt"),
			JwtAudience:    &[]*string{userPoolClient.UserPoolClientId(), spaClient.UserPoolClientId()},
			IdentitySource: jsii.Strings("$request.header.Authorization"),
		},
	)

	connectIntegration := awsapigatewayv2integrations.NewHttpLambdaIntegration(
		jsii.String("ConnectIntegration"),
		connectFn,
		&awsapigatewayv2integrations.HttpLambdaIntegrationProps{
			PayloadFormatVersion: awsapigatewayv2.PayloadFormatVersion_VERSION_2_0(),
		},
	)

	connectAPI := awsapigatewayv2.NewHttpApi(stack, jsii.String("ConnectHttpApi"), &awsapigatewayv2.HttpApiProps{
		ApiName:           jsii.String("cainban-connect"),
		Description:       jsii.String("Connect API (Phase 4 P4.3) — HTTP API + Cognito JWT authorizer; /connect/github/callback is authorizer-exempt (HMAC state auth)"),
		DefaultAuthorizer: connectAuthorizer,
		CorsPreflight: &awsapigatewayv2.CorsPreflightOptions{
			AllowOrigins: jsii.Strings("*"),
			AllowMethods: &[]awsapigatewayv2.CorsHttpMethod{awsapigatewayv2.CorsHttpMethod_ANY},
			AllowHeaders: jsii.Strings("content-type", "authorization"),
		},
	})

	// Authenticated /connect routes — inherit the default JWT authorizer.
	connectAPI.AddRoutes(&awsapigatewayv2.AddRoutesOptions{
		Path:        jsii.String("/connect/github/start"),
		Methods:     &[]awsapigatewayv2.HttpMethod{awsapigatewayv2.HttpMethod_GET},
		Integration: connectIntegration,
	})
	connectAPI.AddRoutes(&awsapigatewayv2.AddRoutesOptions{
		Path:        jsii.String("/connect/repo"),
		Methods:     &[]awsapigatewayv2.HttpMethod{awsapigatewayv2.HttpMethod_POST, awsapigatewayv2.HttpMethod_DELETE},
		Integration: connectIntegration,
	})
	connectAPI.AddRoutes(&awsapigatewayv2.AddRoutesOptions{
		Path:        jsii.String("/connect/repos"),
		Methods:     &[]awsapigatewayv2.HttpMethod{awsapigatewayv2.HttpMethod_GET},
		Integration: connectIntegration,
	})
	// Option A: list the repos the signed-in user can access through the App
	// installation. Cognito-JWT-authed under the SAME default authorizer as the
	// other authenticated /connect routes (NOT the callback exemption).
	connectAPI.AddRoutes(&awsapigatewayv2.AddRoutesOptions{
		Path:        jsii.String("/connect/available-repos"),
		Methods:     &[]awsapigatewayv2.HttpMethod{awsapigatewayv2.HttpMethod_GET},
		Integration: connectIntegration,
	})
	connectAPI.AddRoutes(&awsapigatewayv2.AddRoutesOptions{
		Path:        jsii.String("/connect/app-info"),
		Methods:     &[]awsapigatewayv2.HttpMethod{awsapigatewayv2.HttpMethod_GET},
		Integration: connectIntegration,
	})

	// The GitHub OAuth callback — EXEMPT from the JWT authorizer. HttpNoneAuthorizer
	// explicitly removes the default authorizer for this one route. Auth here is
	// the HMAC-signed sub-bound state param verified inside handleCallback.
	connectAPI.AddRoutes(&awsapigatewayv2.AddRoutesOptions{
		Path:        jsii.String("/connect/github/callback"),
		Methods:     &[]awsapigatewayv2.HttpMethod{awsapigatewayv2.HttpMethod_GET},
		Integration: connectIntegration,
		Authorizer:  awsapigatewayv2.NewHttpNoneAuthorizer(),
	})

	awslogs.NewLogGroup(stack, jsii.String("ConnectLogGroup"), &awslogs.LogGroupProps{
		LogGroupName:  jsii.String("/aws/lambda/cainban-connect"),
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
	awscdk.NewCfnOutput(stack, jsii.String("ConnectApiUrl"), &awscdk.CfnOutputProps{
		Value:       connectAPI.Url(),
		Description: jsii.String("Connect API endpoint (Phase 4 P4.3) — HTTP API + Cognito JWT authorizer; /connect/* require Authorization: Bearer <jwt>. The GitHub App Callback URL is <this>connect/github/callback (authorizer-exempt; HMAC state auth)."),
	})
	awscdk.NewCfnOutput(stack, jsii.String("TableName"), &awscdk.CfnOutputProps{
		Value:       table.TableName(),
		Description: jsii.String("DynamoDB table backing cainban"),
	})
	awscdk.NewCfnOutput(stack, jsii.String("GrantsTableName"), &awscdk.CfnOutputProps{
		Value:       grantsTable.TableName(),
		Description: jsii.String("DynamoDB grants table (Phase 4) — read by the pre-token trigger"),
	})
	awscdk.NewCfnOutput(stack, jsii.String("GitHubAppSecretName"), &awscdk.CfnOutputProps{
		Value:       githubAppSecret.SecretName(),
		Description: jsii.String("Secrets Manager secret holding GitHub App credentials (Phase 4) — PLACEHOLDER; operator fills post-deploy (see docs/github-app-setup.md)"),
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
	awscdk.NewCfnOutput(stack, jsii.String("EntraRedirectUri"), &awscdk.CfnOutputProps{
		Value:       entraRedirectURI,
		Description: jsii.String("EXACT redirect (reply) URI to register in the Entra app registration: <HostedUiDomain>/oauth2/idpresponse"),
	})
	awscdk.NewCfnOutput(stack, jsii.String("SpaClientId"), &awscdk.CfnOutputProps{
		Value:       spaClient.UserPoolClientId(),
		Description: jsii.String("Cognito app client id for the browser SPA (public, PKCE, authorization-code grant, Entra federated). Use as VITE_USER_POOL_CLIENT_ID."),
	})
	awscdk.NewCfnOutput(stack, jsii.String("SpaOauthScopes"), &awscdk.CfnOutputProps{
		Value:       jsii.String("openid email profile"),
		Description: jsii.String("OAuth scopes granted to the SPA client."),
	})
	awscdk.NewCfnOutput(stack, jsii.String("EntraOidcSecretName"), &awscdk.CfnOutputProps{
		Value:       entraOidcSecret.SecretName(),
		Description: jsii.String("Secrets Manager secret holding the Entra OIDC client credentials — PLACEHOLDER; operator fills client_id/client_secret post-deploy."),
	})

	// --- Amplify Hosting service role ------------------------------------
	//
	// The IAM role AWS Amplify Hosting assumes to run builds for the connect-page
	// SPA (web/): write CloudWatch build logs, manage the app's build/deploy
	// artifacts. It is defined here (rather than auto-created in the console) so
	// the role is IaC — versioned, reviewable, and consistent with the rest of
	// the stack. It does NOT connect the GitHub repo (that is a one-time console
	// OAuth) and grants nothing to the SPA itself; it is only the build-time
	// service identity. Uses the AWS-managed AdministratorAccess-Amplify policy,
	// the documented managed policy for the Amplify service role.
	//
	// Attach it to the Amplify app after deploy (console: App settings → General
	// → Service role → select cainban-amplify-service-role; or
	// `aws amplify update-app --app-id <id> --iam-service-role-arn <this output>`).
	amplifyServiceRole := awsiam.NewRole(stack, jsii.String("AmplifyServiceRole"), &awsiam.RoleProps{
		RoleName:    jsii.String("cainban-amplify-service-role"),
		AssumedBy:   awsiam.NewServicePrincipal(jsii.String("amplify.amazonaws.com"), nil),
		Description: jsii.String("Service role AWS Amplify Hosting assumes to build/deploy the cainban connect-page SPA."),
		ManagedPolicies: &[]awsiam.IManagedPolicy{
			awsiam.ManagedPolicy_FromAwsManagedPolicyName(jsii.String("AdministratorAccess-Amplify")),
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
