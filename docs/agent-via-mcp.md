# Using cainban as an AI agent's task backend (over MCP)

This is where cainban's serverless multi-user work was headed: an AI code agent
uses a per-repo cainban board as its own task backend over MCP. The agent breaks
a feature into tasks, tracks its backlog, and moves the work forward itself,
rather than having a human plan every step.

If you run that agent, this guide is for you. Everything below comes from the
actual source. The tool names, argument names, headers, routes, and env vars are
what the code registers, not a wish list.

## How it works

An agent is just an MCP client. cainban's serverless endpoint is a stateless
Streamable-HTTP MCP handler
([`src/systems/mcp/server.go`](../src/systems/mcp/server.go): `Handler` builds
`mcp.NewStreamableHTTPHandler(..., &mcp.StreamableHTTPOptions{Stateless: true})`).
You don't need a new protocol or a new credential type. The public Lambda wraps
that same handler with signature-first JWT auth (`HandlerWithAuth` →
[`AuthMiddleware`](../src/systems/mcp/tenant.go)), so the agent presents a bearer
JWT whose validated `repos` claim authorizes a repo, names the target repo with a
header, and calls the ordinary tools. The server keeps no "current board" and no
per-connection state. Every tool call resolves and opens the right repo-scoped
store on its own, which is what lets one handler safely serve concurrent agents
for different repos.

## Before you start

