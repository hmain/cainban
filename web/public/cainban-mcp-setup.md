# Set up the cainban MCP server for your AI coding agent

cainban is a per-repo kanban board exposed as a remote MCP server (Streamable
HTTP, OAuth 2.1). This guide sets it up as the task backend for an AI coding tool
(Claude Code, Kiro, Cursor, VS Code). You register the server, sign in once in
the browser, verify by listing the board, and point the agent at your repo.

cainban authenticates the user, never the agent, and authorizes each request by
the token's validated `repos` claim. The agent handles no credentials.

## What you need

Three values, all public, all on the cainban connect page — and if an agent was
handed the connect page's setup prompt, they are already in that prompt. Use
them. Don't stop to ask the user for them, and never ask for a token or secret.

- **mcp_api** — the cainban MCP server URL (`<MCP_API>`).
- **client_id** — the public OAuth client id (`<CLIENT_ID>`). It is public and
  authorizes nothing on its own, so it is safe in config. It is not a secret;
  don't treat it as one.
- **repo** — the target repository as `<owner>/<repo>`. For a single-repo user
  you do NOT put this in the config: when you connected the repo, cainban set it
  as your token's `default_repo`, so the server picks the right board from your
  token with no header. You only need to name the repo explicitly — with an
  `X-Cainban-Repo` header — to pin a *different* repo in a multi-repo setup (see
  "Multi-repo: pin a repo with a header" below). The repo string is a plain
  owner/repo selector, never a secret: authorization always comes from the
  token's `repos` claim.

If one of the three genuinely wasn't given to you, it's on the connect page —
ask for that one thing in a single message, then carry on without further
back-and-forth.

### Prerequisites

An MCP-capable AI tool and a browser on the same machine for the one-time
sign-in. For the OAuth path (recommended) the tool must support the MCP
Streamable-HTTP transport with client-run OAuth; Claude Code and most modern
clients do. A header-only client uses the fallback in Step 2b.

## How to run this

Do the steps yourself — run the commands, write the config — and tell the user
which step you're on. One step is the user's: the browser sign-in in Step 3.
Pause there and let them finish it.

Two things that trip people up, so expect them:

- A newly-added MCP server usually loads only in a **new session**, not the one
  you added it in. So after registering and authenticating, verify in a fresh
  chat — the cainban tools won't appear in the current one.
- Verifying can return **403** — that means your account isn't granted the repo
  yet. It's not a setup bug; connect the repo on the connect page and retry.

If a step fails for another reason, check Troubleshooting at the end; if it's
not covered there, report the full output and stop.

## Steps

### Step 1: Identify the AI tool and its MCP config

Determine which tool is in use and where its MCP servers are configured (not
exhaustive):

| Agent       | MCP config              | Location                     |
| ----------- | ----------------------- | ---------------------------- |
| Claude Code | `claude mcp add` / `.mcp.json` | CLI or project root   |
| Kiro Crew   | `~/.kiro/crew/mcp.json` | gateway host (then `kirocrew restart`) |
| Kiro        | `mcp.json`              | Kiro MCP settings            |
| Cursor      | `.cursor/mcp.json`      | `.cursor/` directory         |
| VS Code     | `.vscode/mcp.json`      | `.vscode/` directory         |

**Success:** you know the tool and its MCP config target.

