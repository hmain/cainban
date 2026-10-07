import { Link, useNavigate } from "react-router-dom";
import type { ReposState } from "./useConnectedRepos";

// RepoRail is the persistent connected-repo navigation: a left rail on wide
// screens (CSS) and a header dropdown on narrow screens. Selecting a repo
// navigates to its board URL. The currently-viewed repo is marked selected.
export function RepoRail({
  repos,
  current,
}: {
  repos: ReposState;
  current: string | null;
}) {
  const navigate = useNavigate();

  const items =
    repos.status === "ready" ? repos.repos : ([] as string[]);

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

      {/* Wide: a list of links. */}
      {items.length > 0 && (
        <ul className="repo-rail-list">
          {items.map((full) => (
            <li key={full}>
              <Link
                className={
                  full === current ? "repo-rail-link selected" : "repo-rail-link"
                }
                to={`/r/${full}`}
                aria-current={full === current ? "page" : undefined}
              >
                {full}
              </Link>
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
