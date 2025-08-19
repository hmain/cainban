package main

import (
	"fmt"
	"os"
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
		handleList()
	case "move":
		handleMove(os.Args[2:])
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
	fmt.Println("  cainban init                 Initialize new board")
	fmt.Println("  cainban add <title>          Add new task")
	fmt.Println("  cainban list                 List all tasks")
	fmt.Println("  cainban move <id> <status>   Move task between columns")
	fmt.Println("  cainban mcp                  Start MCP server")
	fmt.Println("  cainban version              Show version")
}

func handleInit() {
	fmt.Println("Initializing cainban board...")
	// TODO: Implement board initialization
}

func handleAdd(args []string) {
	if len(args) == 0 {
		fmt.Println("Error: task title required")
		os.Exit(1)
	}
	title := args[0]
	fmt.Printf("Adding task: %s\n", title)
	// TODO: Implement task creation
}

func handleList() {
	fmt.Println("Listing tasks...")
	// TODO: Implement task listing
}

func handleMove(args []string) {
	if len(args) < 2 {
		fmt.Println("Error: task ID and status required")
		os.Exit(1)
	}
	id := args[0]
	status := args[1]
	fmt.Printf("Moving task %s to %s\n", id, status)
	// TODO: Implement task movement
}

func handleMCP() {
	fmt.Println("Starting MCP server...")
	// TODO: Implement MCP server
}

func handleVersion() {
	fmt.Println("cainban v0.1.0")
}
