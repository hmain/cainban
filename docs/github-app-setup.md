# cainban — GitHub App setup (operator checklist)

cainban's Phase 4 connect/verify flow needs a GitHub App of its own. This guide
walks you through registering that App and loading its credentials into AWS
Secrets Manager. You do this once, by hand, out of band from the code. At
runtime the code (`src/systems/github`, `src/systems/secrets`) only reads the
finished secret and uses it to call the GitHub API. No credential from this
guide ever goes into the repo, the code, or the CDK template.

> Phase 4 target: **github.com** only. Enterprise/GitLab are out of scope.

---

## Why a GitHub App, and not an OAuth App

cainban checks a user's access to `owner/repo` on the server before it writes a
grant. A GitHub App is what makes that check possible. It gives you
fine-grained, per-repo permissions, an org admin can install it on an
organization, and it authenticates as itself with an App JWT to discover which
repos it covers. That coverage check is the whole point, and an OAuth App can't
do it.

---

## 1. Register the App

GitHub → **Settings → Developer settings → GitHub Apps → New GitHub App**
(register under the **organization** that will own it, or a user account for
testing).

| Field | Value |
| --- | --- |
| **GitHub App name** | e.g. `cainban-connect` (must be globally unique) |
| **Homepage URL** | your cainban homepage or repo URL |
| **Callback URL** | the P4.3 connect callback: `<ConnectApiUrl>connect/github/callback`. Read `<ConnectApiUrl>` from the stack output after deploy (see below). Register a placeholder now and update it once the stack is deployed. |
| **Expire user authorization tokens** | ✅ enabled (short-lived user tokens) |
| **Request user authorization (OAuth) during installation** | ✅ enabled (needed for the P4.3 OAuth leg that identifies the connecting user) |
| **Webhook** | **☐ OFF** — uncheck **Active**. Phase 4 uses no webhook (issue/PR sync comes in a later phase). Leave the webhook URL/secret blank. |
| **Setup URL** | optional; leave blank for now |

## 2. Permissions (least privilege)

Give the App only what it needs. Set **Repository permissions**:

| Permission | Access | Why |
| --- | --- | --- |
| **Metadata** | **Read-only** | mandatory; enables `GET /repos/{owner}/{repo}/installation` (coverage check) |
| **Administration** | **Read-only** | enables `GET /repos/{owner}/{repo}/collaborators/{login}/permission` (collaborator entitlement) |

Set **Organization permissions**:

| Permission | Access | Why |
| --- | --- | --- |
| **Members** | **Read-only** | enables `GET /orgs/{org}/members/{login}` (org-membership entitlement) |

Leave every other permission at **No access**, and subscribe to no webhook
events.

## 3. Installability

- **Where can this GitHub App be installed?** → **Any account**, so org admins
  of customer orgs can install it. Pick **Only on this account** instead if you
  are scoping to a single org for now.
- An org admin installs the App on an organization and chooses **all repos** or
  **selected repos**. cainban's verification returns `true` only for a repo the
  installation actually covers.

## 4. Generate credentials

Once the App exists, grab four things from its settings page:

1. **App ID** — note the numeric **App ID** (top of the page).
2. **Client ID** — note the **Client ID** (`Iv1.…` / `Iv23…`).
3. **Client secret** — **Generate a new client secret** and copy it now; GitHub
   shows it once. The P4.3 OAuth leg uses it.
4. **Private key** — **Generate a private key**. A `.pem` (PKCS#1, header
   `-----BEGIN RSA PRIVATE KEY-----`) downloads. This is the App's signing key
   for the App JWT. Keep it only in Secrets Manager (step 6). Never commit it,
   and never paste it into code or CDK.

## 5. Install the App

Install the App on the target **organization** (or user) and select the repos it
should cover. Verification passes only for covered repos.

## 5a. Finalize the App Callback URL (P4.3)

The connect flow's OAuth callback is served by the **connect API Gateway HTTP
API**. After `cdk deploy`, read the URL from the stack's **`ConnectApiUrl`**
output and set the GitHub App's **Callback URL** to that URL **plus**
`connect/github/callback`:

