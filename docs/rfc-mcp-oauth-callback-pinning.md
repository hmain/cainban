# RFC: Pin the Kiro MCP OAuth callback so sign-in stops failing

## Status

Proposed. Diagnosis and the Kiro config schema below are verified against the
live Cognito client and the official Kiro MCP docs (kiro.dev, updated
2026-10-01). No code is changed yet.

## Problem

Authorizing cainban as a remote MCP server from the KiroCrew dashboard's
**Authorize** banner fails with Cognito's generic page: *"An error was
encountered with the requested page."* The server registers correctly; the
browser sign-in never completes.

The config cainban's connect page and `cainban-mcp-setup.md` emit today carries
a `clientId` and headers but **no OAuth redirect pin**:

```json
{
  "mcpServers": {
    "cainban": {
      "url": "https://cl4qelp4hf.execute-api.eu-north-1.amazonaws.com",
      "clientId": "79jr68h816vb3bcbgi6ueum2ht",
      "headers": { "X-Cainban-Repo": "<owner>/<repo>" }
    }
  }
}
```

## Root cause

When `oauth.redirectUri` is omitted, Kiro's loopback OAuth client **picks a
random available port** for its callback listener (Kiro MCP configuration docs,
"Redirect URI formats": *"OS assigns a random available port on `localhost`"*).

cainban's Cognito app client cannot match a random port. Cognito requires every
callback URL to be registered **exactly** — it does not honor RFC 8252
port-agnostic loopback matching. So the authorize request carries a
`redirect_uri` on an unregistered port, Cognito returns `redirect_mismatch`, and
the browser lands on its generic error page.

Verified directly against the live authorize endpoint:

- Registered callback (`:3118`) -> `302 -> /login` (accepted).
- Unregistered port -> `302 -> /error?error=redirect_mismatch`.

This is distinct from, but related to, Kiro issue
[#11586](https://github.com/kirodotdev/Kiro/issues/11586) (an *unpinned*
callback also mismatches on path and host inside kiro-cli). Pinning the redirect
sidesteps both: the docs state the pinned value is *"the exact `redirect_uri`
Kiro sends and the only endpoint its local server answers."*

Three earlier doc PRs (#44, #45, #46, #47) improved the setup UX but none pinned
the callback, so none could fix this — documentation cannot out-argue a random
port.

## The Kiro config that fixes it (verified schema)

Kiro's documented remote-server `oauth` block supports a `redirectUri` pin:

| `oauth` key | Meaning |
|---|---|
| `clientId` | Pre-registered client; skips Dynamic Client Registration |
| `redirectUri` | Pins the loopback callback (host, port, path) |
| `oauthScopes` | Scopes to request (`[]` if the server rejects scopes) |

`redirectUri` formats (from the docs):

| Format | Example | Path default |
|---|---|---|
| Full URL | `http://localhost:7778/oauth/callback` | explicit |
| Host + port | `127.0.0.1:7778` | `/oauth/callback` |
| Port only | `:7778` | `/oauth/callback` |
| Omitted | *(random port)* | `/oauth/callback` |

Two documented constraints decide the exact value:

1. Kiro's loopback listener serves the path **`/oauth/callback`** (the default
   for every pinned form).
2. `localhost` and `127.0.0.1` are **not interchangeable** to the auth server —
   the registered value must match the spelling Kiro sends character-for-character.

### The one callback that already matches

cainban's live Cognito client registers these callbacks:

```
http://127.0.0.1/callback          http://localhost/callback
http://127.0.0.1:3118/callback     http://localhost:3118/callback
http://127.0.0.1:3334/oauth/callback   http://localhost:3334/oauth/callback
http://127.0.0.1:41842/callback    http://localhost:41842/callback
http://127.0.0.1:56152/callback    http://localhost:56152/callback
```

Every `/callback` entry has the **wrong path** for Kiro (it serves
`/oauth/callback`). The only registered pair that matches Kiro's path is
**`:3334/oauth/callback`**. So the pin must be:

```json
{
  "mcpServers": {
    "cainban": {
      "url": "https://cl4qelp4hf.execute-api.eu-north-1.amazonaws.com",
      "oauth": {
        "clientId": "79jr68h816vb3bcbgi6ueum2ht",
        "redirectUri": "http://127.0.0.1:3334/oauth/callback",
        "oauthScopes": ["openid", "email", "profile"]
      },
      "headers": { "X-Cainban-Repo": "<owner>/<repo>" }
    }
  }
}
```

Both the dashboard "Add Custom Server" parser and the file-based `mcp.json`
loader accept this `oauth` block (the parser shape-checks only
`oauth.clientId`/`oauth.oauthScopes` and passes other sub-keys, including
`redirectUri`, through verbatim to the runtime).

## The infra drift this exposes (must fix together)

The CLI/MCP client's callback URLs are defined in
`infra/stack.go` (`mcpCliCallbackUrls`, the `McpCliClient` default). That default
registers only **`/callback`** paths:

```
http://localhost:3118/callback   http://127.0.0.1:3118/callback
http://localhost:41842/callback  http://127.0.0.1:41842/callback
http://localhost/callback        http://127.0.0.1/callback
```

It does **not** include `3334/oauth/callback`. The live registration of
`3334/oauth/callback` was therefore added out-of-band and is **not in tracked
source**. This is the repo's known CDK-context drift footgun: a plain
`cdk deploy` would synthesize the source default and **revert** the one callback
that makes Kiro sign-in work.

So the fix is two coordinated changes, not one:

1. **Infra** — add the matching `/oauth/callback` URLs to the `mcpCliCallbackUrls`
   default in `infra/stack.go` (both `127.0.0.1` and `localhost` on port 3334, or
   a chosen pinned port), so a flagless deploy keeps them registered. Persist the
   real live value in tracked `cdk.json` context if context is the source of
   truth. Run `cdk diff --strict` and read it fully before deploy — the revert
   only shows against deployed state.
2. **Docs + connect page** — emit the pinned `redirectUri` config above:
   - `web/public/cainban-mcp-setup.md` — the KiroCrew/dashboard recipe and the
     generic remote config.
   - `web/src/App.tsx` — `agentPrompt` / `oauthConfig` generation.

## Alternatives considered

- **Register more random ports** — rejected. Cognito cannot wildcard ports; this
  is the whack-a-mole trap already on file for cainban MCP OAuth.
- **Port-only pin (`:3334`)** — works (path defaults to `/oauth/callback`), but a
  full URL is clearer in the doc and removes ambiguity about host spelling.
- **CIMD (`clientMetadataUrl`)** — heavier; still requires a pinned `redirectUri`.
  Not warranted for a single pre-registered client.
- **Keep documenting the error** — rejected; the earlier doc PRs show a random
  port cannot be fixed with prose.

## Scope boundary

This RFC is about the OAuth callback port only. The separate header/`[REDACTED]`
masking question (`default_repo` header-free config) is tracked in
`docs/rfc-default-repo-headerless.md`; the two are complementary and can ship
independently.

## Verification plan

1. Land the infra change; `cdk diff --strict` shows ONLY the added callback URLs.
2. Deploy; confirm `describe-user-pool-client` lists `:3334/oauth/callback` on
   both hosts.
3. Ship the doc/connect-page config; confirm the live `setup.md` and SPA bundle
   carry the pinned `redirectUri`.
4. End-to-end: paste the config, run the KiroCrew Authorize flow, confirm it
   reaches the Cognito login page (not the error page) and completes to a token.
5. In a fresh chat, `list_tasks` returns (empty board = success; 403 = repo not
   granted).
