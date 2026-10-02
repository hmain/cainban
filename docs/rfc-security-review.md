# RFC: Security Review & Remediation Plan — cainban

- **Status:** Draft (for discussion)
- **Date:** 2026-10-01
- **Scope:** Full-repo static security review (read-only). No code was modified.
- **Author:** Security audit (Kiro)
- **Reviewers:** TBD

---

## 1. Summary

cainban is a Go monorepo delivering a kanban board as (a) a local CLI/TUI, (b) three
AWS Lambda entry points behind API Gateway v2 HTTP APIs — the MCP server, the GitHub
"connect" API, and a Cognito pre-token-generation trigger — and (c) a React/Vite SPA
hosted on Amplify. Auth is Cognito (federated to Entra ID + a machine client + a CLI
client); authorization is a signature-validated `repos` claim intersected with a
client-supplied target repo; GitHub repo access is verified server-side; data lives in
two PAY_PER_REQUEST DynamoDB tables with structural per-repo partition isolation.

**Overall posture is strong and above the norm for a project this size.** The
load-bearing controls are correct:

- **Signature-first JWT validation** with `alg=none`/downgrade defeated, issuer/audience/exp
  checks, and no claim trusted before the signature verifies.
- **Fail-closed authorization** everywhere: the pre-token trigger emits no `repos` claim on a
  table error; an unscoped request cannot open a data store; a DynamoDB-error path never
  fabricates a grant.
- **Structural tenant isolation**: every DynamoDB key is built under a `REPO#<owner>/<repo>#`
  prefix, and `NormalizeRepo` blocks `#`, NUL and `..` so a crafted repo value cannot break out
  of its partition.
- **No injection reachable**: all DynamoDB expressions use placeholders; all SQLite queries are
  parameterized (`?`); the CLI shells out to nothing (`os/exec` is absent); board names are
  sanitized to `[a-zA-Z0-9_-]` before touching the filesystem.
- **Least-privilege IAM**: per-ARN, per-action policies with no service wildcards; reserved
  concurrency caps on all three Lambdas; bounded log retention; secrets only in Secrets Manager.
- **No XSS sink** in the SPA (no `dangerouslySetInnerHTML`; React auto-escaping), **no
  secret-shaped `VITE_` var**, and the bearer token is **not logged**.

No **critical** or confirmed **high-exploitability** vulnerability was found. The findings
below are a prioritized set of hardening gaps and configuration items to confirm. The two
genuinely worth acting on soon are the **wildcard CORS** on the bearer APIs (defense-in-depth)
and the **`AdministratorAccess-Amplify`** managed policy (least-privilege regression in an
otherwise minimal stack).

---

## 2. Methodology

Static read-only review of the auth layer, the three Lambda handlers, the MCP server and
tenancy, the GitHub connect/OAuth flow, the DynamoDB/grants/KMS/secrets data layer, the CDK
stack, CI/build config, and the SPA + CLI. Where a finding's root cause lived outside a
reviewer's file set (e.g. "is `SuccessRedirect` request-controlled?"), the dependency was
traced to ground truth before ranking. No dynamic testing, no live-credential testing, no
dependency-CVE scan was run against a live advisory DB (see §6, D1).

Severity uses CVSS-style qualitative bands weighing **impact** × **exploitability** in the
deployed context (bearer-token APIs, single small tenant today, JWT-gated data path).

---

## 3. Findings, ranked

### HIGH

#### H-1 — Wildcard CORS (`AllowOrigins: "*"`) on both bearer-token HTTP APIs
- **Location:** `infra/stack.go:597` (MCP API), `infra/stack.go:864` (Connect API)
- **Impact:** Any web origin may invoke the APIs from a browser. These are `Authorization:
  Bearer` APIs with **no** `Access-Control-Allow-Credentials`, so this is *not* the classic
  `*`+credentials cookie-theft bug. The real cost is a removed defense-in-depth boundary: a
  token obtained by a malicious page (e.g. via a token-exfil bug anywhere) can be replayed
  cross-origin with no origin constraint.
- **Exploitability:** Low–moderate — requires the attacker to already hold a valid token. The
  JWT authorizer still fully gates the data path.
- **Confidence:** High (wildcard confirmed); needs product decision on allowed origins.
- **Why High:** It is the broadest-reach, lowest-effort hardening win and contradicts the
  stack's otherwise-tight posture.

#### H-2 — Amplify service role uses the `AdministratorAccess-Amplify` managed policy
- **Location:** `infra/stack.go:~1009` (role `cainban-amplify-service-role`)
- **Impact:** A build-time compromise (poisoned dependency, malicious build step — the pipeline
  runs `npm ci && npm run build`) inherits an admin-tier managed policy, giving account-wide
  blast radius instead of build/deploy-scoped. This is the one wildcard-ish grant in a stack
  that is otherwise strictly per-ARN least-privilege.
