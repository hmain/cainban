import { useCallback, useEffect, useState } from "react";
import {
  signInWithRedirect,
  signOut,
  getCurrentUser,
  fetchUserAttributes,
} from "aws-amplify/auth";
import { Hub } from "aws-amplify/utils";
import { ENTRA_PROVIDER_NAME, MCP_API } from "./amplify";
import {
  connectRepo,
  listRepos,
  linkGitHubIdentity,
  listAvailableRepos,
  getAppInstallUrl,
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
      <ChooseRepo />
      <ConnectRepo />
      <MyRepos />
      <McpConfig />
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

function ChooseRepo() {
  const [repos, setRepos] = useState<AvailableRepo[]>([]);
  const [login, setLogin] = useState<string | null>(null);
  const [state, setState] = useState<"loading" | "list" | "manual-only">(
    "loading",
  );
  const [notice, setNotice] = useState("");
  const [installUrl, setInstallUrl] = useState("");
  const [busyRepo, setBusyRepo] = useState<string | null>(null);
  const [msg, setMsg] = useState<{ kind: "ok" | "err"; text: string } | null>(
    null,
  );

  const load = useCallback(async () => {
    setState("loading");
    setNotice("");
    setMsg(null);
    const r = await listAvailableRepos();
    setLogin(r.githubLogin);
    if (!r.ok) {
      // FAIL CLOSED: never show an empty list as "no repos". Fall back to the
      // manual entry below with the reason.
      setState("manual-only");
      setNotice(
        r.needsLink
          ? "Link your GitHub identity first, then reload to list your repos."
          : `Couldn't load your repos (${r.error ?? "unknown error"}) — enter one manually below.`,
      );
      return;
    }
    if (r.noInstallation) {
      setState("manual-only");
      setNotice(
        "The cainban GitHub App isn't installed on any account you can access yet — install it below, or enter a repo manually.",
      );
      // Offer a one-click install: fetch the App install URL (with a signed
      // state) so the user can install and be returned here.
      setInstallUrl(await getAppInstallUrl());
      return;
    }
    setRepos(r.repos);
    setState("list");
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const onPick = async (fullName: string) => {
    const slash = fullName.indexOf("/");
    if (slash <= 0 || slash === fullName.length - 1) {
      setMsg({ kind: "err", text: `Unexpected repo name: ${fullName}` });
      return;
    }
    const owner = fullName.slice(0, slash);
    const repo = fullName.slice(slash + 1);
    setBusyRepo(fullName);
    setMsg(null);
    try {
      const res = await connectRepo(owner, repo);
      if (res.status === 200) {
        setMsg({ kind: "ok", text: `Connected ${res.granted}.` });
        // Reflect the new granted state in the list.
        setRepos((prev) =>
          prev.map((p) =>
            p.full_name === fullName ? { ...p, already_granted: true } : p,
          ),
        );
      } else if (res.status === 403) {
        setMsg({
          kind: "err",
          text: `GitHub access to ${fullName} was not verified for your account.`,
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

  return (
    <section className="card" aria-labelledby="choose-heading">
      <div className="account-row">
        <h2 id="choose-heading">Choose a repo</h2>
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
        . Pick one to connect it.
      </p>

      {state === "loading" && <p className="hint">Loading your repos…</p>}

      {notice && (
        <p role="status" className="hint">
          {notice}
        </p>
      )}

      {installUrl && (
        <p>
          <a className="btn" href={installUrl}>
            Install the cainban GitHub App
          </a>
        </p>
      )}

      {state === "list" &&
        (repos.length === 0 ? (
          <p className="hint">
            No repos found for this installation — enter one manually below.
          </p>
        ) : (
          <ul className="repo-list">
            {repos.map((r) => (
              <li key={r.full_name} className="account-row">
                <code>{r.full_name}</code>
                {r.already_granted ? (
                  <span className="hint">Connected</span>
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
        ))}

      {msg && (
        <p
          role={msg.kind === "err" ? "alert" : "status"}
          className={msg.kind === "err" ? "error" : "ok"}
        >
          {msg.text}
        </p>
      )}
    </section>
  );
}

function ConnectRepo() {
  const [owner, setOwner] = useState("");
  const [repo, setRepo] = useState("");
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ kind: "ok" | "err"; text: string } | null>(
    null,
  );

  const onSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setMsg(null);
    try {
      const r = await connectRepo(owner.trim(), repo.trim());
      if (r.status === 200) {
        setMsg({ kind: "ok", text: `Connected ${r.granted}.` });
      } else if (r.status === 403) {
        setMsg({
          kind: "err",
          text: "GitHub access to that repo was not verified for your account.",
        });
      } else if (r.status === 409) {
        setMsg({
          kind: "err",
          text: "No linked GitHub identity yet — use “Link GitHub identity” first.",
        });
      } else {
        setMsg({ kind: "err", text: r.error || `Request failed (${r.status}).` });
      }
    } catch (err) {
      setMsg({ kind: "err", text: String(err) });
    } finally {
      setBusy(false);
    }
  };

  return (
    <section className="card" aria-labelledby="connect-heading">
      <h2 id="connect-heading">Or enter a repo manually</h2>
      <p className="hint">
        For a repo the App isn’t installed on yet, or if the list above didn’t
        load. Access is still verified before it’s connected.
      </p>
      <form onSubmit={(e) => void onSubmit(e)} className="repo-form">
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
            value={repo}
            onChange={(e) => setRepo(e.target.value)}
            placeholder="hello-world"
            autoComplete="off"
            required
          />
        </label>
        <button className="primary" disabled={busy} type="submit">
          {busy ? "Connecting…" : "Connect repo"}
        </button>
      </form>
      {msg && (
        <p role={msg.kind === "err" ? "alert" : "status"} className={msg.kind === "err" ? "error" : "ok"}>
          {msg.text}
        </p>
      )}
    </section>
  );
}

function MyRepos() {
  const [repos, setRepos] = useState<string[]>([]);
  const [login, setLogin] = useState<string | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  const refresh = async () => {
    setBusy(true);
    setError("");
    try {
      const r = await listRepos();
      setRepos(r.repos);
      setLogin(r.github_login);
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy(false);
    }
  };

  useEffect(() => {
    void refresh();
  }, []);

  return (
    <section className="card" aria-labelledby="repos-heading">
      <div className="account-row">
        <h2 id="repos-heading">My connected repos</h2>
        <button className="link" disabled={busy} onClick={() => void refresh()}>
          {busy ? "Refreshing…" : "Refresh"}
        </button>
      </div>
      {login && (
        <p className="hint">
          Linked GitHub identity: <strong>{login}</strong>
        </p>
      )}
      {repos.length === 0 ? (
        <p className="hint">No repos connected yet.</p>
      ) : (
        <ul className="repo-list">
          {repos.map((r) => (
            <li key={r}>
              <code>{r}</code>
            </li>
          ))}
        </ul>
      )}
      {error && <p role="alert" className="error">{error}</p>}
    </section>
  );
}

function McpConfig() {
  const [copied, setCopied] = useState(false);
  const config = mcpConfigSnippet(MCP_API);
  const onCopy = async () => {
    try {
      await navigator.clipboard.writeText(config);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // Clipboard may be unavailable (insecure context); the block is
      // selectable regardless.
    }
  };
  return (
    <section className="card" aria-labelledby="mcp-heading">
      <div className="account-row">
        <h2 id="mcp-heading">MCP client config</h2>
        <button className="link" onClick={() => void onCopy()}>
          {copied ? "Copied!" : "Copy"}
        </button>
      </div>
      <p className="hint">
        Point your agent at the MCP endpoint below. Replace{" "}
        <code>&lt;ID_TOKEN&gt;</code> with a current Cognito ID token and{" "}
        <code>owner/repo</code> with a repo you connected above.
      </p>
      <pre className="code-block">
        <code>{config}</code>
      </pre>
    </section>
  );
}

function mcpConfigSnippet(mcpApi: string): string {
  const url = mcpApi || "https://<MCP_API>";
  return JSON.stringify(
    {
      mcpServers: {
        cainban: {
          url,
          headers: {
            Authorization: "Bearer <ID_TOKEN>",
            "X-Cainban-Repo": "owner/repo",
          },
        },
      },
    },
    null,
    2,
  );
}
