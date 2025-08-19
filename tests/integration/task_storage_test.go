package integration

import (
	"testing"

	"github.com/hmain/cainban/src/systems/storage"
	"github.com/hmain/cainban/src/systems/task"
)

func TestTaskSystem_Integration(t *testing.T) {
	// Setup in-memory database
	db, err := storage.NewMemory()
	if err != nil {
		t.Fatalf("Failed to create memory database: %v", err)
	}
	defer db.Close()

	taskSystem := task.New(db.Conn())

	t.Run("CreateAndRetrieve", func(t *testing.T) {
		// Create a task
		created, err := taskSystem.Create(1, "Test task", "Test description")
		if err != nil {
			t.Fatalf("Create() error = %v", err)
		}

		if created.ID == 0 {
			t.Error("Created task should have non-zero ID")
		}
		if created.Title != "Test task" {
			t.Errorf("Created task title = %v, want %v", created.Title, "Test task")
		}
		if created.Status != task.StatusTodo {
			t.Errorf("Created task status = %v, want %v", created.Status, task.StatusTodo)
		}

		// Retrieve the task
		retrieved, err := taskSystem.GetByID(created.ID)
		if err != nil {
			t.Fatalf("GetByID() error = %v", err)
		}

		if retrieved.ID != created.ID {
			t.Errorf("Retrieved task ID = %v, want %v", retrieved.ID, created.ID)
		}
		if retrieved.Title != created.Title {
			t.Errorf("Retrieved task title = %v, want %v", retrieved.Title, created.Title)
		}
		if retrieved.Description != created.Description {
			t.Errorf("Retrieved task description = %v, want %v", retrieved.Description, created.Description)
		}
	})

	t.Run("ListTasks", func(t *testing.T) {
		// Create multiple tasks
		task1, err := taskSystem.Create(1, "Task 1", "Description 1")
		if err != nil {
			t.Fatalf("Create() error = %v", err)
		}

		task2, err := taskSystem.Create(1, "Task 2", "Description 2")
		if err != nil {
			t.Fatalf("Create() error = %v", err)
		}

		// Move one task to doing
		err = taskSystem.UpdateStatus(task2.ID, task.StatusDoing)
		if err != nil {
			t.Fatalf("UpdateStatus() error = %v", err)
		}

		// List all tasks
		allTasks, err := taskSystem.List(1)
		if err != nil {
			t.Fatalf("List() error = %v", err)
		}

		if len(allTasks) < 2 {
			t.Errorf("List() returned %d tasks, want at least 2", len(allTasks))
		}

		// List tasks by status
		todoTasks, err := taskSystem.ListByStatus(1, task.StatusTodo)
		if err != nil {
			t.Fatalf("ListByStatus() error = %v", err)
		}

		doingTasks, err := taskSystem.ListByStatus(1, task.StatusDoing)
		if err != nil {
			t.Fatalf("ListByStatus() error = %v", err)
		}

		// Verify task1 is in todo
		found := false
		for _, t := range todoTasks {
			if t.ID == task1.ID {
				found = true
				break
			}
		}
		if !found {
			t.Error("Task1 should be in todo status")
		}

		// Verify task2 is in doing
		found = false
		for _, t := range doingTasks {
			if t.ID == task2.ID {
				found = true
				break
			}
		}
		if !found {
			t.Error("Task2 should be in doing status")
		}
	})

	t.Run("UpdateTask", func(t *testing.T) {
		// Create a task
		created, err := taskSystem.Create(1, "Original title", "Original description")
		if err != nil {
			t.Fatalf("Create() error = %v", err)
		}

		// Update the task
		newTitle := "Updated title"
		newDescription := "Updated description"
		err = taskSystem.Update(created.ID, newTitle, newDescription)
		if err != nil {
			t.Fatalf("Update() error = %v", err)
		}

		// Retrieve and verify
		updated, err := taskSystem.GetByID(created.ID)
		if err != nil {
			t.Fatalf("GetByID() error = %v", err)
		}

		if updated.Title != newTitle {
			t.Errorf("Updated task title = %v, want %v", updated.Title, newTitle)
		}
		if updated.Description != newDescription {
			t.Errorf("Updated task description = %v, want %v", updated.Description, newDescription)
		}
	})

	t.Run("UpdateStatus", func(t *testing.T) {
		// Create a task
		created, err := taskSystem.Create(1, "Status test", "")
		if err != nil {
			t.Fatalf("Create() error = %v", err)
		}

		// Update status to doing
		err = taskSystem.UpdateStatus(created.ID, task.StatusDoing)
		if err != nil {
			t.Fatalf("UpdateStatus() error = %v", err)
		}

		// Verify status change
		updated, err := taskSystem.GetByID(created.ID)
		if err != nil {
			t.Fatalf("GetByID() error = %v", err)
		}

		if updated.Status != task.StatusDoing {
			t.Errorf("Updated task status = %v, want %v", updated.Status, task.StatusDoing)
		}

		// Update status to done
		err = taskSystem.UpdateStatus(created.ID, task.StatusDone)
		if err != nil {
			t.Fatalf("UpdateStatus() error = %v", err)
		}

		// Verify status change
		updated, err = taskSystem.GetByID(created.ID)
		if err != nil {
			t.Fatalf("GetByID() error = %v", err)
		}

		if updated.Status != task.StatusDone {
			t.Errorf("Updated task status = %v, want %v", updated.Status, task.StatusDone)
		}
	})

	t.Run("ErrorHandling", func(t *testing.T) {
		// Test getting non-existent task
		_, err := taskSystem.GetByID(99999)
		if err == nil {
			t.Error("GetByID() should return error for non-existent task")
		}

		// Test updating non-existent task
		err = taskSystem.Update(99999, "Title", "Description")
		if err == nil {
			t.Error("Update() should return error for non-existent task")
		}

		// Test updating status of non-existent task
		err = taskSystem.UpdateStatus(99999, task.StatusDone)
		if err == nil {
			t.Error("UpdateStatus() should return error for non-existent task")
		}

		// Test creating task with empty title
		_, err = taskSystem.Create(1, "", "Description")
		if err == nil {
			t.Error("Create() should return error for empty title")
		}

		// Test updating with invalid status
		created, err := taskSystem.Create(1, "Valid task", "")
		if err != nil {
			t.Fatalf("Create() error = %v", err)
		}

		err = taskSystem.UpdateStatus(created.ID, task.Status("invalid"))
		if err == nil {
			t.Error("UpdateStatus() should return error for invalid status")
		}
	})
}