Prefer a ready-made recipe below over deriving the steps: **Per-agent quick
setup** has an exact, ordered recipe for each tool. If your tool is listed there,
follow its recipe and skip to Step 3; the generic Step 2a/2b below is the
fallback for a tool with no recipe.

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
      "oauth": {
        "clientId": "<CLIENT_ID>",
        "redirectUri": "http://127.0.0.1:3334/oauth/callback"
      }
    }
  }
}
```

**Pin `oauth.redirectUri`.** Kiro serves its OAuth loopback callback on
`/oauth/callback` and, when the redirect is unpinned, picks a *random* port.
Cognito registers callbacks exactly and cannot wildcard ports, so an unpinned
flow fails with `redirect_mismatch` — the browser shows "An error was
encountered with the requested page." The value above is registered on
cainban's Cognito client; `127.0.0.1` and `localhost` are not interchangeable to
Cognito, so keep the spelling exactly.

**Success:** the server is registered and the client shows it as needing
authentication. Proceed to Step 3.

Your token's `default_repo` (set when you connected the repo) picks the board, so
no `X-Cainban-Repo` header is needed here. To pin a *different* repo in a
multi-repo setup, add the header — see "Multi-repo: pin a repo with a header".

### Multi-repo: pin a repo with a header

If you have connected MORE than one repo and want this connection to act on a
specific one (not your `default_repo`), name it with the `X-Cainban-Repo` header.
One header pins one connection to one repo:

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

The repo string is a plain selector, not a secret — authorization is still your
token's `repos` claim, never the header. Some clients (including the KiroCrew
dashboard) mask every header value as `[REDACTED]` on display, because headers
usually hold tokens. For `X-Cainban-Repo` that mask is **cosmetic**: the stored
value is intact and the connection works. Leave it as you set it; don't try to
"fix" the mask. Single-repo users don't hit this at all, because they send no
header.

### Per-agent quick setup

Ready-made recipes. Each is self-contained: register → where it loads → the one
human step → verify. Substitute `<MCP_API>`, `<CLIENT_ID>`, and `<owner>/<repo>`
from the connect page (an agent handed the setup prompt already has them). Follow
your tool's recipe, then go to Step 4 to verify.

#### Claude Code

One command — Claude Code runs the OAuth itself:

```
claude mcp add --transport http --client-id <CLIENT_ID> cainban <MCP_API>
```

Your token's `default_repo` selects the board, so Claude Code's entry needs no
header. (To pin a *different* repo in a multi-repo setup, add `"headers": {
"X-Cainban-Repo": "<owner>/<repo>" }` to the entry — see "Multi-repo: pin a repo
with a header".) Authenticate with `/mcp → cainban → Authenticate` and finish the
browser sign-in. Verify with `list_tasks`. If the callback page won't load but
its URL has `?code=…`, relaunch with `MCP_OAUTH_CALLBACK_PORT=3118 claude` and
retry.

#### Kiro Crew dashboard (Add Custom Server)

The dashboard's **Settings → MCP Servers → Add Custom Server** paste box uses a
stricter spec than the on-disk `mcp.json`: it accepts `url`, `clientId`,
`scopes`, and `headers` only. It rejects `type` (transport is inferred from the
URL) and the snake_case `client_id` — paste a block with those keys and it fails
with `unknown spec key 'client_id'`. Use the camelCase `clientId` and drop
`type`:

```json
{
  "mcpServers": {
    "cainban": {
      "url": "<MCP_API>",
      "clientId": "<CLIENT_ID>",
      "oauth": {
        "clientId": "<CLIENT_ID>",
        "redirectUri": "http://127.0.0.1:3334/oauth/callback"
      }
    }
  }
}
```

Your token's `default_repo` selects the board, so no `headers` block is needed.
The `oauth.redirectUri` pin is required here too — the paste box forwards the
`oauth` block to the runtime verbatim, and without the pin the sign-in fails with
`redirect_mismatch`. Then rebuild/restart and authenticate as in the runtime
recipe below.

#### Kiro Crew runtime (this gateway, config file)

The agent config is layered: a writable source you edit, projected read-only into
the agent spec by the gateway. Edit the writable source and let the gateway
project it — never hand-edit the projection.

1. Merge the cainban server into `~/.kiro/crew/mcp.json` (the highest-priority
   **writable** MCP source; the gateway's config rebuild merges it into the agent
   spec and auto-mounts the `@cainban` tools):

   ```json
   {
     "mcpServers": {
       "cainban": {
         "type": "http",
         "url": "<MCP_API>",
         "client_id": "<CLIENT_ID>",
         "oauth": {
           "clientId": "<CLIENT_ID>",
           "redirectUri": "http://127.0.0.1:3334/oauth/callback"
         }
       }
     }
   }
   ```

   Do **not** edit the projected `~/.kiro/agents/<agent>.json` (read-only,
   gateway-owned), and do **not** add `allowedTools` to the entry — that waives
   the governance ceiling and the gateway revokes it on the next rebuild.
2. The gateway owner rebuilds the projection and restarts the gateway from a
   **terminal** (`kirocrew restart`) — not from inside a chat, which would kill
   the session mid-turn.
3. Open a **fresh chat** after the restart. The runtime hits cainban's 401 and
   raises an inline **Authorize** banner — click it and complete the sign-in.
4. Verify in that chat: `list_tasks` for `<owner>/<repo>`. An empty board is
   success.

Runtime-specific gotcha: if the Authorize banner fails closed with "URL contained
credential or exfiltration pattern," cainban's Cognito sign-in host isn't in the
gateway's OAuth-endpoint allowlist. The gateway owner adds it to
`~/.kiro/crew/oauth_endpoints.json` (hand-edit, no restart needed), using
cainban's own Cognito domain and region:

```json
{ "additional_authorization_endpoints": [
  { "host": "<cognito-domain>.auth.<region>.amazoncognito.com", "path": "/oauth2/authorize" }
] }
```

#### Kiro

Add the server to Kiro's MCP settings (`mcp.json`), merging into any existing
`mcpServers`:

```json
{
  "mcpServers": {
    "cainban": {
      "type": "http",
      "url": "<MCP_API>",
      "client_id": "<CLIENT_ID>",
      "oauth": {
        "clientId": "<CLIENT_ID>",
        "redirectUri": "http://127.0.0.1:3334/oauth/callback"
      }
    }
  }
}
```

Reload Kiro's MCP servers, complete the browser sign-in when prompted, verify
with `list_tasks`.

#### Cursor

Same entry in `.cursor/mcp.json` (project) or Cursor's global MCP config:

```json
{
  "mcpServers": {
    "cainban": {
      "type": "http",
      "url": "<MCP_API>",
      "client_id": "<CLIENT_ID>",
      "oauth": {
        "clientId": "<CLIENT_ID>",
        "redirectUri": "http://127.0.0.1:3334/oauth/callback"
      }
    }
  }
}
```

Reload the window, complete the browser sign-in when Cursor prompts, verify with
`list_tasks`.

#### VS Code

Same entry in `.vscode/mcp.json`:

```json
{
  "mcpServers": {
    "cainban": {
      "type": "http",
      "url": "<MCP_API>",
      "client_id": "<CLIENT_ID>",
      "oauth": {
        "clientId": "<CLIENT_ID>",
        "redirectUri": "http://127.0.0.1:3334/oauth/callback"
      }
    }
  }
}
```

Reload the window / restart the MCP servers, complete the browser sign-in when
prompted, verify with `list_tasks`.

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
`create_task`, `update_task`, `update_task_status`, `update_task_priority`,
`search_tasks`, `delete_task`, `restore_task`, `link_tasks`, `unlink_tasks`,
`get_task_links`, `list_links`, `list_activity`, `whoami`.

End by telling the user: cainban is set up for `<owner>/<repo>`. Your agent will
list, create, and move tasks on the board as it works.

### Step 6 (recommended): Install a steering rule so cainban is used automatically

So the user never has to say "use cainban" again, write a rule into THIS
project's agent-rules file. Pick the file for the tool in use:

| Agent       | Rules file                 | Location                  |
| ----------- | -------------------------- | ------------------------- |
| Claude Code | `CLAUDE.md`                | project root              |
| Codex / generic | `AGENTS.md`            | project root              |
| Kiro        | `.kiro/steering/cainban.md`| `.kiro/steering/`         |
| Cursor      | `.cursor/rules/cainban.mdc`| `.cursor/rules/`          |

Write it **non-destructively** — these files hold the project's own instructions
and are usually committed:

- If the file does not exist, create it (and its directory) and write the rule.
- If it exists, **append** the rule between idempotent markers rather than
  overwriting. Use `<<<CAINBAN_STEERING_START>>>` / `<<<CAINBAN_STEERING_END>>>`
  so re-running replaces only the marked block instead of adding a second copy.
- Where the project's own instructions conflict with the rule, the project wins.

The rule to write (substitute `<owner>/<repo>`):

```markdown
# Task management: use cainban

This project tracks work on a cainban board (a per-repo kanban board exposed
over MCP) for `<owner>/<repo>`. Treat cainban as the source of truth for what
needs doing and what is in progress — use it automatically, without being asked.

- At the start of any work session, call `list_tasks` before deciding what to do.
- When given work, break it into tasks with `create_task` (one per cohesive
  unit); set a priority when it is obvious.
- As you work, keep the board honest with `update_task_status` (`todo` → `doing`
  → `done`), and record a blocks / depends_on relationship with `link_tasks`
  when one task gates another.
- Do not ask which tool to use for tasks, and do not keep a separate TODO list
  in chat or scratch files — the board is it. The repo is selected by your
  token, so no `X-Cainban-Repo` header is needed.
```

**Success:** the rule is present in the correct rules file, pre-existing project
instructions are preserved, and a fresh session uses cainban without prompting.

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
