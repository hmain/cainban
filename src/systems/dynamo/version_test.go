package dynamo

import (
	"errors"
	"testing"

	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/hmain/cainban/src/systems/task"
)

// TestCreateSetsVersionOne proves a newly created task starts at version 1 and
// that the version round-trips through a read.
func TestCreateSetsVersionOne(t *testing.T) {
	s := newTestStore()
	created, err := s.Create(1, "t", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Version != 1 {
		t.Fatalf("created version = %d, want 1", created.Version)
	}
	got, err := s.GetByBoardTaskID(1, 1)
	if err != nil {
		t.Fatalf("GetByBoardTaskID: %v", err)
	}
	if got.Version != 1 {
		t.Fatalf("read-back version = %d, want 1", got.Version)
	}
}

// TestUpdateStatusIfVersion_MatchIncrements proves a matching expected version
// applies the change and atomically increments the stored version.
func TestUpdateStatusIfVersion_MatchIncrements(t *testing.T) {
	s := newTestStore()
	created, _ := s.Create(1, "t", "")

	if err := s.UpdateStatusIfVersion(created.ID, task.StatusDoing, created.Version); err != nil {
		t.Fatalf("UpdateStatusIfVersion (match): %v", err)
	}

	got, _ := s.GetByBoardTaskID(1, created.ID)
	if got.Status != task.StatusDoing {
		t.Fatalf("status = %q, want doing", got.Status)
	}
	if got.Version != created.Version+1 {
		t.Fatalf("version = %d, want %d", got.Version, created.Version+1)
	}
}

// TestUpdateStatusIfVersion_MismatchConflict proves a stale expected version is
// rejected with ErrVersionConflict and leaves the task unchanged.
func TestUpdateStatusIfVersion_MismatchConflict(t *testing.T) {
	s := newTestStore()
	created, _ := s.Create(1, "t", "")

	err := s.UpdateStatusIfVersion(created.ID, task.StatusDoing, created.Version+99)
	if !errors.Is(err, task.ErrVersionConflict) {
		t.Fatalf("expected ErrVersionConflict, got %v", err)
	}

	got, _ := s.GetByBoardTaskID(1, created.ID)
	if got.Status != task.StatusTodo {
		t.Fatalf("status changed on conflict: %q", got.Status)
	}
	if got.Version != created.Version {
		t.Fatalf("version changed on conflict: %d", got.Version)
	}
}

// TestUpdateIfVersion_MatchAndMismatch covers the title/description guard.
func TestUpdateIfVersion_MatchAndMismatch(t *testing.T) {
	s := newTestStore()
	created, _ := s.Create(1, "old", "olddesc")

	if err := s.UpdateIfVersion(created.ID, "new", "newdesc", created.Version); err != nil {
		t.Fatalf("UpdateIfVersion (match): %v", err)
	}
	got, _ := s.GetByBoardTaskID(1, created.ID)
	if got.Title != "new" || got.Description != "newdesc" {
		t.Fatalf("fields not updated: %+v", got)
	}
	if got.Version != created.Version+1 {
		t.Fatalf("version = %d, want %d", got.Version, created.Version+1)
	}

	// Re-using the original (now stale) version must conflict.
	if err := s.UpdateIfVersion(created.ID, "x", "y", created.Version); !errors.Is(err, task.ErrVersionConflict) {
		t.Fatalf("expected ErrVersionConflict on stale version, got %v", err)
	}
}

// TestUpdatePriorityIfVersion_MatchAndMismatch covers the priority guard.
func TestUpdatePriorityIfVersion_MatchAndMismatch(t *testing.T) {
	s := newTestStore()
	created, _ := s.Create(1, "t", "")

	if err := s.UpdatePriorityIfVersion(created.ID, "high", created.Version); err != nil {
		t.Fatalf("UpdatePriorityIfVersion (match): %v", err)
	}
	got, _ := s.GetByBoardTaskID(1, created.ID)
	if got.Priority != task.PriorityHigh {
		t.Fatalf("priority = %d, want high(%d)", got.Priority, task.PriorityHigh)
	}
	if got.Version != created.Version+1 {
		t.Fatalf("version = %d, want %d", got.Version, created.Version+1)
	}

	if err := s.UpdatePriorityIfVersion(created.ID, "low", created.Version); !errors.Is(err, task.ErrVersionConflict) {
		t.Fatalf("expected ErrVersionConflict on stale version, got %v", err)
	}
}

// TestIfVersion_LostUpdatePrevention is the concurrency-style test: two callers
// read the SAME version, then both write with that expected version. The first
// succeeds; the second — whose read is now stale — gets ErrVersionConflict.
// This is exactly the lost-update the guard exists to prevent.
func TestIfVersion_LostUpdatePrevention(t *testing.T) {
	s := newTestStore()
	created, _ := s.Create(1, "t", "")
	sharedVersion := created.Version // both "readers" saw this version

	// First writer wins.
	if err := s.UpdateStatusIfVersion(created.ID, task.StatusDoing, sharedVersion); err != nil {
		t.Fatalf("first writer should succeed: %v", err)
	}
	// Second writer used the same (now stale) version -> conflict.
	err := s.UpdateStatusIfVersion(created.ID, task.StatusDone, sharedVersion)
	if !errors.Is(err, task.ErrVersionConflict) {
		t.Fatalf("second writer should conflict, got %v", err)
	}

	got, _ := s.GetByBoardTaskID(1, created.ID)
	if got.Status != task.StatusDoing {
		t.Fatalf("lost update: status = %q, want doing (first writer)", got.Status)
	}
	if got.Version != sharedVersion+1 {
		t.Fatalf("version = %d, want %d (exactly one write applied)", got.Version, sharedVersion+1)
	}
}

// TestUpdateStatusIfVersion_LegacyItemExpectedZero proves a legacy item with NO
// version attribute is treated as version 0, so an expected_version of 0
// matches it and the write succeeds (bumping it to 1).
func TestUpdateStatusIfVersion_LegacyItemExpectedZero(t *testing.T) {
	fake := newFakeDDB()
	s := New(fake, "cainban-test")

	// Seed a legacy task item directly, WITHOUT a version attribute.
	pk := s.boardPK(1)
	sk := taskSK(1)
	fake.items[keyOf(pk, sk)] = map[string]ddbtypes.AttributeValue{
		"PK":            &ddbtypes.AttributeValueMemberS{Value: pk},
		"SK":            &ddbtypes.AttributeValueMemberS{Value: sk},
		"board_id":      &ddbtypes.AttributeValueMemberN{Value: "1"},
		"board_task_id": &ddbtypes.AttributeValueMemberN{Value: "1"},
		"title":         &ddbtypes.AttributeValueMemberS{Value: "legacy"},
		"status":        &ddbtypes.AttributeValueMemberS{Value: "todo"},
		"priority":      &ddbtypes.AttributeValueMemberN{Value: "0"},
	}

	// A read reports version 0 for the legacy item.
	got, err := s.GetByBoardTaskID(1, 1)
	if err != nil {
		t.Fatalf("GetByBoardTaskID: %v", err)
	}
	if got.Version != 0 {
		t.Fatalf("legacy item version = %d, want 0", got.Version)
	}

	// expected_version == 0 matches the missing attribute -> succeeds, bumps to 1.
	if err := s.UpdateStatusIfVersion(1, task.StatusDoing, 0); err != nil {
		t.Fatalf("legacy expected=0 should succeed: %v", err)
	}
	got, _ = s.GetByBoardTaskID(1, 1)
	if got.Status != task.StatusDoing {
		t.Fatalf("status = %q, want doing", got.Status)
	}
	if got.Version != 1 {
		t.Fatalf("version = %d, want 1 after first bump", got.Version)
	}
}

// TestUpdateStatusIfVersion_NotFound proves a guard against a missing task
// returns a not-found error (not a version conflict).
func TestUpdateStatusIfVersion_NotFound(t *testing.T) {
	s := newTestStore()
	err := s.UpdateStatusIfVersion(999, task.StatusDoing, 1)
	if err == nil {
		t.Fatalf("expected error for missing task")
	}
	if errors.Is(err, task.ErrVersionConflict) {
		t.Fatalf("missing task should be not-found, not version conflict: %v", err)
	}
}

// TestBlindUpdateBumpsVersion proves the existing (unguarded) update path also
// increments version now, so every write advances the counter.
func TestBlindUpdateBumpsVersion(t *testing.T) {
	s := newTestStore()
	created, _ := s.Create(1, "t", "")

	if err := s.UpdateStatus(created.ID, task.StatusDoing); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	got, _ := s.GetByBoardTaskID(1, created.ID)
	if got.Version != created.Version+1 {
		t.Fatalf("blind update version = %d, want %d", got.Version, created.Version+1)
	}
}
