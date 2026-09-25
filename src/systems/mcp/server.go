package mcp

import (
	"context"
	"fmt"
	"net"
	"net/http"

	"github.com/hmain/cainban/src/systems/board"
	"github.com/hmain/cainban/src/systems/store"
	"github.com/hmain/cainban/src/systems/task"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// serverName / serverVersion identify the cainban MCP server in the
// initialize handshake. Kept as package constants so stdio and HTTP transports
// advertise the same implementation info.
const (
	serverName    = "cainban"
	serverVersion = "0.2.1"
)

// Server wraps the official MCP SDK server with cainban functionality.
//
// It is deliberately STATELESS with respect to request routing: it holds no
// database handle and no "current board" selection. Every tool call resolves
// and opens the correct board database on its own, so the same Server value can
// safely serve concurrent requests for different boards (required by the
// stateless Streamable HTTP transport, where a fresh *mcp.Server is built per
// request via getServer).
type Server struct {
	boardSystem *board.System
	// schemaCache is shared across the per-request *mcp.Server instances built
	// by getServer, so JSON schema reflection/resolution happens once rather
	// than on every request.
	schemaCache *mcp.SchemaCache
	// mcpServer is the underlying SDK server used by the stdio transport
	// (Start). The HTTP transport builds its own per-request servers.
	mcpServer *mcp.Server
}

// New creates a new stateless cainban MCP server.
//
// The taskSystem argument is retained for backwards compatibility with existing
// callers/tests but is IGNORED: the server no longer keeps a process-wide task
// system, because board resolution is now per request. Pass nil.
func New(_ *task.System) *Server {
	return NewStateless()
}

// NewStateless creates a new stateless cainban MCP server. This is the
// preferred constructor.
func NewStateless() *Server {
	s := &Server{
		boardSystem: board.New(),
		schemaCache: mcp.NewSchemaCache(),
	}
	s.mcpServer = s.newMCPServer()
	return s
}

// newMCPServer builds a fresh *mcp.Server with all cainban tools registered and
// the shared schema cache installed. Used both for the long-lived stdio server
// and, via getServer, for each stateless HTTP request.
func (s *Server) newMCPServer() *mcp.Server {
	mcpServer := mcp.NewServer(&mcp.Implementation{
		Name:    serverName,
		Version: serverVersion,
	}, &mcp.ServerOptions{
		SchemaCache: s.schemaCache,
	})
	s.registerTools(mcpServer)
	return mcpServer
}

// Start starts the MCP server using the stdio transport (the default mode).
func (s *Server) Start() error {
	return s.mcpServer.Run(context.Background(), &mcp.StdioTransport{})
}

// getServer returns a per-request *mcp.Server for the stateless HTTP handler.
// Tools and the schema cache are shared; no request state leaks between calls
// because the handlers themselves open their board DB per request.
func (s *Server) getServer(_ *http.Request) *mcp.Server {
	return s.newMCPServer()
}

// Handler returns the stateless Streamable-HTTP MCP handler as a plain
// http.Handler. Both the local HTTP server (ServeHTTP) and the Lambda
// entrypoint (cmd/cainban-lambda) mount this same handler, so the transport
// behavior is identical in-process and on Lambda. A fresh *mcp.Server is built
// per request via getServer, keeping the handler safe for concurrent requests.
func (s *Server) Handler() http.Handler {
	return mcp.NewStreamableHTTPHandler(s.getServer, &mcp.StreamableHTTPOptions{
		Stateless: true,
	})
}

// ServeHTTP serves the stateless Streamable HTTP transport on addr, bound to
// loopback only. addr may be ":8080" or "127.0.0.1:8080"; a bare-port or
// wildcard host is rewritten to 127.0.0.1 so the server never binds a public
// interface (multi-user/public exposure is deferred to Phase 3).
func (s *Server) ServeHTTP(addr string) error {
	handler := s.Handler()

	loopbackAddr, err := loopbackOnly(addr)
	if err != nil {
		return err
	}

	httpServer := &http.Server{
		Addr:    loopbackAddr,
		Handler: handler,
	}
	fmt.Printf("cainban MCP server (stateless, Streamable HTTP) listening on http://%s\n", loopbackAddr)
	return httpServer.ListenAndServe()
}

// loopbackOnly forces the host portion of addr to 127.0.0.1, keeping the port.
func loopbackOnly(addr string) (string, error) {
	if addr == "" {
		return "", fmt.Errorf("empty address")
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		// addr had no host:port form (e.g. bare port "8080" or ":8080" already
		// handled by SplitHostPort). Treat a bare number as a port.
		return "", fmt.Errorf("invalid address %q (use host:port or :port): %w", addr, err)
	}
	if host == "" || host == "0.0.0.0" || host == "::" || host == "*" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port), nil
}

// resolveTaskSystem opens the correct board store for a single request and
// returns a store.TaskStore bound to it plus a close func the caller MUST
// defer.
//
// The concrete backend is chosen by store.OpenTask from CAINBAN_BACKEND
// (default sqlite for local dev; dynamodb for the Lambda/serverless path). The
// handlers below depend only on the store.TaskStore interface, so swapping the
// backend requires no handler change.
//
// Board selection is fully explicit per request (no reliance on the
// ~/.cainban/current-board file):
//   - boardName selects which board DATABASE FILE to open under the SQLite
//     backend; empty means the default board. Under DynamoDB the path is
//     ignored (a single table holds every board partition).
//   - Each board DB has its own boards table whose primary board is id 1, so
//     the numeric board ROW defaults to 1 in the handlers.
func (s *Server) resolveTaskSystem(boardName string) (store.TaskStore, func(), error) {
	if boardName == "" {
		boardName = "default"
	}
	dbPath := s.boardSystem.GetBoardPath(boardName)
	ts, closer, err := store.OpenTask(context.Background(), dbPath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open board %q: %w", boardName, err)
	}
	closeFn := func() { _ = closer() }
	return ts, closeFn, nil
}

