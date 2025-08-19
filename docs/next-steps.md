# Next Steps - cainban Implementation

This document tracks implementation steps optimized for AI development workflow.

## Phase 1: Foundation (Current)
**Goal**: Create minimal viable product for AI integration

### 1.1 Project Setup ✅
- [x] Initialize Git repository
- [x] Create project structure
- [x] Write README.md
- [x] Create next-steps.md

### 1.2 Core Infrastructure
- [ ] Initialize Go module (`go mod init github.com/emhamin/cainban`)
- [ ] Create basic CLI structure in `cmd/cainban/main.go`
- [ ] Implement storage system in `src/systems/storage/`
  - SQLite database initialization
  - Schema creation for boards and tasks
  - Basic CRUD operations
- [ ] Create basic task system in `src/systems/task/`
  - Task struct definition
  - Task states (todo, doing, done)
  - Task CRUD operations

### 1.3 MCP Integration (Priority)
- [ ] Implement MCP server in `src/systems/mcp/`
  - MCP protocol implementation
  - Tool definitions for kanban operations
  - Server startup and management
- [ ] Define MCP tools:
  - `create_task`: Create new task
  - `list_tasks`: List tasks by status
  - `update_task`: Update task details
  - `move_task`: Change task status
  - `get_board`: Get full board state

### 1.4 Basic CLI Commands
- [ ] `cainban init` - Initialize new board
- [ ] `cainban add <title>` - Add new task
- [ ] `cainban list` - List all tasks
- [ ] `cainban move <id> <status>` - Move task between columns
- [ ] `cainban mcp` - Start MCP server

## Phase 2: AI Integration
**Goal**: Full Amazon Q integration and enhanced functionality

### 2.1 Amazon Q Integration
- [ ] Test MCP server with Q Chat CLI
- [ ] Implement natural language task parsing
- [ ] Add context awareness for project tasks
- [ ] Create Q-specific command shortcuts

### 2.2 Enhanced Task Management
- [ ] Task descriptions with markdown support
- [ ] Task priorities and labels
- [ ] Task dependencies
- [ ] Due dates and time tracking

### 2.3 Board System
- [ ] Implement board system in `src/systems/board/`
- [ ] Multiple board support
- [ ] Board templates
- [ ] Board sharing and export

## Phase 3: User Experience
**Goal**: Rich terminal interface and advanced features

### 3.1 Terminal UI
- [ ] Implement Bubble Tea TUI
- [ ] Interactive task management
- [ ] Markdown rendering with Glow
- [ ] Keyboard shortcuts and navigation

### 3.2 Advanced Features
- [ ] Task search and filtering
- [ ] Task history and audit log
- [ ] Backup and restore functionality
- [ ] Configuration management

## Implementation Notes for AI

### Database Schema (SQLite)
```sql
-- boards table
CREATE TABLE boards (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    description TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- tasks table
CREATE TABLE tasks (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    board_id INTEGER NOT NULL,
    title TEXT NOT NULL,
    description TEXT,
    status TEXT NOT NULL DEFAULT 'todo', -- todo, doing, done
    priority INTEGER DEFAULT 0,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (board_id) REFERENCES boards(id)
);
```

### MCP Tool Specifications
Each MCP tool should follow this pattern:
- Clear input/output schemas
- Error handling with descriptive messages
- Atomic operations (no partial state changes)
- Consistent response format

### System Architecture
- Each system in `src/systems/` is self-contained
- Systems communicate through well-defined interfaces
- No circular dependencies between systems
- Storage system is the single source of truth

### Testing Strategy
- Unit tests for each system
- Integration tests for MCP server
- End-to-end tests for CLI commands
- Race condition testing for concurrent access

### Error Handling
- Use Go's standard error handling
- Wrap errors with context using `fmt.Errorf`
- Log errors appropriately for debugging
- Graceful degradation where possible

## Current Priority
**Start with Phase 1.2 and 1.3** - Focus on getting the MCP server working with basic task operations. This will enable immediate AI integration and faster iteration cycles.

The first working version should support:
1. Creating tasks via MCP
2. Listing tasks via MCP  
3. Moving tasks between states via MCP
4. Basic CLI commands for verification

This minimal set will provide immediate value for AI agents and establish the foundation for all future features.
