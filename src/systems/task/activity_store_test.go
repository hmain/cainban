package task

import (
	"testing"
	"time"

	"github.com/hmain/cainban/src/systems/storage"
)

func newActivityTestSystem(t *testing.T) *System {
	t.Helper()
	db, err := storage.NewMemory()
	if err != nil {
		t.Fatalf("NewMemory: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return New(db.Conn())
}

func TestSystem_RecordAndListActivity(t *testing.T) {
	s := newActivityTestSystem(t)

	events := []ActivityEvent{
		{BoardID: 1, BoardTaskID: 1, Action: ActivityCreated, Actor: "a@example.com", Detail: "First"},
		{BoardID: 1, BoardTaskID: 1, Action: ActivityStatusChanged, Actor: "a@example.com", Detail: "todo -> doing"},
		{BoardID: 1, BoardTaskID: 2, Action: ActivityCreated, Actor: "sub-xyz", Detail: "Second"},
	}
	for i := range events {
		if err := s.RecordActivity(events[i]); err != nil {
			t.Fatalf("RecordActivity[%d]: %v", i, err)
		}
	}

	// Whole board, newest first (by insertion id).
	all, err := s.ListActivity(1, 0, 0, time.Time{})
	if err != nil {
		t.Fatalf("ListActivity(board): %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 events, got %d", len(all))
	}
	if all[0].Detail != "Second" || all[0].BoardTaskID != 2 {
		t.Fatalf("expected newest-first ordering, got first=%+v", all[0])
	}
	if all[2].Detail != "First" {
		t.Fatalf("expected oldest last, got %+v", all[2])
	}
	if all[0].Actor != "sub-xyz" || all[0].Action != ActivityCreated {
		t.Fatalf("actor/action not preserved: %+v", all[0])
	}

	// Task filter.
	t1, err := s.ListActivity(1, 1, 0, time.Time{})
	if err != nil {
		t.Fatalf("ListActivity(task 1): %v", err)
	}
	if len(t1) != 2 {
		t.Fatalf("expected 2 events for task 1, got %d", len(t1))
	}
	for _, ev := range t1 {
		if ev.BoardTaskID != 1 {
			t.Fatalf("task filter leaked a foreign task: %+v", ev)
		}
	}
	if t1[0].Action != ActivityStatusChanged {
		t.Fatalf("expected task 1 newest to be status_changed, got %+v", t1[0])
	}

	// Limit is respected.
	limited, err := s.ListActivity(1, 0, 1, time.Time{})
	if err != nil {
		t.Fatalf("ListActivity(limit 1): %v", err)
	}
	if len(limited) != 1 {
		t.Fatalf("expected 1 event under limit, got %d", len(limited))
	}
	if limited[0].Detail != "Second" {
		t.Fatalf("expected newest event under limit, got %+v", limited[0])
	}
}

func TestSystem_ListActivity_EmptyBoard(t *testing.T) {
	s := newActivityTestSystem(t)
	got, err := s.ListActivity(1, 0, 0, time.Time{})
	if err != nil {
		t.Fatalf("ListActivity: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected no events on a fresh board, got %d", len(got))
	}
}

// TestSystem_ListActivity_Since verifies the delta-polling cursor: with a since
// timestamp only strictly-newer events are returned, and omitting since is
// behavior-preserving. created_at is a second-granular SQLite DATETIME, so the
// test inserts rows with explicit UTC timestamps to control the boundary.
func TestSystem_ListActivity_Since(t *testing.T) {
	db, err := storage.NewMemory()
	if err != nil {
		t.Fatalf("NewMemory: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s := New(db.Conn())

	// Three events at distinct seconds, plus a second event sharing t2's second.
	t1 := "2026-01-01 12:00:01"
	t2 := "2026-01-01 12:00:02"
	t3 := "2026-01-01 12:00:03"
	insert := func(taskID int, detail, ts string) {
		if _, err := db.Conn().Exec(
			"INSERT INTO activity (board_id, board_task_id, action, actor, detail, created_at) VALUES (?, ?, ?, ?, ?, ?)",
			1, taskID, string(ActivityCreated), "a@example.com", detail, ts,
		); err != nil {
			t.Fatalf("insert %s: %v", detail, err)
		}
	}
	insert(1, "e1", t1)
	insert(2, "e2a", t2)
	insert(3, "e2b", t2) // shares t2's second
	insert(4, "e3", t3)

	// since = t1 -> everything strictly after 12:00:01 (e2a, e2b, e3).
	parse := func(ts string) time.Time {
		p, perr := time.Parse(sqliteTimeLayout, ts)
		if perr != nil {
			t.Fatalf("parse %s: %v", ts, perr)
		}
		return p.UTC()
	}
	after1, err := s.ListActivity(1, 0, 0, parse(t1))
	if err != nil {
		t.Fatalf("ListActivity(since t1): %v", err)
	}
	if len(after1) != 3 {
		t.Fatalf("since t1: expected 3 strictly-newer events, got %d: %+v", len(after1), after1)
	}
	for _, ev := range after1 {
		if ev.Detail == "e1" {
			t.Fatalf("since t1 leaked the boundary event e1: %+v", ev)
		}
	}

	// since = t3 -> nothing strictly newer.
	after3, err := s.ListActivity(1, 0, 0, parse(t3))
	if err != nil {
		t.Fatalf("ListActivity(since t3): %v", err)
	}
	if len(after3) != 0 {
		t.Fatalf("since t3: expected 0 events, got %d", len(after3))
	}

	// Omitted since (zero) returns all four, behavior-preserving.
	all, err := s.ListActivity(1, 0, 0, time.Time{})
	if err != nil {
		t.Fatalf("ListActivity(no since): %v", err)
	}
	if len(all) != 4 {
		t.Fatalf("no since: expected 4 events, got %d", len(all))
	}
}
