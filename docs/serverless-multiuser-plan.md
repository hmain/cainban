# cainban: stateless MCP → AWS serverless → multi-user (team kanban) plan

Status: draft · Owner: default (Kiro) · Last updated: 2026-09-25 (Phase 3 built)

This is a staged plan. Each phase is an independent PR that leaves `main`
deployable. Ordering is deliberate: statelessness is the precondition for
serverless, and serverless (per-request server + auth context) is the
precondition for clean multi-user.

## Decisions locked

- **Tenancy model: shared team kanban.** A repo's board is shared by every
  authenticated user with access to that repo. Partition key is the repo, not
  the user.
- **MCP SDK:** `github.com/modelcontextprotocol/go-sdk` → `v1.8.0`.
- **IaC:** AWS CDK (per user preference; avoid SAM).
- **Region/account:** AWS profile `aws-test-hamin` (aws-test-hamin dev account), region `eu-north-1`.

## Current state (verified 2026-09-25)

- MCP server: `src/systems/mcp/server.go`, 8 tools, handlers already match the
  v1.8.0 signature `func(ctx, *mcp.CallToolRequest, args) (*mcp.CallToolResult, any, error)`.
- Transport: **stdio only** (`server.Start()` → `mcp.StdioTransport`), started
  from `handleMCP()` in `cmd/cainban/main.go`.
- **Ambient state:** board selection lives in `~/.cainban/current-board` (a file),
  read/written by `board.System`. `handleMCP()` resolves the DB **once at process
  start**; four handlers (`update_task_status`, `get_task`, `update_task`,
  `update_task_priority`) hardcode `boardID := 1`. `change_board` is a global,
  cross-client side effect.
- **Storage:** `mattn/go-sqlite3` (CGO, local disk, WAL). This is the biggest
  serverless blocker.
- `board_task_id` is a per-board sequence backed by SQLite AUTOINCREMENT logic.

---

## Phase 1 — Stateless MCP on go-sdk v1.8.0

Goal: same tools, no server-side ambient state, add a stateless HTTP transport,
keep stdio as the default.

1. Bump `go-sdk v0.5.0 → v1.8.0`; fix any API drift (`go build ./...`).
2. Remove ambient board state:
   - Thread `board_id` (and/or `board_name`) into the four hardcoded handlers.
   - Open/resolve the correct board DB **per request**, not once at process start.
   - `change_board` becomes a no-op or is removed; delete reliance on the
     `current-board` file for request routing.
3. Add HTTP transport: `cainban mcp --http :PORT` using
   `mcp.NewStreamableHTTPHandler(getServer, &mcp.StreamableHTTPOptions{Stateless: true})`
   with a shared `mcp.SchemaCache`. **stdio stays the default** (`cainban mcp`).
   Bind to `127.0.0.1` locally.
4. Verify: `go build`, `go test ./...`, and confirm the `tools/list` wire schema
   is unchanged (diff against a captured baseline).

Exit criteria: stdio behaviour unchanged; HTTP mode serves the same tools;
no shared mutable process state across requests.

---

## Phase 2 — AWS serverless (CDK)

Goal: run the Phase 1 stateless HTTP handler on Lambda with durable, non-local
storage.

### Status: BUILT (branch `feat/phase2-dynamodb-cdk`) — NOT deployed

The plan below was implemented as described. Summary of what shipped:

1. **Storage swap (done).** A backend-neutral `store.TaskStore` interface
   (`src/systems/store`) is the seam the MCP handlers depend on. Two backends
   satisfy it:
   - SQLite `task.System` (unchanged, CGO) — local `cainban` CLI/TUI.
   - DynamoDB `dynamo.Store` (`src/systems/dynamo`, pure Go, aws-sdk-go-v2) —
     the serverless path.
   A **selector** (`CAINBAN_BACKEND=sqlite|dynamodb`, default `sqlite`) picks the
   backend at runtime; the Lambda forces `dynamodb`. Handlers were **not**
   changed beyond swapping the concrete return type of `resolveTaskSystem` for
   the interface.
   - **Single table** `cainban`, keyed `(PK, SK)` — chosen over three tables so a
     board's counter + tasks + links stay co-located in one partition (cheaper,
     single-partition consistency, one IAM/infra surface).
     - `PK = BOARD#<id>` (Phase 2), `SK` namespaces items: `META`, `COUNTER`,
       `TASK#<padded id>`, `LINK#<from>#<to>#<type>`.
   - SQLite `AUTOINCREMENT` for `board_task_id` is replaced by an **atomic
     counter** item per board: `UpdateItem ADD seq :one` with
     `ReturnValues=UPDATED_NEW`, giving unique monotonic `1..N` ids per board
     under concurrency.
   - Soft-delete (`deleted_at`) and `task_links` relationships are preserved.
   - Lambda code path is `CGO_ENABLED=0` (pure Go), verified.
