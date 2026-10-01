import { useCallback, useEffect, useState } from "react";
import {
  signInWithRedirect,
  signOut,
  fetchAuthSession,
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
import { listActivity, type ActivityEvent } from "./mcpApi";

type AuthState = "loading" | "signed-out" | "signed-in";

export function App() {
  const [authState, setAuthState] = useState<AuthState>("loading");
  const [email, setEmail] = useState<string>("");

  const refreshUser = useCallback(async () => {
    try {
      // Read the session + email from the ID TOKEN, not fetchUserAttributes().
      // fetchUserAttributes() calls Cognito's GetUser API, which requires the
      // `aws.cognito.signin.user.admin` scope on the access token — the SPA only
      // requests `openid email profile`, so GetUser returns a 400
      // (NotAuthorizedException: Access Token does not have required scopes).
      // The email is already a claim in the ID token (guaranteed by the `email`
      // scope + the Entra attribute map), so we read it there: no cognito-idp
      // round-trip, no 400, and it works for federated (Entra) users whose
      // attributes GetUser serves poorly anyway.
      const session = await fetchAuthSession();
      const idToken = session.tokens?.idToken;
      if (!idToken) {
        setAuthState("signed-out");
        return;
      }
      const claimEmail = idToken.payload?.email;
      setEmail(typeof claimEmail === "string" ? claimEmail : "");
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
      <ActivityFeed selectedRepo={selectedRepo} />
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
  const setupUrl =
    typeof window !== "undefined"
      ? `${window.location.origin}/cainban-mcp-setup.md`
      : "/cainban-mcp-setup.md";
  const prompt = agentPrompt(MCP_API, repo, setupUrl);

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
        {!selectedRepo && (
          <p className="hint">
            Connect and select a repo above to fill this in for you.
          </p>
        )}

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
          board over MCP. It tells the agent to read the full setup guide and set
          itself up.
        </p>
        <pre className="code-block">
          <code>{prompt}</code>
        </pre>
        <p className="install-again">
          Full agent-readable setup guide:{" "}
          <a className="link" href="/cainban-mcp-setup.md" download>
            cainban-mcp-setup.md
          </a>
        </p>
      </section>
    </>
  );
}

// ActivityFeed shows the append-only "who changed what" audit feed (P5A.4) for
// the selected repo's board, read from the MCP `list_activity` tool over the
// MCP JSON-RPC endpoint (see mcpApi.ts). Read-only: it never mutates the board.
// It renders nothing until a repo is selected, and loads on demand (and on an
// explicit Refresh) so a normal visit makes no MCP call.
function ActivityFeed({ selectedRepo }: { selectedRepo: string }) {
  const [events, setEvents] = useState<ActivityEvent[]>([]);
  const [state, setState] = useState<"idle" | "loading" | "ready" | "error">(
    "idle",
  );
  const [error, setError] = useState("");
  const [empty, setEmpty] = useState(false);

  const load = useCallback(async () => {
    if (!selectedRepo) return;
    setState("loading");
    setError("");
    try {
      const res = await listActivity(selectedRepo, { limit: 50 });
      setEvents(res.events);
      setEmpty(res.empty);
      setState("ready");
    } catch (e) {
      setError(String(e instanceof Error ? e.message : e));
      setState("error");
    }
  }, [selectedRepo]);

  // Auto-load when the selected repo changes (and clear when deselected).
  useEffect(() => {
    if (!selectedRepo) {
      setEvents([]);
      setEmpty(false);
      setState("idle");
      setError("");
      return;
    }
    void load();
  }, [selectedRepo, load]);

  if (!selectedRepo) return null;

  return (
    <section className="card" aria-labelledby="activity-heading">
      <div className="account-row">
        <h2 id="activity-heading">Recent activity</h2>
        <button
          className="link"
          disabled={state === "loading"}
          onClick={() => void load()}
        >
          {state === "loading" ? "Loading…" : "Refresh"}
        </button>
      </div>
      <p className="hint">
        Who changed what on <code>{selectedRepo}</code>’s board, newest first.
        This is a read-only audit feed — it never changes a task.
      </p>

      {state === "loading" && <p className="hint">Loading activity…</p>}
      {state === "error" && (
        <p role="alert" className="error">
          {error}
        </p>
      )}
      {state === "ready" && empty && (
        <p className="hint">
          No activity recorded yet. Activity appears here once tasks are created
          or moved on this board.
        </p>
      )}
      {state === "ready" && !empty && (
        <ul className="activity-list">
          {events.map((ev, i) => (
            <li key={`${ev.Timestamp}-${ev.BoardTaskID}-${i}`} className="activity-item">
              <span className={`activity-badge activity-${ev.Action}`}>
                {actionLabel(ev.Action)}
              </span>
              <span className="activity-body">
                <code>#{ev.BoardTaskID}</code> {ev.Detail}
              </span>
              <span className="activity-meta">
                {formatActor(ev.Actor)} · {formatWhen(ev.Timestamp)}
              </span>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

// actionLabel maps the server action strings to short human labels.
function actionLabel(action: string): string {
  switch (action) {
    case "created":
      return "created";
    case "status_changed":
      return "moved";
    case "priority_changed":
      return "priority";
    case "updated":
      return "edited";
    default:
      return action || "changed";
  }
}

// formatActor shows the email/sub as-is, but shortens an opaque UUID sub to a
// prefix so the line stays readable when email was unavailable.
function formatActor(actor: string): string {
  if (!actor) return "unknown";
  if (actor.includes("@")) return actor;
  // Looks like an opaque sub (UUID) — show a short prefix.
  if (/^[0-9a-f-]{20,}$/i.test(actor)) return `${actor.slice(0, 8)}…`;
  return actor;
}

// formatWhen renders an RFC3339 timestamp as a compact local time, falling back
// to the raw string if it does not parse.
function formatWhen(ts: string): string {
  const d = new Date(ts);
  if (Number.isNaN(d.getTime())) return ts;
  return d.toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
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

function agentPrompt(mcpApi: string, repo: string, setupUrl: string): string {
  const url = mcpApi || "https://<MCP_API>";
  return [
    `Set up the cainban MCP server as your task backend for the repo ${repo} by`,
    `following the instructions at ${setupUrl} — fetch that file and do what it`,
    `says. In short: register the MCP server at ${url} (OAuth is preferred — you`,
    `sign in once in the browser and tokens self-refresh; no token to paste),`,
    `target this repo with the header X-Cainban-Repo: ${repo}, then verify by`,
    `calling list_tasks. Once connected, use it as your task backend: list_tasks`,
    `before starting, create_task to break down the work, and update_task_status`,
    `(todo → doing → done) as you go. Start by listing the current tasks for`,
    `${repo}.`,
  ].join(" ");
}
