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

## Fast path: auto-configure (try this first)

Before doing any of the manual steps below, try the one-command path. It fetches
the correct MCP URL, OAuth client id and authorize host **from the server
itself**, so it cannot pick the wrong URL — the single most common setup
mistake (the Connect API URL and the MCP API URL look alike, and only the MCP
API serves OAuth discovery).

If the `cainban` CLI is available, run (substitute `<MCP_API>`):

```
cainban connect-agent --mcp-url <MCP_API> --format kirocrew
```

Pick the `--format` for your tool: `kirocrew`, `claude`, `kiro-ide`, `cursor`,
or `vscode`. It prints the exact config to merge (add `--output <path>` to write
it directly). For Kiro Crew, if the Authorize banner later fails with an
exfiltration-pattern error, also run `--format kirocrew-exfil-gate` and merge
that into `~/.kiro/crew/oauth_endpoints.json`.

**No CLI?** The same data is a public endpoint any agent can GET and parse —
`<MCP_API>/.well-known/mcp-client-config`. It returns the MCP URL, the OAuth
client id, the resolved authorize/token endpoints, and pre-built config snippets
keyed by client (`configs.kirocrew`, `configs.claude_code_command`,
`configs.kirocrew_exfil_gate`, `configs.kiro_ide`, `configs.cursor`,
`configs.vscode`). Fetch it, pick your client's snippet, and write it verbatim.
If that endpoint 404s/401s the server predates auto-config — fall back to the
manual steps below.

Either way you still do the one human step (the browser sign-in, Step 3) and the
verify (Step 4). If auto-config isn't available, continue with the manual steps.

## Steps

### Step 1: Identify yourself, then use only your own recipe

**First, state which tool you are.** You are reading this as the agent being set
up, so name yourself before doing anything: Claude Code, Kiro, Kiro IDE, Kiro
Crew, Cursor, or VS Code. If you cannot tell from your own runtime, ask the user
the single question "which tool am I running in?" and wait for the answer — do
not guess, because each tool configures MCP differently and following the wrong
recipe wastes a round trip.

Each tool has a different MCP config target (not exhaustive):

| Agent       | MCP config              | Location                     |
| ----------- | ----------------------- | ---------------------------- |
| Claude Code | `claude mcp add` / `.mcp.json` | CLI or project root   |
| Kiro Crew   | `~/.kiro/crew/mcp.json` | gateway host (then `kirocrew restart`) |
| Kiro / Kiro IDE | `mcp.json`          | Kiro MCP settings            |
| Cursor      | `.cursor/mcp.json`      | `.cursor/` directory         |
| VS Code     | `.vscode/mcp.json`      | `.vscode/` directory         |

**Once you know which tool you are, go straight to that tool's recipe in
Per-agent quick setup and follow ONLY it.** Each recipe is self-contained
(register → where it loads → the one human step → verify), so you do not read the
other tools' sections, and you do not fall through to the generic Step 2a/2b —
those are the fallback for a tool with no recipe of its own. Do the setup for
your specific tool and nothing else.

**Success:** you have named your tool and found its recipe below.

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
credential or exfiltration pattern," an OAuth host cainban redirects through isn't
in the gateway's OAuth-endpoint allowlist. **There are two such hosts, each a
separate allowlist entry, and you may hit either (or both, one after the other):**

- the **MCP server's own authorize route** on API Gateway — host like
  `<id>.execute-api.<region>.amazonaws.com`, path **`/authorize`** (this is the
  first redirect, so it is usually the one you hit first); and
- the **Cognito sign-in host** — cainban's full Cognito domain
  `cainban-<org>-<account_id>.auth.<region>.amazoncognito.com`, path
  **`/oauth2/authorize`** (the handoff after the first redirect).

