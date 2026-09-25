package mcp

import (
	"testing"

	"github.com/hmain/cainban/src/systems/storage"
	"github.com/hmain/cainban/src/systems/task"
)

// testServer bundles a stateless Server with a task.System bound to an
// in-memory database, so the behavioural tests below can drive the underlying
// task operations directly (the MCP SDK owns the protocol layer). The server
// itself is stateless and resolves its board DB per request, so the task
// system is held here in the test harness rather than on the server.
type testServer struct {
	server     *Server
	taskSystem *task.System
}

func setupTestServer(t *testing.T) *testServer {
	// Setup in-memory database
	db, err := storage.NewMemory()
	if err != nil {
		t.Fatalf("Failed to create memory database: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	return &testServer{
		server:     NewStateless(),
		taskSystem: task.New(db.Conn()),
	}
}

func TestServer_New(t *testing.T) {
	ts := setupTestServer(t)

	if ts.server == nil {
		t.Fatal("Server should not be nil")
	}

	if ts.server.boardSystem == nil {
		t.Error("Board system should not be nil")
	}

	if ts.server.mcpServer == nil {
		t.Error("MCP server should not be nil")
	}

	if ts.server.schemaCache == nil {
		t.Error("Schema cache should not be nil")
	}
}

func TestServer_CreateTask(t *testing.T) {
	ts := setupTestServer(t)

	taskData, err := ts.taskSystem.Create(1, "Test task", "Test description")
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
	ts := setupTestServer(t)

	_, err := ts.taskSystem.Create(1, "Test task for listing", "")
	if err != nil {
		t.Fatalf("Failed to create test task: %v", err)
	}

	tasks, err := ts.taskSystem.List(1)
	if err != nil {
		t.Errorf("List tasks should not return error: %v", err)
	}

	if len(tasks) == 0 {
		t.Error("Should have at least one task")
	}

	found := false
	for _, tk := range tasks {
		if tk.Title == "Test task for listing" {
			found = true
			break
		}
	}

	if !found {
		t.Error("Created task should be in the list")
	}
}

func TestServer_UpdateTaskStatus(t *testing.T) {
	ts := setupTestServer(t)

	taskData, err := ts.taskSystem.Create(1, "Test task for status update", "")
	if err != nil {
		t.Fatalf("Failed to create test task: %v", err)
	}

	err = ts.taskSystem.UpdateStatus(taskData.ID, "doing")
	if err != nil {
		t.Errorf("Update task status should not return error: %v", err)
	}

	updatedTask, err := ts.taskSystem.GetByID(taskData.ID)
	if err != nil {
		t.Errorf("Failed to get updated task: %v", err)
	}

	if updatedTask.Status != "doing" {
		t.Errorf("Expected status 'doing', got %s", updatedTask.Status)
	}
}

func TestServer_GetTask(t *testing.T) {
	ts := setupTestServer(t)

	taskData, err := ts.taskSystem.Create(1, "Test task for retrieval", "Test description")
	if err != nil {
		t.Fatalf("Failed to create test task: %v", err)
	}

	retrievedTask, err := ts.taskSystem.GetByID(taskData.ID)
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
	ts := setupTestServer(t)

	taskData, err := ts.taskSystem.Create(1, "Test task for priority", "")
	if err != nil {
		t.Fatalf("Failed to create test task: %v", err)
	}

	err = ts.taskSystem.UpdatePriority(taskData.ID, 3) // high priority
	if err != nil {
		t.Errorf("Update task priority should not return error: %v", err)
	}

	updatedTask, err := ts.taskSystem.GetByID(taskData.ID)
	if err != nil {
		t.Errorf("Failed to get updated task: %v", err)
	}

	if updatedTask.Priority != 3 {
		t.Errorf("Expected priority 3, got %d", updatedTask.Priority)
	}
}

func TestServer_DeleteAndRestoreTask(t *testing.T) {
	ts := setupTestServer(t)

	taskData, err := ts.taskSystem.Create(1, "Test task for deletion", "")
	if err != nil {
		t.Fatalf("Failed to create test task: %v", err)
	}

	err = ts.taskSystem.Delete(taskData.ID)
	if err != nil {
		t.Errorf("Delete task should not return error: %v", err)
	}

	tasks, err := ts.taskSystem.List(1)
	if err != nil {
		t.Errorf("List tasks should not return error: %v", err)
	}

	for _, tk := range tasks {
		if tk.ID == taskData.ID {
			t.Error("Deleted task should not appear in regular list")
		}
	}

	err = ts.taskSystem.RestoreTask(taskData.ID)
	if err != nil {
		t.Errorf("Restore task should not return error: %v", err)
	}

	restoredTask, err := ts.taskSystem.GetByID(taskData.ID)
	if err != nil {
		t.Errorf("Failed to get restored task: %v", err)
	}

	if restoredTask.Title != "Test task for deletion" {
		t.Errorf("Expected title 'Test task for deletion', got %s", restoredTask.Title)
	}
}
