# cainban connect-page SPA (`web/`)

A React + Vite + TypeScript single page whose one job is to get a user a
ready-to-paste **MCP client config** for cainban. Authentication is
**GitHub-only**: the page runs GitHub's **device flow** against the MCP API and
hands back the user's own GitHub token as the MCP bearer. There is no Cognito,
no Entra, no Amplify Auth, and no backend sign-in.

What the user does:

1. **Connect with GitHub** (`/connect`) — the page POSTs to
   `<MCP_API>/auth/github/device`, shows the user code + `github.com/login/device`
   link, and polls `<MCP_API>/auth/github/device/poll` until GitHub returns a
   token.
2. **Copy the MCP config** — the success screen renders a per-client config
   (Claude Desktop, Cursor, VS Code, Kiro, or generic) with
   `Authorization: Bearer <github_token>` and an optional `X-Cainban-Repo`
   default repo. The user pastes it into their AI client.
3. Grab an embeddable **README badge** at `/badges` — see
   [`docs/badges.md`](../docs/badges.md).

Authorization happens server-side on every MCP request: the Lambda validates the
GitHub token (`GET /user`) for identity and checks repo access with the GitHub
App installation token. The SPA never holds a Cognito token and never performs an
OAuth redirect.

## Configuration (`VITE_*`)

Injected at build time. Copy `.env.example` to `.env` for local dev, or set it as
an Amplify Hosting environment variable. There are no secrets.

| Env var | Source (CDK stack output) | Example |
| --- | --- | --- |
| `VITE_MCP_API` | `McpApiUrl` | `https://cl4qelp4hf.execute-api.eu-north-1.amazonaws.com/` |

Read the output after deploy with `cd infra && cdk deploy` (see `infra/README.md`).

## Develop

```
cd web
npm install
cp .env.example .env   # set VITE_MCP_API to the McpApiUrl output
npm run dev            # http://localhost:5173
```

## Build

```
cd web
npm ci
npm run build          # tsc -b && vite build -> web/dist
npm run build:widget   # embeddable board widget -> web/dist/widget
```

## Deploy (Amplify Hosting)

`amplify.yml` (repo root) builds `web/` with Node 22 and publishes `web/dist`.
Connect this repo to an Amplify app, set `VITE_MCP_API` in the Amplify console,
and deploy. There is no Amplify backend and no identity provider to wire up — the
Amplify app origin must be in the MCP API's CORS allowlist (`spaCallbackUrls`
context in `infra/cdk.json`) so the browser can reach the device-flow endpoints.
