package tui

import (
	"fmt"
	"os"
	"strings"
	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/hmain/cainban/src/systems/task"
	"github.com/hmain/cainban/src/systems/board"
	"github.com/hmain/cainban/src/systems/storage"
)

// debugEnabled checks if debug logging is enabled via environment variable
func debugEnabled() bool {
	return os.Getenv("CAINBAN_DEBUG") != ""
}

// debugLog prints debug messages only when debug is enabled
func debugLog(format string, args ...interface{}) {
	if debugEnabled() {
		fmt.Fprintf(os.Stderr, format, args...)
	}
}

// Model represents the main TUI application state
type Model struct {
	// Systems
	taskSystem  *task.System
	boardSystem *board.System
	storage     *storage.DB

	// UI State
	width  int
	height int
	
	// Current view state
	currentView View
	focused     Column
	
	// Task data
	tasks map[task.Status][]*task.Task
	
	// Current board
	currentBoard string
	
	// Selected task indices for each column
	selectedTask map[Column]int
	
	// Viewports for each column (handles scrolling)
	viewports map[Column]viewport.Model
	
	// Styles
	styles Styles
}

// View represents different TUI views
type View int

const (
	ViewKanban View = iota
	ViewHelp
	ViewTaskDetail
)

// Column represents kanban board columns
type Column int

const (
	ColumnTodo Column = iota
	ColumnDoing
	ColumnDone
)

// Styles contains all the styling for the TUI
type Styles struct {
	Base           lipgloss.Style
	Header         lipgloss.Style
	Column         lipgloss.Style
	ColumnTitle    lipgloss.Style
	Task           lipgloss.Style
	TaskSelected   lipgloss.Style
	TaskPriority   map[int]lipgloss.Style
	Help           lipgloss.Style
	StatusBar      lipgloss.Style
}

// NewModel creates a new TUI model
func NewModel(db *storage.DB) *Model {
	taskSystem := task.New(db.Conn())
	boardSystem := board.New()
	
	currentBoard, _ := boardSystem.GetCurrentBoard()
	if currentBoard == "" {
		currentBoard = "default"
	}
	
	// Initialize selectedTask map with all columns set to 0
	selectedTaskMap := make(map[Column]int)
	selectedTaskMap[ColumnTodo] = 0
	selectedTaskMap[ColumnDoing] = 0
	selectedTaskMap[ColumnDone] = 0

	// Initialize viewports for each column
	viewportMap := make(map[Column]viewport.Model)
	viewportMap[ColumnTodo] = viewport.New(30, 20)   // Default size, will be updated
	viewportMap[ColumnDoing] = viewport.New(30, 20)
	viewportMap[ColumnDone] = viewport.New(30, 20)

	model := &Model{
		taskSystem:   taskSystem,
		boardSystem:  boardSystem,
		storage:      db,
		currentView:  ViewKanban,
		focused:      ColumnTodo,
		tasks:        make(map[task.Status][]*task.Task),
		currentBoard: currentBoard,
		selectedTask: selectedTaskMap,
		viewports:    viewportMap,
		styles:       DefaultStyles(), // Will be updated when window size is received
		width:        0, // Will be set by first WindowSizeMsg
		height:       0, // Will be set by first WindowSizeMsg
	}
	
	return model
}

// Init initializes the TUI model
func (m Model) Init() tea.Cmd {
	// DEBUG: Log TUI initialization
	debugLog("[DEBUG] TUI Init() called with dimensions %dx%d\n", m.width, m.height)
	return tea.Batch(
		m.refreshTasks(),
		tea.WindowSize(), // Request current window size immediately
		func() tea.Msg {
			// Initialize viewport content after a short delay to ensure tasks are loaded
			return "init_viewports"
		},
	)
}

