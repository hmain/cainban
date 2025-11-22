package tui

import (
	"fmt"
	"strings"
	
	"github.com/charmbracelet/lipgloss"
	"github.com/hmain/cainban/src/systems/task"
)

// View renders the current TUI state
func (m Model) View() string {
	switch m.currentView {
	case ViewKanban:
		return m.renderKanbanView()
	case ViewHelp:
		return m.renderHelpView()
	case ViewTaskDetail:
		return m.renderTaskDetailView()
	case ViewTaskCreate:
		return m.renderTaskCreateView()
	case ViewConfirmDialog:
		return m.renderConfirmDialog()
	case ViewTaskEdit:
		return m.renderTaskEditView()
	case ViewBoardSelector:
		return m.renderBoardSelectorView()
	default:
		return m.renderKanbanView()
	}
}

// renderKanbanView renders the main kanban board view using viewports
func (m Model) renderKanbanView() string {
	debugLog("[RENDER] Starting viewport-based renderKanbanView, terminal size: %dx%d\n", m.width, m.height)
	
	// Simple header
	header := fmt.Sprintf("Cainban - %s", m.currentBoard)
	
	// Render columns using viewports
	columns := m.renderViewportColumns()
	
	// Simple status bar with all commands
	statusBar := "n: new • v: view • e: edit • p: priority • d: delete • b: boards • ?: help • q: quit"
	
	// Simple layout - no complex styling for now
	content := header + "\n\n" + columns + "\n\n" + statusBar
	
	debugLog("[RENDER] Viewport-based rendering complete\n")
	
	return content
}

// renderViewportColumns renders the three columns using viewport components
func (m Model) renderViewportColumns() string {
	debugLog("[VIEWPORT] Rendering columns with viewports\n")
	
	// Get the viewport content for each column
	todoView := m.renderViewportColumn(ColumnTodo, "📝 Todo")
	doingView := m.renderViewportColumn(ColumnDoing, "🔄 Doing")
	doneView := m.renderViewportColumn(ColumnDone, "✅ Done")
	
	// Join horizontally - simple approach
	columns := lipgloss.JoinHorizontal(
		lipgloss.Top,
		todoView,
		doingView,
		doneView,
	)
	
	return columns
}

// renderViewportColumn renders a single column with its viewport
func (m Model) renderViewportColumn(col Column, title string) string {
	status := m.columnToStatus(col)
	tasks := m.tasks[status]
	titleWithCount := fmt.Sprintf("%s (%d)", title, len(tasks))
	
	// Simple column style
	columnStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Padding(1).
		Margin(0, 1).
		Width(m.calculateColumnWidth())
	
	// Highlight focused column
	if col == m.focused {
		columnStyle = columnStyle.BorderForeground(lipgloss.Color("#7C3AED"))
	} else {
		columnStyle = columnStyle.BorderForeground(lipgloss.Color("#4B5563"))
	}
	
	// Get viewport content
	vp := m.viewports[col]
	viewportContent := vp.View()
	
	// Add scroll indicator if there are more tasks than fit in viewport
	scrollInfo := ""
	if len(tasks) > 0 {
		selectedIndex := m.selectedTask[col] + 1 // 1-indexed for display
		totalTasks := len(tasks)
		
		if totalTasks > vp.Height {
			// Show scroll position when there's overflow
			scrollInfo = fmt.Sprintf(" [%d/%d]", selectedIndex, totalTasks)
		}
	}
	
	// Combine title, scroll info, and viewport content
	header := titleWithCount + scrollInfo
	content := header + "\n\n" + viewportContent
	
	debugLog("[VIEWPORT] Column %d: title=%s, viewport_lines=%d\n", 
		col, titleWithCount, strings.Count(viewportContent, "\n")+1)
	
	return columnStyle.Render(content)
}

// renderColumns renders the three kanban columns side by side
func (m Model) renderColumns() string {
	debugLog("[COLUMNS] Starting renderColumns\n")
	
	todoColumn := m.renderColumn(ColumnTodo, "📝 Todo", task.StatusTodo)
	todoLines := strings.Count(todoColumn, "\n") + 1
	debugLog("[COLUMNS] Todo column: %d lines\n", todoLines)
	
	doingColumn := m.renderColumn(ColumnDoing, "🔄 Doing", task.StatusDoing)  
	doingLines := strings.Count(doingColumn, "\n") + 1
	debugLog("[COLUMNS] Doing column: %d lines\n", doingLines)
	
	doneColumn := m.renderColumn(ColumnDone, "✅ Done", task.StatusDone)
	doneLines := strings.Count(doneColumn, "\n") + 1
	debugLog("[COLUMNS] Done column: %d lines\n", doneLines)
	
	// FIX: Explicit top alignment for proper column positioning  
	columnsLayout := lipgloss.JoinHorizontal(
		lipgloss.Top, // CHANGED: Top alignment to start columns at top
		todoColumn,
		doingColumn, 
		doneColumn,
	)
	
	joinedLines := strings.Count(columnsLayout, "\n") + 1
	debugLog("[COLUMNS] After JoinHorizontal: %d lines\n", joinedLines)
	
	// Force full width usage to prevent centering
	if m.width > 0 {
		debugLog("[COLUMNS] Applying full width style (%d)\n", m.width)
		columnsLayout = lipgloss.NewStyle().
			Width(m.width).
			Align(lipgloss.Left).
			Render(columnsLayout)
		
		finalLines := strings.Count(columnsLayout, "\n") + 1
		debugLog("[COLUMNS] After width styling: %d lines\n", finalLines)
	}
	
	return columnsLayout
}

