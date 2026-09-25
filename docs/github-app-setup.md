# cainban — GitHub App setup (operator checklist)

This is the **operator** procedure to register the GitHub App that cainban's
Phase 4 connect/verify flow uses, and to load its credentials into AWS Secrets
Manager. It is a **human step done once**, out of band from the code — the code
(`src/systems/github`, `src/systems/secrets`) only *reads* the finished secret
at runtime and makes GitHub API calls with it. **No credential from this
checklist ever goes into the repo, code, or the CDK template.**

> Phase 4 target: **github.com** only. Enterprise/GitLab are out of scope.

---

## Why a GitHub App (not an OAuth App)

cainban verifies a user's access to `owner/repo` **server-side** before writing
a grant. A GitHub **App** gives fine-grained, per-repo permissions, is
installable on an **organization** by an org admin, and authenticates as itself
(App JWT) to discover which repos it covers — exactly what the org-repo
verification needs. An OAuth App cannot do the installation-coverage check.

---

## 1. Register the App

GitHub → **Settings → Developer settings → GitHub Apps → New GitHub App**
(register under the **organization** that will own it, or a user account for
testing).

| Field | Value |
| --- | --- |
| **GitHub App name** | e.g. `cainban-connect` (must be globally unique) |
| **Homepage URL** | your cainban homepage or repo URL |
| **Callback URL** | the P4.3 connect callback, e.g. `https://<function-url>/connect/github/callback` (can be a placeholder now; P4.3 finalizes it) |
| **Expire user authorization tokens** | ✅ enabled (short-lived user tokens) |
| **Request user authorization (OAuth) during installation** | ✅ enabled (needed for the P4.3 OAuth leg that identifies the connecting user) |
| **Webhook** | **☐ OFF** — uncheck **Active**. No webhook is used in Phase 4 (issue/PR sync is a later phase). Leave the webhook URL/secret blank. |
| **Setup URL** | optional; leave blank for now |

## 2. Permissions (least privilege)

Set **Repository permissions**:

| Permission | Access | Why |
| --- | --- | --- |
| **Metadata** | **Read-only** | mandatory; enables `GET /repos/{owner}/{repo}/installation` (coverage check) |
| **Administration** | **Read-only** | enables `GET /repos/{owner}/{repo}/collaborators/{login}/permission` (collaborator entitlement) |

Set **Organization permissions**:

| Permission | Access | Why |
| --- | --- | --- |
| **Members** | **Read-only** | enables `GET /orgs/{org}/members/{login}` (org-membership entitlement) |

Leave every other permission at **No access**. Subscribe to **no** webhook
events.

## 3. Installability

- **Where can this GitHub App be installed?** → **Any account** (so org admins
  of customer orgs can install it) — or **Only on this account** if you are
  scoping to a single org for now.
- The App must be **installable on an organization** and installed by an **org
  admin**, who chooses **all repos** or **selected repos**. cainban's
  verification only ever returns `true` for a repo the installation actually
  covers.

## 4. Generate credentials

After creating the App, on its settings page:

1. **App ID** — note the numeric **App ID** (top of the page).
2. **Client ID** — note the **Client ID** (`Iv1.…` / `Iv23…`).
3. **Client secret** — **Generate a new client secret**; copy it now (shown
   once). Used by the P4.3 OAuth leg.
4. **Private key** — **Generate a private key**; a `.pem` (PKCS#1, header
   `-----BEGIN RSA PRIVATE KEY-----`) downloads. This is the App's signing key
   for the App JWT. **Store it only in Secrets Manager** (step 6) — never commit
   it, never paste it into code or CDK.

## 5. Install the App

Install the App on the target **organization** (or user) and select the repos it
should cover. Verification will only pass for covered repos.

## 6. Load the credentials into AWS Secrets Manager

The CDK stack creates a **placeholder** secret named **`cainban/github-app`**
(empty JSON envelope, `RETAIN` on delete) and grants the cainban Lambda
least-privilege `secretsmanager:GetSecretValue` on **that secret only**. After
deploy, fill it with the JSON the loader (`src/systems/secrets`) expects:

```json
{
  "app_id": "123456",
  "client_id": "Iv1.abc123def456",
  "client_secret": "the-client-secret",
  "private_key": "-----BEGIN RSA PRIVATE KEY-----\n…\n-----END RSA PRIVATE KEY-----\n"
}
```

- `app_id` may be a JSON string or number.
- `private_key` is the **full PEM**, newlines escaped as `\n` inside the JSON
  string (or use the file-based command below, which handles newlines for you).
- `client_id` / `client_secret` are read but used only by the P4.3 OAuth leg —
  they may be left empty until P4.3, but the private key + app_id are required
  for `VerifyRepoAccess`.

Put the value in with the AWS CLI (never echo the key into shell history in a
shared environment; prefer the file form). The PEM you downloaded is
`cainban-connect.private-key.pem`:

```sh
export AWS_PROFILE=aws-test-hamin AWS_REGION=eu-north-1

# Build the JSON from the PEM file safely (jq keeps the newlines correct):
jq -n \
  --arg app_id "123456" \
  --arg client_id "Iv1.abc123def456" \
  --arg client_secret "the-client-secret" \
  --rawfile private_key ./cainban-connect.private-key.pem \
  '{app_id:$app_id, client_id:$client_id, client_secret:$client_secret, private_key:$private_key}' \
  > /tmp/cainban-github-app.json

aws secretsmanager put-secret-value \
  --secret-id cainban/github-app \
  --secret-string file:///tmp/cainban-github-app.json

rm -f /tmp/cainban-github-app.json   # do not leave the key on disk
```

> The secret **name** is what the Lambda reads via the `CAINBAN_GITHUB_APP_SECRET`
> env var (the CDK sets it from the stack's `GitHubAppSecretName` output). If you
> rename the secret, update that env var.

## 7. Verify (no secret value leaves AWS)

- Confirm the secret exists and is filled: `aws secretsmanager describe-secret
  --secret-id cainban/github-app` (metadata only — does **not** print the value).
- P4.3's connect flow exercises `VerifyRepoAccess` end to end. In P4.2 the
  package is unit-tested entirely against a mocked GitHub API; there is no live
  GitHub call in code or CI.

---

## Rotation & revocation

- **Rotate the private key**: generate a new key on the App page, update the
  `private_key` field in `cainban/github-app` (`put-secret-value`), then delete
  the old key on GitHub. The loader reads the current secret each cold start.
- **Rotate the client secret**: same pattern for `client_secret`.
- **Revoke access to a repo**: uninstall the App from the org/repo (or narrow
  the installed repos). `VerifyRepoAccess` then returns `false` for that repo,
  and the P4.3 connect flow will refuse to (re)grant it.

## Security notes

- The private key and client secret exist **only** in Secrets Manager. They are
  not in the repository, the code, or the CDK template — the CDK creates only an
  empty placeholder.
- IAM is least-privilege: the Lambda holds `GetSecretValue` (+ `DescribeSecret`)
  on the `cainban/github-app` secret ARN **alone**.
- The App JWT is short-lived (well under GitHub's 10-minute ceiling) and minted
  per call; installation tokens are short-lived by GitHub design.
- cainban **never** trusts a client-supplied claim of access — every
  authorization decision is a server-side GitHub API result.
