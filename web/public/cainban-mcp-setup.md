# Set up the cainban MCP server for your AI coding agent

## Overview

This file sets up **cainban** — a per-repo kanban board exposed as a remote
**MCP server** (Streamable HTTP, OAuth 2.1) — as a task backend for an AI coding
tool (Claude Code, Kiro, Cursor, VS Code, …). The workflow:

- Detecting which AI tool is in use and its MCP configuration file
- Registering the cainban MCP server, preferring **OAuth** (the client signs in
  once in the browser and refreshes its own tokens — nothing long-lived in the
  config) and falling back to a header token only if the client cannot do OAuth
- Authenticating the user via the browser sign-in the client runs itself
- Verifying the connection by listing the board
- Pointing the agent at the target repo

cainban authenticates the user (never the agent) and authorizes each request by
the token's validated `repos` claim. The agent does no credential handling.

## Before you start

Gather these inputs and confirm prerequisites **before** running any step. Ask
for anything missing in a single message, then proceed autonomously.

### Required inputs

- **mcp_api** (required): the cainban MCP server URL, e.g.
  `<MCP_API>`. From the connect page.
- **client_id** (required for OAuth): the public OAuth client id, e.g.
  `<CLIENT_ID>`. It is public (no secret) — safe to place in config. From the
  connect page.
- **repo** (required): the target repository as `<owner>/<repo>`. Sent as the
  `X-Cainban-Repo` header so cainban knows which board to use.

### Input constraints

- You MUST NOT ask the user for a bearer token, access key, or secret — OAuth is
  handled entirely by the client's browser sign-in flow.
- The `client_id` is a PUBLIC identifier of the application, shared by all users;
  it authorizes nothing on its own. You MUST NOT treat it as a secret.
- You MUST send `X-Cainban-Repo: <owner>/<repo>` on requests where the client
  supports custom headers; otherwise the token's `default_repo` claim selects
  the repo.

### Prerequisites

- An MCP-capable AI tool. For OAuth (recommended) the tool MUST support the MCP
  Streamable-HTTP transport with client-run OAuth (Claude Code does; most modern
  clients do). A header-only client uses the fallback in Step 2b.
- A browser on the same machine for the one-time sign-in.

## How to run this file

Complete the steps yourself by running the commands / writing the config. Tell
the user which step you are on and why. One step requires the human to act —
**Step 3 (browser sign-in)** — pause and let them complete it. If a step fails,
see **Troubleshooting** at the end for that step, apply the resolution, and
resume; for errors not covered there, report the full output and do not proceed.

## Steps

### Step 1: Identify the AI tool and its MCP config

Determine which tool is in use and where its MCP servers are configured (not
exhaustive):

| Agent       | MCP config              | Location                     |
| ----------- | ----------------------- | ---------------------------- |
| Claude Code | `claude mcp add` / `.mcp.json` | CLI or project root   |
| Kiro        | `mcp.json`              | Kiro MCP settings            |
| Cursor      | `.cursor/mcp.json`      | `.cursor/` directory         |
| VS Code     | `.vscode/mcp.json`      | `.vscode/` directory         |

**Success:** you know the tool and its MCP config target.

### Step 2a (recommended): Register via OAuth — no token in config

#### Claude Code

```
claude mcp add --transport http --client-id <CLIENT_ID> cainban <MCP_API>
```

#### Config-file clients (Kiro / Cursor / VS Code / …) that can run OAuth

Add this server entry (merge into the existing `mcpServers`, do NOT overwrite
other servers):

```json
{
  "mcpServers": {
    "cainban": {
      "type": "http",
      "url": "<MCP_API>",
      "client_id": "<CLIENT_ID>",
      "headers": { "X-Cainban-Repo": "<owner>/<repo>" }
    }
  }
}
```

**Success:** the server is registered and the client shows it as needing
authentication. Proceed to Step 3.

### Step 2b (fallback only): Register with a bearer token

Use ONLY if the client cannot run OAuth. Paste a short-lived ID token from the
connect page. It expires (~1h) and must be regenerated on a 401 — prefer 2a.

```json
{
  "mcpServers": {
    "cainban": {
      "url": "<MCP_API>",
      "headers": {
        "Authorization": "Bearer <ID_TOKEN_FROM_CONNECT_PAGE>",
        "X-Cainban-Repo": "<owner>/<repo>"
      }
    }
  }
}
```

**Success:** the server entry is present. Skip Step 3 (no browser flow); go to
Step 4.

### Step 3: Authenticate (OAuth path only — human acts)

Trigger the client's sign-in. In Claude Code, run `/mcp` and choose
**cainban → Authenticate**. A browser opens; the human signs in. Wait for it to
complete.

**Success:** the client reports cainban as authenticated / connected.

> If the browser callback fails to load *after* a successful sign-in, the
> loopback port was not one the server registered. For Claude Code, launch it
> with the pinned callback port and retry:
>
> ```
> MCP_OAUTH_CALLBACK_PORT=3118 claude
> ```

### Step 4: Verify the connection

Call the `list_tasks` tool (or `list_boards`) for the target repo.

**Success:** the tool returns without error (an empty board is a valid success).
A **403** means your account is not granted that repo — connect it on the connect
page first. Confirm to the user that cainban is working.

### Step 5: Use the board

Once connected, use cainban as the task backend for `<owner>/<repo>`:

1. Before starting work, call `list_tasks` to see the current board.
2. Decompose the work you are given into tasks with `create_task`.
3. Move each task with `update_task_status` (`todo` → `doing` → `done`) as you
   progress.
4. Keep the board reflecting reality — it is the shared source of truth for the
   repo's work.

**Available tools:** `list_boards`, `change_board`, `list_tasks`, `get_task`,
`create_task`, `update_task`, `update_task_status`, `update_task_priority`.

End by telling the user: cainban is set up for `<owner>/<repo>`. Your agent will
list, create, and move tasks on the board as it works.

## Troubleshooting

- **Step 2/3 — client refuses the flow / no PKCE:** the client must read
  cainban's discovery docs. cainban serves RFC 9728 protected-resource metadata
  and an RFC 8414 authorization-server metadata shim that advertises S256 PKCE
  (Cognito omits it). If the client cannot discover, confirm it supports MCP
  OAuth; otherwise use the Step 2b bearer-token fallback.
- **Step 3 — browser lands on a page that won't load, URL has `?code=…`:** the
  sign-in succeeded but the callback port is unregistered. Use
  `MCP_OAUTH_CALLBACK_PORT=3118` (Claude Code) and retry.
- **Step 4 — 401:** the token expired or is missing. On OAuth the client
  refreshes automatically; if it persists, re-authenticate (Step 3). On the
  bearer fallback, regenerate the token on the connect page.
- **Step 4 — 403:** your account's `repos` claim does not grant
  `<owner>/<repo>`. Connect the repo on the connect page, then retry.
