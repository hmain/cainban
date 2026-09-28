# cainban Phase 5 — MCP-native OAuth ("set and forget" agent auth)

> Status: **design/plan** (not a build). Researched against the MCP
> authorization spec **2026-07-28** (latest; supersedes 2025-11-25) and AWS
> Cognito's OAuth capabilities (2025-Q3). Goal: an MCP client signs in ONCE via
> OAuth and refreshes tokens itself, so nothing long-lived or huge sits in the
> agent's config — replacing today's "paste a 1-hour ID token and re-copy it
> hourly" UX.

## 0. What changed in spec 2026-07-28 (vs the 2025-11-25 basis)

Two authorization-relevant deltas, plus protocol-level changes that affect
cainban's MCP server independently of OAuth:

- **DCR is now DEPRECATED** (12-month window), not merely optional. **Client ID
  Metadata Documents (CIMD)** is the forward registration path. This
  *strengthens* the plan below: the proxy should present **CIMD**, with DCR only
  as a deprecated fallback.
- **New client duty (server-adjacent):** MCP clients MUST validate the `iss`
  parameter in the authorization response (RFC 9207); credentials are keyed by
  issuer and re-registered on issuer change. Our auth-server/proxy MUST emit
  `iss` correctly.
- **Protocol (not OAuth) — affects the MCP server when we upgrade the go-sdk:**
  sessions are gone (`Mcp-Session-Id` + the `initialize` handshake removed; MCP
  is fully stateless with per-request `_meta`); new `server/discover` RPC;
  required `Mcp-Method`/`Mcp-Name` headers; `CacheableResult` (`ttlMs`/
  `cacheScope`) on list results; deterministic `tools/list` order. cainban's
  handler is already stateless, so this is a clean SDK-upgrade item, tracked
  separately from this OAuth work.

## 1. The goal, precisely

Today the connect page hands the user a Cognito **ID token** to paste into
`Authorization: Bearer` in their MCP client config. It is large and expires in
~1h, so it is neither "set and forget" nor pleasant. The MCP-native answer:
the **MCP client** performs OAuth against cainban's authorization server, stores
the tokens, and **auto-refreshes** — re-prompting the human only when the
refresh chain finally lapses (~24h+, configurable). The config holds **no
token**.

## 2. What the MCP spec (2026-07-28) requires of the SERVER

cainban is an OAuth 2.1 **resource server**; it never authenticates the user
itself. Its obligations:

1. **Protected Resource Metadata (RFC 9728) — MUST.** Serve
   `/.well-known/oauth-protected-resource` (and/or a `WWW-Authenticate:
   Bearer resource_metadata="…"` header on 401) whose `authorization_servers`
   points at cainban's authorization server. Include a `scope` hint.
2. **Authorization Server Metadata discovery — MUST (via the auth server).**
   The auth server must expose RFC 8414 OAuth AS metadata **or** OIDC discovery
   (`/.well-known/openid-configuration`), and emit the `iss` authorization-
   response parameter (RFC 9207). Cognito serves OIDC discovery already.
3. **Token audience validation — MUST.** Accept only tokens issued *for cainban*
   — validate the `aud`/resource per **Resource Indicators (RFC 8707)**. cainban
   already validates iss/aud/exp signature-first; this tightens it to the
   resource-indicator model (the client sends `resource=<canonical MCP URL>`).
4. **401 / 403 / 400** with the right `WWW-Authenticate` challenges, including
   `insufficient_scope` on 403 for step-up (ties into the read/write-scope work
   in the authorization-hardening plan).

The client (Claude/Cursor/VS Code/etc.) does PKCE, the authorization-code flow,
token storage and refresh — **none of which cainban implements**.

## 3. The blocker: Cognito does NOT support Dynamic Client Registration

This is the load-bearing finding, verified across AWS re:Post, the FastMCP
Cognito integration, and multiple MCP-OAuth writeups:

- **Amazon Cognito user pools do not support DCR (RFC 7591).** There is no
  `registration_endpoint`. Cognito is an OIDC IdP, not an MCP-compliant
  authorization server.
- Historically many MCP clients *required* DCR (no pre-existing client
  relationship → they auto-register). Against Cognito that fails at the
  handshake ("no registration endpoint").

### What the spec now prefers (2026-07-28)
The spec **DEPRECATES DCR** (12-month removal window) and makes **Client ID
Metadata Documents (CIMD)** the forward path. Client registration priority:
1. **Pre-registration** — a hardcoded/configured client id (+ maybe secret).
2. **Client ID Metadata Documents (CIMD)** — the client's `client_id` is an
   HTTPS URL pointing at a JSON metadata doc; no registration call at all. This
   is now the *preferred* "no prior relationship" mechanism — but the
   **authorization server** must support fetching+honoring CIMD, which **Cognito
   does not**.
3. **DCR** — **deprecated**, kept only for auth servers that don't do CIMD.
3. **DCR** — fallback only.

