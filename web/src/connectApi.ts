import { fetchAuthSession } from "aws-amplify/auth";
import { CONNECT_API } from "./amplify";

// A thin typed client for the cainban Connect API. Every call carries the
// Cognito ID token as `Authorization: Bearer <idToken>` — the connect Lambda
// validates it signature-first before any GitHub/secret/grant action (see
// src/systems/connect/handler.go). The ID token (not the access token) is used
// because the connect API's JWT audience is the SPA client id, which the ID
// token's `aud` matches.

/** Return the current Cognito ID token, or throw if the user is not signed in. */
export async function getIdToken(): Promise<string> {
  const session = await fetchAuthSession();
  const token = session.tokens?.idToken?.toString();
  if (!token) {
    throw new Error("not signed in");
  }
  return token;
}

async function authHeader(): Promise<HeadersInit> {
  return { Authorization: `Bearer ${await getIdToken()}` };
}

export interface ConnectRepoResult {
  status: number;
  /** Canonical owner/repo granted, on 200. */
  granted?: string;
  /** Server error message, on non-200. */
  error?: string;
}

/**
 * POST /connect/repo {owner,repo}. Maps the documented handler responses:
 *   200 -> { granted }         access verified, grant written
 *   403 -> not verified        GitHub access to repo not verified
 *   409 -> no linked identity  must run linkGitHubIdentity() first
 */
export async function connectRepo(
  owner: string,
  repo: string,
): Promise<ConnectRepoResult> {
  const res = await fetch(`${CONNECT_API}/connect/repo`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      ...(await authHeader()),
    },
    body: JSON.stringify({ owner, repo }),
  });
  const body = (await res.json().catch(() => ({}))) as {
    granted?: string;
    error?: string;
  };
  return { status: res.status, granted: body.granted, error: body.error };
}

export interface ReposList {
  repos: string[];
  github_login: string | null;
}

/** GET /connect/repos -> { repos[], github_login }. */
export async function listRepos(): Promise<ReposList> {
  const res = await fetch(`${CONNECT_API}/connect/repos`, {
    headers: await authHeader(),
  });
  if (!res.ok) {
    throw new Error(`GET /connect/repos failed: ${res.status}`);
  }
  const body = (await res.json()) as ReposList;
  return { repos: body.repos ?? [], github_login: body.github_login ?? null };
}

/**
 * Start the GitHub OAuth link. A plain browser navigation cannot set the
 * Authorization header, so we fetch /connect/github/start WITH the bearer,
 * DON'T follow the redirect (redirect: "manual"), read the 302 Location the
 * handler returns (handleStart -> http.Redirect to the GitHub authorize URL),
 * and then navigate the browser there ourselves.
 */
export async function linkGitHubIdentity(): Promise<void> {
  const res = await fetch(`${CONNECT_API}/connect/github/start`, {
    method: "GET",
    headers: await authHeader(),
    redirect: "manual",
  });

  // With redirect:"manual" a 302 surfaces as an opaqueredirect (status 0) and
  // the Location header is not readable cross-origin. To read Location we ask
  // the server not to be followed by the browser; API Gateway returns the 302
  // with the Location header on a normal (non-opaque) response when the fetch
  // is same-spec. Handle both: prefer a readable Location, else fall back to a
  // full-page navigation that lets the browser follow the redirect chain.
  const location = res.headers.get("Location");
  if (location) {
    window.location.assign(location);
    return;
  }

  // Fallback: some environments hide the Location on a manual redirect. Re-issue
  // as a normal fetch and follow it in JS via the final response URL.
  const followed = await fetch(`${CONNECT_API}/connect/github/start`, {
    method: "GET",
    headers: await authHeader(),
  });
  if (followed.url && followed.url !== `${CONNECT_API}/connect/github/start`) {
    window.location.assign(followed.url);
    return;
  }
  throw new Error(
    "could not read the GitHub authorize URL from /connect/github/start",
  );
}
