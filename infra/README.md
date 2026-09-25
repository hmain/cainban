# cainban infra (Phase 3 CDK)

AWS CDK (Go) app that provisions the serverless stack for cainban:

- **DynamoDB** single table `cainban` (on-demand, PITR, `RETAIN` on delete)
- **Lambda** `cainban-mcp` — `provided.al2023`, **arm64**, pure-Go bootstrap
  built from [`cmd/cainban-lambda`](../cmd/cainban-lambda) (serves the stateless
  Streamable-HTTP MCP handler, wrapped in **signature-first JWT auth +
  repo-scoped tenancy**)
- **Cognito user pool** `cainban-users` + app client — issues the JWTs the
  Lambda validates; carries the `repos` / `default_repo` authorization claims
- **Pre-token-generation trigger** `cainban-pretoken` — a pure-Go arm64 Lambda
  attached to the user pool that maps each user's `custom:repos` /
  `custom:default_repo` attributes into the **top-level** `repos` /
  `default_repo` claims the validator authorizes against (see
  [Granting a user access to a repo](#granting-a-user-access-to-a-repo))
- **Lambda Function URL** — **`AuthType: AWS_IAM`** (edge auth; no anonymous
  reachability)
- **IAM** least-privilege: only `GetItem`, `PutItem`, `UpdateItem`,
  `DeleteItem`, `Query` on the `cainban` table ARN
- **CloudWatch** log group `/aws/lambda/cainban-mcp` (30-day retention)

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

The CDK stack packages `../.build/lambda` (the MCP handler) and
`../.build/pretoken` (the Cognito pre-token trigger) via `Code.fromAsset`. Build
both before any synth/deploy:

```sh
# from the repo root
make bundles          # builds .build/lambda + .build/pretoken
# or individually:
make lambda           # => .build/lambda/bootstrap
make pretoken         # => .build/pretoken/bootstrap
# both: CGO_ENABLED=0 GOOS=linux GOARCH=arm64, lambda.norpc
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

After deploy, the stack outputs `FunctionUrl` (the MCP endpoint), `TableName`,
`UserPoolId` and `UserPoolClientId`. Smoke test with a `tools/list` call and one
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
and, during token generation, reads the user's `custom:repos` /
`custom:default_repo` attributes and returns `claimsToAddOrOverride`:

| Top-level claim it emits | Value shape                                                        |
| ------------------------ | ------------------------------------------------------------------ |
| `repos`                  | a **JSON-array-encoded string** of `owner/repo`, e.g. `["acme/a","acme/b"]` (Cognito claim-override values are always strings) |
| `default_repo`           | the user's default repo (string), when `custom:default_repo` is set |

The validator's `repos`-claim decoder accepts **both** a native JSON array (used
by the self-signed test tokens) and this string shape (JSON-array-encoded, or
space/comma-delimited), so the trigger's output flows straight through
authorization. The trigger is a **pure reflector** of the user's stored
attributes: if `custom:repos` is empty/absent it emits **no** `repos` claim and
the user has no grants (→ 403) — it never invents a grant. It needs no
permissions beyond basic CloudWatch Logs (it reads only what Cognito hands it in
the event), and needs no extra store or table.

### Granting a user access to a repo

Grants live entirely in the user's Cognito `custom:repos` attribute (a
space-, comma-, or JSON-array-delimited list of `owner/repo`), with an optional
`custom:default_repo`. An operator sets them with the Cognito admin API — no
deploy, no code change:

```sh
export AWS_PROFILE=aws-test-hamin AWS_REGION=eu-north-1
# grant a user two repos + a default (space-delimited is fine; JSON array also accepted)
aws cognito-idp admin-update-user-attributes \
  --user-pool-id <UserPoolId> \
  --username <user-email-or-sub> \
  --user-attributes \
      Name=custom:repos,Value="acme/repo-a acme/repo-b" \
      Name=custom:default_repo,Value="acme/repo-a"
```

The change takes effect on the user's **next token** (the trigger runs at token
generation), so the user re-authenticates / refreshes to pick up a new grant.
To **revoke** access, remove the repo from `custom:repos`. A future option (not
built — frugal for now) is a separate grants table the trigger reads instead of
the attribute; the custom attribute needs zero extra infrastructure.

## Storage backend selector

The application chooses its backend from `CAINBAN_BACKEND`:

| `CAINBAN_BACKEND` | Backend    | Notes                                             |
| ----------------- | ---------- | ------------------------------------------------- |
| unset / `sqlite`  | SQLite     | Local CLI/TUI. Requires CGO.                      |
| `dynamodb`        | DynamoDB   | Serverless path. Pure Go. Used by the Lambda.     |

Extra DynamoDB env vars: `CAINBAN_DDB_TABLE` (default `cainban`),
`CAINBAN_DDB_REGION` (falls back to the standard AWS region resolution). The
Lambda sets all three via the CDK `Environment` block.

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
