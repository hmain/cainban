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

import (
	"time"

	"github.com/hmain/cainban/src/systems/task"
)

// ErrVersionConflict is returned by the *IfVersion methods when the caller's
// expected version does not match the version currently stored for a task.
//
// It signals an optimistic-concurrency lost-update prevention: the task was
// modified since the caller last read it. Callers should re-read the task and
// retry. Backends wrap this sentinel with a message that includes the task id,
// so use errors.Is(err, store.ErrVersionConflict) to detect it.
//
// The canonical value is defined in package task (store imports task, so the
// value cannot live here without forcing task to import store — a cycle). This
// is an alias of that single value, so a conflict from either backend matches.
var ErrVersionConflict = task.ErrVersionConflict

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

	// UpdateStatusIfVersion / UpdateIfVersion / UpdatePriorityIfVersion are the
	// optimistic-concurrency variants: they apply the field change only if the
	// task's stored version equals expectedVersion, atomically incrementing the
	// version on success. On a version mismatch they return
	// ErrVersionConflict (wrapped with the task id). Legacy rows with no
	// recorded version read as version 0, so expectedVersion == 0 matches them.
	UpdateStatusIfVersion(id int, status task.Status, expectedVersion int) error
	UpdateIfVersion(id int, title, description string, expectedVersion int) error
	UpdatePriorityIfVersion(id int, priority interface{}, expectedVersion int) error

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
	// ListLinks returns every link on a board (both directions), newest first.
	// Unlike GetTaskLinks it is not scoped to one task, so a caller (e.g. the
	// SPA board overlay) can fetch the whole dependency graph in one call.
	ListLinks(boardID int) ([]task.TaskLink, error)

	// RecordActivity appends an append-only audit event for a board. It is
	// best-effort observability and MUST NOT be read for task/board state.
	RecordActivity(ev task.ActivityEvent) error
	// ListActivity returns recent events for a board, newest first, capped at
	// limit (<=0 or >200 means a sane default of 50). If boardTaskID > 0, only
	// events for that task are returned. When since is non-zero, only events
	// strictly newer than since are returned (for SPA delta polling); a zero
	// since (time.Time{}) means "no filter" and is behavior-preserving.
	ListActivity(boardID, boardTaskID, limit int, since time.Time) ([]task.ActivityEvent, error)
}

// Compile-time assertion that the SQLite implementation satisfies the contract
// lives in the task package's own test to avoid an import cycle here.

// BoardSummary is the backend-neutral view of a board as the board tools
// (list_boards, change_board) expose it. The canonical type lives in package
// task (store imports task, and dynamo returns task.BoardSummary without
// importing store — this alias keeps store callers using store.BoardSummary).
type BoardSummary = task.BoardSummary

// ErrBoardNotFound is returned by BoardStore.ResolveBoard when the selector
// matches no board in the current scope. Handlers use errors.Is to format a
// scope-aware "not found" message. Alias of the canonical value in package
// task so a not-found from either backend matches.
var ErrBoardNotFound = task.ErrBoardNotFound

// BoardStore lists and resolves the boards visible to a SINGLE request. It is
// scoped the same way TaskStore is: the DynamoDB implementation is constructed
// with the request's tenant partition prefix, so a request authorized for repo
// A can never see repo B's boards. The SQLite implementation ignores the prefix
// and serves the local ~/.cainban tree (single-user mode).
//
// Both backends satisfy this interface and callers depend only on it, so the
// two board tools behave correctly in both deployments with no handler-level
// branch on CAINBAN_BACKEND — mirroring the TaskStore design.
type BoardStore interface {
	// ListBoards returns every board in the current scope.
	ListBoards() ([]BoardSummary, error)
	// ResolveBoard maps a selector (a board name, or a numeric id as a string)
	// to a concrete board, or ErrBoardNotFound when nothing matches. Used by
	// change_board.
	ResolveBoard(selector string) (BoardSummary, error)
}
