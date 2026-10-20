// Configuration for the cainban SPA.
// With GitHub-only auth, we no longer need Cognito/Amplify Auth.
// This file exports the API endpoint for the GitHub device flow.

// MCP API endpoint base, normalized to no trailing slash for clean path joins.
export const MCP_API = String(
  import.meta.env.VITE_MCP_API || "https://cl4qelp4hf.execute-api.eu-north-1.amazonaws.com/"
).replace(/\/+$/, "");
