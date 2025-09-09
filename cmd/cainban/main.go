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
	case "search":
		handleSearch(os.Args[2:])
	case "priority":
		handlePriority(os.Args[2:])
	case "board":
		handleBoard(os.Args[2:])
	case "link":
		handleLink(os.Args[2:])
	case "unlink":
		handleUnlink(os.Args[2:])
	case "links":
		handleLinks(os.Args[2:])
	case "delete":
		handleDelete(os.Args[2:])
	case "restore":
		handleRestore(os.Args[2:])
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
	fmt.Println("  cainban add <title> [description] [--priority <level>]  Add new task with optional priority")
	fmt.Println("  cainban list [status]                List all tasks or by status")
	fmt.Println("  cainban move <id|title> <status>        Move task between columns")
	fmt.Println("  cainban get <id|title>               Get task details")
	fmt.Println("  cainban update <id|title> <title> [description] Update task")
	fmt.Println("  cainban search <query>                  Search tasks by title")
	fmt.Println("  cainban priority <id|title> <level>     Set task priority")
	fmt.Println("  cainban link <from_id> <to_id> [type]   Link two tasks")
	fmt.Println("  cainban unlink <from_id> <to_id> [type] Unlink two tasks")
	fmt.Println("  cainban links <task_id>              Show task links")
	fmt.Println("  cainban delete <task_id> [--hard]    Delete task (soft delete by default)")
	fmt.Println("  cainban restore <task_id>            Restore deleted task")
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
	fmt.Println("Link types: blocks, blocked_by, related, depends_on")
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
		fmt.Println("Usage: cainban add <title> [description] [--priority <level>]")
		fmt.Println("Priority levels: none, low, medium, high, critical (or 0-4)")
		os.Exit(1)
	}

	title := args[0]
	description := ""
	var priority interface{} = task.PriorityNone
	
	// Parse arguments for description and priority
	i := 1
	for i < len(args) {
		if args[i] == "--priority" || args[i] == "-p" {
			if i+1 >= len(args) {
				fmt.Println("Error: --priority requires a value")
				os.Exit(1)
			}
			priorityStr := args[i+1]
			
			// Try to convert numeric strings to integers
			if priorityInt, err := strconv.Atoi(priorityStr); err == nil {
				priority = priorityInt
			} else {
				priority = priorityStr
			}
			
			if !task.IsValidPriority(priority) {
				fmt.Println("Error: invalid priority level")
				fmt.Println("Valid levels: none, low, medium, high, critical (or 0-4)")
				os.Exit(1)
			}
			i += 2
		} else {
			// Treat as part of description
			if description == "" {
				description = args[i]
			} else {
				description += " " + args[i]
			}
			i++
		}
	}

	db, taskSystem, boardName, err := getCurrentBoardDB()
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	var createdTask *task.Task
	if priority != task.PriorityNone {
		createdTask, err = taskSystem.CreateWithPriority(1, title, description, priority)
	} else {
		createdTask, err = taskSystem.Create(1, title, description)
	}
	
	if err != nil {
		fmt.Printf("Error creating task: %v\n", err)
		os.Exit(1)
	}

	priorityStr := ""
	if createdTask.Priority > 0 {
		priorityStr = fmt.Sprintf(" [%s]", task.GetPriorityName(createdTask.Priority))
	}

	fmt.Printf("Created task #%d%s in board '%s': %s\n", createdTask.ID, priorityStr, boardName, createdTask.Title)
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
		fmt.Println("Error: task ID/title and status required")
		fmt.Println("Usage: cainban move <id|title> <status>")
		fmt.Println("Examples:")
		fmt.Println("  cainban move 5 doing")
		fmt.Println("  cainban move \"bubble tea\" doing")
		os.Exit(1)
	}

	taskIdentifier := args[0]
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

	// Find task by ID or fuzzy match
	foundTask, err := taskSystem.FindTaskByFuzzyID(1, taskIdentifier)
	if err != nil {
		fmt.Printf("Error finding task: %v\n", err)
		os.Exit(1)
	}

	if err := taskSystem.UpdateStatus(foundTask.ID, task.Status(status)); err != nil {
		fmt.Printf("Error moving task: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Moved task #%d \"%s\" to %s in board '%s'\n", foundTask.ID, foundTask.Title, status, boardName)
}

