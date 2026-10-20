import { useState, useEffect, useCallback } from "react";
import { Link } from "react-router";
import { MCP_API } from "../amplify";

/**
 * GitHubConnectPage provides a zero-friction MCP client setup experience:
 *
 * 1. User clicks "Connect with GitHub"
 * 2. Page starts device flow, shows verification code
 * 3. User authorizes on GitHub (opens in new tab)
 * 4. Page polls and automatically gets the token
 * 5. Shows ready-to-copy config for their MCP client
 *
 * No CLI required. No manual token handling. Just click and copy.
 */

type FlowState =
  | { step: "ready" }
  | { step: "pending"; userCode: string; verificationUri: string; deviceCode: string; expiresAt: number; interval: number }
  | { step: "success"; token: string }
  | { step: "error"; message: string };

const API_BASE = MCP_API.replace(/\/$/, "");

export function GitHubConnectPage() {
  const [state, setState] = useState<FlowState>({ step: "ready" });
  const [repo, setRepo] = useState("");

  const startFlow = useCallback(async () => {
    try {
      const res = await fetch(`${API_BASE}/auth/github/device`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: "{}",
      });
      if (!res.ok) {
        throw new Error(`Failed to start: ${res.status}`);
      }
      const data = await res.json();
      setState({
        step: "pending",
        userCode: data.user_code,
        verificationUri: data.verification_uri,
        deviceCode: data.device_code,
        expiresAt: Date.now() + data.expires_in * 1000,
        interval: data.interval * 1000,
      });
    } catch (e) {
      setState({ step: "error", message: String(e) });
    }
  }, []);

  // Poll for token when in pending state
  useEffect(() => {
    if (state.step !== "pending") return;

    let cancelled = false;
    const poll = async () => {
      while (!cancelled && Date.now() < state.expiresAt) {
        await new Promise((r) => setTimeout(r, state.interval));
        if (cancelled) break;

        try {
          const res = await fetch(`${API_BASE}/auth/github/device/poll`, {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ device_code: state.deviceCode }),
          });
          const data = await res.json();

          if (data.access_token) {
            setState({ step: "success", token: data.access_token });
            return;
          }
          if (data.error === "authorization_pending") {
            continue;
          }
          if (data.error === "slow_down") {
            // Increase interval
            continue;
          }
          if (data.error) {
            setState({ step: "error", message: data.error_description || data.error });
            return;
          }
        } catch (e) {
          console.error("Poll error:", e);
        }
      }

      if (!cancelled && Date.now() >= state.expiresAt) {
        setState({ step: "error", message: "Authorization timed out. Please try again." });
      }
    };

    void poll();
    return () => { cancelled = true; };
  }, [state]);

  return (
    <section className="connect-page github-connect">
      <div className="account-row">
        <h2>Connect to cainban</h2>
        <Link className="link" to="/">← Back</Link>
      </div>

      {state.step === "ready" && (
        <ReadyState onStart={startFlow} />
      )}

      {state.step === "pending" && (
        <PendingState
          userCode={state.userCode}
          verificationUri={state.verificationUri}
        />
      )}

      {state.step === "success" && (
        <SuccessState token={state.token} repo={repo} onRepoChange={setRepo} />
      )}

      {state.step === "error" && (
        <ErrorState message={state.message} onRetry={() => setState({ step: "ready" })} />
      )}
    </section>
  );
}

function ReadyState({ onStart }: { onStart: () => void }) {
  return (
    <div className="card">
      <h3>Connect your AI agent to cainban</h3>
      <p>
        Sign in with GitHub to get your MCP config. Your GitHub permissions
        determine which repos you can access.
      </p>
      <button className="btn primary" onClick={onStart}>
        Connect with GitHub
      </button>
      <p className="hint" style={{ marginTop: "1rem" }}>
        This uses GitHub's secure device flow — no passwords are stored.
      </p>
    </div>
  );
}

function PendingState({
  userCode,
  verificationUri,
}: {
  userCode: string;
  verificationUri: string;
}) {
  const [copied, setCopied] = useState(false);

  const copyCode = async () => {
    await navigator.clipboard.writeText(userCode);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };

  return (
    <div className="card">
      <h3>Authorize on GitHub</h3>

      <div className="device-flow-box">
        <p>1. Copy this code:</p>
        <div className="code-display">
          <code className="user-code">{userCode}</code>
          <button className="copy-btn" onClick={() => void copyCode()}>
            {copied ? "✓" : "Copy"}
          </button>
        </div>

        <p>2. Open GitHub and paste the code:</p>
        <a
          href={verificationUri}
          target="_blank"
          rel="noopener noreferrer"
          className="btn"
        >
          Open GitHub →
        </a>
      </div>

      <div className="waiting-indicator">
        <span className="spinner" /> Waiting for authorization...
      </div>
    </div>
  );
}

