# MCP Server Integration Example

This example demonstrates how to use cainban's MCP server with AI agents like Amazon Q.

## Starting the MCP Server

```bash
# Initialize the board first
cainban init

# Start the MCP server
cainban mcp
```

The MCP server will listen on stdin/stdout for JSON-RPC messages.

## Available MCP Tools

### 1. create_task
Creates a new task in the kanban board.

**Input Schema:**
```json
{
  "title": "string (required)",
  "description": "string (optional)",
  "board_id": "integer (optional, defaults to 1)"
}
```

**Example:**
```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "tools/call",
  "params": {
    "name": "create_task",
    "arguments": {
      "title": "Implement user authentication",
      "description": "Add login and registration functionality"
    }
  }
}
```

### 2. list_tasks
Lists tasks from the kanban board, optionally filtered by status.

**Input Schema:**
```json
{
  "board_id": "integer (optional, defaults to 1)",
  "status": "string (optional, one of: todo, doing, done)"
}
```

**Example:**
```json
{
  "jsonrpc": "2.0",
  "id": 2,
  "method": "tools/call",
  "params": {
    "name": "list_tasks",
    "arguments": {
      "status": "todo"
    }
  }
}
```

### 3. update_task_status
Updates the status of a task.

**Input Schema:**
```json
{
  "id": "integer (required)",
  "status": "string (required, one of: todo, doing, done)"
}
```

**Example:**
```json
{
  "jsonrpc": "2.0",
  "id": 3,
  "method": "tools/call",
  "params": {
    "name": "update_task_status",
    "arguments": {
      "id": 1,
      "status": "doing"
    }
  }
}
```

### 4. get_task
Retrieves a specific task by ID.

**Input Schema:**
```json
{
  "id": "integer (required)"
}
```

**Example:**
```json
{
  "jsonrpc": "2.0",
  "id": 4,
  "method": "tools/call",
  "params": {
    "name": "get_task",
    "arguments": {
      "id": 1
    }
  }
}
```

### 5. update_task
Updates a task's title and description.

**Input Schema:**
```json
{
  "id": "integer (required)",
  "title": "string (required)",
  "description": "string (optional)"
}
```

**Example:**
```json
{
  "jsonrpc": "2.0",
  "id": 5,
  "method": "tools/call",
  "params": {
    "name": "update_task",
    "arguments": {
      "id": 1,
      "title": "Updated task title",
      "description": "Updated description"
    }
  }
}
```

## Integration with Amazon Q

To integrate with Amazon Q Chat CLI:

1. Start the cainban MCP server:
   ```bash
   cainban mcp
   ```

2. Configure Q Chat CLI to use the MCP server (configuration depends on Q's MCP client setup)

3. Use natural language with Q to manage tasks:
   - "Create a task to implement user authentication"
   - "List all tasks that are in progress"
   - "Move task 1 to done"
   - "Show me the details of task 2"

## Response Format

All MCP responses follow the JSON-RPC 2.0 format:

**Success Response:**
```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "content": [
      {
        "type": "text",
        "text": "Created task #1: Implement user authentication"
      }
    ],
    "task": {
      "id": 1,
      "board_id": 1,
      "title": "Implement user authentication",
      "description": "Add login and registration functionality",
      "status": "todo",
      "priority": 0,
      "created_at": "2025-08-19T18:20:02Z",
      "updated_at": "2025-08-19T18:20:02Z"
    }
  }
}
```

**Error Response:**
```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "error": {
    "code": -32602,
    "message": "title is required and must be a string"
  }
}
```

## Testing the MCP Server

You can test the MCP server manually using echo and pipes:

```bash
# Start the server in background
cainban mcp &

# Send a create_task request
echo '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"create_task","arguments":{"title":"Test task"}}}' | cainban mcp

# Send a list_tasks request
echo '{"jsonrpc":"2.0","id":2,"method":"tools/list"}' | cainban mcp
```

## Error Codes

- `-32700`: Parse error (invalid JSON)
- `-32600`: Invalid request
- `-32601`: Method not found / Tool not found
- `-32602`: Invalid params
- `-32603`: Internal error (database errors, etc.)
