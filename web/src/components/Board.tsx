import { useCallback, useEffect, useMemo, useReducer, useRef, useState } from "react";
import { Link } from "react-router";
import { listTasks, listLinks, type Task, type TaskLink } from "../mcpApi";
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
import { TaskDetail } from "./TaskDetail";

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

  // Item 1: the clicked task opens a read-only detail modal. We hold the id (not
  // the task object) so a live update to that task while the modal is open is
  // reflected from the current board state.
  const [openId, setOpenId] = useState<number | null>(null);
  const openTask = openId != null ? state.tasksById[openId] ?? null : null;
  // If the open task is deleted live, close the modal.
  useEffect(() => {
    if (openId != null && !state.tasksById[openId]) setOpenId(null);
  }, [openId, state.tasksById]);

  // Item 3: client-side filtering over the already-loaded board. A text query
  // (id or title), a minimum priority, and an "agent-touched only" toggle. All
  // derived — no refetch.
  const [query, setQuery] = useState("");
  const [minPriority, setMinPriority] = useState(0);
  const [agentOnly, setAgentOnly] = useState(false);
  const filterActive = query.trim() !== "" || minPriority > 0 || agentOnly;

  const matches = useCallback(
    (t: Task): boolean => {
      if (minPriority > 0 && t.Priority < minPriority) return false;
      if (agentOnly && !agents.has(t.BoardTaskID)) return false;
      const q = query.trim().toLowerCase();
      if (q) {
        const hay = `#${t.BoardTaskID} ${t.Title}`.toLowerCase();
        if (!hay.includes(q)) return false;
      }
      return true;
    },
    [query, minPriority, agentOnly, agents],
  );

  // Total non-filtered card count, to drive the empty-state (item 4) vs the
  // "no matches" filtered-empty message.
  const totalCards = Object.keys(state.tasksById).length;
  const visibleCount = useMemo(
    () =>
      Object.values(state.tasksById).filter((t) => matches(t)).length,
    [state.tasksById, matches],
  );

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
          {totalCards === 0 ? (
            <BoardEmpty />
          ) : (
            <>
              <FilterBar
                query={query}
                setQuery={setQuery}
                minPriority={minPriority}
                setMinPriority={setMinPriority}
                agentOnly={agentOnly}
                setAgentOnly={setAgentOnly}
                active={filterActive}
                visibleCount={visibleCount}
                totalCards={totalCards}
                onClear={() => {
                  setQuery("");
                  setMinPriority(0);
                  setAgentOnly(false);
                }}
              />
              {hasLinks && showLinks && !filterActive && <DepLegend />}
              <div className="board-columns" ref={columnsRef}>
                {STATUS_ORDER.map((s: Status) => (
                  <Column
                    key={s}
                    status={s}
                    tasks={state.columns[s]
                      .map((id) => state.tasksById[id])
                      .filter((t) => Boolean(t) && matches(t))}
                    agentTasks={agents}
                    workingTasks={working}
                    onOpen={(t) => setOpenId(t.BoardTaskID)}
                  />
                ))}
                {/* Hide the dependency overlay while filtering: hidden cards
                    would leave lines pointing at empty space. */}
                <DependencyOverlay
                  containerRef={columnsRef}
                  links={links}
                  visible={showLinks && !filterActive}
                />
              </div>
              {filterActive && visibleCount === 0 && (
                <p className="hint board-no-matches">
                  No tasks match the current filter.
                </p>
              )}
            </>
          )}
        </>
      )}

      {openTask && (
        <TaskDetail
          repo={full}
          task={openTask}
          onClose={() => setOpenId(null)}
        />
      )}
    </section>
  );
}

// BoardEmpty is the first-run state (item 4): a connected repo with zero tasks.
// It tells the user cards appear when an agent works the board over MCP and
// links to the connect/setup surface, closing the "why is this blank?" gap.
function BoardEmpty() {
  return (
    <div className="board-empty card">
      <h3>No tasks on this board yet</h3>
      <p className="muted">
        Cards appear here as soon as a task is created on this repo’s board —
        usually by an AI agent working it over MCP, or from the{" "}
        <code>cainban</code> CLI/TUI.
      </p>
      <p className="muted">
        Not wired up yet?{" "}
        <Link className="link" to="/connect">
          Open Connect
        </Link>{" "}
        to copy the MCP client config for your agent.
      </p>
    </div>
  );
}

// FilterBar is the client-side board filter (item 3): text (id/title), minimum
// priority, and agent-touched-only. Purely derived — it never refetches.
function FilterBar({
  query,
  setQuery,
  minPriority,
  setMinPriority,
  agentOnly,
  setAgentOnly,
  active,
  visibleCount,
  totalCards,
  onClear,
}: {
  query: string;
  setQuery: (v: string) => void;
  minPriority: number;
  setMinPriority: (v: number) => void;
  agentOnly: boolean;
  setAgentOnly: (v: boolean) => void;
  active: boolean;
  visibleCount: number;
  totalCards: number;
  onClear: () => void;
}) {
  return (
    <div className="board-filter" role="search">
      <label className="board-filter-field">
        <span className="sr-only">Filter tasks by id or title</span>
        <input
          type="search"
          placeholder="Filter by #id or title…"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
        />
      </label>
      <label className="board-filter-field board-filter-select">
        <span className="muted">Min priority</span>
        <select
          value={minPriority}
          onChange={(e) => setMinPriority(Number(e.target.value))}
        >
          <option value={0}>Any</option>
          <option value={1}>Low+</option>
          <option value={2}>Medium+</option>
          <option value={3}>High+</option>
          <option value={4}>Critical</option>
        </select>
      </label>
      <label className="board-filter-check">
        <input
          type="checkbox"
          checked={agentOnly}
          onChange={(e) => setAgentOnly(e.target.checked)}
        />
        <span>Agent-touched</span>
      </label>
      {active && (
        <>
          <span className="muted board-filter-count">
            {visibleCount} / {totalCards}
          </span>
          <button type="button" className="link" onClick={onClear}>
            Clear
          </button>
        </>
      )}
    </div>
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
