# Deploying cainban to prod

A step-by-step runbook for standing up the **prod** cainban environment. The
existing stack (profile `aws-test-hamin`, account `528757808822`, `eu-north-1`)
is **dev**; prod is a separate account in a different region.

The CDK app is already **parameterized** — account, region, and stack name come
from CDK context, so no code change is needed to target prod. A flagless deploy
still targets dev; prod is three `-c` flags.

Everything below is grounded in `infra/stack.go`, `infra/cainban.go`,
`infra/cdk.json`, and the `Makefile`. Read it once end-to-end before starting —
the secret ordering in Step 2 and Step 5 is the one thing that fails a first
deploy if done out of order.

---

## Prod target (fixed values)

| | Value |
|---|---|
| AWS profile | `elastic-mobile-aws-reseller` |
| Account | `563329104476` |
| Region | `eu-central-1` |
| Stack name | `CainbanProdStack` |

**Reuse of external apps:** prod **reuses** the dev Entra app registration and
the dev GitHub App where possible (see Steps 2, 6, 7). This avoids standing up
new apps, at the cost of sharing credentials and (for the GitHub App) one
Callback URL. The implications are called out in each step.

The three context flags, used on every prod cdk command:

```
-c account=563329104476 -c region=eu-central-1 -c stackName=CainbanProdStack
```

---

## 1. Authenticate to the prod account

```bash
aws sso login --profile elastic-mobile-aws-reseller
aws sts get-caller-identity --profile elastic-mobile-aws-reseller
# expect Account == 563329104476
```

---

## 2. Create and fill the Entra OIDC secret — BEFORE the first deploy

This is the ordering trap. The stack **references** `cainban/entra-oidc` via
`Secret_FromSecretNameV2` (it does **not** create it). A Cognito OIDC provider
is **rejected at create time if its `client_secret` resolves empty**, so the
secret must already hold a real value when the first `cdk deploy` runs.

Because prod **reuses the dev Entra app registration**, the `client_id` /
`client_secret` are the **same values** already in dev's `cainban/entra-oidc`.
But the secret name is account-scoped, so it must be created fresh in the prod
account:

```bash
aws secretsmanager create-secret \
  --name cainban/entra-oidc \
  --secret-string '{"client_id":"65a6a7c8-d1aa-43ae-ad6d-3170b8037a41","client_secret":"<dev-entra-client-secret>"}' \
  --profile elastic-mobile-aws-reseller --region eu-central-1
```

- `client_id` `65a6a7c8-…` is the dev Entra app id (from `infra/cdk.json`), reused.
- `<dev-entra-client-secret>` is the same client secret dev uses. **Best practice
  is a dedicated Entra client secret per environment** (one Entra app can hold
  several secrets) — if you mint a prod-only secret on the same app, use that
  value here instead. Either way the `client_id` is unchanged.
- The JSON shape must be exactly `{"client_id":"...","client_secret":"..."}`.
- The client secret must **not** be empty (an empty secret makes the provider
  fail with *"value of provider details should not be empty"*).

> If you skip this step, the first deploy fails creating `EntraOidcProvider`.

---

## 3. Context values for prod

`entraIssuer` and `entraClientId` are **reused from dev** (same Entra tenant/app)
and already match the defaults in `infra/cdk.json`, so they can be omitted — but
pass them explicitly to be safe. The SPA URLs are the one thing that is
prod-specific and not known until the prod SPA is hosted (Step 8), so the first
deploy uses a localhost placeholder:

| Context key | Prod value |
|---|---|
| `entraIssuer` | `https://login.microsoftonline.com/10adc47f-7651-4d30-b70e-c93cc9ec44fa/v2.0` (reused) |
| `entraClientId` | `65a6a7c8-d1aa-43ae-ad6d-3170b8037a41` (reused) |
| `spaCallbackUrls` | `http://localhost:5173/` on first deploy; prod SPA URL after Step 8 |
| `spaLogoutUrls` | same as `spaCallbackUrls` |
| `connectSuccessUrl` | `http://localhost:5173/` on first deploy; prod SPA URL after Step 8 |

