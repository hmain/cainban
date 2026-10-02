# Connect Kiro and Claude Code to cainban's MCP OAuth

> **Where this fits:** this is step (a) of the Phase 5 plan. cainban is a
> spec-compliant OAuth 2.1 **resource server** (MCP authorization spec
> **2026-07-28**, RFC 9728 + RFC 8707), and it now also serves a small **RFC 8414
> authorization-server metadata shim** so that a PKCE-strict MCP client like
> Claude Code can finish the flow against Cognito.
>
> **What we skipped, deliberately — step (b):** the DCR/CIMD OAuth-proxy façade.
> DCR (RFC 7591) is deprecated under the 2026-07-28 spec and CIMD is where things
> are headed, but neither one works against raw Cognito, so both wait for the
> step-(b) proxy. The two clients below (Kiro with a header token, Claude Code
> with pre-registered PKCE) don't need a proxy.

## Why the shim exists: the PKCE-advertisement gap

Cognito supports S256 PKCE, but its OIDC discovery document
(`<issuer>/.well-known/openid-configuration`) reports:

```json
"code_challenge_methods_supported": null
```

The MCP spec says a spec-compliant client **MUST refuse to proceed** when that
field is absent. So Claude Code won't run the flow against Cognito's own
metadata, even though the PKCE it needs is fully supported.

Here's the fix. cainban serves a **public** RFC 8414 Authorization Server
Metadata document that mirrors Cognito's real endpoints and adds the one thing
Cognito leaves out — the PKCE advertisement:

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
  (`CAINBAN_HOSTED_UI_DOMAIN`, set in CDK from the `HostedUiDomain` output, not
  hardcoded). `jwks_uri` stays Cognito's (`CAINBAN_AUTH_ISSUER`) because the
  tokens are Cognito-signed.
- This is not an OAuth proxy. cainban mints no tokens, holds no secret, and
  terminates no flow. The client still runs PKCE, the code exchange, and refresh
  directly against Cognito's Hosted UI. All the shim does is let the client *see*
  that PKCE (S256) is available.

### How RFC 8414 resolves the discovery URL

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

For the deployed API-Gateway HTTP API
(`https://<apiId>.execute-api.<region>.amazonaws.com/`), that resolves to:

```
https://<apiId>.execute-api.<region>.amazonaws.com/.well-known/oauth-authorization-server
```

The `McpApiUrl` stack output ends in `/`; the **origin** is that URL with the
path stripped (`scheme://host`), which is exactly what the Lambda advertises
(`mcpAPIOrigin()` in `cmd/cainban-lambda/adapter.go`). The RFC 8414 `issuer` in
the shim equals this origin, which satisfies the RFC 8414 §3.3 issuer-match
check.

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

## Setup 1 — Kiro (remote MCP, header token, no OAuth flow)

Kiro connects to a remote MCP server by **URL + custom headers**. It doesn't run
the OAuth flow at all; you paste a **Cognito ID token** as a bearer header, which
you get from the cainban connect page. The token lasts about an hour, so re-paste
it when it expires. (Smoothing out that re-paste is one of the things the step-(b)
proxy will eventually handle for you.)

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

- `url` = the `McpApiUrl` stack output (the HTTP API root, Streamable-HTTP
  transport).
- `X-Cainban-Repo` names the target repo. The token's validated `repos` claim
  has to grant it, or the request gets a 403.
- No `client_id`, no discovery, no PKCE. Kiro just sends the header on every
  request.

## Setup 2 — Claude Code (OAuth auth-code + PKCE, browser Entra login)

Claude Code speaks the MCP Streamable-HTTP transport and runs the **OAuth flow
itself**: PKCE, the code exchange, and automatic token refresh. Use the
dedicated public client `McpCliClientId`.

Add the server:

```
claude mcp add --transport http --client-id <McpCliClientId> cainban <McpApiUrl>
```

Then, inside Claude Code:

```
/mcp
```

and pick **cainban → Authenticate**. Here's what Claude Code does:

1. Fetches `<McpApiUrl>` and gets a 401 with
   `WWW-Authenticate: Bearer resource_metadata="…/.well-known/oauth-protected-resource"`.
2. Reads the RFC 9728 doc, where `authorization_servers[0]` is the **MCP API
   origin**.
3. Runs RFC 8414 discovery at
   `<McpApiOrigin>/.well-known/oauth-authorization-server`, hits **our shim**, and
   sees `code_challenge_methods_supported:["S256"]` (the field Cognito omits)
   alongside Cognito's authorize/token endpoints.
4. Opens the browser to Cognito's Hosted UI. You sign in via **Entra**, and
   Cognito redirects back to Claude Code's **loopback callback** with the code.
5. Exchanges the code (PKCE) at Cognito's token endpoint, then stores and
   refreshes the tokens itself. No token lives in the config.

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

You still send `X-Cainban-Repo` on each call where the client supports custom
headers. Otherwise the token's `default_repo` claim picks the repo.

### Claude Code callback (redirect URI)

Claude Code uses a **loopback** callback, `http://localhost:<PORT>/callback`
(and the `127.0.0.1` literal). It picks an **ephemeral port** when the flow runs,
and Cognito requires each callback URL to be **registered exactly** — it does not
honor RFC 8252 port-agnostic loopback matching. (There are open Claude Code
issues where a ported loopback is sent but the provider expects the registered
value — see anthropics/claude-code #37747 and #90370.)

Since you can't know the exact port ahead of time, the `cainban-mcp-cli` client
registers the commonly-used ports plus a portless loopback, on both hostnames:

```
http://localhost:3118/callback      http://127.0.0.1:3118/callback
http://localhost:41842/callback     http://127.0.0.1:41842/callback
http://localhost/callback           http://127.0.0.1/callback
```

If your Claude Code build uses a different port, register it at deploy time:

```
cdk deploy -c mcpCliCallbackUrls="http://localhost:<PORT>/callback,http://127.0.0.1:<PORT>/callback"
```

(the context value **replaces** the default list). When the login browser lands
on a page that fails to load *after* a successful Entra sign-in, it's almost
always an unregistered port — read the address bar for the `?code=…` and check
the port against the list above.

---

## What cainban does and doesn't implement

What it does: validate the bearer token signature-first (Cognito JWKS), enforce
the repo-scoped `repos` claim for tenant isolation, serve the RFC 9728 and
RFC 8414 discovery documents publicly, and emit the RFC 9728 `WWW-Authenticate`
challenge on a 401.

What it doesn't: run PKCE, the authorization-code exchange, or token refresh —
the **client** does all of that against Cognito. It also mints no tokens and
holds no client secret. The RFC 8414 endpoint is a discovery shim, not an
authorization server.

What we left out on purpose (step b): the DCR/CIMD OAuth-proxy and consent UI.
Neither DCR nor CIMD works against raw Cognito, and the two clients above don't
need it.

## A note on scopes

The AS-metadata shim advertises `scopes_supported` as `openid email profile`
(the Cognito login scopes), and the RFC 9728 doc advertises `cainban:tasks` as a
resource-scope hint. But cainban's real authorization decision is the validated
`repos` claim — repo-scoped tenancy — not any OAuth scope string. So treat those
scope strings as discovery metadata, and look to the `repos` claim for what a
token can actually do.
