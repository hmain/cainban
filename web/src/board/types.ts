import type { Task } from "../mcpApi";

export type Status = "todo" | "doing" | "done";

export const STATUS_ORDER: Status[] = ["todo", "doing", "done"];

export const STATUS_LABEL: Record<Status, string> = {
  todo: "To do",
  doing: "Doing",
  done: "Done",
};

// Priority 0..4 -> name, matching the Go task.PriorityNames map.
export const PRIORITY_NAME: Record<number, string> = {
  0: "none",
  1: "low",
  2: "medium",
  3: "high",
  4: "critical",
};

export function priorityName(p: number): string {
  return PRIORITY_NAME[p] ?? "none";
}

// sortCards orders a column: priority descending (critical=4 → none=0), then
// BoardTaskID ascending — the order the board requirement specifies.
export function sortCards(tasks: Task[]): Task[] {
  return [...tasks].sort((a, b) => {
    if (b.Priority !== a.Priority) return b.Priority - a.Priority;
    return a.BoardTaskID - b.BoardTaskID;
  });
}
