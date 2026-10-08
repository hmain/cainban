package task

import (
	"fmt"
	"testing"

	"github.com/hmain/cainban/src/systems/storage"
)

func TestTaskLinking(t *testing.T) {
	// Create in-memory database for testing
	db, err := storage.NewMemory()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	taskSystem := New(db.Conn())

	// Create test tasks
	task1, err := taskSystem.Create(1, "Task 1", "First task")
	if err != nil {
		t.Fatalf("Failed to create task 1: %v", err)
	}

	task2, err := taskSystem.Create(1, "Task 2", "Second task")
	if err != nil {
		t.Fatalf("Failed to create task 2: %v", err)
	}

	// Test linking tasks
	err = taskSystem.LinkTasks(task1.ID, task2.ID, LinkTypeBlocks)
	if err != nil {
		t.Fatalf("Failed to link tasks: %v", err)
	}

	// Test getting links
	links, err := taskSystem.GetTaskLinks(task1.ID)
	if err != nil {
		t.Fatalf("Failed to get task links: %v", err)
	}

	if len(links) != 1 {
		t.Fatalf("Expected 1 link, got %d", len(links))
	}

	link := links[0]
	if link.FromTaskID != task1.ID || link.ToTaskID != task2.ID || link.LinkType != LinkTypeBlocks {
		t.Fatalf("Link data incorrect: got %+v", link)
	}

	// Test unlinking tasks
	err = taskSystem.UnlinkTasks(task1.ID, task2.ID, LinkTypeBlocks)
	if err != nil {
		t.Fatalf("Failed to unlink tasks: %v", err)
	}

	// Verify link was removed
	links, err = taskSystem.GetTaskLinks(task1.ID)
	if err != nil {
		t.Fatalf("Failed to get task links after unlinking: %v", err)
	}

	if len(links) != 0 {
		t.Fatalf("Expected 0 links after unlinking, got %d", len(links))
	}
}

func TestTaskLinkingValidation(t *testing.T) {
	db, err := storage.NewMemory()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	taskSystem := New(db.Conn())

	task1, err := taskSystem.Create(1, "Task 1", "First task")
	if err != nil {
		t.Fatalf("Failed to create task: %v", err)
	}

	// Test self-linking prevention
	err = taskSystem.LinkTasks(task1.ID, task1.ID, LinkTypeBlocks)
	if err == nil {
		t.Fatal("Expected error when linking task to itself")
	}

	// Test linking to non-existent task
	err = taskSystem.LinkTasks(task1.ID, 999, LinkTypeBlocks)
	if err == nil {
		t.Fatal("Expected error when linking to non-existent task")
	}
}

func TestListLinks(t *testing.T) {
	db, err := storage.NewMemory()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	ts := New(db.Conn())

	a, _ := ts.Create(1, "A", "")
	b, _ := ts.Create(1, "B", "")
	c, _ := ts.Create(1, "C", "")

	// Empty board: no links.
	links, err := ts.ListLinks(1)
	if err != nil {
		t.Fatalf("ListLinks on empty board: %v", err)
	}
	if len(links) != 0 {
		t.Fatalf("expected 0 links, got %d", len(links))
	}

	// Two links of different types.
	if err := ts.LinkTasks(a.ID, b.ID, LinkTypeBlocks); err != nil {
		t.Fatalf("link a->b: %v", err)
	}
	if err := ts.LinkTasks(b.ID, c.ID, LinkTypeDependsOn); err != nil {
		t.Fatalf("link b->c: %v", err)
	}

	links, err = ts.ListLinks(1)
	if err != nil {
		t.Fatalf("ListLinks: %v", err)
	}
	if len(links) != 2 {
		t.Fatalf("expected 2 links, got %d: %+v", len(links), links)
	}

	// Both links present with correct endpoints/types (order is newest-first,
	// so assert by content, not position).
	seen := map[string]bool{}
	for _, l := range links {
		seen[fmt.Sprintf("%d-%s-%d", l.FromTaskID, l.LinkType, l.ToTaskID)] = true
	}
	if !seen[fmt.Sprintf("%d-%s-%d", a.ID, LinkTypeBlocks, b.ID)] {
		t.Errorf("missing a->b blocks link; got %+v", links)
	}
	if !seen[fmt.Sprintf("%d-%s-%d", b.ID, LinkTypeDependsOn, c.ID)] {
		t.Errorf("missing b->c depends_on link; got %+v", links)
	}

	// A link on a DIFFERENT board must not appear in board 1's list.
	d, _ := ts.Create(2, "D", "")
	e, _ := ts.Create(2, "E", "")
	if err := ts.LinkTasks(d.ID, e.ID, LinkTypeRelated); err != nil {
		t.Fatalf("link d->e: %v", err)
	}
	links, err = ts.ListLinks(1)
	if err != nil {
		t.Fatalf("ListLinks after cross-board link: %v", err)
	}
	if len(links) != 2 {
		t.Fatalf("board 1 should still have 2 links, got %d: %+v", len(links), links)
	}

	// A hard-deleted endpoint drops its link from the board list.
	if err := ts.HardDelete(a.ID); err != nil {
		t.Fatalf("hard delete a: %v", err)
	}
	links, err = ts.ListLinks(1)
	if err != nil {
		t.Fatalf("ListLinks after delete: %v", err)
	}
	if len(links) != 1 {
		t.Fatalf("expected 1 link after deleting a, got %d: %+v", len(links), links)
	}
	if links[0].FromTaskID != b.ID || links[0].ToTaskID != c.ID {
		t.Errorf("surviving link should be b->c, got %+v", links[0])
	}
}
