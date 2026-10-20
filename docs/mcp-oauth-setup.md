# Connect Kiro and Claude Code to cainban's MCP OAuth

cainban is a spec-compliant OAuth 2.1 **resource server** (MCP authorization spec
**2026-07-28**, RFC 9728 + RFC 8707). It serves a small **RFC 8414
authorization-server metadata shim** so that PKCE-strict MCP clients like Claude
Code can complete the OAuth flow against Cognito.

## Why the shim exists: the PKCE-advertisement gap

Cognito supports S256 PKCE, but its OIDC discovery document reports:

```json
"code_challenge_methods_supported": null
```

The MCP spec says a spec-compliant client **MUST refuse to proceed** when that
field is absent. So Claude Code won't run the flow against Cognito's own
metadata, even though PKCE is fully supported.

cainban serves a **public** RFC 8414 Authorization Server Metadata document that
mirrors Cognito's real endpoints and adds the PKCE advertisement:

```
GET <McpApiOrigin>/.well-known/oauth-authorization-server
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

This is not an OAuth proxy. cainban mints no tokens and holds no secret. The
client runs PKCE, code exchange, and refresh directly against Cognito's Hosted UI.

## Stack outputs you need

| Output | Use |
| --- | --- |
| `McpApiUrl` | The MCP server URL |
| `McpProtectedResourceMetadataUrl` | RFC 9728 discovery endpoint (public) |
| `HostedUiDomain` | Cognito authorize/token base |
| `SpaClientId` | Public PKCE client for the browser SPA |
| **`McpCliClientId`** | **Dedicated public PKCE client for Claude Code** |
| `UserPoolId` | Cognito user pool |

---

## Setup 1 — Kiro (remote MCP, header token)

Kiro connects via URL + custom headers. Paste a Cognito ID token as a bearer
header from a signed-in session.

`mcp.json`:

```json
{
  "mcpServers": {
    "cainban": {
      "url": "<McpApiUrl>",
      "headers": {
        "Authorization": "Bearer <Cognito ID token>",
        "X-Cainban-Repo": "<owner>/<repo>"
      }
    }
  }
}
```

- `X-Cainban-Repo` names the target repo. Your GitHub user must have push access.

## Setup 2 — Claude Code (OAuth auth-code + PKCE)

Claude Code runs the OAuth flow itself: PKCE, code exchange, and automatic token
refresh. Use the dedicated public client `McpCliClientId`.

Add the server:

```
claude mcp add --transport http --client-id <McpCliClientId> cainban <McpApiUrl>
```

Then inside Claude Code:

```
/mcp
```

and pick **cainban → Authenticate**. Claude Code will:

1. Fetch `<McpApiUrl>` and get a 401 with the RFC 9728 challenge
2. Read the protected-resource doc
3. Run RFC 8414 discovery and see `code_challenge_methods_supported:["S256"]`
4. Open the browser to Cognito's Hosted UI for Entra sign-in
5. Exchange the code and store/refresh tokens automatically

Equivalent JSON config:

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

### Claude Code callback (redirect URI)

Claude Code uses a **loopback** callback (`http://localhost:<PORT>/callback`).
It picks an ephemeral port, and Cognito requires exact registration. The
`cainban-mcp-cli` client registers commonly-used ports:

```
http://localhost:3118/callback      http://127.0.0.1:3118/callback
http://localhost:3334/oauth/callback http://127.0.0.1:3334/oauth/callback
http://localhost:41842/callback     http://127.0.0.1:41842/callback
http://localhost/callback           http://127.0.0.1/callback
```

If your Claude Code build uses a different port, register it at deploy time:

```
cdk deploy -c mcpCliCallbackUrls="http://localhost:<PORT>/callback,..."
```

---

## Authorization model

With **GitHub-native permissions** enabled (`CAINBAN_USE_GITHUB_PERMISSIONS=true`):

1. User authenticates via Cognito (Entra federation)
2. Token includes `github_user` and `github_token` claims
3. On each request, cainban checks the GitHub API to verify push access
4. Results are cached for 5 minutes

Users can access **any repo they have push access to on GitHub** — no separate
"connect" step is required.

## What cainban implements

**Does:**
- Validate bearer token signature-first (Cognito JWKS)
- Check GitHub API for repo permissions (cached 5 min)
- Serve RFC 9728 and RFC 8414 discovery documents publicly
- Emit RFC 9728 `WWW-Authenticate` challenge on 401

**Doesn't:**
- Run PKCE, code exchange, or token refresh — the client does that
- Mint tokens or hold client secrets
- Store grants in a database — permissions are checked live via GitHub
