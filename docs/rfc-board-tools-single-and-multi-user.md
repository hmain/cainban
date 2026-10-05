# RFC: Make the board tools work in both single-user and multi-user modes

Status: Draft
Scope: `src/systems/mcp`, `src/systems/board`, `src/systems/store`, `src/systems/dynamo`
Related: `rfc-default-repo-headerless.md` (merged), the single-table key design in `src/systems/dynamo/dynamo.go`

## Summary

`list_boards` and `change_board` are the only two MCP tools that do **not** work
against the deployed (Lambda/DynamoDB, auth-scoped) server. Every other tool
resolves its backend per request through the `store.TaskStore` seam and works
identically in single-user (local CLI, SQLite) and multi-user (Lambda,
DynamoDB, repo-scoped tenant) modes. The two board tools bypass that seam and
read the local **filesystem** (`~/.cainban/*.db`), which does not exist on
Lambda — so `list_boards` returns `"No boards found"` and `change_board` returns
`board '<name>' not found` for every input, in the deployed server.

This RFC routes the two board tools through a backend-neutral `BoardStore` seam —
mirroring the existing `TaskStore` design — so they behave correctly in both
modes with no handler-level branching on deployment.

## Full tool audit (why only these two are affected)

Verified against `src/systems/mcp/server.go` (v0.2.1). Every tool is listed, not
just the board pair.

| Tool | Backend path | Single-user (SQLite) | Multi-user (DynamoDB) |
|---|---|---|---|
| `create_task` | `resolveTaskSystem` → `store.OpenTaskForTenant` | ✅ | ✅ |
| `list_tasks` | `resolveTaskSystem` | ✅ | ✅ |
| `get_task` | `resolveTaskSystem` | ✅ | ✅ |
| `update_task` | `resolveTaskSystem` | ✅ | ✅ |
| `update_task_status` | `resolveTaskSystem` | ✅ | ✅ |
| `update_task_priority` | `resolveTaskSystem` | ✅ | ✅ |
| `list_activity` | `resolveTaskSystem` | ✅ | ✅ |
| **`list_boards`** | **`s.boardSystem.ListBoards()` (filesystem scan)** | ✅ | ❌ returns "No boards found" |
| **`change_board`** | **`s.boardSystem.GetBoard()` (filesystem scan) + no-op** | ⚠️ validates only | ❌ returns "not found" |

The nine task/activity tools are correct in both modes because:

- `resolveTaskSystem` (`server.go`) selects the backend via `store.OpenTask` /
  `store.OpenTaskForTenant`, driven by `CAINBAN_BACKEND` (`sqlite` default,
  `dynamodb` on Lambda), and applies the request's tenant `partitionPrefix`
  (`REPO#<owner>/<repo>#`) when the context carries an authorized tenant.
- The handlers depend only on the `store.TaskStore` interface, so the SQLite and
  DynamoDB implementations are drop-in.

The two board tools are the leftover of the pre-store single-user design:

- `board.System` (`src/systems/board/board.go`) is a pure filesystem/SQLite
  model — `ListBoards()` scans `~/.cainban/cainban.db` and `~/.cainban/boards/*.db`;
  `GetBoard()` filters that scan; `SetCurrentBoard()` writes `~/.cainban/current-board`.
