# cainban

cainban (c-AI-nban) is a command-line kanban board designed to expose all commands as command-line options. You can use it to manage a todo-list, your daily tasks, or your personal development backlog. 
It also enables AI code generators through its MCP server to decompose tasks into smaller components, allowing the AI agent to concentrate on delivering the entire project step by step.

## Overview

- **Command-line first**: All operations can be performed via CLI without launching a GUI application
- **Interactive TUI**: Full-featured Terminal User Interface with viewport-based scrolling for large task lists
- **MCP Integration**: Built-in Model Context Protocol (MCP) server for seamless AI integration
- **SQLite Backend**: Lightweight, file-based database storage

## Quick Start

## Installation

### Option 1: Download Pre-built Binary (Recommended)

Download the latest release for your platform from [GitHub Releases](https://github.com/hmain/cainban/releases):

- **Linux**: `cainban-linux-amd64` or `cainban-linux-arm64`
- **macOS**: `cainban-darwin-amd64` or `cainban-darwin-arm64` 
- **Windows**: `cainban-windows-amd64.exe`

```bash
# Example for Linux
wget https://github.com/hmain/cainban/releases/latest/download/cainban-linux-amd64
chmod +x cainban-linux-amd64
sudo mv cainban-linux-amd64 /usr/local/bin/cainban
```

### Option 2: Build from Source

```bash
# Clone the repository
git clone https://github.com/hmain/cainban.git
cd cainban

# Quick setup with install script (recommended)
./install.sh

# Or manual build:
go mod tidy
go build -o cainban cmd/cainban/main.go

# Initialize your kanban board
./cainban init
```

### 2. Basic Usage

```bash
# Add tasks
./cainban add "Implement user authentication" "Add login and registration functionality"

# List all tasks (shows board-scoped IDs: #1, #2, #3, etc.)
./cainban list

# List tasks by status
./cainban list todo
./cainban list doing
./cainban list done

# Move tasks between columns (by board-scoped ID or fuzzy title match)
./cainban move 1 doing
./cainban move "user auth" doing

# Get task details (by board-scoped ID or fuzzy title match)
./cainban get 1
./cainban get "user auth"

# Update task (by board-scoped ID or fuzzy title match)
./cainban update 1 "Updated task title" "Updated description"
./cainban update "user auth" "Enhanced authentication system"

# Set task priority
./cainban priority 1 high
./cainban priority "user auth" critical

# Link tasks together (using board-scoped IDs)
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

# Launch interactive TUI
./cainban tui
```

### 3. MCP Server for AI Integration

cainban includes a built-in Model Context Protocol (MCP) server using the official [Go MCP SDK](https://github.com/modelcontextprotocol/go-sdk), ensuring full compatibility with AI tools like Amazon Q CLI, Claude Desktop, and other MCP clients.

1. **For Amazon Q CLI** (recommended):
   
Add to `~/.aws/amazonq/mcp.json`:
```json
{
  "mcpServers": {
    "cainban": {
      "command": "cainban",
      "args": ["mcp"]
    }
  }
}
```

2. **For Claude Desktop**:

Add to your Claude Desktop configuration:
```json
{
  "mcpServers": {
    "cainban": {
      "command": "/path/to/your/cainban/cainban",
      "args": ["mcp"]
    }
  }
}
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

### 🖥️ **Interactive Terminal UI**
Experience cainban through a powerful, responsive TUI built with Bubble Tea:

```bash
# Launch the interactive interface
./cainban tui
```

**TUI Features:**
- **Viewport-Based Scrolling**: Smooth navigation through large task lists (635+ tasks tested)
- **Enhanced Navigation**: 
  - `j`/`k` or `↑`/`↓` for line-by-line movement with auto-scroll
  - `Page Up`/`Page Down` for page-based scrolling
  - `Home`/`End` for instant jumping to top/bottom
- **Visual Indicators**: Real-time scroll position display `[X/Y]` for large datasets
- **Responsive Design**: Dynamic column widths that adapt to your terminal size
- **Professional UX**: Starts at the top, handles terminal resizing, follows Bubble Tea best practices
- **Intuitive Controls**: Press `q` to quit, `?` for help

**Navigation Example:**
```
┌─ cainban v0.2.1-dev.11 ─ Go MCP SDK Integration ──────────────┐
│ TODO [1/3]:                                                    │
│   #8 [critical] Implement task dependencies                    │
│   #6 [high] Enhanced TUI with viewport scrolling              │
│ DOING [2/3]:                                                   │
│   #10 [high] Prepare for public release                       │
│ DONE [3/3]:                                                    │
│   #9 [medium] Enhanced AI features                            │
└────────────────────────────── Press q to quit ───────────────┘
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
- **TUI Framework**: [Bubble Tea](https://github.com/charmbracelet/bubbletea) with viewport-based scrolling
- **Terminal UI**: Full-featured responsive interface with professional UX patterns
- TODO: **Markdown Rendering**: [Glow](https://github.com/charmbracelet/glow)

## AI Integration

cainban is designed to work seamlessly with AI agents:

### MCP Server
- Built with official [Go MCP SDK](https://github.com/modelcontextprotocol/go-sdk) for maximum compatibility
- Exposes cainban operations as MCP tools
- Real-time board state synchronization
- JSON-RPC 2.0 compliant
- Compatible with Amazon Q CLI, Claude Desktop, and other MCP clients
- Tools available: create_task, list_tasks, update_task_status, get_task, update_task_priority, update_task, list_boards, change_board

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

cainban includes comprehensive tests for all systems including TUI interactions:

```bash
# Run all tests
go test ./...

# Run with coverage
go test -cover ./...

# Run with race detection
go test -race ./...

# Run specific system tests
go test ./src/systems/task/...
go test ./src/tui/...

# Verbose output
go test -v ./src/tui/
```

**Test Coverage:**
- **TUI Tests**: Key navigation, window resizing, quit commands
- **Task System**: CRUD operations, status updates, priority management
- **Storage**: Database operations, migrations
- **MCP Server**: Tool registration and execution

All tests use in-memory databases for fast, isolated testing.

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

**Current Version**: v0.2.1 - Board-Scoped Task IDs  
**Major Features**: Board-scoped task IDs, automatic migration, enhanced MCP efficiency, GitHub Actions releases  
**Previous**: Official Go MCP SDK, enhanced AI compatibility, interactive TUI with viewport scrolling  

## Recent Updates:

### v0.2.1 - Board-Scoped Task IDs
- **Board-Scoped Task IDs**: Each board now has its own task ID sequence (1, 2, 3, etc.)
- **Automatic Migration**: Existing boards seamlessly upgrade to board-scoped IDs
- **MCP Efficiency**: Reduced context overflow in AI conversations with smaller task IDs
- **GitHub Actions**: Automatic releases with multi-platform binaries
- **Backward Compatibility**: Internal global IDs preserved for database relationships

### Key Benefits:
- **Reduced Token Usage**: Task IDs are now 1-3 digits instead of large numbers
- **Better UX**: Users see intuitive task numbers (#1, #2, #3) per board  
- **AI-Friendly**: MCP operations use manageable task references
- **Seamless Upgrade**: No manual migration needed - works automatically


## Contributing

1. Fork the repository
2. Create a feature branch
3. Make your changes following the code quality guidelines
4. Add tests for new functionality
5. Submit a pull request

## License

MIT License - see LICENSE file for details.


