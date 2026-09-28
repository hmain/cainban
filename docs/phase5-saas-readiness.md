# cainban Phase 5 — SaaS-readiness design

> Status: **design/plan** (not a build). Captures how cainban goes from
> "used inside the Awiant GitHub org" to "a multi-tenant SaaS" **without
> throwing away anything built in Phases 1–4**. Each section is an *additive*
> workstream; nothing here requires re-architecting the core.

## 0. The premise: the core is already multi-tenant

Phases 1–4 made the hard multi-tenant decisions correctly, so SaaS is mostly an
**identity + onboarding + operations** story rather than a rewrite:

- **Data isolation is structural.** Every item is keyed
  `REPO#<owner>/<repo>#…`; a request authorized for repo A cannot address repo
  B's partition. True for one org or ten thousand.
- **Authorization is earned, not asserted.** A grant is written only after a
  server-side GitHub `VerifyRepoAccess`; the validated `repos` claim is the sole
  source of truth. Tenant-agnostic by construction.
- **Cost scales per use.** On-demand DynamoDB + per-request arm64 Lambdas +
  reserved-concurrency caps = no idle floor, bounded burst.

So the SaaS gaps are the layers *above* the repo: **who logs in**, **how a
customer onboards**, **billing**, **per-tenant limits**, and **operations**.

## 1. Identity — the one decision that either boxes you in or scales

### The rule
Keep **Cognito as the token broker** (the validator, the API Gateway JWT
authorizer, and the pre-token `repos`-claim injection are all built on it — do
not disturb them). Treat **identity providers as configuration**, not as the
foundation. This is the standard B2B-SaaS shape: one broker, many federated
IdPs.

### Progression (each step additive, none thrown away)
1. **Now (Awiant):** federate **Awiant's Entra ID** into Cognito as an OIDC
   provider (native — no shim). Awiant developers log in with corporate SSO;
   Cognito still issues the JWT the backend validates. *(This is the Phase-5a
   build: `feat/entra-federation-connect-page`.)*
2. **Enterprise SaaS customers:** each customer that wants their own SSO is
   **another OIDC provider added to the same pool** (their Entra / Okta / Google
   Workspace) — config, not code. The IdP list in `infra/stack.go` is
   deliberately an iterated slice so this is a one-entry addition.
3. **Individual (non-enterprise) SaaS users:** Cognito Hosted UI with a native
   social IdP (Google/Apple) or email — for developers with no corporate tenant.
4. **(Optional) GitHub as a login IdP:** natural for a GitHub-centric tool, but
   GitHub OAuth is **not OIDC**, so it needs an OpenID-wrapper shim (an extra
   maintained component in the auth path). Deferred unless a concrete need
   appears — the connect flow already uses GitHub for *repo authorization*, so
   GitHub-as-login is a UX nicety, not a requirement.

### Identity vs authorization (keep these separate — they are different axes)
- **Identity** ("who is this human") → the federated IdP (Entra now).
- **Authorization** ("which repos may they touch") → the GitHub App connect flow
  + `VerifyRepoAccess`, unchanged regardless of the login IdP.

### What NOT to do
Do **not** make any single customer's IdP (e.g. Awiant's Entra tenant) *the*
identity foundation. It doesn't generalize to other customers. Wire it as the
*first* federated provider, behind the pluggable pattern.

## 2. Tenant / account model (above repos)

Today a "tenant" is effectively a repo partition. SaaS needs an **account/org**
entity as the billing + membership subject — a *light layer above* the existing
`REPO#` keys, not a replacement.

- **Natural boundary = the GitHub App installation.** A customer installing the
  cainban GitHub App on their org *is* an account. The installation id maps to
  an account; the repos it covers map to that account's boards.
- **Proposed key additions** (same single-table style):
  - `ACCOUNT#<id>` → account metadata (plan, status, GitHub installation id).
  - `ACCOUNT#<id>` / `MEMBER#<sub>` → membership (which Cognito subjects belong).
  - Existing `REPO#<owner>/<repo>#` boards gain an owning-account attribute for
    billing/rollup (the isolation key itself is unchanged).
- Grants stay keyed by subject (`USER#<sub>`); the account layer is for
  billing/admin/rollup, not for the per-request authorization path (which stays
  exactly as-is — do not add account lookups to the hot path).

## 3. Self-serve onboarding

