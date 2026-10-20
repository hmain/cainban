import { useState } from "react";
import { Link, useNavigate } from "react-router";
import type { ReposState } from "./useConnectedRepos";
import { rememberLastRepo } from "./useConnectedRepos";
import { removeLocalRepo } from "../connectApi";

// RepoRail is the persistent repo navigation: a left rail on wide screens (CSS)
// and a header dropdown on narrow screens. Selecting a repo navigates to its
// board URL. The currently-viewed repo is marked selected.
//
// With GitHub-native permissions, repos are stored locally — removing one from
// the rail just removes it from the local list. The user can re-add it anytime
// if they have GitHub access.
export function RepoRail({
  repos,
  current,
  reload,
}: {
  repos: ReposState;
  current: string | null;
  reload: () => void;
}) {
  const navigate = useNavigate();

  const items =
    repos.status === "ready" ? repos.repos : ([] as string[]);

  // Which repo is mid-confirm
  const [confirming, setConfirming] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  const doRemove = (full: string) => {
    setError(null);
    const removed = removeLocalRepo(full);
    if (!removed) {
      setError(`Couldn't remove ${full}.`);
      return;
    }
    // If we just removed the repo we're viewing, navigate home
    if (current === full) {
      rememberLastRepo(""); // clear so redirect doesn't bounce back
      navigate("/");
    }
    setConfirming(null);
    reload();
    // Notify other components that the repo list changed
    window.dispatchEvent(new CustomEvent("cainban:repos-changed"));
  };

  return (
    <nav className="repo-rail" aria-label="Your repositories">
      <div className="repo-rail-head">
        <span className="repo-rail-title">Repos</span>
        <Link className="link" to="/connect">
          + Add
        </Link>
      </div>

      {repos.status === "loading" && <p className="hint">Loading…</p>}
      {repos.status === "error" && (
        <p role="alert" className="error">
          Couldn't load your repos.
        </p>
      )}
      {repos.status === "ready" && items.length === 0 && (
        <p className="hint">
          No repos yet. <Link to="/connect">Add one</Link>.
        </p>
      )}

      {error && (
        <p role="alert" className="error repo-rail-error">
          {error}
        </p>
      )}

      {/* Wide: a list of links, each with a Remove control. */}
      {items.length > 0 && (
        <ul className="repo-rail-list">
          {items.map((full) => (
            <li key={full} className="repo-rail-item">
              <Link
                className={
                  full === current ? "repo-rail-link selected" : "repo-rail-link"
                }
                to={`/r/${full}`}
                aria-current={full === current ? "page" : undefined}
              >
                {full}
              </Link>

              {confirming === full ? (
                <span className="repo-rail-confirm">
                  <span className="repo-rail-confirm-q">Remove?</span>
                  <button
                    type="button"
                    className="link repo-rail-confirm-yes"
                    onClick={() => doRemove(full)}
                  >
                    Yes
                  </button>
                  <button
                    type="button"
                    className="link repo-rail-confirm-no"
                    onClick={() => {
                      setConfirming(null);
                      setError(null);
                    }}
                  >
                    No
                  </button>
                </span>
              ) : (
                <button
                  type="button"
                  className="repo-rail-remove"
                  aria-label={`Remove ${full}`}
                  title={`Remove ${full}`}
                  onClick={() => {
                    setConfirming(full);
                    setError(null);
                  }}
                >
                  ×
                </button>
              )}
            </li>
          ))}
        </ul>
      )}

      {/* Narrow: a dropdown (CSS shows one or the other). */}
      {items.length > 0 && (
        <label className="repo-rail-select">
          <span className="sr-only">Select a repository</span>
          <select
            value={current ?? ""}
            onChange={(e) => {
              if (e.target.value) navigate(`/r/${e.target.value}`);
            }}
          >
            {!current && <option value="">Select a repo…</option>}
            {items.map((full) => (
              <option key={full} value={full}>
                {full}
              </option>
            ))}
          </select>
        </label>
      )}
    </nav>
  );
}
