# RFC: Surface the current repo and board scope

Status: Draft
Scope: `src/systems/mcp` (handlers + a new `whoami` tool), no infra change
Related: `rfc-board-tools-single-and-multi-user.md` + `#51` (BoardStore seam),
`rfc-default-repo-headerless.md` + `#48` (default_repo claim)

## Summary

A caller cannot tell, from the MCP surface alone, **which repo and board they are
operating in**. The repo is resolved server-side from the token
(`repos`/`default_repo` claim or the `X-Cainban-Repo` header) into a
`REPO#<owner>/<repo>#` partition before any tool runs, and no tool echoes it
back. The board is only inferable by reading `board_id` off a task or calling
`list_boards`. There is no single answer to "where am I?"

This was observed directly: in a fresh session a caller could not confirm the
active repo, and `list_tasks` output names tasks but never the scope they belong
to. This RFC makes the resolved scope **visible** in two complementary ways:

1. A dedicated **`whoami`** tool that returns the resolved `{repo, actor, boards,
   default_board}`.
2. A one-line **scope header** prefixed to `list_tasks` / `list_boards` output,
   so the scope is visible on calls the user already makes.

Both read state the server **already has in hand** at response time
(`tenantFromContext(ctx)` → `*auth.Tenant` with `.Repo` and `.Actor`, resolved
by `AuthMiddleware` before the handler runs). No new auth, no schema change to
existing task tools, no infra change.

## Motivation

- **Ambiguity is a correctness risk, not just UX.** An agent acting on the wrong
  repo's board is a silent error. Surfacing the scope lets the caller catch a
  wrong-token / wrong-`default_repo` situation immediately.
- **The repo is deliberately invisible today.** `X-Cainban-Repo` and
  `default_repo` are untrusted *selectors* authorized against the signed `repos`
  claim; the authorized result (`tenant.Repo`) is never reflected to the caller.
  Reflecting it is safe — it is the caller's own authorized repo, derived from
  their own token.
- **The board axis is now real** (post-`#51`): `list_boards` returns the board
  set. "Where am I?" should name both repo and board in one place.

## Design

### 1. New `whoami` tool

```
Name:        "whoami"
Description: "Report the repo and board scope the current token resolves to."
Args:        {}   // no input; scope comes from the authenticated request
```

Handler (`handleWhoami`), mirroring the existing resolve pattern:

- Read the tenant via `tenantFromContext(ctx)`.
- If **unscoped** (authenticated but no repo named and no `default_repo`): return
  a clear, non-error message naming the gap and the remedy — *"No repo in scope.
  Set the X-Cainban-Repo header or a default_repo claim."* This mirrors the
  fail-closed message the data tools already return, but `whoami` reports it as
  normal content (not `isError`), because asking "where am I?" with no scope is a
  valid question with the answer "nowhere yet."
- Otherwise open the board store (`resolveBoardStore`, from `#51`) and return:

```json
{
  "repo": "Awiant-AB/ec2-idle-shutdown",
  "actor": "hamin.mousavi@awiant.com",
  "boards": [{ "id": 1, "name": "default" }],
  "default_board": { "id": 1, "name": "default" }
}
```

Text form: `Repo: Awiant-AB/ec2-idle-shutdown · Actor: hamin.mousavi@awiant.com · Boards: default (id 1)`.

`actor` is the human-readable caller already resolved for the activity feed
(`actorFromCtx` → email when present, else subject), so it is free to include and
confirms *which identity* the token represents — useful when a wrong token is the
suspected problem.

### 2. Scope header on `list_tasks` / `list_boards`

Prefix a single line to the existing output, built from the resolved tenant:

```
Scope — repo: Awiant-AB/ec2-idle-shutdown · board: default (id 1)
```

- It is **prepended** to the existing content array; the structured content of
  these tools is unchanged, so no consumer that parses the JSON array breaks.
