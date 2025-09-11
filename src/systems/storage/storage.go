package storage

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3"
)

// DB wraps the SQLite database connection
type DB struct {
	conn *sql.DB
	path string
}

// New creates a new database connection
func New(dbPath string) (*DB, error) {
	// Create directory if it doesn't exist
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create database directory: %w", err)
	}

	conn, err := sql.Open("sqlite3", dbPath+"?_foreign_keys=on&_journal_mode=WAL")
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	db := &DB{
		conn: conn,
		path: dbPath,
	}

	if err := db.initialize(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to initialize database: %w", err)
	}

	return db, nil
}

// NewMemory creates an in-memory database for testing
func NewMemory() (*DB, error) {
	conn, err := sql.Open("sqlite3", ":memory:?_foreign_keys=on")
	if err != nil {
		return nil, fmt.Errorf("failed to open memory database: %w", err)
	}

	db := &DB{
		conn: conn,
		path: ":memory:",
	}

	if err := db.initialize(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to initialize memory database: %w", err)
	}

	return db, nil
}

// initialize creates the database schema
func (db *DB) initialize() error {
	schema := `
	CREATE TABLE IF NOT EXISTS boards (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		description TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS tasks (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		board_id INTEGER NOT NULL,
		title TEXT NOT NULL,
		description TEXT,
		status TEXT NOT NULL DEFAULT 'todo',
		priority INTEGER DEFAULT 0,
		deleted_at DATETIME NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (board_id) REFERENCES boards(id) ON DELETE CASCADE
	);

	CREATE TABLE IF NOT EXISTS task_links (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		from_task_id INTEGER NOT NULL,
		to_task_id INTEGER NOT NULL,
		link_type TEXT NOT NULL DEFAULT 'blocks',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (from_task_id) REFERENCES tasks(id) ON DELETE CASCADE,
		FOREIGN KEY (to_task_id) REFERENCES tasks(id) ON DELETE CASCADE,
		UNIQUE(from_task_id, to_task_id, link_type)
	);

	CREATE INDEX IF NOT EXISTS idx_tasks_board_id ON tasks(board_id);
	CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status);
	CREATE INDEX IF NOT EXISTS idx_task_links_from ON task_links(from_task_id);
	CREATE INDEX IF NOT EXISTS idx_task_links_to ON task_links(to_task_id);

	-- Create default board if none exists
	INSERT OR IGNORE INTO boards (id, name, description) 
	VALUES (1, 'Default Board', 'Default kanban board');
	`

	_, err := db.conn.Exec(schema)
	if err != nil {
		return err
	}

	// Handle migration for existing databases
	return db.migrate()
}

// migrate handles database migrations for existing databases
func (db *DB) migrate() error {
	// Check if deleted_at column exists
	rows, err := db.conn.Query("PRAGMA table_info(tasks)")
	if err != nil {
		return fmt.Errorf("failed to get table info: %w", err)
	}
	defer rows.Close()

	hasDeletedAt := false
	for rows.Next() {
		var cid int
		var name, dataType string
		var notNull, pk int
		var defaultValue interface{}

		err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &pk)
		if err != nil {
			return fmt.Errorf("failed to scan column info: %w", err)
		}

		if name == "deleted_at" {
			hasDeletedAt = true
			break
		}
	}

	// Add deleted_at column if it doesn't exist
	if !hasDeletedAt {
		_, err = db.conn.Exec("ALTER TABLE tasks ADD COLUMN deleted_at DATETIME NULL")
		if err != nil {
			return fmt.Errorf("failed to add deleted_at column: %w", err)
		}
	}

	return nil
}

// Close closes the database connection
func (db *DB) Close() error {
	if db.conn != nil {
		return db.conn.Close()
	}
	return nil
}

// Conn returns the underlying database connection
func (db *DB) Conn() *sql.DB {
	return db.conn
}

// Path returns the database file path
func (db *DB) Path() string {
	return db.path
}

// Ping tests the database connection
func (db *DB) Ping() error {
	return db.conn.Ping()
}
