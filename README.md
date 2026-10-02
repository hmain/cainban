# cainban

cainban (c-AI-nban) is a command-line kanban board designed to expose all commands as command-line options. You can use it to manage a todo-list, your daily tasks, or your personal development backlog. 
It also enables AI code generators through its MCP server to decompose tasks into smaller components, allowing the AI agent to concentrate on delivering the entire project step by step.

## Overview

- **Command-line first**: All operations can be performed via CLI without launching a GUI application
- **Interactive TUI**: Full-featured Terminal User Interface with viewport-based scrolling for large task lists
- **MCP Integration**: Built-in Model Context Protocol (MCP) server for seamless AI integration
- **Two backends, one codebase**: local **SQLite** (CLI/TUI) and a serverless **DynamoDB** path (the AWS Lambda MCP deployment) — selected at runtime via `CAINBAN_BACKEND`
- **Serverless multi-user**: a stateless MCP server on AWS Lambda (arm64) behind an API Gateway HTTP API + **Cognito JWT authorizer**, with repo-scoped tenancy and a GitHub-App "connect" flow — see [Serverless deployment](#serverless-deployment) and [`infra/README.md`](infra/README.md)

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

cainban includes a built-in Model Context Protocol (MCP) server using the official [Go MCP SDK](https://github.com/modelcontextprotocol/go-sdk), ensuring full compatibility with AI tools like Kiro, Claude Desktop, and other MCP clients.

> **Using cainban as an AI agent's task backend?** See [`docs/agent-via-mcp.md`](docs/agent-via-mcp.md) for the agent-over-MCP guide: the auth/repo-scoping model, the exact MCP tools, and a worked agent loop against the serverless endpoint.

> **Serverless / multi-user.** Beyond the local CLI, cainban also runs as a
> multi-user serverless deployment: the stateless MCP server on AWS Lambda
> (arm64) behind an **API Gateway HTTP API + Cognito JWT authorizer**, with a
> DynamoDB backend and repo-scoped tenancy (`REPO#<owner>/<repo>#`). Clients send
> `Authorization: Bearer <Cognito JWT>` (no request signing). A GitHub App
> "connect" flow verifies a user's repo access before granting it. See
> [`infra/README.md`](infra/README.md) for the deployed stack and IAM surface,
> and [`docs/github-app-setup.md`](docs/github-app-setup.md) to connect a repo.

1. **For Kiro** (recommended):
   
Add to `~/.kiro/settings/mcp.json`:
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

Add to your Claude Desktop configuration (update the `command` path to point to your cainban binary):
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

> **Project-specific access.** To share cainban with a team, commit an `mcp.json`
> with the same `mcpServers` block to your project root — teammates get cainban
> automatically when they clone the repo.

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

- Start working on the next tasks in the **Cainban** to-do list or backlog. If a task has subtasks, begin with those (get_task_links). Follow good Git practices, like using branches and other Git tools. Use the "default" **Cainban** board for your tasks. If you find any issues, create new tasks for them. Break down tasks into smaller subtasks so you can focus on one small problem at a time.


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

- **Language**: Go (single language across CLI, Lambdas, and CDK infra)
- **Storage**: pluggable via `CAINBAN_BACKEND` — **SQLite** (local CLI/TUI, requires CGO) or **DynamoDB** (serverless, pure Go, used by the Lambda)
- **Systems Architecture**: Modular systems in `src/systems/` (`auth`, `board`, `task`, `mcp`, `store`, `storage`, `dynamo`, `grants`, `github`, `connect`, `crypter`, `secrets`)
- **TUI Framework**: [Bubble Tea](https://github.com/charmbracelet/bubbletea) with viewport-based scrolling
- **Serverless**: three arm64 `provided.al2023` Lambdas (`cainban-mcp`, `cainban-pretoken`, `cainban-connect`) provisioned by an **AWS CDK (Go)** app in [`infra/`](infra/); a Cognito user pool + JWT authorizer; DynamoDB data + grants tables; a React/Vite SPA (`web/`) hosted on Amplify
- TODO: **Markdown Rendering**: [Glow](https://github.com/charmbracelet/glow)

## AI Integration

cainban is designed to work seamlessly with AI agents:

### MCP Server
- Built with official [Go MCP SDK](https://github.com/modelcontextprotocol/go-sdk) for maximum compatibility
- Exposes cainban operations as MCP tools
- Real-time board state synchronization
- JSON-RPC 2.0 compliant
- Compatible with Kiro, Claude Desktop, and other MCP clients
- Tools available: `create_task`, `list_tasks`, `update_task_status`, `get_task`, `update_task_priority`, `update_task`, `link_tasks`, `unlink_tasks`, `get_task_links`, `delete_task`, `restore_task`, `list_boards`, `change_board`, `list_activity` (see the full table below)

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
| `list_activity` | Recent task activity (who changed what), newest first | "Show recent activity on this board" |

## Development

### Prerequisites
- Go 1.26+ (matches `go.mod` and CI)
- SQLite3 (for the local backend; CGO required)
- For serverless work: AWS CDK v2 CLI, AWS credentials, and `curl` (see [`infra/README.md`](infra/README.md))

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

### Development → Production Workflow

cainban ships through a gated pipeline: nothing reaches the live serverless edge
without passing local gates, CI, a reviewed infra diff, and a post-deploy smoke
check. The flow below is the single source of truth — the Makefile targets and
CI jobs it names are what actually run.

```
 feature branch ──▶ local gates ──▶ PR + CI ──▶ review ──▶ merge to main
                       │                │                      │
             make quality (lint+test)   test.yml              │
             make bundles (if infra)    (vet · test -race ·   │
                                         golangci-lint ·      │
                                         build)               ▼
                                                       cdk diff (review FULLY)
                                                              │
                                                              ▼
                                                   make deploy  ─────────────┐
                                                   (bundles → cdk deploy →    │
                                                    verify-deploy smoke check)│
                                                              │              │
                                              Amplify auto-build (web/)       │
                                                              ▼              ▼
                                                        PRODUCTION (live edge verified)
```

#### 1. Branch

Feature branches off `main`, descriptive names (`feature/activity-feed`,
`fix/cors-preflight`, `docs/...`). Never commit to `main` directly; never force-push a protected branch.

#### 2. Local gates (before every push)

```bash
make quality          # golangci-lint + go test -race -cover ./...  (lint + test in one)
# or individually:
make lint             # golangci-lint
make test             # go test -race -cover ./...
go vet ./...          # also run in CI

# If you touched infra or any Lambda handler, confirm the bundles still build:
make bundles          # builds .build/lambda + .build/pretoken + .build/connect (arm64)

# If you touched the SPA:
cd web && npm ci && npm run build
```

Pre-commit hooks enforce the basics automatically — install once with `make setup-hooks`.

#### 3. Pull request + CI

Open a PR against `main`. The **`test.yml`** workflow runs three required jobs on
every push/PR: **test** (`go mod verify`, `go vet`, `go test -race` + coverage),
**lint** (`golangci-lint`), and **build** (`go build ./cmd/cainban`). All must be
green. CI runs Go **1.26** across every job.

> **Dependabot PRs** do not get a human-authored review by default, and a CLEAN
> status only means "no checks failed" — not "verified good". Check out the
> branch and run the full gate locally before merging, especially major bumps
> (a bad transitive bump can break `npm install`/`go build` without failing a
> status check).

#### 4. Review the infra diff (infra/Lambda changes only)

Code merging to `main` does **not** auto-deploy the serverless stack — deploys are
a deliberate, credentialed step. Before deploying, **always** review the CloudFormation diff and read it in full:

```bash
make bundles
cd infra && npx -y aws-cdk@2.1143.0 diff CainbanPhase2Stack
```

Look for: unexpected resource replacements/deletions, IAM widening, and
context-param drift (a flagless deploy can silently revert live values set
out-of-band — the real Entra/URL values live in the tracked `infra/cdk.json`
context so a plain deploy stays idempotent).

#### 5. Deploy (the one canonical command)

```bash
export AWS_PROFILE=aws-test-hamin AWS_REGION=eu-north-1
make deploy
```

`make deploy` is the only sanctioned path. It chains three steps so none can be
skipped:

1. `make bundles` — rebuild all three Lambda bundles from the current source
   (never trust a stale `.build/` after a branch switch).
2. `cdk deploy CainbanPhase2Stack` — apply the stack.
3. `make verify-deploy` — **auto-runs** as the final gate.

#### 6. Post-deploy verification (automatic, and re-runnable)

`make verify-deploy` smoke-checks the **live API edge** using endpoints read from
the deployed CloudFormation stack outputs (nothing hardcoded):

- the **CORS preflight** must succeed (unauthenticated `OPTIONS` → 2xx with
  `Access-Control-Allow-Origin`), on both `/` and a sub-path;
- the **data path must stay auth-gated** (unauthenticated request → 401), so a
  preflight fix can never silently open the data plane.

It exits non-zero on any failure (CI/pipeline-friendly). Run it standalone any
time against a deployed stack: `STACK=CainbanPhase2Stack make verify-deploy`.

This check exists because a CORS/authorizer regression is invisible to unit tests
and `cdk synth` — it only appears when a real browser hits the real gateway.
**When a frontend starts calling a new backend origin, the cross-origin preflight
is a first-class acceptance test, not an afterthought.**

#### 7. The web SPA

The `web/` SPA deploys separately via **Amplify Hosting**, which auto-builds on
push to `main` (outside GitHub Actions). After a merge that changes `web/`, confirm
the Amplify build succeeded on the merge commit and the live bundle carries the
change before calling it shipped.

#### Rollback

Infra/Lambda: redeploy the previous known-good commit with `make deploy` (the
stack is CloudFormation-managed; `cdk deploy` of an earlier source rolls forward
to that state). The DynamoDB tables are `RETAIN` + PITR, so data survives a stack
issue. The SPA rolls back by reverting the offending commit on `main` (Amplify
rebuilds).

### Git conventions

1. Feature branches from `main`; descriptive names.
2. Squash-merge to keep `main` history clean; delete the branch after merge.
3. No compatibility bridges — breaking changes are acceptable during development.
4. Verify a merge actually landed the tip commits on `main` before trusting a
   "merged" status.

### Project Structure

```
cainban/
├── cmd/
│   ├── cainban/           # Main CLI + TUI application
│   ├── cainban-lambda/    # Serverless MCP handler (arm64 Lambda bootstrap)
│   ├── cainban-pretoken/  # Cognito pre-token-generation trigger
│   └── cainban-connect/   # GitHub-connect OAuth API Lambda
├── src/systems/           # Modular systems
│   ├── auth/             # Signature-first JWT validation + repo-scoped tenancy
│   ├── board/ task/      # Board + task domain logic
│   ├── mcp/              # MCP server + tool dispatch
│   ├── store/ storage/   # Storage abstraction
│   ├── dynamo/           # DynamoDB backend (serverless)
│   ├── grants/           # Per-user repo grants (grants table)
│   ├── github/ connect/  # GitHub App + connect flow
│   └── crypter/ secrets/ # KMS + Secrets Manager helpers
├── src/tui/              # Bubble Tea TUI
├── infra/               # AWS CDK (Go) app + verify-deploy.sh
├── web/                 # React/Vite SPA (Amplify-hosted)
├── docs/                # Design docs, RFCs, setup guides
└── tests/               # Integration tests
```

## Serverless deployment

Beyond the local CLI, cainban runs as a multi-user serverless deployment: a
stateless MCP server on **AWS Lambda (arm64)** behind an **API Gateway HTTP API +
Cognito JWT authorizer**, a **DynamoDB** backend with repo-scoped tenancy
(`REPO#<owner>/<repo>#`), and a **GitHub-App connect flow** that verifies a user's
repo access server-side before granting it. Clients send
`Authorization: Bearer <Cognito JWT>` (no request signing).

Everything deploy-related — the full stack, IAM surface, env vars, the auth
design, the pre-token trigger, and the grants model — is documented in
[`infra/README.md`](infra/README.md). Deploy with the gated flow in
[Development → Production Workflow](#development--production-workflow)
(`make deploy`, which auto-runs `make verify-deploy`).

Related guides:
- [`docs/agent-via-mcp.md`](docs/agent-via-mcp.md) — using cainban as an AI agent's task backend over MCP
- [`docs/github-app-setup.md`](docs/github-app-setup.md) — register the GitHub App and connect a repo
- [`docs/mcp-oauth-setup.md`](docs/mcp-oauth-setup.md) — MCP OAuth client setup

## Troubleshooting

### MCP Server Issues
1. **Server not loading**: Increase the MCP server launch timeout in your client's MCP settings (in Kiro, set a higher `timeout` on the server entry in `~/.kiro/settings/mcp.json`)
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

## Status

**Local CLI/TUI**: stable — board-scoped task IDs, fuzzy search, task links, soft/hard delete, interactive TUI.

**Serverless (multi-user)**: live — stateless MCP on Lambda behind a Cognito JWT authorizer, DynamoDB with repo-scoped tenancy, atomic per-board id counter, optimistic-concurrency `version` guard, an append-only activity feed (`list_activity`), and the GitHub-App connect flow + React/Vite connect SPA. A gated `make deploy` → `make verify-deploy` pipeline guards the live edge.

See [`docs/`](docs/) for the MCP, agent, and GitHub-App setup guides.


## Contributing

1. Fork the repository
2. Create a feature branch
3. Make your changes following the code quality guidelines
4. Add tests for new functionality
5. Submit a pull request

## License

MIT License - see LICENSE file for details.


