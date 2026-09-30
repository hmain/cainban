import { useCallback, useEffect, useState } from "react";
import {
  signInWithRedirect,
  signOut,
  getCurrentUser,
  fetchUserAttributes,
} from "aws-amplify/auth";
import { Hub } from "aws-amplify/utils";
import { ENTRA_PROVIDER_NAME, MCP_API, MCP_CLI_CLIENT_ID, MCP_OAUTH_CALLBACK_PORT } from "./amplify";
import {
  connectRepo,
  listRepos,
  linkGitHubIdentity,
  listAvailableRepos,
  getAppInstallUrl,
  getIdToken,
  type AvailableRepo,
} from "./connectApi";

type AuthState = "loading" | "signed-out" | "signed-in";

export function App() {
  const [authState, setAuthState] = useState<AuthState>("loading");
  const [email, setEmail] = useState<string>("");

  const refreshUser = useCallback(async () => {
    try {
      await getCurrentUser();
      let mail = "";
      try {
        const attrs = await fetchUserAttributes();
        mail = attrs.email ?? "";
      } catch {
        // Attributes may be unavailable briefly right after redirect; ignore.
      }
      setEmail(mail);
      setAuthState("signed-in");
    } catch {
      setAuthState("signed-out");
    }
  }, []);

  useEffect(() => {
    void refreshUser();
    // Re-check the session on Hub auth events (sign-in completes after the
    // Hosted UI redirect returns).
    const unsub = Hub.listen("auth", ({ payload }) => {
      if (
        payload.event === "signedIn" ||
        payload.event === "signInWithRedirect" ||
        payload.event === "signedOut"
      ) {
        void refreshUser();
      }
    });
    return unsub;
  }, [refreshUser]);

  if (authState === "loading") {
    return (
      <main className="container">
        <p>Loading…</p>
      </main>
    );
  }

  return (
    <main className="container">
      <header>
        <h1>cainban</h1>
        <p className="subtitle">Connect a GitHub repo to your MCP agent.</p>
      </header>

      {authState === "signed-out" ? (
        <SignedOut />
      ) : (
        <SignedIn email={email} />
      )}
    </main>
  );
}

function SignedOut() {
  const [error, setError] = useState<string>("");
  const onSignIn = async () => {
    setError("");
    try {
      // Go straight to the Awiant Entra IdP via the Cognito Hosted UI.
      await signInWithRedirect({
        provider: { custom: ENTRA_PROVIDER_NAME },
      });
    } catch (e) {
      setError(String(e));
    }
  };
  return (
    <section aria-labelledby="signin-heading" className="card">
      <h2 id="signin-heading">Sign in</h2>
      <button className="primary" onClick={() => void onSignIn()}>
        Sign in with Awiant (Entra)
      </button>
      {error && <p role="alert" className="error">{error}</p>}
    </section>
  );
}

function SignedIn({ email }: { email: string }) {
  // Selecting a connected repo drives both the MCP config and the agent prompt.
  const [selectedRepo, setSelectedRepo] = useState<string>("");
  return (
    <>
      <section className="card" aria-label="Account">
        <div className="account-row">
          <span>
            Signed in{email ? <> as <strong>{email}</strong></> : null}
          </span>
          <button className="link" onClick={() => void signOut()}>
            Sign out
          </button>
        </div>
      </section>

      <LinkIdentity />
      <Repos selectedRepo={selectedRepo} onSelectRepo={setSelectedRepo} />
      <McpConfig selectedRepo={selectedRepo} />
    </>
  );
}

