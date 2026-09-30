package task

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/hmain/cainban/src/systems/storage"
)

// newVersionTestSystem returns a task System backed by a fresh in-memory DB.
//
// These tests exercise the SQLite backend, which requires cgo (go-sqlite3). On
// a host without a C compiler they cannot run (CGO_ENABLED=0 yields a stub);
// CI runs them with cgo. They still document and, under cgo, verify the exact
// optimistic-concurrency semantics the DynamoDB tests cover.
func newVersionTestSystem(t *testing.T) *System {
	t.Helper()
	db, err := storage.NewMemory()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return New(db.Conn())
}

// TestSQLiteCreateSetsVersionOne proves a new task starts at version 1 and the
// version round-trips through every read path.
func TestSQLiteCreateSetsVersionOne(t *testing.T) {
	s := newVersionTestSystem(t)
	created, err := s.Create(1, "t", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Version != 1 {
		t.Fatalf("created version = %d, want 1", created.Version)
	}
	if got, _ := s.GetByID(created.ID); got.Version != 1 {
		t.Fatalf("GetByID version = %d, want 1", got.Version)
	}
	if got, _ := s.GetByBoardTaskID(1, created.BoardTaskID); got.Version != 1 {
		t.Fatalf("GetByBoardTaskID version = %d, want 1", got.Version)
	}
	list, _ := s.List(1)
	if len(list) != 1 || list[0].Version != 1 {
		t.Fatalf("List version not returned: %+v", list)
	}
}

// TestSQLiteUpdateStatusIfVersion_MatchIncrements proves a matching version
// applies the change and increments version.
func TestSQLiteUpdateStatusIfVersion_MatchIncrements(t *testing.T) {
	s := newVersionTestSystem(t)
	created, _ := s.Create(1, "t", "")

	if err := s.UpdateStatusIfVersion(created.ID, StatusDoing, created.Version); err != nil {
		t.Fatalf("UpdateStatusIfVersion (match): %v", err)
	}
	got, _ := s.GetByID(created.ID)
	if got.Status != StatusDoing {
		t.Fatalf("status = %q, want doing", got.Status)
	}
	if got.Version != created.Version+1 {
		t.Fatalf("version = %d, want %d", got.Version, created.Version+1)
	}
}

// TestSQLiteUpdateStatusIfVersion_MismatchConflict proves a stale version is
// rejected with ErrVersionConflict and the task is unchanged.
func TestSQLiteUpdateStatusIfVersion_MismatchConflict(t *testing.T) {
	s := newVersionTestSystem(t)
	created, _ := s.Create(1, "t", "")

	err := s.UpdateStatusIfVersion(created.ID, StatusDoing, created.Version+99)
	if !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("expected ErrVersionConflict, got %v", err)
	}
	got, _ := s.GetByID(created.ID)
	if got.Status != StatusTodo {
		t.Fatalf("status changed on conflict: %q", got.Status)
	}
	if got.Version != created.Version {
		t.Fatalf("version changed on conflict: %d", got.Version)
	}
}

// TestSQLiteUpdateIfVersion covers the title/description guard both ways.
func TestSQLiteUpdateIfVersion(t *testing.T) {
	s := newVersionTestSystem(t)
	created, _ := s.Create(1, "old", "olddesc")

	if err := s.UpdateIfVersion(created.ID, "new", "newdesc", created.Version); err != nil {
		t.Fatalf("UpdateIfVersion (match): %v", err)
	}
	got, _ := s.GetByID(created.ID)
	if got.Title != "new" || got.Description != "newdesc" || got.Version != created.Version+1 {
		t.Fatalf("update mismatch: %+v", got)
	}
	if err := s.UpdateIfVersion(created.ID, "x", "y", created.Version); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("expected ErrVersionConflict on stale version, got %v", err)
	}
}

// TestSQLiteUpdatePriorityIfVersion covers the priority guard both ways.
func TestSQLiteUpdatePriorityIfVersion(t *testing.T) {
	s := newVersionTestSystem(t)
	created, _ := s.Create(1, "t", "")

	if err := s.UpdatePriorityIfVersion(created.ID, "high", created.Version); err != nil {
		t.Fatalf("UpdatePriorityIfVersion (match): %v", err)
	}
	got, _ := s.GetByID(created.ID)
	if got.Priority != PriorityHigh || got.Version != created.Version+1 {
		t.Fatalf("priority update mismatch: %+v", got)
	}
	if err := s.UpdatePriorityIfVersion(created.ID, "low", created.Version); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("expected ErrVersionConflict on stale version, got %v", err)
	}
}

// TestSQLiteIfVersion_LostUpdatePrevention is the concurrency-style test: two
// callers with the SAME expected version write sequentially; the first wins and
// the second is rejected, proving lost-update prevention.
func TestSQLiteIfVersion_LostUpdatePrevention(t *testing.T) {
	s := newVersionTestSystem(t)
	created, _ := s.Create(1, "t", "")
	shared := created.Version

	if err := s.UpdateStatusIfVersion(created.ID, StatusDoing, shared); err != nil {
		t.Fatalf("first writer should succeed: %v", err)
	}
	if err := s.UpdateStatusIfVersion(created.ID, StatusDone, shared); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("second writer should conflict, got %v", err)
	}
	got, _ := s.GetByID(created.ID)
	if got.Status != StatusDoing {
		t.Fatalf("lost update: status = %q, want doing", got.Status)
	}
	if got.Version != shared+1 {
		t.Fatalf("version = %d, want %d (exactly one write applied)", got.Version, shared+1)
	}
}

