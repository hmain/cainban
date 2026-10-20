import { useState, useCallback, useEffect } from "react";
import { Link } from "react-router";
import { MCP_API, MCP_CLI_CLIENT_ID, MCP_OAUTH_REDIRECT_URI } from "../amplify";
import { getLocalRepos, addLocalRepo, removeLocalRepo } from "../connectApi";

/**
 * ConnectPageSimplified implements Phase 5 of RFC magical-onboarding:
 * a simplified connect page that emphasizes the CLI command and reduces
 * the cognitive load of the manual setup process.
 *
 * With GitHub-native permissions, users don't "connect" repos via an API call.
 * Instead, they add repos to a local list, and the backend checks GitHub
 * permissions at request time. This page lets users manage their local repo
 * list and set up the MCP client.
 */
export function ConnectPageSimplified() {
  return (
    <section className="connect-page simplified">
      <div className="account-row">
        <h2>Connect cainban to your AI agent</h2>
        <Link className="link" to="/">
          ← Back to board
        </Link>
      </div>

      <HeroCTA />
      <InstallBadges />
      <RepoList />

      <details className="card manual-fallback">
        <summary>Manual setup (if the CLI doesn't work)</summary>
        <ManualSetup />
      </details>
    </section>
  );
}

function HeroCTA() {
  const [copied, setCopied] = useState(false);

  // `cainban` is a Go binary distributed via GitHub Releases / `go install`,
  // NOT an npm package — `npx cainban` 404s on the npm registry. Install it
  // first, then run the one-command connect flow.
  const installCommand = `go install github.com/hmain/cainban/cmd/cainban@latest`;
  const connectCommand = `cainban connect --mcp-url ${MCP_API}`;
  const command = `${installCommand}\n${connectCommand}`;

  const copyCommand = async () => {
    await navigator.clipboard.writeText(command);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };

  return (
    <section className="card hero-cta">
      <h3>Two commands to connect</h3>
      <p className="hint">
        Install the cainban CLI, then run connect to discover, authenticate, and
        configure cainban in one step:
      </p>
      <div className="command-box">
        <code className="command">{command}</code>
        <button
          className="copy-btn"
          onClick={() => void copyCommand()}
          aria-label="Copy commands"
        >
          {copied ? "✓ Copied" : "Copy"}
        </button>
      </div>
      <p className="hint small">
        Prefer a pre-built binary? Grab one from{" "}
        <a
          href="https://github.com/hmain/cainban/releases/latest"
          target="_blank"
          rel="noopener noreferrer"
        >
          GitHub Releases
        </a>
        . The CLI auto-detects your AI client (Claude, Cursor, VS Code, Kiro) and
        writes the config for you.
      </p>
    </section>
  );
}

function InstallBadges() {
  // Placeholder for Phase 3: one-click install badges
  // These will be deep links that register the MCP server with a single click
  return (
    <section className="card install-badges">
      <h3>Or install with one click</h3>
      <div className="badges">
        <span className="badge placeholder" title="Coming soon">
          Claude Desktop
        </span>
        <span className="badge placeholder" title="Coming soon">
          Cursor
        </span>
        <span className="badge placeholder" title="Coming soon">
          VS Code
        </span>
        <span className="badge placeholder" title="Coming soon">
          Kiro
        </span>
      </div>
      <p className="hint small">One-click install coming soon</p>
    </section>
  );
}

function RepoList() {
  const [repos, setRepos] = useState<string[]>([]);
  const [newRepo, setNewRepo] = useState("");
  const [error, setError] = useState("");
  const [success, setSuccess] = useState("");

  const loadRepos = useCallback(() => {
    setRepos(getLocalRepos().sort((a, b) => a.localeCompare(b)));
  }, []);

  useEffect(() => {
    loadRepos();
  }, [loadRepos]);

  const onAddRepo = (e: React.FormEvent) => {
    e.preventDefault();
    setError("");
    setSuccess("");

    const trimmed = newRepo.trim();
    if (!trimmed) return;

    // Validate format: owner/repo
    if (!trimmed.includes("/") || trimmed.split("/").length !== 2) {
      setError("Enter a repo in owner/repo format (e.g., hmain/cainban)");
      return;
    }

    if (addLocalRepo(trimmed)) {
      setSuccess(`Added ${trimmed}. You'll need permission on GitHub to access it.`);
      setNewRepo("");
      loadRepos();
      window.dispatchEvent(new CustomEvent("cainban:repos-changed"));
    } else {
      setError(`${trimmed} is already in your list`);
    }
  };

  const onRemoveRepo = (repo: string) => {
    setError("");
    setSuccess("");
    if (removeLocalRepo(repo)) {
      loadRepos();
      window.dispatchEvent(new CustomEvent("cainban:repos-changed"));
    }
  };

  return (
    <section className="card">
      <h3>Your repositories</h3>
      <p className="hint">
        Add repos you want to use with cainban. You'll need read access on GitHub
        to view boards — the backend checks your permissions when you open a board.
      </p>

      <form className="add-repo-form" onSubmit={onAddRepo}>
        <input
          type="text"
          value={newRepo}
          onChange={(e) => setNewRepo(e.target.value)}
          placeholder="owner/repo"
          className="repo-input"
          aria-label="Repository name"
        />
        <button type="submit" className="btn">
          Add
        </button>
      </form>

      {error && <p className="error">{error}</p>}
      {success && <p className="success">{success}</p>}

      {repos.length === 0 ? (
        <p className="hint">No repos added yet. Add one above to get started.</p>
      ) : (
        <ul className="repo-list">
          {repos.map((repo) => (
            <li key={repo} className="repo">
              <Link to={`/board/${repo}`} className="repo-name">
                {repo}
              </Link>
              <button
                className="link remove-btn"
                onClick={() => onRemoveRepo(repo)}
                aria-label={`Remove ${repo}`}
              >
                Remove
              </button>
            </li>
          ))}
        </ul>
      )}

      <p className="hint small">
        <strong>Note:</strong> If you don't have access to a repo on GitHub,
        you'll see a permission error when opening its board.
      </p>
    </section>
  );
}

function ManualSetup() {
  const mcpUrl = MCP_API;
  const clientId = MCP_CLI_CLIENT_ID;
  const redirectUri = MCP_OAUTH_REDIRECT_URI;

  const claudeCommand = `claude mcp add --transport http --client-id ${clientId} cainban ${mcpUrl}`;

  const jsonConfig = JSON.stringify(
    {
      cainban: {
        type: "http",
        url: mcpUrl,
        oauth: {
          clientId,
          redirectUri,
        },
      },
    },
    null,
    2
  );

  return (
    <div className="manual-setup">
      <h4>Claude Code (CLI)</h4>
      <pre className="code-block">{claudeCommand}</pre>

      <h4>Other clients (JSON config)</h4>
      <p className="hint small">
        Add to your MCP config file (mcpServers section):
      </p>
      <pre className="code-block">{jsonConfig}</pre>

      <h4>Steps after adding config</h4>
      <ol className="steps">
        <li>Reload or restart your MCP client</li>
        <li>Authenticate when prompted (browser sign-in)</li>
        <li>
          Start a <strong>new session</strong> (some clients require this)
        </li>
        <li>
          Verify with <code>list_tasks</code>
        </li>
      </ol>

      <p className="hint small">
        Need more help?{" "}
        <a
          href="/cainban-mcp-setup.md"
          target="_blank"
          rel="noopener noreferrer"
        >
          Full setup guide
        </a>
      </p>
    </div>
  );
}

export default ConnectPageSimplified;
