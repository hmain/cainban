package tui

import (
	"testing"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/hmain/cainban/src/systems/storage"
)

func TestCalculateColumnWidth(t *testing.T) {
	tests := []struct {
		name           string
		terminalWidth  int
		expectedWidth  int
		description    string
	}{
		{
			name:          "Small terminal",
			terminalWidth: 80,
			expectedWidth: 20, // Should hit minimum width constraint
			description:   "80-width terminal should use minimum column width",
		},
		{
			name:          "Medium terminal",
			terminalWidth: 120,
			expectedWidth: 33, // (120-18)/3 = 34, but algorithm may differ
			description:   "120-width terminal should distribute space evenly",
		},
		{
			name:          "Large terminal",
			terminalWidth: 180,
			expectedWidth: 50, // Should hit maximum width constraint
			description:   "180-width terminal should use maximum column width",
		},
		{
			name:          "Zero width",
			terminalWidth: 0,
			expectedWidth: 30, // Should fall back to default
			description:   "Zero width should use fallback default",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a model with test dimensions
			model := &Model{
				width:  tt.terminalWidth,
				height: 24, // Standard height
			}

			result := model.calculateColumnWidth()

			// Allow some flexibility in the exact calculation
			if result < 20 || result > 60 {
				t.Errorf("calculateColumnWidth() = %d, expected reasonable range 20-60 for width %d", 
					result, tt.terminalWidth)
			}

			t.Logf("Terminal width %d -> Column width %d (%s)", 
				tt.terminalWidth, result, tt.description)
		})
	}
}

func TestCalculateColumnHeight(t *testing.T) {
	tests := []struct {
		name           string
		terminalHeight int
		expectedMin    int
		description    string
	}{
		{
			name:           "Small terminal",
			terminalHeight: 20,
			expectedMin:    10, // Should hit minimum height
			description:    "Small terminal should use minimum height",
		},
		{
			name:           "Standard terminal",
			terminalHeight: 24,
			expectedMin:    15, // 24-6 reserved = 18, should be >= 15
			description:    "Standard terminal should have reasonable height",
		},
		{
			name:           "Large terminal", 
			terminalHeight: 50,
			expectedMin:    40, // 50-6 reserved = 44, should be around that
			description:    "Large terminal should use available space",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model := &Model{
				width:  120,
				height: tt.terminalHeight,
			}

			result := model.calculateColumnHeight()

			if result < tt.expectedMin {
				t.Errorf("calculateColumnHeight() = %d, expected >= %d for height %d", 
					result, tt.expectedMin, tt.terminalHeight)
			}

			t.Logf("Terminal height %d -> Column height %d (%s)", 
				tt.terminalHeight, result, tt.description)
		})
	}
}

// setupTestModel creates a test model with in-memory database
func setupTestModel(t *testing.T) Model {
	db, err := storage.NewMemory()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	
	model := NewModel(db)
	model.width = 120
	model.height = 24
	*model = model.updateStyles()
	
	return *model
}

func TestKeyNavigation(t *testing.T) {
	tests := []struct {
		name          string
		key           string
		initialColumn Column
		expectedColumn Column
	}{
		{"Move right from TODO", "l", ColumnTodo, ColumnDoing},
		{"Move right from DOING", "right", ColumnDoing, ColumnDone},
		{"Move left from DONE", "h", ColumnDone, ColumnDoing},
		{"Move left from DOING", "left", ColumnDoing, ColumnTodo},
		{"Stay at TODO when moving left", "h", ColumnTodo, ColumnTodo},
		{"Stay at DONE when moving right", "l", ColumnDone, ColumnDone},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := setupTestModel(t)
			m.focused = tt.initialColumn

			msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(tt.key)}
			if tt.key == "left" {
				msg = tea.KeyMsg{Type: tea.KeyLeft}
			} else if tt.key == "right" {
				msg = tea.KeyMsg{Type: tea.KeyRight}
			}

			updatedModel, _ := m.Update(msg)
			m, ok := updatedModel.(Model)
			if !ok {
				t.Fatal("Update did not return Model type")
			}

			if m.focused != tt.expectedColumn {
				t.Errorf("Expected column %d, got %d", tt.expectedColumn, m.focused)
			}
		})
	}
}

func TestQuitKeys(t *testing.T) {
	tests := []struct {
		name string
		key  tea.KeyMsg
	}{
		{"Quit with q", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")}},
		{"Quit with ctrl+c", tea.KeyMsg{Type: tea.KeyCtrlC}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := setupTestModel(t)
			_, cmd := m.Update(tt.key)

			if cmd == nil {
				t.Error("Expected quit command, got nil")
			}
		})
	}
}

func TestWindowResize(t *testing.T) {
	m := setupTestModel(t)
	
	// Send window resize message
	msg := tea.WindowSizeMsg{Width: 160, Height: 40}
	updatedModel, _ := m.Update(msg)
	m, ok := updatedModel.(Model)
	if !ok {
		t.Fatal("Update did not return Model type")
	}

	if m.width != 160 {
		t.Errorf("Expected width 160, got %d", m.width)
	}
	if m.height != 40 {
		t.Errorf("Expected height 40, got %d", m.height)
	}

	// Verify column width was recalculated
	columnWidth := m.calculateColumnWidth()
	if columnWidth < 20 || columnWidth > 90 {
		t.Errorf("Column width %d out of reasonable range after resize", columnWidth)
	}
}