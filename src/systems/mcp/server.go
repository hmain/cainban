package mcp

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"strings"

	"github.com/hmain/cainban/src/systems/board"
	"github.com/hmain/cainban/src/systems/task"
)

// Server implements the MCP (Model Context Protocol) server
type Server struct {
	taskSystem  *task.System
	boardSystem *board.System
	input       io.Reader
	output      io.Writer
}

// New creates a new MCP server
func New(taskSystem *task.System, input io.Reader, output io.Writer) *Server {
	return &Server{
		taskSystem:  taskSystem,
		boardSystem: board.New(),
		input:       input,
		output:      output,
	}
}

// MCPRequest represents an MCP request
type MCPRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// MCPResponse represents an MCP response
type MCPResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   *MCPError   `json:"error,omitempty"`
}

// MCPError represents an MCP error
type MCPError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// Tool represents an MCP tool definition
type Tool struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema interface{} `json:"inputSchema"`
}

// Start starts the MCP server
func (s *Server) Start() error {
	decoder := json.NewDecoder(s.input)
	encoder := json.NewEncoder(s.output)

	for {
		var req MCPRequest
		if err := decoder.Decode(&req); err != nil {
			if err == io.EOF {
				break
			}
			log.Printf("Error decoding request: %v", err)
			continue
		}

		resp := s.handleRequest(&req)
		if err := encoder.Encode(resp); err != nil {
			log.Printf("Error encoding response: %v", err)
		}
	}

	return nil
}

// handleRequest processes an MCP request
func (s *Server) handleRequest(req *MCPRequest) *MCPResponse {
	switch req.Method {
	case "initialize":
		return s.handleInitialize(req)
	case "tools/list":
		return s.handleToolsList(req)
	case "tools/call":
		return s.handleToolsCall(req)
	default:
		return &MCPResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &MCPError{
				Code:    -32601,
				Message: "Method not found",
			},
		}
	}
}

// handleInitialize handles the initialize request
func (s *Server) handleInitialize(req *MCPRequest) *MCPResponse {
	result := map[string]interface{}{
		"protocolVersion": "2024-11-05",
		"capabilities": map[string]interface{}{
			"tools": map[string]interface{}{},
		},
		"serverInfo": map[string]interface{}{
			"name":    "cainban",
			"version": "0.1.0",
		},
	}

	return &MCPResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  result,
	}
}

