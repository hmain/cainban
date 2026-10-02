# cainban MCP OAuth — client setup (Kiro & Claude Code)

> **Status:** MCP-native OAuth step (a) of the Phase 5 plan — cainban is a
> spec-compliant OAuth 2.1 **resource server** (MCP authorization spec
> **2026-07-28**, RFC 9728 + RFC 8707) and now also serves a small **RFC 8414
> authorization-server metadata shim** so a PKCE-strict MCP client (Claude Code)
> can complete the flow against Cognito.
>
> **Not built (step b), on purpose:** the DCR/CIMD OAuth-proxy façade. DCR
> (RFC 7591) is deprecated per the 2026-07-28 spec and CIMD is the forward path,
> but **neither works against raw Cognito**, so both wait for the step-(b) proxy.
> The two clients below (Kiro via a header token, Claude Code via pre-registered
> PKCE) need no proxy.

## The PKCE-advertisement gap (why the shim exists)

Cognito supports S256 PKCE, but its OIDC discovery document
(`<issuer>/.well-known/openid-configuration`) reports:

```json
"code_challenge_methods_supported": null
```

Per the MCP spec a spec-compliant client **MUST refuse to proceed** when that
field is absent. So Claude Code will not run the flow against Cognito's own
metadata — even though the PKCE it needs is fully supported.

**Fix (this change):** cainban serves a **public** RFC 8414 Authorization Server
Metadata document that **mirrors Cognito's real endpoints** but **adds the PKCE
advertisement**:

```
GET <McpApiOrigin>/.well-known/oauth-authorization-server        (AuthorizationType: NONE)
```

```json
{
  "issuer": "<McpApiOrigin>",
  "authorization_endpoint": "<HostedUiDomain>/oauth2/authorize",
  "token_endpoint": "<HostedUiDomain>/oauth2/token",
  "jwks_uri": "<CognitoIssuer>/.well-known/jwks.json",
  "response_types_supported": ["code"],
  "grant_types_supported": ["authorization_code", "refresh_token"],
  "code_challenge_methods_supported": ["S256"],
  "scopes_supported": ["openid", "email", "profile"],
  "token_endpoint_auth_methods_supported": ["none"]
}
```

- `authorization_endpoint` / `token_endpoint` come from the **Hosted UI domain**
  (`CAINBAN_HOSTED_UI_DOMAIN`, set in CDK from the `HostedUiDomain` output — not
  hardcoded). `jwks_uri` stays **Cognito's** (`CAINBAN_AUTH_ISSUER`) because the
  tokens are Cognito-signed.
- This is **not an OAuth proxy**: cainban mints no tokens, holds no secret, and
  terminates no flow. The client still runs PKCE + the code exchange + refresh
  **directly against Cognito's Hosted UI**. The shim only lets the client *see*
  that PKCE (S256) is available.

### Discovery-URL math (RFC 8414) — the resolved URL

RFC 8414 §3 builds the metadata URL from the issuer by **inserting**
`/.well-known/oauth-authorization-server` **after the host and before any
path**:

| Issuer the client discovers on | RFC 8414 metadata URL |
| --- | --- |
| `https://h` (no path) | `https://h/.well-known/oauth-authorization-server` ✅ |
| `https://cognito-idp.<r>.amazonaws.com/<poolId>` (Cognito, has a path) | `https://cognito-idp.<r>.amazonaws.com/.well-known/oauth-authorization-server/<poolId>` ❌ (Cognito serves no such doc, and its OIDC doc omits PKCE) |

So the RFC 9728 **protected-resource** document advertises the **MCP API
origin** (a path-less issuer) as its `authorization_servers[0]`, and the shim
sets its own `issuer` to that same origin. A client then resolves discovery to:

```
<McpApiOrigin>/.well-known/oauth-authorization-server
```

**Concrete resolved URL** (for the deployed API-Gateway HTTP API
`https://<apiId>.execute-api.<region>.amazonaws.com/`):

```
https://<apiId>.execute-api.<region>.amazonaws.com/.well-known/oauth-authorization-server
```

The `McpApiUrl` stack output ends in `/`; the **origin** is that URL with the
path stripped (`scheme://host`), which is exactly what the Lambda advertises
(`mcpAPIOrigin()` in `cmd/cainban-lambda/adapter.go`). The RFC 8414 `issuer` in
the shim equals this origin, satisfying the RFC 8414 §3.3 issuer-match check.

## Stack outputs you need

| Output | Use |
| --- | --- |
| `McpApiUrl` | The MCP server URL (Kiro `url`; Claude Code server URL). Its **origin** is the RFC 8414 issuer. |
| `McpProtectedResourceMetadataUrl` | RFC 9728 discovery endpoint (public) |
| `HostedUiDomain` | Cognito authorize/token base (feeds the shim + the header-token connect page) |
| `SpaClientId` | Public PKCE client for the browser SPA / connect page |
| **`McpCliClientId`** | **Dedicated public PKCE client `cainban-mcp-cli` for Claude Code** (code flow, no secret, Entra IdP) |
| `UserPoolId` / issuer | `https://cognito-idp.<region>.amazonaws.com/<UserPoolId>` (the `jwks_uri` base) |

