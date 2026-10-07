import { Link } from "react-router-dom";

// NotConnected is shown when the board route names a repo that is not in the
// user's connected set — a clear "connect it first" state rather than a blank
// board or an unhandled error.
export function NotConnected({ repo }: { repo: string }) {
  return (
    <section className="card not-connected" aria-labelledby="nc-heading">
      <h2 id="nc-heading">Not connected</h2>
      <p className="hint">
        <code>{repo}</code> isn’t connected to your account yet, so there’s no
        board to show. Connect it first, then open its board.
      </p>
      <Link className="primary" to="/connect">
        Go to Connect
      </Link>
    </section>
  );
}