// renderColumn renders a single kanban column with virtual scrolling
func (m Model) renderColumn(col Column, title string, status task.Status) string {
	var content []string
	
	// Column title with task count
	tasks := m.tasks[status]
	titleWithCount := fmt.Sprintf("%s (%d)", title, len(tasks))
	
	columnTitle := m.styles.ColumnTitle.Render(titleWithCount)
	content = append(content, columnTitle)
	
	// Calculate visible task window
	maxVisibleTasks := m.getMaxVisibleTasks()
	selectedIndex := m.selectedTask[col]
	
	debugLog("[VIRTUAL] Column %d: %d tasks, selected: %d, maxVisible: %d\n", col, len(tasks), selectedIndex, maxVisibleTasks)
	debugLog("[VIRTUAL] selectedTask map contents: Todo=%d, Doing=%d, Done=%d\n", 
		m.selectedTask[ColumnTodo], m.selectedTask[ColumnDoing], m.selectedTask[ColumnDone])
	
	// Tasks with virtual scrolling
	if len(tasks) == 0 {
		emptyMsg := m.styles.Task.Copy().
			Foreground(lipgloss.Color("#6B7280")).
			Italic(true).
			Render("No tasks")
		content = append(content, emptyMsg)
	} else {
		// Calculate the visible task window
		startIndex, endIndex := m.calculateTaskWindow(len(tasks), selectedIndex, maxVisibleTasks)
		
		debugLog("[VIRTUAL] Showing tasks %d-%d of %d total\n", startIndex, endIndex-1, len(tasks))
		
		// Add scroll indicators if needed
		if startIndex > 0 {
			scrollIndicator := m.styles.Task.Copy().
				Foreground(lipgloss.Color("#6B7280")).
				Italic(true).
				Render("▲ (" + fmt.Sprintf("%d more above", startIndex) + ")")
			content = append(content, scrollIndicator)
		}
		
		// Render visible tasks
		for i := startIndex; i < endIndex; i++ {
			if i < len(tasks) {
				taskView := m.renderTask(tasks[i], i == selectedIndex && col == m.focused)
				content = append(content, taskView)
			}
		}
		
		// Add bottom scroll indicator if needed
		if endIndex < len(tasks) {
			remaining := len(tasks) - endIndex
			scrollIndicator := m.styles.Task.Copy().
				Foreground(lipgloss.Color("#6B7280")).
				Italic(true).
				Render("▼ (" + fmt.Sprintf("%d more below", remaining) + ")")
			content = append(content, scrollIndicator)
		}
	}
	
	// Column styling
	columnContent := strings.Join(content, "\n")
	
	// Highlight focused column
	columnStyle := m.styles.Column
	if col == m.focused {
		columnStyle = columnStyle.Copy().
			BorderForeground(lipgloss.Color("#7C3AED")).
			BorderStyle(lipgloss.ThickBorder())
	}
	
	return columnStyle.Render(columnContent)
}

// renderTask renders a single task
func (m Model) renderTask(t *task.Task, selected bool) string {
	// Priority indicator
	priority := m.styles.PriorityIndicator(t.Priority)
	
	// Dynamic task title truncation based on column width
	columnWidth := m.calculateColumnWidth()
	// Account for padding, border, priority indicator, and some breathing room
	maxTitleLength := columnWidth - 10
	if maxTitleLength < 15 {
		maxTitleLength = 15 // Minimum readable length
	}
	
	title := t.Title
	if len(title) > maxTitleLength {
		title = title[:maxTitleLength-3] + "..."
	}
	
	// Task content
	taskContent := fmt.Sprintf("%s %s", priority, title)
	
	// Apply styling
	style := m.styles.Task
	if selected {
		style = m.styles.TaskSelected
	} else {
		// Use priority-based styling
		if priorityStyle, exists := m.styles.TaskPriority[t.Priority]; exists {
			style = priorityStyle
		}
	}
	
	return style.Render(taskContent)
}

// renderStatusBar renders the bottom status/help bar
func (m Model) renderStatusBar() string {
	var help []string
	
	switch m.currentView {
	case ViewKanban:
		help = append(help, 
			"h/l: columns", 
			"j/k: navigate tasks",
			"enter: move task",
			"n: new task",
			"d: delete",
			"r: refresh",
			"?: help",
			"q: quit",
		)
	}
	
	helpText := strings.Join(help, " • ")
	return m.styles.StatusBar.Render(helpText)
}

