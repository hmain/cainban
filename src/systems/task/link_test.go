package task

import (
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
