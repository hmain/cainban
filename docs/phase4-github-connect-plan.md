# cainban Phase 4 plan — GitHub App connect + AI-agent repo access

Status: PLAN (locked decisions) · Owner: default (Kiro) · 2026-09-25 · GitHub.com only

## End goal (what everything serves)

**AI code agents use cainban per repo as their own task backend over MCP.** An
agent working on `owner/repo` connects to that repo's board and creates / reads /
updates / closes its own tasks — so planning lives in the board, not in a human's
head or a one-off prompt. Humans and agents share the same repo board (the Phase 3
"shared team kanban" model already supports this); Phase 4 makes access *verified*
and adds a *non-interactive agent credential* so an agent can authenticate without
a browser.

## Locked decisions

1. **GitHub App** (not OAuth App) — fine-grained per-repo perms, org installs,
   webhook-ready for a later sync phase.
2. **DynamoDB grants table** is the grant home (supersedes Cognito `custom:repos`,
   which stays as a read fallback for migration). Pre-token trigger reads it.
3. **Verification-only** this phase — issue/PR sync is deferred (Phase 5).
4. **Org repos required** — the App must be installable on an org by an org admin;
   verification must handle installation-covers-repo + user/agent membership.

## The agent uses MCP — it needs no new protocol

The AI agent's interface IS the existing MCP endpoint (Phases 1–3): it points an
MCP client at cainban and calls the same tools (create_task, list_tasks,
update_task_status, …). Nothing new is required for the agent to PARTICIPATE.

The only auth question is which bearer token the agent presents to the
JWT-validated endpoint. Two options, and the simple one is enough for the end
goal:

- **Reuse an existing token (DEFAULT, no backend work).** The agent acts on the
  human's behalf using the human's Cognito token / session in its MCP client auth
  header. The moment Phase 4's connect grants the repo, the agent using MCP Just
  Works. This satisfies "an agent handles tasks on my repo."
- **A dedicated machine principal (OPTIONAL, deferred — was P4.4).** Only worth it
  if the agent must be a DISTINCT principal from the human: its own audit trail,
  independently revocable, unattended with no human session. This is a client-
  credentials token authorized against the same `repos` claim path — a bolt-on,
  not a prerequisite. Do NOT build it speculatively; add it only when a concrete
  need for a separate agent identity appears.

So the minimum to reach the end goal is the human connect+verify flow
(P4.1–P4.3); agent-via-MCP then works with the granted token.

## The two actors (only if a distinct agent identity is later wanted)

Phase 3's auth is subject-based human tokens only. If (and only if) a distinct
agent principal is later required:

- **Human user** — interactive: signs in (Cognito), clicks *Connect GitHub*,
  authorizes the App, picks org/repos; verified access → grants written.
- **AI agent** — non-interactive: needs a credential to call the MCP endpoint with
  a repo scope, WITHOUT a browser OAuth dance. Two viable models (decide in P4.4):
  - **(a) Agent = the GitHub App installation.** The agent authenticates as the
    App installation for `owner/repo` (installation token / a cainban-minted
    machine JWT derived from a verified installation). Access is intrinsic: if the
    App is installed on the repo, the agent may act on that repo's board. Cleanest
    fit for "per repo" and for org installs.
  - **(b) Agent = a scoped service principal** (Cognito app client, client-
    credentials JWT) whose `repos` claim is provisioned from the grants table,
    exactly like a human. Reuses Phase 3 validation as-is (just an `azp`/client-id
    subject instead of a user `sub`); grant still comes from a verified human/org
    connect.
  - Recommendation: **(b) for the credential mechanics** (reuses the signature-
    first validator, no new token type) **driven by (a) for the grant source** (the
    App installation is what proves org/repo access). I.e. connecting the App to a
    repo provisions an agent-usable grant; the agent presents a client-credentials
    token and is authorized against the same `repos` claim path.

## Security requirements (carry Phase 3's guarantees)

- Every connect API + MCP call validated signature-first (JWKS → iss/aud/exp →
  claims) before any GitHub call, grant write, or store open.
- Grants are written only for the **validated subject** (human `sub` or agent
  `azp`/client-id), tied to a GitHub identity/installation that actually proved
  access. No self-granting an unverified repo; no writing another subject's grant.
- **Org verification**: confirm the App installation covers `owner/repo` AND the
  connecting principal is entitled (org member / repo collaborator) — server-side
  via the GitHub API, never a client claim.
- Agent credentials are **repo-scoped and revocable**: revoking the App install or
  the grant drops the repo from the next token; keep token TTL short (15–60 min) as
  the revocation lag. An agent token must not be able to widen its own scope.
- GitHub App private key + secrets in **Secrets Manager**, least-priv read.

## Work breakdown (independent PRs, no deploy, same verification bar)

