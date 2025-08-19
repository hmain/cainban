# cainban

An AI-centric kanban board designed for command-line first interaction with first-class support for AI agents like Amazon Q.

## Overview

cainban (c-AI-nban) is a kanban board system built specifically for AI workflows. It provides:

- **Command-line first**: All operations can be performed via CLI without launching a GUI application
- **AI-native**: First-class support for Amazon Q and other AI agents
- **MCP Integration**: Built-in Model Context Protocol (MCP) server for seamless AI integration
- **Terminal UI**: Rich terminal interface using Bubble Tea
- **Markdown Support**: Native markdown rendering with Glow
- **SQLite Backend**: Lightweight, file-based database storage

## Architecture

- **Language**: Go
- **Database**: SQLite
- **TUI Framework**: [Bubble Tea](https://github.com/charmbracelet/bubbletea)
- **Markdown Rendering**: [Glow](https://github.com/charmbracelet/glow)
- **Systems Architecture**: Modular systems in `src/systems/` for extensibility

## Quick Start

```bash
# Install cainban
go install github.com/hmain/cainban@latest

# Initialize a new board
cainban init

# Add a task
cainban add "Implement user authentication"

# View board
cainban board

# Start TUI
cainban tui
```

## AI Integration

cainban is designed to work seamlessly with AI agents:

### Amazon Q Integration
- Native support for Q Chat CLI
- Task creation and management through natural language
- Automatic project context awareness

### MCP Server
- Built-in MCP server for AI tool integration
- Exposes kanban operations as MCP tools
- Real-time board state synchronization

## Development

### Prerequisites
- Go 1.21+
- SQLite3

### Setup
```bash
git clone https://github.com/hmain/cainban.git
cd cainban
go mod tidy
go run cmd/cainban/main.go
```

### Testing
```bash
# Run all tests
go test ./...

# Run with coverage
go test -cover ./...

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

## Contributing

1. Fork the repository
2. Create a feature branch
3. Make your changes following the code quality guidelines
4. Add tests for new functionality
5. Submit a pull request

## License

MIT License - see LICENSE file for details.
