package dynamo

import (
	"testing"

	"github.com/hmain/cainban/src/systems/task"
)

func newTestStore() *Store {
	return New(newFakeDDB(), "cainban-test")
}

func TestCreateAndGet(t *testing.T) {
	s := newTestStore()

	created, err := s.Create(1, "first task", "desc")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.BoardTaskID != 1 || created.ID != 1 {
		t.Fatalf("first task should be #1, got id=%d boardTaskID=%d", created.ID, created.BoardTaskID)
	}
	if created.Status != task.StatusTodo {
		t.Fatalf("new task status = %q, want todo", created.Status)
	}

	got, err := s.GetByBoardTaskID(1, 1)
	if err != nil {
		t.Fatalf("GetByBoardTaskID: %v", err)
	}
	if got.Title != "first task" || got.Description != "desc" {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
}

// TestAtomicCounter proves the per-board counter yields strictly increasing
// 1..N ids (the AUTOINCREMENT replacement).
func TestAtomicCounter(t *testing.T) {
	s := newTestStore()
	for i := 1; i <= 5; i++ {
		got, err := s.Create(1, "t", "")
		if err != nil {
			t.Fatalf("Create #%d: %v", i, err)
		}
		if got.BoardTaskID != i {
			t.Fatalf("expected board task id %d, got %d", i, got.BoardTaskID)
		}
	}
	// A second board keeps its own 1..N sequence.
	got, err := s.Create(2, "other board", "")
	if err != nil {
		t.Fatalf("Create on board 2: %v", err)
	}
	if got.BoardTaskID != 1 {
		t.Fatalf("board 2 first task should be #1, got %d", got.BoardTaskID)
	}
}

func TestListAndListByStatusOrdering(t *testing.T) {
	s := newTestStore()
	_, _ = s.CreateWithPriority(1, "low", "", task.PriorityLow)
	_, _ = s.CreateWithPriority(1, "critical", "", task.PriorityCritical)
	_, _ = s.Create(1, "none", "")

	all, err := s.List(1)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 tasks, got %d", len(all))
	}
	// Ordering: priority DESC, then board_task_id ASC.
	if all[0].Title != "critical" {
		t.Fatalf("highest priority should sort first, got %q", all[0].Title)
	}

	// Move one to doing and filter.
	if err := s.UpdateStatus(all[0].ID, task.StatusDoing); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	doing, err := s.ListByStatus(1, task.StatusDoing)
	if err != nil {
		t.Fatalf("ListByStatus: %v", err)
	}
	if len(doing) != 1 || doing[0].Title != "critical" {
		t.Fatalf("expected only 'critical' in doing, got %+v", doing)
	}
}

func TestUpdateStatusPriorityAndFields(t *testing.T) {
	s := newTestStore()
	created, _ := s.Create(1, "orig", "orig desc")

	if err := s.UpdateStatus(created.ID, task.StatusDone); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	if err := s.UpdatePriority(created.ID, "high"); err != nil {
		t.Fatalf("UpdatePriority: %v", err)
	}
	if err := s.Update(created.ID, "new title", "new desc"); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err := s.GetByBoardTaskID(1, created.BoardTaskID)
	if err != nil {
		t.Fatalf("GetByBoardTaskID: %v", err)
	}
	if got.Status != task.StatusDone {
		t.Fatalf("status = %q, want done", got.Status)
	}
	if got.Priority != task.PriorityHigh {
		t.Fatalf("priority = %d, want %d", got.Priority, task.PriorityHigh)
	}
	if got.Title != "new title" || got.Description != "new desc" {
		t.Fatalf("update did not persist: %+v", got)
	}
}

func TestUpdateMissingTask(t *testing.T) {
	s := newTestStore()
	if err := s.UpdateStatus(999, task.StatusDone); err == nil {
		t.Fatal("expected error updating a non-existent task")
	}
}

func TestSoftDeleteHidesAndRestore(t *testing.T) {
	s := newTestStore()
	created, _ := s.Create(1, "deleteme", "")

	if err := s.SoftDelete(created.ID); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}
	if _, err := s.GetByBoardTaskID(1, created.BoardTaskID); err == nil {
		t.Fatal("soft-deleted task should not be gettable")
	}
	list, _ := s.List(1)
	if len(list) != 0 {
		t.Fatalf("soft-deleted task should not appear in List, got %d", len(list))
	}
	// Double soft-delete is rejected.
	if err := s.SoftDelete(created.ID); err == nil {
		t.Fatal("double soft-delete should error")
	}

	if err := s.RestoreTask(created.ID); err != nil {
		t.Fatalf("RestoreTask: %v", err)
	}
	if _, err := s.GetByBoardTaskID(1, created.BoardTaskID); err != nil {
		t.Fatalf("restored task should be gettable: %v", err)
	}
}