// handleToolsList handles the tools/list request
func (s *Server) handleToolsList(req *MCPRequest) *MCPResponse {
	tools := []Tool{
		{
			Name:        "create_task",
			Description: "Create a new task in the kanban board",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"title": map[string]interface{}{
						"type":        "string",
						"description": "The title of the task",
					},
					"description": map[string]interface{}{
						"type":        "string",
						"description": "The description of the task",
					},
					"board_id": map[string]interface{}{
						"type":        "integer",
						"description": "The board ID (defaults to 1)",
						"default":     1,
					},
					"priority": map[string]interface{}{
						"description": "Priority level (none, low, medium, high, critical or 0-4)",
						"oneOf": []interface{}{
							map[string]interface{}{"type": "integer", "minimum": 0, "maximum": 4},
							map[string]interface{}{"type": "string", "enum": []string{"none", "low", "medium", "high", "critical"}},
						},
					},
				},
				"required": []string{"title"},
			},
		},
		{
			Name:        "list_tasks",
			Description: "List tasks from the kanban board",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"board_id": map[string]interface{}{
						"type":        "integer",
						"description": "The board ID (defaults to 1)",
						"default":     1,
					},
					"status": map[string]interface{}{
						"type":        "string",
						"description": "Filter by status (todo, doing, done)",
						"enum":        []string{"todo", "doing", "done"},
					},
				},
			},
		},
		{
			Name:        "update_task_status",
			Description: "Update the status of a task",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id": map[string]interface{}{
						"type":        "integer",
						"description": "The task ID",
					},
					"status": map[string]interface{}{
						"type":        "string",
						"description": "The new status",
						"enum":        []string{"todo", "doing", "done"},
					},
				},
				"required": []string{"id", "status"},
			},
		},
		{
			Name:        "get_task",
			Description: "Get a specific task by ID",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id": map[string]interface{}{
						"type":        "integer",
						"description": "The task ID",
					},
				},
				"required": []string{"id"},
			},
		},
		{
			Name:        "update_task_priority",
			Description: "Update the priority of a task",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id": map[string]interface{}{
						"type":        "integer",
						"description": "Task ID to update",
					},
					"priority": map[string]interface{}{
						"description": "Priority level (none, low, medium, high, critical or 0-4)",
						"oneOf": []interface{}{
							map[string]interface{}{"type": "integer", "minimum": 0, "maximum": 4},
							map[string]interface{}{"type": "string", "enum": []string{"none", "low", "medium", "high", "critical"}},
						},
					},
				},
				"required": []string{"id", "priority"},
			},
		},
		{
			Name:        "update_task",
			Description: "Update a task's title and description",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id": map[string]interface{}{
						"type":        "integer",
						"description": "The task ID",
					},
					"title": map[string]interface{}{
						"type":        "string",
						"description": "The new title",
					},
					"description": map[string]interface{}{
						"type":        "string",
						"description": "The new description",
					},
				},
				"required": []string{"id", "title"},
			},
		},
		{
			Name:        "list_boards",
			Description: "List all available kanban boards",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			Name:        "link_tasks",
			Description: "Create a link between two tasks",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"from_task_id": map[string]interface{}{
						"type":        "integer",
						"description": "The ID of the source task",
					},
					"to_task_id": map[string]interface{}{
						"type":        "integer",
						"description": "The ID of the target task",
					},
					"link_type": map[string]interface{}{
						"type":        "string",
						"description": "Type of link (blocks, blocked_by, related, depends_on)",
						"enum":        []string{"blocks", "blocked_by", "related", "depends_on"},
						"default":     "blocks",
					},
				},
				"required": []string{"from_task_id", "to_task_id"},
			},
		},
		{
			Name:        "unlink_tasks",
			Description: "Remove a link between two tasks",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"from_task_id": map[string]interface{}{
						"type":        "integer",
						"description": "The ID of the source task",
					},
					"to_task_id": map[string]interface{}{
						"type":        "integer",
						"description": "The ID of the target task",
					},
					"link_type": map[string]interface{}{
						"type":        "string",
						"description": "Type of link to remove (blocks, blocked_by, related, depends_on)",
						"enum":        []string{"blocks", "blocked_by", "related", "depends_on"},
						"default":     "blocks",
					},
				},
				"required": []string{"from_task_id", "to_task_id"},
			},
		},
		{
			Name:        "get_task_links",
			Description: "Get all links for a specific task",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"task_id": map[string]interface{}{
						"type":        "integer",
						"description": "The task ID to get links for",
					},
				},
				"required": []string{"task_id"},
			},
		},
		{
			Name:        "delete_task",
			Description: "Delete a task (soft delete by default)",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"task_id": map[string]interface{}{
						"type":        "integer",
						"description": "The task ID to delete",
					},
					"hard_delete": map[string]interface{}{
						"type":        "boolean",
						"description": "Whether to permanently delete the task",
						"default":     false,
					},
				},
				"required": []string{"task_id"},
			},
		},
		{
			Name:        "restore_task",
			Description: "Restore a soft-deleted task",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"task_id": map[string]interface{}{
						"type":        "integer",
						"description": "The task ID to restore",
					},
				},
				"required": []string{"task_id"},
			},
		},
		{
			Name:        "change_board",
			Description: "Change the active kanban board",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"board_name": map[string]interface{}{
						"type":        "string",
						"description": "The name of the board to switch to",
					},
				},
				"required": []string{"board_name"},
			},
		},
	}

	return &MCPResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]interface{}{
			"tools": tools,
		},
	}
}