2. **Compute (done).** New `cmd/cainban-lambda`: Lambda `provided.al2023`,
   **arm64**, serving the same stateless Streamable-HTTP handler
   (`mcp.Server.Handler`, one `*mcp.Server` per request via `getServer`), adapted
   to a Function URL event via `aws-lambda-go-api-proxy`.
3. **Edge (done).** Lambda **Function URL**, `AuthType: NONE` — a TEMPORARY,
   dev-only, unauthenticated endpoint. Auth is deferred to Phase 3.
4. **CDK stack (done).** Go CDK app in `infra/`: DynamoDB table (on-demand,
   PITR, RETAIN), arm64 Lambda, Function URL, **least-privilege IAM** (only
   `GetItem`/`PutItem`/`UpdateItem`/`DeleteItem`/`Query` on the table ARN),
   explicit CloudWatch log group. `cdk synth` verified; **not deployed**.
5. Deploy discipline (documented, un-run): see `infra/README.md` for the exact
   `cdk bootstrap` / `cdk diff` / `cdk deploy` commands for profile
   `aws-test-hamin`, region `eu-north-1`.

**ID model note:** DynamoDB has no global auto-increment, so in the
single-board/single-tenant Phase 2 world `Task.ID == Task.BoardTaskID` (the
user-visible `#N`). Task links reference that same `1..N` number, which is what
the CLI link commands already pass.

**Backend selector:** `CAINBAN_BACKEND` (`sqlite` default | `dynamodb`), plus
`CAINBAN_DDB_TABLE` (default `cainban`) and `CAINBAN_DDB_REGION`.

**Verification (local, no cloud):** `CGO_ENABLED=0 go build ./...`, `go vet`,
`gofmt -l`, `golangci-lint run` (v2.14.0), DynamoDB backend tests against an
in-memory fake aws-sdk-go-v2 client (host has no Docker/JVM for DynamoDB Local),
existing SQLite CLI/TUI tests (CGO), and `cdk synth`.

Exit criteria: a public (auth-gated in Phase 3) endpoint speaks MCP; data
persists in DynamoDB; no local disk. *(Endpoint is defined but not yet
deployed.)*

---

## Phase 3 — Multi-user, shared team kanban keyed by repo

Goal: many users, each repo one shared board, no cross-tenant access.

### Status: BUILT (branch `feat/phase3-auth-multitenant`) — NOT deployed

What shipped:

1. **Identity / authn (signature-first).** A new `src/systems/auth` package
   validates a bearer **JWT** on every HTTP/Lambda request, in this strict order
   (`auth.Validator.Validate`):
   1. **Signature** verified against the IdP **JWKS** (RS256/384/512; `alg=none`
      and unknown algs are rejected — no downgrade), then **issuer**,
      **audience** and **expiry** (`exp`/`nbf`) checked.
   2. Only **after** (1) passes are claims read.
   A missing/invalid/expired/unsigned/wrong-key/wrong-issuer/wrong-audience
   token is a **401** and **no tenant is resolved** (so no store is ever
   opened). No claim-based decision is made before signature validation.
   The `AuthMiddleware` (`src/systems/mcp/tenant.go`) wraps the stateless MCP
   handler and is the only entrypoint on the Lambda (`mcp.HandlerWithAuth`).

2. **IdP: Amazon Cognito user pool** (in the CDK stack), issuing the JWTs. Chosen
   because it is self-contained and frugal for this dev account (no cost at rest)
   and needs no external federation to stand up. It is **swappable**: the Lambda
   only consumes an OIDC **issuer + audience + JWKS URL** via
   `CAINBAN_AUTH_ISSUER` / `CAINBAN_AUTH_AUDIENCE` / `CAINBAN_AUTH_JWKS_URL`, so
   pointing at an existing IdP later is a config change, not a code change.
   - *Why in-Lambda JWKS validation, not an API Gateway JWT authorizer:* the
     signature-first ordering is the security core of this phase and must be
     **unit-testable locally with a mock JWKS** — the in-Lambda validator is;
     a managed authorizer is not. It also avoids adding an API Gateway surface
     for no functional gain over the Function URL the Phase 2 transport already
     targets.

