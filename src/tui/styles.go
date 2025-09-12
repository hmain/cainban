package tui

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/hmain/cainban/src/systems/task"
)

// DefaultStyles returns the default styling configuration
func DefaultStyles() Styles {
	return DefaultStylesWithDimensions(30, 20) // Default fallback dimensions
}

// DefaultStylesWithDimensions returns styling with custom column dimensions
func DefaultStylesWithDimensions(columnWidth, columnHeight int) Styles {
	// Color palette
	var (
		primary    = lipgloss.Color("#7C3AED")  // Purple
		secondary  = lipgloss.Color("#3B82F6")  // Blue
		warning    = lipgloss.Color("#F59E0B")  // Yellow
		danger     = lipgloss.Color("#EF4444")  // Red
		muted      = lipgloss.Color("#6B7280")  // Gray
		background = lipgloss.Color("#1F2937")  // Dark gray
		surface    = lipgloss.Color("#374151")  // Medium gray
		border     = lipgloss.Color("#4B5563")  // Light gray
	)

	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#F9FAFB")).
		Background(background).
		Align(lipgloss.Left). // Horizontal alignment
		AlignVertical(lipgloss.Top) // Vertical alignment - start at top

	header := lipgloss.NewStyle().
		Bold(true).
		Foreground(primary).
		Background(surface).
		Padding(0, 1).
		Margin(0, 0, 1, 0)

	column := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(border).
		Padding(1, 2). // Better internal padding
		Margin(0, 1).  // Small margin between columns
		Width(columnWidth).
		Height(columnHeight).
		Align(lipgloss.Left). // Horizontal alignment
		AlignVertical(lipgloss.Top) // Vertical alignment - start content at top

	columnTitle := lipgloss.NewStyle().
		Bold(true).
		Foreground(secondary).
		Align(lipgloss.Center).
		Padding(0, 1).
		Margin(0, 0, 1, 0)

	taskBase := lipgloss.NewStyle().
		Padding(0, 1).
		Margin(0, 0, 1, 0).
		Border(lipgloss.NormalBorder()).
		BorderForeground(border).
		Background(surface)

	taskSelected := taskBase.Copy().
		BorderForeground(primary).
		Background(lipgloss.Color("#312E81"))

	help := lipgloss.NewStyle().
		Foreground(muted).
		Margin(1, 0).
		Padding(1)

	statusBar := lipgloss.NewStyle().
		Background(surface).
		Foreground(muted).
		Padding(0, 1).
		Margin(1, 0, 0, 0)

	// Priority styles
	priorityStyles := map[int]lipgloss.Style{
		task.PriorityNone:     taskBase.Copy().BorderForeground(muted),
		task.PriorityLow:      taskBase.Copy().BorderForeground(lipgloss.Color("#059669")),
		task.PriorityMedium:   taskBase.Copy().BorderForeground(warning),
		task.PriorityHigh:     taskBase.Copy().BorderForeground(lipgloss.Color("#DC2626")),
		task.PriorityCritical: taskBase.Copy().BorderForeground(danger).Bold(true),
	}

	return Styles{
		Base:         base,
		Header:       header,
		Column:       column,
		ColumnTitle:  columnTitle,
		Task:         taskBase,
		TaskSelected: taskSelected,
		TaskPriority: priorityStyles,
		Help:         help,
		StatusBar:    statusBar,
	}
}

// PriorityIndicator returns a styled priority indicator
func (s Styles) PriorityIndicator(priority int) string {
	indicators := map[int]string{
		task.PriorityNone:     " ",
		task.PriorityLow:      "●",
		task.PriorityMedium:   "●●",
		task.PriorityHigh:     "●●●",
		task.PriorityCritical: "🔥",
	}
	
	colors := map[int]lipgloss.Color{
		task.PriorityNone:     lipgloss.Color("#6B7280"),
		task.PriorityLow:      lipgloss.Color("#059669"),
		task.PriorityMedium:   lipgloss.Color("#F59E0B"),
		task.PriorityHigh:     lipgloss.Color("#DC2626"),
		task.PriorityCritical: lipgloss.Color("#EF4444"),
	}
	
	indicator := indicators[priority]
	color := colors[priority]
	
	return lipgloss.NewStyle().Foreground(color).Render(indicator)
}

// StatusColor returns the color for a given task status
func StatusColor(status task.Status) lipgloss.Color {
	switch status {
	case task.StatusTodo:
		return lipgloss.Color("#6B7280")  // Gray
	case task.StatusDoing:
		return lipgloss.Color("#3B82F6")  // Blue
	case task.StatusDone:
		return lipgloss.Color("#10B981")  // Green
	default:
		return lipgloss.Color("#6B7280")
	}
}