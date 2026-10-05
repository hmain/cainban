import { Amplify } from "aws-amplify";

// Amplify Gen 2 client-library configuration for the EXISTING CDK-managed
// Cognito user pool + the new public SPA client. We deliberately do NOT use
// `defineAuth`/`defineData` (which would create a NEW backend pool) — identity
// stays owned by the CDK stack. Amplify is used purely as the browser auth
// client (Hosted UI OAuth + token storage/refresh) and, separately, for
// Hosting/CI via amplify.yml.
//
// Every value comes from a VITE_ env var injected at build time from the stack
// outputs (see web/README.md). There is no client secret — a public PKCE SPA
// client has none, which is correct and expected.

const redirectUrl =
  import.meta.env.VITE_REDIRECT_URL || `${window.location.origin}/`;

export function configureAmplify(): void {
  Amplify.configure({
    Auth: {
      Cognito: {
        userPoolId: import.meta.env.VITE_USER_POOL_ID,
        userPoolClientId: import.meta.env.VITE_USER_POOL_CLIENT_ID,
        loginWith: {
          oauth: {
            domain: import.meta.env.VITE_COGNITO_DOMAIN,
            scopes: ["openid", "email", "profile"],
            redirectSignIn: [redirectUrl],
            redirectSignOut: [redirectUrl],
            // Authorization-code grant + PKCE (public client, no secret).
            responseType: "code",
          },
        },
      },
    },
  });
}

// The Cognito provider name for Awiant's Entra federation, as declared in the
// CDK stack (SupportedIdentityProviders on the SPA client). signInWithRedirect
// targets this custom provider so the user goes straight to Entra.
export const ENTRA_PROVIDER_NAME = "EntraAwiant";

// Endpoint bases, normalized to no trailing slash for clean path joins.
export const CONNECT_API = String(import.meta.env.VITE_CONNECT_API || "").replace(
  /\/+$/,
  "",
);
export const MCP_API = String(import.meta.env.VITE_MCP_API || "").replace(
  /\/+$/,
  "",
);

// The dedicated public PKCE client id for MCP clients that run OAuth themselves
// (Claude Code). Injected from the McpCliClientId stack output. Used only to
// render the OAuth setup snippet — the browser SPA itself signs in with the SPA
// client (VITE_USER_POOL_CLIENT_ID), not this one.
export const MCP_CLI_CLIENT_ID = String(
  import.meta.env.VITE_MCP_CLI_CLIENT_ID || "",
).trim();

// Claude Code's OAuth loopback callback port. cainban-mcp-cli registers this
// port on the Cognito client, so pinning it avoids the ephemeral-port
// redirect-mismatch (Cognito can't wildcard loopback ports). Shown in the setup
// instructions as MCP_OAUTH_CALLBACK_PORT.
export const MCP_OAUTH_CALLBACK_PORT = "3118";

// Kiro's oauth.redirectUri pin. Kiro (kiro-cli / the Kiro Crew dashboard
// Authorize banner) serves its OAuth loopback callback on the path
// /oauth/callback and, when redirectUri is unset, picks a RANDOM port that
// Cognito rejects (redirect_mismatch — the "An error was encountered with the
// requested page" failure). This exact value is registered on the cainban-mcp-cli
// Cognito client (infra/stack.go mcpCliCallbackUrls); 127.0.0.1 and localhost are
// not interchangeable to Cognito, so the spelling must match the registered one.
export const MCP_OAUTH_REDIRECT_URI = "http://127.0.0.1:3334/oauth/callback";
