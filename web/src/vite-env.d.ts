/// <reference types="vite/client" />

// Typed VITE_ configuration. All are injected at build time from the CDK stack
// outputs (see web/README.md). None is a secret — a public SPA client has no
// client secret, which is expected.
interface ImportMetaEnv {
  /** Cognito user pool id, e.g. eu-north-1_knwIJmOs6 (stack output UserPoolId). */
  readonly VITE_USER_POOL_ID: string;
  /** SPA app client id (stack output SpaClientId) — public, PKCE. */
  readonly VITE_USER_POOL_CLIENT_ID: string;
  /**
   * Cognito Hosted UI domain WITHOUT scheme, e.g.
   * cainban-emawiant-528757808822.auth.eu-north-1.amazoncognito.com
   * (derived from stack output HostedUiDomain).
   */
  readonly VITE_COGNITO_DOMAIN: string;
  /** Connect API base URL (stack output ConnectApiUrl), trailing slash ok. */
  readonly VITE_CONNECT_API: string;
  /** MCP API base URL (stack output McpApiUrl), shown for the client config. */
  readonly VITE_MCP_API: string;
  /**
   * Redirect URL registered as a SPA client callback, e.g.
   * http://localhost:5173/ in dev or the Amplify app URL in prod. Optional —
   * defaults to window.location.origin + "/".
   */
  readonly VITE_REDIRECT_URL?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}
