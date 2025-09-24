# cainban

A command-line kanban board with AI integration through MCP (Model Context Protocol). Manage tasks via CLI, TUI, or natural language with AI assistants.

## Getting Started

### Installation

**Option 1: Quick Install (Recommended)**
```bash
curl -sSL https://raw.githubusercontent.com/hmain/cainban/main/install.sh | bash
```

**Option 2: Manual Download**
Download from [GitHub Releases](https://github.com/hmain/cainban/releases) for your platform (Linux, macOS, Windows).

**Option 3: Build from Source**
```bash
git clone https://github.com/hmain/cainban.git
cd cainban
go build -o cainban cmd/cainban/main.go
./cainban init
```

### CLI Usage

```bash
# Basic task management
./cainban add "Fix login bug" "Update authentication system"
./cainban list
./cainban move 1 doing
./cainban priority 1 high

# Interactive TUI
./cainban tui
```

### MCP Integration

Add to `~/.aws/amazonq/mcp.json` for Amazon Q CLI:

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

Then use natural language: *"Create a task to implement user auth"*, *"Move task 3 to doing"*, *"List my high priority tasks"*

## Architecture

- **Language**: Go with SQLite backend
- **Systems**: Modular design in `src/systems/` (board, task, mcp, storage)
- **TUI**: Bubble Tea with viewport scrolling
- **MCP**: Official Go MCP SDK for AI integration

```
cainban/
├── cmd/cainban/           # Main CLI application
├── src/systems/           # Modular system components
│   ├── board/            # Board management
│   ├── task/             # Task management  
│   ├── mcp/              # MCP server
│   └── storage/          # Database layer
└── internal/             # Internal packages
```

## Development

### Setup
```bash
git clone https://github.com/hmain/cainban.git
cd cainban
go mod tidy
go run cmd/cainban/main.go init
```

### Testing
```bash
go test ./...
go test -race ./...
go test -cover ./...
```

### Workflow
- Feature branches from `main`
- Squash commits before merge
- Use `go vet` and `golangci-lint`
- Breaking changes acceptable during development

## License

MIT License


