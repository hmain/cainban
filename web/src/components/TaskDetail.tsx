import { useCallback, useEffect, useRef, useState } from "react";
import type { Task } from "../mcpApi";
import { listActivity, type ActivityEvent } from "../mcpApi";
import { priorityName } from "../board/types";
import { STATUS_LABEL } from "../board/types";
import { displayActor } from "../board/actor";
import { actionLabel, formatWhen } from "./ActivityFeed";

// TaskDetail is a read-only modal that opens when a card is clicked. It shows
// the full task (id, title, status, priority, description, last-updated) plus
// that task's own activity history, fetched from list_activity scoped by
// task_id — no new MCP tool. Rendered as a native <dialog> (the pattern Pico
// CSS uses for modals): the browser gives us the top layer, Esc-to-close, and
// focus semantics for free. Backdrop click and Esc both close.
export function TaskDetail({
  repo,
  task,
  onClose,
}: {
  repo: string;
  task: Task;
  onClose: () => void;
}) {
  const ref = useRef<HTMLDialogElement | null>(null);
  const [events, setEvents] = useState<ActivityEvent[]>([]);
  const [state, setState] = useState<"loading" | "ready" | "error">("loading");
  const [error, setError] = useState("");

  // Show the dialog as a true modal (top layer + backdrop) on mount.
  useEffect(() => {
    const dlg = ref.current;
    if (dlg && !dlg.open) dlg.showModal();
  }, []);

  const load = useCallback(async () => {
    setState("loading");
    setError("");
    try {
      const res = await listActivity(repo, { taskId: task.BoardTaskID, limit: 50 });
      setEvents(res.events);
      setState("ready");
    } catch (e) {
      setError(String(e instanceof Error ? e.message : e));
      setState("error");
    }
  }, [repo, task.BoardTaskID]);

  useEffect(() => {
    void load();
  }, [load]);

  const pname = priorityName(task.Priority);

  return (
    <dialog
      ref={ref}
      className="task-detail"
      onClose={onClose}
      onClick={(e) => {
        // Backdrop click: the dialog element itself is the click target only
        // when the user clicks outside the inner content box.
        if (e.target === ref.current) ref.current?.close();
      }}
      aria-labelledby="task-detail-title"
    >
      <article className="task-detail-body">
        <header className="task-detail-head">
          <div className="task-detail-head-main">
            <code className="board-card-id">#{task.BoardTaskID}</code>
            <span className="task-detail-status">{STATUS_LABEL[task.Status]}</span>
            {task.Priority > 0 && (
              <span className={`pill pill-${pname}`}>{pname}</span>
            )}
          </div>
          <button
            type="button"
            className="task-detail-close"
            aria-label="Close"
            onClick={() => ref.current?.close()}
          >
            ×
          </button>
        </header>

        <h2 id="task-detail-title" className="task-detail-title">
          {task.Title}
        </h2>

        {task.Description ? (
          <p className="task-detail-desc">{task.Description}</p>
        ) : (
          <p className="task-detail-desc muted">No description.</p>
        )}

        {task.UpdatedAt && (
          <p className="muted task-detail-updated">
            Last updated {formatWhen(task.UpdatedAt)}
          </p>
        )}

        <h3 className="task-detail-subhead">History</h3>
        {state === "loading" && <p className="hint">Loading history…</p>}
        {state === "error" && (
          <p role="alert" className="error">
            {error}{" "}
            <button className="link" onClick={() => void load()}>
              Retry
            </button>
          </p>
        )}
        {state === "ready" && events.length === 0 && (
          <p className="hint">No activity recorded for this task yet.</p>
        )}
        {state === "ready" && events.length > 0 && (
          <ul className="activity-list">
            {events.map((ev, i) => (
              <li
                key={`${ev.Timestamp}-${i}`}
                className="activity-item"
              >
                <span className={`activity-badge activity-${ev.Action}`}>
                  {actionLabel(ev.Action)}
                </span>
                <span className="activity-body">{ev.Detail}</span>
                <span className="activity-meta">
                  {displayActor(ev.Actor)} · {formatWhen(ev.Timestamp)}
                </span>
              </li>
            ))}
          </ul>
        )}
      </article>
    </dialog>
  );
}