function SuccessState({
  token,
  repo,
  onRepoChange,
}: {
  token: string;
  repo: string;
  onRepoChange: (r: string) => void;
}) {
  const [copied, setCopied] = useState<string>("");
  const [client, setClient] = useState<"claude" | "cursor" | "vscode" | "other">("claude");

  const mcpUrl = `${API_BASE}/mcp`;
  const maskedToken = token.slice(0, 10) + "..." + token.slice(-4);

  const configs: Record<string, string> = {
    claude: JSON.stringify({
      mcpServers: {
        cainban: {
          url: mcpUrl,
          headers: {
            Authorization: `Bearer ${token}`,
            ...(repo ? { "X-Cainban-Repo": repo } : {}),
          },
        },
      },
    }, null, 2),

    cursor: JSON.stringify({
      mcpServers: {
        cainban: {
          type: "http",
          url: mcpUrl,
          headers: {
            Authorization: `Bearer ${token}`,
            ...(repo ? { "X-Cainban-Repo": repo } : {}),
          },
        },
      },
    }, null, 2),

    vscode: JSON.stringify({
      servers: {
        cainban: {
          type: "http",
          url: mcpUrl,
          headers: {
            Authorization: `Bearer ${token}`,
            ...(repo ? { "X-Cainban-Repo": repo } : {}),
          },
        },
      },
    }, null, 2),

    other: `URL: ${mcpUrl}
Authorization: Bearer ${token}${repo ? `\nX-Cainban-Repo: ${repo}` : ""}`,
  };

  const configPaths: Record<string, string> = {
    claude: "~/.config/claude/claude_desktop_config.json",
    cursor: "Settings → MCP → Add Server (or mcp.json)",
    vscode: ".vscode/mcp.json",
    other: "Your MCP client's config",
  };

  const copyConfig = async (which: string) => {
    await navigator.clipboard.writeText(configs[client]);
    setCopied(which);
    setTimeout(() => setCopied(""), 2000);
  };

  return (
    <div className="card success-card">
      <h3>✓ Connected!</h3>
      <p>
        Your token: <code>{maskedToken}</code>
      </p>

      <div className="repo-input-section">
        <label>
          Default repo (optional):
          <input
            type="text"
            value={repo}
            onChange={(e) => onRepoChange(e.target.value)}
            placeholder="owner/repo"
            className="repo-input"
          />
        </label>
        <p className="hint">
          Set a default repo, or omit it and pass the repo per-request with <code>X-Cainban-Repo</code> header.
        </p>
      </div>

      <div className="client-selector">
        <p>Choose your MCP client:</p>
        <div className="client-buttons">
          {(["claude", "cursor", "vscode", "other"] as const).map((c) => (
            <button
              key={c}
              className={`btn ${client === c ? "active" : ""}`}
              onClick={() => setClient(c)}
            >
              {c === "claude" ? "Claude Desktop" :
               c === "cursor" ? "Cursor" :
               c === "vscode" ? "VS Code" : "Other"}
            </button>
          ))}
        </div>
      </div>

      <div className="config-section">
        <p className="config-path">
          Add to: <code>{configPaths[client]}</code>
        </p>
        <div className="config-box">
          <pre className="config-code">{configs[client]}</pre>
          <button
            className="copy-btn"
            onClick={() => void copyConfig(client)}
          >
            {copied === client ? "✓ Copied" : "Copy"}
          </button>
        </div>
      </div>

      <div className="next-steps">
        <h4>Next steps:</h4>
        <ol>
          <li>Copy the config above</li>
          <li>Add it to your MCP client's config file</li>
          <li>Restart or reload your MCP client</li>
          <li>Try: "List my cainban tasks"</li>
        </ol>
      </div>
    </div>
  );
}

function ErrorState({
  message,
  onRetry,
}: {
  message: string;
  onRetry: () => void;
}) {
  return (
    <div className="card error-card">
      <h3>Connection failed</h3>
      <p className="error-message">{message}</p>
      <button className="btn" onClick={onRetry}>
        Try again
      </button>
    </div>
  );
}

export default GitHubConnectPage;
