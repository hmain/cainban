package task

import (
	"database/sql"
	"fmt"
	"time"
)

// activity limit bounds shared with the DynamoDB backend's contract.
const (
	activityDefaultLimit = 50
	activityMaxLimit     = 200
)

// sqliteTimeLayout matches how SQLite stores activity.created_at
// (DATETIME DEFAULT CURRENT_TIMESTAMP): a second-granular UTC string
// "YYYY-MM-DD HH:MM:SS". The since-cursor bound is formatted this way so a
// string comparison against the column is chronologically correct.
const sqliteTimeLayout = "2006-01-02 15:04:05"

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
// task's events are returned. When since is non-zero, only events strictly
// newer than since are returned (created_at > since); a zero since means "no
// filter". The activity.created_at column is a second-granular SQLite DATETIME
// stored in UTC ("YYYY-MM-DD HH:MM:SS"), so the bound is formatted to match it;
// same-second events on the boundary are handled by the SPA's overlapping poll
// window + idempotent reducer, not by this query.
func (s *System) ListActivity(boardID, boardTaskID, limit int, since time.Time) ([]ActivityEvent, error) {
	limit = clampActivityLimit(limit)

	var (
		rows *sql.Rows
		err  error
	)
	switch {
	case boardTaskID > 0 && !since.IsZero():
		rows, err = s.db.Query(
			"SELECT board_id, board_task_id, action, actor, detail, created_at FROM activity WHERE board_id = ? AND board_task_id = ? AND created_at > ? ORDER BY id DESC LIMIT ?",
			boardID, boardTaskID, since.UTC().Format(sqliteTimeLayout), limit,
		)
	case boardTaskID > 0:
		rows, err = s.db.Query(
			"SELECT board_id, board_task_id, action, actor, detail, created_at FROM activity WHERE board_id = ? AND board_task_id = ? ORDER BY id DESC LIMIT ?",
			boardID, boardTaskID, limit,
		)
	case !since.IsZero():
		rows, err = s.db.Query(
			"SELECT board_id, board_task_id, action, actor, detail, created_at FROM activity WHERE board_id = ? AND created_at > ? ORDER BY id DESC LIMIT ?",
			boardID, since.UTC().Format(sqliteTimeLayout), limit,
		)
	default:
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
