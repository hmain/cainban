import { fetchAuthSession } from "aws-amplify/auth";

// Token management for the cainban SPA. With GitHub-native permissions enabled,
// the Connect API no longer exists — repo authorization is checked at request
// time via the GitHub API. This module provides only token utilities.

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
 * Force a fresh Cognito session. With GitHub-native permissions, repo access is
 * checked at request time, so there's no need to refresh after "connecting" a
 * repo. This is kept for cases where the session may have become stale.
 */
export async function refreshIdToken(): Promise<void> {
  try {
    await fetchAuthSession({ forceRefresh: true });
  } catch {
    // Non-fatal: the session refreshes on its normal cadence regardless.
  }
}

// --- Local repo management ---
// With GitHub-native permissions, users don't "connect" repos via an API.
// Instead, they add repos to a local list, and the backend checks GitHub
// permissions when they access the board.

const REPOS_KEY = "cainban:repos";

/** Get the list of repos the user has added locally. */
export function getLocalRepos(): string[] {
  try {
    const raw = localStorage.getItem(REPOS_KEY);
    if (!raw) return [];
    const parsed = JSON.parse(raw);
    return Array.isArray(parsed) ? parsed.filter((r) => typeof r === "string") : [];
  } catch {
    return [];
  }
}

/** Add a repo to the local list. Returns true if added, false if already present. */
export function addLocalRepo(repo: string): boolean {
  const normalized = repo.trim().toLowerCase();
  if (!normalized || !normalized.includes("/")) return false;
  const repos = getLocalRepos();
  if (repos.some((r) => r.toLowerCase() === normalized)) return false;
  repos.push(repo.trim());
  try {
    localStorage.setItem(REPOS_KEY, JSON.stringify(repos));
  } catch {
    return false;
  }
  return true;
}

/** Remove a repo from the local list. Returns true if removed. */
export function removeLocalRepo(repo: string): boolean {
  const normalized = repo.trim().toLowerCase();
  const repos = getLocalRepos();
  const filtered = repos.filter((r) => r.toLowerCase() !== normalized);
  if (filtered.length === repos.length) return false;
  try {
    localStorage.setItem(REPOS_KEY, JSON.stringify(filtered));
  } catch {
    return false;
  }
  return true;
}
