package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/hmain/cainban/src/systems/board"
	"github.com/hmain/cainban/src/systems/mcp"
	"github.com/hmain/cainban/src/systems/storage"
	"github.com/hmain/cainban/src/systems/task"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	command := os.Args[1]

	switch command {
	case "init":
		handleInit(os.Args[2:])
	case "add":
		handleAdd(os.Args[2:])
	case "list":
		handleList(os.Args[2:])
	case "move":
		handleMove(os.Args[2:])
	case "get":
		handleGet(os.Args[2:])
	case "update":
		handleUpdate(os.Args[2:])
	case "priority":
		handlePriority(os.Args[2:])
	case "board":
		handleBoard(os.Args[2:])
	case "mcp":
		handleMCP()
	case "version":
		handleVersion()
	default:
		fmt.Printf("Unknown command: %s\n", command)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println("cainban - AI-centric kanban board")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  cainban init [board-name]            Initialize new board")
	fmt.Println("  cainban add <title> [description]    Add new task")
	fmt.Println("  cainban list [status]                List all tasks or by status")
	fmt.Println("  cainban move <id> <status>           Move task between columns")
	fmt.Println("  cainban get <id>                     Get task details")
	fmt.Println("  cainban update <id> <title> [description] Update task")
	fmt.Println("  cainban priority <id> <level>           Set task priority")
	fmt.Println("  cainban board <command>              Board management")
	fmt.Println("  cainban mcp                          Start MCP server")
	fmt.Println("  cainban version                      Show version")
	fmt.Println()
	fmt.Println("Board commands:")
	fmt.Println("  cainban board list                   List all boards")
	fmt.Println("  cainban board current                Show current board")
	fmt.Println("  cainban board switch <name>          Switch to board")
	fmt.Println("  cainban board create <name> [desc]   Create new board")
	fmt.Println("  cainban board delete <name>          Delete board")
	fmt.Println()
	fmt.Println("Priority levels: none, low, medium, high, critical (or 0-4)")
	fmt.Println("Statuses: todo, doing, done")
}

func getCurrentBoardDB() (*storage.DB, *task.System, string, error) {
	boardSystem := board.New()
	
	// Get current board name
	boardName, err := boardSystem.GetCurrentBoard()
	if err != nil {
		return nil, nil, "", fmt.Errorf("failed to get current board: %w", err)
	}
	
	// Get database path for current board
	dbPath := boardSystem.GetBoardPath(boardName)
	
	// Initialize database
	db, err := storage.New(dbPath)
	if err != nil {
		return nil, nil, "", fmt.Errorf("failed to initialize database: %w", err)
	}

	taskSystem := task.New(db.Conn())
	return db, taskSystem, boardName, nil
}