Generalize the Phase-5a connect page into a full onboarding surface:
1. **Sign in** (federated IdP).
2. **Install the GitHub App** on your org (GitHub's install flow) → cainban
   learns the installation via the **install webhook** (below).
3. **Repos appear**; the user connects/grants the ones they want (existing
   `/connect/*` flow, verified).
4. **Copy the MCP endpoint + config** to wire an agent.

### GitHub App install webhook (the deferred "Phase 5 sync" becomes load-bearing)
- Add a webhook endpoint (its own Lambda route) subscribed to
  `installation` / `installation_repositories` events.
- On install: create/activate the account, record the installation id + covered
  repos. On uninstall: deactivate the account, revoke grants (fail-closed — a
  removed installation must stop authorizing).
- Webhook signature verification (the App's webhook secret, in Secrets Manager)
  — treat the payload as untrusted until the HMAC is checked.
- This is also the hook for future **issue/PR ↔ task sync** (a larger,
  separate feature — keep it out of the onboarding MVP).

## 4. Billing

Two viable models; recommend evaluating **GitHub Marketplace billing first**
given cainban is already a GitHub App:

- **GitHub Marketplace billing** — GitHub handles payment, subscriptions, and
  plan entitlement (Marketplace `purchase` webhooks), analogous to the
  "buy via Fortnox" pattern: it removes the Stripe/metering + PCI workstream and
  meets customers where they already are (GitHub). Constraint: GitHub's revenue
  share + Marketplace listing review (external lead time — start early).
- **Stripe** — full control over plans/metering/trials, works outside GitHub,
  but you own billing, dunning, and PCI-scope-minimization (keep card data off
  your Lambdas — offload to Stripe Checkout).

Metering axis options: per-account, per-connected-repo, or per-seat
(Cognito subjects in the account). Per-repo is the most natural given the
board-per-repo model.

## 5. Per-tenant limits & abuse control

Structural data isolation exists; SaaS adds **fairness + abuse limits**:
- **Reserved concurrency is currently GLOBAL** (5/10/5). For SaaS, one noisy
  account must not exhaust the shared pool — revisit toward per-tenant fairness
  (e.g. usage-plan throttling on API Gateway keyed by account, or a token-bucket
  in the resolver keyed by account/sub).
- **Rate limits per account** (API Gateway usage plans, or in-Lambda).
- **Quotas** (max repos/tasks per plan tier), enforced at the account layer.
- **Abuse signals**: alarm on anomalous create rates; the fail-closed grant
  model already prevents cross-tenant access.

## 6. Operations & compliance (documentation + small technical gaps, not architecture)

Single-region EU deployment keeps this tractable:
- **GDPR (unconditional):** data-export + erasure per subject/account (the
  subject/account keys make this a bounded query+delete), a DPA template,
  SSE-KMS on the tables (confirm; DynamoDB is encrypted at rest by default —
  consider a CMK for a paying-customer story).
- **Per-tenant observability:** structured logs tagged with account id; per-
  account dashboards/alarms.
- **Status page + incident process** once there are external customers.
- **Backups/retention:** tables are PITR + RETAIN today — good; document RPO/RTO.
- Confirm NIS2/DORA are **out of scope** for a small accounting-adjacent dev
  SaaS before building any of that machinery (they likely do not apply; confirm
  with counsel rather than pre-building).

## 7. Sequencing (nothing built now is thrown away)

| Step | Workstream | Depends on | Status |
| --- | --- | --- | --- |
| 5a | Entra OIDC federation + connect page (pluggable IdP pattern) | Phase 4 | **in progress** (`feat/entra-federation-connect-page`) |
| 5b | GitHub App **install webhook** + account/org model | 5a | planned |
| 5c | Self-serve onboarding page (install → connect → agent config) | 5b | planned |
| 5d | Billing (GitHub Marketplace preferred) | 5b | planned |
| 5e | Per-tenant limits/quotas (revisit global reserved concurrency) | 5b | planned |
| 5f | Ops/compliance hardening (GDPR export/erasure, per-tenant observability) | 5c | planned |
| 5g | (optional) issue/PR ↔ task sync via the webhook | 5b | deferred |
| 5h | MCP-native OAuth: (a) resource-server metadata + pre-registered client, then (b) OAuth-proxy façade — see `docs/phase5-mcp-oauth.md` | Phase 4 | (a) **in progress** |
| 5i | go-sdk upgrade to MCP spec **2026-07-28**: fully-stateless transport (remove `Mcp-Session-Id` + `initialize` handshake), `server/discover` RPC, `Mcp-Method`/`Mcp-Name` headers, `CacheableResult` (`ttlMs`/`cacheScope`), deterministic `tools/list` order. Gated on the go-sdk publishing 2026-07-28 support; cainban is already stateless-by-design so this is a clean adopt, not a redesign | go-sdk release | planned |

## 8. Explicitly out of scope for Phase 5 (record so it isn't silently assumed)
- GitLab / non-GitHub forges (Phase 4 is github.com-only by design).
- A dedicated machine/agent principal distinct from a human token (old P4.4;
  agents use MCP with a granted token today — revisit only if a separate audit
  identity is required).
- NIS2/DORA machinery (confirm scope before building anything).
- Multi-region / active-active (single EU region is the frugal default).

---

### Design invariants to preserve through all of Phase 5
1. **Cognito stays the broker; IdPs are config.** Never anchor on one customer's IdP.
2. **Authorization stays earned** (server-side GitHub verify → validated claim). No account-layer shortcut into the per-request auth path.
3. **Isolation stays structural** (`REPO#` partition), never a filter.
4. **Frugality** (on-demand, arm64, bounded concurrency) — add per-tenant fairness without adding an idle cost floor.
