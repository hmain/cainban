# cainban infra (Phase 3 CDK)

AWS CDK (Go) app that provisions the serverless stack for cainban:

- **DynamoDB** single table `cainban` (on-demand, PITR, `RETAIN` on delete)
- **DynamoDB** grants table `cainban-grants` (**Phase 4** — on-demand, PITR,
  `RETAIN`) — the real home for per-user repo grants
  (`PK=USER#<sub>` / `SK=GRANT#<owner>/<repo>`), read by the pre-token trigger;
  supersedes the Cognito `custom:repos` attribute, which stays as a read
  fallback for migration
- **Lambda** `cainban-mcp` — `provided.al2023`, **arm64**, pure-Go bootstrap
  built from [`cmd/cainban-lambda`](../cmd/cainban-lambda) (serves the stateless
  Streamable-HTTP MCP handler, wrapped in **signature-first JWT auth +
  repo-scoped tenancy**)
- **Cognito user pool** `cainban-users` + app client — issues the JWTs the
  Lambda validates; carries the `repos` / `default_repo` authorization claims
- **Secrets Manager** secret `cainban/github-app` (**Phase 4, P4.2**) — a
  **PLACEHOLDER** for the GitHub App credentials (App id, OAuth client
  id/secret, RSA private key) that the connect/verify flow (P4.3) loads at
  runtime via [`src/systems/secrets`](../src/systems/secrets). Created empty
  (`RETAIN`); an operator fills it post-deploy (see
  [`docs/github-app-setup.md`](../docs/github-app-setup.md)). **No secret value
  in code, this repo, or the CDK template.** The MCP Lambda gets least-privilege
  `secretsmanager:GetSecretValue` on this secret ARN alone; its name reaches the
  Lambda via the `CAINBAN_GITHUB_APP_SECRET` env var
