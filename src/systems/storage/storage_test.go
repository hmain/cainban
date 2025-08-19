package storage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNew_ValidPath_CreatesDatabase(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := New(dbPath)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer db.Close()

	if db.Path() != dbPath {
		t.Errorf("Path() = %v, want %v", db.Path(), dbPath)
	}

	// Test that database file was created
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Error("Database file was not created")
	}

	// Test connection
	if err := db.Ping(); err != nil {
		t.Errorf("Ping() error = %v", err)
	}
}

func TestNewMemory_CreatesInMemoryDatabase(t *testing.T) {
	db, err := NewMemory()
	if err != nil {
		t.Fatalf("NewMemory() error = %v", err)
	}
	defer db.Close()

	if db.Path() != ":memory:" {
		t.Errorf("Path() = %v, want :memory:", db.Path())
	}

	// Test connection
	if err := db.Ping(); err != nil {
		t.Errorf("Ping() error = %v", err)
	}
}

func TestInitialize_CreatesTablesAndDefaultBoard(t *testing.T) {
	db, err := NewMemory()
	if err != nil {
		t.Fatalf("NewMemory() error = %v", err)
	}
	defer db.Close()

	// Check that boards table exists and has default board
	var count int
	err = db.Conn().QueryRow("SELECT COUNT(*) FROM boards WHERE id = 1").Scan(&count)
	if err != nil {
		t.Fatalf("Failed to query boards table: %v", err)
	}
	if count != 1 {
		t.Errorf("Expected 1 default board, got %d", count)
	}

	// Check that tasks table exists
	_, err = db.Conn().Exec("SELECT COUNT(*) FROM tasks")
	if err != nil {
		t.Errorf("Tasks table does not exist: %v", err)
	}
}

func TestClose_ClosesConnection(t *testing.T) {
	db, err := NewMemory()
	if err != nil {
		t.Fatalf("NewMemory() error = %v", err)
	}

	// Close the database
	if err := db.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}

	// Ping should fail after close
	if err := db.Ping(); err == nil {
		t.Error("Expected Ping() to fail after Close(), but it succeeded")
	}
}