// calculateColumnWidth returns the optimal column width based on terminal width
// IMPROVED ALGORITHM - addresses user feedback about narrow columns
func (m Model) calculateColumnWidth() int {
	if m.width <= 0 {
		return 35 // Better fallback default for modern terminals
	}
	
	// DEBUG: Log width calculation
	debugLog("[WIDTH] Terminal width: %d\n", m.width)
	
	// IMPROVED: Dynamic reserved space calculation
	// Base: 3 columns × 2 borders each = 6, plus some spacing
	reservedSpace := 10
	// Adjust based on terminal size for better utilization
	if m.width > 150 {
		reservedSpace = 15
	}
	
	availableWidth := m.width - reservedSpace
	
	// DEBUG: Log available space
	debugLog("[WIDTH] Available width: %d (reserved: %d)\n", availableWidth, reservedSpace)
	
	// Divide remaining space among 3 columns
	columnWidth := availableWidth / 3
	
	// IMPROVED: Dynamic minimum width based on terminal size
	var minWidth int
	if m.width < 90 {
		minWidth = 25 // Very small terminals
	} else if m.width < 120 {
		minWidth = 30 // Small terminals
	} else {
		minWidth = 35 // Normal and large terminals
	}
	
	if columnWidth < minWidth {
		debugLog("[WIDTH] Using minimum width: %d\n", minWidth)
		// Safety check: ensure we don't overflow the terminal
		maxPossible := (m.width - 6) / 3 // Absolute minimum space needed
		if minWidth > maxPossible && maxPossible > 10 {
			debugLog("[WIDTH] Adjusting minimum to prevent overflow: %d\n", maxPossible)
			return maxPossible
		}
		return minWidth
	}
	
	// IMPROVED: Progressive maximum width based on terminal size
	var maxWidth int
	switch {
	case m.width < 100:
		maxWidth = 40 // Very small terminals
	case m.width < 150:
		maxWidth = 50 // Medium terminals
	case m.width < 200:
		maxWidth = 70 // Large terminals
	default:
		maxWidth = 90 // Very large terminals
	}
	
	if columnWidth > maxWidth {
		debugLog("[WIDTH] Using maximum width: %d\n", maxWidth)
		return maxWidth
	}
	
	debugLog("[WIDTH] Calculated column width: %d\n", columnWidth)
	return columnWidth
}

// calculateColumnHeight returns the optimal column height based on terminal height
func (m Model) calculateColumnHeight() int {
	if m.height <= 0 {
		debugLog("[HEIGHT] No terminal height set, using fallback: 20\n")
		return 20 // Fallback to default
	}
	
	// Reserve space for header, status bar, and margins
	reservedHeight := 6 // Header + status bar + margins
	availableHeight := m.height - reservedHeight
	
	debugLog("[HEIGHT] Terminal: %d, Reserved: %d, Available: %d\n", m.height, reservedHeight, availableHeight)
	
	// Ensure minimum height
	minHeight := 10
	if availableHeight < minHeight {
		debugLog("[HEIGHT] Available height %d < min %d, using minimum\n", availableHeight, minHeight)
		return minHeight
	}
	
	debugLog("[HEIGHT] Final column height: %d\n", availableHeight)
	return availableHeight
}

// updateStyles recalculates and updates styles based on current dimensions
// Returns the updated model to work with Bubble Tea's value receiver pattern
func (m Model) updateStyles() Model {
	// DEBUG: Log style update call
	debugLog("[DEBUG] updateStyles() called with dimensions %dx%d\n", m.width, m.height)
	
	columnWidth := m.calculateColumnWidth()
	columnHeight := m.calculateColumnHeight()
	
	// DEBUG: Log calculated dimensions
	debugLog("[DEBUG] Calculated column dimensions: %dx%d\n", columnWidth, columnHeight)
	
	m.styles = DefaultStylesWithDimensions(columnWidth, columnHeight)
	
	// Update viewport dimensions
	for col, vp := range m.viewports {
		vp.Width = columnWidth - 4  // Account for borders and padding
		vp.Height = columnHeight - 3 // Account for title and borders
		m.viewports[col] = vp
		debugLog("[VIEWPORT] Updated viewport %d to %dx%d\n", col, vp.Width, vp.Height)
	}
	
	// DEBUG: Confirm style update
	debugLog("[DEBUG] Styles updated successfully\n")
	
	return m
}

// Public methods for testing

// SetDimensions sets the model dimensions for testing
func (m *Model) SetDimensions(width, height int) {
	m.width = width
	m.height = height
}

// CalculateColumnWidth exports the column width calculation for testing
func (m Model) CalculateColumnWidth() int {
	return m.calculateColumnWidth()
}

// GetMaxVisibleTasks exports the max visible tasks calculation for testing
func (m Model) GetMaxVisibleTasks() int {
	return m.getMaxVisibleTasks()
}

// updateViewportContent updates the content in each column viewport
func (m *Model) updateViewportContent() {
	debugLog("[VIEWPORT] Updating viewport content\n")
	
	// Update each column viewport
	for col := ColumnTodo; col <= ColumnDone; col++ {
		status := m.columnToStatus(col)
		tasks := m.tasks[status]
		
		// Generate content for this column
		var content []string
		
		if len(tasks) == 0 {
			content = append(content, "No tasks")
		} else {
			for i, t := range tasks {
				isSelected := i == m.selectedTask[col] && col == m.focused
				taskLine := m.renderTaskLine(t, isSelected)
				content = append(content, taskLine)
			}
		}
		
		// Set the content in the viewport
		vp := m.viewports[col]
		vp.SetContent(strings.Join(content, "\n"))
		
		// Scroll to keep selected item visible
		m.scrollToSelectedTask(col)
		
		m.viewports[col] = vp
		
		debugLog("[VIEWPORT] Column %d: %d tasks, %d lines\n", col, len(tasks), len(content))
	}
}