`mcpCliCallbackUrls` is defaulted in code to loopback URLs (the pinned Kiro
`http://127.0.0.1:3334/oauth/callback` and Claude Code ports) — environment
-agnostic, leave as-is.

---

## 4. Build, diff, bootstrap, deploy

Run from the repo root.

**Build the Lambda bundles** (arm64, CGO-free):

```bash
make bundles
```

**Preview the diff — read it in full** (a brand-new environment shows the whole
stack being created):

```bash
cd infra
AWS_PROFILE=elastic-mobile-aws-reseller AWS_REGION=eu-central-1 \
  npx -y aws-cdk@2.1143.0 diff CainbanProdStack \
  -c account=563329104476 -c region=eu-central-1 -c stackName=CainbanProdStack \
  -c entraIssuer=https://login.microsoftonline.com/10adc47f-7651-4d30-b70e-c93cc9ec44fa/v2.0 \
  -c entraClientId=65a6a7c8-d1aa-43ae-ad6d-3170b8037a41 \
  -c spaCallbackUrls='http://localhost:5173/' \
  -c spaLogoutUrls='http://localhost:5173/' \
  -c connectSuccessUrl='http://localhost:5173/'
cd ..
```

**First-ever deploy only — bootstrap** the account/region for CDK assets:

```bash
cd infra
AWS_PROFILE=elastic-mobile-aws-reseller AWS_REGION=eu-central-1 \
  npx -y aws-cdk@2.1143.0 bootstrap aws://563329104476/eu-central-1
cd ..
```

**Deploy.** `make deploy` chains `bundles -> cdk deploy -> verify-deploy`:

```bash
AWS_PROFILE=elastic-mobile-aws-reseller AWS_REGION=eu-central-1 \
STACK=CainbanProdStack \
CDK='npx -y aws-cdk@2.1143.0 -c account=563329104476 -c region=eu-central-1 -c stackName=CainbanProdStack -c entraIssuer=https://login.microsoftonline.com/10adc47f-7651-4d30-b70e-c93cc9ec44fa/v2.0 -c entraClientId=65a6a7c8-d1aa-43ae-ad6d-3170b8037a41 -c spaCallbackUrls=http://localhost:5173/ -c spaLogoutUrls=http://localhost:5173/ -c connectSuccessUrl=http://localhost:5173/' \
  make deploy
```

> On this agent host, exporting `AWS_PROFILE` inline before a command can trip a
> safety policy — from the agent, prefer passing `--profile`/`--region` to the
> cdk/aws CLIs. From your own terminal the inline env vars above are fine.

Record the stack **outputs** when it finishes:

```bash
aws cloudformation describe-stacks --stack-name CainbanProdStack \
  --profile elastic-mobile-aws-reseller --region eu-central-1 \
  --query 'Stacks[0].Outputs' --output table
```

The live prod stack (deployed) produced these — use them in Steps 5–8:

| Output | Value |
|---|---|
| `McpApiUrl` | `https://o9ftud9cma.execute-api.eu-central-1.amazonaws.com/` |
| `ConnectApiUrl` | `https://w241x2n8qj.execute-api.eu-central-1.amazonaws.com/` |
| `HostedUiDomain` | `https://cainban-emawiant-563329104476.auth.eu-central-1.amazoncognito.com` |
| `EntraRedirectUri` | `https://cainban-emawiant-563329104476.auth.eu-central-1.amazoncognito.com/oauth2/idpresponse` |
| `SpaClientId` | `2n08c333d497nblhg8dttabmrl` |
| `McpCliClientId` | `3867okvs3l6iudktvrfq7cq23k` |
| `UserPoolId` | `eu-central-1_gzEhznD4C` |
| `GitHubAppSecretName` | `cainban/github-app` |
| `AmplifyServiceRoleArn` | `arn:aws:iam::563329104476:role/cainban-amplify-service-role` |