func handleInit(args []string) {
	boardSystem := board.New()
	
	var boardName string
	if len(args) > 0 {
		boardName = args[0]
	} else {
		// Try to detect project board name
		boardName = boardSystem.DetectProjectBoard()
		if boardName != "default" {
			fmt.Printf("Auto-detected board name: %s\n", boardName)
		}
	}
	
	fmt.Printf("Initializing cainban board: %s\n", boardName)
	
	// Create board if it doesn't exist
	if boardName != "default" {
		_, err := boardSystem.CreateBoard(boardName, fmt.Sprintf("Board for %s", boardName))
		if err != nil && !strings.Contains(err.Error(), "already exists") {
			fmt.Printf("Error creating board: %v\n", err)
			os.Exit(1)
		}
	}
	
	// Set as current board
	if err := boardSystem.SetCurrentBoard(boardName); err != nil {
		fmt.Printf("Error setting current board: %v\n", err)
		os.Exit(1)
	}
	
	// Initialize database
	dbPath := boardSystem.GetBoardPath(boardName)
	db, err := storage.New(dbPath)
	if err != nil {
		fmt.Printf("Error initializing database: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	fmt.Printf("Board '%s' initialized at: %s\n", boardName, dbPath)
	fmt.Printf("You can now add tasks with: cainban add \"Your task title\"\n")
}

func handleAdd(args []string) {
	if len(args) == 0 {
		fmt.Println("Error: task title required")
		fmt.Println("Usage: cainban add <title> [description]")
		os.Exit(1)
	}

	title := args[0]
	description := ""
	if len(args) > 1 {
		description = strings.Join(args[1:], " ")
	}

	db, taskSystem, boardName, err := getCurrentBoardDB()
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	createdTask, err := taskSystem.Create(1, title, description) // Default board ID = 1
	if err != nil {
		fmt.Printf("Error creating task: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Created task #%d in board '%s': %s\n", createdTask.ID, boardName, createdTask.Title)
	if createdTask.Description != "" {
		fmt.Printf("Description: %s\n", createdTask.Description)
	}
}

func handleList(args []string) {
	db, taskSystem, boardName, err := getCurrentBoardDB()
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	var tasks []*task.Task

	if len(args) > 0 {
		status := args[0]
		if !task.IsValidStatus(status) {
			fmt.Printf("Error: invalid status '%s'. Valid statuses: todo, doing, done\n", status)
			os.Exit(1)
		}
		tasks, err = taskSystem.ListByStatus(1, task.Status(status))
	} else {
		tasks, err = taskSystem.List(1)
	}

	if err != nil {
		fmt.Printf("Error listing tasks: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Board: %s\n", boardName)

	if len(tasks) == 0 {
		fmt.Println("No tasks found")
		return
	}

	// Group tasks by status for better display
	tasksByStatus := make(map[task.Status][]*task.Task)
	for _, t := range tasks {
		tasksByStatus[t.Status] = append(tasksByStatus[t.Status], t)
	}

	statuses := []task.Status{task.StatusTodo, task.StatusDoing, task.StatusDone}
	for _, status := range statuses {
		if statusTasks, exists := tasksByStatus[status]; exists && len(statusTasks) > 0 {
			fmt.Printf("\n%s:\n", strings.ToUpper(string(status)))
			for _, t := range statusTasks {
				priorityStr := ""
				if t.Priority > 0 {
					priorityStr = fmt.Sprintf(" [%s]", task.GetPriorityName(t.Priority))
				}
				fmt.Printf("  #%d%s %s\n", t.ID, priorityStr, t.Title)
				if t.Description != "" {
					fmt.Printf("      %s\n", t.Description)
				}
			}
		}
	}
}

func handleMove(args []string) {
	if len(args) < 2 {
		fmt.Println("Error: task ID and status required")
		fmt.Println("Usage: cainban move <id> <status>")
		os.Exit(1)
	}

	id, err := strconv.Atoi(args[0])
	if err != nil {
		fmt.Printf("Error: invalid task ID '%s'\n", args[0])
		os.Exit(1)
	}

	status := args[1]
	if !task.IsValidStatus(status) {
		fmt.Printf("Error: invalid status '%s'. Valid statuses: todo, doing, done\n", status)
		os.Exit(1)
	}

	db, taskSystem, boardName, err := getCurrentBoardDB()
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	if err := taskSystem.UpdateStatus(id, task.Status(status)); err != nil {
		fmt.Printf("Error moving task: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Moved task #%d to %s in board '%s'\n", id, status, boardName)
}

func handleGet(args []string) {
	if len(args) == 0 {
		fmt.Println("Error: task ID required")
		fmt.Println("Usage: cainban get <id>")
		os.Exit(1)
	}

	id, err := strconv.Atoi(args[0])
	if err != nil {
		fmt.Printf("Error: invalid task ID '%s'\n", args[0])
		os.Exit(1)
	}

	db, taskSystem, boardName, err := getCurrentBoardDB()
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	t, err := taskSystem.GetByID(id)
	if err != nil {
		fmt.Printf("Error getting task: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Board: %s\n", boardName)
	fmt.Printf("Task #%d [%s]\n", t.ID, t.Status)
	fmt.Printf("Title: %s\n", t.Title)
	if t.Priority > 0 {
		fmt.Printf("Priority: %s (%d)\n", task.GetPriorityName(t.Priority), t.Priority)
	}
	if t.Description != "" {
		fmt.Printf("Description: %s\n", t.Description)
	}
	fmt.Printf("Created: %s\n", t.CreatedAt.Format("2006-01-02 15:04:05"))
	fmt.Printf("Updated: %s\n", t.UpdatedAt.Format("2006-01-02 15:04:05"))
}

func handleUpdate(args []string) {
	if len(args) < 2 {
		fmt.Println("Error: task ID and title required")
		fmt.Println("Usage: cainban update <id> <title> [description]")
		os.Exit(1)
	}

	id, err := strconv.Atoi(args[0])
	if err != nil {
		fmt.Printf("Error: invalid task ID '%s'\n", args[0])
		os.Exit(1)
	}

	title := args[1]
	description := ""
	if len(args) > 2 {
		description = strings.Join(args[2:], " ")
	}

	db, taskSystem, boardName, err := getCurrentBoardDB()
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	if err := taskSystem.Update(id, title, description); err != nil {
		fmt.Printf("Error updating task: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Updated task #%d in board '%s': %s\n", id, boardName, title)
}

func handlePriority(args []string) {
	if len(args) < 2 {
		fmt.Println("Error: task ID and priority level required")
		fmt.Println("Usage: cainban priority <id> <level>")
		fmt.Println("Priority levels: none, low, medium, high, critical (or 0-4)")
		os.Exit(1)
	}

	id, err := strconv.Atoi(args[0])
	if err != nil {
		fmt.Printf("Error: invalid task ID '%s'\n", args[0])
		os.Exit(1)
	}

	priority := args[1]
	
	// Try to parse as integer first, but keep as interface{}
	var priorityValue interface{} = priority
	if priorityInt, err := strconv.Atoi(priority); err == nil {
		priorityValue = priorityInt
	}

	if !task.IsValidPriority(priorityValue) {
		fmt.Printf("Error: invalid priority '%s'\n", args[1])
		fmt.Println("Valid priorities: none, low, medium, high, critical (or 0-4)")
		os.Exit(1)
	}

	db, taskSystem, boardName, err := getCurrentBoardDB()
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	if err := taskSystem.UpdatePriority(id, priorityValue); err != nil {
		fmt.Printf("Error updating task priority: %v\n", err)
		os.Exit(1)
	}

	priorityLevel, _ := task.ParsePriority(priorityValue)
	priorityName := task.GetPriorityName(priorityLevel)
	fmt.Printf("Updated task #%d priority to %s (%d) in board '%s'\n", id, priorityName, priorityLevel, boardName)
}

func handleBoard(args []string) {
	if len(args) == 0 {
		fmt.Println("Error: board command required")
		fmt.Println("Usage: cainban board <command>")
		fmt.Println("Commands: list, current, switch, create, delete")
		os.Exit(1)
	}

	boardSystem := board.New()
	command := args[0]

	switch command {
	case "list":
		boards, err := boardSystem.ListBoards()
		if err != nil {
			fmt.Printf("Error listing boards: %v\n", err)
			os.Exit(1)
		}

		if len(boards) == 0 {
			fmt.Println("No boards found")
			return
		}

		currentBoard, _ := boardSystem.GetCurrentBoard()
		fmt.Println("Available boards:")
		for _, b := range boards {
			marker := "  "
			if b.Name == currentBoard {
				marker = "* "
			}
			fmt.Printf("%s%s\n", marker, b.Name)
			if b.Description != "" {
				fmt.Printf("    %s\n", b.Description)
			}
		}

	case "current":
		currentBoard, err := boardSystem.GetCurrentBoard()
		if err != nil {
			fmt.Printf("Error getting current board: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Current board: %s\n", currentBoard)

	case "switch":
		if len(args) < 2 {
			fmt.Println("Error: board name required")
			fmt.Println("Usage: cainban board switch <name>")
			os.Exit(1)
		}

		boardName := args[1]
		
		// Check if board exists
		_, err := boardSystem.GetBoard(boardName)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}

		if err := boardSystem.SetCurrentBoard(boardName); err != nil {
			fmt.Printf("Error switching board: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Switched to board: %s\n", boardName)

	case "create":
		if len(args) < 2 {
			fmt.Println("Error: board name required")
			fmt.Println("Usage: cainban board create <name> [description]")
			os.Exit(1)
		}

		boardName := args[1]
		description := ""
		if len(args) > 2 {
			description = strings.Join(args[2:], " ")
		}

		board, err := boardSystem.CreateBoard(boardName, description)
		if err != nil {
			fmt.Printf("Error creating board: %v\n", err)
			os.Exit(1)
		}

		// Initialize the database
		db, err := storage.New(board.Path)
		if err != nil {
			fmt.Printf("Error initializing board database: %v\n", err)
			os.Exit(1)
		}
		db.Close()

		fmt.Printf("Created board '%s' at: %s\n", boardName, board.Path)

	case "delete":
		if len(args) < 2 {
			fmt.Println("Error: board name required")
			fmt.Println("Usage: cainban board delete <name>")
			os.Exit(1)
		}

		boardName := args[1]
		if err := boardSystem.DeleteBoard(boardName); err != nil {
			fmt.Printf("Error deleting board: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Deleted board: %s\n", boardName)

	default:
		fmt.Printf("Unknown board command: %s\n", command)
		fmt.Println("Commands: list, current, switch, create, delete")
		os.Exit(1)
	}
}

func handleMCP() {
	fmt.Println("Starting MCP server...")
	
	db, taskSystem, _, err := getCurrentBoardDB()
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	server := mcp.New(taskSystem, os.Stdin, os.Stdout)
	if err := server.Start(); err != nil {
		fmt.Printf("Error starting MCP server: %v\n", err)
		os.Exit(1)
	}
}

func handleVersion() {
	fmt.Println("cainban v0.2.0 - Multi-board support")
}
