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

/**
 * Force a fresh Cognito session so the ID token's `repos` / `default_repo`
 * claims are re-minted. The claims are baked into the token by the pre-token
 * Lambda from the LIVE grants store, so after connecting/disconnecting a repo
 * the EXISTING token is stale (it carries the pre-change claim for up to ~1h).
 * The board authorizes `list_tasks` against that claim, so without this a
 * just-connected repo 403s ("not authorized — connect it first") until the
 * token would naturally refresh. Calling this after a successful connect makes
 * the next board navigation carry a token that grants the repo. Best-effort:
 * a refresh failure is swallowed (the token still refreshes on its own later).
 */
export async function refreshIdToken(): Promise<void> {
  try {
    await fetchAuthSession({ forceRefresh: true });
  } catch {
    // Non-fatal: the session refreshes on its normal cadence regardless.
  }
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

export interface DisconnectRepoResult {
  status: number;
  /** Canonical owner/repo revoked, on 200. */
  revoked?: string;
  /** Server error message, on non-200. */
  error?: string;
}

/**
 * DELETE /connect/repo {owner,repo} — revoke THIS user's grant for a repo.
 * Per-user only: the backend removes just the caller's grant (and re-points
 * their default_repo if needed); it never deletes the shared board/tasks and
 * never affects another user. Re-connecting restores the board.
 * Maps the handler responses:
 *   200 -> { revoked }
 *   4xx/5xx -> { error }
 */
export async function disconnectRepo(
  owner: string,
  repo: string,
): Promise<DisconnectRepoResult> {
  const res = await fetch(`${CONNECT_API}/connect/repo`, {
    method: "DELETE",
    headers: {
      "Content-Type": "application/json",
      ...(await authHeader()),
    },
    body: JSON.stringify({ owner, repo }),
  });
  const body = (await res.json().catch(() => ({}))) as {
    revoked?: string;
    error?: string;
  };
  return { status: res.status, revoked: body.revoked, error: body.error };
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

/** One repo the signed-in user can access through the App installation. */
export interface AvailableRepo {
  full_name: string;
  already_granted: boolean;
}

/**
 * Result of GET /connect/available-repos. `ok` distinguishes a real list from a
 * fail-closed condition so the SPA NEVER mistakes an error/empty for "no repos"
 * and always offers manual entry when it cannot list.
 *   ok:true                      -> render the pick-list (may be empty +
 *                                    noInstallation when the App isn't installed)
 *   ok:false, needsLink:true     -> 409: prompt "Link GitHub first" / re-link
 *   ok:false, needsLink:false    -> other error: show manual entry + reason
 */
export interface AvailableReposResult {
  ok: boolean;
  repos: AvailableRepo[];
  githubLogin: string | null;
  noInstallation: boolean;
  needsLink: boolean;
  error?: string;
}

/**
 * GET /connect/available-repos. FAIL CLOSED on the client too: any non-200 (or
 * a fetch/parse throw) resolves to { ok:false, ... } with a reason — it never
 * throws and never returns a fabricated list, so the caller renders manual
 * entry instead of an empty pick-list that could read as "you have no repos".
 */
export async function listAvailableRepos(): Promise<AvailableReposResult> {
  try {
    const res = await fetch(`${CONNECT_API}/connect/available-repos`, {
      headers: await authHeader(),
    });
    const body = (await res.json().catch(() => ({}))) as {
      repos?: AvailableRepo[];
      github_login?: string | null;
      no_installation?: boolean;
      error?: string;
    };
    if (!res.ok) {
      return {
        ok: false,
        repos: [],
        githubLogin: body.github_login ?? null,
        noInstallation: false,
        needsLink: res.status === 409,
        error: body.error || `Request failed (${res.status}).`,
      };
    }
    return {
      ok: true,
      repos: body.repos ?? [],
      githubLogin: body.github_login ?? null,
      noInstallation: body.no_installation ?? false,
      needsLink: false,
    };
  } catch (e) {
    return {
      ok: false,
      repos: [],
      githubLogin: null,
      noInstallation: false,
      needsLink: false,
      error: String(e),
    };
  }
}

/**
 * GET /connect/app-info -> the GitHub App install URL (with a signed state) so
 * the SPA can send the user to install the App when they have no installation
 * yet. Returns "" when the backend has no slug configured (SPA then keeps the
 * manual-entry fallback only). Fail-closed: any error yields "".
 */
export async function getAppInstallUrl(): Promise<string> {
  try {
    const res = await fetch(`${CONNECT_API}/connect/app-info`, {
      headers: await authHeader(),
    });
    if (!res.ok) return "";
    const body = (await res.json().catch(() => ({}))) as {
      install_url?: string;
    };
    return body.install_url ?? "";
  } catch {
    return "";
  }
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
