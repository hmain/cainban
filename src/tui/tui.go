package tui

import (
	"fmt"
	"os"
	
	tea "github.com/charmbracelet/bubbletea"
	"github.com/hmain/cainban/src/systems/storage"
)

// Run starts the TUI application
func Run(db *storage.DB) error {
	// Create the model
	model := NewModel(db)
	
	// Create the program
	program := tea.NewProgram(
		model,
		tea.WithAltScreen(),       // Use alternate screen buffer
		tea.WithMouseCellMotion(), // Enable mouse support
		tea.WithOutput(os.Stderr), // Send output to stderr for debugging
	)
	
	// Start the program
	if _, err := program.Run(); err != nil {
		return fmt.Errorf("failed to start TUI: %w", err)
	}
	
	return nil
}