---

## 5. Fill the GitHub App secret — AFTER deploy

Unlike the Entra secret, `cainban/github-app` is **created by the stack** (empty,
RETAIN). Fill it after the first deploy. Because prod **reuses the dev GitHub
App**, these are the same credentials dev uses:

```bash
aws secretsmanager put-secret-value \
  --secret-id cainban/github-app \
  --secret-string '{"app_id":"<id>","client_id":"<id>","client_secret":"<secret>","private_key":"-----BEGIN RSA PRIVATE KEY-----\n...\n-----END RSA PRIVATE KEY-----"}' \
  --profile elastic-mobile-aws-reseller --region eu-central-1
```

The deploy succeeds with this secret empty; the **connect flow** just won't work
until it's filled.

---

## 6. Register the Entra redirect URI (post-deploy)

Register the stack's `EntraRedirectUri` output (it is
`<HostedUiDomain>/oauth2/idpresponse`) in the **reused** Entra app registration.

- An Entra app can hold **multiple** redirect URIs, so adding the prod Hosted UI
  redirect alongside the existing dev one does **not** disturb dev.
- Platform type must be **Web**, **not SPA** — Cognito is a confidential client
  that sends the Entra client secret; a SPA-platform registration refuses the
  secret and the code exchange fails.
- In Entra: **App registrations -> (the reused app) -> Authentication -> Web ->
  Add URI ->** paste the prod `EntraRedirectUri` value.

---

## 7. GitHub App Callback URL — reuse caveat

The GitHub App's **Callback URL** must be `<ConnectApiUrl>connect/github/callback`.

**This is the one place reuse bites:** a GitHub App has a **single** Callback URL
field (it does accept a small number of additional callback URLs — up to the
GitHub limit — under *"Add callback URL"*). Add the prod `ConnectApiUrl`-based
callback as an **additional** callback URL on the reused App so both dev and prod
work; do **not** replace the dev one. If the reused App cannot hold both, prod
needs its own GitHub App. Full procedure and permissions: `docs/github-app-setup.md`.

---

## 8. Deploy the SPA on Amplify Hosting with a custom domain

The browser SPA (`web/`) is **not** part of the CDK stack — the stack creates
only the Amplify **service role** (`AmplifyServiceRoleArn` output); there is no
`CfnApp`/`CfnBranch`/`CfnDomain`. The SPA is hosted by **Amplify Hosting
connected to the GitHub repo**, configured via the console + CLI. `amplify.yml`
at the repo root already builds it (Node 22 -> `web/dist`), and every setting
the SPA needs is a `VITE_*` build env var — all public, no secrets.

> **The one gotcha that will bite you:** Vite **bakes `VITE_*` vars into the
> bundle at build time**, not at runtime. If the first build runs before the
> env vars are set, the bundle ships with empty config and the SPA throws
> `AuthUserPoolException: Auth UserPool not configured` the moment you click
> sign-in. Setting the vars without triggering a **fresh build** does nothing —
> "Redeploy this version" replays the old artifact and does **not** re-bake.
> Always set the vars, then run a new build, then confirm the baked bundle.

### 8a. Create the Amplify app (console)

Connecting a GitHub repo needs the GitHub OAuth handshake, which is a console
action:

1. Amplify console (prod account, `eu-central-1`) -> **Create new app** ->
   **Host web app** -> connect **GitHub** -> repo `hmain/cainban`, branch `main`.
2. Amplify auto-detects `amplify.yml` at the root — accept it unchanged.
3. **Service role:** attach the stack's `AmplifyServiceRoleArn`
   (`arn:aws:iam::563329104476:role/cainban-amplify-service-role`), or set it
   later under App settings -> General -> Service role.

### 8b. Set the `VITE_*` env vars, then build (CLI)

App-level env vars apply to all branches. From the `app_id` (e.g.
`d3ovi21dv77gti` for `awiant-cainban`):