// handleToolsCall handles the tools/call request
func (s *Server) handleToolsCall(req *MCPRequest) *MCPResponse {
	var params struct {
		Name      string                 `json:"name"`
		Arguments map[string]interface{} `json:"arguments"`
	}

	if err := json.Unmarshal(req.Params, &params); err != nil {
		return &MCPResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &MCPError{
				Code:    -32602,
				Message: "Invalid params",
				Data:    err.Error(),
			},
		}
	}

	switch params.Name {
	case "create_task":
		return s.handleCreateTask(req, params.Arguments)
	case "list_tasks":
		return s.handleListTasks(req, params.Arguments)
	case "update_task_status":
		return s.handleUpdateTaskStatus(req, params.Arguments)
	case "get_task":
		return s.handleGetTask(req, params.Arguments)
	case "update_task_priority":
		return s.handleUpdateTaskPriority(req, params.Arguments)
	case "update_task":
		return s.handleUpdateTask(req, params.Arguments)
	case "list_boards":
		return s.handleListBoards(req, params.Arguments)
	case "change_board":
		return s.handleChangeBoard(req, params.Arguments)
	case "link_tasks":
		return s.handleLinkTasks(req, params.Arguments)
	case "unlink_tasks":
		return s.handleUnlinkTasks(req, params.Arguments)
	case "get_task_links":
		return s.handleGetTaskLinks(req, params.Arguments)
	case "delete_task":
		return s.handleDeleteTask(req, params.Arguments)
	case "restore_task":
		return s.handleRestoreTask(req, params.Arguments)
	default:
		return &MCPResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &MCPError{
				Code:    -32601,
				Message: "Tool not found",
			},
		}
	}
}

// handleCreateTask handles the create_task tool call
func (s *Server) handleCreateTask(req *MCPRequest, args map[string]interface{}) *MCPResponse {
	title, ok := args["title"].(string)
	if !ok {
		return s.errorResponse(req.ID, -32602, "title is required and must be a string")
	}

	description, _ := args["description"].(string)
	
	boardID := 1 // Default board
	if bid, ok := args["board_id"].(float64); ok {
		boardID = int(bid)
	}

	var createdTask *task.Task
	var err error

	// Check if priority is provided
	if priority, hasPriority := args["priority"]; hasPriority {
		if !task.IsValidPriority(priority) {
			return s.errorResponse(req.ID, -32602, "Invalid priority level")
		}
		createdTask, err = s.taskSystem.CreateWithPriority(boardID, title, description, priority)
	} else {
		createdTask, err = s.taskSystem.Create(boardID, title, description)
	}

	if err != nil {
		return s.errorResponse(req.ID, -32603, fmt.Sprintf("Failed to create task: %v", err))
	}

	priorityStr := ""
	if createdTask.Priority > 0 {
		priorityStr = fmt.Sprintf(" [%s]", task.GetPriorityName(createdTask.Priority))
	}

	return &MCPResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]interface{}{
			"content": []map[string]interface{}{
				{
					"type": "text",
					"text": fmt.Sprintf("Created task #%d%s: %s", createdTask.ID, priorityStr, createdTask.Title),
				},
			},
			"task": createdTask,
		},
	}
}

// handleListTasks handles the list_tasks tool call
func (s *Server) handleListTasks(req *MCPRequest, args map[string]interface{}) *MCPResponse {
	boardID := 1 // Default board
	if bid, ok := args["board_id"].(float64); ok {
		boardID = int(bid)
	}

	var tasks []*task.Task
	var err error

	if statusStr, ok := args["status"].(string); ok {
		status := task.Status(statusStr)
		if !task.IsValidStatus(statusStr) {
			return s.errorResponse(req.ID, -32602, "Invalid status")
		}
		tasks, err = s.taskSystem.ListByStatus(boardID, status)
	} else {
		tasks, err = s.taskSystem.List(boardID)
	}

	if err != nil {
		return s.errorResponse(req.ID, -32603, fmt.Sprintf("Failed to list tasks: %v", err))
	}

	// Format tasks for display with board context
	var content []map[string]interface{}
	if len(tasks) == 0 {
		content = append(content, map[string]interface{}{
			"type": "text",
			"text": "No tasks found in current board",
		})
	} else {
		// Group by status for better display
		tasksByStatus := make(map[task.Status][]*task.Task)
		for _, t := range tasks {
			tasksByStatus[t.Status] = append(tasksByStatus[t.Status], t)
		}

		statuses := []task.Status{task.StatusTodo, task.StatusDoing, task.StatusDone}
		for _, status := range statuses {
			if statusTasks, exists := tasksByStatus[status]; exists && len(statusTasks) > 0 {
				content = append(content, map[string]interface{}{
					"type": "text",
					"text": fmt.Sprintf("\n%s:", strings.ToUpper(string(status))),
				})
				for _, t := range statusTasks {
					priorityStr := ""
					if t.Priority > 0 {
						priorityStr = fmt.Sprintf(" [%s]", task.GetPriorityName(t.Priority))
					}
					content = append(content, map[string]interface{}{
						"type": "text",
						"text": fmt.Sprintf("• #%d%s %s", t.ID, priorityStr, t.Title),
					})
				}
			}
		}
	}

	return &MCPResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]interface{}{
			"content": content,
			"tasks":   tasks,
		},
	}
}

