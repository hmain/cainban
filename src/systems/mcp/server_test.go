package mcp

import (
	"testing"

	"github.com/hmain/cainban/src/systems/storage"
	"github.com/hmain/cainban/src/systems/task"
)

func setupTestServer(t *testing.T) *Server {
	// Setup in-memory database
	db, err := storage.NewMemory()
	if err != nil {
		t.Fatalf("Failed to create memory database: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	taskSystem := task.New(db.Conn())
	server := New(taskSystem)

	return server
}

func TestServer_New(t *testing.T) {
	server := setupTestServer(t)

	if server == nil {
		t.Fatal("Server should not be nil")
	}

	if server.taskSystem == nil {
		t.Error("Task system should not be nil")
	}

	if server.boardSystem == nil {
		t.Error("Board system should not be nil")
	}

	if server.mcpServer == nil {
		t.Error("MCP server should not be nil")
	}
}

func TestServer_CreateTask(t *testing.T) {
	server := setupTestServer(t)

	// Test creating a task through the task system directly
	// since the MCP SDK handles the protocol layer
	taskData, err := server.taskSystem.Create(1, "Test task", "Test description")
	if err != nil {
		t.Errorf("Create task should not return error: %v", err)
	}

	if taskData.Title != "Test task" {
		t.Errorf("Expected title 'Test task', got %s", taskData.Title)
	}

	if taskData.Description != "Test description" {
		t.Errorf("Expected description 'Test description', got %s", taskData.Description)
	}
}

func TestServer_ListTasks(t *testing.T) {
	server := setupTestServer(t)

	// Create a test task first
	_, err := server.taskSystem.Create(1, "Test task for listing", "")
	if err != nil {
		t.Fatalf("Failed to create test task: %v", err)
	}

	// List tasks
	tasks, err := server.taskSystem.List(1)
	if err != nil {
		t.Errorf("List tasks should not return error: %v", err)
	}

	if len(tasks) == 0 {
		t.Error("Should have at least one task")
	}

	found := false
	for _, task := range tasks {
		if task.Title == "Test task for listing" {
			found = true
			break
		}
	}

	if !found {
		t.Error("Created task should be in the list")
	}
}

func TestServer_UpdateTaskStatus(t *testing.T) {
	server := setupTestServer(t)

	// Create a test task
	taskData, err := server.taskSystem.Create(1, "Test task for status update", "")
	if err != nil {
		t.Fatalf("Failed to create test task: %v", err)
	}

	// Update task status
	err = server.taskSystem.UpdateStatus(taskData.ID, "doing")
	if err != nil {
		t.Errorf("Update task status should not return error: %v", err)
	}

	// Verify status was updated
	updatedTask, err := server.taskSystem.GetByID(taskData.ID)
	if err != nil {
		t.Errorf("Failed to get updated task: %v", err)
	}

	if updatedTask.Status != "doing" {
		t.Errorf("Expected status 'doing', got %s", updatedTask.Status)
	}
}

func TestServer_GetTask(t *testing.T) {
	server := setupTestServer(t)

	// Create a test task
	taskData, err := server.taskSystem.Create(1, "Test task for retrieval", "Test description")
	if err != nil {
		t.Fatalf("Failed to create test task: %v", err)
	}

	// Get the task
	retrievedTask, err := server.taskSystem.GetByID(taskData.ID)
	if err != nil {
		t.Errorf("Get task should not return error: %v", err)
	}

	if retrievedTask.ID != taskData.ID {
		t.Errorf("Expected ID %d, got %d", taskData.ID, retrievedTask.ID)
	}

	if retrievedTask.Title != "Test task for retrieval" {
		t.Errorf("Expected title 'Test task for retrieval', got %s", retrievedTask.Title)
	}
}

func TestServer_UpdateTaskPriority(t *testing.T) {
	server := setupTestServer(t)

	// Create a test task
	taskData, err := server.taskSystem.Create(1, "Test task for priority", "")
	if err != nil {
		t.Fatalf("Failed to create test task: %v", err)
	}

	// Update task priority
	err = server.taskSystem.UpdatePriority(taskData.ID, 3) // high priority
	if err != nil {
		t.Errorf("Update task priority should not return error: %v", err)
	}

	// Verify priority was updated
	updatedTask, err := server.taskSystem.GetByID(taskData.ID)
	if err != nil {
		t.Errorf("Failed to get updated task: %v", err)
	}

	if updatedTask.Priority != 3 {
		t.Errorf("Expected priority 3, got %d", updatedTask.Priority)
	}
}

func TestServer_DeleteAndRestoreTask(t *testing.T) {
	server := setupTestServer(t)

	// Create a test task
	taskData, err := server.taskSystem.Create(1, "Test task for deletion", "")
	if err != nil {
		t.Fatalf("Failed to create test task: %v", err)
	}

	// Delete the task (soft delete)
	err = server.taskSystem.Delete(taskData.ID)
	if err != nil {
		t.Errorf("Delete task should not return error: %v", err)
	}

	// Verify task is deleted (should not appear in regular list)
	tasks, err := server.taskSystem.List(1)
	if err != nil {
		t.Errorf("List tasks should not return error: %v", err)
	}

	for _, task := range tasks {
		if task.ID == taskData.ID {
			t.Error("Deleted task should not appear in regular list")
		}
	}

	// Restore the task
	err = server.taskSystem.RestoreTask(taskData.ID)
	if err != nil {
		t.Errorf("Restore task should not return error: %v", err)
	}

	// Verify task is restored
	restoredTask, err := server.taskSystem.GetByID(taskData.ID)
	if err != nil {
		t.Errorf("Failed to get restored task: %v", err)
	}

	if restoredTask.Title != "Test task for deletion" {
		t.Errorf("Expected title 'Test task for deletion', got %s", restoredTask.Title)
	}
}
