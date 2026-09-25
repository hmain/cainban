# cainban Phase 4 — GitHub App "Connect repo" with access verification

Status: SPEC (draft) · Owner: default (Kiro) · 2026-09-25 · GitHub-only for now

## Why this phase exists

Phases 1–3 shipped a stateless, serverless, multi-tenant MCP server whose tenancy
key is `owner/repo`. But today a repo grant is **asserted, not verified**: an
operator manually sets a user's Cognito `custom:repos` attribute
(`aws cognito-idp admin-update-user-attributes`) and the pre-token trigger copies
it into the validated `repos` claim. Nothing confirms the user actually has access
to that GitHub repo, and there is no product-level "connect" action.

Phase 4 replaces the manual admin step with a **self-serve, verified GitHub
connect flow**: a user connects their GitHub account, cainban confirms via the
GitHub API that they can access `owner/repo`, and only then grants the `repos`
claim. This is the honest completion of the multi-user story — authorization
becomes *earned*, not typed.

Scope: GitHub.com only (no GHE/GitLab yet). Verification + grant only — NOT
issue/PR sync (that is a separate, larger Phase 5 option).

## What "connect a repo" means after Phase 4

1. User signs in (existing Cognito identity).
2. User clicks **Connect GitHub** → GitHub App installation / OAuth authorize.
3. cainban stores the GitHub identity/installation for that Cognito user.
4. User picks a repo they can access (or types `owner/repo`); cainban **verifies
   access via the GitHub API** (collaborator / installation-covers-repo).
5. On success, cainban **adds `owner/repo` to the user's grant set**, which flows
   into the next token's validated `repos` claim (unchanged Phase 3 path).
6. The board for that repo already exists implicitly (first write to the
   `REPO#<owner>/<repo>#` partition) — no separate provisioning needed, though we
   add an explicit idempotent "ensure board META" on connect for a clean UX.

The MCP request path (`Resolver` → `repos` claim → `REPO#owner/repo#` prefix) is
**unchanged**. Phase 4 only changes *how the grant set is populated* — from manual
attribute to verified connect.

## Design decisions to make (call these out to the user)

### D1. GitHub App vs OAuth App
- **GitHub App (recommended).** Fine-grained, per-repo installation permissions,
  short-lived installation tokens, higher rate limits, and it is the modern path.
  Verification = "does this installation cover `owner/repo` and is the user a
  member?" Also the prerequisite if Phase 5 ever adds issue/PR sync (webhooks).
- OAuth App: simpler, user-token calls `GET /repos/{owner}/{repo}` /
  `GET /user/repos`; access = whatever the user token can see. Less granular, no
  webhooks. Adequate for verification-only but a dead end for sync.
- **Recommendation: GitHub App**, verification-only in Phase 4, webhooks deferred.

### D2. Where the grant set lives (this changes Phase 3's assumption)
Phase 3 stored grants in Cognito `custom:repos` (a single mutable string). That is
fine for a handful of admin-set repos but is the wrong home for self-serve,
verified, potentially-many grants:
- **Recommended: a grants table** (DynamoDB) — `PK=USER#<cognitoSub>`,
  `SK=GRANT#<owner>/<repo>`, plus a `GITHUB#<cognitoSub>` item holding the linked
  GitHub identity/installation id. The **pre-token trigger reads this table**
  instead of (or in addition to) `custom:repos` to build the `repos` claim.
- This is the frugal-but-correct evolution flagged as "future option" in Phase 3.
  It keeps grants auditable, multi-valued, and revocable per repo.
- Migration: keep reading `custom:repos` as a fallback so nothing breaks; new
  grants go to the table.

### D3. Where verification runs
- A small **connect API** (new Lambda + Function URL/APIGW route, Cognito-auth'd
  like the MCP endpoint) handling: `GET /connect/github/start` (OAuth/install
  redirect), `GET /connect/github/callback` (exchange code, store identity),
  `POST /connect/repo {owner,repo}` (verify via GitHub API, write grant),
  `DELETE /connect/repo {owner,repo}` (revoke), `GET /connect/repos` (list
  connected). Verification calls the GitHub API server-side with the user's/App
  token — never trusts a client-supplied "I have access".

### D4. Secrets
- GitHub App private key + client secret in **AWS Secrets Manager**; the connect
  Lambda gets least-priv `secretsmanager:GetSecretValue` on just those secrets.
  Never in env/CDK source.

## Security requirements (non-negotiable, consistent with Phase 3)

- The connect API is itself behind the **same Cognito JWT validation** (signature
  → issuer/audience/expiry → claims) before any GitHub call or grant write.
- The grant is written for the **validated Cognito subject**, keyed to the GitHub
  identity that subject actually authorized — a user cannot grant themselves a
  repo they cannot prove access to, and cannot write grants for another subject.
- GitHub access is verified **server-side against the GitHub API** at connect time
  (and SHOULD be re-checked periodically / on a webhook if Phase 5 lands) — a
  client claim of access is never sufficient.
- Revocation: removing a grant (or losing GitHub access) drops the repo from the
  next token's `repos` claim. Note tokens are valid until expiry — document the
  access-token TTL as the revocation lag, keep it short (e.g. 15–60 min).
- The `owner/repo` string is normalized (lowercase, single `/`) identically to the
  Phase 3 resolver, so a connected repo maps to exactly one partition.

## Work breakdown (each an independent PR, no deploy)

1. **Grants table + pre-token read** — add the DynamoDB grants table (CDK),
   migrate the pre-token trigger to read grants from it (fallback to
   `custom:repos`), tests for the claim assembly. (Backend/infra.)
2. **GitHub App plumbing** — App registration doc + Secrets Manager wiring (CDK),
   a `github` package: installation/user token minting, `VerifyRepoAccess(owner,
   repo, identity) (bool, error)` against the GitHub API, with a mockable client
   interface + unit tests (mock GitHub API, no live calls in CI).
3. **Connect API Lambda + routes** — the endpoints in D3, Cognito-auth'd, writing
   verified grants to the table; unit tests for: unauth→401, authed-but-no-GitHub
   →link required, verify-fail→403 (access not confirmed), verify-ok→grant
   written, revoke→grant removed, cannot-write-another-subject's-grant.
4. **Docs + (optional) minimal connect UI** — README connect flow, update the plan
   doc; a tiny static "Connect GitHub / pick repo" page is optional and can be a
   follow-up.

## Explicitly out of scope for Phase 4 (candidate Phase 5)
- GitHub Issues/PR ↔ cainban task two-way sync (webhooks, mapping, conflict rules).
- Org-level bulk connect / team → board mapping.
- Non-GitHub providers (GitLab, GHE).

## Verification bar (same as prior phases)
`CGO_ENABLED=0 go build ./...`, vet, gofmt, golangci-lint v2.14.0 (0 issues, all
modules), unit tests incl. GitHub API mocked + the security scenarios above,
`cdk synth` succeeds. No `cdk deploy`/`bootstrap`, no live GitHub calls in CI.
If a dep bumps the go directive, bump the CI matrix/pins to match.

## Open questions for the user
- Q1: GitHub **App** (recommended, webhook-ready) or OAuth App (simpler)?
- Q2: OK to introduce the **DynamoDB grants table** (supersedes `custom:repos` as
  the grant home, keeping it as fallback)?
- Q3: Is verification-only enough for Phase 4, with issue/PR **sync deferred** to a
  separate phase? (Recommended: yes.)
- Q4: Personal repos only, or must **org repos** work at launch (affects whether an
  org admin must install the App on the org)?
