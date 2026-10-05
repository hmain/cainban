package mcp

import (
	"context"
	"strings"
	"testing"
)

// TestInvalidStatus_IsErrorResult proves an invalid status value is returned as
// an isError TOOL RESULT (model-recoverable, names the valid values) rather than
// a JSON-RPC transport error. Validation runs before any store access, so this
// needs no database.
func TestInvalidStatus_IsErrorResult(t *testing.T) {
	s := NewStateless()
	res, _, err := s.handleUpdateTaskStatus(context.Background(), nil, UpdateTaskStatusArgs{ID: 1, Status: "bogus"})
	if err != nil {
		t.Fatalf("expected a tool result, got transport error: %v", err)
	}
	if res == nil || !res.IsError {
		t.Fatal("invalid status should be an isError tool result, not a success/transport error")
	}
	text := textOf(t, res.Content[0])
	if !strings.Contains(text, "todo") || !strings.Contains(text, "doing") || !strings.Contains(text, "done") {
		t.Errorf("invalid-status message = %q, want it to name the valid statuses", text)
	}
}

// TestNotFoundTask_IsErrorResult proves a get_task for a missing id returns an
// isError tool result (not a transport error) that points the model at
// list_tasks. Uses the default SQLite board (no tenant), which is empty, so any
// id is missing.
func TestNotFoundTask_IsErrorResult(t *testing.T) {
	s := NewStateless()
	res, _, err := s.handleGetTask(context.Background(), nil, GetTaskArgs{ID: 999})
	if err != nil {
		t.Fatalf("expected a tool result, got transport error: %v", err)
	}
	if res == nil || !res.IsError {
		t.Fatal("not-found task should be an isError tool result, not a transport error")
	}
	text := textOf(t, res.Content[0])
	if !strings.Contains(text, "999") || !strings.Contains(text, "list_tasks") {
		t.Errorf("not-found message = %q, want it to name the id and point at list_tasks", text)
	}
}