- `handleChangeBoard` is already a deliberate no-op with respect to server state
  (its own comment: *"board selection is now per-request … pass the board
  explicitly on each call"*). It only validates existence via the filesystem
  scan, which always fails on Lambda.

## Problem statement

Two true facts to reconcile:

1. **Single-user mode genuinely has multiple named boards.** The local CLI/TUI
   creates `~/.cainban/boards/<name>.db` files, and `change_board` writing
   `current-board` is a legitimate, useful stateful selection for one user on
   one machine.
2. **Multi-user mode is stateless and per-request.** There is no process-wide
   "current board"; each request is scoped to a tenant (repo) and passes
   `board_id` explicitly. A cross-client global `current-board` file would be a
   correctness bug (one client's selection would leak into another's requests).

So the tools must not have one behavior. They must resolve the same way the task
tools already do: **the backend decides**, selected per request.

## Design

### 1. Introduce a `store.BoardStore` seam (mirrors `TaskStore`)

Add a small interface in `src/systems/store/store.go`:

```go
// BoardSummary is the backend-neutral view of a board as the board tools expose it.
type BoardSummary struct {
    ID   int    `json:"id"`
    Name string `json:"name"`
}

// BoardStore lists and resolves the boards visible to a single request.
// It is scoped the same way TaskStore is: the DynamoDB implementation is built
// with the request's tenant partition prefix, so a request authorized for repo
// A never sees repo B's boards.
type BoardStore interface {
    // ListBoards returns every board in the current scope (tenant/partition for
    // DynamoDB; the local ~/.cainban tree for SQLite).
    ListBoards() ([]BoardSummary, error)
    // ResolveBoard maps a board selector (name or numeric id as string) to a
    // concrete board, or an error the caller can surface. Used by change_board.
    ResolveBoard(selector string) (BoardSummary, error)
}
```

Open it per request alongside the task store, honoring the same backend selector
and tenant prefix:

```go
func OpenBoardForTenant(ctx context.Context, sqlitePath, partitionPrefix string) (BoardStore, func() error, error)
```

### 2. Two implementations, chosen by `CAINBAN_BACKEND`

**SQLite (single-user)** — wrap the existing `board.System`. `ListBoards()`
returns the `~/.cainban` boards it already finds; `ResolveBoard(name)` reuses
`GetBoard(name)`. This is a thin adapter over code that already works, so
single-user behavior is preserved exactly.

**DynamoDB (multi-user)** — the single-table design already reserves the board
rows this needs. From the key-design header in `dynamo.go`:

```
PK = partitionPrefix + "BOARD#<boardID>"
SK = "META"   -> board metadata
```

`ListBoards()` queries the tenant's partition for `BOARD#*` / `SK=META` rows and
returns their `{id, name}`. `ResolveBoard(selector)` fetches the one
`BOARD#<id>#META` row (or matches on name). Today exactly one board (`id 1`)
exists per tenant, so `list_boards` returns `[{id:1, name:"default"}]` instead of
the misleading `"No boards found"`.

A `META` row for board 1 should be written lazily on first task create if absent
(a `BOARD#1#META` upsert in the DynamoDB `Create` path), so an existing tenant
with tasks but no explicit board row still lists board 1.

### 3. Rewire the two handlers

```go
func (s *Server) handleListBoards(ctx context.Context, ...) (...) {
    bs, closeFn, err := s.resolveBoardStore(ctx)   // mirrors resolveTaskSystem
    ...
    boards, err := bs.ListBoards()
    // format "Available boards:" / per-board bullets, else "No boards found"
}

func (s *Server) handleChangeBoard(ctx context.Context, args ChangeBoardArgs) (...) {
    bs, closeFn, err := s.resolveBoardStore(ctx)
    b, err := bs.ResolveBoard(args.BoardName)
    if err != nil { return ... "board %q not found in this scope" }
    // STILL a no-op on server state in multi-user mode: report the resolved
    // board + id and instruct the caller to pass board_id explicitly.
    // In SQLite mode it MAY additionally SetCurrentBoard for CLI/TUI parity.
}
```

`change_board` keeps its documented per-request semantics in multi-user mode —
it validates and reports, never mutating shared state — but now validates
against the **right** backend. In single-user mode it may still set the local
`current-board` for CLI/TUI continuity. The one tool, two correct behaviors,
chosen by the backend, not by a hand-coded branch in the handler.

### 4. `resolveBoardStore(ctx)` helper

Mirror `resolveTaskSystem`: default board path from `boardSystem.GetBoardPath`,
pull the tenant from context, fail closed on an `Unscoped` tenant with the same
"name a repo" message, and call `store.OpenBoardForTenant`. This keeps the
tenant-isolation guarantee identical to the task path (structural, not a filter).

## Repo correlation & auth

Boards are bound to a GitHub repo. The critical design point is that **this
binding is NOT re-derived in the board tools** — it is inherited, unchanged,
from the request's authorized tenant. Repo→board correlation and authorization
are decided exactly once per request in `AuthMiddleware` → `auth.Resolver.Resolve`
(`src/systems/auth/resolver.go`), *before any store is opened*, and the board
tools consume the result the same way the task tools already do. Any
board-specific authorization code would be a second, driftable copy of a
decision that is already made correctly upstream; this RFC deliberately adds
none.

### The chain that binds a board list to a repo

1. **Signature-first validation.** `Resolve` validates the bearer JWT signature
   before reading any claim. A bad/missing token is a 401 and never reaches a
   handler.
2. **Repo identity vs. authorization are separate, and only the token
   authorizes.** The *target* repo (identity) comes from the untrusted
   `X-Cainban-Repo` header → MCP tool arg → the token's `default_repo` claim.
   *Authorization* is `identity.authorizes(target)` against the signed `repos`
   claim. A named-but-ungranted repo is a **403**; the header/arg are never
   trusted to grant access.
3. **The authorized repo becomes a structural partition prefix.**
   `auth.PartitionPrefixFor("owner/repo")` → `REPO#owner/repo#`, carried on the
   `auth.Tenant` and attached to the request context by `withTenant`
   (`src/systems/mcp/tenant.go`).
4. **Board rows live under that same prefix.** The single-table key design is
   `PK = <partitionPrefix>BOARD#<id>`, `SK = META`. So a `ListBoards()` that
   queries `PK = <tenant.PartitionPrefix>BOARD#*` can only ever return the
   authorized repo's boards — a token for repo A physically cannot address repo
   B's board partition. Correlation is therefore the same structural isolation
   that already protects tasks, not a filter the board tool has to apply.

### Mandatory correctness rules for `resolveBoardStore`

These are not new guarantees — they are the task path's existing guarantees,
which the board path MUST match exactly:

1. **Fail closed on an `Unscoped` tenant (the single most important rule).**
   When a request names no repo and the token carries no `default_repo`,
   `Resolve` returns `Tenant{Unscoped: true}` with an EMPTY partition prefix.
   `resolveTaskSystem` already refuses to open a store on it, because an empty
   prefix would collapse every tenant into one partition. `resolveBoardStore`
   MUST perform the identical check and return the same "set the X-Cainban-Repo
   header or a default_repo claim" error — otherwise `list_boards` on an
   unscoped tenant would enumerate *every* tenant's boards. This is the one
   place a mistake becomes a cross-tenant data leak.
2. **Never trust the header/arg for the answer.** `list_boards` takes no repo
   argument and `change_board`'s `board_name` is a board selector, not a repo.
   Both tools use only the resolved `tenant.PartitionPrefix`; the repo was
   authorized upstream.
3. **Keep handshake ops open.** `initialize` / `tools/list` / `ping` /
   `notifications` run on an unscoped tenant by design, so a client whose token
   has no default repo can still discover tools. Only the *data* operation
   (opening the board store) fails closed. This matches the task path precisely.

### Single-user mode

The local CLI/stdio path sets no tenant (`tenantFromContext` returns false), so
the partition prefix is empty and the SQLite backend — which ignores the prefix
entirely — lists the local `~/.cainban` boards. There is no repo binding in
single-user mode and none is needed: one user, one machine, local files. The
same `resolveBoardStore` code therefore serves both modes with no branch on
deployment; the backend selector (`CAINBAN_BACKEND`) and the presence/absence of
a tenant do all the work.

## Alternatives considered

- **Remove the two tools entirely.** They are vestigial in multi-user mode, and
  `board_id` on `list_tasks`/`create_task` is the real axis. But removal changes
  the `tools/list` schema (clients and the TUI reference them), and single-user
  mode legitimately uses named boards. Rejected: it drops a working single-user
  feature and churns the schema.
- **Make `list_boards` hard-code `board 1` on DynamoDB.** Cheapest fix for the
  symptom, but it bakes the single-board assumption into the handler and blocks
  real multi-board support. Acceptable only as an interim; the `BoardStore` seam
  is the durable shape.
- **Keep the filesystem model and sync it to Lambda.** Lambda's filesystem is
  ephemeral and per-invocation; there is nothing to sync. Non-starter.

## Scope boundary

This RFC does **not** add board creation/deletion over MCP, multi-board task
routing, or a board-rename tool. It makes the two **existing** tools return
correct, scope-isolated results in both deployments. Creating named boards in
multi-user mode is a follow-up once the `BoardStore` write path exists.

## Verification plan

1. **Unit** — a fake DynamoDB `API` (the existing injectable interface in
   `dynamo.go`) returns `BOARD#1#META`; assert `ListBoards()` yields
   `[{1,"default"}]` and `ResolveBoard("default")`/`ResolveBoard("1")` succeed,
   `ResolveBoard("nope")` errors.
2. **Unit** — SQLite adapter over a temp `~/.cainban` with two board files lists
   both; preserves current single-user behavior.
3. **Tenant isolation** — a store built with prefix `REPO#A/x#` never returns
   `REPO#B/y#` board rows (the cross-tenant correlation guarantee).
4. **Fail closed on unscoped** — `resolveBoardStore` on a `Tenant{Unscoped:true}`
   (no repo named, no `default_repo`) returns the "name a repo" error and opens
   NO store; `list_boards` must not enumerate any boards. This is the
   cross-tenant-leak guard and gets its own dedicated test, mirroring the
   existing `tenant_unscoped_test.go` for the task path.
5. **Auth boundary (handler level)** — through `AuthMiddleware`: a missing/invalid
   token → 401 before `list_boards` runs; a token naming a repo it does not grant
   → 403; `tools/list` still succeeds on an unscoped tenant (handshake stays
   open).
6. **Schema** — `schema_test.go` still sees both tools with unchanged
   names/args.
7. **Live (multi-user)** — against the deployed MCP endpoint with an authorized
   token, `list_boards` returns board 1 (not "No boards found"); `change_board 1`
   resolves and reports per-request semantics; `change_board <repo-name>` fails
   with a clear scope message.
8. **Live (single-user)** — `cainban mcp --http` loopback: `list_boards` shows
   local boards; `change_board <name>` works as before.

## Rollout

Pure code change in `src/systems/{store,dynamo,board,mcp}`; no infra/CDK change
(the DynamoDB table and key design already accommodate `BOARD#*#META` rows).
Ship behind the normal build → lambda-bundle → full test gates; the lazy
`BOARD#1#META` upsert is backward-compatible with existing tenants that have
tasks but no board row.
