import { useState } from "react";
import { Link } from "react-router";
import { InstallBadges } from "../components/InstallBadges";

/**
 * BadgesPage (route `/badges`) shows the available cainban README badges with
 * copy-paste Markdown and HTML snippets. An optional OWNER/REPO field deep-links
 * the Connect page in the generated snippets so maintainers can drop a
 * repo-scoped "Connect with cainban" button straight into their README.
 */
export function BadgesPage() {
  const [repo, setRepo] = useState("");
  const trimmed = repo.trim();
  const valid = trimmed === "" || /^[^/\s]+\/[^/\s]+$/.test(trimmed);

  return (
    <section className="badges-page">
      <div className="account-row">
        <h2>README badges</h2>
        <Link className="link" to="/">
          ← Back to board
        </Link>
      </div>

      <p className="hint">
        Add a cainban badge to your repository's README so visitors can connect
        their AI agent in one click. Copy the Markdown or HTML snippet below.
      </p>

      <section className="card">
        <label className="badge-repo-field">
          <span>Repository (optional)</span>
          <input
            type="text"
            placeholder="owner/repo"
            value={repo}
            onChange={(e) => setRepo(e.target.value)}
            spellCheck={false}
            autoCapitalize="none"
            autoCorrect="off"
          />
        </label>
        {!valid && (
          <p className="error">Use the form owner/repo (e.g. hmain/cainban).</p>
        )}
        <p className="hint small">
          Leave blank for a generic badge, or enter <code>owner/repo</code> to
          deep-link the Connect page to that repository.
        </p>
      </section>

      <InstallBadges repo={valid ? trimmed : ""} />
    </section>
  );
}

export default BadgesPage;
