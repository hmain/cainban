import { useCallback, useEffect, useState } from "react";
import { getLocalRepos } from "../connectApi";

export type ReposState =
  | { status: "loading" }
  | { status: "ready"; repos: string[] }
  | { status: "error"; error: string };

// useConnectedRepos loads the user's repo list from localStorage. With GitHub-
// native permissions, "connected" repos are just the list of repos the user has
// added locally — actual access is checked at request time via the GitHub API.
// The rail, the `/` redirect, and guards all read this one source.
export function useConnectedRepos(): ReposState & { reload: () => void } {
  const [state, setState] = useState<ReposState>({ status: "loading" });

  const reload = useCallback(() => {
    // localStorage is synchronous, but we keep the loading state for consistency
    // with components that expect the state machine.
    setState({ status: "loading" });
    try {
      const repos = getLocalRepos().sort((a, b) => a.localeCompare(b));
      setState({ status: "ready", repos });
    } catch (e) {
      setState({ status: "error", error: String(e) });
    }
  }, []);

  useEffect(() => {
    reload();
    // Listen for repo changes from other components
    const handler = () => reload();
    window.addEventListener("cainban:repos-changed", handler);
    return () => window.removeEventListener("cainban:repos-changed", handler);
  }, [reload]);

  return { ...state, reload };
}

const LAST_REPO_KEY = "cainban:lastRepo";

/** Remember the last board the user viewed, for the `/` redirect. */
export function rememberLastRepo(repo: string): void {
  try {
    window.localStorage.setItem(LAST_REPO_KEY, repo);
  } catch {
    // localStorage may be unavailable (private mode); the redirect falls back
    // to the first connected repo.
  }
}

/** The last board the user viewed, if any. */
export function lastViewedRepo(): string | null {
  try {
    return window.localStorage.getItem(LAST_REPO_KEY);
  } catch {
    return null;
  }
}
