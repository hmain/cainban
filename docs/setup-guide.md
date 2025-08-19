# cainban Setup Guide

This guide explains how to set up cainban MCP integration with Amazon Q CLI for different use cases.

## Quick Answer to Your Questions

### Is cainban available every time I start chatting?
**Yes!** With the current global configuration, cainban is available in Amazon Q from any directory.

### How can I add it to other projects?
You have several options depending on your needs:

## Setup Options

### Option 1: Global Access (Current Setup) ✅ Recommended

**Configuration**: `~/.aws/amazonq/mcp.json`
```json
{
  "mcpServers": {
    "cainban": {
      "command": "/Users/emhamin/cainban/cainban",
      "args": ["mcp"]
    }
  }
}
```

**Benefits**:
- ✅ Available from any directory
- ✅ One central kanban board for all projects
- ✅ No additional setup needed
- ✅ Fast startup with binary

**Use this when**: You want one kanban board to manage tasks across all your projects.

### Option 2: Project-Specific Configuration

**Configuration**: Create `.amazonq/mcp.json` in each project directory
```json
{
  "mcpServers": {
    "cainban": {
      "command": "/Users/emhamin/cainban/cainban",
      "args": ["mcp"]
    }
  }
}
```

**Setup for a new project**:
```bash
cd /path/to/your/project
mkdir -p .amazonq
cp /Users/emhamin/cainban/examples/portable-mcp-config.json .amazonq/mcp.json
```

**Benefits**:
- ✅ Only available in specific projects
- ✅ Can be shared with team members via git
- ✅ Project-specific tool availability

**Use this when**: You want cainban only available in certain projects or want to share the configuration with your team.

### Option 3: Install cainban Globally (Future Enhancement)

For easier distribution, you could install cainban to your PATH:

```bash
# Build and install to a directory in your PATH
cd /Users/emhamin/cainban
go build -o ~/bin/cainban cmd/cainban/main.go

# Or use go install (when published)
go install github.com/hmain/cainban@latest
```

Then update MCP configuration to use the global binary:
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

## Current Status

### ✅ What's Working Now
- **Global access**: cainban works from any directory
- **Fast startup**: Binary loads in ~0.1s vs ~0.4s for Go compilation
- **All tools available**: create_task, list_tasks, update_task_status, get_task, update_task
- **Natural language**: "List my tasks", "Create a task", "Move task to doing"

### 🔧 Configuration Details

**Current global config**: `/Users/emhamin/.aws/amazonq/mcp.json`
```json
{
  "mcpServers": {
    "cainban": {
      "command": "/Users/emhamin/cainban/cainban",
      "args": ["mcp"]
    }
  }
}
```

**Database location**: `~/.cainban/cainban.db` (global, persistent)

## Testing Your Setup

### From any directory:
```bash
cd /tmp
q chat "List my cainban tasks"
```

### Expected result:
- MCP server loads successfully
- Q recognizes cainban tools
- Tasks are listed with proper formatting

## Troubleshooting

### MCP Server Not Loading
1. **Check timeout settings**:
   ```bash
   q settings mcp.noInteractiveTimeout 5000
   ```

2. **Verify binary exists and is executable**:
   ```bash
   ls -la /Users/emhamin/cainban/cainban
   /Users/emhamin/cainban/cainban version
   ```

3. **Test MCP server manually**:
   ```bash
   echo '{"jsonrpc":"2.0","id":1,"method":"initialize"}' | /Users/emhamin/cainban/cainban mcp
   ```

### Tools Not Available
1. **Check if server loaded**:
   - Look for "✓ cainban loaded" message when starting Q chat
   - Use `/tools` command in Q chat to see available tools

2. **Verify configuration**:
   ```bash
   cat ~/.aws/amazonq/mcp.json
   ```

### Database Issues
1. **Initialize if needed**:
   ```bash
   /Users/emhamin/cainban/cainban init
   ```

2. **Check database location**:
   ```bash
   ls -la ~/.cainban/cainban.db
   ```

## Sharing with Team Members

### Option A: Share Binary and Config
1. Build binary for their platform
2. Share the MCP configuration file
3. Update paths in configuration

### Option B: Share Source and Build Instructions
1. Share the cainban repository
2. Provide build instructions
3. Share MCP configuration template

### Option C: Project-Specific Setup
1. Add `.amazonq/mcp.json` to your project repository
2. Team members get cainban automatically when they clone
3. Requires each team member to build cainban locally

## Advanced Configuration

### Multiple Boards (Future Feature)
When multi-board support is added, you could configure different boards for different projects:

```json
{
  "mcpServers": {
    "cainban-work": {
      "command": "/Users/emhamin/cainban/cainban",
      "args": ["mcp", "--board", "work"]
    },
    "cainban-personal": {
      "command": "/Users/emhamin/cainban/cainban",
      "args": ["mcp", "--board", "personal"]
    }
  }
}
```

### Custom Database Location
```json
{
  "mcpServers": {
    "cainban": {
      "command": "/Users/emhamin/cainban/cainban",
      "args": ["mcp", "--db", "/path/to/project/tasks.db"]
    }
  }
}
```

## Recommendations

### For Individual Use
- **Use Option 1 (Global)**: One kanban board for all your work
- **Benefits**: Simple, always available, no setup per project

### For Team Projects
- **Use Option 2 (Project-specific)**: Each project has its own configuration
- **Benefits**: Team can share the setup, project isolation

### For Distribution
- **Use Option 3 (Global install)**: Install cainban as a system tool
- **Benefits**: Easy to share, no path dependencies

The current global setup is perfect for getting started and works great for individual productivity!
