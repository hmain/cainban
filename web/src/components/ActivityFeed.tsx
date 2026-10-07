import { useCallback, useEffect, useState } from "react";
import { listActivity, type ActivityEvent } from "../mcpApi";
import { displayActor } from "../board/actor";

// ActivityFeed shows the append-only "who changed what" audit feed for a repo's
// board, read from the MCP `list_activity` tool. Read-only. The repo now comes
// from the route (/r/:owner/:repo/activity) rather than a selectedRepo prop.
export function ActivityFeed({ repo }: { repo: string }) {
  const [events, setEvents] = useState<ActivityEvent[]>([]);
  const [state, setState] = useState<"idle" | "loading" | "ready" | "error">(
    "idle",
  );
  const [error, setError] = useState("");
  const [empty, setEmpty] = useState(false);

  const load = useCallback(async () => {
    if (!repo) return;
    setState("loading");
    setError("");
    try {
      const res = await listActivity(repo, { limit: 50 });
      setEvents(res.events);
      setEmpty(res.empty);
      setState("ready");
    } catch (e) {
      setError(String(e instanceof Error ? e.message : e));
      setState("error");
    }
  }, [repo]);

  useEffect(() => {
    void load();
  }, [load]);

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
        Who changed what on <code>{repo}</code>’s board, newest first. This is a
        read-only audit feed — it never changes a task.
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
            <li
              key={`${ev.Timestamp}-${ev.BoardTaskID}-${i}`}
              className="activity-item"
            >
              <span className={`activity-badge activity-${ev.Action}`}>
                {actionLabel(ev.Action)}
              </span>
              <span className="activity-body">
                <code>#{ev.BoardTaskID}</code> {ev.Detail}
              </span>
              <span className="activity-meta">
                {displayActor(ev.Actor)} · {formatWhen(ev.Timestamp)}
              </span>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

// actionLabel maps the server action strings to short human labels.
export function actionLabel(action: string): string {
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

// formatWhen renders an RFC3339 timestamp as a compact local time, falling back
// to the raw string if it does not parse.
export function formatWhen(ts: string): string {
  const d = new Date(ts);
  if (Number.isNaN(d.getTime())) return ts;
  return d.toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}
