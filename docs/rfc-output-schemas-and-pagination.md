# RFC: MCP output schemas and list pagination

Status: proposed
Depends on: #51 (BoardStore), #53 (whoami/scope header), #55 (annotations/isError),
#56 (delete/restore/search), #57 (links) — the full 16-tool surface is merged and live.

## Background

The best-practices RFC (`docs/rfc-mcp-server-best-practices.md`) landed its two
Medium-severity items — tool annotations and `isError` semantics (#55) — and
deferred three Low-severity ones. Two of those three remain and are addressed
here: **output schemas** and **list pagination**. (The third, human `Title`
fields, was in practice delivered through `annotations.title` in #55 and needs no
further work.)

Both gaps are quality/robustness, not correctness: every tool works today. They
matter as the board grows and as strict MCP clients begin validating structured
output against a declared schema.

## Gap 1 — No tool declares an `OutputSchema`

### What the code does today

The server uses the official `github.com/modelcontextprotocol/go-sdk v1.8.0`.
Its `AddTool`/`ToolHandlerFor[In, Out]` generic derives an **output** schema
automatically from the `Out` type parameter — but ONLY when `Out` is not the
empty interface `any` (SDK `tool.go`: *"If the Out type is not the empty
interface [any], it provides the default output schema for the tool"*).

Every cainban handler is declared with `Out = any`:

```go
func (s *Server) handleListTasks(ctx context.Context, req *mcp.CallToolRequest, args ListTasksArgs) (*mcp.CallToolResult, any, error)
func (s *Server) handleGetTask(...)      (*mcp.CallToolResult, any, error)
func (s *Server) handleListBoards(...)   (*mcp.CallToolResult, any, error)
func (s *Server) handleWhoami(...)       (*mcp.CallToolResult, any, error)
func (s *Server) handleSearchTasks(...)  (*mcp.CallToolResult, any, error)
func (s *Server) handleGetTaskLinks(...) (*mcp.CallToolResult, any, error)
```

So even though these handlers DO return a typed value as the second result
(`[]*task.Task`, `whoamiResult`, `[]task.TaskLink`), the SDK sees `any` and emits
no output schema. A live `tools/list` confirms it: **every tool has
`inputSchema` but none has `outputSchema`.**

### Why it matters

A tool that returns `structuredContent` SHOULD declare an `outputSchema` so a
client can validate the shape and present typed fields. Without it, the
structured content is opaque — a client must treat it as untyped JSON. This is
exactly the class of tools cainban has: `list_tasks`, `get_task`, `list_boards`,
`whoami`, `search_tasks`, `get_task_links`, `list_activity` all return typed
structured content.

### Proposal

Change the six (plus `list_activity`) read handlers' `Out` type parameter from
`any` to the concrete type they already return, and let the SDK derive the
schema. For example:

```go
// before
func (s *Server) handleListTasks(...) (*mcp.CallToolResult, any, error)
// after
func (s *Server) handleListTasks(...) (*mcp.CallToolResult, []*task.Task, error)
```

Handlers that return `nil` as the typed value on an error/empty path must return
the zero value of the concrete type instead (`nil` slice is fine; a nil pointer
for a struct return is fine). The `isError` tool-result paths are unaffected —
they set `IsError` on the `*CallToolResult` and the output value is ignored.

Tools whose only output is a human string and carry no meaningful structured
content (`create_task`, the `update_*` family, `delete_task`, `restore_task`,
`link_tasks`, `unlink_tasks`, `change_board`) keep `Out = any` — there is no
typed payload to describe, and inventing a `{message: string}` wrapper would add
schema noise without value. This is a deliberate split, documented in the code.

One wrinkle to verify: the SDK derives the schema from the Go type via
reflection + `jsonschema`. `task.Task` has a `*time.Time` field (`DeletedAt`)
and `time.Time` fields; the generated schema must round-trip those as strings.
The verification plan includes asserting the derived schema marshals and that a
real response validates against it.

## Gap 2 — No pagination on `list_tasks` / `list_activity`

### What the code does today

`store.TaskStore` exposes:

```go
List(boardID int) ([]*task.Task, error)
ListByStatus(boardID int, status task.Status) ([]*task.Task, error)
ListActivity(boardID, boardTaskID, limit int) ([]task.ActivityEvent, error)
```

- **DynamoDB** `List`/`ListActivity` loop over `Query` pages internally until
  `LastEvaluatedKey` is empty, accumulating **every** row, then (for activity)
  sort newest-first and the handler caps at `limit`. `list_tasks` returns the
  whole board unconditionally.
- **SQLite** `List` is a single `SELECT ... ORDER BY priority DESC, board_task_id
  ASC` with no `LIMIT`/`OFFSET`.

So the server already drains all pages server-side; the cursor the backend
produces (`LastEvaluatedKey`) is consumed internally and never surfaced to the
caller.

### Why it is low priority today

A single board holds a handful of tasks; `list_activity` already clamps to <=200
events. Pagination earns its complexity only when a board (or one tenant's
activity feed) grows past a single reasonable response — not the case now. This
RFC therefore proposes the DESIGN and defers the BUILD until a board approaches
that size, so the seam is agreed before it is needed.

### Proposal (design, build deferred)

Introduce an opaque cursor on the two list tools, carried as optional args and
returned in the structured output:

```
list_tasks    args: { board_id?, status?, limit?, cursor? }
list_activity args: { task_id?, limit?, cursor? }
```

- `limit` bounds one page (default 50, max 200, matching `clampActivityLimit`).
- `cursor` is an **opaque, base64url** token the server minted on the previous
  page; the client passes it back verbatim to get the next page. It is never
  constructed by the client and its internals are not part of the contract.
- The response's structured content gains a `next_cursor` field (empty/absent on
  the last page).

Backend seam — extend `TaskStore` with cursor-aware variants rather than
changing the existing signatures (keeping the CLI/TUI callers untouched):

```go
ListPage(boardID int, status task.Status, limit int, cursor string) (items []*task.Task, next string, err error)
ListActivityPage(boardID, boardTaskID, limit int, cursor string) (events []task.ActivityEvent, next string, err error)
```

- **DynamoDB**: the cursor encodes the `LastEvaluatedKey` map (JSON →
  base64url). A page is one `Query` call (no internal drain loop); `next` is set
  when `LastEvaluatedKey` is non-empty. The `boardTaskID` filter on activity
  must move server-side into the Query where possible, or the cursor must encode
  the post-filter position — the verification plan covers this.
- **SQLite**: the cursor encodes `(priority, board_task_id)` of the last row for
  a keyset `WHERE (priority, board_task_id) < (?, ?)` continuation (keyset, not
  `OFFSET`, so inserts don't shift pages). `next` is set when a full page was
  returned.

Cursor opacity is a hard rule: the token is backend-specific (a DynamoDB key map
vs. a SQLite keyset tuple), so a client that cracks it open couples to the
backend. Encode a one-byte version tag so a future format change is detectable.

### Scope boundary

- Read-only. No change to task/board state, auth, keys, or tool names.
- `TestToolsListStable` stays valid — no tools added or renamed; new OPTIONAL
  args and output fields do not change the tool set.
- Pagination is DESIGNED here and BUILT in a follow-up gated on board size;
  output schemas are BUILT now (small, high-value, no new surface).

## Alternatives considered

- **Leave output schemas unset** — rejected: declaring them is nearly free (flip
  `Out` from `any` to the concrete type) and is what a strict client expects.
- **Hand-write `OutputSchema` JSON per tool** — rejected: the SDK derives it from
  the Go type; hand-writing it would drift from the actual return type.
- **`OFFSET` pagination for SQLite** — rejected: `OFFSET` shifts pages when rows
  are inserted/deleted between calls; keyset pagination is stable.
- **Expose the raw `LastEvaluatedKey` as the cursor** — rejected: it leaks the
  DynamoDB backend into the wire contract; an opaque versioned token keeps the
  backend swappable.
- **Build pagination now** — rejected: YAGNI at current board sizes; the design
  is agreed here so the build is mechanical when a board grows.

## Verification plan

1. Output schema present: `tools/list` shows a non-empty `outputSchema` on
   `list_tasks`, `get_task`, `list_boards`, `whoami`, `search_tasks`,
   `get_task_links`, `list_activity`; absent on the string-only writers.
2. Schema round-trips the `time.Time`/`*time.Time` fields — a real `list_tasks`
   response validates against the derived schema (SDK validates output when the
   schema is set).
3. Full `-race` suite green via the gcc toolchain
   (`CC=.../gccwrap.sh CGO_ENABLED=1`), both backends.
4. `TestToolsListStable` unchanged and still passing (no tool-set change).
5. Lambda bundle builds CGO-free; `strings` confirms the changed handlers.
6. (Pagination, when built) a seeded >limit board returns a `next_cursor`, the
   cursor fetches the next page, pages do not overlap or skip, and the last page
   has an empty cursor — on BOTH backends.
7. (Pagination, when built) a cursor minted by one backend is rejected cleanly
   if replayed against the other (version-tag mismatch), never silently
   mis-paged.
