# cainban

cainban (c-AI-nban) is a command-line kanban board designed to expose all commands as command-line options. You can use it to manage a todo-list, your daily tasks, or your personal development backlog. 
It also enables AI code generators through its MCP server to decompose tasks into smaller components, allowing the AI agent to concentrate on delivering the entire project step by step.

## Overview

- **Command-line first**: All operations can be performed via CLI without launching a GUI application
- **AI-native**: Support for Claude code, Amazon Q Developer and other AI agents
- **MCP Integration**: Built-in Model Context Protocol (MCP) server for seamless AI integration
- **Terminal UI**: Rich terminal interface using Bubble Tea
- **Markdown Support**: Native markdown rendering with Glow
- **SQLite Backend**: Lightweight, file-based database storage

## Quick Start

### 1. Install and Setup

```bash
# Clone the repository
git clone https://github.com/hmain/cainban.git
cd cainban

# Build dependencies
go mod tidy

# Build the binary
go build -o cainban cmd/cainban/main.go

# Initialize your kanban board
./cainban init
```

### 2. Basic Usage

```bash
# Add tasks
./cainban add "Implement user authentication" "Add login and registration functionality"

# List all tasks
./cainban list

# List tasks by status
./cainban list todo
./cainban list doing
./cainban list done

# Move tasks between columns (by ID or fuzzy title match)
./cainban move 1 doing
./cainban move "user auth" doing

# Get task details (by ID or fuzzy title match)
./cainban get 1
./cainban get "user auth"

# Update task (by ID or fuzzy title match)
./cainban update 1 "Updated task title" "Updated description"
./cainban update "user auth" "Enhanced authentication system"

# Set task priority
./cainban priority 1 high
./cainban priority "user auth" critical

# Link tasks together
./cainban link 1 2 blocks          # Task 1 blocks Task 2
./cainban link 3 4 depends_on      # Task 3 depends on Task 4
./cainban links 1                  # Show all links for Task 1
./cainban unlink 1 2 blocks        # Remove link between tasks

# Delete and restore tasks
./cainban delete 5                 # Soft delete (can be restored)
./cainban delete 6 --hard          # Permanent delete (cannot be restored)
./cainban restore 5                # Restore soft-deleted task

# Search tasks by title
./cainban search "auth"
```

### 3. MCP Server for AI Codegen integration

1. **Create MCP configuration**:
   
```bash
cat > mcp.json << 'EOF'
{
  "mcpServers": {
    "cainban": {
      "command": "/path/to/your/cainban/cainban",
      "args": ["mcp"]
    }
  }
}
EOF
```

2. **Update the path** in the configuration above to point to your cainban binary location.

3. **Test the integration**:
   
```bash
# Try these commands:
"List all my tasks in cainban"
"Create a new task called 'Setup CI/CD pipeline'"
"Move task 1 to doing status"
```

#### Natural Language Task Management

Once configured, you can manage your kanban board through natural conversation:

- **"List my tasks"** → Shows all tasks organized by status with priority indicators
- **"Create a task to implement user auth"** → Creates new task
- **"Move task 3 to doing"** → Updates task status
- **"Set task 5 to high priority"** → Updates task priority
- **"Show me details for task 5"** → Gets complete task information
- **"Add a task for code review with description 'Review PR #123'"** → Creates task with description
- **"List all my boards"** → Shows available kanban boards
- **"Switch to the project board"** → Changes active board

### Advanced Usage

For a bit more advanced usage:

- Start working on the next tasks in the **Cainban** to-do list or backlog. If a task has subtasks, begin with those (get_task_links). Update the **next-steps.md** file with a clear plan for how to solve the problem. Follow good Git practices, like using branches and other Git tools. Use the "default" **Cainban** board for your tasks. If you find any issues, create new tasks for them. Break down tasks into smaller subtasks so you can focus on one small problem at a time.


## Key Features

### 🎯 **Task Priority Management**
Set and manage task priorities with both CLI and AI integration:

```bash
# Set priority levels: none, low, medium, high, critical (or 0-4)
./cainban priority 1 high
./cainban priority "user auth" critical

# Tasks automatically sort by priority in listings
# Critical tasks appear first, followed by high, medium, low, none
```

**Priority Display:**
```
TODO:
  #8 [critical] Implement task dependencies
  #6 [high] Implement Bubble Tea TUI  
  #10 [high] Prepare for public release
  #9 [medium] Enhanced AI features
  #2 Create terminal UI (legacy)        # No priority = none
```

### 🔍 **Fuzzy Task Search**
Reference tasks by partial titles instead of remembering IDs:

```bash
# Instead of: ./cainban move 10 doing
./cainban move "prep public" doing

# Instead of: ./cainban get 6  
./cainban get "bubble tea"

# Instead of: ./cainban priority 9 high
./cainban priority "enhanced ai" high

# Explicit search for exploration
./cainban search "terminal"
```

**Smart Matching:**
- **Exact match**: Highest priority
- **Substring match**: High priority  
- **Word prefix**: Medium priority
- **Multiple words**: Bonus scoring

**Conflict Resolution:**
- Numeric input prioritizes ID lookup first
- Falls back to fuzzy search if ID doesn't exist
- Multiple matches show helpful suggestions

## Architecture