```bash
aws amplify update-app --app-id <app_id> \
  --profile elastic-mobile-aws-reseller --region eu-central-1 \
  --environment-variables \
    VITE_USER_POOL_ID=<UserPoolId>,\
VITE_USER_POOL_CLIENT_ID=<SpaClientId>,\
VITE_COGNITO_DOMAIN=<HostedUiDomain without https://>,\
VITE_CONNECT_API=<ConnectApiUrl>,\
VITE_MCP_API=<McpApiUrl>,\
VITE_MCP_CLI_CLIENT_ID=<McpCliClientId>,\
VITE_REDIRECT_URL=https://cainban.awiant.tech/

# then trigger a FRESH build (re-runs the build phase, re-bakes the vars)
aws amplify start-job --app-id <app_id> --branch-name main --job-type RELEASE \
  --profile elastic-mobile-aws-reseller --region eu-central-1
```

Concrete prod values for the table above:

| Env var | Value |
|---|---|
| `VITE_USER_POOL_ID` | `eu-central-1_gzEhznD4C` |
| `VITE_USER_POOL_CLIENT_ID` | `2n08c333d497nblhg8dttabmrl` (`SpaClientId`) |
| `VITE_COGNITO_DOMAIN` | `cainban-emawiant-563329104476.auth.eu-central-1.amazoncognito.com` (no `https://`) |
| `VITE_CONNECT_API` | `https://w241x2n8qj.execute-api.eu-central-1.amazonaws.com/` |
| `VITE_MCP_API` | `https://o9ftud9cma.execute-api.eu-central-1.amazonaws.com/` |
| `VITE_MCP_CLI_CLIENT_ID` | `3867okvs3l6iudktvrfq7cq23k` (`McpCliClientId`) |
| `VITE_REDIRECT_URL` | `https://cainban.awiant.tech/` |

Setting `VITE_REDIRECT_URL` to the final custom domain up front avoids a second
rebuild — the SPA just won't fully sign in until the domain serves and the
callback is registered (8c, 8d).

**Confirm the vars actually baked in** — do not trust the build "SUCCEED" alone.
Fetch the deployed bundle and grep for the pool id; the asset hash changes when
a rebuild took:

```bash
# find the hashed JS asset, then grep it for the pool id
curl -s https://cainban.awiant.tech/ | grep -oE 'index-[A-Za-z0-9_-]+\.js'
curl -s https://cainban.awiant.tech/assets/index-<hash>.js | grep -c eu-central-1_gzEhznD4C
# expect >= 1; a 0 means the vars were not present at build time
```

### 8c. Custom domain `cainban.awiant.tech` (Route 53 delegated subdomain)

The subdomain is delegated to a Route 53 hosted zone in the **prod** account, so
Amplify can manage the validation + alias records itself.

1. **Create the hosted zone** (once):
   ```bash
   aws route53 create-hosted-zone --name cainban.awiant.tech \
     --caller-reference "cainban-$(date +%s)" \
     --profile elastic-mobile-aws-reseller --region eu-central-1
   ```
   Live zone id: `Z08830631OYA9U5QLUY93`. Read its four nameservers:
   ```bash
   aws route53 get-hosted-zone --id Z08830631OYA9U5QLUY93 \
     --profile elastic-mobile-aws-reseller --query 'DelegationSet.NameServers'
   ```
2. **Delegate from the parent `awiant.tech` DNS:** add an `NS` record for
   `cainban` pointing at those four nameservers. Verify:
   `dig NS cainban.awiant.tech +short` returns the four `awsdns-*` names.
3. **Add the custom domain in Amplify** (App settings -> Custom domains ->
   `cainban.awiant.tech`, map subdomain -> branch `main`). Because the zone is
   delegated and in the same account, Amplify writes the records into the zone
   itself: an ACM-validation CNAME, an **A/ALIAS** `cainban.awiant.tech` ->
   `d<xxxx>.cloudfront.net`, and a `www` CNAME. ACM validation + distribution
   activation takes ~15–60 min; `HTTP 000` / an empty apex `dig` during that
   window is normal, not a failure.