func handleGet(args []string) {
	if len(args) == 0 {
		fmt.Println("Error: task ID or title required")
		fmt.Println("Usage: cainban get <id|title>")
		fmt.Println("Examples:")
		fmt.Println("  cainban get 5")
		fmt.Println("  cainban get \"bubble tea\"")
		os.Exit(1)
	}

	taskIdentifier := args[0]

	db, taskSystem, boardName, err := getCurrentBoardDB()
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	// Find task by ID or fuzzy match
	t, err := taskSystem.FindTaskByFuzzyID(1, taskIdentifier)
	if err != nil {
		fmt.Printf("Error finding task: %v\n", err)
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
		fmt.Println("Error: task ID/title and new title required")
		fmt.Println("Usage: cainban update <id|title> <new_title> [description]")
		fmt.Println("Examples:")
		fmt.Println("  cainban update 5 \"New title\"")
		fmt.Println("  cainban update \"bubble tea\" \"Updated TUI task\"")
		os.Exit(1)
	}

	taskIdentifier := args[0]
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

	// Find task by ID or fuzzy match
	foundTask, err := taskSystem.FindTaskByFuzzyID(1, taskIdentifier)
	if err != nil {
		fmt.Printf("Error finding task: %v\n", err)
		os.Exit(1)
	}

	if err := taskSystem.Update(foundTask.ID, title, description); err != nil {
		fmt.Printf("Error updating task: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Updated task #%d in board '%s': %s\n", foundTask.ID, boardName, title)
}

func handleSearch(args []string) {
	if len(args) == 0 {
		fmt.Println("Error: search query required")
		fmt.Println("Usage: cainban search <query>")
		fmt.Println("Examples:")
		fmt.Println("  cainban search \"bubble tea\"")
		fmt.Println("  cainban search \"prep public\"")
		os.Exit(1)
	}

	query := strings.Join(args, " ")

	db, taskSystem, boardName, err := getCurrentBoardDB()
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	matches, err := taskSystem.SearchTasks(1, query)
	if err != nil {
		fmt.Printf("Error searching tasks: %v\n", err)
		os.Exit(1)
	}

	if len(matches) == 0 {
		fmt.Printf("No tasks found matching '%s' in board '%s'\n", query, boardName)
		return
	}

	fmt.Printf("Search results for '%s' in board '%s':\n\n", query, boardName)
	for i, t := range matches {
		if i >= 10 { // Limit to top 10 results
			fmt.Printf("... and %d more matches\n", len(matches)-10)
			break
		}
		
		priorityStr := ""
		if t.Priority > 0 {
			priorityStr = fmt.Sprintf(" [%s]", task.GetPriorityName(t.Priority))
		}
		
		fmt.Printf("  #%d%s [%s] %s\n", t.ID, priorityStr, t.Status, t.Title)
		if t.Description != "" {
			fmt.Printf("      %s\n", t.Description)
		}
	}
}

func handlePriority(args []string) {
	if len(args) < 2 {
		fmt.Println("Error: task ID/title and priority level required")
		fmt.Println("Usage: cainban priority <id|title> <level>")
		fmt.Println("Priority levels: none, low, medium, high, critical (or 0-4)")
		fmt.Println("Examples:")
		fmt.Println("  cainban priority 5 high")
		fmt.Println("  cainban priority \"bubble tea\" critical")
		os.Exit(1)
	}

	taskIdentifier := args[0]
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

	// Find task by ID or fuzzy match
	foundTask, err := taskSystem.FindTaskByFuzzyID(1, taskIdentifier)
	if err != nil {
		fmt.Printf("Error finding task: %v\n", err)
		os.Exit(1)
	}

	if err := taskSystem.UpdatePriority(foundTask.ID, priorityValue); err != nil {
		fmt.Printf("Error updating task priority: %v\n", err)
		os.Exit(1)
	}

	priorityLevel, _ := task.ParsePriority(priorityValue)
	priorityName := task.GetPriorityName(priorityLevel)
	fmt.Printf("Updated task #%d \"%s\" priority to %s (%d) in board '%s'\n", foundTask.ID, foundTask.Title, priorityName, priorityLevel, boardName)
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

func handleLink(args []string) {
	if len(args) < 2 {
		fmt.Println("Error: from_task_id and to_task_id required")
		fmt.Println("Usage: cainban link <from_task_id> <to_task_id> [link_type]")
		fmt.Println("Link types: blocks (default), blocked_by, related, depends_on")
		os.Exit(1)
	}

	fromTaskID, err := strconv.Atoi(args[0])
	if err != nil {
		fmt.Printf("Error: invalid from_task_id '%s'\n", args[0])
		os.Exit(1)
	}

	toTaskID, err := strconv.Atoi(args[1])
	if err != nil {
		fmt.Printf("Error: invalid to_task_id '%s'\n", args[1])
		os.Exit(1)
	}

	linkType := "blocks" // default
	if len(args) > 2 {
		linkType = args[2]
	}

	db, taskSystem, _, err := getCurrentBoardDB()
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	if err := taskSystem.LinkTasks(fromTaskID, toTaskID, task.LinkType(linkType)); err != nil {
		fmt.Printf("Error linking tasks: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Linked task %d %s task %d\n", fromTaskID, linkType, toTaskID)
}

func handleUnlink(args []string) {
	if len(args) < 2 {
		fmt.Println("Error: from_task_id and to_task_id required")
		fmt.Println("Usage: cainban unlink <from_task_id> <to_task_id> [link_type]")
		fmt.Println("Link types: blocks (default), blocked_by, related, depends_on")
		os.Exit(1)
	}

	fromTaskID, err := strconv.Atoi(args[0])
	if err != nil {
		fmt.Printf("Error: invalid from_task_id '%s'\n", args[0])
		os.Exit(1)
	}

	toTaskID, err := strconv.Atoi(args[1])
	if err != nil {
		fmt.Printf("Error: invalid to_task_id '%s'\n", args[1])
		os.Exit(1)
	}

	linkType := "blocks" // default
	if len(args) > 2 {
		linkType = args[2]
	}

	db, taskSystem, _, err := getCurrentBoardDB()
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	if err := taskSystem.UnlinkTasks(fromTaskID, toTaskID, task.LinkType(linkType)); err != nil {
		fmt.Printf("Error unlinking tasks: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Unlinked task %d %s task %d\n", fromTaskID, linkType, toTaskID)
}

func handleLinks(args []string) {
	if len(args) < 1 {
		fmt.Println("Error: task_id required")
		fmt.Println("Usage: cainban links <task_id>")
		os.Exit(1)
	}

	taskID, err := strconv.Atoi(args[0])
	if err != nil {
		fmt.Printf("Error: invalid task_id '%s'\n", args[0])
		os.Exit(1)
	}

	db, taskSystem, _, err := getCurrentBoardDB()
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	links, err := taskSystem.GetTaskLinks(taskID)
	if err != nil {
		fmt.Printf("Error getting task links: %v\n", err)
		os.Exit(1)
	}

	if len(links) == 0 {
		fmt.Printf("Task %d has no links\n", taskID)
		return
	}

	fmt.Printf("Task %d links:\n", taskID)
	for _, link := range links {
		if link.FromTaskID == taskID {
			fmt.Printf("• %s task %d\n", link.LinkType, link.ToTaskID)
		} else {
			fmt.Printf("• %s by task %d\n", link.LinkType, link.FromTaskID)
		}
	}
}

func handleDelete(args []string) {
	if len(args) < 1 {
		fmt.Println("Error: task_id required")
		fmt.Println("Usage: cainban delete <task_id> [--hard]")
		os.Exit(1)
	}

	taskID, err := strconv.Atoi(args[0])
	if err != nil {
		fmt.Printf("Error: invalid task_id '%s'\n", args[0])
		os.Exit(1)
	}

	hardDelete := len(args) > 1 && args[1] == "--hard"

	db, taskSystem, _, err := getCurrentBoardDB()
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	if hardDelete {
		if err := taskSystem.HardDelete(taskID); err != nil {
			fmt.Printf("Error permanently deleting task: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Task %d permanently deleted\n", taskID)
	} else {
		if err := taskSystem.SoftDelete(taskID); err != nil {
			fmt.Printf("Error deleting task: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Task %d deleted (can be restored)\n", taskID)
	}
}

func handleRestore(args []string) {
	if len(args) < 1 {
		fmt.Println("Error: task_id required")
		fmt.Println("Usage: cainban restore <task_id>")
		os.Exit(1)
	}

	taskID, err := strconv.Atoi(args[0])
	if err != nil {
		fmt.Printf("Error: invalid task_id '%s'\n", args[0])
		os.Exit(1)
	}

	db, taskSystem, _, err := getCurrentBoardDB()
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	if err := taskSystem.RestoreTask(taskID); err != nil {
		fmt.Printf("Error restoring task: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Task %d restored\n", taskID)
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