// renderHelpView renders the help screen
func (m Model) renderHelpView() string {
	helpContent := `
Cainban - Terminal Kanban Board

NAVIGATION:
  h, ←         Move to left column
  l, →         Move to right column  
  j, ↓         Navigate down in current column
  k, ↑         Navigate up in current column
  PgUp/PgDn    Scroll viewport
  Home/End     Jump to top/bottom

TASK ACTIONS:
  n            Create new task (with priority)
  e            Edit selected task (title & description)
  p            Cycle task priority (none → low → medium → high → critical)
  d            Delete task (soft delete, can restore)
  D            Hard delete task (permanent)
  Shift+←      Move task to previous column
  Shift+→      Move task to next column
  
OTHER:
  r            Refresh tasks from database
  ?            Show/hide this help
  q, Ctrl+C    Quit application

PRIORITY LEVELS:
  none         No priority
  low          ● Low priority
  medium       ●● Medium priority 
  high         ●●● High priority
  critical     🔥 Critical priority

Press ? or Esc to return to the kanban board...
`

	return m.styles.Base.Render(
		m.styles.Help.Render(helpContent),
	)
}

// renderTaskDetailView renders detailed task information  
func (m Model) renderTaskDetailView() string {
	// Find the task
	var foundTask *task.Task
	for _, tasks := range m.tasks {
		for _, t := range tasks {
			if t.ID == m.viewTaskID {
				foundTask = t
				break
			}
		}
		if foundTask != nil {
			break
		}
	}
	
	if foundTask == nil {
		return "Task not found\n\nPress Esc to return..."
	}
	
	var b strings.Builder
	
	b.WriteString("\n  Task Details\n")
	b.WriteString("  " + strings.Repeat("─", 60) + "\n\n")
	
	b.WriteString(fmt.Sprintf("  ID: #%d\n", foundTask.BoardTaskID))
	b.WriteString(fmt.Sprintf("  Title: %s\n\n", foundTask.Title))
	
	if foundTask.Description != "" {
		b.WriteString(fmt.Sprintf("  Description:\n  %s\n\n", foundTask.Description))
	}
	
	priorityNames := []string{"none", "low", "medium", "high", "critical"}
	b.WriteString(fmt.Sprintf("  Priority: %s\n", priorityNames[foundTask.Priority]))
	b.WriteString(fmt.Sprintf("  Status: %s\n\n", foundTask.Status))
	
	b.WriteString(fmt.Sprintf("  Created: %s\n", foundTask.CreatedAt.Format("2006-01-02 15:04")))
	b.WriteString(fmt.Sprintf("  Updated: %s\n\n", foundTask.UpdatedAt.Format("2006-01-02 15:04")))
	
	b.WriteString("  Press Esc to return\n")
	
	return b.String()
}

// renderTaskCreateView renders the task creation form
func (m Model) renderTaskCreateView() string {
	var b strings.Builder
	
	priorityNames := []string{"none", "low", "medium", "high", "critical"}
	
	b.WriteString("\n  Create New Task\n\n")
	b.WriteString("  " + m.titleInput.View() + "\n\n")
	b.WriteString("  " + m.descriptionInput.View() + "\n\n")
	b.WriteString(fmt.Sprintf("  Priority: %s\n\n", priorityNames[m.selectedPriority]))
	b.WriteString("  Tab: Switch fields | P: Change priority | Enter: Create | Esc: Cancel\n")
	
	return b.String()
}

// renderConfirmDialog renders the confirmation dialog
func (m Model) renderConfirmDialog() string {
	var b strings.Builder
	
	b.WriteString("\n\n")
	
	if m.confirmAction == "hard_delete" {
		b.WriteString("  ⚠️  Permanently delete this task?\n\n")
		b.WriteString("  This cannot be undone!\n\n")
	} else {
		b.WriteString("  Delete this task?\n\n")
		b.WriteString("  (Can be restored later)\n\n")
	}
	
	b.WriteString("  Y: Yes | N: No\n")
	
	return b.String()
}

// renderTaskEditView renders the task edit form
func (m Model) renderTaskEditView() string {
	var b strings.Builder
	
	b.WriteString("\n  Edit Task\n\n")
	b.WriteString("  " + m.titleInput.View() + "\n\n")
	b.WriteString("  " + m.descriptionInput.View() + "\n\n")
	b.WriteString("  Tab: Switch fields | Enter: Save | Esc: Cancel\n")
	
	return b.String()
}

// renderBoardSelectorView renders the board selector
func (m Model) renderBoardSelectorView() string {
	var b strings.Builder
	
	b.WriteString("\n  Select Board\n")
	b.WriteString("  " + strings.Repeat("─", 40) + "\n\n")
	
	for i, board := range m.availableBoards {
		prefix := "  "
		if i == m.selectedBoardIndex {
			prefix = "> "
		}
		
		marker := " "
		if board == m.currentBoard {
			marker = "●"
		}
		
		b.WriteString(fmt.Sprintf("  %s%s %s\n", prefix, marker, board))
	}
	
	b.WriteString("\n  ↑/↓: Navigate | Enter: Select | Esc: Cancel\n")
	
	return b.String()
}