- **Pre-token-generation trigger** `cainban-pretoken` — a pure-Go arm64 Lambda
  attached to the user pool that builds each user's **top-level** `repos` /
  `default_repo` claims (the ones the validator authorizes against). **Phase 4:**
  it reads grants from the `cainban-grants` table, falling back to the user's
  `custom:repos` / `custom:default_repo` attributes when the table has nothing,
  and **failing closed** (no claim) on a table error (see
  [Granting a user access to a repo](#granting-a-user-access-to-a-repo))
- **Connect API Lambda** `cainban-connect` (**Phase 4, P4.3**) — a SEPARATE
  pure-Go arm64 Lambda built from [`cmd/cainban-connect`](../cmd/cainban-connect)
  serving the human GitHub-connect routes (`GET /connect/github/start`,
  `GET /connect/github/callback`, `POST`/`DELETE /connect/repo`,
  `GET /connect/repos`) behind the SAME signature-first Cognito JWT check. It
  identifies the connecting user via the App's user-OAuth leg, verifies repo
  access server-side (`github.VerifyRepoAccess`), and writes/revokes grants in
  the `cainban-grants` table. Own Function URL (`AWS_IAM` edge). See
  [Connect API (Phase 4, P4.3)](#connect-api-phase-4-p43).
- **Lambda Function URL** — **`AuthType: AWS_IAM`** (edge auth; no anonymous
  reachability)
- **IAM** least-privilege: the MCP Lambda gets only `GetItem`, `PutItem`,
  `UpdateItem`, `DeleteItem`, `Query` on the `cainban` table ARN, plus
  `secretsmanager:GetSecretValue` (+ `DescribeSecret`) on the
  `cainban/github-app` secret ARN alone (Phase 4, for the P4.3 connect flow);
  the pre-token trigger gets only `GetItem` + `Query` on the `cainban-grants`
  table ARN (read only, no access to the data table, and no secret access); the
  **connect** Lambda gets `GetItem` + `Query` + `PutItem` + `DeleteItem` on the
  `cainban-grants` table ARN (read **and write** — it persists verified grants
  and the linked identity) plus `secretsmanager:GetSecretValue`
  (+ `DescribeSecret`) on the `cainban/github-app` secret ARN, and — like the
  pre-token trigger — **no access to the `cainban` data table**
- **CloudWatch** log groups `/aws/lambda/cainban-mcp`,
  `/aws/lambda/cainban-pretoken`, `/aws/lambda/cainban-connect` (30-day retention)

> **The endpoint is authenticated (Phase 3).** Two layers gate it: the Function
> URL `AWS_IAM` edge (SigV4) and, in the Lambda, a **signature-first** Cognito
> **JWT** check (JWKS signature → issuer/audience/expiry → claims) that resolves
> the caller to exactly one authorized repo before any DynamoDB access. A
> missing/invalid token is **401**; a valid token without access to the target
> repo is **403**. See [`docs/serverless-multiuser-plan.md`](../docs/serverless-multiuser-plan.md)
> (Phase 3) for the auth design and the isolation proof.

Go CDK (not TypeScript) is used so the whole repo stays single-language: the
Lambda handler and the infrastructure are both Go.

## Deploy target

| Setting  | Value                          |
| -------- | ------------------------------ |
| Profile  | `aws-test-hamin`               |
| Region   | `eu-north-1`                   |
| Account  | resolved from the profile at deploy time |

## Prerequisites (installed into home/scratch, never system)

- Go (repo uses `~/goroot/bin/go`)
- AWS CDK CLI v2 (`npm i -g aws-cdk`, or a local prefix)
- AWS credentials for profile `aws-test-hamin`

## Build the Lambda bundles FIRST

The CDK stack packages `../.build/lambda` (the MCP handler), `../.build/pretoken`
(the Cognito pre-token trigger) and `../.build/connect` (the Phase 4 connect API)
via `Code.fromAsset`. Build all three before any synth/deploy:

```sh
# from the repo root
make bundles          # builds .build/lambda + .build/pretoken + .build/connect
# or individually:
make lambda           # => .build/lambda/bootstrap
make pretoken         # => .build/pretoken/bootstrap
make connect          # => .build/connect/bootstrap
# all: CGO_ENABLED=0 GOOS=linux GOARCH=arm64, lambda.norpc
```

## Synthesize (safe — no cloud calls)

```sh
cd infra
cdk synth --no-lookups
```

## Deploy (NOT run by the Phase 2 PR — a separate, approved step)

These commands are documented but were deliberately **NOT executed**. Bootstrap
is required once per account/region before the first deploy.

```sh
export AWS_PROFILE=aws-test-hamin
export CDK_DEFAULT_REGION=eu-north-1

# one-time per account/region:
cd infra
cdk bootstrap aws://<ACCOUNT_ID>/eu-north-1

# review then deploy:
cdk diff
cdk deploy CainbanPhase2Stack
```

After deploy, the stack outputs `FunctionUrl` (the MCP endpoint),
`ConnectFunctionUrl` (the Phase 4 connect API endpoint — its
`connect/github/callback` path is what the operator sets as the GitHub App
Callback URL, see [`docs/github-app-setup.md`](../docs/github-app-setup.md)),
`TableName`, `GrantsTableName`, `GitHubAppSecretName` (the placeholder GitHub App
secret to fill), `UserPoolId` and `UserPoolClientId`. Smoke test with a
`tools/list` call and one
tool call (SigV4-sign the request for the `AWS_IAM` edge, and send a Cognito
`Authorization: Bearer <JWT>` for the app layer), then verify the Lambda
`LastModified` advanced.

## Authentication (Phase 3)

The Lambda validates a **Cognito JWT signature-first** on every request
(`src/systems/auth`): JWKS signature → issuer/audience/expiry → claims, then
resolves the caller to one authorized repo and scopes the DynamoDB store to
`REPO#<owner>/<repo>#`. Config comes from Lambda env vars the CDK stack sets from
the Cognito user pool:

| Env var                 | Meaning                                                        |
| ----------------------- | ------------------------------------------------------------- |
| `CAINBAN_AUTH_ISSUER`   | OIDC issuer (`https://cognito-idp.<region>.amazonaws.com/<poolId>`) |
| `CAINBAN_AUTH_AUDIENCE` | expected `aud` (the Cognito app client id)                    |
| `CAINBAN_AUTH_JWKS_URL` | optional JWKS override; defaults to `<issuer>/.well-known/jwks.json` |

**Repo identity vs authorization:** the repo a request *targets* comes from an
MCP tool arg, the `X-Cainban-Repo` header, or the token's `default_repo` claim
(identity — untrusted alone). Whether the caller *may* touch it is decided by
the validated **`repos` claim** in the signed JWT (authorization). The target
can only narrow within the granted set, never escalate.

**Swapping the IdP:** the Lambda only needs an issuer + audience + JWKS URL, so
replacing Cognito with an existing IdP is a change to those three env vars (and
the CDK user-pool block), not a code change.

### How grants reach the token: the pre-token-generation trigger

The validator authorizes against a **top-level** `repos` claim (and optional
`default_repo`). Cognito, though, stores a user's grants in the **custom
attributes** `custom:repos` / `custom:default_repo`, and it does **not** surface
custom attributes as top-level claims — a raw Cognito token carries them as
`custom:repos` (a string), never as top-level `repos`. Without a bridge, no
user's grants would ever reach the validated claim and **every request would
403**.

The `cainban-pretoken` Lambda (`cmd/cainban-pretoken`) closes that gap. It is
attached to the user pool as the **PreTokenGeneration** trigger (CDK
`userPool.AddTrigger(UserPoolOperation_PRE_TOKEN_GENERATION(), fn, LambdaVersion_V1_0)`)
and, during token generation, builds `claimsToAddOrOverride`:

| Top-level claim it emits | Value shape                                                        |
| ------------------------ | ------------------------------------------------------------------ |
| `repos`                  | a **JSON-array-encoded string** of `owner/repo`, e.g. `["acme/a","acme/b"]` (Cognito claim-override values are always strings) |
| `default_repo`           | the user's default repo (string), when set |

The validator's `repos`-claim decoder accepts **both** a native JSON array (used
by the self-signed test tokens) and this string shape (JSON-array-encoded, or
space/comma-delimited), so the trigger's output flows straight through
authorization.

**Phase 4 — the grants table is the grant source.** The trigger now reads the
subject's grants from the DynamoDB **`cainban-grants`** table
(`src/systems/grants`), keyed `PK=USER#<sub>` / `SK=GRANT#<owner>/<repo>`. Its
resolution order per token:

1. **Subject** = the user's `sub` standard attribute (the same value that
   becomes the token's `sub` claim; `userName` is used only if `sub` is absent).
2. **Table has grants** → build the `repos` claim from the table (and the
   `default_repo` from the table's `META` item).
3. **Table empty** → fall back to the `custom:repos` / `custom:default_repo`
   attribute path, so users not yet migrated keep working.
4. **Table ERROR** → **fail closed**: emit **no** `repos` claim. A table error
   never falls back to the attributes and never fabricates a grant — no claim
   means the user has no grants on this token (→ 403 downstream), the safe
   outcome.
5. If `CAINBAN_GRANTS_TABLE` is **unset**, the trigger degrades to the
   attribute-only path — it never fails token issuance on missing config.

The trigger is still a **pure reflector**: with an empty table AND empty
attributes it emits **no** `repos` claim (→ 403) — it never invents a grant. It
holds **read-only** IAM (`GetItem` + `Query`) on the `cainban-grants` table ARN
alone — no writes, and no access to the `cainban` data table.

Its grants-table config comes from env vars the CDK stack sets:

| Env var                 | Meaning                                                            |
| ----------------------- | ------------------------------------------------------------------ |
| `CAINBAN_GRANTS_TABLE`  | grants table name (`cainban-grants`); unset ⇒ attribute-only path  |
| `CAINBAN_GRANTS_REGION` | grants table region (falls back to standard AWS region resolution) |

### Granting a user access to a repo

**Phase 4:** the real grant home is the `cainban-grants` table. A grant is one
item per repo — `PK=USER#<sub>`, `SK=GRANT#<owner>/<repo>` (presence = granted),
with an optional `default_repo` on the `SK=META` item and a reserved
`SK=IDENTITY#github` item for the linked GitHub install (populated by later
phases). Grants will be **written by the P4.3 connect API** once a user's GitHub
access to `owner/repo` is verified server-side; in the interim an operator can
put a grant item directly, e.g.:

```sh
export AWS_PROFILE=aws-test-hamin AWS_REGION=eu-north-1
aws dynamodb put-item --table-name cainban-grants --item '{
  "PK": {"S": "USER#<cognito-sub>"},
  "SK": {"S": "GRANT#acme/repo-a"},
  "repo": {"S": "acme/repo-a"}
}'
# optional default repo:
aws dynamodb put-item --table-name cainban-grants --item '{
  "PK": {"S": "USER#<cognito-sub>"},
  "SK": {"S": "META"},
  "default_repo": {"S": "acme/repo-a"}
}'
```

**Fallback (pre-Phase-4, still supported):** grants may also live in the user's
Cognito `custom:repos` attribute (a space-, comma-, or JSON-array-delimited list
of `owner/repo`), with an optional `custom:default_repo`. The trigger uses these
**only when the grants table has nothing** for the subject:

```sh
export AWS_PROFILE=aws-test-hamin AWS_REGION=eu-north-1
aws cognito-idp admin-update-user-attributes \
  --user-pool-id <UserPoolId> \
  --username <user-email-or-sub> \
  --user-attributes \
      Name=custom:repos,Value="acme/repo-a acme/repo-b" \
      Name=custom:default_repo,Value="acme/repo-a"
```

Either way the change takes effect on the user's **next token** (the trigger
runs at token generation), so the user re-authenticates / refreshes to pick up a
new grant. To **revoke**, delete the `GRANT#` item (or remove the repo from
`custom:repos`).

## Storage backend selector

The application chooses its backend from `CAINBAN_BACKEND`:

| `CAINBAN_BACKEND` | Backend    | Notes                                             |
| ----------------- | ---------- | ------------------------------------------------- |
| unset / `sqlite`  | SQLite     | Local CLI/TUI. Requires CGO.                      |
| `dynamodb`        | DynamoDB   | Serverless path. Pure Go. Used by the Lambda.     |

Extra DynamoDB env vars: `CAINBAN_DDB_TABLE` (default `cainban`),
`CAINBAN_DDB_REGION` (falls back to the standard AWS region resolution). The
Lambda sets all three via the CDK `Environment` block.

## GitHub App credentials (Phase 4, P4.2)

The connect/verify flow (P4.3, hosted on the **separate `cainban-connect`
Lambda**) loads the GitHub App credentials from **AWS Secrets Manager at
runtime** — never from code or the CDK. The CDK creates a **placeholder** secret
and grants least-privilege read to both the MCP Lambda and the connect Lambda;
an operator fills it after deploy.

| Resource / env var | Meaning |
| --- | --- |
| Secret `cainban/github-app` | JSON `{app_id, client_id, client_secret, private_key}` for the GitHub App. Created **empty** by the CDK (`RETAIN`); filled by the operator post-deploy. |
| `CAINBAN_GITHUB_APP_SECRET` | the secret's **name** the Lambda reads (CDK sets it from the `GitHubAppSecretName` output). Consumed by [`src/systems/secrets`](../src/systems/secrets). |
| `CAINBAN_GITHUB_APP_SECRET_REGION` | optional region override for the Secrets Manager read; absent, standard AWS region resolution applies. |

The loader parses the secret into a `github.AppConfig` and the `github` package
([`src/systems/github`](../src/systems/github)) uses it to mint the App JWT,
exchange an installation token, and run `VerifyRepoAccess` — proving a
principal's access to `owner/repo` **server-side** before a grant is written. No
secret value is in the repo, code, or CDK template. **Full operator procedure to
register the App and load the secret:**
[`docs/github-app-setup.md`](../docs/github-app-setup.md).

## Connect API (Phase 4, P4.3)

The `cainban-connect` Lambda ([`cmd/cainban-connect`](../cmd/cainban-connect),
handler in [`src/systems/connect`](../src/systems/connect)) is the human
GitHub-connect API — a SEPARATE function from `cainban-mcp` so its IAM and blast
radius stay minimal (grants read/write + secret read; **no data-table access**).

**Routes** (all validate the Cognito JWT **signature-first**; unauth → `401`):

| Route | Purpose |
| --- | --- |
| `GET /connect/github/start` | issue a sub-bound anti-CSRF state, `302` to the GitHub OAuth authorize URL |
| `GET /connect/github/callback?code&state` | validate state, exchange `code` for the user's GitHub login, persist it for the validated sub |
| `POST /connect/repo {owner,repo}` | require a linked identity (`409` otherwise), `VerifyRepoAccess` against the OAuth login, write grant on `true`, `403` on `false`, fail closed on error |
| `DELETE /connect/repo {owner,repo}` | revoke the grant for the validated sub |
| `GET /connect/repos` | list only the caller's grants + linked login |

**Compute choice — Function URL (not API Gateway).** Same rationale as the MCP
endpoint: the security core is the **in-Lambda** signature-first Cognito JWT
validation (unit-testable with a mock JWKS), and a Function URL adds no managed
surface. Edge is `AuthType: AWS_IAM`, so the browser OAuth hop is SigV4-signed by
an authenticated client — no anonymous reachability.

**Anti-CSRF state.** `state` is an HMAC-SHA256 token over
`"<sub>|<expiryUnix>|<nonce>"`, keyed by a value **derived from the App's OAuth
client secret** (so the key lives only in Secrets Manager and rotates with it).
It is stateless, **sub-bound** (the callback rejects a state whose sub ≠ the
caller's validated sub), expiring (10 min) and unforgeable.

**Identity vs authorization.** IDENTITY (the GitHub login) comes ONLY from the
OAuth leg; AUTHORIZATION (may this sub touch owner/repo) comes ONLY from
`VerifyRepoAccess` against that login. A client claim is never sufficient; a
verify error fails **closed** (no grant).

**Env vars** (set by the CDK): `CAINBAN_AUTH_ISSUER`, `CAINBAN_AUTH_AUDIENCE`
(same Cognito pool as MCP), `CAINBAN_GITHUB_APP_SECRET`, `CAINBAN_GRANTS_TABLE`,
`CAINBAN_GRANTS_REGION`. Optional: `CAINBAN_CONNECT_REDIRECT_URI` (echoed to
GitHub's OAuth), `CAINBAN_CONNECT_SUCCESS_URL` (browser redirect after a
successful link).

**IAM (least privilege):** `dynamodb:GetItem/Query/PutItem/DeleteItem` on the
`cainban-grants` table ARN alone + `secretsmanager:GetSecretValue`
(+ `DescribeSecret`) on the `cainban/github-app` secret ARN. No access to the
`cainban` data table.

**Operator callback URL:** after deploy, set the GitHub App's Callback URL to the
`ConnectFunctionUrl` output **plus** `connect/github/callback` (see
[`docs/github-app-setup.md`](../docs/github-app-setup.md) § 5a).

## DynamoDB table / key design

Single table, keyed `(PK, SK)`. In Phase 3 the PK carries the tenant prefix
`REPO#<owner>/<repo>#` (empty on the single-tenant local SQLite path):

| Item     | PK (Phase 3)                          | SK                                | Purpose                          |
| -------- | ------------------------------------- | --------------------------------- | -------------------------------- |
| board    | `REPO#<owner>/<repo>#BOARD#<id>`      | `META`                            | board metadata                   |
| counter  | `REPO#<owner>/<repo>#BOARD#<id>`      | `COUNTER`                         | atomic per-board task-id counter |
| task     | `REPO#<owner>/<repo>#BOARD#<id>`      | `TASK#<zero-padded boardTaskID>`  | a task                           |
| link     | `REPO#<owner>/<repo>#BOARD#<id>`      | `LINK#<from>#<to>#<type>`         | a task link                      |

- **Atomic counter** replaces SQLite `AUTOINCREMENT`: `UpdateItem` with
  `ADD seq :one` and `ReturnValues=UPDATED_NEW` yields a unique, monotonic
  `1..N` id per board even under concurrent writers. Each repo tenant has its
  own counter (own PK prefix), so ids restart at `1..N` per repo.
- **Soft delete** is a `deleted_at` attribute; reads filter it out, matching the
  SQLite behavior. Hard delete removes the item and its links.

### Tenant isolation (Phase 3, active)

The partition prefix is set per request to `REPO#<owner>/<repo>#` (via
`dynamo.NewWithPrefix`, resolved from the validated JWT), so every key a request
touches is under that prefix. A request authorized for repo A can only ever
address `REPO#A#…` — it structurally cannot read or write repo B's items, with
**no change to the sort-key layout, item shape, or IAM**. The DynamoDB client is
cached process-wide and reused; only the prefix varies per tenant.

## Grants table / key design (Phase 4)

A **separate** table `cainban-grants` (on-demand, PITR, `RETAIN`) is the home
for per-user repo grants — the "real" grant store that supersedes the Cognito
`custom:repos` attribute (kept as a read fallback). It is keyed `(PK, SK)` but on
a **subject** axis, disjoint from the `cainban` data table's `REPO#…` axis:

| Item      | PK             | SK                       | Purpose                                             |
| --------- | -------------- | ------------------------ | --------------------------------------------------- |
| grant     | `USER#<sub>`   | `GRANT#<owner>/<repo>`   | one granted repo (presence = granted)               |
| default   | `USER#<sub>`   | `META`                   | `default_repo` attribute (the user's default repo)  |
| identity  | `USER#<sub>`   | `IDENTITY#github`        | linked GitHub identity/install id (reserved; P4.2/P4.3 populate) |

- `<sub>` is the Cognito `sub` (an agent principal, in a later phase, is keyed
  identically by its client-id). `owner/repo` is normalized via
  `auth.NormalizeRepo` (the same case-preserving canonicalization the resolver
  uses), so a grant maps to exactly one partition prefix.
- **Why separate from `cainban`:** grants are read on the token-minting path and
  share no partition with board data, so a separate table lets the pre-token
  Lambda's IAM be scoped to the grants ARN alone (least privilege — it
  structurally cannot read task data) and keeps PITR/backup boundaries clean.
  On-demand billing makes the two-table cost identical to co-tenanting (zero at
  rest).
- **Access:** `ListReposForSubject` is a single `Query` over the subject's
  partition (`begins_with(SK, "GRANT#")`); `GetDefaultRepo` is a `GetItem` on
  `META`. The pre-token Lambda uses only these two reads. The grant package
  (`src/systems/grants`) also exposes `PutGrant` / `DeleteGrant` /
  `SetDefaultRepo` for the P4.3 connect API to write verified grants (not used
  by the read path, no write IAM granted to the trigger).