- **P4.1 — Grants table + pre-token read.** ✅ **BUILT** (branch
  `feat/p4.1-grants-table`, not deployed). DynamoDB grants table
  (`PK=USER#<sub>` / `SK=GRANT#<owner>/<repo>`, an `IDENTITY#github` item for the
  linked GitHub install; agent principals keyed the same way by client-id).
  Migrate the pre-token trigger to read grants from the table (fallback
  `custom:repos`). Tests for claim assembly + fallback.
  - **Table: SEPARATE `cainban-grants` table** (not the single `cainban` table).
    Grants are keyed by SUBJECT and read on the token-minting path; a separate
    table lets the pre-token Lambda's IAM be scoped to the grants ARN alone
    (least privilege — it structurally cannot read task data), keeps
    PITR/backup boundaries clean, and shares no partition with board data. Cost
    is identical (on-demand, zero at rest). On-demand + PITR + RETAIN, matching
    the main table.
  - **Key design:** `PK=USER#<cognitoSub>`; `SK=GRANT#<owner>/<repo>`
    (presence = granted); `SK=META` holds the `default_repo` marker;
    `SK=IDENTITY#github` reserved for the linked GitHub identity/install id
    (minimal now; P4.2/P4.3 populate it). `owner/repo` is normalized via
    `auth.NormalizeRepo` (the same case-preserving canonicalization the resolver
    uses), so a grant maps to exactly one partition prefix.
  - **Pre-token resolution / fallback / fail-closed:** the trigger reads the
    subject's grants from the table (subject = the `sub` standard attribute,
    which equals the token's `sub` claim; falls back to `userName` only if
    absent). Table has grants → build the `repos` claim from the table (+ table
    `default_repo`). Table EMPTY → fall back to the `custom:repos` /
    `custom:default_repo` attribute path (unmigrated users keep working). Table
    ERROR → **FAIL CLOSED**: emit NO `repos` claim — never fall back to the
    attributes and never fabricate a grant (no claim ⇒ 403 downstream, the safe
    outcome). If `CAINBAN_GRANTS_TABLE` is unset the trigger degrades to the
    attribute-only path (never fails token issuance on missing config).
  - **IAM:** pre-token Lambda gets ONLY `dynamodb:GetItem` + `dynamodb:Query`
    on the grants table ARN — no writes, no access to the `cainban` data table.
  - **Grant package** `src/systems/grants`: `ListReposForSubject`, `Get`,
    `PutGrant`, `DeleteGrant`, `GetDefaultRepo`, `SetDefaultRepo`, behind a
    mockable DynamoDB `API` interface, unit-tested with an in-memory fake
    client. (Write methods are implemented now for the P4.3 connect API to use;
    P4.1 only exercises the read path.)
- **P4.2 — GitHub App package.** App registration doc; Secrets Manager wiring
  (CDK). `github` package: installation + user token minting, and
  `VerifyRepoAccess(owner, repo, principal) (bool, error)` handling **org
  installs** (installation-covers-repo + membership), behind a mockable client
  interface. Unit tests with a mocked GitHub API (no live calls in CI).
- **P4.3 — Connect API Lambda.** Cognito-auth'd routes: `/connect/github/start`,
  `/connect/github/callback`, `POST/DELETE /connect/repo`, `GET /connect/repos`.
  Verifies server-side, writes/revokes grants. Security-scenario tests
  (unauth→401, no-github-link→link-required, verify-fail→403, ok→grant,
  revoke→removed, cannot-write-another-subject).
- **P4.4 — (OPTIONAL, deferred) Dedicated agent principal.** ONLY if a distinct
  agent identity is later needed (separate audit/revocation/unattended). Implement
  a client-credentials machine token authorized against the same `repos` claim;
  extend the resolver to accept an agent subject (`azp`/client-id). Not built
  speculatively — the agent uses MCP with an existing granted token by default.
- **P4.5 — Agent usage docs + smoke.** Document, for an AI code agent, exactly how
  to point its MCP client at cainban for a repo: the endpoint, the bearer token it
  presents (the human's granted token by default), and the tool calls
  (create_task/list_tasks/update_task_status…) it uses to run its own plan. A
  worked example: "agent on `org/repo` creates a task, moves it doing→done."

## Out of scope (Phase 5 candidates)
- GitHub Issues/PR ↔ task two-way sync (webhooks).
- GitLab / GitHub Enterprise.
- Board UI. (Agents and MCP clients need none; humans can get one later.)

## Verification bar (unchanged)
`CGO_ENABLED=0 go build ./...`, vet, gofmt, golangci-lint v2.14.0 (0 issues, all
modules), unit tests incl. mocked GitHub API + the security scenarios,
`cdk synth`. No `cdk deploy`/`bootstrap`, no live GitHub calls in CI. Bump CI Go
pins if the go directive moves.

## Sequencing
P4.1 → P4.2 → P4.3 (human connect + verify works end to end) → P4.5 (agent-via-MCP
usage docs). At that point the END GOAL is met: a granted repo's board is usable by
an AI agent over MCP with the granted token. P4.4 (dedicated agent principal) is
optional and built only if a distinct agent identity is later required.