// handleUpdateTaskStatus handles the update_task_status tool call
func (s *Server) handleUpdateTaskStatus(req *MCPRequest, args map[string]interface{}) *MCPResponse {
	idFloat, ok := args["id"].(float64)
	if !ok {
		return s.errorResponse(req.ID, -32602, "id is required and must be a number")
	}
	id := int(idFloat)

	statusStr, ok := args["status"].(string)
	if !ok {
		return s.errorResponse(req.ID, -32602, "status is required and must be a string")
	}

	if !task.IsValidStatus(statusStr) {
		return s.errorResponse(req.ID, -32602, "Invalid status")
	}

	status := task.Status(statusStr)
	if err := s.taskSystem.UpdateStatus(id, status); err != nil {
		return s.errorResponse(req.ID, -32603, fmt.Sprintf("Failed to update task status: %v", err))
	}

	return &MCPResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]interface{}{
			"content": []map[string]interface{}{
				{
					"type": "text",
					"text": fmt.Sprintf("Updated task #%d status to %s", id, status),
				},
			},
		},
	}
}

// handleGetTask handles the get_task tool call
func (s *Server) handleGetTask(req *MCPRequest, args map[string]interface{}) *MCPResponse {
	idFloat, ok := args["id"].(float64)
	if !ok {
		return s.errorResponse(req.ID, -32602, "id is required and must be a number")
	}
	id := int(idFloat)

	t, err := s.taskSystem.GetByID(id)
	if err != nil {
		return s.errorResponse(req.ID, -32603, fmt.Sprintf("Failed to get task: %v", err))
	}

	return &MCPResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]interface{}{
			"content": []map[string]interface{}{
				{
					"type": "text",
					"text": fmt.Sprintf("#%d [%s] %s\n%s", t.ID, t.Status, t.Title, t.Description),
				},
			},
			"task": t,
		},
	}
}

// handleUpdateTaskPriority handles the update_task_priority tool call
func (s *Server) handleUpdateTaskPriority(req *MCPRequest, args map[string]interface{}) *MCPResponse {
	idFloat, ok := args["id"].(float64)
	if !ok {
		return s.errorResponse(req.ID, -32602, "Invalid or missing task ID")
	}
	id := int(idFloat)

	priority, ok := args["priority"]
	if !ok {
		return s.errorResponse(req.ID, -32602, "Missing priority")
	}

	if !task.IsValidPriority(priority) {
		return s.errorResponse(req.ID, -32602, "Invalid priority level")
	}

	if err := s.taskSystem.UpdatePriority(id, priority); err != nil {
		return s.errorResponse(req.ID, -32603, fmt.Sprintf("Failed to update task priority: %v", err))
	}

	priorityLevel, _ := task.ParsePriority(priority)
	priorityName := task.GetPriorityName(priorityLevel)

	return &MCPResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]interface{}{
			"content": []map[string]interface{}{
				{
					"type": "text",
					"text": fmt.Sprintf("Task #%d priority updated to %s (%d)", id, priorityName, priorityLevel),
				},
			},
		},
	}
}

