# cainban: stateless MCP → AWS serverless → multi-user (team kanban) plan

Status: draft · Owner: default (Kiro) · Last updated: 2026-09-25

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
- **Region/account:** TBD by user at deploy time.

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

1. **Storage swap (the real work):** replace SQLite with **DynamoDB** behind the
   existing `task.System` / `board.System` interfaces so handlers don't change.
   - Tables (single-table or multi-table TBD): boards, tasks, task_links.
   - Replace SQLite AUTOINCREMENT for `board_task_id` with an **atomic counter**
     item per board (DynamoDB `UpdateItem ADD`).
   - After this, `CGO_ENABLED=0` (pure Go) — required for a clean Lambda build.
2. **Compute:** Lambda `provided.al2023`, ARM64, serving the stateless
   Streamable-HTTP handler (one `Server` per request via `getServer`).
3. **Edge:** Lambda Function URL or API Gateway HTTP API (decide on auth needs).
4. **CDK stack** (Go CDK or TS): DynamoDB tables, Lambda, URL/APIGW, IAM
   least-privilege, CloudWatch logs.
5. Deploy discipline: `cdk diff` first; after deploy verify Lambda `LastModified`
   advanced and smoke-test a `tools/list` + one tool call.

Exit criteria: a public (auth-gated in Phase 3) endpoint speaks MCP; data
persists in DynamoDB; no local disk.

---

## Phase 3 — Multi-user, shared team kanban keyed by repo

Goal: many users, each repo one shared board, no cross-tenant access.

1. **Identity:** bearer/JWT (Cognito or existing IdP) validated at the edge.
   Handlers read the caller via `RequestExtra.TokenInfo` / request headers.
2. **Tenancy key = repo (shared):**
   - Partition key `REPO#<owner>/<repo>`; all collaborators of a repo share one
     board partition.
   - Sort keys namespace boards/tasks/links/counter within the repo partition.
   - Repo identity derived from the client (git remote) or passed explicitly per
     call; validated against the caller's authorization.
3. **Authorization:** every handler is scoped to `REPO#<owner>/<repo>`; a caller
   may only touch repos they're authorized for. No global board list leakage.
4. **Verify:** an isolation test proving user A cannot read/write user B's repo
   board, and that two collaborators on the same repo see the same board.

Exit criteria: two users on the same repo share a board; a user with no access
to a repo is denied; all access is repo-scoped.

---

## Cross-cutting notes / risks

- SQLite→DynamoDB (CGO removal) is the dominant serverless risk; Phase 2 is
  mostly a storage port, not a transport change.
- Keep `task.System` / `board.System` as the seam — swapping the backend behind
  them keeps the MCP handlers stable across all three phases.
- Local dev: retain the SQLite path for `cainban tui` / CLI, or provide a local
  DynamoDB (dynamodb-local) profile — decide in Phase 2.
