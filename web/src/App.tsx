import { Route, Routes, Navigate, Link } from "react-router";
import { ThemeToggle } from "./components/ThemeToggle";
import { GitHubConnectPage } from "./pages/GitHubConnectPage";
import { BadgesPage } from "./pages/BadgesPage";

/**
 * Cainban SPA - GitHub-only authentication
 *
 * This SPA exists for ONE purpose: help users get their MCP config.
 * The board viewing functionality has been removed since MCP clients
 * (Claude, Cursor, VS Code) are the primary interface.
 *
 * Routes:
 * - / (home) → redirects to /connect
 * - /connect → GitHub device flow to get MCP config
 * - /badges → README badge generator
 */

export function App() {
  return (
    <div className="app-shell simple">
      <header className="app-header">
        <Link className="brand" to="/">
          cainban
        </Link>
        <div className="app-header-right">
          <Link className="link" to="/connect">
            Connect
          </Link>
          <Link className="link" to="/badges">
            Badges
          </Link>
          <ThemeToggle />
        </div>
      </header>

      <main className="app-main container">
        <Routes>
          <Route path="/" element={<Navigate to="/connect" replace />} />
          <Route path="/connect" element={<GitHubConnectPage />} />
          <Route path="/connect/github" element={<Navigate to="/connect" replace />} />
          <Route path="/badges" element={<BadgesPage />} />
          <Route path="*" element={<NotFound />} />
        </Routes>
      </main>

      <footer className="app-footer">
        <p className="hint">
          cainban is an MCP server for kanban boards. Connect your AI agent to manage tasks.
        </p>
      </footer>
    </div>
  );
}

function NotFound() {
  return (
    <section className="card">
      <h2>Page not found</h2>
      <p>The page you're looking for doesn't exist.</p>
      <Link to="/connect" className="btn primary">
        Go to Connect
      </Link>
    </section>
  );
}