function LinkIdentity() {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const onLink = async () => {
    setBusy(true);
    setError("");
    try {
      // Navigates the browser to GitHub's authorize URL (see connectApi).
      await linkGitHubIdentity();
    } catch (e) {
      setError(String(e));
      setBusy(false);
    }
  };
  return (
    <section className="card" aria-labelledby="link-heading">
      <h2 id="link-heading">Link GitHub identity</h2>
      <p className="hint">
        Authorize cainban to confirm which GitHub account is you. Required once
        before connecting a repo.
      </p>
      <button className="primary" disabled={busy} onClick={() => void onLink()}>
        {busy ? "Redirecting…" : "Link GitHub identity"}
      </button>
      {error && <p role="alert" className="error">{error}</p>}
    </section>
  );
}

// Repos is the single consolidated repo section: it lists the repos the App can
// access (each shown as connected or connectable), lets the user pick a
// connected repo to configure their agent for, offers manual entry inline, and
// always exposes an "install / add more repos" link (so a user who installed
// the App on only some repos can add others).
function Repos({
  selectedRepo,
  onSelectRepo,
}: {
  selectedRepo: string;
  onSelectRepo: (r: string) => void;
}) {
  const [repos, setRepos] = useState<AvailableRepo[]>([]);
  const [login, setLogin] = useState<string | null>(null);
  const [state, setState] = useState<"loading" | "ready">("loading");
  const [notice, setNotice] = useState("");
  const [installUrl, setInstallUrl] = useState("");
  const [busyRepo, setBusyRepo] = useState<string | null>(null);
  const [msg, setMsg] = useState<{ kind: "ok" | "err"; text: string } | null>(
    null,
  );
  const [owner, setOwner] = useState("");
  const [manualRepo, setManualRepo] = useState("");
  const [manualBusy, setManualBusy] = useState(false);

  const load = useCallback(async () => {
    setState("loading");
    setNotice("");
    setMsg(null);
    // Always fetch the install URL so "add more repos" is available even when
    // the App is already installed on some repos.
    setInstallUrl(await getAppInstallUrl());
    const [avail, granted] = await Promise.all([
      listAvailableRepos(),
      listRepos().catch(() => ({ repos: [], github_login: null })),
    ]);
    setLogin(avail.githubLogin ?? granted.github_login ?? null);

    // Build a merged map: everything the App can access, plus anything already
    // granted (a granted repo may not be in the available list if the App was
    // later narrowed). Granted wins for the connected flag.
    const map = new Map<string, AvailableRepo>();
    if (avail.ok) {
      for (const r of avail.repos) map.set(r.full_name, { ...r });
    }
    for (const full of granted.repos) {
      map.set(full, { full_name: full, already_granted: true });
    }
    setRepos([...map.values()].sort((a, b) => a.full_name.localeCompare(b.full_name)));

    if (!avail.ok) {
      setNotice(
        avail.needsLink
          ? "Link your GitHub identity first, then reload to list your repos."
          : `Couldn't load the App's repo list (${avail.error ?? "unknown error"}) — you can still enter a repo manually.`,
      );
    } else if (avail.noInstallation && granted.repos.length === 0) {
      setNotice(
        "The cainban GitHub App isn't installed on any account you can access yet — install it, or enter a repo manually.",
      );
    }
    // Default the selection to the first connected repo, if any.
    const firstGranted = [...map.values()].find((r) => r.already_granted);
    if (firstGranted && !selectedRepo) onSelectRepo(firstGranted.full_name);
    setState("ready");
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [onSelectRepo]);

  useEffect(() => {
    void load();
  }, [load]);

  const doConnect = async (owner: string, repo: string, key: string) => {
    setBusyRepo(key);
    setMsg(null);
    try {
      const res = await connectRepo(owner, repo);
      const full = `${owner}/${repo}`;
      if (res.status === 200) {
        setMsg({ kind: "ok", text: `Connected ${res.granted}.` });
        setRepos((prev) => {
          const next = prev.some((p) => p.full_name === (res.granted ?? full))
            ? prev.map((p) =>
                p.full_name === (res.granted ?? full)
                  ? { ...p, already_granted: true }
                  : p,
              )
            : [...prev, { full_name: res.granted ?? full, already_granted: true }];
          return next.sort((a, b) => a.full_name.localeCompare(b.full_name));
        });
        onSelectRepo(res.granted ?? full);
      } else if (res.status === 403) {
        setMsg({
          kind: "err",
          text: `GitHub access to ${full} was not verified for your account.`,
        });
      } else if (res.status === 409) {
        setMsg({
          kind: "err",
          text: "No linked GitHub identity yet — use “Link GitHub identity” first.",
        });
      } else {
        setMsg({ kind: "err", text: res.error || `Request failed (${res.status}).` });
      }
    } catch (err) {
      setMsg({ kind: "err", text: String(err) });
    } finally {
      setBusyRepo(null);
    }
  };

  const onPick = (fullName: string) => {
    const slash = fullName.indexOf("/");
    if (slash <= 0 || slash === fullName.length - 1) {
      setMsg({ kind: "err", text: `Unexpected repo name: ${fullName}` });
      return;
    }
    return doConnect(fullName.slice(0, slash), fullName.slice(slash + 1), fullName);
  };

  const onManual = async (e: React.FormEvent) => {
    e.preventDefault();
    setManualBusy(true);
    await doConnect(owner.trim(), manualRepo.trim(), "__manual__");
    setManualBusy(false);
  };

  return (
    <section className="card" aria-labelledby="repos-heading">
      <div className="account-row">
        <h2 id="repos-heading">Repositories</h2>
        <button
          className="link"
          disabled={state === "loading"}
          onClick={() => void load()}
        >
          {state === "loading" ? "Loading…" : "Reload"}
        </button>
      </div>
      <p className="hint">
        Repos the cainban GitHub App can access for
        {login ? (
          <>
            {" "}
            <strong>{login}</strong>
          </>
        ) : (
          " your account"
        )}
        . Connect one, then select it below to configure your agent. A
        <strong> Connected</strong> repo is ready to use.
      </p>

      {state === "loading" && <p className="hint">Loading your repos…</p>}
      {notice && (
        <p role="status" className="hint">
          {notice}
        </p>
      )}

      {repos.length > 0 && (
        <ul className="repo-list">
          {repos.map((r) => (
            <li key={r.full_name} className="account-row">
              <label className="repo-pick">
                {r.already_granted && (
                  <input
                    type="radio"
                    name="selected-repo"
                    checked={selectedRepo === r.full_name}
                    onChange={() => onSelectRepo(r.full_name)}
                  />
                )}
                <code>{r.full_name}</code>
              </label>
              {r.already_granted ? (
                <span className="ok">Connected</span>
              ) : (
                <button
                  className="primary"
                  disabled={busyRepo !== null}
                  onClick={() => void onPick(r.full_name)}
                >
                  {busyRepo === r.full_name ? "Connecting…" : "Connect"}
                </button>
              )}
            </li>
          ))}
        </ul>
      )}

      {msg && (
        <p
          role={msg.kind === "err" ? "alert" : "status"}
          className={msg.kind === "err" ? "error" : "ok"}
        >
          {msg.text}
        </p>
      )}

      <details className="manual-entry">
        <summary>Add a repo manually</summary>
        <p className="hint">
          For a repo not in the list. Access is still verified before it’s
          connected.
        </p>
        <form onSubmit={(e) => void onManual(e)} className="repo-form">
          <label>
            Owner
            <input
              value={owner}
              onChange={(e) => setOwner(e.target.value)}
              placeholder="octocat"
              autoComplete="off"
              required
            />
          </label>
          <label>
            Repo
            <input
              value={manualRepo}
              onChange={(e) => setManualRepo(e.target.value)}
              placeholder="hello-world"
              autoComplete="off"
              required
            />
          </label>
          <button className="primary" disabled={manualBusy} type="submit">
            {manualBusy ? "Connecting…" : "Connect repo"}
          </button>
        </form>
      </details>

      {installUrl && (
        <p className="install-again">
          Don’t see a repo?{" "}
          <a className="link" href={installUrl}>
            Install the cainban App on more repositories
          </a>
          {" "}— if you installed it on only some repos, you can add others.
        </p>
      )}
    </section>
  );
}

function McpConfig({ selectedRepo }: { selectedRepo: string }) {
  const repo = selectedRepo || "owner/repo";
  const clientId = MCP_CLI_CLIENT_ID || "<McpCliClientId>";
  const [copied, setCopied] = useState<string>("");

  const addCmd = claudeAddCommand(MCP_API, clientId);
  const oauthConfig = mcpOAuthConfigSnippet(MCP_API, clientId, repo);
  const prompt = agentPrompt(MCP_API, repo);

  const copy = async (text: string, which: string) => {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(which);
      setTimeout(() => setCopied(""), 1500);
    } catch {
      // Clipboard may be unavailable (insecure context); text stays selectable.
    }
  };

  return (
    <>
      <section className="card" aria-labelledby="mcp-heading">
        <h2 id="mcp-heading">Add cainban to your MCP client</h2>
        <p className="hint">
          {selectedRepo ? (
            <>
              Set up for <strong>{selectedRepo}</strong>. Your client signs in
              once through your browser and refreshes its own tokens — there is{" "}
              <strong>no token to paste or renew</strong>.
            </>
          ) : (
            <>Connect and select a repo above to fill this in for you.</>
          )}
        </p>

        <h3 className="subhead">Claude Code (recommended)</h3>
        <p className="hint">
          One command, then authenticate in the browser. Claude Code runs OAuth
          (PKCE) and refreshes tokens itself.
        </p>
        <div className="account-row">
          <span className="muted">Add the server:</span>
          <button className="link" onClick={() => void copy(addCmd, "cmd")}>
            {copied === "cmd" ? "Copied!" : "Copy"}
          </button>
        </div>
        <pre className="code-block">
          <code>{addCmd}</code>
        </pre>
        <p className="hint">
          Then run <code>/mcp</code> in Claude Code and pick{" "}
          <strong>cainban → Authenticate</strong>. If the browser callback fails
          to load after you sign in, launch Claude Code with the pinned callback
          port: <code>MCP_OAUTH_CALLBACK_PORT={MCP_OAUTH_CALLBACK_PORT} claude</code>.
        </p>

        <details className="manual-entry">
          <summary>Config-file form (Kiro, VS Code, other clients)</summary>
          <div className="account-row">
            <span className="muted">
              For clients configured by JSON. No token — the client runs OAuth.
            </span>
            <button
              className="link"
              onClick={() => void copy(oauthConfig, "cfg")}
            >
              {copied === "cfg" ? "Copied!" : "Copy"}
            </button>
          </div>
          <pre className="code-block">
            <code>{oauthConfig}</code>
          </pre>
          <p className="hint">
            {selectedRepo ? (
              <>
                Where the client supports custom headers, keep{" "}
                <code>X-Cainban-Repo: {repo}</code>; otherwise your token’s
                <code> default_repo</code> selects the repo.
              </>
            ) : (
              <>Select a repo above to fill in the repo header.</>
            )}
          </p>
        </details>

        <BearerTokenFallback repo={repo} />
      </section>

      <section className="card" aria-labelledby="prompt-heading">
        <div className="account-row">
          <h2 id="prompt-heading">Set up your AI agent</h2>
          <button className="link" onClick={() => void copy(prompt, "prompt")}>
            {copied === "prompt" ? "Copied!" : "Copy"}
          </button>
        </div>
        <p className="hint">
          Paste this to your AI coding agent to point it at this repo’s cainban
          board over MCP.
        </p>
        <pre className="code-block">
          <code>{prompt}</code>
        </pre>
      </section>
    </>
  );
}

