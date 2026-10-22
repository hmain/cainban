# cainban — GitHub App setup (operator checklist)

cainban uses a GitHub App to verify user permissions at runtime. When a user
makes a request, cainban uses the **App's own installation token** to check
whether that user has access to the requested repo. The user never hands cainban
a GitHub token — they only prove their identity. This guide walks you through
registering the App and loading its credentials into AWS Secrets Manager.

> **App-based authorization:** Authorization is decided server-side with the
> App installation token (not a user token), cached briefly. A user is
> authorized for `owner/repo` when the App is installed on that repo AND the
> user is an org member or a repo collaborator with read+ access. See
> `docs/rfc-github-app-authorization.md` for the model and rollout.

---

## Why a GitHub App

cainban checks a user's access to `owner/repo` on the server at request time. A
GitHub App authenticates as itself with an App JWT, mints a short-lived
installation token, and uses it to verify coverage (the App is installed on the
repo) and entitlement (the user is an org member or a repo collaborator). This
check happens on every request (with brief caching) rather than storing grants
in a database — and crucially, it needs no GitHub token from the user.

---

## 1. Register the App

GitHub → **Settings → Developer settings → GitHub Apps → New GitHub App**
(register under the **organization** that will own it, or a user account for
testing).

| Field | Value |
| --- | --- |
| **GitHub App name** | e.g. `cainban` (must be globally unique) |
| **Homepage URL** | your cainban homepage or repo URL |
| **Callback URL** | Not needed for runtime permission checks (leave blank or set a placeholder) |
| **Webhook** | **☐ OFF** — uncheck **Active**. cainban uses no webhook. |

## 2. Permissions (least privilege)

Give the App only what it needs. Set **Repository permissions**:

| Permission | Access | Why |
| --- | --- | --- |
| **Metadata** | **Read-only** | mandatory; enables basic repo access |
| **Administration** | **Read-only** | enables `GET /repos/{owner}/{repo}/collaborators/{login}/permission` (collaborator permission check) |

Leave every other permission at **No access**, and subscribe to no webhook events.

## 3. Installability

- **Where can this GitHub App be installed?** → **Any account**, so org admins
  of customer orgs can install it.
- An org admin installs the App on an organization and chooses **all repos** or
  **selected repos**. Permission checks only work for repos the installation covers.

## 4. Generate credentials

Once the App exists, grab these from its settings page:

1. **App ID** — note the numeric **App ID** (top of the page).
2. **Client ID** — note the **Client ID** (`Iv1.…` / `Iv23…`).
3. **Client secret** — **Generate a new client secret** and copy it now.
4. **Private key** — **Generate a private key**. A `.pem` downloads. Keep it
   only in Secrets Manager.

## 5. Install the App

Install the App on the target **organization** (or user) and select the repos it
should cover. Permission checks only work for covered repos.

## 6. Load the credentials into AWS Secrets Manager

The CDK stack creates a **placeholder** secret named **`cainban/github-app`**.
After deploy, fill it with the JSON the loader expects:

```json
{
  "app_id": "123456",
  "client_id": "Iv1.abc123def456",
  "client_secret": "the-client-secret",
  "private_key": "-----BEGIN RSA PRIVATE KEY-----\n…\n-----END RSA PRIVATE KEY-----\n"
}
```

Put the value in with the AWS CLI:

```sh
export AWS_PROFILE=aws-test-hamin AWS_REGION=eu-north-1

jq -n \
  --arg app_id "123456" \
  --arg client_id "Iv1.abc123def456" \
  --arg client_secret "the-client-secret" \
  --rawfile private_key ./cainban.private-key.pem \
  '{app_id:$app_id, client_id:$client_id, client_secret:$client_secret, private_key:$private_key}' \
  > /tmp/cainban-github-app.json

aws secretsmanager put-secret-value \
  --secret-id cainban/github-app \
  --secret-string file:///tmp/cainban-github-app.json

rm -f /tmp/cainban-github-app.json
```

## 7. Enable App-based authorization

> **Status:** the App authorization path is being wired in per
> `docs/rfc-github-app-authorization.md`. The legacy
> `CAINBAN_USE_GITHUB_PERMISSIONS=true` flag selected the **user-token**
> (`ghperm`) path, which this model replaces — do not rely on it for the App
> path. The App path reads its credentials from the `cainban/github-app` secret
> loaded above; once the resolver wiring (RFC Task C) ships, it is selected by
> the Lambda at cold start when that secret is present. Prefer setting mode in
> the CDK stack and redeploying with `make deploy` over an out-of-band
> `update-function-configuration`, so the live config matches infra-as-code.

---

## How it works at runtime

1. User authenticates via Cognito (GitHub federation); their token carries the
   non-secret `github_user` (login) claim — never a GitHub token.
2. When they request `owner/repo`, the MCP Lambda:
   - Reads the user's login from the validated token (identity only)
   - Mints an App JWT, looks up the repo's installation, and gets an
     installation token
   - Uses the installation token to check: is the user an org member, or a repo
     collaborator with read+ access?
   - Caches the decision briefly (short TTL on grants to bound revocation)
   - Grants or denies — fail closed on any API error

No user GitHub token is ever sent to cainban or stored. The App's repository
permissions stay **read-only** (Metadata + Administration).

## Rotation & revocation

- **Rotate the private key**: generate a new key on the App page, update the
  secret, then delete the old key on GitHub.
- **Revoke access to a repo**: remove the user's access in GitHub, or uninstall
  the App from the repo. The decision cache uses a short grant TTL, so a
  revocation takes effect quickly (bounded by that TTL).

## Security notes

- The private key and client secret live **only** in Secrets Manager.
- IAM is least-privilege: the Lambda holds `GetSecretValue` on the secret ARN alone.
- cainban **never** trusts a client-supplied claim of access. Every
  authorization decision is a server-side GitHub API result.