```sh
export AWS_PROFILE=aws-test-hamin AWS_REGION=eu-north-1

# The connect endpoint base (note the trailing slash the API GW URL includes):
CONNECT_URL=$(aws cloudformation describe-stacks \
  --stack-name CainbanPhase2Stack \
  --query "Stacks[0].Outputs[?OutputKey=='ConnectApiUrl'].OutputValue" \
  --output text)

# The value to paste into the GitHub App's "Callback URL" field:
echo "${CONNECT_URL}connect/github/callback"
```

Set that exact value as the App's **Callback URL** (GitHub → your App →
**General** → *Identifying and authorizing users* → **Callback URL**). It has to
match the `redirect_uri` the connect Lambda sends. The stack passes the connect
Lambda a `CAINBAN_CONNECT_REDIRECT_URI` only if you set that env var; leave it
unset to rely on the App's registered default callback.

> **Transport:** an **API Gateway HTTP API with a managed Cognito JWT
> authorizer** fronts the `/connect/*` routes. Every route needs
> `Authorization: Bearer <Cognito JWT>` (no SigV4). The **callback route
> (`/connect/github/callback`) is deliberately exempt from the authorizer**: it
> is a GitHub browser redirect that carries no JWT, so it authenticates from the
> HMAC-signed, sub-bound `state` parameter instead (minted by
> `/connect/github/start`, which itself required a valid JWT). The browser hop to
> `/connect/github/start` needs a Cognito token; the redirect back to the
> callback works as a plain browser navigation.

## 6. Load the credentials into AWS Secrets Manager

The CDK stack creates a **placeholder** secret named **`cainban/github-app`** (an
empty JSON envelope, `RETAIN` on delete) and grants the cainban Lambda
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
- `private_key` is the **full PEM**, with newlines escaped as `\n` inside the
  JSON string. Or use the file-based command below, which handles the newlines
  for you.
- `client_id` / `client_secret` do double duty: the P4.3 OAuth leg uses them for
  the code↔login exchange, and the connect API derives its anti-CSRF
  state-signing key from them. The connect flow (`/connect/*`) now **requires**
  both — the connect Lambda fails at cold start if either is empty. `app_id` +
  `private_key` stay required for `VerifyRepoAccess`.

Put the value in with the AWS CLI. In a shared environment, never echo the key
into shell history; prefer the file form. The PEM you downloaded is
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
> env var (the CDK sets it from the stack's `GitHubAppSecretName` output). Rename
> the secret and you have to update that env var too.

## 7. Verify (no secret value leaves AWS)

- Confirm the secret exists and is filled: `aws secretsmanager describe-secret
  --secret-id cainban/github-app`. This prints metadata only, not the value.
- P4.3's connect flow exercises `VerifyRepoAccess` end to end. In P4.2 the
  package is unit-tested entirely against a mocked GitHub API; no live GitHub
  call happens in code or CI.

---

## Rotation & revocation

- **Rotate the private key**: generate a new key on the App page, update the
  `private_key` field in `cainban/github-app` (`put-secret-value`), then delete
  the old key on GitHub. The loader reads the current secret on each cold start.
- **Rotate the client secret**: same pattern, for `client_secret`.
- **Revoke access to a repo**: uninstall the App from the org/repo, or narrow
  the installed repos. `VerifyRepoAccess` then returns `false` for that repo, and
  the P4.3 connect flow refuses to (re)grant it.

## Security notes

- The private key and client secret live **only** in Secrets Manager. They are
  not in the repository, the code, or the CDK template; the CDK creates only an
  empty placeholder.
- IAM is least-privilege: the Lambda holds `GetSecretValue` (+ `DescribeSecret`)
  on the `cainban/github-app` secret ARN **alone**.
- The App JWT is short-lived (well under GitHub's 10-minute ceiling) and minted
  per call; installation tokens are short-lived by GitHub design.
- cainban **never** trusts a client-supplied claim of access. Every
  authorization decision is a server-side GitHub API result.