3. **Tenancy key = repo (shared team board).** After auth, the request resolves
   to exactly one authorized repo and the store is built with that repo's
   DynamoDB partition prefix `REPO#<owner>/<repo>#` via
   `dynamo.NewWithPrefix` (the hook Phase 2 reserved) — wired through
   `store.OpenTaskForTenant` and a per-request tenant carried in the request
   context. The DynamoDB **client is cached and reused** across requests; only
   the prefix varies per tenant. The stdio/local CLI path sets no tenant
   (empty prefix, single-tenant SQLite) — **auth applies only to the
   HTTP/Lambda path**.

4. **Repo IDENTITY vs AUTHORIZATION (where each comes from):**
   - **Identity** (which repo a request targets): the client-supplied target —
     an MCP tool arg, else the `X-Cainban-Repo` header, else the token's
     `default_repo` claim. This is **untrusted on its own**: it only names a
     repo.
   - **Authorization** (may this caller touch that repo): the **validated
     `repos` claim** inside the signature-checked JWT. Access is granted **iff**
     the target repo is a member of that claim. A valid token targeting a repo
     it does not grant is a **403**. The client-supplied target is never trusted
     for authorization; it can only *narrow* to a repo the token already grants,
     never escalate.

5. **Isolation is structural.** Every DynamoDB key for a request is built under
   its tenant prefix, so a request authorized for repo A can only ever address
   PK values under `REPO#A#…` — it cannot read or write repo B's items. This is
   a key-space property, not a runtime filter that could be bypassed. IAM stays
   least-privilege on the one table (prefixing is a data-model concern, same
   table and actions).

6. **Edge auth (CDK).** The Phase 2 Function URL `AuthType: NONE` is **replaced
   by `AWS_IAM`** — the endpoint rejects any request not SigV4-signed by an
   allowed principal, so **no anonymous reachability remains** (defence in depth
   on top of the in-Lambda JWT). The Cognito user pool + app client are added to
   the stack; the Lambda gets `CAINBAN_AUTH_ISSUER`/`CAINBAN_AUTH_AUDIENCE`.
   Still **NO deploy** — `cdk synth` verified only.

### The 5 verified scenarios (unit tests, mock DynamoDB + self-signed JWKS)

| # | Scenario | Test | Result |
|---|----------|------|--------|
| a | valid token for repo A → CRUD on repo A | `auth.TestResolve_ValidTokenAuthorizedRepo`, `dynamo.TestTenantIsolation_*` | PASS |
| b | valid token **without** repo A access → 403 | `auth.TestResolve_ValidTokenUnauthorizedRepo`, `mcp.TestAuthMiddleware_UnauthorizedRepo403` | PASS |
| c | invalid/expired/unsigned token → 401, no store access | `auth.TestValidate_Rejects*`, `auth.TestResolve_InvalidTokenNoTenant`, `mcp.TestAuthMiddleware_{Missing,Invalid}Token401` | PASS |
| d | isolation: caller scoped to A cannot touch B | `dynamo.TestTenantIsolation_AcannotSeeB`, `auth.TestResolve_TenantPrefixIsolation` | PASS |
| e | two users, same repo → same board | `dynamo.TestTenantIsolation_SameRepoSharedBoard` | PASS |

Also covered: `alg=none` downgrade rejected, payload-tamper rejected, wrong
signing key rejected, wrong issuer/audience rejected, client-supplied target can
only narrow within the validated claim (never escalate).

Exit criteria (met): two users on the same repo share a board; a user with no
access to a repo is denied (403); an unauthenticated request is denied (401);
all access is repo-scoped. **Endpoint is defined and authenticated but NOT
deployed** — deploy is a separate approved step.

---

## Cross-cutting notes / risks

- SQLite→DynamoDB (CGO removal) is the dominant serverless risk; Phase 2 is
  mostly a storage port, not a transport change.
- Keep `task.System` / `board.System` as the seam — swapping the backend behind
  them keeps the MCP handlers stable across all three phases.
- Local dev: retain the SQLite path for `cainban tui` / CLI, or provide a local
  DynamoDB (dynamodb-local) profile — decide in Phase 2.
