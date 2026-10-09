import { useEffect, useRef, useState, type KeyboardEvent } from "react";
import type { Task } from "../mcpApi";
import { priorityName } from "../board/types";

// Card renders one task: #id, title, and a priority pill (none is muted). It
// also carries the live motion cues (Stage E): a brief "enter" animation on
// first appearance and a one-shot "landed" accent when the card arrives in
// `done`. All motion collapses to nothing under prefers-reduced-motion (handled
// in styles.css), so this component does not branch on the media query itself.
export function Card({
  task,
  agent,
  working,
  onOpen,
}: {
  task: Task;
  agent?: boolean;
  working?: boolean;
  onOpen?: (task: Task) => void;
}) {
  const pname = priorityName(task.Priority);
  const firstRender = useRef(true);
  const prevStatus = useRef(task.Status);
  const [entering, setEntering] = useState(true);
  const [landed, setLanded] = useState(false);

  // Clear the one-shot enter class after the animation window.
  useEffect(() => {
    if (!firstRender.current) return;
    firstRender.current = false;
    const t = setTimeout(() => setEntering(false), 400);
    return () => clearTimeout(t);
  }, []);

  // Flash the landed accent when the card transitions INTO done.
  useEffect(() => {
    if (prevStatus.current !== "done" && task.Status === "done") {
      setLanded(true);
      const t = setTimeout(() => setLanded(false), 650);
      prevStatus.current = task.Status;
      return () => clearTimeout(t);
    }
    prevStatus.current = task.Status;
  }, [task.Status]);

  const cls = [
    "board-card",
    onOpen ? "clickable" : "",
    working ? "working" : "",
    entering ? "enter" : "",
    landed ? "done-landed" : "",
  ]
    .filter(Boolean)
    .join(" ");

  return (
    <article
      className={cls}
      data-task-id={task.BoardTaskID}
      {...(onOpen
        ? {
            role: "button",
            tabIndex: 0,
            "aria-label": `Open task #${task.BoardTaskID}: ${task.Title}`,
            onClick: () => onOpen(task),
            onKeyDown: (e: KeyboardEvent) => {
              if (e.key === "Enter" || e.key === " ") {
                e.preventDefault();
                onOpen(task);
              }
            },
          }
        : {})}
    >
      <div className="board-card-head">
        <code className="board-card-id">#{task.BoardTaskID}</code>
        {task.Priority > 0 && (
          <span className={`pill pill-${pname}`}>{pname}</span>
        )}
      </div>
      <div className="board-card-title">{task.Title}</div>
      {(agent || working) && (
        <div className="board-card-foot">
          <span className="agent-badge" title="Changed by an agent">
            ⚡ {working ? "agent working" : "agent"}
          </span>
        </div>
      )}
    </article>
  );
}
