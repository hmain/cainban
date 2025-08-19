package task

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// Status represents the state of a task
type Status string

const (
	StatusTodo  Status = "todo"
	StatusDoing Status = "doing"
	StatusDone  Status = "done"
)

// Priority levels
const (
	PriorityNone   = 0
	PriorityLow    = 1
	PriorityMedium = 2
	PriorityHigh   = 3
	PriorityCritical = 4
)

// PriorityNames maps priority levels to names
var PriorityNames = map[int]string{
	PriorityNone:     "none",
	PriorityLow:      "low",
	PriorityMedium:   "medium",
	PriorityHigh:     "high",
	PriorityCritical: "critical",
}

// PriorityLevels maps names to priority levels
var PriorityLevels = map[string]int{
	"none":     PriorityNone,
	"low":      PriorityLow,
	"medium":   PriorityMedium,
	"high":     PriorityHigh,
	"critical": PriorityCritical,
}

// IsValidPriority checks if a priority level is valid
func IsValidPriority(priority interface{}) bool {
	switch p := priority.(type) {
	case int:
		return p >= PriorityNone && p <= PriorityCritical
	case string:
		_, exists := PriorityLevels[strings.ToLower(p)]
		return exists
	default:
		return false
	}
}

// ParsePriority converts string or int to priority level
func ParsePriority(priority interface{}) (int, error) {
	switch p := priority.(type) {
	case int:
		if !IsValidPriority(p) {
			return 0, fmt.Errorf("invalid priority level: %d (must be 0-4)", p)
		}
		return p, nil
	case string:
		level, exists := PriorityLevels[strings.ToLower(p)]
		if !exists {
			return 0, fmt.Errorf("invalid priority name: %s (must be none, low, medium, high, critical)", p)
		}
		return level, nil
	default:
		return 0, fmt.Errorf("priority must be int or string")
	}
}

// GetPriorityName returns the name for a priority level
func GetPriorityName(priority int) string {
	if name, exists := PriorityNames[priority]; exists {
		return name
	}
	return "unknown"
}

// ValidStatuses returns all valid task statuses
func ValidStatuses() []Status {
	return []Status{StatusTodo, StatusDoing, StatusDone}
}

// IsValidStatus checks if a status is valid
func IsValidStatus(status string) bool {
	for _, s := range ValidStatuses() {
		if string(s) == status {
			return true
		}
	}
	return false
}

// Task represents a kanban task
type Task struct {
	ID          int       `json:"id"`
	BoardID     int       `json:"board_id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Status      Status    `json:"status"`
	Priority    int       `json:"priority"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// System handles task operations
type System struct {
	db *sql.DB
}

// New creates a new task system
func New(db *sql.DB) *System {
	return &System{db: db}
}

// Create creates a new task
func (s *System) Create(boardID int, title, description string) (*Task, error) {
	if err := ValidateTitle(title); err != nil {
		return nil, err
	}

	query := `
		INSERT INTO tasks (board_id, title, description, status, priority)
		VALUES (?, ?, ?, ?, ?)
		RETURNING id, created_at, updated_at
	`

	var task Task
	err := s.db.QueryRow(query, boardID, title, description, StatusTodo, 0).Scan(
		&task.ID, &task.CreatedAt, &task.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create task: %w", err)
	}

	task.BoardID = boardID
	task.Title = title
	task.Description = description
	task.Status = StatusTodo
	task.Priority = 0

	return &task, nil
}

// GetByID retrieves a task by ID
func (s *System) GetByID(id int) (*Task, error) {
	query := `
		SELECT id, board_id, title, description, status, priority, created_at, updated_at
		FROM tasks WHERE id = ?
	`

	var task Task
	err := s.db.QueryRow(query, id).Scan(
		&task.ID, &task.BoardID, &task.Title, &task.Description,
		&task.Status, &task.Priority, &task.CreatedAt, &task.UpdatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("task with id %d not found", id)
		}
		return nil, fmt.Errorf("failed to get task: %w", err)
	}

	return &task, nil
}

// List retrieves all tasks for a board
func (s *System) List(boardID int) ([]*Task, error) {
	query := `
		SELECT id, board_id, title, description, status, priority, created_at, updated_at
		FROM tasks WHERE board_id = ?
		ORDER BY priority DESC, created_at ASC
	`

	rows, err := s.db.Query(query, boardID)
	if err != nil {
		return nil, fmt.Errorf("failed to list tasks: %w", err)
	}
	defer rows.Close()

	var tasks []*Task
	for rows.Next() {
		var task Task
		err := rows.Scan(
			&task.ID, &task.BoardID, &task.Title, &task.Description,
			&task.Status, &task.Priority, &task.CreatedAt, &task.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan task: %w", err)
		}
		tasks = append(tasks, &task)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating tasks: %w", err)
	}

	return tasks, nil
}

// ListByStatus retrieves tasks by status for a board
func (s *System) ListByStatus(boardID int, status Status) ([]*Task, error) {
	query := `
		SELECT id, board_id, title, description, status, priority, created_at, updated_at
		FROM tasks WHERE board_id = ? AND status = ?
		ORDER BY priority DESC, created_at ASC
	`

	rows, err := s.db.Query(query, boardID, status)
	if err != nil {
		return nil, fmt.Errorf("failed to list tasks by status: %w", err)
	}
	defer rows.Close()

	var tasks []*Task
	for rows.Next() {
		var task Task
		err := rows.Scan(
			&task.ID, &task.BoardID, &task.Title, &task.Description,
			&task.Status, &task.Priority, &task.CreatedAt, &task.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan task: %w", err)
		}
		tasks = append(tasks, &task)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating tasks: %w", err)
	}

	return tasks, nil
}

// UpdateStatus updates a task's status
func (s *System) UpdateStatus(id int, status Status) error {
	if !IsValidStatus(string(status)) {
		return fmt.Errorf("invalid status: %s", status)
	}

	query := `
		UPDATE tasks 
		SET status = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`

	result, err := s.db.Exec(query, status, id)
	if err != nil {
		return fmt.Errorf("failed to update task status: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("task with id %d not found", id)
	}

	return nil
}

// Update updates a task's title and description
func (s *System) Update(id int, title, description string) error {
	if err := ValidateTitle(title); err != nil {
		return err
	}

	query := `
		UPDATE tasks 
		SET title = ?, description = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`

	result, err := s.db.Exec(query, title, description, id)
	if err != nil {
		return fmt.Errorf("failed to update task: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("task with id %d not found", id)
	}

	return nil
}

// Delete deletes a task
func (s *System) Delete(id int) error {
	query := `DELETE FROM tasks WHERE id = ?`

	result, err := s.db.Exec(query, id)
	if err != nil {
		return fmt.Errorf("failed to delete task: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("task with id %d not found", id)
	}

	return nil
}

// UpdatePriority updates a task's priority
func (s *System) UpdatePriority(id int, priority interface{}) error {
	priorityLevel, err := ParsePriority(priority)
	if err != nil {
		return err
	}

	query := `
		UPDATE tasks 
		SET priority = ?, updated_at = CURRENT_TIMESTAMP 
		WHERE id = ?
	`

	result, err := s.db.Exec(query, priorityLevel, id)
	if err != nil {
		return fmt.Errorf("failed to update task priority: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check update result: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("task with ID %d not found", id)
	}

	return nil
}

// ValidateTitle validates a task title
func ValidateTitle(title string) error {
	title = strings.TrimSpace(title)
	if title == "" {
		return fmt.Errorf("task title cannot be empty")
	}
	if len(title) > 255 {
		return fmt.Errorf("task title cannot exceed 255 characters")
	}
	return nil
}
