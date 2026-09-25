package dynamo

import (
	"testing"

	"github.com/hmain/cainban/src/systems/auth"
)

// These tests prove the Phase 3 tenant isolation guarantee AT THE STORE LAYER:
// a dynamo.Store built with repo A's partition prefix addresses a completely
// different DynamoDB key space than one built with repo B's prefix, even when
// they share the same underlying table/client. Isolation is therefore
// structural (the PK can only ever be under one prefix), not a runtime filter
// that could be bypassed.
//
// The prefix strings come from auth.PartitionPrefixFor — the SAME function the
// request-time resolver uses — so the test and production cannot drift on how a
// repo maps to a partition.

// storeForRepo builds a store scoped to repo, sharing the given fake table so
// cross-tenant reads are actually possible to ATTEMPT (and must come back
// empty).
func storeForRepo(fake *fakeDDB, repo string) *Store {
	return NewWithPrefix(fake, "cainban-test", auth.PartitionPrefixFor(repo))
}

// Scenario (d): a caller scoped to repo A cannot read or write data under repo
// B's prefix.
func TestTenantIsolation_AcannotSeeB(t *testing.T) {
	fake := newFakeDDB()
	repoA := storeForRepo(fake, "acme/repo-a")
	repoB := storeForRepo(fake, "acme/repo-b")

	// A writes a task to its own board.
	if _, err := repoA.Create(1, "A-secret", "only for repo A"); err != nil {
		t.Fatalf("repoA.Create: %v", err)
	}
	// B writes a task to its own board.
	if _, err := repoB.Create(1, "B-secret", "only for repo B"); err != nil {
		t.Fatalf("repoB.Create: %v", err)
	}

	// A lists its board: sees ONLY its own task.
	aTasks, err := repoA.List(1)
	if err != nil {
		t.Fatalf("repoA.List: %v", err)
	}
	if len(aTasks) != 1 || aTasks[0].Title != "A-secret" {
		t.Fatalf("repoA should see only its own task, got %+v", aTasks)
	}

	// B lists its board: sees ONLY its own task.
	bTasks, err := repoB.List(1)
	if err != nil {
		t.Fatalf("repoB.List: %v", err)
	}
	if len(bTasks) != 1 || bTasks[0].Title != "B-secret" {
		t.Fatalf("repoB should see only its own task, got %+v", bTasks)
	}

	// A tries to read B's task #1 by id: because A's keys are under repo-a's
	// prefix, A's GetByBoardTaskID resolves A's own task, NEVER B's. Prove the
	// content is A's, not B's.
	got, err := repoA.GetByBoardTaskID(1, 1)
	if err != nil {
		t.Fatalf("repoA.GetByBoardTaskID: %v", err)
	}
	if got.Title == "B-secret" {
		t.Fatal("ISOLATION BREACH: repo A read repo B's task")
	}
	if got.Title != "A-secret" {
		t.Fatalf("repoA task #1 should be A-secret, got %q", got.Title)
	}

	// A mutates task #1 (status). It must affect only A's copy; B's stays.
	if err := repoA.UpdateStatus(1, "done"); err != nil {
		t.Fatalf("repoA.UpdateStatus: %v", err)
	}
	bTask, err := repoB.GetByBoardTaskID(1, 1)
	if err != nil {
		t.Fatalf("repoB.GetByBoardTaskID after A mutate: %v", err)
	}
	if string(bTask.Status) == "done" {
		t.Fatal("ISOLATION BREACH: repo A's update leaked into repo B")
	}
}

// Independent id sequences: each repo has its own atomic counter, so repo B's
// first task is #1 even after repo A already allocated ids.
func TestTenantIsolation_IndependentCounters(t *testing.T) {
	fake := newFakeDDB()
	repoA := storeForRepo(fake, "acme/repo-a")
	repoB := storeForRepo(fake, "acme/repo-b")

	for i := 0; i < 3; i++ {
		if _, err := repoA.Create(1, "a", ""); err != nil {
			t.Fatalf("repoA.Create: %v", err)
		}
	}
	first, err := repoB.Create(1, "b-first", "")
	if err != nil {
		t.Fatalf("repoB.Create: %v", err)
	}
	if first.BoardTaskID != 1 {
		t.Fatalf("repo B first task should be #1 (own counter), got #%d", first.BoardTaskID)
	}
}

// Scenario (e): two different authorized users on the SAME repo see the SAME
// board (shared team kanban). Two independent Store values built with the same
// repo prefix share the partition, so a task one user creates is visible to the
// other.
func TestTenantIsolation_SameRepoSharedBoard(t *testing.T) {
	fake := newFakeDDB()
	// Two separate store instances (as two concurrent Lambda requests would
	// build) for the SAME repo.
	userX := storeForRepo(fake, "acme/team-repo")
	userY := storeForRepo(fake, "acme/team-repo")

	created, err := userX.Create(1, "shared task", "team work")
	if err != nil {
		t.Fatalf("userX.Create: %v", err)
	}

	// User Y lists the same repo board and sees user X's task.
	yTasks, err := userY.List(1)
	if err != nil {
		t.Fatalf("userY.List: %v", err)
	}
	if len(yTasks) != 1 || yTasks[0].Title != "shared task" {
		t.Fatalf("userY should see the shared task, got %+v", yTasks)
	}

	// User Y updates the task; user X sees the update — one shared board.
	if err := userY.UpdateStatus(created.BoardTaskID, "doing"); err != nil {
		t.Fatalf("userY.UpdateStatus: %v", err)
	}
	xTask, err := userX.GetByBoardTaskID(1, created.BoardTaskID)
	if err != nil {
		t.Fatalf("userX.GetByBoardTaskID: %v", err)
	}
	if string(xTask.Status) != "doing" {
		t.Fatalf("shared board: userX should see status 'doing', got %q", xTask.Status)
	}
}