---

## Setup 1 — Kiro (remote MCP, header token, NO OAuth flow)

Kiro connects to a remote MCP server by **URL + custom headers**. It does **not**
run the OAuth flow; you paste a **Cognito ID token** as a bearer header (obtain
it from the cainban connect page). The token is ~1h-lived — re-paste when it
expires (this is the UX the step-(b) proxy would eventually remove).

`mcp.json`:

```json
{
  "mcpServers": {
    "cainban": {
      "url": "<McpApiUrl>",
      "headers": {
        "Authorization": "Bearer <Cognito ID token from the connect page>",
        "X-Cainban-Repo": "<owner>/<repo>"
      }
    }
  }
}
```

- `url` = the `McpApiUrl` stack output (the HTTP API root; Streamable-HTTP
  transport).
- `X-Cainban-Repo` names the target repo; the token's validated `repos` claim
  must grant it or the request is a 403.
- No `client_id`, no discovery, no PKCE — Kiro just sends the header on every
  request.

## Setup 2 — Claude Code (OAuth auth-code + PKCE, browser Entra login)

Claude Code speaks the MCP Streamable-HTTP transport and runs the **OAuth flow
itself** (PKCE, code exchange, and **automatic token refresh**). Use the
dedicated public client `McpCliClientId`.

Add the server:

```
claude mcp add --transport http --client-id <McpCliClientId> cainban <McpApiUrl>
```

Then, inside Claude Code:

```
/mcp
```

and pick **cainban → Authenticate**. Claude Code:

1. Fetches `<McpApiUrl>` → 401 with
   `WWW-Authenticate: Bearer resource_metadata="…/.well-known/oauth-protected-resource"`.
2. Reads the RFC 9728 doc → `authorization_servers[0]` = the **MCP API origin**.
3. Runs RFC 8414 discovery at
   `<McpApiOrigin>/.well-known/oauth-authorization-server` → **our shim**, which
   advertises `code_challenge_methods_supported:["S256"]` (the field Cognito
   omits) and Cognito's authorize/token endpoints.
4. Opens the browser to Cognito's Hosted UI, you sign in via **Entra**, and
   Cognito redirects back to Claude Code's **loopback callback** with the code.
5. Exchanges the code (PKCE) at Cognito's token endpoint and **stores +
   refreshes** the tokens itself. No token lives in the config.

Equivalent JSON config form (e.g. `.mcp.json` / project config):

```json
{
  "mcpServers": {
    "cainban": {
      "type": "http",
      "url": "<McpApiUrl>",
      "client_id": "<McpCliClientId>"
    }
  }
}
```

You still send `X-Cainban-Repo` per call where the client supports custom
headers; otherwise the token's `default_repo` claim selects the repo.

### Claude Code callback (redirect URI)

Claude Code uses a **loopback** callback, `http://localhost:<PORT>/callback`
(and the `127.0.0.1` literal). It picks an **ephemeral port** at flow time, and
Cognito requires each callback URL to be **registered exactly** — it does *not*
honor RFC 8252 port-agnostic loopback matching. (There are open Claude Code
issues where a ported loopback is sent while the provider expects the registered
value — e.g. anthropics/claude-code #37747, #90370.)

Because the exact port cannot be known in advance, the `cainban-mcp-cli` client
registers the commonly-used ports plus a portless loopback, on both hostnames:

```
http://localhost:3118/callback      http://127.0.0.1:3118/callback
http://localhost:41842/callback     http://127.0.0.1:41842/callback
http://localhost/callback           http://127.0.0.1/callback
```

If your Claude Code build uses a different port, add it at deploy time:

```
cdk deploy -c mcpCliCallbackUrls="http://localhost:<PORT>/callback,http://127.0.0.1:<PORT>/callback"
```

(the context value **replaces** the default list). If the login browser lands on
a page that fails to load *after* a successful Entra sign-in, it is almost
always the port not being registered — read the address bar for the `?code=…`
and check the port against the list above.

---

## What cainban does / does not implement

- **Does:** validate the bearer token signature-first (Cognito JWKS), enforce
  the repo-scoped `repos` claim (tenant isolation), serve the RFC 9728 +
  RFC 8414 discovery documents publicly, and emit the RFC 9728
  `WWW-Authenticate` challenge on 401.
- **Does NOT:** run PKCE, the authorization-code exchange, or token refresh
  (the **client** does all of that against Cognito); mint tokens; hold any
  client secret. The RFC 8414 endpoint is a **discovery shim**, not an
  authorization server.
- **Intentionally NOT built (step b):** the DCR/CIMD OAuth-proxy + consent UI.
  Neither DCR nor CIMD works against raw Cognito, and the two clients above do
  not need it.

## Scope note

`scopes_supported` advertises `openid email profile` (the Cognito login scopes)
in the AS-metadata shim, and the RFC 9728 doc advertises `cainban:tasks` as a
**resource scope hint**. cainban's actual authorization decision is the
validated `repos` claim (repo-scoped tenancy), not an OAuth scope string.