// scrollToSelectedTask scrolls the viewport to keep the selected task visible
func (m *Model) scrollToSelectedTask(col Column) {
	selectedIndex := m.selectedTask[col]
	vp := m.viewports[col]
	
	// Calculate the line number of the selected task (0-indexed)
	lineNumber := selectedIndex
	
	// Get viewport dimensions
	viewportHeight := vp.Height
	
	// If the selected task is outside the visible area, scroll to it
	if lineNumber < vp.YOffset {
		// Selected task is above visible area, scroll up
		vp.YOffset = lineNumber
		debugLog("[SCROLL] Scrolling up to line %d for column %d\n", lineNumber, col)
	} else if lineNumber >= vp.YOffset+viewportHeight {
		// Selected task is below visible area, scroll down
		vp.YOffset = lineNumber - viewportHeight + 1
		debugLog("[SCROLL] Scrolling down to line %d for column %d\n", lineNumber, col)
	}
	
	// Ensure we don't scroll past the content
	if vp.YOffset < 0 {
		vp.YOffset = 0
	}
	
	m.viewports[col] = vp
}

// renderTaskLine renders a single task as a simple text line (no styling yet)
func (m Model) renderTaskLine(t *task.Task, selected bool) string {
	// Simple text representation for now
	prefix := "  "
	if selected {
		prefix = "> "
	}
	
	// Priority indicator
	priority := ""
	switch t.Priority {
	case 0:
		priority = " "
	case 1:
		priority = "●"
	case 2:
		priority = "●●"
	case 3:
		priority = "●●●"
	case 4:
		priority = "🔥"
	}
	
	return fmt.Sprintf("%s%s %s", prefix, priority, t.Title)
}

// columnToStatus converts a column to its corresponding task status
func (m Model) columnToStatus(col Column) task.Status {
	switch col {
	case ColumnTodo:
		return task.StatusTodo
	case ColumnDoing:
		return task.StatusDoing
	case ColumnDone:
		return task.StatusDone
	default:
		return task.StatusTodo
	}
}

// getMaxVisibleTasks calculates how many tasks can fit in the visible column area
func (m Model) getMaxVisibleTasks() int {
	columnHeight := m.calculateColumnHeight()
	
	// Account for column title, borders, padding, and scroll indicators
	// Title (1) + top/bottom padding (2) + potential scroll indicators (2)
	reservedLines := 5
	availableLines := columnHeight - reservedLines
	
	// Ensure minimum of 3 tasks are visible
	if availableLines < 3 {
		availableLines = 3
	}
	
	debugLog("[VIRTUAL] Column height: %d, available for tasks: %d\n", columnHeight, availableLines)
	return availableLines
}

// calculateTaskWindow determines which tasks should be visible based on selection
func (m Model) calculateTaskWindow(totalTasks, selectedIndex, maxVisible int) (startIndex, endIndex int) {
	if totalTasks <= maxVisible {
		// All tasks fit in the window
		debugLog("[VIRTUAL] All tasks fit: showing 0-%d\n", totalTasks-1)
		return 0, totalTasks
	}
	
	// FIXED: For the first quarter of the window, always start from the top
	// This ensures initial positioning starts at the top
	quarterWindow := maxVisible / 4
	if selectedIndex < quarterWindow {
		startIndex = 0
		debugLog("[VIRTUAL] Near start (selected=%d < quarter=%d): forcing start=0\n", selectedIndex, quarterWindow)
	} else {
		// Try to center the selected task in the visible window
		halfWindow := maxVisible / 2
		startIndex = selectedIndex - halfWindow
		debugLog("[VIRTUAL] Centering: selected=%d, halfWindow=%d, calculated start=%d\n", selectedIndex, halfWindow, startIndex)
	}
	
	// Adjust if we're at the beginning
	if startIndex < 0 {
		startIndex = 0
		debugLog("[VIRTUAL] Corrected negative start to 0\n")
	}
	
	endIndex = startIndex + maxVisible
	
	// Adjust if we're at the end
	if endIndex > totalTasks {
		endIndex = totalTasks
		startIndex = endIndex - maxVisible
		if startIndex < 0 {
			startIndex = 0
		}
		debugLog("[VIRTUAL] Adjusted for end: endIndex=%d, newStart=%d\n", endIndex, startIndex)
	}
	
	debugLog("[VIRTUAL] Final window: selected=%d, start=%d, end=%d (total=%d, maxVisible=%d)\n", 
		selectedIndex, startIndex, endIndex, totalTasks, maxVisible)
	
	return startIndex, endIndex
}