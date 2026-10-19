import { useEffect, useRef, useState } from "react";
import { fetchCounts } from "./api";
import type { Counts, WidgetConfig } from "./api";

export interface WidgetAppProps extends WidgetConfig {
  /** Full cainban board URL opened on click/Enter. */
  boardUrl: string;
  /** Poll interval in ms (0 disables polling). */
  pollMs: number;
  /** Whether to render the proportional mini-board bar. */
  showBar: boolean;
}

type LoadState =
  | { phase: "loading" }
  | { phase: "ok"; counts: Counts }
  | { phase: "error"; message: string };

/**
 * Minimal React component for the embeddable widget. It renders inside a Shadow
 * DOM root (mounted by widget.ts), so it owns no global styles and reads its
 * palette from CSS variables defined in widget.css. It polls the MCP endpoint
 * for task counts and opens the full board in a new tab on click.
 */
export function WidgetApp(props: WidgetAppProps) {
  const [state, setState] = useState<LoadState>({ phase: "loading" });
  // Keep the last good counts visible across refresh errors / transient blips.
  const lastCounts = useRef<Counts | null>(null);

  useEffect(() => {
    let active = true;
    let timer: ReturnType<typeof setTimeout> | undefined;

    const load = async () => {
      try {
        const counts = await fetchCounts(props);
        if (!active) return;
        lastCounts.current = counts;
        setState({ phase: "ok", counts });
      } catch (e) {
        if (!active) return;
        // On a refresh failure keep showing the last good data, flag error only
        // when we have nothing yet.
        if (lastCounts.current) {
          setState({ phase: "ok", counts: lastCounts.current });
        } else {
          setState({ phase: "error", message: errMsg(e) });
        }
      } finally {
        if (active && props.pollMs > 0) {
          timer = setTimeout(load, props.pollMs);
        }
      }
    };

    void load();
    return () => {
      active = false;
      if (timer) clearTimeout(timer);
    };
  }, [props.api, props.repo, props.token, props.pollMs]);

  const open = () => window.open(props.boardUrl, "_blank", "noopener");
  const onKey = (e: React.KeyboardEvent) => {
    if (e.key === "Enter" || e.key === " ") {
      e.preventDefault();
      open();
    }
  };

  return (
    <div
      className="cb-card"
      role="link"
      tabIndex={0}
      onClick={open}
      onKeyDown={onKey}
      title={`Open the ${props.repo} board on cainban`}
    >
      <div className="cb-head">
        <h3 className="cb-title">{props.repo}</h3>
        <span className="cb-brand">cainban</span>
      </div>

      {state.phase === "loading" && (
        <div className="cb-state">
          <span className="cb-spin" />
          Loading board…
        </div>
      )}

      {state.phase === "error" && (
        <div className="cb-state cb-state--error">{state.message}</div>
      )}

      {state.phase === "ok" && (
        <Board counts={state.counts} showBar={props.showBar} />
      )}

      <div className="cb-foot">
        <span>
          {state.phase === "ok" ? `${state.counts.total} tasks` : "\u00a0"}
        </span>
        <span className="cb-open">Open board →</span>
      </div>
    </div>
  );
}

function Board({ counts, showBar }: { counts: Counts; showBar: boolean }) {
  const { todo, doing, done, total } = counts;
  const pct = (n: number) => (total > 0 ? (n / total) * 100 : 0);
  return (
    <>
      <div className="cb-counts">
        <Pill kind="todo" label="To Do" value={todo} />
        <Pill kind="doing" label="Doing" value={doing} />
        <Pill kind="done" label="Done" value={done} />
      </div>
      {showBar && total > 0 && (
        <div className="cb-bar" aria-hidden="true">
          <span
            className="cb-bar-seg cb-bar-seg--todo"
            style={{ width: `${pct(todo)}%` }}
          />
          <span
            className="cb-bar-seg cb-bar-seg--doing"
            style={{ width: `${pct(doing)}%` }}
          />
          <span
            className="cb-bar-seg cb-bar-seg--done"
            style={{ width: `${pct(done)}%` }}
          />
        </div>
      )}
    </>
  );
}

function Pill({
  kind,
  label,
  value,
}: {
  kind: "todo" | "doing" | "done";
  label: string;
  value: number;
}) {
  return (
    <div className={`cb-pill cb-pill--${kind}`}>
      <span className="cb-num">{value}</span>
      <span className="cb-label">{label}</span>
    </div>
  );
}

function errMsg(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}
