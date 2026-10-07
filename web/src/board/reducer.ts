import type { Task } from "../mcpApi";
import type { Status } from "./types";
import { STATUS_ORDER } from "./types";

export type ConnectionState =
  | "connecting"
  | "live"
  | "paused"
  | "reconnecting"
  | "stale";

// Actor classification carried on each mutating event, so attribution is
// identical regardless of transport (poll or a future SSE adapter).
export type Actor = "agent" | "human" | "unknown";

export interface BoardCard extends Task {}

export interface BoardState {
  tasksById: Record<number, BoardCard>;
  columns: Record<Status, number[]>; // ordered BoardTaskID lists
  connectionState: ConnectionState;
  // Whether the SPA is on the degraded (polling) transport vs full push. Phase 1
  // is always polling; the flag lets the chip read honestly once SSE lands.
  degraded: boolean;
  lastCursor: string | null; // RFC3339(Nano) of the newest applied event
  lastActorByTask: Record<number, Actor>;
  // When each task was last touched by an agent (ms epoch), for the derived
  // "agent working" window.
  agentTouchedAt: Record<number, number>;
}

export type BoardEvent =
  | { type: "snapshot"; tasks: Task[]; cursor?: string | null }
  | {
      type: "task_created";
      task: Partial<Task> & { BoardTaskID: number };
      actor: Actor;
      ts?: string;
    }
  | { type: "status_changed"; id: number; to: Status; actor: Actor; ts?: string }
  | {
      type: "priority_changed";
      id: number;
      priority: number;
      actor: Actor;
      ts?: string;
    }
  | {
      type: "task_edited";
      id: number;
      title?: string;
      description?: string;
      actor: Actor;
      ts?: string;
    }
  | { type: "task_deleted"; id: number; actor: Actor; ts?: string }
  | { type: "task_restored"; id: number; actor: Actor; ts?: string }
  | { type: "connection"; state: ConnectionState; degraded?: boolean }
  | { type: "cursor"; value: string };

export function initialBoardState(): BoardState {
  return {
    tasksById: {},
    columns: { todo: [], doing: [], done: [] },
    connectionState: "connecting",
    degraded: false,
    lastCursor: null,
    lastActorByTask: {},
    agentTouchedAt: {},
  };
}

function emptyColumns(): Record<Status, number[]> {
  return { todo: [], doing: [], done: [] };
}

// Place a task id into its status column if not already present. Columns stay
// as insertion-ordered id lists; final display order is decided by sortCards at
// render time, so we only need set membership here.
function addToColumn(cols: Record<Status, number[]>, status: Status, id: number) {
  if (!cols[status].includes(id)) cols[status].push(id);
}

function removeFromAllColumns(cols: Record<Status, number[]>, id: number) {
  for (const s of STATUS_ORDER) {
    const i = cols[s].indexOf(id);
    if (i >= 0) cols[s].splice(i, 1);
  }
}

function recordActor(
  state: BoardState,
  id: number,
  actor: Actor,
  ts?: string,
): void {
  state.lastActorByTask[id] = actor;
  if (actor === "agent") {
    const when = ts ? Date.parse(ts) : Date.now();
    state.agentTouchedAt[id] = Number.isNaN(when) ? Date.now() : when;
  }
}

function advanceCursor(state: BoardState, ts?: string): void {
  if (!ts) return;
  if (!state.lastCursor || ts > state.lastCursor) state.lastCursor = ts;
}