### 8d. Register the SPA callback on the Cognito client (stack redeploy)

The prod SPA client initially only allows the `localhost` placeholder. Redeploy
the stack with the real domain so Cognito accepts the `cainban.awiant.tech`
callback — rerun Step 4's deploy with:

```
-c spaCallbackUrls=https://cainban.awiant.tech/,http://localhost:5173/
-c spaLogoutUrls=https://cainban.awiant.tech/,http://localhost:5173/
-c connectSuccessUrl=https://cainban.awiant.tech/
```

(Keep `localhost:5173` so local dev still works.) This is a tiny diff — only the
SPA client's callback/logout URL lists change — and it is **independent of the
domain going live**, so you can run it as soon as the domain is decided.

> **Entra note:** the Entra redirect target is always the Cognito
> `oauth2/idpresponse` URL (registered in Step 6), **not** the SPA origin. For
> the Cognito Hosted-UI flow the SPA uses, you do **not** need to add
> `cainban.awiant.tech` to the Entra app — Step 8d covers it. Add the SPA origin
> to Entra only if you later point the SPA at Entra directly.

---

## 9. Grant the first users

Authorization is the validated `repos` claim, sourced from the grants table
(`GrantsTableName` output) keyed by Cognito `sub`. Grant either:

- **Via the connect flow** — a signed-in user connects a GitHub repo they can
  prove access to, and a grant is written automatically.
- **Operator bootstrap** — write a grant directly to the grants table for the
  first admin user. See the grants key design in `infra/README.md`.

---

## 10. Verify

**Automated edge smoke test** (CORS preflight 2xx + data path 401):

```bash
STACK=CainbanProdStack AWS_PROFILE=elastic-mobile-aws-reseller AWS_REGION=eu-central-1 make verify-deploy
```

**Manual checks:**

- `McpApiUrl` + `.well-known/oauth-protected-resource` returns the RFC 9728
  metadata (public, no auth).
- `McpApiUrl` + `.well-known/oauth-authorization-server` returns the RFC 8414
  shim pointing at the prod Hosted UI + pool, PKCE `S256`.
- An unauthenticated request to `McpApiUrl` returns **401**.
- A real `tools/list` through an authenticated MCP client returns the tools
  (with `outputSchema` on the 7 read tools, as of PR #58).
- The deployed SPA bundle at `https://cainban.awiant.tech/assets/index-<hash>.js`
  contains the pool id (`grep -c eu-central-1_gzEhznD4C` >= 1) — proves the
  `VITE_*` vars baked in (see 8b).
- Open `https://cainban.awiant.tech/`, click **Sign in with Awiant (Entra)** ->
  it redirects to `login.microsoftonline.com/<tenant>/oauth2/v2.0/authorize`
  with the correct Entra `client_id` and the prod `idpresponse` redirect URI,
  and Entra serves its login page (no `AADSTS50011` redirect-mismatch, no
  `AuthUserPoolException`). Reaching the Entra login page is the correct
  automated stopping point — the actual credential entry is a human step and
  exercises the full login -> callback -> token round-trip.

---

## Quick reference — the two secrets

| Secret | Created by | Fill when | Shape | Prod value |
|---|---|---|---|---|
| `cainban/entra-oidc` | **You, before deploy** (stack references it) | **Before** first deploy (Step 2) | `{"client_id","client_secret"}` | reused dev Entra app |
| `cainban/github-app` | **The stack** (empty, RETAIN) | **After** deploy (Step 5) | `{"app_id","client_id","client_secret","private_key"}` | reused dev GitHub App |

## Rollback

- **Stack:** redeploy the previous known-good commit (`make deploy` rolls
  CloudFormation forward). DynamoDB tables are RETAIN + PITR, so data survives.
- **SPA:** revert the offending `web/` commit on `main`; Amplify rebuilds.
- **Secrets** are RETAIN / not stack-owned — a `cdk destroy` never drops an
  operator-filled key.
