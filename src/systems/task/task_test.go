package task

import (
	"strings"
	"testing"
)

// setupTestSystem is commented out until storage system integration is complete
// TODO: Update when storage system is integrated
// func setupTestSystem(t *testing.T) *System {
//	return nil
// }

func TestValidateTitle(t *testing.T) {
	tests := []struct {
		name    string
		title   string
		wantErr bool
	}{
		{"valid title", "Fix bug in parser", false},
		{"empty title", "", true},
		{"whitespace only", "   ", true},
		{"too long", strings.Repeat("a", 256), true},
		{"valid with whitespace", "  Valid title  ", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateTitle(tt.title)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateTitle() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestIsValidStatus(t *testing.T) {
	tests := []struct {
		name   string
		status string
		want   bool
	}{
		{"todo", "todo", true},
		{"doing", "doing", true},
		{"done", "done", true},
		{"invalid", "invalid", false},
		{"empty", "", false},
		{"uppercase", "TODO", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsValidStatus(tt.status); got != tt.want {
				t.Errorf("IsValidStatus() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValidStatuses(t *testing.T) {
	statuses := ValidStatuses()
	expected := []Status{StatusTodo, StatusDoing, StatusDone}

	if len(statuses) != len(expected) {
		t.Errorf("ValidStatuses() length = %v, want %v", len(statuses), len(expected))
	}

	for i, status := range statuses {
		if status != expected[i] {
			t.Errorf("ValidStatuses()[%d] = %v, want %v", i, status, expected[i])
		}
	}
}

// Integration tests are now implemented in tests/integration/task_storage_test.go
// These tests verify:
// ✅ Create task with valid data
// ✅ Get task by ID
// ✅ List tasks by board
// ✅ List tasks by status
// ✅ Update task status
// ✅ Update task details
// ✅ Delete task
// ✅ Error handling for non-existent tasks