// boardReducer applies a normalized event to the board. Every mutation is
// IDEMPOTENT: re-applying an already-applied event is a no-op, so an
// overlapping poll window (or a future redelivered SSE frame) cannot
// double-create, double-move, or resurrect a deleted card. A snapshot always
// replaces state wholesale.
export function boardReducer(state: BoardState, ev: BoardEvent): BoardState {
  switch (ev.type) {
    case "snapshot": {
      const tasksById: Record<number, BoardCard> = {};
      const columns = emptyColumns();
      for (const t of ev.tasks) {
        tasksById[t.BoardTaskID] = { ...t };
        addToColumn(columns, t.Status, t.BoardTaskID);
      }
      return {
        ...state,
        tasksById,
        columns,
        lastCursor: ev.cursor !== undefined ? ev.cursor : state.lastCursor,
      };
    }

    case "task_created": {
      const next = clone(state);
      const id = ev.task.BoardTaskID;
      if (!next.tasksById[id]) {
        next.tasksById[id] = {
          BoardTaskID: id,
          Title: ev.task.Title ?? "",
          Status: ev.task.Status ?? "todo",
          Priority: ev.task.Priority ?? 0,
          Description: ev.task.Description,
          UpdatedAt: ev.task.UpdatedAt,
        };
        addToColumn(next.columns, next.tasksById[id].Status, id);
      }
      recordActor(next, id, ev.actor, ev.ts);
      advanceCursor(next, ev.ts);
      return next;
    }

    case "status_changed": {
      const next = clone(state);
      const card = next.tasksById[ev.id];
      if (card && card.Status !== ev.to) {
        removeFromAllColumns(next.columns, ev.id);
        card.Status = ev.to;
        addToColumn(next.columns, ev.to, ev.id);
      }
      recordActor(next, ev.id, ev.actor, ev.ts);
      advanceCursor(next, ev.ts);
      return next;
    }

    case "priority_changed": {
      const next = clone(state);
      const card = next.tasksById[ev.id];
      if (card) card.Priority = ev.priority;
      recordActor(next, ev.id, ev.actor, ev.ts);
      advanceCursor(next, ev.ts);
      return next;
    }

    case "task_edited": {
      const next = clone(state);
      const card = next.tasksById[ev.id];
      if (card) {
        if (ev.title !== undefined) card.Title = ev.title;
        if (ev.description !== undefined) card.Description = ev.description;
      }
      recordActor(next, ev.id, ev.actor, ev.ts);
      advanceCursor(next, ev.ts);
      return next;
    }

    case "task_deleted": {
      const next = clone(state);
      if (next.tasksById[ev.id]) {
        delete next.tasksById[ev.id];
        removeFromAllColumns(next.columns, ev.id);
      }
      recordActor(next, ev.id, ev.actor, ev.ts);
      advanceCursor(next, ev.ts);
      return next;
    }

    case "task_restored": {
      // A restore brings a task back; without a snapshot we do not know its
      // fields, so we add a minimal placeholder in todo only if absent. The
      // next poll's snapshot (on reconnect) or a subsequent event fills detail.
      const next = clone(state);
      if (!next.tasksById[ev.id]) {
        next.tasksById[ev.id] = {
          BoardTaskID: ev.id,
          Title: `#${ev.id}`,
          Status: "todo",
          Priority: 0,
        };
        addToColumn(next.columns, "todo", ev.id);
      }
      recordActor(next, ev.id, ev.actor, ev.ts);
      advanceCursor(next, ev.ts);
      return next;
    }

    case "connection":
      return {
        ...state,
        connectionState: ev.state,
        degraded: ev.degraded ?? state.degraded,
      };

    case "cursor":
      return { ...state, lastCursor: ev.value };

    default:
      return state;
  }
}

// clone makes a shallow-but-safe copy for an in-place mutation path (columns and
// tasksById are copied; individual card objects are copied on write where
// needed above by replacing fields on a copied map entry).
function clone(state: BoardState): BoardState {
  const tasksById: Record<number, BoardCard> = {};
  for (const [k, v] of Object.entries(state.tasksById)) {
    tasksById[Number(k)] = { ...v };
  }
  return {
    ...state,
    tasksById,
    columns: {
      todo: [...state.columns.todo],
      doing: [...state.columns.doing],
      done: [...state.columns.done],
    },
    lastActorByTask: { ...state.lastActorByTask },
    agentTouchedAt: { ...state.agentTouchedAt },
  };
}

// workingTaskIds derives the "agent working" set: tasks in `doing` touched by an
// agent within windowMs. Used by the board to flag pulsing cards (Stage E).
export function workingTaskIds(state: BoardState, windowMs = 30000): Set<number> {
  const now = Date.now();
  const out = new Set<number>();
  for (const idStr of state.columns.doing) {
    const id = Number(idStr);
    const touched = state.agentTouchedAt[id];
    if (touched && now - touched <= windowMs) out.add(id);
  }
  return out;
}

// agentTaskIds returns tasks whose last actor was an agent (for the ⚡ badge).
export function agentTaskIds(state: BoardState): Set<number> {
  const out = new Set<number>();
  for (const [idStr, actor] of Object.entries(state.lastActorByTask)) {
    if (actor === "agent") out.add(Number(idStr));
  }
  return out;
}
