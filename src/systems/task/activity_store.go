package task

import (
	"database/sql"
	"fmt"
)

// activity limit bounds shared with the DynamoDB backend's contract.
const (
	activityDefaultLimit = 50
	activityMaxLimit     = 200
)

// clampActivityLimit normalizes a caller-supplied limit: <=0 or >max collapses
// to the sane default so a missing/absurd value never returns an unbounded feed.
func clampActivityLimit(limit int) int {
	if limit <= 0 || limit > activityMaxLimit {
		return activityDefaultLimit
	}
	return limit
}

// RecordActivity appends an append-only audit event. It is best-effort
// observability (the MCP handler swallows any error so a failed record never
// fails the tool call) and the activity table is NEVER read to derive task or
// board state.
func (s *System) RecordActivity(ev ActivityEvent) error {
	_, err := s.db.Exec(
		"INSERT INTO activity (board_id, board_task_id, action, actor, detail) VALUES (?, ?, ?, ?, ?)",
		ev.BoardID, ev.BoardTaskID, string(ev.Action), ev.Actor, ev.Detail,
	)
	if err != nil {
		return fmt.Errorf("failed to record activity: %w", err)
	}
	return nil
}

// ListActivity returns recent events for a board, newest first (by insertion
// id, which is monotonic), capped at limit. When boardTaskID > 0 only that
// task's events are returned.
func (s *System) ListActivity(boardID, boardTaskID, limit int) ([]ActivityEvent, error) {
	limit = clampActivityLimit(limit)

	var (
		rows *sql.Rows
		err  error
	)
	if boardTaskID > 0 {
		rows, err = s.db.Query(
			"SELECT board_id, board_task_id, action, actor, detail, created_at FROM activity WHERE board_id = ? AND board_task_id = ? ORDER BY id DESC LIMIT ?",
			boardID, boardTaskID, limit,
		)
	} else {
		rows, err = s.db.Query(
			"SELECT board_id, board_task_id, action, actor, detail, created_at FROM activity WHERE board_id = ? ORDER BY id DESC LIMIT ?",
			boardID, limit,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to list activity: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var events []ActivityEvent
	for rows.Next() {
		var (
			ev     ActivityEvent
			action string
			actor  sql.NullString
			detail sql.NullString
		)
		if err := rows.Scan(&ev.BoardID, &ev.BoardTaskID, &action, &actor, &detail, &ev.Timestamp); err != nil {
			return nil, fmt.Errorf("failed to scan activity event: %w", err)
		}
		ev.Action = ActivityAction(action)
		ev.Actor = actor.String
		ev.Detail = detail.String
		events = append(events, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate activity events: %w", err)
	}
	return events, nil
}
