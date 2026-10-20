# cainban — GitHub App setup (operator checklist)

cainban uses a GitHub App to verify user permissions at runtime. When a user
makes a request, cainban checks the GitHub API to see if they have push access
to the requested repo. This guide walks you through registering the App and
loading its credentials into AWS Secrets Manager.

> **GitHub-native permissions:** Authorization is checked at runtime via
> GitHub API calls (cached for 5 minutes). Users can access any repo they have
> push access to on GitHub — no separate "connect" step required.

---

## Why a GitHub App

cainban checks a user's access to `owner/repo` on the server at request time. A
GitHub App authenticates as itself with an App JWT to make API calls that verify
whether a user has push permission to a repository. This check happens on every
request (with 5-minute caching) rather than storing grants in a database.

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

## 7. Enable GitHub-native permissions

Set the environment variable on the MCP Lambda to enable the new auth mode:

```sh
aws lambda update-function-configuration \
  --function-name cainban-mcp \
  --environment "Variables={CAINBAN_USE_GITHUB_PERMISSIONS=true,...}"
```

Or add it to the CDK stack and redeploy.

---

## How it works at runtime

1. User authenticates via Cognito (Entra federation)
2. Their Cognito token includes `github_user` and `github_token` claims
3. When they request a repo, the MCP Lambda:
   - Extracts the GitHub identity from the token
   - Calls GitHub API to check if the user has push permission
   - Caches the result for 5 minutes
   - Grants or denies access based on the GitHub response

## Rotation & revocation

- **Rotate the private key**: generate a new key on the App page, update the
  secret, then delete the old key on GitHub.
- **Revoke access to a repo**: remove the user's access in GitHub, or uninstall
  the App from the repo. The permission cache expires in 5 minutes.

## Security notes

- The private key and client secret live **only** in Secrets Manager.
- IAM is least-privilege: the Lambda holds `GetSecretValue` on the secret ARN alone.
- cainban **never** trusts a client-supplied claim of access. Every
  authorization decision is a server-side GitHub API result.
