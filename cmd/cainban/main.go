package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

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
		handleInit()
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
	fmt.Println("  cainban init                     Initialize new board")
	fmt.Println("  cainban add <title> [description] Add new task")
	fmt.Println("  cainban list [status]            List all tasks or by status")
	fmt.Println("  cainban move <id> <status>       Move task between columns")
	fmt.Println("  cainban get <id>                 Get task details")
	fmt.Println("  cainban update <id> <title> [description] Update task")
	fmt.Println("  cainban mcp                      Start MCP server")
	fmt.Println("  cainban version                  Show version")
	fmt.Println()
	fmt.Println("Statuses: todo, doing, done")
}

func getDBPath() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "./cainban.db"
	}
	return filepath.Join(homeDir, ".cainban", "cainban.db")
}

func initDB() (*storage.DB, *task.System, error) {
	db, err := storage.New(getDBPath())
	if err != nil {
		return nil, nil, fmt.Errorf("failed to initialize database: %w", err)
	}

	taskSystem := task.New(db.Conn())
	return db, taskSystem, nil
}

func handleInit() {
	fmt.Println("Initializing cainban board...")
	
	db, _, err := initDB()
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	fmt.Printf("Board initialized at: %s\n", db.Path())
	fmt.Println("You can now add tasks with: cainban add \"Your task title\"")
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

	db, taskSystem, err := initDB()
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

	fmt.Printf("Created task #%d: %s\n", createdTask.ID, createdTask.Title)
	if createdTask.Description != "" {
		fmt.Printf("Description: %s\n", createdTask.Description)
	}
}

func handleList(args []string) {
	db, taskSystem, err := initDB()
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
				fmt.Printf("  #%d %s\n", t.ID, t.Title)
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

	db, taskSystem, err := initDB()
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	if err := taskSystem.UpdateStatus(id, task.Status(status)); err != nil {
		fmt.Printf("Error moving task: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Moved task #%d to %s\n", id, status)
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

	db, taskSystem, err := initDB()
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

	fmt.Printf("Task #%d [%s]\n", t.ID, t.Status)
	fmt.Printf("Title: %s\n", t.Title)
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

	db, taskSystem, err := initDB()
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	if err := taskSystem.Update(id, title, description); err != nil {
		fmt.Printf("Error updating task: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Updated task #%d: %s\n", id, title)
}

func handleMCP() {
	fmt.Println("Starting MCP server...")
	
	db, taskSystem, err := initDB()
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
	fmt.Println("cainban v0.1.0")
}