- **Exploitability:** Moderate (needs build-chain compromise).
- **Confidence:** High on over-provisioning; needs a scoped-policy design.

### MEDIUM

#### M-1 — Third-party GitHub Actions pinned to mutable tags; stale Go in release
- **Location:** `.github/workflows/release.yml:18` (`setup-go@v4`, `go 1.21`), `:36`
  (`softprops/action-gh-release@v1`); `.github/workflows/test.yml:60` (`codecov-action@v3`)
- **Impact:** A compromised/retagged action runs in a workflow holding `contents: write` and
  publishing release binaries users download — a supply-chain path to tampered artifacts. Go
  1.21 in the release path may carry fixed stdlib CVEs (CI uses 1.26).
- **Exploitability:** Moderate (depends on upstream action compromise); high impact because it
  touches distributed binaries.
- **Mitigations already present:** no `pull_request_target`; forked-PR code runs without
  secrets; `test.yml` grants no elevated permissions.
- **Confidence:** High.

#### M-2 — OAuth `state` is signed + sub-bound + expiring, but not single-use (replayable in TTL)
- **Location:** `src/systems/connect/state.go:47` (`stateTTL = 10m`), `:140` (`VerifyAndExtractSub`)
- **Impact:** A captured `code`+`state` can be replayed within 10 minutes. Blunted hard by
  GitHub authorization codes being single-use and short-lived (a replayed callback with a spent
  code fails at token exchange). Residual risk is a race in the brief window before the real
  callback consumes the code.
- **Exploitability:** Low–medium (needs callback-URL interception + winning a race).
- **Confidence:** High on behavior. The HMAC compare is constant-time; the state key is derived
  (SHA-256) from the OAuth client secret — see M-5.

#### M-3 — GitHub API path segments interpolated without `url.PathEscape`
- **Location:** `src/systems/github/client.go:198` (`RepoInstallationID`), `:218` (`IsOrgMember`),
  `:244` (`CollaboratorPermission`)
- **Impact:** `owner`/`repo`/`login` are `fmt.Sprintf`'d into the API path. The **host is a
  fixed constant** (`https://api.github.com`), so there is no SSRF-to-internal. The connect POST
  path normalizes `owner/repo` via `NormalizeRepo` first (blocks `..`, `#`, NUL). The residual
  gap is `login` (from GitHub) and any future caller passing an un-normalized segment: a `../`
  or query char could re-target the path *within* api.github.com.
- **Exploitability:** Low (GitHub identifier charset + `NormalizeRepo` on the main path).
- **Confidence:** High on the missing escaping; defense-in-depth fix.

#### M-4 — ID token used as the API bearer, and minted into copy-paste MCP config
- **Location:** `web/src/connectApi.ts:11-24` (`getIdToken`), `web/src/App.tsx` `BearerTokenFallback`
- **Impact:** The Cognito **ID token** (a full identity assertion carrying `email` + validated
  `repos`) is used as the API credential and, in the "Generate token" fallback, written into a
  JSON config blob the user pastes into on-disk MCP client config. Anyone who reads that file
  holds the user's authority until expiry (~1h). Using an ID token (vs access token) as an API
  bearer is an OAuth anti-pattern that widens the blast radius of a leak.
- **Exploitability:** Medium — needs access to the user's clipboard/config file; token is
  short-lived; the path is opt-in and collapsed in the UI.
- **Confidence:** High (explicit in code and acceptable by the authorizer, whose audience list
  includes the SPA client). The token is **not** logged (verified).

#### M-5 — HMAC `state` key reused from the OAuth client secret (no key separation)
- **Location:** `cmd/cainban-connect/wire.go` (`stateKey = sha256("cainban-connect-state|" + ClientSecret)`)
- **Impact:** One secret serves two cryptographic purposes. The derivation is one-way (the state
  key cannot expose the client secret), and rotating the client secret invalidates in-flight
  states — both acceptable. The principle gap: a dedicated/HKDF-derived key would isolate blast
  radius if either use were exposed.
- **Exploitability:** Very low. Design hardening only.
- **Confidence:** High.

### LOW

#### L-1 — No runtime guard that `partitionPrefix` is non-empty in multi-tenant mode
- **Location:** `src/systems/dynamo/dynamo.go` (`boardPK`); wiring in the Lambda entrypoint
- **Impact:** An empty prefix collapses all tenants into one partition. Today the Lambda path
  always sets the prefix from the validated tenant (and the unscoped path fails closed in
  `resolveTaskSystem`), so this is latent, not live. A future wiring bug would be silent.
