package tui

import (
	"time"
	"github.com/charmbracelet/bubbletea"
	"github.com/hmain/cainban/src/systems/task"
)

// Update handles all TUI state updates based on messages
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		// DEBUG: Log window resize events
		debugLog("[DEBUG] WindowSizeMsg: %dx%d\n", msg.Width, msg.Height)
		
		oldWidth, oldHeight := m.width, m.height
		oldColumnWidth := m.calculateColumnWidth()
		
		// Update dimensions
		m.width = msg.Width
		m.height = msg.Height
		
		// DEBUG: Log dimension changes
		debugLog("[DEBUG] Dimensions changed: %dx%d -> %dx%d\n", oldWidth, oldHeight, m.width, m.height)
		
		// Recalculate styles when window is resized - CRITICAL FIX
		m = m.updateStyles()
		newColumnWidth := m.calculateColumnWidth()
		
		// DEBUG: Log column width calculation
		debugLog("[DEBUG] Column width: %d -> %d\n", oldColumnWidth, newColumnWidth)
		
		// Force a re-render by returning a command that does nothing
		// This ensures the UI is updated with new dimensions
		return m, tea.Tick(1, func(_ time.Time) tea.Msg { return nil })
		
	case tea.KeyMsg:
		return m.handleKeyPress(msg)
		
	case TasksRefreshedMsg:
		m.tasks = msg.Tasks
		// Update viewport content when tasks change
		m.updateViewportContent()
		return m, nil
		
	case ErrorMsg:
		// Handle errors (could show in status bar)
		return m, nil
		
	case string:
		if msg == "init_viewports" {
			// Initialize viewport content
			m.updateViewportContent()
			return m, nil
		}
	}
	
	return m, nil
}

// handleKeyPress processes keyboard input
func (m Model) handleKeyPress(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.currentView {
	case ViewKanban:
		return m.handleKanbanKeys(msg)
	case ViewHelp:
		return m.handleHelpKeys(msg)
	case ViewTaskDetail:
		return m.handleTaskDetailKeys(msg)
	}
	
	return m, nil
}

// handleKanbanKeys processes keyboard input for the kanban view
func (m Model) handleKanbanKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	var cmds []tea.Cmd
	
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
		
	case "?":
		m.currentView = ViewHelp
		return m, nil
		
	case "r":
		return m, m.refreshTasks()
		
	// Navigation
	case "h", "left":
		if m.focused > ColumnTodo {
			m.focused--
		}
		return m, nil
		
	case "l", "right":
		if m.focused < ColumnDone {
			m.focused++
		}
		return m, nil
		
	case "j", "down":
		m.moveSelectionDown()
		// Also update the focused viewport to handle scrolling
		vp := m.viewports[m.focused]
		vp, cmd = vp.Update(msg)
		m.viewports[m.focused] = vp
		cmds = append(cmds, cmd)
		return m, tea.Batch(cmds...)
		
	case "k", "up":
		m.moveSelectionUp()
		// Also update the focused viewport to handle scrolling
		vp := m.viewports[m.focused]
		vp, cmd = vp.Update(msg)
		m.viewports[m.focused] = vp
		cmds = append(cmds, cmd)
		return m, tea.Batch(cmds...)
		
	// Task actions
	case "enter":
		return m.handleTaskAction()
		
	case "n":
		// TODO: Open new task dialog
		return m, nil
		
	case "d":
		return m.handleDeleteTask()
		
	case "e":
		// TODO: Edit task
		return m, nil
		
	// Pass other keys to focused viewport for scrolling (pgup/pgdn, etc.)
	default:
		vp := m.viewports[m.focused]
		vp, cmd = vp.Update(msg)
		m.viewports[m.focused] = vp
		return m, cmd
	}
	
	return m, nil
}

// handleHelpKeys processes keyboard input for the help view
func (m Model) handleHelpKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c", "esc", "?":
		m.currentView = ViewKanban
		return m, nil
	}
	
	return m, nil
}

// handleTaskDetailKeys processes keyboard input for the task detail view
func (m Model) handleTaskDetailKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c", "esc":
		m.currentView = ViewKanban
		return m, nil
	}
	
	return m, nil
}

// moveSelectionDown moves the selection down in the current column
func (m *Model) moveSelectionDown() {
	currentStatus := m.columnToStatus(m.focused)
	tasks := m.tasks[currentStatus]
	
	if len(tasks) > 0 {
		current := m.selectedTask[m.focused]
		if current < len(tasks)-1 {
			m.selectedTask[m.focused] = current + 1
			// Update viewport content to reflect new selection
			m.updateViewportContent()
		}
	}
}

// moveSelectionUp moves the selection up in the current column
func (m *Model) moveSelectionUp() {
	current := m.selectedTask[m.focused]
	if current > 0 {
		m.selectedTask[m.focused] = current - 1
		// Update viewport content to reflect new selection
		m.updateViewportContent()
	}
}

// handleTaskAction handles the main action for the selected task (move to next status)
func (m Model) handleTaskAction() (tea.Model, tea.Cmd) {
	currentStatus := m.columnToStatus(m.focused)
	tasks := m.tasks[currentStatus]
	
	if len(tasks) == 0 {
		return m, nil
	}
	
	selectedIndex := m.selectedTask[m.focused]
	if selectedIndex >= len(tasks) {
		return m, nil
	}
	
	selectedTask := tasks[selectedIndex]
	var newStatus task.Status
	
	switch currentStatus {
	case task.StatusTodo:
		newStatus = task.StatusDoing
	case task.StatusDoing:
		newStatus = task.StatusDone
	case task.StatusDone:
		// Already done, maybe show task details instead
		return m, nil
	}
	
	return m, m.moveTask(selectedTask.ID, newStatus)
}

// handleDeleteTask handles deleting the selected task
func (m Model) handleDeleteTask() (tea.Model, tea.Cmd) {
	currentStatus := m.columnToStatus(m.focused)
	tasks := m.tasks[currentStatus]
	
	if len(tasks) == 0 {
		return m, nil
	}
	
	selectedIndex := m.selectedTask[m.focused]
	if selectedIndex >= len(tasks) {
		return m, nil
	}
	
	selectedTask := tasks[selectedIndex]
	return m, m.deleteTask(selectedTask.ID)
}