// BearerTokenFallback is the LEGACY manual path for MCP clients that cannot run
// OAuth themselves. It mints a short-lived Cognito ID token to paste as a bearer
// header — the ~1h re-paste UX that OAuth (above) removes. It is collapsed by
// default and the token is fetched ONLY when the user opens it, so a normal
// visit does not mint a token.
function BearerTokenFallback({ repo }: { repo: string }) {
  const [token, setToken] = useState<string>("");
  const [tokenErr, setTokenErr] = useState<string>("");
  const [loaded, setLoaded] = useState(false);
  const [copied, setCopied] = useState(false);

  const loadToken = useCallback(async () => {
    setTokenErr("");
    try {
      setToken(await getIdToken());
      setLoaded(true);
    } catch (e) {
      setTokenErr(String(e));
    }
  }, []);

  const config = mcpConfigSnippet(MCP_API, token || "<ID_TOKEN>", repo);

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(config);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // Clipboard may be unavailable; text stays selectable.
    }
  };

  return (
    <details
      className="manual-entry"
      onToggle={(e) => {
        // Fetch the token lazily, only when the fallback is first expanded.
        if ((e.target as HTMLDetailsElement).open && !loaded) void loadToken();
      }}
    >
      <summary>Client can’t do OAuth? Use a bearer token (advanced)</summary>
      <p className="hint">
        For a client that connects by URL + headers only and cannot run OAuth
        (e.g. plain header-token setups). This ID token expires (~1h) — you must
        regenerate it (Refresh) whenever your agent starts failing with 401.
        Prefer the OAuth setup above, which never needs this.
      </p>
      <div className="account-row">
        <span className="muted">Header-token config:</span>
        <button className="link" onClick={() => void copy()}>
          {copied ? "Copied!" : "Copy"}
        </button>
      </div>
      <pre className="code-block">
        <code>{config}</code>
      </pre>
      <button className="link" onClick={() => void loadToken()}>
        {loaded ? "Refresh token" : "Generate token"}
      </button>
      {tokenErr && <p role="alert" className="error">{tokenErr}</p>}
    </details>
  );
}