func TestHardDeleteRemovesTaskAndLinks(t *testing.T) {
	s := newTestStore()
	a, _ := s.Create(1, "a", "")
	b, _ := s.Create(1, "b", "")
	if err := s.LinkTasks(a.ID, b.ID, task.LinkTypeBlocks); err != nil {
		t.Fatalf("LinkTasks: %v", err)
	}

	if err := s.HardDelete(a.ID); err != nil {
		t.Fatalf("HardDelete: %v", err)
	}
	if _, err := s.GetByBoardTaskID(1, a.BoardTaskID); err == nil {
		t.Fatal("hard-deleted task should be gone")
	}
	// The link should have been removed with the task.
	links, err := s.GetTaskLinks(b.ID)
	if err != nil {
		t.Fatalf("GetTaskLinks: %v", err)
	}
	if len(links) != 0 {
		t.Fatalf("link should be gone after hard delete, got %+v", links)
	}
}

func TestLinkUnlinkAndSelfLink(t *testing.T) {
	s := newTestStore()
	a, _ := s.Create(1, "a", "")
	b, _ := s.Create(1, "b", "")

	if err := s.LinkTasks(a.ID, a.ID, task.LinkTypeBlocks); err == nil {
		t.Fatal("self-link should be rejected")
	}
	if err := s.LinkTasks(a.ID, b.ID, task.LinkTypeBlocks); err != nil {
		t.Fatalf("LinkTasks: %v", err)
	}
	links, err := s.GetTaskLinks(a.ID)
	if err != nil {
		t.Fatalf("GetTaskLinks: %v", err)
	}
	if len(links) != 1 || links[0].ToTaskID != b.ID || links[0].LinkType != task.LinkTypeBlocks {
		t.Fatalf("unexpected links: %+v", links)
	}
	// Both endpoints see the link.
	if bl, _ := s.GetTaskLinks(b.ID); len(bl) != 1 {
		t.Fatalf("b should also see the link, got %+v", bl)
	}

	if err := s.UnlinkTasks(a.ID, b.ID, task.LinkTypeBlocks); err != nil {
		t.Fatalf("UnlinkTasks: %v", err)
	}
	if links, _ := s.GetTaskLinks(a.ID); len(links) != 0 {
		t.Fatalf("link should be gone, got %+v", links)
	}
	// Unlinking a missing link errors.
	if err := s.UnlinkTasks(a.ID, b.ID, task.LinkTypeBlocks); err == nil {
		t.Fatal("unlinking a non-existent link should error")
	}
}

func TestSearchAndFuzzyFind(t *testing.T) {
	s := newTestStore()
	_, _ = s.Create(1, "write the parser", "")
	_, _ = s.Create(1, "write the lexer", "")
	_, _ = s.Create(1, "deploy to prod", "")

	matches, err := s.SearchTasks(1, "write")
	if err != nil {
		t.Fatalf("SearchTasks: %v", err)
	}
	if len(matches) != 2 {
		t.Fatalf("expected 2 'write' matches, got %d", len(matches))
	}

	// FindTaskByFuzzyID by board id.
	got, err := s.FindTaskByFuzzyID(1, "3")
	if err != nil {
		t.Fatalf("FindTaskByFuzzyID by id: %v", err)
	}
	if got.Title != "deploy to prod" {
		t.Fatalf("expected task #3, got %q", got.Title)
	}

	// Ambiguous fuzzy match errors.
	if _, err := s.FindTaskByFuzzyID(1, "write"); err == nil {
		t.Fatal("ambiguous fuzzy match should error")
	}
}

func TestValidationErrors(t *testing.T) {
	s := newTestStore()
	if _, err := s.Create(1, "", "no title"); err == nil {
		t.Fatal("empty title should be rejected")
	}
	if _, err := s.CreateWithPriority(1, "t", "", "bogus-priority"); err == nil {
		t.Fatal("invalid priority should be rejected")
	}
	if err := s.UpdateStatus(1, task.Status("banana")); err == nil {
		t.Fatal("invalid status should be rejected")
	}
}
