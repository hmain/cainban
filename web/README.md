# cainban connect-page SPA (`web/`)

A minimal React + Vite + TypeScript single page that lets a user:

1. **Sign in with Microsoft Entra ID** via the existing Cognito **Hosted UI**
   (`signInWithRedirect` → Entra → back with tokens).
2. **Link a GitHub identity** (`GET /connect/github/start` with a bearer token,
   then follow the returned 302 to GitHub).
3. **Connect a GitHub repo** (`POST /connect/repo {owner,repo}` with the bearer).
4. See **My connected repos** (`GET /connect/repos`).
5. Copy a ready-to-paste **MCP client config** (MCP URL + `Authorization: Bearer`
   + `X-Cainban-Repo`).

It is configured against the **existing** CDK-managed Cognito user pool and the
**new** public SPA app client — it does **not** use Amplify `defineAuth`/`defineData`
to create a new backend pool. Amplify is used only as (a) the browser auth client
(Hosted UI OAuth + token storage/refresh) and (b) Hosting/CI via `amplify.yml`.

## Configuration (`VITE_*`)

All configuration is injected at build time from environment variables. Copy
`.env.example` to `.env` for local dev, or set these as Amplify Hosting
environment variables. **None of these is a secret** — a public PKCE SPA client
has no client secret, which is expected.

| Env var | Source (CDK stack output) | Example |
| --- | --- | --- |
| `VITE_USER_POOL_ID` | `UserPoolId` | `eu-north-1_knwIJmOs6` |
| `VITE_USER_POOL_CLIENT_ID` | `SpaClientId` | (the SPA client id) |
| `VITE_COGNITO_DOMAIN` | `HostedUiDomain` (strip `https://`) | `cainban-emawiant-528757808822.auth.eu-north-1.amazoncognito.com` |
| `VITE_CONNECT_API` | `ConnectApiUrl` | `https://64f3ievzmf.execute-api.eu-north-1.amazonaws.com/` |
| `VITE_MCP_API` | `McpApiUrl` | `https://cl4qelp4hf.execute-api.eu-north-1.amazonaws.com/` |
| `VITE_MCP_CLI_CLIENT_ID` (optional) | `McpCliClientId` | `79jr68h816vb3bcbgi6ueum2ht` |
| `VITE_REDIRECT_URL` (optional) | your app URL | `http://localhost:5173/` |

Read the outputs after deploy with:

```
cd infra
cdk deploy --profile aws-test-hamin \
  -c entraIssuer=https://login.microsoftonline.com/<tenant>/v2.0 \
  -c entraClientId=<entra-app-client-id>
```

`HostedUiDomain` is printed as `https://<prefix>.auth.eu-north-1.amazoncognito.com`;
set `VITE_COGNITO_DOMAIN` to that value **without** the `https://`.

## Develop

```
cd web
npm install
cp .env.example .env   # fill in the values from the stack outputs
npm run dev            # http://localhost:5173
```

## Build

```
cd web
npm ci
npm run build          # tsc -b && vite build -> web/dist
```

## Deploy (Amplify Hosting)

`amplify.yml` (at the repo root) builds `web/` with Node 22 and publishes
`web/dist`. Connect this repo to an Amplify app, set the `VITE_*` env vars above
in the Amplify console, and deploy. There is no Amplify backend — identity stays
owned by the CDK stack.

## Operator steps (one-time, post-deploy)

1. **Deploy the stack** (`infra/`) with the real Entra `-c entraIssuer=…` and
   `-c entraClientId=…`. Note the `HostedUiDomain`, `EntraRedirectUri`,
   `SpaClientId` outputs.
2. **Register the redirect URI in Entra**: in the Entra app registration, add the
   `EntraRedirectUri` output (`<HostedUiDomain>/oauth2/idpresponse`) as a Web
   redirect URI.
3. **Fill the OIDC secret**: put the Entra app's `client_id` and `client_secret`
   into the `cainban/entra-oidc` Secrets Manager secret (created empty by CDK).
4. **Register the SPA callback URL**: after the first Amplify deploy you get the
   app URL. Add it to the SPA client's callback/logout URLs by redeploying with
   `-c spaCallbackUrls=<amplify-url>,http://localhost:5173/`
   `-c spaLogoutUrls=<amplify-url>,http://localhost:5173/`, then set
   `VITE_REDIRECT_URL` to the Amplify URL.
5. **Set the Amplify env vars** (`VITE_*`) from the stack outputs and rebuild.

## SaaS pluggability

Adding a second customer's OIDC IdP is additive in the CDK: append an
`oidcProviderSpec` entry (its own placeholder secret) in `infra/stack.go`. The SPA
would then let the user pick which provider to sign in with (today it targets the
single `EntraAwiant` provider directly).