// claudeAddCommand builds the one-liner that registers cainban with Claude Code
// over the OAuth (PKCE) path — no token, the client self-refreshes.
function claudeAddCommand(mcpApi: string, clientId: string): string {
  const url = mcpApi || "https://<MCP_API>";
  return `claude mcp add --transport http --client-id ${clientId} cainban ${url}`;
}

// mcpOAuthConfigSnippet is the JSON config form for clients configured by file
// (Kiro/VS Code/etc.) that CAN run OAuth: it carries the client_id and NO token.
function mcpOAuthConfigSnippet(
  mcpApi: string,
  clientId: string,
  repo: string,
): string {
  const url = mcpApi || "https://<MCP_API>";
  return JSON.stringify(
    {
      mcpServers: {
        cainban: {
          type: "http",
          url,
          client_id: clientId,
          headers: {
            "X-Cainban-Repo": repo,
          },
        },
      },
    },
    null,
    2,
  );
}

function mcpConfigSnippet(mcpApi: string, token: string, repo: string): string {
  const url = mcpApi || "https://<MCP_API>";
  return JSON.stringify(
    {
      mcpServers: {
        cainban: {
          url,
          headers: {
            Authorization: `Bearer ${token}`,
            "X-Cainban-Repo": repo,
          },
        },
      },
    },
    null,
    2,
  );
}

function agentPrompt(mcpApi: string, repo: string): string {
  const url = mcpApi || "https://<MCP_API>";
  return [
    `You have a cainban kanban board for the repo ${repo}, reachable as an MCP`,
    `server at ${url} (target it with the header X-Cainban-Repo: ${repo}). Use`,
    `it as your task backend: before starting work, call list_tasks to see the`,
    `board; decompose the work I give you into tasks with create_task; move a`,
    `task with update_task_status (todo → doing → done) as you progress; and`,
    `keep the board reflecting reality. Start by listing the current tasks for`,
    `${repo}.`,
  ].join(" ");
}
