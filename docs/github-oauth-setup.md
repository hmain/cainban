# cainban — GitHub OAuth setup (MCP client authentication)

cainban uses **GitHub OAuth with Device Flow** for MCP client authentication.
When a CLI client (Claude Code, Kiro, etc.) connects, the user authenticates
directly with GitHub. Their GitHub access token is then used for API requests
and permission checks.

> **No Cognito federation:** GitHub isn't a native Cognito identity provider,
> so we skip federation entirely. The MCP API accepts GitHub access tokens
> directly in the Authorization header.

---

## Why GitHub OAuth directly

1. **Cognito limitation**: Cognito doesn't support GitHub as a native IdP
   (only Facebook, Google, Amazon, Apple, SAML, and OIDC).
2. **Simpler flow**: No token translation or claim mapping needed.
3. **Better CLI UX**: Device Flow works great for CLIs—no browser callback required.
4. **Direct permission checks**: The user's GitHub token is used to check repo
   permissions at runtime (no separate grants database).

---

## 1. Create a GitHub OAuth App

GitHub → **Settings → Developer settings → OAuth Apps → New OAuth App**

| Field | Value |
| --- | --- |
| **Application name** | e.g. `cainban` |
| **Homepage URL** | Your cainban homepage or repo URL |
| **Authorization callback URL** | `http://localhost/callback` (not used for device flow, but required) |

After creating:
1. Note the **Client ID** (e.g., `Iv23liJhsDW79P7585Wq`)
2. **Generate a new client secret** and copy it immediately

## 2. Enable Device Flow

This is critical—device flow must be explicitly enabled:

1. Go to your OAuth App's settings page
2. Check **☑ Enable Device Flow**
3. Save changes

Without this, the device flow endpoint returns a 400 error.

## 3. Load credentials into AWS Secrets Manager

Create a secret with the OAuth credentials:

```sh
export AWS_PROFILE=your-profile AWS_REGION=eu-north-1

aws secretsmanager create-secret \
  --name cainban/github-oauth \
  --secret-string '{"client_id":"Iv23liJhsDW79P7585Wq","client_secret":"your-client-secret"}'
```

Or update an existing secret:

```sh
aws secretsmanager put-secret-value \
  --secret-id cainban/github-oauth \
  --secret-string '{"client_id":"Iv23liJhsDW79P7585Wq","client_secret":"your-client-secret"}'
```

---

## How authentication works

### Device Flow (CLI clients)

1. Client calls `POST /auth/github/device`
2. Response includes `user_code` and `verification_uri`
3. User opens `https://github.com/login/device` and enters the code
4. Client polls `POST /auth/github/device/poll` with `device_code`
5. When user completes auth, poll returns `access_token`
6. Client uses `Authorization: Bearer <github_token>` for all MCP requests

### At request time

1. Lambda receives `Authorization: Bearer <github_token>`
2. Validates token by calling `GET https://api.github.com/user`
3. If valid, extracts GitHub username
4. Checks repo permission via GitHub API (cached 5 minutes)
5. Processes the MCP request

---

## API endpoints

### POST /auth/github/device

Initiates the device flow.

**Request** (optional body):
```json
{"scope": "user:email read:user repo"}
```

**Response**:
```json
{
  "user_code": "ABCD-1234",
  "verification_uri": "https://github.com/login/device",
  "verification_uri_complete": "https://github.com/login/device?user_code=ABCD-1234",
  "device_code": "dc_abc123...",
  "expires_in": 900,
  "interval": 5
}
```

Display `user_code` and `verification_uri` to the user. Store `device_code` for polling.

### POST /auth/github/device/poll

Polls for completion. Call every `interval` seconds.

**Request**:
```json
{"device_code": "dc_abc123..."}
```

**Response (pending)**:
```json
{"error": "authorization_pending"}
```

**Response (success)**:
```json
{
  "access_token": "gho_xyz...",
  "token_type": "bearer"
}
```

---

## Scope requirements

The default scope is `user:email read:user repo`:

- `user:email` — Read email address (for identity)
- `read:user` — Read user profile (for username)
- `repo` — Full repo access (required for permission checks on private repos)

---

## Security notes

- The client secret lives **only** in Secrets Manager
- IAM is least-privilege: Lambda has `GetSecretValue` only for this secret
- GitHub tokens are validated server-side on every request
- Repo permissions are checked via GitHub API (cached 5 minutes)
- Expired/revoked tokens return 401

---

## Troubleshooting

### "Device Flow must be explicitly enabled"

Go to your OAuth App settings and check "Enable Device Flow".

### 401 on MCP requests

Check that:
1. The Authorization header is `Bearer <github_token>` (not a Cognito token)
2. The GitHub token hasn't expired
3. The token has the `repo` scope for private repos

### Permission denied (403)

The user doesn't have push access to the requested repo on GitHub.
Check their GitHub permissions.