// registerTools registers all cainban tools on the given SDK server.
func (s *Server) registerTools(mcpServer *mcp.Server) {
	mcp.AddTool(mcpServer, &mcp.Tool{
		Name:        "create_task",
		Description: "Create a new task in the kanban board",
	}, s.handleCreateTask)

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name:        "list_tasks",
		Description: "List tasks from the kanban board",
	}, s.handleListTasks)

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name:        "update_task_status",
		Description: "Update the status of a task",
	}, s.handleUpdateTaskStatus)

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name:        "get_task",
		Description: "Get a specific task by ID",
	}, s.handleGetTask)

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name:        "update_task_priority",
		Description: "Update the priority of a task",
	}, s.handleUpdateTaskPriority)

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name:        "update_task",
		Description: "Update a task's title and description",
	}, s.handleUpdateTask)

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name:        "list_boards",
		Description: "List all available kanban boards",
	}, s.handleListBoards)

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name:        "change_board",
		Description: "Change the active kanban board",
	}, s.handleChangeBoard)
}

// Tool handler argument types.

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

// Tool handlers. Each resolves its board database per request.

func (s *Server) handleCreateTask(ctx context.Context, req *mcp.CallToolRequest, args CreateTaskArgs) (*mcp.CallToolResult, any, error) {
	taskSystem, closeFn, err := s.resolveTaskSystem("")
	if err != nil {
		return nil, nil, err
	}
	defer closeFn()

	boardID := args.BoardID
	if boardID == 0 {
		boardID = 1
	}

	var createdTask *task.Task
	if args.Priority != nil && task.IsValidPriority(args.Priority) {
		createdTask, err = taskSystem.CreateWithPriority(boardID, args.Title, args.Description, args.Priority)
	} else {
		createdTask, err = taskSystem.Create(boardID, args.Title, args.Description)
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
	taskSystem, closeFn, err := s.resolveTaskSystem("")
	if err != nil {
		return nil, nil, err
	}
	defer closeFn()

	boardID := args.BoardID
	if boardID == 0 {
		boardID = 1
	}

	var tasks []*task.Task
	if args.Status != "" {
		if !task.IsValidStatus(args.Status) {
			return nil, nil, fmt.Errorf("invalid status: %s", args.Status)
		}
		tasks, err = taskSystem.ListByStatus(boardID, task.Status(args.Status))
	} else {
		tasks, err = taskSystem.List(boardID)
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

	taskSystem, closeFn, err := s.resolveTaskSystem("")
	if err != nil {
		return nil, nil, err
	}
	defer closeFn()

	// Board row 1: each board database has its own boards table with ID 1.
	boardID := 1

	t, err := taskSystem.GetByBoardTaskID(boardID, args.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to find task #%d: %w", args.ID, err)
	}

	if err := taskSystem.UpdateStatus(t.ID, task.Status(args.Status)); err != nil {
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
	taskSystem, closeFn, err := s.resolveTaskSystem("")
	if err != nil {
		return nil, nil, err
	}
	defer closeFn()

	boardID := 1

	t, err := taskSystem.GetByBoardTaskID(boardID, args.ID)
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

	taskSystem, closeFn, err := s.resolveTaskSystem("")
	if err != nil {
		return nil, nil, err
	}
	defer closeFn()

	boardID := 1

	t, err := taskSystem.GetByBoardTaskID(boardID, args.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to find task #%d: %w", args.ID, err)
	}

	if err := taskSystem.UpdatePriority(t.ID, args.Priority); err != nil {
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
	taskSystem, closeFn, err := s.resolveTaskSystem("")
	if err != nil {
		return nil, nil, err
	}
	defer closeFn()

	boardID := 1

	t, err := taskSystem.GetByBoardTaskID(boardID, args.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to find task #%d: %w", args.ID, err)
	}

	if err := taskSystem.Update(t.ID, args.Title, args.Description); err != nil {
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
		content = append(content, &mcp.TextContent{
			Text: fmt.Sprintf("• %s", b.Name),
		})
	}

	return &mcp.CallToolResult{Content: content}, boards, nil
}

// handleChangeBoard is now a NO-OP with respect to server-side state.
//
// In the stateful design it wrote ~/.cainban/current-board, a process-wide,
// cross-client global side effect that made request routing depend on ambient
// state. In the stateless design each request selects its own board, so this
// handler only VALIDATES that the requested board exists and reports success;
// it does NOT mutate any shared state. The tool is retained (rather than
// removed) so the tools/list schema is unchanged. Board selection per request
// is deferred to Phase 3 (auth-scoped, repo-keyed tenancy).
func (s *Server) handleChangeBoard(ctx context.Context, req *mcp.CallToolRequest, args ChangeBoardArgs) (*mcp.CallToolResult, any, error) {
	if _, err := s.boardSystem.GetBoard(args.BoardName); err != nil {
		return nil, nil, fmt.Errorf("board '%s' not found", args.BoardName)
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{
				Text: fmt.Sprintf("Board '%s' exists. Note: board selection is now per-request (change_board no longer changes global state); pass the board explicitly on each call.", args.BoardName),
			},
		},
	}, nil, nil
}
