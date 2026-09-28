# cainban MCP OAuth — pre-registered client setup (step a)

> **Status:** step (a) of the Phase 5 MCP-native OAuth plan
> ([docs/phase5-mcp-oauth.md](phase5-mcp-oauth.md)) — cainban is now a
> spec-compliant OAuth 2.1 **resource server** (MCP authorization spec
> **2026-07-28**, RFC 9728 + RFC 8707). This works today with any MCP client
> that supports **pre-registration** (a configured `client_id`).
>
> **Not yet supported (step b):** the OAuth-proxy façade that lets a client
> with **no prior relationship** register itself. DCR (RFC 7591) is deprecated
> per the 2026-07-28 spec, and CIMD (Client ID Metadata Documents) is the
> forward path — **neither works against raw Cognito**, so both wait for the
> step-(b) proxy. See [docs/phase5-mcp-oauth.md](phase5-mcp-oauth.md) §3–§4.

## What step (a) adds

1. **RFC 9728 Protected Resource Metadata** — a **public** discovery endpoint:

   ```
   GET <McpApiUrl>/.well-known/oauth-protected-resource
   ```

   It requires **no authentication** (a client must be able to discover auth
   *before* it holds a token) and returns:

   ```json
   {
     "resource": "<McpApiUrl without trailing slash>",
     "authorization_servers": ["https://cognito-idp.<region>.amazonaws.com/<poolId>"],
     "scopes_supported": ["cainban:tasks"],
     "bearer_methods_supported": ["header"]
   }
   ```

   - `resource` is cainban's **canonical MCP URL** (the `McpApiUrl` stack output,
     trailing slash trimmed) — this is the value a client sends as the RFC 8707
     `resource` indicator.
   - `authorization_servers[0]` is the **Cognito issuer**, derived at runtime
     from the Lambda's `CAINBAN_AUTH_ISSUER` env (never hardcoded).

   In the API Gateway this one route is `AuthorizationType: NONE`
   (`HttpNoneAuthorizer`); **every other MCP route stays behind the managed
   Cognito JWT authorizer** — the same exemption pattern the connect
   `GET /connect/github/callback` route uses.

2. **`WWW-Authenticate` challenge on 401** — a request to a protected MCP route
   without a valid token now returns:

   ```
   HTTP/1.1 401 Unauthorized
   WWW-Authenticate: Bearer resource_metadata="<McpApiUrl>/.well-known/oauth-protected-resource", scope="cainban:tasks"
   ```

   A spec-compliant MCP client reads `resource_metadata` to find the discovery
   document above, and from it the authorization server.

3. **RFC 8707 resource-indicator awareness (documented, no behavior change).**
   Under MCP OAuth the client requests a token *for cainban* by sending
   `resource=<canonical MCP URL>` to the authorization server. cainban's
   in-Lambda validator still authorizes on the **existing `aud` check** (the
   token's `aud` must contain a configured Cognito app-client id) plus the
   validated `repos` claim — step (a) deliberately does **not** widen the
   accepted audiences, so the load-bearing audience binding is unchanged. If a
   future authorization server (the step-(b) proxy) mints tokens whose `aud` is
   the canonical MCP URL, add that URL to `CAINBAN_AUTH_AUDIENCE` (the validator
   already accepts a comma-separated list) at that time.

## Pre-registered client flow (what an MCP client does today)

cainban does **not** implement PKCE, the authorization-code exchange, or token
refresh — the **MCP client** does all of that against **Cognito** directly.
cainban only validates the resulting bearer token. To configure a client:

1. **Discover** (optional but spec-correct): `GET <McpApiUrl>/.well-known/oauth-protected-resource`
   and read `authorization_servers[0]` (the Cognito issuer). Fetch that issuer's
   OIDC discovery — `<issuer>/.well-known/openid-configuration` — for the
   `authorization_endpoint` and `token_endpoint`. (Cognito's authorize/token
   live under the **Hosted UI domain**, surfaced as the `HostedUiDomain` stack
   output, not under the issuer host.)

2. **Client id (pre-registration):** use the existing **SPA app client id**
   (`SpaClientId` stack output) — a public PKCE client, no secret — or register
   a dedicated Cognito app client for the MCP client. Cognito has **no**
   `registration_endpoint`, so the client id must be configured out of band
   (this is exactly the "pre-registration" the MCP spec allows, and the reason
   step (b) exists for clients that cannot be pre-configured).

3. **Authorize (PKCE authorization-code grant)** against the Cognito **Hosted
   UI** (`HostedUiDomain` output):

   ```
   GET https://<HostedUiDomain>/oauth2/authorize
       ?response_type=code
       &client_id=<SpaClientId>
       &redirect_uri=<one of the SPA client's registered callback URLs>
       &scope=openid+email+profile
       &code_challenge=<PKCE S256>
       &code_challenge_method=S256
   ```

   The user signs in (Entra-federated), and Cognito redirects back to
   `redirect_uri` with `?code=…`.

4. **Token exchange** at `https://<HostedUiDomain>/oauth2/token`
   (`grant_type=authorization_code`, the PKCE `code_verifier`, same
   `client_id`/`redirect_uri`). The client stores the tokens and **refreshes**
   them itself.

5. **Call cainban** with the token in the header (the only supported bearer
   method):

   ```
   POST <McpApiUrl>
   Authorization: Bearer <access-or-id-token>
   X-Cainban-Repo: <owner>/<repo>
   ```

   The token's validated `repos` claim must grant `<owner>/<repo>` or the
   request is a 403. (The `repos`/`default_repo` claims are populated by the
   pool's pre-token-generation trigger from the user's grants — see
   [docs/agent-via-mcp.md](agent-via-mcp.md) and
   [docs/phase4-github-connect-plan.md](phase4-github-connect-plan.md).)

## Stack outputs you need

| Output | Use |
| --- | --- |
| `McpApiUrl` | Canonical MCP URL (`resource`) + base of the discovery endpoint |
| `McpProtectedResourceMetadataUrl` | The RFC 9728 discovery endpoint (public) |
| `UserPoolId` / issuer | `authorization_servers[0]` = `https://cognito-idp.<region>.amazonaws.com/<UserPoolId>` |
| `HostedUiDomain` | Cognito authorize/token endpoints for the PKCE flow |
| `SpaClientId` | Pre-registered public PKCE `client_id` |
| `SpaOauthScopes` | `openid email profile` |

## Scope note

`scopes_supported` advertises `cainban:tasks` as a **hint**. cainban's actual
authorization decision is the validated `repos` claim (repo-scoped tenancy), not
an OAuth scope string — the scope is there for spec-conformance and future
read/write step-up (tracked with the authorization-hardening work), not as the
access-control mechanism today.