- On an unscoped tenant these tools already fail closed before producing output,
  so there is nothing to prefix — the existing "name a repo" error stands.
- `list_boards` names the repo (its boards all belong to one repo); the per-board
  lines already carry the board, so the header there is just the repo line.

This is where the ambiguity actually bites — someone reading `list_tasks` output
— so the scope is shown without the user having to remember a separate call.

### Why both, not one

- `whoami` answers an explicit "where am I?" and is the natural thing an agent
  calls once at the start of a session.
- The inline header answers the *implicit* question on every list call, catching
  a wrong-scope mistake even when the user never thinks to ask.

They share one helper (`scopeLine(ctx)`), so the format cannot drift between them.

## Repo correlation & auth

No new authorization. `whoami` and the scope header report **only** the already
-resolved `tenant.Repo` — the repo the caller's signed token authorized, turned
into the partition prefix by `AuthMiddleware` → `Resolver.Resolve` before the
handler ran. The same guarantees from the board RFC apply unchanged:

- The reported repo is the authorized one, never the raw untrusted header/arg.
- `whoami` opening the board store inherits the **fail-closed-on-unscoped** rule
  via `resolveBoardStore`; it never enumerates another tenant's boards.
- Single-user (SQLite, no tenant): `whoami` reports `repo: (local)` / no repo and
  the local boards; the scope header names the local board only. There is no repo
  binding locally and none is implied.

Nothing secret is exposed: a caller already knows their own token; reflecting the
repo and email it resolved to reveals nothing they could not read from the token
itself.

## Alternatives considered

- **Scope header only, no `whoami`.** Misses the explicit "where am I?" an agent
  wants to call deterministically at session start, and bloats every list call's
  parsing story if a consumer wants *just* the scope. Rejected: the dedicated
  tool is cheap and clearer.
- **`whoami` only, no inline header.** Leaves the ambiguity in `list_tasks`
  output — the exact place it was observed. Rejected: the inline header is where
  the value is.
- **Put the repo in every tool's structured output.** Larger schema churn across
  seven tools for a fact that belongs in one place; the header + `whoami` cover it
  without touching task-tool schemas.

## Scope boundary

This RFC only makes the EXISTING resolved scope visible. It does not add board
creation/switching semantics (board selection stays per-request per `#51`), does
not change how the repo is resolved, and adds no new auth. `whoami` is read-only.

## Verification plan

1. **Unit** — `handleWhoami` on a scoped tenant returns the repo, actor, and the
   board list from `resolveBoardStore`; on an unscoped tenant returns the
   "no repo in scope" message as non-error content and opens no store.
2. **Unit** — `scopeLine(ctx)` is prefixed to `list_tasks`/`list_boards` content
   on a scoped tenant; structured content is unchanged (a consumer parsing the
   JSON array still sees the same objects).
3. **Tenant isolation** — `whoami` for a token scoped to repo A reports repo A and
   only A's boards; never B's.
4. **Auth boundary** — missing/invalid token → 401 before `whoami` runs; `whoami`
   is present in `tools/list` on an unscoped tenant (handshake stays open) and
   reports the no-scope message when called.
5. **Schema** — `schema_test.go` sees the new `whoami` tool with empty args and
   unchanged existing tools.
6. **Live (multi-user)** — against the deployed endpoint with an authorized token:
   `whoami` returns `Awiant-AB/ec2-idle-shutdown` + actor + board 1; `list_tasks`
   output leads with the scope line.
7. **Live (single-user)** — `cainban mcp --http` loopback: `whoami` reports the
   local board and no repo binding.

## Rollout

Pure `src/systems/mcp` change: one new tool handler, one shared `scopeLine`
helper, and a two-line prefix in two existing handlers. No infra/CDK change, no
DynamoDB key change. Ships behind the normal build → lambda-bundle → full test
gates; adding a tool is backward-compatible (clients that ignore `whoami` are
unaffected).
