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
	
	// Simple status bar  
	statusBar := "h/l: columns • j/k: navigate • PgUp/PgDn: scroll • enter: move • q: quit"
	
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
  h, ←     Move to left column
  l, →     Move to right column  
  j, ↓     Navigate down in current column (auto-scroll)
  k, ↑     Navigate up in current column (auto-scroll)
  PgUp     Scroll viewport up
  PgDn     Scroll viewport down
  Home     Go to top of column
  End      Go to bottom of column

TASK ACTIONS:
  enter    Move task to next status (todo → doing → done)
  n        Create new task
  e        Edit selected task
  d        Delete selected task
  
OTHER:
  r        Refresh tasks from database
  ?        Show/hide this help
  q, ^C    Quit application

COLUMNS:
  📝 Todo    Tasks that need to be done
  🔄 Doing   Tasks currently in progress  
  ✅ Done    Completed tasks

PRIORITY INDICATORS:
   (none)   No priority set
  ● (green) Low priority
  ●● (yellow) Medium priority 
  ●●● (red) High priority
  🔥 (red) Critical priority

Press any key to return to the kanban board...
`

	return m.styles.Base.Render(
		m.styles.Help.Render(helpContent),
	)
}

// renderTaskDetailView renders detailed task information  
func (m Model) renderTaskDetailView() string {
	// TODO: Implement detailed task view
	return m.styles.Base.Render("Task Detail View - Coming Soon!\n\nPress ESC to return...")
}

