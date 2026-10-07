import { useCallback, useEffect, useState } from "react";
import {
  Navigate,
  Route,
  Routes,
  useParams,
  useLocation,
  Link,
} from "react-router-dom";
import { signInWithRedirect, signOut, fetchAuthSession } from "aws-amplify/auth";
import { Hub } from "aws-amplify/utils";
import { ENTRA_PROVIDER_NAME } from "./amplify";
import { Board } from "./components/Board";
import { ActivityFeed } from "./components/ActivityFeed";
import { ConnectPage } from "./pages/ConnectPage";
import { RepoRail } from "./shell/RepoRail";
import { NotConnected } from "./shell/NotConnected";
import {
  useConnectedRepos,
  lastViewedRepo,
  rememberLastRepo,
  type ReposState,
} from "./shell/useConnectedRepos";

type AuthState = "loading" | "signed-out" | "signed-in";

export function App() {
  const [authState, setAuthState] = useState<AuthState>("loading");
  const [email, setEmail] = useState<string>("");

  const refreshUser = useCallback(async () => {
    try {
      // Read the session + email from the ID TOKEN, not fetchUserAttributes()
      // (which needs the cognito user.admin scope the SPA doesn't request). The
      // email is a claim in the ID token (email scope + Entra attribute map).
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

  if (authState === "signed-out") {
    return (
      <main className="container">
        <header>
          <h1>cainban</h1>
          <p className="subtitle">Watch your repo’s board move as agents work it.</p>
        </header>
        <SignedOut />
      </main>
    );
  }

  return <SignedInShell email={email} />;
}

function SignedOut() {
  const [error, setError] = useState<string>("");
  const onSignIn = async () => {
    setError("");
    try {
      await signInWithRedirect({ provider: { custom: ENTRA_PROVIDER_NAME } });
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
      {error && (
        <p role="alert" className="error">
          {error}
        </p>
      )}
    </section>
  );
}

// SignedInShell owns the repo-scoped layout: a header, a persistent repo rail,
// and the routed main area. The connected-repo set is loaded once here and
// shared with the rail, the `/` redirect, and the board's not-connected guard.
function SignedInShell({ email }: { email: string }) {
  const repos = useConnectedRepos();
  return (
    <div className="app-shell">
      <header className="app-header">
        <Link className="brand" to="/">
          cainban
        </Link>
        <div className="app-header-right">
          <Link className="link" to="/connect">
            Connect
          </Link>
          <span className="muted">{email}</span>
          <button className="link" onClick={() => void signOut()}>
            Sign out
          </button>
        </div>
      </header>

      <div className="app-body">
        <RepoRail repos={repos} current={useCurrentRepo()} />
        <main className="app-main container">
          <Routes>
            <Route path="/" element={<HomeRedirect repos={repos} />} />
            <Route path="/r/:owner/:repo" element={<BoardRoute repos={repos} />} />
            <Route
              path="/r/:owner/:repo/activity"
              element={<ActivityRoute repos={repos} />}
            />
            <Route path="/connect" element={<ConnectPage />} />
            <Route path="*" element={<Navigate to="/" replace />} />
          </Routes>
        </main>
      </div>
    </div>
  );
}

// useCurrentRepo reads owner/repo from the active pathname (reactively, via
// useLocation) so the rail's selected marker updates on navigation. Returns
// null when not on a board route.
function useCurrentRepo(): string | null {
  const { pathname } = useLocation();
  const m = pathname.match(/^\/r\/([^/]+)\/([^/]+)/);
  return m ? `${decodeURIComponent(m[1])}/${decodeURIComponent(m[2])}` : null;
}

// HomeRedirect implements the `/` rule: with repos, go to the last-viewed board
// (if still connected) else the first repo; with none, go to Connect.
function HomeRedirect({ repos }: { repos: ReposState }) {
  if (repos.status === "loading") return <p className="hint">Loading…</p>;
  if (repos.status === "error") {
    return (
      <p role="alert" className="error">
        Couldn’t load your repos.
      </p>
    );
  }
  if (repos.repos.length === 0) return <Navigate to="/connect" replace />;
  const last = lastViewedRepo();
  const target =
    last && repos.repos.includes(last) ? last : repos.repos[0];
  return <Navigate to={`/r/${target}`} replace />;
}

function isConnected(repos: ReposState, full: string): boolean {
  return repos.status === "ready" && repos.repos.includes(full);
}

function BoardRoute({ repos }: { repos: ReposState }) {
  const { owner, repo } = useParams();
  const full = owner && repo ? `${owner}/${repo}` : "";
  // Hook must run unconditionally (before any early return). A "" full is a
  // harmless no-op write guarded inside the effect.
  useRememberRepo(full);
  if (!owner || !repo) return <Navigate to="/" replace />;
  if (repos.status === "loading") return <p className="hint">Loading board…</p>;
  if (!isConnected(repos, full)) return <NotConnected repo={full} />;
  return <Board owner={owner} repo={repo} />;
}

function ActivityRoute({ repos }: { repos: ReposState }) {
  const { owner, repo } = useParams();
  if (!owner || !repo) return <Navigate to="/" replace />;
  const full = `${owner}/${repo}`;
  if (repos.status === "loading") return <p className="hint">Loading…</p>;
  if (!isConnected(repos, full)) return <NotConnected repo={full} />;
  return (
    <section className="activity-route">
      <div className="account-row">
        <h2>{full} — activity</h2>
        <Link className="link" to={`/r/${full}`}>
          ← Back to board
        </Link>
      </div>
      <ActivityFeed repo={full} />
    </section>
  );
}

// useRememberRepo persists the last-viewed board for the `/` redirect.
function useRememberRepo(full: string) {
  useEffect(() => {
    if (full) rememberLastRepo(full);
  }, [full]);
}