1. Connect and grant the repo first. Authorization comes only from the validated
   `repos` claim inside the token. A header or tool arg can *name* a repo but
   never *grants* it
   ([`src/systems/auth/auth.go`](../src/systems/auth/auth.go), "Identity vs
   authorization"). The repo gets into that claim through the GitHub-connect
   flow and the pre-token trigger. Set that up first:
   - [`docs/github-app-setup.md`](./github-app-setup.md) — the GitHub App.
   - The connect API (`cmd/cainban-connect`) routes
     ([`src/systems/connect/handler.go`](../src/systems/connect/handler.go)):
     `GET /connect/github/start`, `GET /connect/github/callback`,
     `POST /connect/repo`, `DELETE /connect/repo`, `GET /connect/repos`.
2. The agent needs a valid token that carries the target repo in `repos`.

### Which token does the agent use?

By default the agent reuses the token the human already has. It acts on the
user's behalf, and the user's grants are already in the token's `repos` claim. A
dedicated machine principal is optional and deferred: it is not built today, so
don't assume a separate service identity exists. If you need one later, you add
it at the IdP/connect layer, not in the MCP server.

## How the target repo is picked per request

The repo a request addresses (its *identity*) is resolved in a fixed precedence
by [`src/systems/auth/resolver.go`](../src/systems/auth/resolver.go)
(`resolveTarget`):

1. An explicit MCP tool argument (`argRepo`), if non-empty.
2. Otherwise the `X-Cainban-Repo` request header (the exported constant
   `HeaderTargetRepo = "X-Cainban-Repo"`).
3. Otherwise the token's `default_repo` claim.

If none of the three yields a repo, the request is a 403 ("no target repo
supplied and token has no default_repo"). cainban never silently falls through
to some other tenant.

> Wiring note (accurate to current code): the HTTP transport's `AuthMiddleware`
> calls `resolver.Resolve(r, "")`, passing an empty `argRepo`. So on the deployed
> Lambda path today the target is chosen by the `X-Cainban-Repo` header, then
> `default_repo`. The tool-arg override is implemented in the resolver but is not
> plumbed from the transport, so in practice you set `X-Cainban-Repo` (or rely on
> `default_repo`) to pick the repo.

Whichever source names the repo, authorization is separate: the resolved target
must be a member of the validated `repos` claim (`Identity.authorizes`), or the
request is a 403. The header and the tool arg are untrusted for authorization.
They only select *which* repo, never *whether* the caller may touch it. Once
authorized, the tenant's DynamoDB partition prefix (`REPO#<owner>/<repo>#`)
isolates that repo's data structurally.

## The MCP tools you can call

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
| `search_tasks` | `query` (string, required, fuzzy title substring), `board_id` (int, optional, defaults to 1) | Fuzzy-search tasks by title in the board. |
| `delete_task` | `id` (int, required), `hard` (bool, optional, default false) | Soft-delete a task (recoverable); `hard: true` permanently removes it and its links. |
| `restore_task` | `id` (int, required) | Restore a soft-deleted task; no-op on one never deleted or hard-deleted. |
| `link_tasks` | `from_id` (int, required), `to_id` (int, required), `type` (string, required: `blocks`/`blocked_by`/`related`/`depends_on`) | Create a directional link between two tasks. |
| `unlink_tasks` | `from_id` (int, required), `to_id` (int, required), `type` (string, required) | Remove a specific link between two tasks. |
| `get_task_links` | `id` (int, required) | List all links referencing a task (both directions). |
| `list_boards` | *(none)* | List available boards. |
| `change_board` | `board_name` (string, required) | Validate a board exists. **No-op for routing:** board selection is per-request now, so this only confirms the board exists; it does not change any server-side "current board". |
| `list_activity` | `task_id` (int, optional — scope to one task), `limit` (int, optional, default 50, max 200) | Append-only activity feed (who changed what), newest first. Read-only audit. |
| `whoami` | *(none)* | Report the repo + board scope the current token resolves to. |

A few things worth knowing if you're writing an agent:

- **Task-link tools ARE exposed.** `link_tasks`, `unlink_tasks`, and
  `get_task_links` are registered on the MCP server and work against the
  serverless endpoint. Link types are `blocks`, `blocked_by`, `related`,
  `depends_on`. (Earlier versions of this guide said no link tools existed; that
  is no longer true.)
- The `id` that `get_task`/`update_*`/`delete_task`/`restore_task`/`get_task_links`
  take is the board-scoped task id (the `#N` shown by `create_task`/`list_tasks`),
  not an internal row id. `link_tasks`/`unlink_tasks` take two of them
  (`from_id`, `to_id`).
- Statuses are exactly `todo`, `doing`, `done`. Priorities are `none`, `low`,
  `medium`, `high`, `critical` (or the integers `0`–`4`).
- `list_activity` is a read-only audit feed and is never used to derive task or
  board state. `whoami` answers "which repo/board does my token resolve to?"
  without inferring it from a tool call.

## Worked example

### 1. Point the MCP client at cainban

For the local, single-tenant dev server (unauthenticated, loopback only):

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

For the deployed serverless endpoint (authenticated, repo-scoped), the agent is
an HTTP MCP client that sends the bearer token and names the repo with the
header. The exact config shape depends on your MCP client. The request headers
are:

```
Authorization: Bearer <JWT whose validated repos claim includes owner/repo>
X-Cainban-Repo: <owner>/<repo>
```

`<endpoint>` is the cainban MCP API Gateway HTTP API URL (an
`https://<api-id>.execute-api.<region>.amazonaws.com/` address, the `McpApiUrl`
stack output). A managed Cognito JWT authorizer validates the token at the edge,
so the agent sends only the bearer token, with no request signing. Here is an
example HTTP-client config:

```json
{
  "mcpServers": {
    "cainban": {
      "url": "https://<api-id>.execute-api.<region>.amazonaws.com/",
      "headers": {
        "Authorization": "Bearer <token>",
        "X-Cainban-Repo": "<owner>/<repo>"
      }
    }
  }
}
```

> Header/URL support varies by MCP client, so use whatever mechanism your client
> provides for setting request headers on a Streamable-HTTP MCP server. The two
> headers above are what cainban reads.

### 2. A realistic agent loop

The agent has been handed a feature ("add rate limiting to the upload endpoint")
and owns the repo's board.

1. Break the feature into tasks, one `create_task` per unit of work:

   ```json
   {"name": "create_task", "arguments": {"title": "Add token-bucket limiter middleware", "priority": "high"}}
   {"name": "create_task", "arguments": {"title": "Wire limiter into upload route", "description": "Return 429 on exceed"}}
   {"name": "create_task", "arguments": {"title": "Add limiter unit tests"}}
   ```

   Each call returns e.g. `Created task #1 [high]: Add token-bucket limiter middleware`.

2. Read the backlog before picking work:

   ```json
   {"name": "list_tasks", "arguments": {"status": "todo"}}
   ```

3. Start a task: move it to `doing`, do the work, then to `done`:

   ```json
   {"name": "update_task_status", "arguments": {"id": 1, "status": "doing"}}
   {"name": "update_task_status", "arguments": {"id": 1, "status": "done"}}
   ```

4. Inspect a specific task when you need its full description:

   ```json
   {"name": "get_task", "arguments": {"id": 2}}
   ```

Every one of these calls carries the same `Authorization` + `X-Cainban-Repo`
headers, and every one is scoped to that repo's partition. The agent cannot see
or touch another repo's board.

## Auth failures the agent will see

The Lambda decides the status in
[`src/systems/auth/auth.go`](../src/systems/auth/auth.go) (`HTTPStatus`) and
writes a JSON error body (`writeAuthError` in
[`src/systems/mcp/tenant.go`](../src/systems/mcp/tenant.go)):

- 401 Unauthorized (`{"error":{"code":401,"message":"unauthorized"}}`, with a
  `WWW-Authenticate: Bearer realm="cainban"` header). The token is missing,
  malformed, has a bad signature, or the wrong issuer/audience/expiry. No tenant
  is resolved and no store is opened. To fix it, present a valid, unexpired
  bearer token.
- 403 Forbidden (`{"error":{"code":403,"message":"forbidden"}}`). The token is
  valid but the target repo is not in its `repos` claim (or no target repo could
  be determined at all). To fix it, connect/grant the repo first (see "Before you
  start" and [`docs/github-app-setup.md`](./github-app-setup.md)), then obtain a
  token whose `repos` claim includes `<owner>/<repo>`.

The MCP endpoint is fronted by an API Gateway HTTP API with a managed Cognito JWT
authorizer. The authorizer validates the token's signature, issuer, audience and
expiry at the edge, so an unauthenticated or invalid-token request is rejected
with 401 before it reaches the Lambda. There is no anonymous reachability. A
valid token is required, and no request signing (SigV4) is needed. The agent
sends only `Authorization: Bearer <token>`.

## MCP-native OAuth discovery (RFC 9728 / RFC 8707 — step a)

Beyond the "paste a bearer token" flow above, cainban is a spec-compliant OAuth
2.1 resource server (MCP authorization spec 2026-07-28), so an MCP client that
supports OAuth can discover where to authenticate and drive PKCE itself:

- Public discovery endpoint: `GET <McpApiUrl>/.well-known/oauth-protected-resource`
  (no auth) returns the RFC 9728 document — `resource` (cainban's canonical MCP
  URL), `authorization_servers` (the Cognito issuer), `scopes_supported`
  (`cainban:tasks`), `bearer_methods_supported` (`["header"]`).
- 401 challenge: a protected route without a valid token replies
  `WWW-Authenticate: Bearer resource_metadata="…/.well-known/oauth-protected-resource", scope="cainban:tasks"`,
  which points a client at that document.
- The client runs the PKCE authorization-code flow against the Cognito Hosted UI
  using the pre-registered SPA `client_id`, then sends the resulting token
  exactly as `Authorization: Bearer <token>` (the same header this guide uses).

This is step (a): it works with any MCP client that supports a pre-registered
client id. Dynamic Client Registration (DCR, now deprecated) and Client ID
Metadata Documents (CIMD) are not yet supported; they need the step-(b) OAuth
proxy. For full setup, see
[`docs/mcp-oauth-setup.md`](./mcp-oauth-setup.md).

## Related docs

- [`docs/mcp-oauth-setup.md`](./mcp-oauth-setup.md), MCP-native OAuth (RFC 9728
  discovery + pre-registered PKCE client); this is step (a).
- [`docs/github-app-setup.md`](./github-app-setup.md), connect a repo (the step
  that puts a repo into the `repos` claim).
- [`infra/README.md`](../infra/README.md), the deployed Lambdas, IAM surface,
  and how the MCP + connect functions are provisioned.