// handleUpdateTask handles the update_task tool call
func (s *Server) handleUpdateTask(req *MCPRequest, args map[string]interface{}) *MCPResponse {
	idFloat, ok := args["id"].(float64)
	if !ok {
		return s.errorResponse(req.ID, -32602, "id is required and must be a number")
	}
	id := int(idFloat)

	title, ok := args["title"].(string)
	if !ok {
		return s.errorResponse(req.ID, -32602, "title is required and must be a string")
	}

	description, _ := args["description"].(string)

	if err := s.taskSystem.Update(id, title, description); err != nil {
		return s.errorResponse(req.ID, -32603, fmt.Sprintf("Failed to update task: %v", err))
	}

	return &MCPResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]interface{}{
			"content": []map[string]interface{}{
				{
					"type": "text",
					"text": fmt.Sprintf("Updated task #%d: %s", id, title),
				},
			},
		},
	}
}

// errorResponse creates an error response
func (s *Server) errorResponse(id interface{}, code int, message string) *MCPResponse {
	return &MCPResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: &MCPError{
			Code:    code,
			Message: message,
		},
	}
}

// handleListBoards handles the list_boards tool call
func (s *Server) handleListBoards(req *MCPRequest, args map[string]interface{}) *MCPResponse {
	boards, err := s.boardSystem.ListBoards()
	if err != nil {
		return s.errorResponse(req.ID, -32603, fmt.Sprintf("Failed to list boards: %v", err))
	}

	currentBoard, _ := s.boardSystem.GetCurrentBoard()

	var content []map[string]interface{}
	if len(boards) == 0 {
		content = append(content, map[string]interface{}{
			"type": "text",
			"text": "No boards found",
		})
	} else {
		content = append(content, map[string]interface{}{
			"type": "text",
			"text": "Available boards:",
		})
		for _, b := range boards {
			marker := ""
			if b.Name == currentBoard {
				marker = " (current)"
			}
			content = append(content, map[string]interface{}{
				"type": "text",
				"text": fmt.Sprintf("• %s%s", b.Name, marker),
			})
		}
	}

	return &MCPResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]interface{}{
			"content": content,
			"boards": boards,
		},
	}
}

// handleChangeBoard handles the change_board tool call
func (s *Server) handleChangeBoard(req *MCPRequest, args map[string]interface{}) *MCPResponse {
	boardName, ok := args["board_name"].(string)
	if !ok {
		return s.errorResponse(req.ID, -32602, "board_name is required and must be a string")
	}

	// Check if board exists
	_, err := s.boardSystem.GetBoard(boardName)
	if err != nil {
		return s.errorResponse(req.ID, -32603, fmt.Sprintf("Board '%s' not found", boardName))
	}

	// Set as current board
	if err := s.boardSystem.SetCurrentBoard(boardName); err != nil {
		return s.errorResponse(req.ID, -32603, fmt.Sprintf("Failed to change board: %v", err))
	}

	return &MCPResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]interface{}{
			"content": []map[string]interface{}{
				{
					"type": "text",
					"text": fmt.Sprintf("Changed to board: %s", boardName),
				},
			},
		},
	}
}

func (s *Server) handleLinkTasks(req *MCPRequest, args map[string]interface{}) *MCPResponse {
	fromTaskID, ok := args["from_task_id"].(float64)
	if !ok {
		return s.errorResponse(req.ID, -32602, "from_task_id is required and must be an integer")
	}

	toTaskID, ok := args["to_task_id"].(float64)
	if !ok {
		return s.errorResponse(req.ID, -32602, "to_task_id is required and must be an integer")
	}

	linkType := "blocks" // default
	if lt, exists := args["link_type"].(string); exists {
		linkType = lt
	}

	err := s.taskSystem.LinkTasks(int(fromTaskID), int(toTaskID), task.LinkType(linkType))
	if err != nil {
		return s.errorResponse(req.ID, -32603, fmt.Sprintf("Failed to link tasks: %v", err))
	}

	return &MCPResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]interface{}{
			"content": []map[string]interface{}{
				{
					"type": "text",
					"text": fmt.Sprintf("Linked task %d %s task %d", int(fromTaskID), linkType, int(toTaskID)),
				},
			},
		},
	}
}

