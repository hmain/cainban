import { listActivity, listTasks, type ActivityEvent } from "../mcpApi";
import type { BoardEvent } from "./reducer";
import type { Status } from "./types";
import { classifyActor } from "./actor";

const POLL_INTERVAL_MS = 4000;
const MAX_BACKOFF_MS = 60000;

type Dispatch = (ev: BoardEvent) => void;
type GetCursor = () => string | null;

// PollAdapter keeps a repo's board current by polling list_activity for deltas
// and dispatching normalized board events into the reducer. It also owns the
// Page-Visibility pause/resume and the error backoff. It never renders. A later
// SSE adapter would replace this file and dispatch the SAME BoardEvents.
export class PollAdapter {
  private repo: string;
  private dispatch: Dispatch;
  private getCursor: GetCursor;
  private timer: ReturnType<typeof setTimeout> | null = null;
  private stopped = false;
  private backoff = POLL_INTERVAL_MS;
  private onVisibility: () => void;

  constructor(repo: string, dispatch: Dispatch, getCursor: GetCursor) {
    this.repo = repo;
    this.dispatch = dispatch;
    this.getCursor = getCursor;
    this.onVisibility = () => {
      if (document.visibilityState === "hidden") {
        this.pause();
      } else {
        void this.resume();
      }
    };
  }

  // start takes the initial snapshot, then begins the poll loop.
  async start(): Promise<void> {
    this.stopped = false;
    document.addEventListener("visibilitychange", this.onVisibility);
    this.dispatch({ type: "connection", state: "connecting", degraded: true });
    await this.snapshot();
    this.dispatch({ type: "connection", state: "live", degraded: true });
    this.schedule(POLL_INTERVAL_MS);
  }

  // stop tears down the timer and the visibility listener (route change/unmount).
  stop(): void {
    this.stopped = true;
    if (this.timer) clearTimeout(this.timer);
    this.timer = null;
    document.removeEventListener("visibilitychange", this.onVisibility);
  }

  // retry is the manual Retry affordance from the stale chip.
  retry(): void {
    if (this.stopped) return;
    this.backoff = POLL_INTERVAL_MS;
    void this.resume();
  }

  private pause(): void {
    if (this.timer) clearTimeout(this.timer);
    this.timer = null;
    this.dispatch({ type: "connection", state: "paused", degraded: true });
  }

  // resume re-snapshots (so a board hidden through many changes self-heals) then
  // resumes the delta loop.
  private async resume(): Promise<void> {
    if (this.stopped) return;
    this.dispatch({ type: "connection", state: "connecting", degraded: true });
    await this.snapshot();
    this.dispatch({ type: "connection", state: "live", degraded: true });
    this.schedule(POLL_INTERVAL_MS);
  }

  private schedule(ms: number): void {
    if (this.stopped) return;
    if (this.timer) clearTimeout(this.timer);
    this.timer = setTimeout(() => void this.tick(), ms);
  }

  // snapshot replaces board state wholesale from list_tasks (the authoritative
  // state after any connect/reconnect/focus).
  private async snapshot(): Promise<void> {
    try {
      const res = await listTasks(this.repo);
      // A fresh snapshot resets the cursor to "now" so the first delta poll
      // only picks up events after this point.
      this.dispatch({
        type: "snapshot",
        tasks: res.tasks,
        cursor: new Date().toISOString(),
      });
      this.backoff = POLL_INTERVAL_MS;
    } catch {
      // Snapshot failure is handled like a poll error: back off and mark stale
      // past the budget. Keep whatever board we already had.
      this.onError();
    }
  }

  private async tick(): Promise<void> {
    if (this.stopped || document.visibilityState === "hidden") return;
    try {
      const since = this.getCursor() ?? undefined;
      const res = await listActivity(this.repo, { since, limit: 200 });
      // Apply oldest-first so column moves land in order.
      const ordered = [...res.events].reverse();
      let needReSnapshot = false;
      for (const ev of ordered) {
        const mapped = mapEvent(ev);
        if (mapped === "resnapshot") {
          needReSnapshot = true;
          break;
        }
        if (mapped) this.dispatch(mapped);
      }
      if (needReSnapshot) {
        await this.snapshot();
      }
      this.backoff = POLL_INTERVAL_MS;
      this.dispatch({ type: "connection", state: "live", degraded: true });
      this.schedule(POLL_INTERVAL_MS);
    } catch {
      this.onError();
    }
  }

  private onError(): void {
    if (this.stopped) return;
    this.backoff = Math.min(this.backoff * 2, MAX_BACKOFF_MS);
    const exhausted = this.backoff >= MAX_BACKOFF_MS;
    this.dispatch({
      type: "connection",
      state: exhausted ? "stale" : "reconnecting",
      degraded: true,
    });
    this.schedule(this.backoff);
  }
}

// mapEvent turns one activity event into a normalized board event, or
// "resnapshot" when a delta cannot be parsed reliably (the board then re-reads
// list_tasks rather than guessing — correct-but-one-extra-fetch).
function mapEvent(ev: ActivityEvent): BoardEvent | "resnapshot" | null {
  const actor = classifyActor(ev.Actor);
  const id = ev.BoardTaskID;
  const ts = ev.Timestamp;
  switch (ev.Action) {
    case "created":
      return {
        type: "task_created",
        task: { BoardTaskID: id, Title: ev.Detail, Status: "todo", Priority: 0 },
        actor,
        ts,
      };
    case "status_changed": {
      const to = parseStatusTarget(ev.Detail);
      if (!to) return "resnapshot";
      return { type: "status_changed", id, to, actor, ts };
    }
    case "priority_changed": {
      const p = parsePriorityTarget(ev.Detail);
      if (p == null) return "resnapshot";
      return { type: "priority_changed", id, priority: p, actor, ts };
    }
    case "updated":
      return { type: "task_edited", id, title: ev.Detail, actor, ts };
    case "deleted":
      return { type: "task_deleted", id, actor, ts };
    case "restored":
      return { type: "task_restored", id, actor, ts };
    // linked / unlinked do not change the board's columns/cards — ignore.
    case "linked":
    case "unlinked":
      return null;
    default:
      return null;
  }
}

// parseStatusTarget reads the "old -> new" Detail the status handler writes
// (fmt "%s -> %s"); returns the new status or null if unrecognized.
function parseStatusTarget(detail: string): Status | null {
  const m = detail.match(/->\s*(todo|doing|done)\s*$/i);
  if (!m) return null;
  return m[1].toLowerCase() as Status;
}

// parsePriorityTarget reads the "oldName -> newName" Detail the priority handler
// writes; maps the new name to its numeric level.
function parsePriorityTarget(detail: string): number | null {
  const m = detail.match(/->\s*(none|low|medium|high|critical)\s*$/i);
  if (!m) return null;
  const names: Record<string, number> = {
    none: 0,
    low: 1,
    medium: 2,
    high: 3,
    critical: 4,
  };
  return names[m[1].toLowerCase()] ?? null;
}
