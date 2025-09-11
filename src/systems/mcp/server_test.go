package mcp

import (
	"bytes"
	"encoding/json"
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

	input := &bytes.Buffer{}
	output := &bytes.Buffer{}
	server := New(taskSystem, input, output)

	return server
}

func TestServer_Initialize(t *testing.T) {
	server := setupTestServer(t)

	req := MCPRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
	}

	resp := server.handleRequest(&req)

	if resp.Error != nil {
		t.Errorf("Initialize should not return error: %v", resp.Error)
	}

	result, ok := resp.Result.(map[string]interface{})
	if !ok {
		t.Fatal("Initialize result should be a map")
	}

	if result["protocolVersion"] != "2024-11-05" {
		t.Errorf("Expected protocol version 2024-11-05, got %v", result["protocolVersion"])
	}
}

func TestServer_ToolsList(t *testing.T) {
	server := setupTestServer(t)

	req := MCPRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/list",
	}

	resp := server.handleRequest(&req)

	if resp.Error != nil {
		t.Errorf("Tools list should not return error: %v", resp.Error)
	}

	result, ok := resp.Result.(map[string]interface{})
	if !ok {
		t.Fatal("Tools list result should be a map")
	}

	tools, ok := result["tools"].([]Tool)
	if !ok {
		t.Fatal("Tools should be a slice of Tool")
	}

	expectedTools := []string{
		"create_task", "list_tasks", "update_task_status", "get_task",
		"update_task_priority", "update_task", "list_boards", "change_board",
		"link_tasks", "unlink_tasks", "get_task_links", "delete_task", "restore_task",
	}
	if len(tools) != len(expectedTools) {
		t.Errorf("Expected %d tools, got %d", len(expectedTools), len(tools))
		return
	}

	// Check that all expected tools are present (order may vary)
	toolMap := make(map[string]bool)
	for _, tool := range tools {
		toolMap[tool.Name] = true
	}

	for _, expectedTool := range expectedTools {
		if !toolMap[expectedTool] {
			t.Errorf("Expected tool %s not found", expectedTool)
		}
	}
}

func TestServer_CreateTask(t *testing.T) {
	server := setupTestServer(t)

	args := map[string]interface{}{
		"title":       "Test task",
		"description": "Test description",
	}

	resp := server.handleCreateTask(&MCPRequest{ID: 1}, args)

	if resp.Error != nil {
		t.Errorf("Create task should not return error: %v", resp.Error)
	}

	result, ok := resp.Result.(map[string]interface{})
	if !ok {
		t.Fatal("Create task result should be a map")
	}

	taskData, ok := result["task"]
	if !ok {
		t.Error("Create task result should include task data")
	}

	// Verify task was created
	if taskData == nil {
		t.Error("Task data should not be nil")
	}
}

func TestServer_ListTasks(t *testing.T) {
	server := setupTestServer(t)

	// First create a task
	createArgs := map[string]interface{}{
		"title": "Test task for listing",
	}
	server.handleCreateTask(&MCPRequest{ID: 1}, createArgs)

	// Then list tasks
	listArgs := map[string]interface{}{}
	resp := server.handleListTasks(&MCPRequest{ID: 2}, listArgs)

	if resp.Error != nil {
		t.Errorf("List tasks should not return error: %v", resp.Error)
	}

	result, ok := resp.Result.(map[string]interface{})
	if !ok {
		t.Fatal("List tasks result should be a map")
	}

	tasks, ok := result["tasks"]
	if !ok {
		t.Error("List tasks result should include tasks")
	}

	if tasks == nil {
		t.Error("Tasks should not be nil")
	}
}

func TestServer_UpdateTaskStatus(t *testing.T) {
	server := setupTestServer(t)

	// First create a task
	createArgs := map[string]interface{}{
		"title": "Test task for status update",
	}
	createResp := server.handleCreateTask(&MCPRequest{ID: 1}, createArgs)

	// Extract task ID from response
	result := createResp.Result.(map[string]interface{})
	taskData := result["task"].(*task.Task)

	// Update task status
	updateArgs := map[string]interface{}{
		"id":     float64(taskData.ID), // JSON numbers are float64
		"status": "doing",
	}

	resp := server.handleUpdateTaskStatus(&MCPRequest{ID: 2}, updateArgs)

	if resp.Error != nil {
		t.Errorf("Update task status should not return error: %v", resp.Error)
	}
}

func TestServer_ErrorHandling(t *testing.T) {
	server := setupTestServer(t)

	t.Run("InvalidMethod", func(t *testing.T) {
		req := MCPRequest{
			JSONRPC: "2.0",
			ID:      1,
			Method:  "invalid_method",
		}

		resp := server.handleRequest(&req)

		if resp.Error == nil {
			t.Error("Invalid method should return error")
		}

		if resp.Error.Code != -32601 {
			t.Errorf("Expected error code -32601, got %d", resp.Error.Code)
		}
	})

	t.Run("InvalidTool", func(t *testing.T) {
		params := map[string]interface{}{
			"name":      "invalid_tool",
			"arguments": map[string]interface{}{},
		}
		paramsJSON, _ := json.Marshal(params)

		req := MCPRequest{
			JSONRPC: "2.0",
			ID:      1,
			Method:  "tools/call",
			Params:  paramsJSON,
		}

		resp := server.handleRequest(&req)

		if resp.Error == nil {
			t.Error("Invalid tool should return error")
		}

		if resp.Error.Code != -32601 {
			t.Errorf("Expected error code -32601, got %d", resp.Error.Code)
		}
	})

	t.Run("MissingRequiredParam", func(t *testing.T) {
		args := map[string]interface{}{
			// Missing required "title" parameter
			"description": "Test description",
		}

		resp := server.handleCreateTask(&MCPRequest{ID: 1}, args)

		if resp.Error == nil {
			t.Error("Missing required param should return error")
		}

		if resp.Error.Code != -32602 {
			t.Errorf("Expected error code -32602, got %d", resp.Error.Code)
		}
	})
}
