# cainban infra (Phase 2 CDK)

AWS CDK (Go) app that provisions the Phase 2 serverless stack for cainban:

- **DynamoDB** single table `cainban` (on-demand, PITR, `RETAIN` on delete)
- **Lambda** `cainban-mcp` — `provided.al2023`, **arm64**, pure-Go bootstrap
  built from [`cmd/cainban-lambda`](../cmd/cainban-lambda) (serves the stateless
  Streamable-HTTP MCP handler)
- **Lambda Function URL** — **`AuthType: NONE` (UNAUTHENTICATED)**
- **IAM** least-privilege: only `GetItem`, `PutItem`, `UpdateItem`,
  `DeleteItem`, `Query` on the `cainban` table ARN
- **CloudWatch** log group `/aws/lambda/cainban-mcp` (30-day retention)

> **The Function URL is intentionally unauthenticated in Phase 2.** It is a
> TEMPORARY, dev-only endpoint for smoke testing. **Do not expose it to
> untrusted callers or use it in production.** Phase 3 adds auth-gated,
> repo-scoped tenancy (see [`docs/serverless-multiuser-plan.md`](../docs/serverless-multiuser-plan.md)).

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

## Build the Lambda bundle FIRST

The CDK stack packages `../.build/lambda` via `Code.fromAsset`. Build it before
any synth/deploy:

```sh
# from the repo root
make lambda
# => .build/lambda/bootstrap  (CGO_ENABLED=0 GOOS=linux GOARCH=arm64, lambda.norpc)
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

After deploy, the stack outputs `FunctionUrl` (the MCP endpoint) and
`TableName`. Smoke test with a `tools/list` call and one tool call, then verify
the Lambda `LastModified` advanced.

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

Single table, keyed `(PK, SK)`:

| Item     | PK (Phase 2)     | SK                                | Purpose                          |
| -------- | ---------------- | --------------------------------- | -------------------------------- |
| board    | `BOARD#<id>`     | `META`                            | board metadata                   |
| counter  | `BOARD#<id>`     | `COUNTER`                         | atomic per-board task-id counter |
| task     | `BOARD#<id>`     | `TASK#<zero-padded boardTaskID>`  | a task                           |
| link     | `BOARD#<id>`     | `LINK#<from>#<to>#<type>`         | a task link                      |

- **Atomic counter** replaces SQLite `AUTOINCREMENT`: `UpdateItem` with
  `ADD seq :one` and `ReturnValues=UPDATED_NEW` yields a unique, monotonic
  `1..N` id per board even under concurrent writers.
- **Soft delete** is a `deleted_at` attribute; reads filter it out, matching the
  SQLite behavior. Hard delete removes the item and its links.

### Phase 3 readiness (no redesign needed)

The partition key is constructed with a prefix that is empty in Phase 2. Phase 3
sets it to `REPO#<owner>/<repo>#` (see `dynamo.NewWithPrefix`), so every board
partition becomes `REPO#<owner>/<repo>#BOARD#<id>` **without any change to the
sort-key layout, item shape, or IAM** — exactly the tenancy model in the plan
doc's Phase 3 section.
