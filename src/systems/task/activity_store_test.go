package task

import (
	"testing"

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
	all, err := s.ListActivity(1, 0, 0)
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
	t1, err := s.ListActivity(1, 1, 0)
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
	limited, err := s.ListActivity(1, 0, 1)
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
	got, err := s.ListActivity(1, 0, 0)
	if err != nil {
		t.Fatalf("ListActivity: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected no events on a fresh board, got %d", len(got))
	}
}
