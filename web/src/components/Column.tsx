import type { Task } from "../mcpApi";
import type { Status } from "../board/types";
import { STATUS_LABEL, sortCards } from "../board/types";
import { Card } from "./Card";

// Column renders one status column with its card count and sorted cards. The
// agentTasks/workingTasks sets drive per-card treatment (Stage E).
export function Column({
  status,
  tasks,
  agentTasks,
  workingTasks,
  onOpen,
}: {
  status: Status;
  tasks: Task[];
  agentTasks?: Set<number>;
  workingTasks?: Set<number>;
  onOpen?: (task: Task) => void;
}) {
  const cards = sortCards(tasks);
  return (
    <section className="board-column" aria-label={STATUS_LABEL[status]}>
      <header className="board-column-head">
        <span className="board-column-title">{STATUS_LABEL[status]}</span>
        <span className="board-column-count">({cards.length})</span>
      </header>
      <div className="board-column-cards">
        {cards.length === 0 ? (
          <p className="board-column-empty">Nothing here yet.</p>
        ) : (
          cards.map((t) => (
            <Card
              key={t.BoardTaskID}
              task={t}
              agent={agentTasks?.has(t.BoardTaskID)}
              working={workingTasks?.has(t.BoardTaskID)}
              onOpen={onOpen}
            />
          ))
        )}
      </div>
    </section>
  );
}