- **Language**: Go
- **Database**: SQLite
- **Systems Architecture**: Modular systems in `src/systems/` for extensibility
- TODO: **TUI Framework**: [Bubble Tea](https://github.com/charmbracelet/bubbletea)
- TODO: **Markdown Rendering**: [Glow](https://github.com/charmbracelet/glow)

## AI Integration

cainban is designed to work seamlessly with AI agents:

### MCP Server
- Exposes cainban operations as MCP tools
- Real-time board state synchronization
- JSON-RPC 2.0 compliant
- Tools available: create_task, list_tasks, update_task_status, get_task, update_task

## MCP Setup Options

### Global Access (Recommended)
Configure cainban globally to use from any project:

```json
{
  "mcpServers": {
    "cainban": {
      "command": "/path/to/cainban/cainban",
      "args": ["mcp"]
    }
  }
}
```

### Project-Specific Access
For team projects, add to your project root:

```bash
cat > mcp.json << 'EOF'
{
  "mcpServers": {
    "cainban": {
      "command": "/path/to/cainban/cainban",
      "args": ["mcp"]
    }
  }
}
EOF
```

Team members will automatically get cainban access when they clone your project.

## Available MCP Tools

| Tool | Description | Example Usage |
|------|-------------|---------------|
| `create_task` | Create new tasks | "Create a task to fix the login bug" |
| `list_tasks` | List all tasks or by status | "Show me all my todo tasks" |
| `update_task_status` | Move tasks between columns | "Move task 3 to doing" |
| `update_task_priority` | Set task priority | "Set task 5 to high priority" |
| `get_task` | Get detailed task information | "Show me details for task 5" |
| `update_task` | Update task title/description | "Update task 2 with new requirements" |
| `link_tasks` | Create links between tasks | "Link task 1 to block task 2" |
| `unlink_tasks` | Remove links between tasks | "Unlink task 1 from task 2" |
| `get_task_links` | Show all links for a task | "Show me all links for task 5" |
| `delete_task` | Delete task (soft delete by default) | "Delete task 8" |
| `restore_task` | Restore a soft-deleted task | "Restore task 8" |
| `list_boards` | List all available boards | "Show me all my boards" |
| `change_board` | Switch to a different board | "Switch to the project board" |

## Development

### Prerequisites
- Go 1.21+
- SQLite3

### Setup
```bash
git clone https://github.com/hmain/cainban.git
cd cainban
go mod tidy
go run cmd/cainban/main.go init
```

### Testing
```bash
# Run all tests
go test ./...

# Run with coverage
go test -cover ./...

# Run with race detection
go test -race ./...

# Run specific system tests
go test ./src/systems/board/...
```

### Code Quality

#### Syntax Validation
- **Go**: Use `go vet` and `golangci-lint` for static analysis
- **SQL**: Validate SQLite schema with `sqlite3 -bail`
- **Markdown**: Use `markdownlint` for documentation consistency

#### Runtime Error Checking
- **Go**: Use `go test -race` for race condition detection
- **Database**: Enable SQLite foreign key constraints and WAL mode
- **Memory**: Use `go test -memprofile` for memory leak detection

### Git Workflow

This project follows a feature branch workflow:

1. Create feature branches from `main`
2. Use descriptive branch names: `feature/board-system`, `fix/sqlite-connection`
3. Squash commits before merging to maintain clean history
4. Delete branches after successful merge
5. No compatibility bridges - breaking changes are acceptable during development

### Project Structure

```
cainban/
├── cmd/cainban/           # Main CLI application
├── src/systems/           # Modular system components
│   ├── board/            # Board management system
│   ├── task/             # Task management system
│   ├── mcp/              # MCP server system
│   └── storage/          # Database abstraction system
├── internal/             # Internal packages
├── docs/                 # Documentation
├── tests/                # Test files and test documentation
└── examples/             # Usage examples
```

## Troubleshooting

### MCP Server Issues
1. **Server not loading**: Check timeout settings with `q settings mcp.noInteractiveTimeout 5000`
2. **Tools not available**: Verify binary path in MCP configuration
3. **Database errors**: Run `./cainban init` to initialize the database

### Common Solutions
```bash
# Test MCP server manually
echo '{"jsonrpc":"2.0","id":1,"method":"initialize"}' | ./cainban mcp

# Check if binary is executable
chmod +x ./cainban

# Verify database location
ls -la ~/.cainban/cainban.db
```

## Status:

**Current Version**: v0.2.2 - Priority Management & Fuzzy Search  
**New Features**: Task priorities, fuzzy search, natural language task references  

### Workflow Examples

**Priority-based Task Management:**
```bash
# Set priorities for better organization
./cainban priority "implement auth" critical
./cainban priority "write docs" medium
./cainban priority "refactor code" low

# Tasks automatically sort by priority
./cainban list todo
# Output:
# TODO:
#   #5 [critical] Implement user authentication
#   #8 [high] Setup CI/CD pipeline
#   #3 [medium] Write documentation
#   #7 [low] Refactor legacy code
#   #2 Update README                    # No priority
```

**Fuzzy Task Operations:**
```bash
# Natural task references (no IDs needed!)
./cainban move "implement auth" doing
./cainban get "ci cd"
./cainban update "legacy code" "Modernize codebase"
./cainban priority "write docs" high

# Search and explore
./cainban search "auth"
./cainban search "setup"
```

**AI-Powered Management:**
```bash
# Natural language 
q chat "Set the authentication task to critical priority"
q chat "Move the CI/CD task to doing status"
q chat "List my high priority tasks"
q chat "Create a task for database migration"
```

## Contributing

1. Fork the repository
2. Create a feature branch
3. Make your changes following the code quality guidelines
4. Add tests for new functionality
5. Submit a pull request

## License

MIT License - see LICENSE file for details.