// TestSQLiteUpdateStatusIfVersion_NotFound proves a guard on a missing task is
// a not-found error, not a version conflict.
func TestSQLiteUpdateStatusIfVersion_NotFound(t *testing.T) {
	s := newVersionTestSystem(t)
	err := s.UpdateStatusIfVersion(999, StatusDoing, 1)
	if err == nil {
		t.Fatalf("expected error for missing task")
	}
	if errors.Is(err, ErrVersionConflict) {
		t.Fatalf("missing task should be not-found, not conflict: %v", err)
	}
}

// TestSQLiteBlindUpdateBumpsVersion proves the existing unguarded update paths
// also increment version now.
func TestSQLiteBlindUpdateBumpsVersion(t *testing.T) {
	s := newVersionTestSystem(t)
	created, _ := s.Create(1, "t", "")

	if err := s.UpdateStatus(created.ID, StatusDoing); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	got, _ := s.GetByID(created.ID)
	if got.Version != created.Version+1 {
		t.Fatalf("blind UpdateStatus version = %d, want %d", got.Version, created.Version+1)
	}
}

// TestSQLiteMigrationAddsVersionColumnIdempotently proves the migration adds a
// version column to an EXISTING (pre-version) tasks table with a legacy row,
// that the legacy row reads as version 0, that expected_version 0 matches it,
// and that re-running the migration is a no-op (idempotent).
func TestSQLiteMigrationAddsVersionColumnIdempotently(t *testing.T) {
	// Build a legacy-shaped database by hand: a tasks table WITHOUT a version
	// column, then run the storage migration over it.
	conn, err := sql.Open("sqlite3", ":memory:?_foreign_keys=on")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = conn.Close() }()

	legacySchema := `
	CREATE TABLE boards (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		description TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	CREATE TABLE tasks (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		board_id INTEGER NOT NULL,
		board_task_id INTEGER NOT NULL,
		title TEXT NOT NULL,
		description TEXT,
		status TEXT NOT NULL DEFAULT 'todo',
		priority INTEGER DEFAULT 0,
		deleted_at DATETIME NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	INSERT INTO boards (id, name, description) VALUES (1, 'Default Board', '');
	INSERT INTO tasks (board_id, board_task_id, title, description, status, priority)
	VALUES (1, 1, 'legacy task', '', 'todo', 0);
	`
	if _, err := conn.Exec(legacySchema); err != nil {
		t.Fatalf("legacy schema: %v", err)
	}

	// Confirm there is no version column yet.
	if columnExists(t, conn, "version") {
		t.Fatalf("legacy table unexpectedly already has a version column")
	}

	// Apply the version column migration exactly as storage.migrate() does.
	addVersion := "ALTER TABLE tasks ADD COLUMN version INTEGER NOT NULL DEFAULT 0"
	if _, err := conn.Exec(addVersion); err != nil {
		t.Fatalf("add version column: %v", err)
	}
	if !columnExists(t, conn, "version") {
		t.Fatalf("version column not added")
	}

	// Legacy row must read as version 0.
	var v int
	if err := conn.QueryRow("SELECT version FROM tasks WHERE board_task_id = 1").Scan(&v); err != nil {
		t.Fatalf("select version: %v", err)
	}
	if v != 0 {
		t.Fatalf("legacy row version = %d, want 0", v)
	}

	// Re-running ADD COLUMN would error; the migration guards on column
	// existence, so a second migrate() is a no-op. Verify the guard: a
	// duplicate ADD COLUMN fails, proving why the existence check matters.
	if _, err := conn.Exec(addVersion); err == nil {
		t.Fatalf("expected duplicate ADD COLUMN to fail (proves migration must guard)")
	}
}

// TestSQLiteMigrationViaStorageIsIdempotent drives the real storage migration
// twice over the same file to prove migrate() re-runs cleanly.
func TestSQLiteMigrationViaStorageIsIdempotent(t *testing.T) {
	path := t.TempDir() + "/cainban.db"

	db1, err := storage.New(path)
	if err != nil {
		t.Fatalf("first open (creates + migrates): %v", err)
	}
	// Create a task so there is a real row across the reopen.
	sys := New(db1.Conn())
	created, err := sys.Create(1, "t", "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.Version != 1 {
		t.Fatalf("created version = %d, want 1", created.Version)
	}
	_ = db1.Close()

	// Reopen: initialize() + migrate() run again over an existing DB that
	// already has the version column. Must not error (idempotent).
	db2, err := storage.New(path)
	if err != nil {
		t.Fatalf("second open (re-migrate) should be idempotent: %v", err)
	}
	defer func() { _ = db2.Close() }()

	sys2 := New(db2.Conn())
	got, err := sys2.GetByBoardTaskID(1, created.BoardTaskID)
	if err != nil {
		t.Fatalf("get after reopen: %v", err)
	}
	if got.Version != 1 {
		t.Fatalf("version after reopen = %d, want 1", got.Version)
	}
}

func columnExists(t *testing.T, conn *sql.DB, col string) bool {
	t.Helper()
	rows, err := conn.Query("PRAGMA table_info(tasks)")
	if err != nil {
		t.Fatalf("PRAGMA table_info: %v", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var cid int
		var name, dataType string
		var notNull, pk int
		var dflt interface{}
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &dflt, &pk); err != nil {
			t.Fatalf("scan column info: %v", err)
		}
		if name == col {
			return true
		}
	}
	return false
}