func (s *Server) handleUnlinkTasks(req *MCPRequest, args map[string]interface{}) *MCPResponse {
	fromTaskID, ok := args["from_task_id"].(float64)
	if !ok {
		return s.errorResponse(req.ID, -32602, "from_task_id is required and must be an integer")
	}

	toTaskID, ok := args["to_task_id"].(float64)
	if !ok {
		return s.errorResponse(req.ID, -32602, "to_task_id is required and must be an integer")
	}

	linkType := "blocks" // default
	if lt, exists := args["link_type"].(string); exists {
		linkType = lt
	}

	err := s.taskSystem.UnlinkTasks(int(fromTaskID), int(toTaskID), task.LinkType(linkType))
	if err != nil {
		return s.errorResponse(req.ID, -32603, fmt.Sprintf("Failed to unlink tasks: %v", err))
	}

	return &MCPResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]interface{}{
			"content": []map[string]interface{}{
				{
					"type": "text",
					"text": fmt.Sprintf("Unlinked task %d %s task %d", int(fromTaskID), linkType, int(toTaskID)),
				},
			},
		},
	}
}

func (s *Server) handleGetTaskLinks(req *MCPRequest, args map[string]interface{}) *MCPResponse {
	taskID, ok := args["task_id"].(float64)
	if !ok {
		return s.errorResponse(req.ID, -32602, "task_id is required and must be an integer")
	}

	links, err := s.taskSystem.GetTaskLinks(int(taskID))
	if err != nil {
		return s.errorResponse(req.ID, -32603, fmt.Sprintf("Failed to get task links: %v", err))
	}

	if len(links) == 0 {
		return &MCPResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]interface{}{
				"content": []map[string]interface{}{
					{
						"type": "text",
						"text": fmt.Sprintf("Task %d has no links", int(taskID)),
					},
				},
			},
		}
	}

	var linkTexts []string
	for _, link := range links {
		if link.FromTaskID == int(taskID) {
			linkTexts = append(linkTexts, fmt.Sprintf("• %s task %d", link.LinkType, link.ToTaskID))
		} else {
			linkTexts = append(linkTexts, fmt.Sprintf("• %s by task %d", link.LinkType, link.FromTaskID))
		}
	}

	return &MCPResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]interface{}{
			"content": []map[string]interface{}{
				{
					"type": "text",
					"text": fmt.Sprintf("Task %d links:\n%s", int(taskID), strings.Join(linkTexts, "\n")),
				},
			},
		},
	}
}

func (s *Server) handleDeleteTask(req *MCPRequest, args map[string]interface{}) *MCPResponse {
	taskID, ok := args["task_id"].(float64)
	if !ok {
		return s.errorResponse(req.ID, -32602, "task_id is required and must be an integer")
	}

	hardDelete := false
	if hd, exists := args["hard_delete"].(bool); exists {
		hardDelete = hd
	}

	var err error
	if hardDelete {
		err = s.taskSystem.HardDelete(int(taskID))
	} else {
		err = s.taskSystem.SoftDelete(int(taskID))
	}

	if err != nil {
		return s.errorResponse(req.ID, -32603, fmt.Sprintf("Failed to delete task: %v", err))
	}

	var deleteType string
	if hardDelete {
		deleteType = "permanently deleted"
	} else {
		deleteType = "deleted (can be restored)"
	}

	return &MCPResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]interface{}{
			"content": []map[string]interface{}{
				{
					"type": "text",
					"text": fmt.Sprintf("Task %d %s", int(taskID), deleteType),
				},
			},
		},
	}
}

func (s *Server) handleRestoreTask(req *MCPRequest, args map[string]interface{}) *MCPResponse {
	taskID, ok := args["task_id"].(float64)
	if !ok {
		return s.errorResponse(req.ID, -32602, "task_id is required and must be an integer")
	}

	err := s.taskSystem.RestoreTask(int(taskID))
	if err != nil {
		return s.errorResponse(req.ID, -32603, fmt.Sprintf("Failed to restore task: %v", err))
	}

	return &MCPResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]interface{}{
			"content": []map[string]interface{}{
				{
					"type": "text",
					"text": fmt.Sprintf("Task %d restored", int(taskID)),
				},
			},
		},
	}
}
