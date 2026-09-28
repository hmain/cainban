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
 * Authorization header, and a fetch() cannot follow a 302 into github.com
 * (cross-origin auth page has no CORS headers). So we fetch
 * /connect/github/start WITH the bearer AND Accept: application/json; the
 * endpoint returns {"authorize_url": "..."} instead of redirecting, and we
 * navigate the top-level window there ourselves.
 */
export async function linkGitHubIdentity(): Promise<void> {
  const res = await fetch(`${CONNECT_API}/connect/github/start`, {
    method: "GET",
    headers: { ...(await authHeader()), Accept: "application/json" },
  });
  if (!res.ok) {
    throw new Error(`connect/github/start failed: ${res.status}`);
  }
  const body = (await res.json()) as { authorize_url?: string };
  if (!body.authorize_url) {
    throw new Error("connect/github/start did not return an authorize_url");
  }
  window.location.assign(body.authorize_url);
}
