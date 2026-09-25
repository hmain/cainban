# Using cainban as an AI agent's task backend (over MCP)

This is the end goal of cainban's serverless multi-user work: **an AI code agent
uses a per-repo cainban board as its own task backend over MCP.** The agent
decomposes a feature into tasks, tracks its backlog, and advances work itself —
instead of a human hand-planning every step.

This guide is for that agent's operator. Everything below is grounded in the
actual source; the tool names, argument names, headers, routes, and env vars are
what the code registers, not a wish list.

## The model in one paragraph

An agent is just an **MCP client**. cainban's serverless endpoint is a
**stateless Streamable-HTTP** MCP handler
([`src/systems/mcp/server.go`](../src/systems/mcp/server.go): `Handler` builds
`mcp.NewStreamableHTTPHandler(..., &mcp.StreamableHTTPOptions{Stateless: true})`).
There is no new protocol and no new credential type: the public Lambda wraps that
same handler with signature-first JWT auth (`HandlerWithAuth` →
[`AuthMiddleware`](../src/systems/mcp/tenant.go)), so the agent presents a
**bearer JWT** whose validated `repos` claim authorizes a repo, names the target
repo with a header, and calls the ordinary tools. The server holds no
"current board" and no per-connection state — every tool call resolves and opens
the correct repo-scoped store on its own, which is what makes one handler safe to
serve concurrent agents for different repos.

## Prerequisites

