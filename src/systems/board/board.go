package board

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Board represents a kanban board
type Board struct {
	ID          int       `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Path        string    `json:"path"` // Database file path
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// System handles board operations
type System struct {
	configDir string
}

// New creates a new board system
func New() *System {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		// Fallback to current directory
		homeDir = "."
	}
	configDir := filepath.Join(homeDir, ".cainban")

	return &System{
		configDir: configDir,
	}
}

// GetBoardPath returns the database path for a board
func (s *System) GetBoardPath(boardName string) string {
	if boardName == "" || boardName == "default" {
		return filepath.Join(s.configDir, "cainban.db")
	}

	// Sanitize board name for filename
	safeName := sanitizeBoardName(boardName)
	return filepath.Join(s.configDir, "boards", safeName+".db")
}

// GetCurrentBoard returns the currently active board name
func (s *System) GetCurrentBoard() (string, error) {
	currentFile := filepath.Join(s.configDir, "current-board")

	data, err := os.ReadFile(currentFile)
	if err != nil {
		if os.IsNotExist(err) {
			return "default", nil // Default board if no current board set
		}
		return "", fmt.Errorf("failed to read current board: %w", err)
	}

	boardName := strings.TrimSpace(string(data))
	if boardName == "" {
		return "default", nil
	}

	return boardName, nil
}

// SetCurrentBoard sets the active board
func (s *System) SetCurrentBoard(boardName string) error {
	if err := os.MkdirAll(s.configDir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	currentFile := filepath.Join(s.configDir, "current-board")

	if boardName == "" || boardName == "default" {
		// Remove current board file to use default
		os.Remove(currentFile)
		return nil
	}

	return os.WriteFile(currentFile, []byte(boardName), 0644)
}

// CreateBoard creates a new board
func (s *System) CreateBoard(name, description string) (*Board, error) {
	if name == "" {
		return nil, fmt.Errorf("board name cannot be empty")
	}

	// Create boards directory
	boardsDir := filepath.Join(s.configDir, "boards")
	if err := os.MkdirAll(boardsDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create boards directory: %w", err)
	}

	boardPath := s.GetBoardPath(name)

	// Check if board already exists
	if _, err := os.Stat(boardPath); err == nil {
		return nil, fmt.Errorf("board '%s' already exists", name)
	}

	board := &Board{
		Name:        name,
		Description: description,
		Path:        boardPath,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	return board, nil
}

// ListBoards returns all available boards
func (s *System) ListBoards() ([]*Board, error) {
	var boards []*Board

	// Add default board
	defaultPath := s.GetBoardPath("default")
	if _, err := os.Stat(defaultPath); err == nil {
		boards = append(boards, &Board{
			Name:        "default",
			Description: "Default kanban board",
			Path:        defaultPath,
		})
	}

	// Add custom boards
	boardsDir := filepath.Join(s.configDir, "boards")
	if _, err := os.Stat(boardsDir); err == nil {
		entries, err := os.ReadDir(boardsDir)
		if err != nil {
			return nil, fmt.Errorf("failed to read boards directory: %w", err)
		}

		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".db") {
				continue
			}

			name := strings.TrimSuffix(entry.Name(), ".db")
			boardPath := filepath.Join(boardsDir, entry.Name())

			boards = append(boards, &Board{
				Name: name,
				Path: boardPath,
			})
		}
	}

	return boards, nil
}

// GetBoard returns a specific board by name
func (s *System) GetBoard(name string) (*Board, error) {
	boards, err := s.ListBoards()
	if err != nil {
		return nil, err
	}

	for _, board := range boards {
		if board.Name == name {
			return board, nil
		}
	}

	return nil, fmt.Errorf("board '%s' not found", name)
}

// DeleteBoard removes a board (except default)
func (s *System) DeleteBoard(name string) error {
	if name == "" || name == "default" {
		return fmt.Errorf("cannot delete default board")
	}

	boardPath := s.GetBoardPath(name)

	if _, err := os.Stat(boardPath); os.IsNotExist(err) {
		return fmt.Errorf("board '%s' does not exist", name)
	}

	// If this is the current board, switch to default
	currentBoard, _ := s.GetCurrentBoard()
	if currentBoard == name {
		if err := s.SetCurrentBoard("default"); err != nil {
			// Log error but don't fail deletion - the board deletion can continue
			// even if setting default fails as it's just a convenience operation
			_ = err // Explicitly ignore error
		}
	}

	return os.Remove(boardPath)
}

// DetectProjectBoard attempts to detect board name from current directory
func (s *System) DetectProjectBoard() string {
	// Try to get git repository name
	if gitName := getGitRepoName(); gitName != "" {
		return gitName
	}

	// Use current directory name
	cwd, err := os.Getwd()
	if err != nil {
		return "default"
	}

	dirName := filepath.Base(cwd)
	if dirName == "." || dirName == "/" {
		return "default"
	}

	return dirName
}

// sanitizeBoardName creates a safe filename from board name
func sanitizeBoardName(name string) string {
	// Replace unsafe characters with underscores
	safe := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, name)

	// Ensure it's not empty
	if safe == "" {
		safe = "unnamed"
	}

	return safe
}

// getGitRepoName tries to get the git repository name
func getGitRepoName() string {
	// Try to read .git/config for remote origin
	gitConfig := ".git/config"
	if _, err := os.Stat(gitConfig); err != nil {
		return ""
	}

	data, err := os.ReadFile(gitConfig)
	if err != nil {
		return ""
	}

	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "url = ") {
			// Extract repo name from URL
			parts := strings.Split(line, "/")
			if len(parts) > 0 {
				repoName := parts[len(parts)-1]
				repoName = strings.TrimSuffix(repoName, ".git")
				if repoName != "" {
					return repoName
				}
			}
		}
	}

	return ""
}
