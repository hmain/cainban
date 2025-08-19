package mcp

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"strings"

	"github.com/hmain/cainban/src/systems/task"
)

// Server implements the MCP (Model Context Protocol) server
type Server struct {
	taskSystem *task.System
	input      io.Reader
	output     io.Writer
}

// New creates a new MCP server
func New(taskSystem *task.System, input io.Reader, output io.Writer) *Server {
	return &Server{
		taskSystem: taskSystem,
		input:      input,
		output:     output,
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

	createdTask, err := s.taskSystem.Create(boardID, title, description)
	if err != nil {
		return s.errorResponse(req.ID, -32603, fmt.Sprintf("Failed to create task: %v", err))
	}

	return &MCPResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]interface{}{
			"content": []map[string]interface{}{
				{
					"type": "text",
					"text": fmt.Sprintf("Created task #%d: %s", createdTask.ID, createdTask.Title),
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
