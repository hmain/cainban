# Cainban MCP Client Onboarding

Connect your AI agent (Claude, Cursor, VS Code Copilot, etc.) to cainban in under 2 minutes.

## Option 1: Web-Based Setup (Easiest)

1. **Go to the connect page**: Visit the cainban web app and click **"Connect with GitHub"**
2. **Authorize**: Enter the code shown on GitHub's authorization page
3. **Copy your config**: The page generates ready-to-paste config for your MCP client

That's it! The web page handles the entire device flow for you.

---

## Option 2: Command Line

### Step 1: Get Your GitHub Token (30 seconds)

Run this in your terminal:

```bash
curl -s -X POST https://cl4qelp4hf.execute-api.eu-north-1.amazonaws.com/auth/github/device -H "Content-Type: application/json" -d '{}'
```

You'll get a response like:
```json
{
  "user_code": "ABCD-1234",
  "verification_uri": "https://github.com/login/device",
  "device_code": "...",
  "expires_in": 899,
  "interval": 5
}
```

### Step 2: Authorize (30 seconds)

1. Open **https://github.com/login/device** in your browser
2. Enter the `user_code` (e.g., `ABCD-1234`)
3. Click **Authorize**

### Step 3: Get Your Token (10 seconds)

Poll for your token (replace `YOUR_DEVICE_CODE` with the `device_code` from step 1):

```bash
curl -s -X POST https://cl4qelp4hf.execute-api.eu-north-1.amazonaws.com/auth/github/device/poll \
  -H "Content-Type: application/json" \
  -d '{"device_code":"YOUR_DEVICE_CODE"}'
```

You'll get:
```json
{
  "access_token": "gho_xxxxxxxxxxxx",
  "token_type": "bearer"
}
```

**Save this `access_token` — you'll use it in your MCP client config.**

### Step 4: Configure Your MCP Client

Add to your MCP client configuration:

**Claude Desktop** (`~/.config/claude/claude_desktop_config.json`):
```json
{
  "mcpServers": {
    "cainban": {
      "url": "https://cl4qelp4hf.execute-api.eu-north-1.amazonaws.com/mcp",
      "headers": {
        "Authorization": "Bearer gho_xxxxxxxxxxxx",
        "X-Cainban-Repo": "your-github-username/your-repo"
      }
    }
  }
}
```

**VS Code** (`.vscode/mcp.json`):
```json
{
  "servers": {
    "cainban": {
      "type": "http",
      "url": "https://cl4qelp4hf.execute-api.eu-north-1.amazonaws.com/mcp",
      "headers": {
        "Authorization": "Bearer gho_xxxxxxxxxxxx",
        "X-Cainban-Repo": "your-github-username/your-repo"
      }
    }
  }
}
```

**Cursor** (Settings → MCP → Add Server):
- URL: `https://cl4qelp4hf.execute-api.eu-north-1.amazonaws.com/mcp`
- Headers:
  - `Authorization`: `Bearer gho_xxxxxxxxxxxx`
  - `X-Cainban-Repo`: `your-github-username/your-repo`

---

## What You Get

Once connected, your AI agent can:

- **Create tasks**: "Create a task to fix the login bug"
- **List tasks**: "Show me all tasks in doing status"
- **Update tasks**: "Move task 5 to done"
- **Search tasks**: "Find tasks related to authentication"
- **Link tasks**: "Task 3 blocks task 7"
- **View activity**: "What changed on the board today?"

---

## How Authentication Works

Cainban uses your **GitHub identity** for authentication and authorization:

1. **Device Flow**: You authorize via github.com/login/device (no passwords stored)
2. **GitHub Token**: The token proves who you are and which repos you can access
3. **Repo Permissions**: Cainban checks GitHub to verify you have write access to the repo

This means:
- ✅ No separate cainban account needed
- ✅ No passwords to manage
- ✅ Your GitHub repo permissions apply automatically
- ✅ Revoke access anytime at github.com/settings/applications

---

## Troubleshooting

### "authorization_pending" when polling
The user hasn't completed the GitHub authorization yet. Wait and poll again.

### "expired_token" when polling
The device code expired (15 minutes). Start over from Step 1.

### 401 Unauthorized on MCP requests
Your GitHub token is invalid or expired. Get a new one from Step 1.

### 403 Forbidden on MCP requests
You don't have write access to the repo in `X-Cainban-Repo`. Check:
- Is the repo name correct? (format: `owner/repo`)
- Do you have push access to this repo on GitHub?

---

## Token Lifetime

GitHub OAuth tokens from the device flow:
- **Access tokens**: Valid until revoked (no automatic expiry)
- **Revocation**: Go to github.com/settings/applications → Authorized OAuth Apps → cainban → Revoke

To get a fresh token, repeat the device flow (Steps 1-3).
