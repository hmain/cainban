package tui

import (
	"github.com/charmbracelet/bubbletea"
	"github.com/hmain/cainban/src/systems/task"
)

// Messages for the TUI update loop

// TasksRefreshedMsg is sent when tasks are refreshed from the database
type TasksRefreshedMsg struct {
	Tasks map[task.Status][]*task.Task
}

// ErrorMsg is sent when an error occurs
type ErrorMsg struct {
	Err error
}

// Commands for the TUI

// refreshTasks loads all tasks from the database
func (m Model) refreshTasks() tea.Cmd {
	return func() tea.Msg {
		// Get current board ID (assuming board ID 1 for now)
		boardID := 1 // TODO: Get actual board ID from board system
		
		// Load tasks by status
		tasks := make(map[task.Status][]*task.Task)
		
		// Load todo tasks
		todoTasks, err := m.taskSystem.ListByStatus(boardID, task.StatusTodo)
		if err == nil {
			tasks[task.StatusTodo] = todoTasks
		}
		
		// Load doing tasks  
		doingTasks, err := m.taskSystem.ListByStatus(boardID, task.StatusDoing)
		if err == nil {
			tasks[task.StatusDoing] = doingTasks
		}
		
		// Load done tasks
		doneTasks, err := m.taskSystem.ListByStatus(boardID, task.StatusDone) 
		if err == nil {
			tasks[task.StatusDone] = doneTasks
		}
		
		return TasksRefreshedMsg{Tasks: tasks}
	}
}

// moveTask moves a task to a new status
func (m Model) moveTask(taskID int, newStatus task.Status) tea.Cmd {
	return func() tea.Msg {
		err := m.taskSystem.UpdateStatus(taskID, newStatus)
		if err != nil {
			return ErrorMsg{Err: err}
		}
		
		// Refresh tasks after move
		return m.refreshTasks()()
	}
}

// deleteTask deletes a task
func (m Model) deleteTask(taskID int) tea.Cmd {
	return func() tea.Msg {
		err := m.taskSystem.Delete(taskID)
		if err != nil {
			return ErrorMsg{Err: err}
		}
		
		// Refresh tasks after deletion
		return m.refreshTasks()()
	}
}

// hardDeleteTask permanently deletes a task
func (m Model) hardDeleteTask(taskID int) tea.Cmd {
	return func() tea.Msg {
		err := m.taskSystem.HardDelete(taskID)
		if err != nil {
			return ErrorMsg{Err: err}
		}
		
		// Refresh tasks after deletion
		return m.refreshTasks()()
	}
}

// updateTaskPriority updates a task's priority
func (m Model) updateTaskPriority(taskID int, priority int) tea.Cmd {
	return func() tea.Msg {
		err := m.taskSystem.UpdatePriority(taskID, priority)
		if err != nil {
			return ErrorMsg{Err: err}
		}
		
		// Refresh tasks after update
		return m.refreshTasks()()
	}
}

// updateTask updates a task's title and description
func (m Model) updateTask(taskID int, title, description string) tea.Cmd {
	return func() tea.Msg {
		err := m.taskSystem.Update(taskID, title, description)
		if err != nil {
			return ErrorMsg{Err: err}
		}
		
		// Refresh tasks after update
		return m.refreshTasks()()
	}
}

// BoardsLoadedMsg is sent when boards are loaded
type BoardsLoadedMsg struct {
	Boards []string
}

// loadBoards loads all available boards
func (m Model) loadBoards() tea.Cmd {
	return func() tea.Msg {
		boards, err := m.boardSystem.ListBoards()
		if err != nil {
			return ErrorMsg{Err: err}
		}
		
		// Extract board names
		boardNames := make([]string, len(boards))
		for i, board := range boards {
			boardNames[i] = board.Name
		}
		
		return BoardsLoadedMsg{Boards: boardNames}
	}
}

// switchBoard switches to a different board
func (m Model) switchBoard(boardName string) tea.Cmd {
	return func() tea.Msg {
		err := m.boardSystem.SetCurrentBoard(boardName)
		if err != nil {
			return ErrorMsg{Err: err}
		}
		
		// Refresh tasks after switching
		return m.refreshTasks()()
	}
}

// createTask creates a new task
func (m Model) createTask(title, description string) tea.Cmd {
	return m.createTaskWithPriority(title, description, 0)
}

// createTaskWithPriority creates a new task with specified priority
func (m Model) createTaskWithPriority(title, description string, priority int) tea.Cmd {
	return func() tea.Msg {
		// Get current board ID (assuming board ID 1 for now)
		boardID := 1 // TODO: Get actual board ID from board system
		
		t, err := m.taskSystem.Create(boardID, title, description)
		if err != nil {
			return ErrorMsg{Err: err}
		}
		
		// Set priority if specified
		if priority > 0 {
			err = m.taskSystem.UpdatePriority(t.ID, priority)
			if err != nil {
				return ErrorMsg{Err: err}
			}
		}
		
		// Refresh tasks after creation
		return m.refreshTasks()()
	}
}