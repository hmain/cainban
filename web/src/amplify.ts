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