So neither CIMD nor DCR works with raw Cognito. That leaves **pre-registration**
(works, but every MCP client must be told the client id out of band — not
"set and forget" for arbitrary clients) or **a bridge**.

## 4. Decision: the OAuth Proxy bridge in front of Cognito

The proven pattern (this is exactly what FastMCP's `AWSCognitoProvider` /
"OAuth Proxy" does) is a **thin authorization-server façade** cainban owns, that:

- exposes the MCP-required discovery docs (RFC 9728 protected-resource metadata
  + RFC 8414 AS metadata) advertising **`client_id_metadata_document_supported:
  true`** (the non-deprecated CIMD path) and, only as a deprecated fallback, a
  `registration_endpoint` (DCR) — so MCP clients think they are talking to a
  fully MCP-compliant auth server;
- accepts CIMD (preferred) or DCR (deprecated) from the client and maps every
  dynamic client onto **one pre-registered Cognito app client** (the confidential
  client cainban owns), holding the Cognito client_secret server-side;
- proxies the authorize + token calls through to Cognito's Hosted UI / token
  endpoint (which does the actual Entra-federated login + token issuance),
  emitting the `iss` authorization-response parameter (RFC 9207);
- issues/relays tokens whose `aud` is cainban's canonical MCP URL (resource
  indicator), which the MCP Lambda then validates.

This is additive: the existing Cognito pool, Entra federation, pre-token
`repos`-claim injection, and the in-Lambda validator all stay. The proxy is a
new small component (a Lambda + the well-known routes on the API Gateway).

### Confused-deputy caution (from the spec's security section)
A proxy fronting a static Cognito client with dynamically-registered downstream
clients is exactly the **confused-deputy** shape the spec warns about. The proxy
**MUST obtain user consent per dynamic client** before forwarding to Cognito,
and **MUST** validate redirect URIs strictly (exact match; warn on localhost).
This is the main security-design work of the phase, not the plumbing.

## 5. Scope / effort

- **Not trivial, not huge.** ~a new "MCP auth proxy" Lambda + `/.well-known/*`
  routes + careful consent/redirect handling + resource-indicator (`aud`)
  alignment in the validator. No change to the tenancy/authorization core.
- **Frugal:** one more arm64 Lambda on the existing API Gateway, on-demand, a
  small token/consent state store (DynamoDB or short-lived). No always-on cost.
- **Depends on client behavior:** confirm the TARGET MCP clients (Claude
  Desktop? Cursor? VS Code? a custom agent?) and which registration mechanism
  each uses — that determines whether the proxy must implement DCR, CIMD, or
  both. Pre-registration alone may suffice for a single known client and is far
  cheaper; the proxy is for "any MCP client, set and forget."

## 6. Alternatives considered (and why not, for now)

- **Pre-registration only** (hardcode a Cognito client id in each MCP client):
  works today with just the RFC 9728 metadata doc + no proxy, but the user must
  paste a client id and it isn't seamless for arbitrary clients. Good *interim*
  step: ship the protected-resource metadata + a documented pre-registered
  client first; add the proxy only if clients need DCR/CIMD.
- **Client-credentials machine principal** (Phase 5 agent-identity item): the
  right answer for UNATTENDED/CI agents, orthogonal to this human-in-the-loop
  OAuth flow. Both can coexist.
- **A different auth server that DOES support DCR/CIMD** (Keycloak, Auth0,
  WorkOS/AuthKit, Descope): removes the proxy entirely and is genuinely MCP-native,
  at the cost of introducing a second IdP alongside Cognito+Entra. Worth
  weighing if the proxy's consent/security burden proves heavy — but it
  fragments the identity story we deliberately centralized on Cognito.

## 7. Recommended sequencing

1. **P5-oauth-a (cheap, high value):** add RFC 9728 **protected-resource
   metadata** + the `WWW-Authenticate` 401 challenge + resource-indicator `aud`
   validation, and document a **pre-registered** Cognito client for MCP clients.
   This makes cainban a spec-compliant resource server and works with any client
   that supports pre-registration — no proxy yet.
2. **P5-oauth-b (the "any client, set and forget" piece):** the **OAuth Proxy**
   façade (DCR + CIMD support mapped onto the pre-registered Cognito client),
   with per-dynamic-client consent and strict redirect validation.
3. Revisit vs. a DCR-native IdP only if (2)'s security/consent burden is higher
   than maintaining the proxy.

## 8. Invariants to preserve
- The in-Lambda **signature-first** validation and the `repos`-claim/tenant
  isolation stay unchanged — the proxy changes how a *client obtains* a token,
  not how cainban *authorizes* a request.
- **Audience binding is mandatory** — never accept a token not issued for
  cainban's canonical MCP URL (RFC 8707); never pass a client's token through to
  an upstream API.
- Short-lived access tokens + rotated refresh tokens (public clients).
