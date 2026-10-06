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

// TestLinkTasks_InvalidType_IsErrorResult proves an invalid link type is an
// isError tool result that names the valid types. Validation runs before any
// store access, so this needs no database.
func TestLinkTasks_InvalidType_IsErrorResult(t *testing.T) {
	s := NewStateless()
	res, _, err := s.handleLinkTasks(context.Background(), nil, LinkTasksArgs{FromID: 1, ToID: 2, LinkType: "bogus"})
	if err != nil {
		t.Fatalf("expected a tool result, got transport error: %v", err)
	}
	if res == nil || !res.IsError {
		t.Fatal("invalid link type should be an isError tool result")
	}
	text := textOf(t, res.Content[0])
	if !strings.Contains(text, "blocks") || !strings.Contains(text, "blocked_by") {
		t.Errorf("invalid-type message = %q, want it to name the valid link types", text)
	}
}

// TestLinkTasks_FromNotFound_IsErrorResult proves linking from a missing task is
// an isError tool result (valid type, but no such task on the empty board).
func TestLinkTasks_FromNotFound_IsErrorResult(t *testing.T) {
	s := NewStateless()
	res, _, err := s.handleLinkTasks(context.Background(), nil, LinkTasksArgs{FromID: 999, ToID: 998, LinkType: "blocks"})
	if err != nil {
		t.Fatalf("expected a tool result, got transport error: %v", err)
	}
	if res == nil || !res.IsError {
		t.Fatal("link from a missing task should be an isError tool result")
	}
	text := textOf(t, res.Content[0])
	if !strings.Contains(text, "999") || !strings.Contains(text, "list_tasks") {
		t.Errorf("from-not-found message = %q, want id + list_tasks pointer", text)
	}
}

// TestLinkRoundTrip exercises link -> get_task_links -> get_task(shows links) ->
// unlink against the default SQLite board.
func TestLinkRoundTrip(t *testing.T) {
	s := NewStateless()
	ctx := context.Background()

	for _, title := range []string{"blocker", "blocked"} {
		if _, _, err := s.handleCreateTask(ctx, nil, CreateTaskArgs{Title: title}); err != nil {
			t.Fatalf("create %q failed: %v", title, err)
		}
	}

	// Link #1 blocks #2.
	lres, _, err := s.handleLinkTasks(ctx, nil, LinkTasksArgs{FromID: 1, ToID: 2, LinkType: "blocks"})
	if err != nil {
		t.Fatalf("link transport error: %v", err)
	}
	if lres == nil || lres.IsError {
		t.Fatalf("link should succeed, got %+v", lres)
	}

	// get_task_links on #1 should show the link.
	glres, _, err := s.handleGetTaskLinks(ctx, nil, GetTaskLinksArgs{ID: 1})
	if err != nil {
		t.Fatalf("get_task_links transport error: %v", err)
	}
	joined := ""
	for _, c := range glres.Content {
		joined += textOf(t, c) + "\n"
	}
	if !strings.Contains(joined, "blocks") {
		t.Errorf("get_task_links output = %q, want the blocks link", joined)
	}

	// get_task on #1 should include a Links section.
	gres, _, _ := s.handleGetTask(ctx, nil, GetTaskArgs{ID: 1})
	gtext := textOf(t, gres.Content[0])
	if !strings.Contains(gtext, "Links:") || !strings.Contains(gtext, "blocks") {
		t.Errorf("get_task output = %q, want a Links section naming the blocks link", gtext)
	}

	// Unlink and confirm it is gone from get_task_links.
	ures, _, err := s.handleUnlinkTasks(ctx, nil, UnlinkTasksArgs{FromID: 1, ToID: 2, LinkType: "blocks"})
	if err != nil {
		t.Fatalf("unlink transport error: %v", err)
	}
	if ures == nil || ures.IsError {
		t.Fatalf("unlink should succeed, got %+v", ures)
	}
	glres2, _, _ := s.handleGetTaskLinks(ctx, nil, GetTaskLinksArgs{ID: 1})
	joined2 := ""
	for _, c := range glres2.Content {
		joined2 += textOf(t, c) + "\n"
	}
	if !strings.Contains(joined2, "no links") {
		t.Errorf("after unlink, get_task_links = %q, want 'no links'", joined2)
	}
}
