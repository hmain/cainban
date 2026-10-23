# Set up the cainban MCP server for your AI coding agent

cainban is a per-repo kanban board exposed as a remote MCP server (Streamable
HTTP). This guide sets it up as the task backend for an AI coding tool (Claude
Code, Kiro, Cursor, VS Code). You register the server with a GitHub token,
verify by listing the board, and point the agent at your repo.

**Auth is GitHub-only.** cainban authenticates the *user* by their own GitHub
token and authorizes each request server-side with a GitHub App: a request is
allowed for `owner/repo` when the App is installed on that repo and the user is
an org member or a repo collaborator. There is no Cognito, no OAuth redirect, and
no browser sign-in — the token is obtained once (device flow or a fine-grained
PAT) and placed in the MCP client config as a bearer. The agent handles the
token only as opaque config; authorization is always re-checked at the server
against GitHub.

## What you need

- **mcp_api** — the cainban MCP server URL (`<MCP_API>`), on the connect page.
- **github_token** — the user's GitHub token. The easiest way to get one is the
  connect page's **Connect with GitHub** button (GitHub device flow); it returns
  a token to paste. A fine-grained PAT with **Metadata: Read-only** on the target
  repo also works. This IS a credential — never print it, log it, or commit it;
  put it only in the client's MCP config.
- **repo** — the target repository as `<owner>/<repo>`. Name it with an
  `X-Cainban-Repo` header, or omit it and pass the repo per request. The repo
  string is a plain selector, never a secret — authorization is the server-side
  GitHub App check, never the header.

If the token genuinely wasn't given to you, point the user at the connect page's
**Connect with GitHub** button and ask them to paste the resulting token — in a
single message — then carry on. Never ask them to invent or hand over a password
or secret beyond that token.

### Prerequisites

An MCP-capable AI tool that supports the MCP Streamable-HTTP transport with
custom request headers (Claude Code and most modern clients do), and a GitHub
token for the user. No OAuth-capable client is required — a plain
`Authorization: Bearer` header is all cainban needs.

## How to run this

Do the steps yourself — run the commands, write the config — and tell the user
which step you're on. The one step that is the user's is obtaining the GitHub
token (the device-flow click on the connect page); pause there if you don't
already have it.

Two things that trip people up, so expect them:

- A newly-added MCP server usually loads only in a **new session**, not the one
  you added it in. After registering, verify in a fresh chat — the cainban tools
  won't appear in the current one.
- Verifying can return **403** — that means the GitHub App isn't installed on the
  repo, or the user isn't a member/collaborator. It's not a setup bug: install
  the App on the repo (or get added to it) and retry. See Troubleshooting.

If a step fails for another reason, check Troubleshooting at the end; if it's not
covered there, report the full output and stop.

## Fast path: auto-configure (try this first)

Before the manual steps, try the one-command path. It fetches the correct MCP
URL **from the server itself**, so it cannot pick the wrong URL — the most common
setup mistake (a Connect-style URL and the MCP API URL look alike, and only the
MCP API serves the discovery documents).

### Step 0: get the `cainban` binary

The one-command path runs the `cainban` CLI's `connect-agent` command. Get the
binary first — in this order, stopping at the first that works:

