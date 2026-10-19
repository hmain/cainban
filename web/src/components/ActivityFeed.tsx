import { useCallback, useEffect, useState } from "react";
import { listActivity, type ActivityEvent } from "../mcpApi";
import { classifyActor, displayActor } from "../board/actor";

// ActivityFeed shows the append-only "who changed what" audit feed for a repo's
// board, read from the MCP `list_activity` tool. Read-only. The repo now comes
// from the route (/r/:owner/:repo/activity) rather than a selectedRepo prop.
//
// Phase 7 enhancements:
// - Agent vs human visual distinction (🤖 / 👤 icons)
// - Action-specific styling
// - Filterable by actor type
export function ActivityFeed({ repo }: { repo: string }) {
  const [events, setEvents] = useState<ActivityEvent[]>([]);
  const [state, setState] = useState<"idle" | "loading" | "ready" | "error">(
    "idle",
  );
  const [error, setError] = useState("");
  const [empty, setEmpty] = useState(false);
  const [filter, setFilter] = useState<"all" | "agent" | "human">("all");

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

  // Filter events by actor type
  const filteredEvents = events.filter((ev) => {
    if (filter === "all") return true;
    const actorType = classifyActor(ev.Actor);
    return actorType === filter;
  });

  // Count by actor type
  const agentCount = events.filter((ev) => classifyActor(ev.Actor) === "agent").length;
  const humanCount = events.filter((ev) => classifyActor(ev.Actor) === "human").length;

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
        Who changed what on <code>{repo}</code>'s board, newest first.
      </p>

      {/* Actor type filter */}
      {state === "ready" && !empty && (
        <div className="activity-filter">
          <button
            className={`activity-filter-btn ${filter === "all" ? "active" : ""}`}
            onClick={() => setFilter("all")}
          >
            All ({events.length})
          </button>
          <button
            className={`activity-filter-btn ${filter === "agent" ? "active" : ""}`}
            onClick={() => setFilter("agent")}
            disabled={agentCount === 0}
          >
            🤖 Agents ({agentCount})
          </button>
          <button
            className={`activity-filter-btn ${filter === "human" ? "active" : ""}`}
            onClick={() => setFilter("human")}
            disabled={humanCount === 0}
          >
            👤 Humans ({humanCount})
          </button>
        </div>
      )}

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
      {state === "ready" && !empty && filteredEvents.length === 0 && (
        <p className="hint">
          No {filter} activity yet.
        </p>
      )}
      {state === "ready" && !empty && filteredEvents.length > 0 && (
        <ul className="activity-list">
          {filteredEvents.map((ev, i) => (
            <ActivityItem
              key={`${ev.Timestamp}-${ev.BoardTaskID}-${i}`}
              event={ev}
            />
          ))}
        </ul>
      )}
    </section>
  );
}

function ActivityItem({ event }: { event: ActivityEvent }) {
  const actorType = classifyActor(event.Actor);
  const isAgent = actorType === "agent";
  
  return (
    <li className={`activity-item ${isAgent ? "agent" : "human"}`}>
      <span className="activity-actor-icon" aria-hidden="true">
        {isAgent ? "🤖" : "👤"}
      </span>
      <span className={`activity-badge activity-${event.Action}`}>
        {actionLabel(event.Action)}
      </span>
      <span className="activity-body">
        <code>#{event.BoardTaskID}</code> {event.Detail}
      </span>
      <span className="activity-meta">
        {displayActor(event.Actor)} · {formatWhen(event.Timestamp)}
      </span>
    </li>
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
    case "deleted":
      return "deleted";
    case "restored":
      return "restored";
    case "linked":
      return "linked";
    case "unlinked":
      return "unlinked";
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
