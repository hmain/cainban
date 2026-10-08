import { useCallback, useEffect, useReducer, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { listTasks, listLinks, type TaskLink } from "../mcpApi";
import {
  boardReducer,
  initialBoardState,
  agentTaskIds,
  workingTaskIds,
} from "../board/reducer";
import { PollAdapter } from "../board/pollAdapter";
import { STATUS_ORDER, type Status } from "../board/types";
import { Column } from "./Column";
import { ConnectionChip } from "./ConnectionChip";
import { DependencyOverlay } from "./DependencyOverlay";

type Gate =
  | { kind: "checking" }
  | { kind: "ok" }
  | { kind: "error"; code: 401 | 403 | "other"; message: string };

// Board renders a repo's live kanban board: a list_tasks snapshot into the
// board-state reducer, then the PollAdapter dispatches delta events (Stage D).
// An explicit first list_tasks gates the auth-error UX (401/403) before the
// adapter — which treats a failed snapshot as backoff — takes over.
export function Board({ owner, repo }: { owner: string; repo: string }) {
  const full = `${owner}/${repo}`;
  const [state, dispatch] = useReducer(boardReducer, undefined, initialBoardState);
  const [gate, setGate] = useState<Gate>({ kind: "checking" });
  const adapterRef = useRef<PollAdapter | null>(null);
  // A ref mirror of the cursor so the adapter reads the latest without a stale
  // closure.
  const cursorRef = useRef<string | null>(null);
  cursorRef.current = state.lastCursor;

  const begin = useCallback(async () => {
    setGate({ kind: "checking" });
    try {
      // Auth/connectivity probe + first snapshot in one call.
      await listTasks(full);
      setGate({ kind: "ok" });
    } catch (e) {
      const msg = String(e instanceof Error ? e.message : e);
      const code = msg.includes("(401)")
        ? 401
        : msg.includes("(403)")
          ? 403
          : "other";
      setGate({ kind: "error", code, message: msg });
    }
  }, [full]);

  // Gate (and re-gate when the repo changes).
  useEffect(() => {
    void begin();
  }, [begin]);

  // Once the gate is ok, start the poll adapter; tear it down on repo change /
  // unmount. The adapter owns its own snapshot + visibility + backoff.
  useEffect(() => {
    if (gate.kind !== "ok") return;
    const adapter = new PollAdapter(full, dispatch, () => cursorRef.current);
    adapterRef.current = adapter;
    void adapter.start();
    return () => {
      adapter.stop();
      adapterRef.current = null;
    };
  }, [gate.kind, full]);

  const agents = agentTaskIds(state);
  const working = workingTaskIds(state);

  // Dependency links (board-wide). Fetched out of the hot poll path: once the
  // gate is ok, on mount and on a slow interval, plus whenever the set of cards
  // changes (a new/removed card usually coincides with a link change). The
  // overlay re-anchors to card positions on its own via ResizeObserver, so this
  // only needs to supply fresh link DATA, not drive layout.
  const [links, setLinks] = useState<TaskLink[]>([]);
  const [showLinks, setShowLinks] = useState(true);
  const columnsRef = useRef<HTMLDivElement | null>(null);
  const taskKey = Object.keys(state.tasksById).sort().join(",");

  useEffect(() => {
    if (gate.kind !== "ok") return;
    let cancelled = false;
    const fetchLinks = async () => {
      try {
        const { links } = await listLinks(full);
        if (!cancelled) setLinks(links);
      } catch {
        // Non-fatal: leave the last-known links; the overlay just goes stale,
        // never blocks the board.
      }
    };
    void fetchLinks();
    const t = setInterval(() => void fetchLinks(), 8000);
    return () => {
      cancelled = true;
      clearInterval(t);
    };
    // Re-run (immediate refetch) when the gate opens, the repo changes, or the
    // card set changes.
  }, [gate.kind, full, taskKey]);

  const hasLinks = links.length > 0;

  return (
    <section className="board" aria-label={`Board for ${full}`}>
      <header className="board-head">
        <h2>{full} — board</h2>
        <div className="board-head-right">
          {hasLinks && (
            <button
              type="button"
              className="link dep-toggle"
              aria-pressed={showLinks}
              onClick={() => setShowLinks((v) => !v)}
              title="Toggle dependency lines"
            >
              {showLinks ? "Hide links" : "Show links"}
            </button>
          )}
          <ConnectionChip
            state={state.connectionState}
            degraded={state.degraded}
            onRetry={() => adapterRef.current?.retry()}
          />
          <Link className="link" to={`/r/${full}/activity`}>
            Activity
          </Link>
        </div>
      </header>

      {gate.kind === "checking" && <p className="hint">Loading board…</p>}

      {gate.kind === "error" && (
        <BoardError state={gate} onRetry={() => void begin()} />
      )}

      {gate.kind === "ok" && (
        <>
          {hasLinks && showLinks && <DepLegend />}
          <div className="board-columns" ref={columnsRef}>
            {STATUS_ORDER.map((s: Status) => (
              <Column
                key={s}
                status={s}
                tasks={state.columns[s].map((id) => state.tasksById[id]).filter(Boolean)}
                agentTasks={agents}
                workingTasks={working}
              />
            ))}
            <DependencyOverlay
              containerRef={columnsRef}
              links={links}
              visible={showLinks}
            />
          </div>
        </>
      )}
    </section>
  );
}

// DepLegend explains the line styles. Terse by design: one row of swatches.
function DepLegend() {
  return (
    <div className="dep-legend" aria-label="Dependency line legend">
      <span className="dep-legend-item">
        <span className="dep-swatch dep-swatch-block" /> blocks
      </span>
      <span className="dep-legend-item">
        <span className="dep-swatch dep-swatch-dep" /> depends on
      </span>
      <span className="dep-legend-item">
        <span className="dep-swatch dep-swatch-related" /> related
      </span>
    </div>
  );
}

function BoardError({
  state,
  onRetry,
}: {
  state: { code: 401 | 403 | "other"; message: string };
  onRetry: () => void;
}) {
  if (state.code === 401) {
    return (
      <p role="alert" className="error">
        Your session expired.{" "}
        <button className="link" onClick={() => window.location.reload()}>
          Reload
        </button>{" "}
        to sign in again.
      </p>
    );
  }
  if (state.code === 403) {
    return (
      <p role="alert" className="error">
        You’re not authorized for this repo.{" "}
        <Link className="link" to="/connect">
          Connect it first
        </Link>
        .
      </p>
    );
  }
  return (
    <p role="alert" className="error">
      Couldn’t load the board: {state.message}{" "}
      <button className="link" onClick={onRetry}>
        Retry
      </button>
    </p>
  );
}