**1. Download the latest GitHub release** (no toolchain needed), from
[github.com/hmain/cainban/releases/latest](https://github.com/hmain/cainban/releases/latest).
Pick the asset for your OS/arch and put it on your `PATH` as `cainban`:

```sh
# Linux x86_64 example — swap the asset name for your platform:
#   cainban-linux-amd64 | cainban-linux-arm64 |
#   cainban-darwin-amd64 | cainban-darwin-arm64 | cainban-windows-amd64.exe
curl -fsSL -o cainban \
  https://github.com/hmain/cainban/releases/latest/download/cainban-linux-amd64
chmod +x cainban && sudo mv cainban /usr/local/bin/cainban   # or any PATH dir
```

**2. Confirm the release is recent enough:**

```sh
cainban connect-agent --help
```

If that prints usage, skip to the `connect-agent` command below. If it errors
with an unknown-command error, the release predates `connect-agent`; use option 3
or 4.

**3. Build from source** (needs the Go toolchain, Go 1.26+):

```sh
go install github.com/hmain/cainban/cmd/cainban@latest   # lands in $(go env GOPATH)/bin
# …or in a checkout of the repo:  make build   # produces ./bin/cainban
```

**4. No toolchain and the release is stale — make GitHub build it.** The release
workflow builds all platform binaries on a `v*` tag (needs push access):

```sh
git tag v0.2.3 && git push origin v0.2.3      # use the next version number
```

When the run finishes, the binaries are on the
[releases page](https://github.com/hmain/cainban/releases/latest) — go back to
option 1.

### Run `connect-agent`

With a `connect-agent`-capable binary on `PATH`, run (substitute `<MCP_API>`):

```
cainban connect-agent --mcp-url <MCP_API> --format kirocrew
```

Pick the `--format` for your tool: `kirocrew`, `claude`, `kiro-ide`, `cursor`, or
`vscode`. It prints the config to merge (add `--output <path>` to write it). It
first confirms `<MCP_API>` is really the MCP server before writing anything, so a
wrong URL fails with a clear message instead of a broken config.

The emitted config carries the server URL only — **you add the GitHub token
yourself** (Step 2), because the token is the user's credential and the CLI never
sees it.

**Can't get the binary at all?** The same data is a public endpoint any agent can
GET: `<MCP_API>/.well-known/mcp-client-config`. It returns the MCP URL and
pre-built per-client server snippets (`configs.kirocrew`, `configs.kiro_ide`,
`configs.cursor`, `configs.vscode`, `configs.claude_code_command`). Fetch it,
pick your client's snippet, add the `Authorization` header, and write it. If that
endpoint 404s, fall back to the manual steps.

## Steps

### Step 1: Identify yourself, then use only your own recipe

**State which tool you are** — Claude Code, Kiro, Kiro IDE, Kiro Crew, Cursor, or
VS Code — before doing anything. If you cannot tell from your runtime, ask the
user "which tool am I running in?" and wait; don't guess, because each tool's MCP
config target differs.

| Agent       | MCP config              | Location                     |
| ----------- | ----------------------- | ---------------------------- |
| Claude Code | `claude mcp add` / `.mcp.json` | CLI or project root   |
| Kiro Crew   | `~/.kiro/crew/mcp.json` | gateway host (then `kirocrew restart`) |
| Kiro / Kiro IDE | `mcp.json`          | Kiro MCP settings            |
| Cursor      | `.cursor/mcp.json`      | `.cursor/` directory         |
| VS Code     | `.vscode/mcp.json`      | `.vscode/` directory         |

Go straight to your tool's recipe in **Per-agent quick setup** and follow ONLY
it. Each recipe is self-contained (register → where it loads → verify).

### Step 2: Register with the GitHub token

cainban takes the GitHub token as a bearer header — there is no OAuth flow to
run. Merge this entry into the client's existing `mcpServers` (do NOT overwrite
other servers); substitute `<MCP_API>`, the user's `<GITHUB_TOKEN>`, and
optionally `<owner>/<repo>`:

```json
{
  "mcpServers": {
    "cainban": {
      "url": "<MCP_API>",
      "headers": {
        "Authorization": "Bearer <GITHUB_TOKEN>",
        "X-Cainban-Repo": "<owner>/<repo>"
      }
    }
  }
}
```

Drop `X-Cainban-Repo` to pass the repo per request instead of pinning a default.
The token is a credential — keep it only in this config file, never in chat, logs,
or a committed file.

> **`type` field:** Kiro and Kiro Crew infer the transport from `url` and
> **reject** a `"type"` field — omit it (as above). Cursor and VS Code instead
> want `"type": "http"` on the entry; add it for those two clients only.

> **Header masking is cosmetic.** Some clients (including the Kiro Crew
> dashboard) display every header value as `[REDACTED]`. The stored value is
> intact and the connection works; don't try to "fix" the mask.

**Success:** the server entry is present with the `Authorization` header. Go to
Step 3 to verify (there is no separate sign-in step).

### Per-agent quick setup

Ready-made recipes. Each is self-contained. Substitute `<MCP_API>`,
`<GITHUB_TOKEN>`, and `<owner>/<repo>`. Follow your tool's recipe, then verify
(Step 3).

#### Claude Code

```
claude mcp add --transport http \
  --header "Authorization: Bearer <GITHUB_TOKEN>" \
  cainban <MCP_API>
```

Add `--header "X-Cainban-Repo: <owner>/<repo>"` to pin a repo. Verify with
`list_tasks` in a fresh session.

#### Kiro Crew dashboard (Add Custom Server)

The dashboard's **Settings → MCP Servers → Add Custom Server** paste box accepts
`url` and `headers` (it rejects `type` — transport is inferred from the URL):

```json
{
  "mcpServers": {
    "cainban": {
      "url": "<MCP_API>",
      "headers": {
        "Authorization": "Bearer <GITHUB_TOKEN>",
        "X-Cainban-Repo": "<owner>/<repo>"
      }
    }
  }
}
```

#### Kiro Crew runtime (this gateway, config file)

The agent config is layered: edit the writable source and let the gateway project
it — never hand-edit the read-only projection.

1. Merge the cainban server into `~/.kiro/crew/mcp.json` (the highest-priority
   **writable** MCP source):

   ```json
   {
     "mcpServers": {
       "cainban": {
         "url": "<MCP_API>",
         "headers": {
           "Authorization": "Bearer <GITHUB_TOKEN>",
           "X-Cainban-Repo": "<owner>/<repo>"
         }
       }
     }
   }
   ```

   Do **not** edit the projected `~/.kiro/agents/<agent>.json` (gateway-owned),
   and do **not** add `allowedTools` to the entry — the gateway revokes it on the
   next rebuild. A one-liner that merges the entry without clobbering other
   servers (requires `jq`):

   ```sh
   TOKEN="<GITHUB_TOKEN>"; URL="<MCP_API>"; REPO="<owner>/<repo>"
   F=~/.kiro/crew/mcp.json
   [ -f "$F" ] || echo '{}' > "$F"
   jq --arg t "$TOKEN" --arg url "$URL" --arg repo "$REPO" \
     '.mcpServers.cainban = {type:"http", url:$url, headers:{Authorization:("Bearer "+$t), "X-Cainban-Repo":$repo}}' \
     "$F" > "$F.tmp" && mv "$F.tmp" "$F"
   ```
2. The gateway owner rebuilds and restarts from a **terminal** (`kirocrew
   restart`) — not from inside a chat.
3. Open a **fresh chat** and verify: `list_tasks` for `<owner>/<repo>`. An empty
   board is success.

Note: because cainban uses a bearer token (no OAuth redirect), the Kiro Crew
exfil-gate / `oauth_endpoints.json` allowlist does **not** apply — there is no
authorize host to allowlist.

#### Kiro / Kiro IDE

Add the entry to Kiro's MCP config (`mcp.json`), merging into any existing
`mcpServers`. **No `type` field** — Kiro infers HTTP transport from `url` and
rejects a `type`:

```json
{
  "mcpServers": {
    "cainban": {
      "url": "<MCP_API>",
      "headers": {
        "Authorization": "Bearer <GITHUB_TOKEN>",
        "X-Cainban-Repo": "<owner>/<repo>"
      }
    }
  }
}
```

Reload the MCP servers, then verify with `list_tasks`.

#### Cursor / VS Code

Same entry in `.cursor/mcp.json` or `.vscode/mcp.json`, but these two clients
**do** take `"type": "http"`:

```json
{
  "mcpServers": {
    "cainban": {
      "type": "http",
      "url": "<MCP_API>",
      "headers": {
        "Authorization": "Bearer <GITHUB_TOKEN>",
        "X-Cainban-Repo": "<owner>/<repo>"
      }
    }
  }
}
```

Reload the window / restart the MCP servers, then verify with `list_tasks`.

### Step 3: Verify the connection

Call the `list_tasks` tool (or `list_boards`) for the target repo, in a fresh
session.

**Success:** the tool returns without error (an empty board is a valid success).
A **401** means the GitHub token is missing, expired, or invalid — refresh it on
the connect page (or regenerate the PAT). A **403** means the GitHub App is not
installed on `<owner>/<repo>`, or the user is not a member/collaborator — fix
that on GitHub and retry. Confirm to the user that cainban is working.

### Step 4: Use the board

Use cainban as the task backend for `<owner>/<repo>`:

1. Before starting work, call `list_tasks` to see the current board.
2. Decompose the work into tasks with `create_task`.
3. Move each task with `update_task_status` (`todo` → `doing` → `done`).
4. Keep the board reflecting reality — it is the shared source of truth.

**Available tools:** `list_boards`, `change_board`, `list_tasks`, `get_task`,
`create_task`, `update_task`, `update_task_status`, `update_task_priority`,
`search_tasks`, `delete_task`, `restore_task`, `link_tasks`, `unlink_tasks`,
`get_task_links`, `list_links`, `list_activity`, `whoami`.

End by telling the user: cainban is set up for `<owner>/<repo>`.

### Step 5 (recommended): Install a steering rule so cainban is used automatically

So the user never has to say "use cainban" again, write a rule into THIS
project's agent-rules file:

| Agent       | Rules file                 | Location                  |
| ----------- | -------------------------- | ------------------------- |
| Claude Code | `CLAUDE.md`                | project root              |
| Codex / generic | `AGENTS.md`            | project root              |
| Kiro        | `.kiro/steering/cainban.md`| `.kiro/steering/`         |
| Cursor      | `.cursor/rules/cainban.mdc`| `.cursor/rules/`          |

Write it **non-destructively**: create the file if absent; if it exists, append
between `<<<CAINBAN_STEERING_START>>>` / `<<<CAINBAN_STEERING_END>>>` markers so
re-running replaces only the marked block. Where the project's own instructions
conflict, the project wins.

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
  in chat or scratch files — the board is it.
```

**Success:** the rule is present, pre-existing instructions are preserved, and a
fresh session uses cainban without prompting.

## Troubleshooting

- **Step 2 — where do I get the token?** The connect page's **Connect with
  GitHub** button runs GitHub's device flow and returns a token to paste. A
  fine-grained PAT with **Metadata: Read-only** on the repo also works. cainban
  never needs more than read-only metadata from the token.
- **Step 3 — 401:** the GitHub token is missing, expired, or malformed. The
  server validates it against GitHub on every request. Get a fresh token from the
  connect page (or regenerate the PAT) and update the `Authorization` header.
- **Step 3 — 403:** the GitHub App is not installed on `<owner>/<repo>`, or the
  user is not an org member / repo collaborator. Install the App on the repo (or
  get added as a collaborator) and retry — this is a GitHub-side grant, not a
  cainban config bug.
- **Tools don't appear after adding the server:** most clients load a new MCP
  server only in a new session. Start a fresh chat and re-check.
