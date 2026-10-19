import { useState, useCallback, useEffect } from "react";
import { Link } from "react-router";
import { MCP_API, MCP_CLI_CLIENT_ID, MCP_OAUTH_REDIRECT_URI } from "../amplify";
import {
  connectRepo,
  listRepos,
  listAvailableRepos,
  getAppInstallUrl,
  type AvailableRepo,
} from "../connectApi";

/**
 * ConnectPageSimplified implements Phase 5 of RFC magical-onboarding:
 * a simplified connect page that emphasizes the CLI command and reduces
 * the cognitive load of the manual setup process.
 *
 * Target: ~150 lines (down from 824)
 *
 * Structure:
 * 1. Hero CTA: `npx cainban connect`
 * 2. Install badges (placeholder)
 * 3. Repo connection (simplified)
 * 4. Single expandable for manual setup fallback
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
      <RepoConnection />

      <details className="card manual-fallback">
        <summary>Manual setup (if the CLI doesn't work)</summary>
        <ManualSetup />
      </details>
    </section>
  );
}

function HeroCTA() {
  const [copied, setCopied] = useState(false);

  const command = `npx cainban connect --mcp-url ${MCP_API}`;

  const copyCommand = async () => {
    await navigator.clipboard.writeText(command);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };

  return (
    <section className="card hero-cta">
      <h3>One command to connect</h3>
      <p className="hint">
        Run this in your terminal to discover, authenticate, and configure
        cainban in one step:
      </p>
      <div className="command-box">
        <code className="command">{command}</code>
        <button
          className="copy-btn"
          onClick={() => void copyCommand()}
          aria-label="Copy command"
        >
          {copied ? "✓ Copied" : "Copy"}
        </button>
      </div>
      <p className="hint small">
        The CLI auto-detects your AI client (Claude, Cursor, VS Code, Kiro) and
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

function RepoConnection() {
  const [repos, setRepos] = useState<AvailableRepo[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [success, setSuccess] = useState("");
  const [connecting, setConnecting] = useState<string | null>(null);
  const [installUrl, setInstallUrl] = useState("");

  const loadRepos = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      setInstallUrl(await getAppInstallUrl());
      const [availableResult, grantedResult] = await Promise.all([
        listAvailableRepos().catch(() => ({ ok: false, repos: [] as AvailableRepo[], needsLink: false, reason: "" })),
        listRepos().catch(() => ({ repos: [] as string[], github_login: null })),
      ]);
      const grantedSet = new Set(grantedResult.repos);
      const merged = availableResult.repos.map((r: AvailableRepo) => ({
        ...r,
        already_granted: grantedSet.has(r.full_name),
      }));
      setRepos(merged);
    } catch (e) {
      setError("Couldn't load repos. Make sure you've linked your GitHub identity.");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadRepos();
  }, [loadRepos]);

  const onConnect = async (fullName: string) => {
    const [owner, repo] = fullName.split("/");
    setConnecting(fullName);
    setError("");
    setSuccess("");
    try {
      const result = await connectRepo(owner, repo);
      if (result.status === 200) {
        setSuccess(`Connected ${fullName}!`);
        setRepos((prev) =>
          prev.map((r) =>
            r.full_name === fullName ? { ...r, already_granted: true } : r
          )
        );
        window.dispatchEvent(new CustomEvent("cainban:repos-changed"));
      } else {
        setError(result.error || `Failed to connect ${fullName}`);
      }
    } catch (e) {
      setError(String(e));
    } finally {
      setConnecting(null);
    }
  };

  const connected = repos.filter((r) => r.already_granted);
  const available = repos.filter((r) => !r.already_granted);

  return (
    <section className="card">
      <h3>Connected repositories</h3>
      {loading ? (
        <p className="hint">Loading...</p>
      ) : connected.length === 0 ? (
        <p className="hint">No repos connected yet. Connect one below.</p>
      ) : (
        <ul className="repo-list">
          {connected.map((r) => (
            <li key={r.full_name} className="repo connected">
              <span className="repo-name">{r.full_name}</span>
              <span className="status">✓ Connected</span>
            </li>
          ))}
        </ul>
      )}

      {available.length > 0 && (
        <>
          <h4>Available to connect</h4>
          <ul className="repo-list">
            {available.slice(0, 5).map((r) => (
              <li key={r.full_name} className="repo">
                <span className="repo-name">{r.full_name}</span>
                <button
                  className="link"
                  onClick={() => void onConnect(r.full_name)}
                  disabled={connecting === r.full_name}
                >
                  {connecting === r.full_name ? "Connecting..." : "Connect"}
                </button>
              </li>
            ))}
          </ul>
          {available.length > 5 && (
            <p className="hint small">
              {available.length - 5} more repos available
            </p>
          )}
        </>
      )}

      {error && <p className="error">{error}</p>}
      {success && <p className="success">{success}</p>}

      {installUrl && (
        <p className="hint small">
          Don't see your repo?{" "}
          <a href={installUrl} target="_blank" rel="noopener noreferrer">
            Install the GitHub App
          </a>
        </p>
      )}
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
