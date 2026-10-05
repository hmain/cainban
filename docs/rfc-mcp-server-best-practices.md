# RFC: MCP server best-practices — tool annotations, error semantics, output schemas

Status: Draft
Scope: `src/systems/mcp` (tool registration + handler return conventions)
Related: `rfc-board-tools-single-and-multi-user.md` (#51),
`rfc-surface-current-repo-and-board-scope.md` (#52, whoami + scope header)
SDK: `github.com/modelcontextprotocol/go-sdk v1.8.0`
Spec: MCP Tools, version 2026-07-28 (modelcontextprotocol.io)

## Summary

cainban's MCP server is sound on the load-bearing points — it is stateless with
respect to request routing, resolves auth per request (never as connection
state), returns structured content alongside text, and advertises a
deterministic tool set with input schemas. This RFC closes the remaining
best-practice gaps against the MCP Tools spec and the go-sdk v1.8.0 API, in
priority order:

1. **Tool annotations** (`readOnlyHint` / `destructiveHint` / `idempotentHint` /
   `title`) on every tool — missing today, so a client cannot tell a read from a
   destructive write.
2. **Error semantics** — "expected" outcomes (task not found, invalid status)
   are returned as JSON-RPC *transport* errors; they SHOULD be `isError: true`
   *tool results* so the model can see and react, following the pattern the
   version-conflict path already uses correctly.
3. **Output schemas** on the tools that return structured content.
4. **Minor**: human-readable `title`s; a pagination story for list tools.

None of these change the data model, the auth model, or the DynamoDB keys. They
change how an LLM client *reasons about and recovers from* the tools — which is
the point of the annotations and error-result parts of the spec.

## Current state (audited)

Verified in `src/systems/mcp/server.go` against the spec and the SDK structs.

**Conformant already:**
- Stateless per-request server (`getServer` builds a fresh `*mcp.Server`); no
  shared "current board". Matches "MUST NOT vary as a side effect of other
  requests."
- Per-request auth as input, not connection state — matches the spec's "MAY vary
  by the authorization presented on the request."
- Structured content returned as the handler's second value on
  `list_tasks` / `list_boards` / `list_activity` / `whoami`.
- Input schemas present on every tool (guarded by `TestToolInputSchemasPresent`).
- Optimistic-concurrency conflicts use `isError: true` tool results
  (`versionConflictResult`, server.go:~577) — the correct pattern.

**Gaps:**

| # | Gap | Evidence | Severity |
|---|---|---|---|
| 1 | No tool annotations on any tool | no `Annotations:` in any `mcp.AddTool` | Medium |
| 2 | "Expected" failures are transport errors, not `isError` results | `return nil, nil, fmt.Errorf(...)` at server.go:476, 526, 540, 597, 611, 624, 670, 756 | Medium |
| 3 | No `OutputSchema` on structured-content tools | no `OutputSchema` set | Low |
| 4 | No `Title` display names | no `Title`/`annotations.title` | Low |
| 5 | No pagination on list tools | `list_tasks`/`list_activity` return all rows (activity caps at 200) | Low today |

## Design

### 1. Tool annotations (Medium — highest value)

The go-sdk v1.8.0 `ToolAnnotations` struct provides exactly the hint fields the
spec defines:

```go
type ToolAnnotations struct {
    ReadOnlyHint    bool   // true: tool does not modify its environment
    DestructiveHint *bool  // meaningful only when ReadOnlyHint == false
    IdempotentHint  bool   // repeat calls with same args = no extra effect
    OpenWorldHint   *bool  // closed world here (no external entities)
    Title           string // human-readable display name
}
```

Classification for cainban's ten tools:

| Tool | ReadOnlyHint | DestructiveHint | IdempotentHint |
|---|---|---|---|
| `list_tasks` | true | — | — |
| `get_task` | true | — | — |
| `list_boards` | true | — | — |
| `list_activity` | true | — | — |
| `whoami` | true | — | — |
| `change_board` | true | — | — (validates only; no state change) |
| `create_task` | false | false (additive) | false (new id each call) |
| `update_task` | false | true (overwrites fields) | true (same args → same state) |
| `update_task_status` | false | true | true |
| `update_task_priority` | false | true | true |

All tools set `OpenWorldHint: false` (a closed kanban domain, no external
entities). The six readers get `ReadOnlyHint: true`; `change_board` is read-only
in the multi-user design (it validates, never mutates shared state). Writers set
`ReadOnlyHint: false` with the destructive/idempotent hints above — the
`update_*` tools are idempotent (re-applying the same field value is a no-op in
effect), `create_task` is not (it mints a new id each call).

A tiny registration helper keeps this declarative:

```go
func readOnly(title string) *mcp.ToolAnnotations {
    return &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptr(false), Title: title}
}
```

### 2. Error semantics (Medium)

The spec splits two error classes:

- **Protocol errors** (unknown tool, malformed args, auth failure) → JSON-RPC
  error. These stay as transport errors. The 401/403 auth path and
  schema-validation failures are already here and correct.
- **Tool execution errors** (a valid call whose domain outcome is a failure:
  task #5 not found, invalid status value) → `isError: true` tool *result* with
  a human/model-readable message, so the model sees it in the content stream and
  can recover (re-read, pick a valid status) instead of the call blowing up as a
  protocol fault.

Today eight sites return domain outcomes as transport errors. Convert the
genuinely "expected" ones to `isError` results, mirroring `versionConflictResult`:

- `task #N not found` (server.go:540, 597, 624, 670) → `isError` result:
  "task #N not found; list_tasks to see valid ids."
- `invalid status` / `invalid priority` (476, 526, 611) → `isError` result naming
  the allowed values. (Arguably a protocol/arg error; as a model-recoverable
  outcome an `isError` result is friendlier and consistent.)
- `board %q not found in this scope` (756) → `isError` result (it already reads
  as a clean domain message; just move it from transport error to result).

Keep as transport errors the true faults: a store/DynamoDB failure
(`failed to open ...`, `failed to list ...`), which are not something the model
can recover from by retrying with different args.

A shared `toolError(format, args...)` helper mirrors `versionConflictResult` so
the convention is applied uniformly and is greppable.

### 3. Output schemas (Low)

For the tools that return structured content, set `OutputSchema` (the go-sdk can
derive it from the typed return via generics, or declare it explicitly). This
lets a client validate the structured payload. Low priority because the typed
second return value already gives well-formed JSON; the schema is the formal
contract.

### 4. Titles (Low)

Add `Title` (or `annotations.title`) human-readable names: "List tasks", "Create
task", "Who am I (current scope)", etc. Display-only.

### 5. Pagination (Low today)

`list_tasks` / `list_activity` return all rows (activity already caps at 200).
The spec's cursor pagination SHOULD apply, but a single repo's board is small, so
this is deferred until a board's task count is large enough to matter. Documented
here so it is a known, deliberate deferral rather than an oversight.

## Alternatives considered

- **Do nothing (status quo).** The server works, but a client cannot distinguish
  read from destructive tools (so cannot keep a human-in-the-loop confirmation on
  writes only), and the model sees "task not found" as a protocol crash. Rejected:
  these are exactly the hints the spec added for safety and recoverability.
- **Annotations only, skip error semantics.** Half the value — the error-result
  change is what lets an agent recover a not-found/invalid-status mid-task
  instead of surfacing a hard fault. Keep both.
- **Full output schemas on everything now.** Larger change for Low value; the
  typed returns already serialize cleanly. Defer with pagination.

## Scope boundary

No change to the data model, auth model, DynamoDB keys, or tool *names*/args
(the `TestToolsListStable` guard stays valid — annotations and titles do not
change the name set). `whoami` and the scope header from #52 are unaffected
except that `whoami` gains `ReadOnlyHint: true` here so it is consistent with the
other readers rather than annotated alone.

## Verification plan

1. **Unit** — a test asserts each tool's annotations match the classification
   table (the six readers incl. `whoami`+`change_board` are `ReadOnlyHint: true`;
   the four writers are `false` with the right destructive/idempotent hints). This
   is the regression guard that a new tool must be classified.
2. **Unit** — the converted handlers return `isError: true` results (not transport
   errors) for not-found / invalid-status / board-not-in-scope, with a message
   naming the recovery; a store failure still returns a transport error.
3. **Schema** — `TestToolsListStable` unchanged (same ten names); a new assertion
   that every tool carries `Annotations` and `OpenWorldHint == false`.
4. **Full suite** — `CC=<toolchain> CGO_ENABLED=1 go test -race ./...` green,
   including the SQLite-backed `mcp`/`store` tests.
5. **Live (multi-user)** — against the deployed endpoint: `tools/list` shows the
   annotations; a `get_task` on a missing id returns an `isError` result (visible
   to the model), not a JSON-RPC error; a read tool is flagged read-only.

## Rollout

Pure `src/systems/mcp` change: annotations in `registerTools`, a `toolError`
helper, and converting eight return sites. No infra/CDK change, no DynamoDB key
change, no tool-name change. Backward-compatible — a client that ignores
annotations is unaffected, and `isError` results are a documented part of the
tool-call response a conformant client already handles. Ships behind the normal
build → lambda-bundle → full `-race` test gates.
