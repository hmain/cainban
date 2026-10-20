import { useState, useEffect, useCallback } from "react";

/**
 * GitHubConnectPage - Zero-friction MCP client setup
 *
 * Flow:
 * 1. User clicks "Connect with GitHub"
 * 2. Page starts device flow, shows verification code
 * 3. User authorizes on GitHub (opens in new tab)
 * 4. Page polls and automatically gets the token
 * 5. Shows ready-to-copy config for their MCP client
 *
 * No CLI required. No manual token handling. Just click and copy.
 */

const MCP_API = import.meta.env.VITE_MCP_API || "https://cl4qelp4hf.execute-api.eu-north-1.amazonaws.com/";
const API_BASE = MCP_API.replace(/\/$/, "");

type FlowState =
  | { step: "ready" }
  | { step: "pending"; userCode: string; verificationUri: string; deviceCode: string; expiresAt: number; interval: number }
  | { step: "success"; token: string }
  | { step: "error"; message: string };

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
        const text = await res.text();
        throw new Error(`Failed to start device flow: ${res.status} - ${text}`);
      }
      const data = await res.json();
      setState({
        step: "pending",
        userCode: data.user_code,
        verificationUri: data.verification_uri,
        deviceCode: data.device_code,
        expiresAt: Date.now() + data.expires_in * 1000,
        interval: (data.interval || 5) * 1000,
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
      let interval = state.interval;

      while (!cancelled && Date.now() < state.expiresAt) {
        await new Promise((r) => setTimeout(r, interval));
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
            interval = Math.min(interval + 5000, 30000);
            continue;
          }
          if (data.error === "expired_token") {
            setState({ step: "error", message: "Authorization expired. Please try again." });
            return;
          }
          if (data.error === "access_denied") {
            setState({ step: "error", message: "Authorization was denied." });
            return;
          }
          if (data.error) {
            setState({ step: "error", message: data.error_description || data.error });
            return;
          }
        } catch (e) {
          console.error("Poll error:", e);
          // Continue polling on network errors
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
    <section className="connect-page">
      <header className="connect-header">
        <h1>Connect to cainban</h1>
        <p className="subtitle">Get your MCP config to use cainban with AI agents</p>
      </header>

      {state.step === "ready" && <ReadyState onStart={startFlow} />}
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
    <div className="card connect-card">
      <h2>Sign in with GitHub</h2>
      <p>
        Connect with GitHub to get your MCP config. Your GitHub permissions
        determine which repos you can access.
      </p>

      <button className="btn primary large" onClick={onStart}>
        <GitHubIcon /> Connect with GitHub
      </button>

      <div className="info-box">
        <h3>How it works</h3>
        <ol>
          <li>Click the button above to start</li>
          <li>Copy the code and authorize on GitHub</li>
          <li>Get your MCP config to paste into your AI client</li>
        </ol>
      </div>

      <p className="hint">
        Uses GitHub's secure device flow — no passwords are stored.
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
    <div className="card connect-card pending-card">
      <h2>Authorize on GitHub</h2>

      <div className="step-box">
        <div className="step">
          <span className="step-number">1</span>
          <div className="step-content">
            <p>Copy this code:</p>
            <div className="code-display">
              <code className="user-code">{userCode}</code>
              <button className="btn small" onClick={() => void copyCode()}>
                {copied ? "✓ Copied" : "Copy"}
              </button>
            </div>
          </div>
        </div>

        <div className="step">
          <span className="step-number">2</span>
          <div className="step-content">
            <p>Open GitHub and paste the code:</p>
            <a
              href={verificationUri}
              target="_blank"
              rel="noopener noreferrer"
              className="btn primary"
            >
              Open GitHub →
            </a>
          </div>
        </div>
      </div>

      <div className="waiting-box">
        <div className="spinner" />
        <p>Waiting for authorization...</p>
        <p className="hint">This page will update automatically once you authorize.</p>
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
  const [client, setClient] = useState<"claude" | "cursor" | "vscode" | "kiro" | "other">("claude");

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

    kiro: JSON.stringify({
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

    other: `MCP URL: ${mcpUrl}
Authorization: Bearer ${token}${repo ? `\nX-Cainban-Repo: ${repo}` : ""}`,
  };

  const configPaths: Record<string, string> = {
    claude: "~/.config/claude/claude_desktop_config.json",
    cursor: "Settings → MCP → Add Server",
    vscode: ".vscode/mcp.json",
    kiro: "Settings → MCP Servers → Add",
    other: "Your MCP client's config",
  };

  const copyConfig = async () => {
    await navigator.clipboard.writeText(configs[client]);
    setCopied(client);
    setTimeout(() => setCopied(""), 2000);
  };

  return (
    <div className="card connect-card success-card">
      <div className="success-header">
        <span className="success-icon">✓</span>
        <div>
          <h2>Connected!</h2>
          <p className="token-info">Token: <code>{maskedToken}</code></p>
        </div>
      </div>

      <div className="config-section">
        <div className="repo-input-group">
          <label htmlFor="repo-input">Default repo (optional):</label>
          <input
            id="repo-input"
            type="text"
            value={repo}
            onChange={(e) => onRepoChange(e.target.value)}
            placeholder="owner/repo"
          />
          <p className="hint">
            Set a default repo, or leave empty to specify per-request.
          </p>
        </div>

        <div className="client-selector">
          <p>Select your MCP client:</p>
          <div className="client-tabs">
            {(["claude", "cursor", "vscode", "kiro", "other"] as const).map((c) => (
              <button
                key={c}
                className={`tab ${client === c ? "active" : ""}`}
                onClick={() => setClient(c)}
              >
                {c === "claude" ? "Claude Desktop" :
                 c === "cursor" ? "Cursor" :
                 c === "vscode" ? "VS Code" :
                 c === "kiro" ? "Kiro" : "Other"}
              </button>
            ))}
          </div>
        </div>

        <div className="config-box">
          <div className="config-header">
            <span className="config-path">{configPaths[client]}</span>
            <button className="btn small" onClick={() => void copyConfig()}>
              {copied === client ? "✓ Copied" : "Copy Config"}
            </button>
          </div>
          <pre className="config-code">{configs[client]}</pre>
        </div>
      </div>

      <div className="next-steps">
        <h3>Next steps</h3>
        <ol>
          <li>Copy the config above</li>
          <li>Add it to <code>{configPaths[client]}</code></li>
          <li>Restart or reload your MCP client</li>
          <li>Try: <em>"List my cainban tasks"</em></li>
        </ol>
      </div>

      <button className="btn link-btn" onClick={() => window.location.reload()}>
        Connect another account
      </button>
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
    <div className="card connect-card error-card">
      <h2>Connection failed</h2>
      <p className="error-message">{message}</p>
      <button className="btn primary" onClick={onRetry}>
        Try again
      </button>
    </div>
  );
}

function GitHubIcon() {
  return (
    <svg className="icon" viewBox="0 0 24 24" fill="currentColor" width="20" height="20">
      <path d="M12 0C5.37 0 0 5.37 0 12c0 5.31 3.435 9.795 8.205 11.385.6.105.825-.255.825-.57 0-.285-.015-1.23-.015-2.235-3.015.555-3.795-.735-4.035-1.41-.135-.345-.72-1.41-1.23-1.695-.42-.225-1.02-.78-.015-.795.945-.015 1.62.87 1.845 1.23 1.08 1.815 2.805 1.305 3.495.99.105-.78.42-1.305.765-1.605-2.67-.3-5.46-1.335-5.46-5.925 0-1.305.465-2.385 1.23-3.225-.12-.3-.54-1.53.12-3.18 0 0 1.005-.315 3.3 1.23.96-.27 1.98-.405 3-.405s2.04.135 3 .405c2.295-1.56 3.3-1.23 3.3-1.23.66 1.65.24 2.88.12 3.18.765.84 1.23 1.905 1.23 3.225 0 4.605-2.805 5.625-5.475 5.925.435.375.81 1.095.81 2.22 0 1.605-.015 2.895-.015 3.3 0 .315.225.69.825.57A12.02 12.02 0 0024 12c0-6.63-5.37-12-12-12z"/>
    </svg>
  );
}

export default GitHubConnectPage;
