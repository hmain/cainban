package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
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

// renderStatusBar renders the bottom status/help bar
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
