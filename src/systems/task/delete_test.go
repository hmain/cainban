package task

import (
	"testing"

	"github.com/hmain/cainban/src/systems/storage"
)

func TestSoftDelete(t *testing.T) {
	db, err := storage.NewMemory()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	taskSystem := New(db.Conn())

	// Create test task
	task, err := taskSystem.Create(1, "Test Task", "Test description")
	if err != nil {
		t.Fatalf("Failed to create task: %v", err)
	}

	// Soft delete the task
	err = taskSystem.SoftDelete(task.ID)
	if err != nil {
		t.Fatalf("Failed to soft delete task: %v", err)
	}

	// Verify task is not found in normal queries
	_, err = taskSystem.GetByID(task.ID)
	if err == nil {
		t.Fatal("Expected error when getting soft deleted task")
	}

	// Verify task is not in list
	tasks, err := taskSystem.List(1)
	if err != nil {
		t.Fatalf("Failed to list tasks: %v", err)
	}

	for _, listTask := range tasks {
		if listTask.ID == task.ID {
			t.Fatal("Soft deleted task should not appear in list")
		}
	}
}

func TestRestoreTask(t *testing.T) {
	db, err := storage.NewMemory()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	taskSystem := New(db.Conn())

	// Create and soft delete task
	task, err := taskSystem.Create(1, "Test Task", "Test description")
	if err != nil {
		t.Fatalf("Failed to create task: %v", err)
	}

	err = taskSystem.SoftDelete(task.ID)
	if err != nil {
		t.Fatalf("Failed to soft delete task: %v", err)
	}

	// Restore the task
	err = taskSystem.RestoreTask(task.ID)
	if err != nil {
		t.Fatalf("Failed to restore task: %v", err)
	}

	// Verify task is accessible again
	restoredTask, err := taskSystem.GetByID(task.ID)
	if err != nil {
		t.Fatalf("Failed to get restored task: %v", err)
	}

	if restoredTask.ID != task.ID {
		t.Fatalf("Expected task ID %d, got %d", task.ID, restoredTask.ID)
	}
}

func TestHardDelete(t *testing.T) {
	db, err := storage.NewMemory()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	taskSystem := New(db.Conn())

	// Create test task
	task, err := taskSystem.Create(1, "Test Task", "Test description")
	if err != nil {
		t.Fatalf("Failed to create task: %v", err)
	}

	// Hard delete the task
	err = taskSystem.HardDelete(task.ID)
	if err != nil {
		t.Fatalf("Failed to hard delete task: %v", err)
	}

	// Verify task cannot be restored
	err = taskSystem.RestoreTask(task.ID)
	if err == nil {
		t.Fatal("Expected error when trying to restore hard deleted task")
	}
}

func TestDeleteWithLinks(t *testing.T) {
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

	// Link tasks
	err = taskSystem.LinkTasks(task1.ID, task2.ID, LinkTypeBlocks)
	if err != nil {
		t.Fatalf("Failed to link tasks: %v", err)
	}

	// Hard delete task1 (should remove links)
	err = taskSystem.HardDelete(task1.ID)
	if err != nil {
		t.Fatalf("Failed to hard delete task: %v", err)
	}

	// Verify links are removed
	links, err := taskSystem.GetTaskLinks(task2.ID)
	if err != nil {
		t.Fatalf("Failed to get task links: %v", err)
	}

	if len(links) != 0 {
		t.Fatalf("Expected 0 links after hard delete, got %d", len(links))
	}
}
