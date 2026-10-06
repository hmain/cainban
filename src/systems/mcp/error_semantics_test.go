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

// TestDeleteTask_NotFound_IsErrorResult proves delete_task on a missing id is an
// isError tool result pointing at list_tasks, not a transport error.
func TestDeleteTask_NotFound_IsErrorResult(t *testing.T) {
	s := NewStateless()
	res, _, err := s.handleDeleteTask(context.Background(), nil, DeleteTaskArgs{ID: 999})
	if err != nil {
		t.Fatalf("expected a tool result, got transport error: %v", err)
	}
	if res == nil || !res.IsError {
		t.Fatal("delete of a missing task should be an isError tool result")
	}
	text := textOf(t, res.Content[0])
	if !strings.Contains(text, "999") || !strings.Contains(text, "list_tasks") {
		t.Errorf("delete not-found message = %q, want id + list_tasks pointer", text)
	}
}

// TestDeleteThenRestore_RoundTrip proves a soft delete hides the task from
// get_task and a restore brings it back, exercising the real store round-trip on
// the default SQLite board.
func TestDeleteThenRestore_RoundTrip(t *testing.T) {
	s := NewStateless()
	ctx := context.Background()

	// Create a task to operate on.
	cres, _, err := s.handleCreateTask(ctx, nil, CreateTaskArgs{Title: "round-trip target"})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if cres == nil || cres.IsError {
		t.Fatalf("create returned an error result: %+v", cres)
	}

	// It should be gettable before deletion.
	if g, _, _ := s.handleGetTask(ctx, nil, GetTaskArgs{ID: 1}); g == nil || g.IsError {
		t.Fatalf("task #1 should exist before delete, got %+v", g)
	}

	// Soft delete.
	dres, _, err := s.handleDeleteTask(ctx, nil, DeleteTaskArgs{ID: 1})
	if err != nil {
		t.Fatalf("delete transport error: %v", err)
	}
	if dres == nil || dres.IsError {
		t.Fatalf("soft delete should succeed, got %+v", dres)
	}

	// Now get_task must report it missing (soft-deleted rows are hidden).
	if g, _, _ := s.handleGetTask(ctx, nil, GetTaskArgs{ID: 1}); g == nil || !g.IsError {
		t.Fatalf("task #1 should be hidden after soft delete, got %+v", g)
	}

	// Restore brings it back.
	rres, _, err := s.handleRestoreTask(ctx, nil, RestoreTaskArgs{ID: 1})
	if err != nil {
		t.Fatalf("restore transport error: %v", err)
	}
	if rres == nil || rres.IsError {
		t.Fatalf("restore should succeed, got %+v", rres)
	}
	if g, _, _ := s.handleGetTask(ctx, nil, GetTaskArgs{ID: 1}); g == nil || g.IsError {
		t.Fatalf("task #1 should be visible again after restore, got %+v", g)
	}
}

// TestSearchTasks_PrependsScopeAndMatches proves search_tasks returns matching
// tasks and prepends the scope header as its first content block.
func TestSearchTasks_PrependsScopeAndMatches(t *testing.T) {
	s := NewStateless()
	ctx := context.Background()
	if _, _, err := s.handleCreateTask(ctx, nil, CreateTaskArgs{Title: "deploy the widget"}); err != nil {
		t.Fatalf("create failed: %v", err)
	}
	res, _, err := s.handleSearchTasks(ctx, nil, SearchTasksArgs{Query: "widget"})
	if err != nil {
		t.Fatalf("search transport error: %v", err)
	}
	if res == nil || res.IsError {
		t.Fatalf("search should succeed, got %+v", res)
	}
	first := textOf(t, res.Content[0])
	if !strings.HasPrefix(first, "Scope — repo:") {
		t.Errorf("first content line = %q, want the scope header", first)
	}
	joined := ""
	for _, c := range res.Content {
		joined += textOf(t, c) + "\n"
	}
	if !strings.Contains(joined, "deploy the widget") {
		t.Errorf("search results = %q, want the matching task", joined)
	}
}