- **Confidence:** High the assertion is absent; recommend a belt-and-braces guard.

#### L-2 — Task **description** has no length cap; title/description not control-char filtered
- **Location:** `src/systems/task/task.go:818` (`ValidateTitle` caps title=255, no desc cap)
- **Impact:** A ~400 KB description (DynamoDB item ceiling) is storable → storage/cost bloat and
  oversized-item write failures. Control chars/ANSI in title/description are stored verbatim →
  possible terminal/log injection *on display* (not a datastore injection; depends on the
  renderer, which does not currently escape on output).
- **Confidence:** High on the missing cap; medium on display impact (needs output-layer review).

#### L-3 — Secrets Manager read per invocation (no cache)
- **Location:** `src/systems/secrets/secrets.go` (`Loader.Load`)
- **Impact:** Operational, not exposure (not caching avoids long-lived plaintext in memory —
  arguably safer). A hot path calling `Load` per request risks throttling/latency.
- **Confidence:** High. If caching is added later, bound plaintext lifetime and never log it.

#### L-4 — Release/Docker base images and runtime hardening
- **Location:** `Dockerfile:2,17` (`golang:1.23-alpine`, `FROM alpine:latest`, runs as root,
  `CGO_ENABLED=1`)
- **Impact:** `alpine:latest` floats (non-reproducible, can drift into new CVEs); container runs
  as root; CGO enlarges native surface. **Not the deployed artifact** — Lambdas are
  `CGO_ENABLED=0` provided.al2023 arm64 pure-Go via the Makefile — so this is the local/CLI
  build only.
- **Confidence:** High. Pin the digest, add a non-root `USER`.

#### L-5 — No Content-Security-Policy / frame-ancestors / referrer policy in served HTML
- **Location:** `web/index.html`
- **Impact:** Absent CSP, any future XSS runs unconstrained; absent frame-ancestors, the
  sign-in/connect UI is clickjackable. No XSS sink exists today (defense-in-depth).
- **Confidence:** Medium — headers *may* be set at the Amplify/CloudFront layer (not visible in
  repo); needs confirmation.

