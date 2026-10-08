import { useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import type { ReposState } from "./useConnectedRepos";
import { rememberLastRepo } from "./useConnectedRepos";
import { disconnectRepo } from "../connectApi";

// RepoRail is the persistent connected-repo navigation: a left rail on wide
// screens (CSS) and a header dropdown on narrow screens. Selecting a repo
// navigates to its board URL. The currently-viewed repo is marked selected.
//
// Each repo carries a Remove control. Removal is PER-USER: it revokes only this
// user's grant (DELETE /connect/repo) and re-points their default on the
// backend; it never deletes the shared board/tasks and never affects another
// user — re-connecting restores the board. The control is confirm-guarded to
// avoid a one-click accident.
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

  // Which repo is mid-confirm, and which is mid-DELETE, plus any error line.
  const [confirming, setConfirming] = useState<string | null>(null);
  const [removing, setRemoving] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  const doRemove = async (full: string) => {
    const [owner, repo] = full.split("/");
    if (!owner || !repo) return;
    setRemoving(full);
    setError(null);
    try {
      const res = await disconnectRepo(owner, repo);
      if (res.status !== 200) {
        setError(res.error || `Couldn’t remove ${full} (${res.status}).`);
        setRemoving(null);
        return;
      }
      // If we just removed the repo we're viewing, don't let the `/` redirect
      // bounce back to it: clear it as last-viewed, then navigate home.
      if (current === full) {
        rememberLastRepo(""); // overwrite so lastViewedRepo no longer names it
        navigate("/");
      }
      setConfirming(null);
      setRemoving(null);
      reload(); // refresh the rail from the server (source of truth)
      // Notify other mounted views (the Connect page's repo list) that the
      // grant set changed, so they refresh without needing a tab blur/focus.
      window.dispatchEvent(new CustomEvent("cainban:repos-changed"));
    } catch (e) {
      setError(String(e));
      setRemoving(null);
    }
  };

  return (
    <nav className="repo-rail" aria-label="Your repositories">
      <div className="repo-rail-head">
        <span className="repo-rail-title">Repos</span>
        <Link className="link" to="/connect">
          + Connect
        </Link>
      </div>

      {repos.status === "loading" && <p className="hint">Loading…</p>}
      {repos.status === "error" && (
        <p role="alert" className="error">
          Couldn’t load your repos.
        </p>
      )}
      {repos.status === "ready" && items.length === 0 && (
        <p className="hint">
          No connected repos yet. <Link to="/connect">Connect one</Link>.
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
                    disabled={removing === full}
                    onClick={() => void doRemove(full)}
                    title="Removes your access only; the board and its tasks stay for anyone else and return if you re-connect"
                  >
                    {removing === full ? "Removing…" : "Yes"}
                  </button>
                  <button
                    type="button"
                    className="link repo-rail-confirm-no"
                    disabled={removing === full}
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
                  title={`Remove ${full} (your access only)`}
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

      {/* Narrow: a dropdown (CSS shows one or the other). The remove control
          lives in the wide list; on narrow screens a user removes from the
          board header's own affordance (future) — keeping the dropdown minimal. */}
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
