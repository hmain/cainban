import { useCallback, useEffect, useState } from "react";
import { listRepos } from "../connectApi";

export type ReposState =
  | { status: "loading" }
  | { status: "ready"; repos: string[] }
  | { status: "error"; error: string };

// useConnectedRepos loads the signed-in user's CONNECTED repos (the granted
// set from GET /connect/repos) once for the app shell, so the rail, the `/`
// redirect, and the not-connected guard all read one source. Sorted
// alphabetically to match the connect page's ordering.
export function useConnectedRepos(): ReposState & { reload: () => void } {
  const [state, setState] = useState<ReposState>({ status: "loading" });

  const reload = useCallback(() => {
    setState({ status: "loading" });
    listRepos()
      .then((r) =>
        setState({
          status: "ready",
          repos: [...r.repos].sort((a, b) => a.localeCompare(b)),
        }),
      )
      .catch((e) => setState({ status: "error", error: String(e) }));
  }, []);

  useEffect(() => {
    reload();
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
