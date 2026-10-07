package dynamo

import (
	"testing"
	"time"

	"github.com/hmain/cainban/src/systems/task"
)

// fixedClock returns a Store whose now() advances by a fixed step on each call,
// so recorded events get strictly increasing, deterministic timestamps.
func newStoreWithClock(fake *fakeDDB, start time.Time, step time.Duration) *Store {
	s := New(fake, "cainban")
	cur := start
	s.now = func() time.Time {
		t := cur
		cur = cur.Add(step)
		return t
	}
	return s
}

func TestStore_RecordAndListActivity(t *testing.T) {
	fake := newFakeDDB()
	start := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	s := newStoreWithClock(fake, start, time.Second)

	// Record three events across two tasks, oldest first.
	events := []task.ActivityEvent{
		{BoardID: 1, BoardTaskID: 1, Action: task.ActivityCreated, Actor: "a@example.com", Detail: "First"},
		{BoardID: 1, BoardTaskID: 1, Action: task.ActivityStatusChanged, Actor: "a@example.com", Detail: "todo -> doing"},
		{BoardID: 1, BoardTaskID: 2, Action: task.ActivityCreated, Actor: "sub-xyz", Detail: "Second"},
	}
	for i := range events {
		if err := s.RecordActivity(events[i]); err != nil {
			t.Fatalf("RecordActivity[%d]: %v", i, err)
		}
	}

	// A raw EVENT# item must exist in the board's partition.
	pk := s.boardPK(1)
	foundEvent := false
	for key := range fake.items {
		if wantPK := pk + "\x00EVENT#"; len(key) >= len(wantPK) && key[:len(wantPK)] == wantPK {
			foundEvent = true
			break
		}
	}
	if !foundEvent {
		t.Fatalf("expected an EVENT# item under %q, found none", pk)
	}

	// Whole board, newest first.
	all, err := s.ListActivity(1, 0, 0, time.Time{})
	if err != nil {
		t.Fatalf("ListActivity(board): %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 events, got %d", len(all))
	}
	// Newest first: the last recorded event (task 2, "Second") is first.
	if all[0].Detail != "Second" || all[0].BoardTaskID != 2 {
		t.Fatalf("expected newest-first ordering, got first=%+v", all[0])
	}
	if all[2].Detail != "First" {
		t.Fatalf("expected oldest last, got %+v", all[2])
	}
	// Actor + action round-tripped.
	if all[0].Actor != "sub-xyz" || all[0].Action != task.ActivityCreated {
		t.Fatalf("actor/action not preserved: %+v", all[0])
	}

	// Task filter: only task 1's two events.
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
	// Newest of task 1 is the status change.
	if t1[0].Action != task.ActivityStatusChanged {
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

// TestStore_ListActivity_Since verifies the delta-polling cursor on the
// DynamoDB backend: with a since timestamp only strictly-newer events are
// returned (via the SK BETWEEN range + the Go post-filter), and omitting since
// is behavior-preserving.
func TestStore_ListActivity_Since(t *testing.T) {
	fake := newFakeDDB()
	start := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	s := newStoreWithClock(fake, start, time.Second)

	// Four events at t0, t0+1s, t0+2s, t0+3s.
	events := []task.ActivityEvent{
		{BoardID: 1, BoardTaskID: 1, Action: task.ActivityCreated, Actor: "a@example.com", Detail: "e0"},
		{BoardID: 1, BoardTaskID: 2, Action: task.ActivityCreated, Actor: "a@example.com", Detail: "e1"},
		{BoardID: 1, BoardTaskID: 3, Action: task.ActivityCreated, Actor: "a@example.com", Detail: "e2"},
		{BoardID: 1, BoardTaskID: 4, Action: task.ActivityCreated, Actor: "a@example.com", Detail: "e3"},
	}
	for i := range events {
		if err := s.RecordActivity(events[i]); err != nil {
			t.Fatalf("RecordActivity[%d]: %v", i, err)
		}
	}

	// since = t0+1s -> strictly newer events are e2, e3 (not the boundary e1).
	since := start.Add(time.Second) // the timestamp e1 was written at
	after, err := s.ListActivity(1, 0, 0, since)
	if err != nil {
		t.Fatalf("ListActivity(since): %v", err)
	}
	if len(after) != 2 {
		t.Fatalf("since: expected 2 strictly-newer events, got %d: %+v", len(after), after)
	}
	for _, ev := range after {
		if ev.Detail == "e0" || ev.Detail == "e1" {
			t.Fatalf("since leaked a boundary/older event: %+v", ev)
		}
	}
	// Newest-first ordering preserved under since.
	if after[0].Detail != "e3" {
		t.Fatalf("since: expected newest-first (e3), got %+v", after[0])
	}

	// since past the newest -> empty.
	none, err := s.ListActivity(1, 0, 0, start.Add(time.Hour))
	if err != nil {
		t.Fatalf("ListActivity(since future): %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("since future: expected 0 events, got %d", len(none))
	}

	// Omitted since -> all four (behavior-preserving).
	all, err := s.ListActivity(1, 0, 0, time.Time{})
	if err != nil {
		t.Fatalf("ListActivity(no since): %v", err)
	}
	if len(all) != 4 {
		t.Fatalf("no since: expected 4 events, got %d", len(all))
	}
}

func TestStore_RecordActivity_DefaultsTimestamp(t *testing.T) {
	fake := newFakeDDB()
	fixed := time.Date(2026, 5, 5, 8, 30, 0, 0, time.UTC)
	s := New(fake, "cainban")
	s.now = func() time.Time { return fixed }

	if err := s.RecordActivity(task.ActivityEvent{BoardID: 1, BoardTaskID: 3, Action: task.ActivityUpdated, Actor: "x", Detail: "New title"}); err != nil {
		t.Fatalf("RecordActivity: %v", err)
	}
	got, err := s.ListActivity(1, 0, 0, time.Time{})
	if err != nil {
		t.Fatalf("ListActivity: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 event, got %d", len(got))
	}
	if !got[0].Timestamp.Equal(fixed) {
		t.Fatalf("expected zero-timestamp event to default to now()=%v, got %v", fixed, got[0].Timestamp)
	}
}
