// Package store defines the backend-neutral seam between cainban's business
// logic (MCP handlers, CLI, TUI) and the concrete persistence layer.
//
// Phase 1 wired the MCP handlers directly to the SQLite-backed *task.System.
// Phase 2 introduces a DynamoDB backend that must be selectable without
// touching those handlers, so the method set they rely on is lifted into the
// TaskStore interface here. Both the SQLite implementation (task.System) and
// the DynamoDB implementation (dynamo.Store) satisfy TaskStore, and callers
// depend only on this interface.
//
// The interface is intentionally the SAME shape as the existing *task.System
// methods so the SQLite type satisfies it with zero changes, keeping the port
// behavior-preserving.
package store

import "github.com/hmain/cainban/src/systems/task"

// TaskStore is the persistence contract cainban's handlers and CLI depend on.
//
// Every method is board-scoped through an explicit boardID (Phase 2 keeps the
// single-tenant default board = 1). The DynamoDB key design reserves room for a
// Phase 3 REPO#<owner>/<repo> tenant prefix in front of the board without any
// signature change here: the tenant becomes part of how a backend is
// constructed, not part of these calls.
type TaskStore interface {
	// Create adds a task with the default (none) priority.
	Create(boardID int, title, description string) (*task.Task, error)
	// CreateWithPriority adds a task with the given priority (int or name).
	CreateWithPriority(boardID int, title, description string, priority interface{}) (*task.Task, error)

	// GetByID looks up a task by its internal global ID.
	GetByID(id int) (*task.Task, error)
	// GetByBoardTaskID looks up a task by its board-scoped 1..N ID.
	GetByBoardTaskID(boardID, boardTaskID int) (*task.Task, error)

	// List returns all non-deleted tasks for a board.
	List(boardID int) ([]*task.Task, error)
	// ListByStatus returns non-deleted tasks in a board filtered by status.
	ListByStatus(boardID int, status task.Status) ([]*task.Task, error)

	// UpdateStatus / Update / UpdatePriority mutate a task by internal ID.
	UpdateStatus(id int, status task.Status) error
	Update(id int, title, description string) error
	UpdatePriority(id int, priority interface{}) error

	// SearchTasks fuzzy-matches task titles within a board.
	SearchTasks(boardID int, query string) ([]*task.Task, error)
	// FindTaskByFuzzyID resolves a board-scoped ID or fuzzy title to one task.
	FindTaskByFuzzyID(boardID int, idOrQuery string) (*task.Task, error)

	// Soft/hard delete and restore preserve the SQLite semantics.
	Delete(id int) error
	SoftDelete(id int) error
	HardDelete(id int) error
	RestoreTask(id int) error

	// Task-link relationships.
	LinkTasks(fromTaskID, toTaskID int, linkType task.LinkType) error
	UnlinkTasks(fromTaskID, toTaskID int, linkType task.LinkType) error
	GetTaskLinks(taskID int) ([]task.TaskLink, error)
}

// Compile-time assertion that the SQLite implementation satisfies the contract
// lives in the task package's own test to avoid an import cycle here.
