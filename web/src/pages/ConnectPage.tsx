import { useCallback, useEffect, useState } from "react";
import { Link } from "react-router-dom";
import {
  MCP_API,
  MCP_CLI_CLIENT_ID,
  MCP_OAUTH_CALLBACK_PORT,
  MCP_OAUTH_REDIRECT_URI,
} from "../amplify";
import {
  connectRepo,
  disconnectRepo,
  refreshIdToken,
  listRepos,
  linkGitHubIdentity,
  listAvailableRepos,
  getAppInstallUrl,
  getIdToken,
  type AvailableRepo,
} from "../connectApi";

// ConnectPage is the connect/config surface (demoted from the SPA home to
// /connect in the Phase 1 board rework): link a GitHub identity, connect a repo,
// and copy the MCP client setup. It is the former SignedIn connect sections,
// moved verbatim; the repo rail / board shell now owns navigation.
export function ConnectPage() {
  return (
    <section className="connect-page">
      <div className="account-row">
        <h2>Connect a repository</h2>
        <Link className="link" to="/">
          ← Back to board
        </Link>
      </div>
      <LinkIdentity />
      <Repos />
      <McpConfig />
    </section>
  );
}

function LinkIdentity() {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const onLink = async () => {
    setBusy(true);
    setError("");
    try {
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
      {error && (
        <p role="alert" className="error">
          {error}
        </p>
      )}
    </section>
  );
}

// Repos lists the repos the App can access (connected or connectable), lets the
// user connect one, offers manual entry, and always exposes an install link.
// Selection no longer drives config via a prop — a connected repo is reached by
// its board URL; this page connects repos and shows the MCP config for the
// first connected repo.
function Repos() {
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
  // Disconnect flow: which repo is mid-confirm, and which is mid-DELETE.
  const [confirmingRemove, setConfirmingRemove] = useState<string | null>(null);
  const [disconnecting, setDisconnecting] = useState<string | null>(null);

  const load = useCallback(async () => {
    setState("loading");
    setNotice("");
    setMsg(null);
    setInstallUrl(await getAppInstallUrl());
    const [avail, granted] = await Promise.all([
      listAvailableRepos(),
      listRepos().catch(() => ({ repos: [], github_login: null })),
    ]);
    setLogin(avail.githubLogin ?? granted.github_login ?? null);

    const map = new Map<string, AvailableRepo>();
    if (avail.ok) {
      for (const r of avail.repos) map.set(r.full_name, { ...r });
    }
    for (const full of granted.repos) {
      map.set(full, { full_name: full, already_granted: true });
    }
    setRepos(
      [...map.values()].sort((a, b) => a.full_name.localeCompare(b.full_name)),
    );

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
    setState("ready");
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  // Refresh when the page regains visibility or focus. The grant set can change
  // on ANOTHER screen (the repo rail's Remove, or a second tab), leaving this
  // list's `already_granted` flags stale — a removed repo would still read
  // "Connected" until a manual Reload. Reloading on visibilitychange/focus
  // makes navigating back to /connect (or refocusing the tab) show live state.
  // The custom event covers the in-page case: the rail is always mounted, so a
  // removal while already on /connect fires no visibility/focus change.
  useEffect(() => {
    const onVisible = () => {
      if (document.visibilityState === "visible") void load();
    };
    const onFocus = () => void load();
    const onReposChanged = () => void load();
    document.addEventListener("visibilitychange", onVisible);
    window.addEventListener("focus", onFocus);
    window.addEventListener("cainban:repos-changed", onReposChanged);
    return () => {
      document.removeEventListener("visibilitychange", onVisible);
      window.removeEventListener("focus", onFocus);
      window.removeEventListener("cainban:repos-changed", onReposChanged);
    };
  }, [load]);

  const doConnect = async (owner: string, repo: string, key: string) => {
    setBusyRepo(key);
    setMsg(null);
    try {
      const res = await connectRepo(owner, repo);
      const full = `${owner}/${repo}`;
      if (res.status === 200) {
        // Re-mint the ID token so its `repos` claim includes the just-granted
        // repo; otherwise the board authorizes against the stale pre-connect
        // claim and 403s ("not authorized — connect it first") until the token
        // refreshes on its own.
        await refreshIdToken();
        setMsg({ kind: "ok", text: `Connected ${res.granted}.` });
        setRepos((prev) => {
          const next = prev.some((p) => p.full_name === (res.granted ?? full))
            ? prev.map((p) =>
                p.full_name === (res.granted ?? full)
                  ? { ...p, already_granted: true }
                  : p,
              )
            : [
                ...prev,
                { full_name: res.granted ?? full, already_granted: true },
              ];
          return next.sort((a, b) => a.full_name.localeCompare(b.full_name));
        });
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
        setMsg({
          kind: "err",
          text: res.error || `Request failed (${res.status}).`,
        });
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
    return doConnect(
      fullName.slice(0, slash),
      fullName.slice(slash + 1),
      fullName,
    );
  };

  const onManual = async (e: React.FormEvent) => {
    e.preventDefault();
    setManualBusy(true);
    await doConnect(owner.trim(), manualRepo.trim(), "__manual__");
    setManualBusy(false);
  };

  // doDisconnect revokes THIS user's grant for a repo (DELETE /connect/repo).
  // Per-user only: the shared board/tasks are kept and the repo stays listed as
  // connectable (the App can still access it). Mirrors the rail's Remove and
  // broadcasts cainban:repos-changed so the rail refreshes in lock-step.
  const doDisconnect = async (fullName: string) => {
    const slash = fullName.indexOf("/");
    if (slash <= 0 || slash === fullName.length - 1) return;
    const owner = fullName.slice(0, slash);
    const repo = fullName.slice(slash + 1);
    setDisconnecting(fullName);
    setMsg(null);
    try {
      const res = await disconnectRepo(owner, repo);
      if (res.status !== 200) {
        setMsg({
          kind: "err",
          text: res.error || `Couldn’t disconnect ${fullName} (${res.status}).`,
        });
        setDisconnecting(null);
        return;
      }
      // Flag the row as no longer granted (keep it listed — the App can still
      // access it, so it stays connectable).
      setRepos((prev) =>
        prev.map((p) =>
          p.full_name === fullName ? { ...p, already_granted: false } : p,
        ),
      );
      setMsg({ kind: "ok", text: `Disconnected ${res.revoked ?? fullName}.` });
      setConfirmingRemove(null);
      setDisconnecting(null);
      // Re-mint the token so its repos claim drops the removed repo, and keep
      // the rail in sync.
      await refreshIdToken();
      window.dispatchEvent(new CustomEvent("cainban:repos-changed"));
    } catch (err) {
      setMsg({ kind: "err", text: String(err) });
      setDisconnecting(null);
    }
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
        . Connect one, then open its board. A<strong> Connected</strong> repo is
        ready to use.
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
              <code>{r.full_name}</code>
              {r.already_granted ? (
                <span className="repo-list-connected">
                  <span className="ok">Connected</span>
                  {confirmingRemove === r.full_name ? (
                    <span className="repo-rail-confirm">
                      <span className="repo-rail-confirm-q">Disconnect?</span>
                      <button
                        type="button"
                        className="link repo-rail-confirm-yes"
                        disabled={disconnecting === r.full_name}
                        onClick={() => void doDisconnect(r.full_name)}
                        title="Removes your access only; the board and its tasks stay for anyone else and return if you re-connect"
                      >
                        {disconnecting === r.full_name ? "Disconnecting…" : "Yes"}
                      </button>
                      <button
                        type="button"
                        className="link repo-rail-confirm-no"
                        disabled={disconnecting === r.full_name}
                        onClick={() => setConfirmingRemove(null)}
                      >
                        No
                      </button>
                    </span>
                  ) : (
                    <button
                      type="button"
                      className="link repo-list-disconnect"
                      onClick={() => {
                        setConfirmingRemove(r.full_name);
                        setMsg(null);
                      }}
                      title={`Disconnect ${r.full_name} (your access only)`}
                    >
                      Disconnect
                    </button>
                  )}
                </span>
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
          </a>{" "}
          — if you installed it on only some repos, you can add others.
        </p>
      )}
    </section>
  );
}

function McpConfig() {
  const clientId = MCP_CLI_CLIENT_ID || "<McpCliClientId>";
  const [copied, setCopied] = useState<string>("");

  const addCmd = claudeAddCommand(MCP_API, clientId);
  const oauthConfig = mcpOAuthConfigSnippet(MCP_API, clientId);
  const setupUrl =
    typeof window !== "undefined"
      ? `${window.location.origin}/cainban-mcp-setup.md`
      : "/cainban-mcp-setup.md";
  const prompt = agentPrompt(MCP_API, clientId, "owner/repo", setupUrl);
  const rule = steeringRule("owner/repo", setupUrl);

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
            The single granted repo is selected by your token’s{" "}
            <code>default_repo</code> claim, so no <code>X-Cainban-Repo</code>{" "}
            header is needed. To use a second repo in the same client, pin it
            with a <code>X-Cainban-Repo</code> header (see the setup guide’s
            multi-repo section).
          </p>
        </details>

        <BearerTokenFallback repo="owner/repo" />
      </section>

      <section className="card" aria-labelledby="prompt-heading">
        <div className="account-row">
          <h2 id="prompt-heading">Set up your AI agent</h2>
          <button className="link" onClick={() => void copy(prompt, "prompt")}>
            {copied === "prompt" ? "Copied!" : "Copy"}
          </button>
        </div>
        <p className="hint">
          Paste this to your AI coding agent. It points the agent at this repo’s
          cainban board over MCP <strong>and</strong> installs a steering rule in
          the project, so the agent uses cainban for task management
          automatically — you won’t have to tell it “use cainban” each time. It
          carries everything the agent needs (server URL, OAuth client id) and
          writes the rule non-destructively (idempotent markers, project rules
          win).
        </p>
        <pre className="code-block">
          <code>{prompt}</code>
        </pre>

        <details className="manual-entry">
          <summary>Just the steering rule (add it yourself)</summary>
          <p className="hint">
            The rule the prompt installs. Drop it into your tool’s agent-rules
            file so cainban is used automatically:{" "}
            <code>CLAUDE.md</code> / <code>AGENTS.md</code> (project root),{" "}
            <code>.kiro/steering/cainban.md</code>, or{" "}
            <code>.cursor/rules/cainban.mdc</code>.
          </p>
          <div className="account-row">
            <span className="muted">Steering rule:</span>
            <button
              className="link"
              onClick={() => void copy(rule, "rule")}
            >
              {copied === "rule" ? "Copied!" : "Copy"}
            </button>
          </div>
          <pre className="code-block">
            <code>{rule}</code>
          </pre>
        </details>

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
        if ((e.target as HTMLDetailsElement).open && !loaded) void loadToken();
      }}
    >
      <summary>Client can’t do OAuth? Use a bearer token (advanced)</summary>
      <p className="hint">
        For a client that connects by URL + headers only and cannot run OAuth.
        This ID token expires (~1h) — regenerate it (Refresh) whenever your
        agent starts failing with 401. Prefer the OAuth setup above.
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
      {tokenErr && (
        <p role="alert" className="error">
          {tokenErr}
        </p>
      )}
    </details>
  );
}

function claudeAddCommand(mcpApi: string, clientId: string): string {
  const url = mcpApi || "https://<MCP_API>";
  return `claude mcp add --transport http --client-id ${clientId} cainban ${url}`;
}

function mcpOAuthConfigSnippet(mcpApi: string, clientId: string): string {
  const url = mcpApi || "https://<MCP_API>";
  return JSON.stringify(
    {
      mcpServers: {
        cainban: {
          type: "http",
          url,
          client_id: clientId,
          oauth: {
            clientId,
            redirectUri: MCP_OAUTH_REDIRECT_URI,
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

function agentPrompt(
  mcpApi: string,
  clientId: string,
  repo: string,
  setupUrl: string,
): string {
  const url = mcpApi || "https://<MCP_API>";
  const cid = clientId || "<CLIENT_ID>";
  const redirectUri = MCP_OAUTH_REDIRECT_URI;
  return [
    `Set up the cainban MCP server as your task backend for ${repo}, install a`,
    `steering rule so you use it automatically from now on, then start using it.`,
    `Everything you need is here — don't ask me for a token or an id.`,
    ``,
    `## 1. Register the MCP server`,
    ``,
    `Register this remote MCP server with your client (prefer OAuth: you sign in`,
    `once in the browser and tokens self-refresh — there is no token to paste):`,
    ``,
    `  URL:        ${url}`,
    `  OAuth client_id: ${cid}   (public, not a secret — safe to put in config)`,
    ``,
    `The repo is selected by your token's default_repo claim (set when ${repo} was`,
    `connected), so NO X-Cainban-Repo header is needed — don't add one or go`,
    `looking for one.`,
    ``,
    `For Claude Code that is:`,
    `  claude mcp add --transport http --client-id ${cid} cainban ${url}`,
    `For a JSON-config client (Kiro, Cursor, VS Code), add under mcpServers —`,
    `pin oauth.redirectUri or the browser sign-in fails with redirect_mismatch`,
    `(Kiro picks a random callback port otherwise, which Cognito rejects):`,
    `  "cainban": { "type": "http", "url": "${url}", "client_id": "${cid}",`,
    `    "oauth": { "clientId": "${cid}", "redirectUri": "${redirectUri}" } }`,
    `If you register through the Kiro Crew dashboard's Add-Custom-Server paste box,`,
    `use "clientId" (camelCase) and omit "type" — that surface rejects "type" and`,
    `"client_id" with "unknown spec key":`,
    `  "cainban": { "url": "${url}", "clientId": "${cid}",`,
    `    "oauth": { "clientId": "${cid}", "redirectUri": "${redirectUri}" } }`,
    ``,
    `Then authenticate (the browser sign-in is the one step I have to do myself —`,
    `pause and let me complete it), and note that a newly-added MCP server usually`,
    `only loads in a NEW session, so verify in a fresh chat. Verify by calling`,
    `list_tasks for ${repo} — an empty board is success; a 403 means my account`,
    `isn't granted this repo yet, so tell me to connect it on the connect page.`,
    ``,
    `## 2. Install a steering rule so you use cainban automatically`,
    ``,
    `So I never have to tell you "use cainban" again, write the rule below into`,
    `THIS project's agent-rules file. Pick the file for your tool:`,
    ``,
    `  Claude Code -> CLAUDE.md (project root)`,
    `  Codex / generic -> AGENTS.md (project root)`,
    `  Kiro -> .kiro/steering/cainban.md`,
    `  Cursor -> .cursor/rules/cainban.mdc`,
    ``,
    `Write it NON-DESTRUCTIVELY: these files hold the project's own instructions`,
    `and are often committed. If the file doesn't exist, create it (and its`,
    `directory). If it exists, APPEND the rule between the idempotent markers`,
    `below rather than overwriting; if those markers are already present, replace`,
    `only what's between them so re-running this doesn't duplicate the rule. Where`,
    `the project's own instructions conflict with this rule, the project wins.`,
    ``,
    `<<<CAINBAN_STEERING_START>>>`,
    ...steeringRule(repo, setupUrl).split("\n"),
    `<<<CAINBAN_STEERING_END>>>`,
    ``,
    `## 3. Start using it`,
    ``,
    `Once the server is connected and the rule is written, follow the rule from`,
    `now on: list_tasks before you start, create_task to break work down,`,
    `update_task_status (todo -> doing -> done) as you go. Full reference if you`,
    `need it: ${setupUrl}`,
  ].join("\n");
}

// steeringRule returns the markdown rule block an agent writes into the
// project's rules file (CLAUDE.md / AGENTS.md / .kiro/steering / .cursor/rules)
// so it uses cainban as the task backend automatically, without the user having
// to name it each time. Kept terse and tool-agnostic; the repo and setup-guide
// link are interpolated.
function steeringRule(repo: string, setupUrl: string): string {
  return [
    `# Task management: use cainban`,
    ``,
    `This project tracks work on a **cainban** board (a per-repo kanban board`,
    `exposed over MCP) for \`${repo}\`. Treat cainban as the source of truth for`,
    `what needs doing and what is in progress — use it automatically, without`,
    `being asked.`,
    ``,
    `**At the start of any work session**, call \`list_tasks\` to see the board`,
    `before deciding what to do.`,
    ``,
    `**When given work**, break it into tasks with \`create_task\` (one task per`,
    `cohesive unit), and set a priority when it is obvious.`,
    ``,
    `**As you work**, keep the board honest with \`update_task_status\`: move a`,
    `task to \`doing\` when you start it and \`done\` when it is complete and`,
    `verified. Use \`link_tasks\` to record a blocks / depends_on relationship`,
    `when one task gates another.`,
    ``,
    `**Do not** ask the user which tool to use for tasks, and do not keep a`,
    `separate TODO list in chat or scratch files — the board is it. The repo is`,
    `selected by your token, so you never need an X-Cainban-Repo header for`,
    `\`${repo}\`.`,
    ``,
    `Setup / tool reference: ${setupUrl}`,
  ].join("\n");
}