Both host **and** path are **matched exactly**, and you do not have to construct
either: **the exact `host` and `path` are already in the error message you are
looking at — copy them from there verbatim.** (Real examples:
`w241x2n8qj.execute-api.eu-central-1.amazonaws.com` + `/authorize` for the API
Gateway host, and `cainban-emawiant-563329104476.auth.eu-central-1.amazoncognito.com`
+ `/oauth2/authorize` for Cognito. A bare `.auth.amazoncognito.com` or a wrong
path never matches and leaves the gate closed.)

Runtime-specific gotcha: if the sign-in lands on a Cognito error page whose URL
reads `.../error?error=invalid_request&client_id=Q+DEV+CLI` (or any `client_id`
that is not cainban's), the cainban server entry has the wrong — or no —
`client_id`, so the runtime fell back to a built-in default (`Q DEV CLI` is the
Amazon Q CLI's). Cognito rejects an unknown client with `invalid_request`. Fix:
set the cainban entry's `client_id` to the `<CLIENT_ID>` from the connect page,
in **both** the top-level `client_id` and the nested `oauth.clientId` (some
runtime paths read one, some the other). One-liner that merges the correct id
into `~/.kiro/crew/mcp.json` without clobbering other servers (requires `jq` —
substitute `<MCP_API>` and `<CLIENT_ID>`):

```sh
CID="<CLIENT_ID>"; URL="<MCP_API>"
F=~/.kiro/crew/mcp.json
[ -f "$F" ] || echo '{}' > "$F"
jq --arg cid "$CID" --arg url "$URL" \
  '.mcpServers.cainban = ((.mcpServers.cainban // {}) + {type:"http", url:$url, client_id:$cid, oauth:{clientId:$cid, redirectUri:"http://127.0.0.1:3334/oauth/callback"}})' \
  "$F" > "$F.tmp" && mv "$F.tmp" "$F"
```

Then the gateway owner restarts the gateway from a terminal and retries
Authorize in a fresh chat.

The gateway owner appends the failing endpoint to
`additional_authorization_endpoints` in `~/.kiro/crew/oauth_endpoints.json`
(hand-edit, no restart needed — just retry Authorize in a fresh chat). This list
is **append-only**: add your entry without removing existing ones, or you drop
other trusted IdPs. If you were blocked on both hosts, add both objects.

```json
{ "additional_authorization_endpoints": [
  { "host": "<id>.execute-api.<region>.amazonaws.com", "path": "/authorize" },
  { "host": "cainban-<org>-<account_id>.auth.<region>.amazoncognito.com", "path": "/oauth2/authorize" }
] }
```

One-liner that appends one entry safely without clobbering existing entries
(requires `jq`) — copy **both** `HOST` and `PATH` straight from the error
message (the `/authorize` API-Gateway entry and the `/oauth2/authorize` Cognito
entry each need their own run):

```sh
HOST="<id>.execute-api.<region>.amazonaws.com"; PATH_="/authorize"
F=~/.kiro/crew/oauth_endpoints.json
[ -f "$F" ] || echo '{}' > "$F"
jq --arg h "$HOST" --arg p "$PATH_" '.additional_authorization_endpoints = ((.additional_authorization_endpoints // []) + [{host:$h, path:$p}] | unique)' "$F" > "$F.tmp" && mv "$F.tmp" "$F"
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
- **Step 3 — Cognito error page `invalid_request` with a bogus `client_id`
  (e.g. `client_id=Q+DEV+CLI`):** the server entry's `client_id` is wrong or
  missing and the runtime used a built-in default that cainban's Cognito pool
  does not know. Set the entry's `client_id` (and nested `oauth.clientId`) to the
  `<CLIENT_ID>` from the connect page — see the Kiro Crew runtime one-liner above
  — then restart and retry Authorize.
- **Step 4 — 401:** the token expired or is missing. On OAuth the client
  refreshes automatically; if it persists, re-authenticate (Step 3). On the
  bearer fallback, regenerate the token on the connect page.
- **Step 4 — 403:** your account's `repos` claim does not grant
  `<owner>/<repo>`. Connect the repo on the connect page, then retry.
