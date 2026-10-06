package task

import "time"

// ActivityAction classifies one append-only audit event. The canonical types
// live in package task (not store) because store imports task, so store cannot
// define types task-package code would need without a cycle — the same reason
// ErrVersionConflict lives here. Both backends and the store interface name
// these values.
type ActivityAction string

const (
	// ActivityCreated records a task creation.
	ActivityCreated ActivityAction = "created"
	// ActivityStatusChanged records a status transition (e.g. "todo -> doing").
	ActivityStatusChanged ActivityAction = "status_changed"
	// ActivityUpdated records a title/description edit.
	ActivityUpdated ActivityAction = "updated"
	// ActivityPriorityChanged records a priority change.
	ActivityPriorityChanged ActivityAction = "priority_changed"
	// ActivityDeleted records a task deletion (soft or hard).
	ActivityDeleted ActivityAction = "deleted"
	// ActivityRestored records a soft-deleted task being restored.
	ActivityRestored ActivityAction = "restored"
	// ActivityLinked records a link created between two tasks.
	ActivityLinked ActivityAction = "linked"
	// ActivityUnlinked records a link removed between two tasks.
	ActivityUnlinked ActivityAction = "unlinked"
)

// ActivityEvent is one append-only audit record of a task mutation. It is
// pure observability: it records WHO changed WHAT and WHEN, and is NEVER read
// to derive task or board state.
type ActivityEvent struct {
	BoardID     int
	BoardTaskID int
	Action      ActivityAction
	Actor       string // email or sub; may be "" on the local CLI path
	Detail      string // human summary, e.g. "todo -> doing", or the new title
	Timestamp   time.Time
}