#### L-6 — Error strings surfaced raw to the SPA; token-endpoint error bodies snippet-logged
- **Location:** `web/src/App.tsx` (`setError(String(e))`), `src/systems/github/oauth.go`
  (`snippet(body)` on the token endpoint's non-`error` non-2xx branch)
- **Impact:** Low info-disclosure. SPA errors render escaped (no XSS) but may show internal
  URLs/detail to the signed-in user. Token-endpoint bodies are not expected to carry the token,
  but snippeting a token endpoint's response is marginally riskier than a read endpoint's.
- **Confidence:** Medium.

### INFORMATIONAL — items examined and found SOUND (no change needed)
- Signature-first JWT; `alg=none` and RS/HS confusion rejected (`src/systems/auth/jwt.go`).
- Fail-closed grants in the pre-token trigger on any table error (`cmd/cainban-pretoken/main.go`).
- Tenant partition break-out blocked by `NormalizeRepo` (`#`, NUL, `..`) (`src/systems/auth/auth.go`).
- DynamoDB expression injection not reachable (all placeholders) (`src/systems/dynamo/dynamo.go`).
- SQLite injection not reachable (all `?`-parameterized) (`src/systems/task/*.go`).
- CLI command injection not reachable (`os/exec` absent) (`cmd/cainban/main.go`).
- Path traversal via board name blocked by `sanitizeBoardName` (`src/systems/board/board.go`).
- Local `--http` MCP forced to loopback (`loopbackOnly` rewrites `0.0.0.0`/`*`/empty →
  `127.0.0.1`) (`src/systems/mcp/server.go`).
- Self-granting a repo prevented: access decided by OAuth-derived login + server-side GitHub
  verification, fail-closed (`src/systems/github/verify.go`, `src/systems/connect/handler.go`).
- KMS AAD binds refresh-token ciphertext to subject; no plaintext fallback (`src/systems/crypter/kms.go`).
- No unsafe deserialization: GitHub responses decoded into narrow structs over `LimitReader`
  (`src/systems/github/client.go`).
- Metadata documents (RFC 9728 / RFC 8414) reflect no request input (`src/systems/mcp/*_metadata.go`).
- IAM least-privilege, PAY_PER_REQUEST + PITR, reserved-concurrency caps, 1-month log retention,
  exact authorizer audience list (`infra/stack.go`).
- No XSS sink / no secret `VITE_` var / token not logged in the SPA (`web/src/*`).

---

## 4. Prioritized remediation

| Pri | ID | Action | Effort | Tradeoff / note |
|-----|------|--------|--------|-----------------|
| 1 | H-1 | Replace `AllowOrigins:["*"]` with the known SPA origin(s) + localhost dev on both HTTP APIs | S | Must enumerate every legit browser origin; machine/CLI clients are unaffected (not browsers). |
| 2 | H-2 | Replace `AdministratorAccess-Amplify` with a scoped policy (CloudWatch Logs for build + specific Amplify deploy actions) | M | Risk of under-scoping breaking a build; validate in a non-prod branch first. |
| 3 | M-1 | SHA-pin all third-party actions; align release Go to 1.26 | S | Dependabot/renovate can track pinned SHAs; small maintenance overhead. |
| 4 | M-3 | `url.PathEscape` every interpolated GitHub path segment | S | Pure defense-in-depth; no behavior change for valid inputs. |
| 5 | M-2 | Shorten `stateTTL` (e.g. 2–5 min) and/or add one-time-use nonce consumption | S/M | One-time-use needs a tiny TTL'd store (DynamoDB w/ TTL) — adds a dependency to a currently-stateless flow. |
| 6 | M-4 | Prefer the OAuth/PKCE path over the ID-token bearer; if the fallback stays, label it clearly, keep expiry short, document the risk | M | ID-token-as-bearer is accepted by the authorizer today; switching the API to the access token is the cleaner long-term fix. |
| 7 | M-5 | Derive the state HMAC key via HKDF from the client secret (or a dedicated secret) | S | Minor; preserves rotate-on-secret-change behavior. |
| 8 | L-1 | Assert non-empty `partitionPrefix` on the multi-tenant store path (fail closed) | S | Belt-and-braces; no runtime cost. |
| 9 | L-2 | Cap description length (e.g. 16 KB); reject/escape control chars; escape on display | S | Pick a cap that fits real use; coordinate with the display layer. |
| 10 | L-4 | Pin Docker base image by digest; add non-root `USER`; align Go | S | Local/CLI image only; low urgency. |
| 11 | L-5 | Add CSP + `frame-ancestors 'none'` + referrer policy (at the hosting/CDN layer) | S | Confirm no inline-script needs before a strict CSP; may need `nonce`s. |
| 12 | L-3 / L-6 | Add bounded secret cache if `Load` is hot; suppress token-endpoint body snippet; trim raw error strings in the SPA | S | Caching trades memory-residency for fewer calls — only if throttling is observed. |

*(Effort: S ≈ hours, M ≈ a day or two.)*

---

## 5. Proposed concrete changes (illustrative, not yet implemented)

- **CORS (H-1):** set `AllowOrigins` to `["https://main.d1x7br21qa4ohy.amplifyapp.com",
  "http://localhost:5173"]` (or the configured custom domain) on both `CorsPreflight` blocks;
  keep the current `AllowHeaders`/`AllowMethods`.
- **Amplify role (H-2):** author a custom `iam.PolicyDocument` granting only the Amplify
  build/deploy actions + the build's CloudWatch Logs group, and attach that instead of the
  managed admin policy.
- **Actions (M-1):** replace `@v1`/`@v4`/`@v3` with `@<full-sha>` and add a comment with the
  human tag; bump `go-version` to `1.26`.
- **Path escaping (M-3):** wrap each segment: `url.PathEscape(owner)`, `url.PathEscape(repo)`,
  `url.PathEscape(login)` before `fmt.Sprintf`.
- **State (M-2):** reduce `stateTTL`; optionally record consumed nonces in a DynamoDB table with
  a TTL attribute and reject a reused nonce in `VerifyAndExtractSub`.

---

## 6. Open items requiring confirmation / follow-up

- **D1 — Dependency CVE scan not run.** `go.mod`/`go.sum` and `web/package-lock.json` were not
  checked against a live advisory DB in this review. **Recommend** wiring `govulncheck` (Go) and
  `npm audit`/Dependabot (web) into CI as a gate. Dependabot already opens PRs for the web deps
  (observed), but there is no `govulncheck` step.
- **L-5 hosting headers:** confirm whether Amplify/CloudFront already sets CSP and security
  headers; if not, add them there.
- **M-4 authorizer audience:** confirm it is intended that the API authorizer accepts the SPA
  **ID token** audience (it does today, by design).
- **GitHub App registration:** confirm the App's registered callback allowlist is tight (bounds
  the `redirect_uri` echo in M-3-adjacent OAuth) and that `SuccessRedirect`
  (`CAINBAN_CONNECT_SUCCESS_URL`, operator-set via CDK context — **not** request-controlled, so
  not a live open redirect) points at a fixed same-origin URL.

---

## 7. Decision requested

Approve the priority order in §4 (or re-rank), and confirm the §6 open items so the HIGH items
(H-1, H-2) can be scheduled first. None of the proposed changes have been implemented — this RFC
is documentation only.
