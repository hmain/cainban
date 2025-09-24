package mcp

import (
	"context"
	"fmt"

	"github.com/hmain/cainban/src/systems/board"
	"github.com/hmain/cainban/src/systems/task"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Server wraps the official MCP SDK server with cainban functionality
type Server struct {
	taskSystem  *task.System
	boardSystem *board.System
	mcpServer   *mcp.Server
}

// New creates a new MCP server using the official Go SDK
func New(taskSystem *task.System) *Server {
	server := &Server{
		taskSystem:  taskSystem,
		boardSystem: board.New(),
	}

	// Create MCP server with cainban implementation info
	server.mcpServer = mcp.NewServer(&mcp.Implementation{
		Name:    "cainban",
		Version: "0.3.0",
	}, nil)

	// Register all tools
	server.registerTools()

	return server
}

// Start starts the MCP server using stdio transport
func (s *Server) Start() error {
	return s.mcpServer.Run(context.Background(), &mcp.StdioTransport{})
}

// registerTools registers all cainban tools with the MCP server
func (s *Server) registerTools() {
	// Create Task tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "create_task",
		Description: "Create a new task in the kanban board",
	}, s.handleCreateTask)

	// List Tasks tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "list_tasks",
		Description: "List tasks from the kanban board",
	}, s.handleListTasks)

	// Update Task Status tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "update_task_status",
		Description: "Update the status of a task",
	}, s.handleUpdateTaskStatus)

	// Get Task tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "get_task",
		Description: "Get a specific task by ID",
	}, s.handleGetTask)

	// Update Task Priority tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "update_task_priority",
		Description: "Update the priority of a task",
	}, s.handleUpdateTaskPriority)

	// Update Task tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "update_task",
		Description: "Update a task's title and description",
	}, s.handleUpdateTask)

	// List Boards tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "list_boards",
		Description: "List all available kanban boards",
	}, s.handleListBoards)

	// Change Board tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "change_board",
		Description: "Change the active kanban board",
	}, s.handleChangeBoard)
}

// Tool handler types for the Go SDK
type CreateTaskArgs struct {
	Title       string      `json:"title" jsonschema:"the title of the task"`
	Description string      `json:"description,omitempty" jsonschema:"the description of the task"`
	BoardID     int         `json:"board_id,omitempty" jsonschema:"the board ID (defaults to 1)"`
	Priority    interface{} `json:"priority,omitempty" jsonschema:"priority level (none, low, medium, high, critical or 0-4)"`
}

type ListTasksArgs struct {
	BoardID int    `json:"board_id,omitempty" jsonschema:"the board ID (defaults to 1)"`
	Status  string `json:"status,omitempty" jsonschema:"filter by status (todo, doing, done)"`
}

type UpdateTaskStatusArgs struct {
	ID     int    `json:"id" jsonschema:"the task ID"`
	Status string `json:"status" jsonschema:"the new status (todo, doing, done)"`
}

type GetTaskArgs struct {
	ID int `json:"id" jsonschema:"the task ID"`
}

type UpdateTaskPriorityArgs struct {
	ID       int         `json:"id" jsonschema:"task ID to update"`
	Priority interface{} `json:"priority" jsonschema:"priority level (none, low, medium, high, critical or 0-4)"`
}

type UpdateTaskArgs struct {
	ID          int    `json:"id" jsonschema:"the task ID"`
	Title       string `json:"title" jsonschema:"the new title"`
	Description string `json:"description,omitempty" jsonschema:"the new description"`
}

type ListBoardsArgs struct{}

type ChangeBoardArgs struct {
	BoardName string `json:"board_name" jsonschema:"the name of the board to switch to"`
}

// Tool handlers
func (s *Server) handleCreateTask(ctx context.Context, req *mcp.CallToolRequest, args CreateTaskArgs) (*mcp.CallToolResult, any, error) {
	boardID := args.BoardID
	if boardID == 0 {
		boardID = 1
	}

	var createdTask *task.Task
	var err error

	if args.Priority != nil && task.IsValidPriority(args.Priority) {
		createdTask, err = s.taskSystem.CreateWithPriority(boardID, args.Title, args.Description, args.Priority)
	} else {
		createdTask, err = s.taskSystem.Create(boardID, args.Title, args.Description)
	}

	if err != nil {
		return nil, nil, fmt.Errorf("failed to create task: %w", err)
	}

	priorityStr := ""
	if createdTask.Priority > 0 {
		priorityStr = fmt.Sprintf(" [%s]", task.GetPriorityName(createdTask.Priority))
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{
				Text: fmt.Sprintf("Created task #%d%s: %s", createdTask.BoardTaskID, priorityStr, createdTask.Title),
			},
		},
	}, createdTask, nil
}