1. **The repo must be connected/granted first.** Authorization comes only from
   the validated `repos` claim inside the token — a header or tool arg can *name*
   a repo but never *grants* it
   ([`src/systems/auth/auth.go`](../src/systems/auth/auth.go), "Identity vs
   authorization"). The repo gets into that claim through the GitHub-connect
   flow and the pre-token trigger. Set that up first:
   - [`docs/github-app-setup.md`](./github-app-setup.md) — the GitHub App.
   - The connect API (`cmd/cainban-connect`) routes
     ([`src/systems/connect/handler.go`](../src/systems/connect/handler.go)):
     `GET /connect/github/start`, `GET /connect/github/callback`,
     `POST /connect/repo`, `DELETE /connect/repo`, `GET /connect/repos`.
2. **The agent needs a valid token that carries the target repo in `repos`.**

### Which token does the agent use?

Per the plan, the default is that **the agent reuses the token the human already
has** — it acts on the user's behalf, and the user's grants are already in the
token's `repos` claim. A **dedicated machine principal is optional and deferred**:
it is not built today, so do not assume a separate service identity exists. If
you need one later it is added at the IdP/connect layer, not in the MCP server.

## How the target repo is selected per request

The repo a request addresses (its *identity*) is resolved in a fixed precedence
by [`src/systems/auth/resolver.go`](../src/systems/auth/resolver.go)
(`resolveTarget`):

1. An explicit **MCP tool argument** (`argRepo`), if non-empty.
2. Otherwise the **`X-Cainban-Repo`** request header (the exported constant
   `HeaderTargetRepo = "X-Cainban-Repo"`).
3. Otherwise the token's **`default_repo`** claim.

If none of the three yields a repo, the request is a **403** ("no target repo
supplied and token has no default_repo") — cainban never silently falls through
to some other tenant.

> **Wiring note (accurate to current code):** the HTTP transport's
> `AuthMiddleware` calls `resolver.Resolve(r, "")` — it passes an empty
> `argRepo`. So on the deployed Lambda path today the target is chosen by the
> **`X-Cainban-Repo` header, then `default_repo`**. The tool-arg override is
> implemented in the resolver but is not plumbed from the transport, so in
> practice **set `X-Cainban-Repo`** (or rely on `default_repo`) to pick the repo.

Whichever source names the repo, **authorization is separate**: the resolved
target must be a member of the validated `repos` claim
(`Identity.authorizes`), or the request is a 403. The header and the tool arg are
untrusted for authorization — they only select *which* repo, never *whether* the
caller may touch it. Once authorized, the tenant's DynamoDB partition prefix
(`REPO#<owner>/<repo>#`) isolates that repo's data structurally.

## The available MCP tools

These are the tools registered in `Server.registerTools`
([`src/systems/mcp/server.go`](../src/systems/mcp/server.go)). Each name and each
argument below was verified against the handler argument structs in that file.

| Tool | Arguments (JSON) | Purpose |
| --- | --- | --- |
| `create_task` | `title` (string, required), `description` (string, optional), `board_id` (int, optional, defaults to 1), `priority` (optional: `none`/`low`/`medium`/`high`/`critical` or `0`–`4`) | Create a task. |
| `list_tasks` | `board_id` (int, optional, defaults to 1), `status` (string, optional: `todo`/`doing`/`done`) | List tasks, optionally filtered by status. |
| `get_task` | `id` (int, required) | Get one task by its board task id. |
| `update_task` | `id` (int, required), `title` (string, required), `description` (string, optional) | Update a task's title/description. |
| `update_task_status` | `id` (int, required), `status` (string, required: `todo`/`doing`/`done`) | Move a task between columns. |
| `update_task_priority` | `id` (int, required), `priority` (required: `none`/`low`/`medium`/`high`/`critical` or `0`–`4`) | Change a task's priority. |
| `list_boards` | *(none)* | List available boards. |
| `change_board` | `board_name` (string, required) | Validate a board exists. **No-op for routing:** board selection is per-request now, so this only confirms the board exists; it does not change any server-side "current board". |

Notes an agent author should know:

- **There are no task-link MCP tools.** The data model has task links
  (`from_task_id`/`to_task_id` columns and fields exist in
  `src/systems/storage` / `src/systems/task` / `src/systems/dynamo`), but **no
  link tool is registered on the MCP server**. Do not call `get_task_links`,
  `create_link`, or similar over MCP — they are not exposed. (README prose that
  mentions `get_task_links` refers to the data model / CLI history, not an MCP
  tool.)
- The `id` that `get_task`/`update_*` take is the **board-scoped task id** (the
  `#N` shown by `create_task`/`list_tasks`), not an internal row id.
- Statuses are exactly `todo`, `doing`, `done`. Priorities are `none`, `low`,
  `medium`, `high`, `critical` (or the integers `0`–`4`).

## Worked example

### 1. Point the MCP client at cainban

For the **local, single-tenant dev** server (unauthenticated, loopback only):

```bash
cainban mcp                 # stdio transport (default)
cainban mcp --http :8080    # stateless HTTP on 127.0.0.1:8080
```

stdio client config (e.g. Amazon Q CLI `~/.aws/amazonq/mcp.json`):

```json
{
  "mcpServers": {
    "cainban": {
      "command": "cainban",
      "args": ["mcp"]
    }
  }
}
```

For the **deployed serverless** endpoint (authenticated, repo-scoped), the agent
is an HTTP MCP client that sends the bearer token and names the repo with the
header. The exact config shape depends on your MCP client; conceptually the
request headers are:

```
Authorization: Bearer <JWT whose validated repos claim includes owner/repo>
X-Cainban-Repo: <owner>/<repo>
```

`<endpoint>` is the cainban MCP Lambda's Function URL (an
`https://….lambda-url.<region>.on.aws/` address). Illustrative HTTP-client
config:

```json
{
  "mcpServers": {
    "cainban": {
      "url": "https://<function-url-id>.lambda-url.<region>.on.aws/",
      "headers": {
        "Authorization": "Bearer <token>",
        "X-Cainban-Repo": "<owner>/<repo>"
      }
    }
  }
}
```

> Header/URL support varies by MCP client — use whatever mechanism your client
> provides for setting request headers on a Streamable-HTTP MCP server. The two
> headers above are what cainban reads.

### 2. A realistic agent loop

The agent has been handed a feature ("add rate limiting to the upload endpoint")
and owns the repo's board.

1. **Decompose the feature into tasks** — one `create_task` per unit of work:

   ```json
   {"name": "create_task", "arguments": {"title": "Add token-bucket limiter middleware", "priority": "high"}}
   {"name": "create_task", "arguments": {"title": "Wire limiter into upload route", "description": "Return 429 on exceed"}}
   {"name": "create_task", "arguments": {"title": "Add limiter unit tests"}}
   ```

   Each call returns e.g. `Created task #1 [high]: Add token-bucket limiter middleware`.

2. **Read the backlog** before picking work:

   ```json
   {"name": "list_tasks", "arguments": {"status": "todo"}}
   ```

3. **Start a task** — move it to `doing`, do the work, then to `done`:

   ```json
   {"name": "update_task_status", "arguments": {"id": 1, "status": "doing"}}
   {"name": "update_task_status", "arguments": {"id": 1, "status": "done"}}
   ```

4. **Inspect a specific task** when it needs its full description:

   ```json
   {"name": "get_task", "arguments": {"id": 2}}
   ```

Every one of these calls carries the same `Authorization` + `X-Cainban-Repo`
headers, and every one is scoped to that repo's partition — the agent cannot see
or touch another repo's board.

## Auth failure modes the agent will see

The Lambda decides the status in
[`src/systems/auth/auth.go`](../src/systems/auth/auth.go) (`HTTPStatus`) and
writes a JSON error body (`writeAuthError` in
[`src/systems/mcp/tenant.go`](../src/systems/mcp/tenant.go)):

- **401 Unauthorized** (`{"error":{"code":401,"message":"unauthorized"}}`, with a
  `WWW-Authenticate: Bearer realm="cainban"` header) — the token is missing,
  malformed, has a bad signature, or the wrong issuer/audience/expiry. No tenant
  is resolved and no store is opened. **Fix:** present a valid, unexpired bearer
  token.
- **403 Forbidden** (`{"error":{"code":403,"message":"forbidden"}}`) — the token
  is valid but the target repo is **not in its `repos` claim** (or no target repo
  could be determined at all). **Fix:** connect/grant the repo first (see
  Prerequisites and [`docs/github-app-setup.md`](./github-app-setup.md)), then
  obtain a token whose `repos` claim includes `<owner>/<repo>`.

Note the Function URL's edge `AuthType` is `AWS_IAM`, so requests must also be
SigV4-signed at the edge in addition to carrying the application bearer token —
there is no anonymous reachability to the endpoint.

## Related docs

- [`docs/github-app-setup.md`](./github-app-setup.md) — connect a repo (the step
  that puts a repo into the `repos` claim).
- [`docs/serverless-multiuser-plan.md`](./serverless-multiuser-plan.md) — the
  full serverless/multi-tenant design this guide is the end goal of.
- [`infra/README.md`](../infra/README.md) — the deployed Lambdas, IAM surface,
  and how the MCP + connect functions are provisioned.