func (s *Server) handleListTasks(ctx context.Context, req *mcp.CallToolRequest, args ListTasksArgs) (*mcp.CallToolResult, any, error) {
	boardID := args.BoardID
	if boardID == 0 {
		boardID = 1
	}

	var tasks []*task.Task
	var err error

	if args.Status != "" {
		if !task.IsValidStatus(args.Status) {
			return nil, nil, fmt.Errorf("invalid status: %s", args.Status)
		}
		tasks, err = s.taskSystem.ListByStatus(boardID, task.Status(args.Status))
	} else {
		tasks, err = s.taskSystem.List(boardID)
	}

	if err != nil {
		return nil, nil, fmt.Errorf("failed to list tasks: %w", err)
	}

	if len(tasks) == 0 {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: "No tasks found in current board"},
			},
		}, tasks, nil
	}

	// Group by status for better display
	tasksByStatus := make(map[task.Status][]*task.Task)
	for _, t := range tasks {
		tasksByStatus[t.Status] = append(tasksByStatus[t.Status], t)
	}

	var content []mcp.Content
	statuses := []task.Status{task.StatusTodo, task.StatusDoing, task.StatusDone}
	for _, status := range statuses {
		if statusTasks, exists := tasksByStatus[status]; exists && len(statusTasks) > 0 {
			content = append(content, &mcp.TextContent{
				Text: fmt.Sprintf("\n%s:", string(status)),
			})
			for _, t := range statusTasks {
				priorityStr := ""
				if t.Priority > 0 {
					priorityStr = fmt.Sprintf(" [%s]", task.GetPriorityName(t.Priority))
				}
				content = append(content, &mcp.TextContent{
					Text: fmt.Sprintf("• #%d%s %s", t.BoardTaskID, priorityStr, t.Title),
				})
			}
		}
	}

	return &mcp.CallToolResult{Content: content}, tasks, nil
}

func (s *Server) handleUpdateTaskStatus(ctx context.Context, req *mcp.CallToolRequest, args UpdateTaskStatusArgs) (*mcp.CallToolResult, any, error) {
	if !task.IsValidStatus(args.Status) {
		return nil, nil, fmt.Errorf("invalid status: %s", args.Status)
	}

	// Use board ID 1 (each board database has its own boards table with ID 1)
	boardID := 1

	// Get task by board-scoped ID
	t, err := s.taskSystem.GetByBoardTaskID(boardID, args.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to find task #%d: %w", args.ID, err)
	}

	// Update using internal ID
	err = s.taskSystem.UpdateStatus(t.ID, task.Status(args.Status))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to update task status: %w", err)
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{
				Text: fmt.Sprintf("Updated task #%d status to %s", args.ID, args.Status),
			},
		},
	}, nil, nil
}

func (s *Server) handleGetTask(ctx context.Context, req *mcp.CallToolRequest, args GetTaskArgs) (*mcp.CallToolResult, any, error) {
	// Use board ID 1 (each board database has its own boards table with ID 1)
	boardID := 1

	// Get task by board-scoped ID
	t, err := s.taskSystem.GetByBoardTaskID(boardID, args.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get task #%d: %w", args.ID, err)
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{
				Text: fmt.Sprintf("#%d [%s] %s\n%s", t.BoardTaskID, t.Status, t.Title, t.Description),
			},
		},
	}, t, nil
}

func (s *Server) handleUpdateTaskPriority(ctx context.Context, req *mcp.CallToolRequest, args UpdateTaskPriorityArgs) (*mcp.CallToolResult, any, error) {
	if !task.IsValidPriority(args.Priority) {
		return nil, nil, fmt.Errorf("invalid priority level")
	}

	// Use board ID 1 (each board database has its own boards table with ID 1)
	boardID := 1

	// Get task by board-scoped ID
	t, err := s.taskSystem.GetByBoardTaskID(boardID, args.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to find task #%d: %w", args.ID, err)
	}

	// Update using internal ID
	err = s.taskSystem.UpdatePriority(t.ID, args.Priority)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to update task priority: %w", err)
	}

	priorityLevel, _ := task.ParsePriority(args.Priority)
	priorityName := task.GetPriorityName(priorityLevel)

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{
				Text: fmt.Sprintf("Task #%d priority updated to %s (%d)", args.ID, priorityName, priorityLevel),
			},
		},
	}, nil, nil
}

func (s *Server) handleUpdateTask(ctx context.Context, req *mcp.CallToolRequest, args UpdateTaskArgs) (*mcp.CallToolResult, any, error) {
	// Use board ID 1 (each board database has its own boards table with ID 1)
	boardID := 1

	// Get task by board-scoped ID
	t, err := s.taskSystem.GetByBoardTaskID(boardID, args.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to find task #%d: %w", args.ID, err)
	}

	// Update using internal ID
	err = s.taskSystem.Update(t.ID, args.Title, args.Description)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to update task: %w", err)
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{
				Text: fmt.Sprintf("Updated task #%d: %s", args.ID, args.Title),
			},
		},
	}, nil, nil
}

func (s *Server) handleListBoards(ctx context.Context, req *mcp.CallToolRequest, args ListBoardsArgs) (*mcp.CallToolResult, any, error) {
	boards, err := s.boardSystem.ListBoards()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list boards: %w", err)
	}

	currentBoard, _ := s.boardSystem.GetCurrentBoard()

	if len(boards) == 0 {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: "No boards found"},
			},
		}, boards, nil
	}

	var content []mcp.Content
	content = append(content, &mcp.TextContent{Text: "Available boards:"})
	for _, b := range boards {
		marker := ""
		if b.Name == currentBoard {
			marker = " (current)"
		}
		content = append(content, &mcp.TextContent{
			Text: fmt.Sprintf("• %s%s", b.Name, marker),
		})
	}

	return &mcp.CallToolResult{Content: content}, boards, nil
}

func (s *Server) handleChangeBoard(ctx context.Context, req *mcp.CallToolRequest, args ChangeBoardArgs) (*mcp.CallToolResult, any, error) {
	// Check if board exists
	_, err := s.boardSystem.GetBoard(args.BoardName)
	if err != nil {
		return nil, nil, fmt.Errorf("board '%s' not found", args.BoardName)
	}

	// Set as current board
	err = s.boardSystem.SetCurrentBoard(args.BoardName)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to change board: %w", err)
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{
				Text: fmt.Sprintf("Changed to board: %s", args.BoardName),
			},
		},
	}, nil, nil
}
