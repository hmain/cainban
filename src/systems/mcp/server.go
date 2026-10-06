package mcp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/hmain/cainban/src/systems/auth"
	"github.com/hmain/cainban/src/systems/board"
	"github.com/hmain/cainban/src/systems/store"
	"github.com/hmain/cainban/src/systems/task"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// serverName / serverVersion identify the cainban MCP server in the
// initialize handshake. Kept as package constants so stdio and HTTP transports
// advertise the same implementation info.
const (
	serverName    = "cainban"
	serverVersion = "0.2.1"
)

// Server wraps the official MCP SDK server with cainban functionality.
//
// It is deliberately STATELESS with respect to request routing: it holds no
// database handle and no "current board" selection. Every tool call resolves
// and opens the correct board database on its own, so the same Server value can
// safely serve concurrent requests for different boards (required by the
// stateless Streamable HTTP transport, where a fresh *mcp.Server is built per
// request via getServer).
type Server struct {
	boardSystem *board.System
	// schemaCache is shared across the per-request *mcp.Server instances built
	// by getServer, so JSON schema reflection/resolution happens once rather
	// than on every request.
	schemaCache *mcp.SchemaCache
	// mcpServer is the underlying SDK server used by the stdio transport
	// (Start). The HTTP transport builds its own per-request servers.
	mcpServer *mcp.Server
}

// New creates a new stateless cainban MCP server.
//
// The taskSystem argument is retained for backwards compatibility with existing
// callers/tests but is IGNORED: the server no longer keeps a process-wide task
// system, because board resolution is now per request. Pass nil.
func New(_ *task.System) *Server {
	return NewStateless()
}

// NewStateless creates a new stateless cainban MCP server. This is the
// preferred constructor.
func NewStateless() *Server {
	s := &Server{
		boardSystem: board.New(),
		schemaCache: mcp.NewSchemaCache(),
	}
	s.mcpServer = s.newMCPServer()
	return s
}

// newMCPServer builds a fresh *mcp.Server with all cainban tools registered and
// the shared schema cache installed. Used both for the long-lived stdio server
// and, via getServer, for each stateless HTTP request.
func (s *Server) newMCPServer() *mcp.Server {
	mcpServer := mcp.NewServer(&mcp.Implementation{
		Name:    serverName,
		Version: serverVersion,
	}, &mcp.ServerOptions{
		SchemaCache: s.schemaCache,
	})
	s.registerTools(mcpServer)
	return mcpServer
}

// Start starts the MCP server using the stdio transport (the default mode).
func (s *Server) Start() error {
	return s.mcpServer.Run(context.Background(), &mcp.StdioTransport{})
}

// getServer returns a per-request *mcp.Server for the stateless HTTP handler.
// Tools and the schema cache are shared; no request state leaks between calls
// because the handlers themselves open their board DB per request.
func (s *Server) getServer(_ *http.Request) *mcp.Server {
	return s.newMCPServer()
}

// Handler returns the stateless Streamable-HTTP MCP handler as a plain
// http.Handler. Both the local HTTP server (ServeHTTP) and the Lambda
// entrypoint (cmd/cainban-lambda) mount this same handler, so the transport
// behavior is identical in-process and on Lambda. A fresh *mcp.Server is built
// per request via getServer, keeping the handler safe for concurrent requests.
//
// This handler is UNAUTHENTICATED (single-tenant, empty partition prefix) — it
// is what the local `cainban mcp --http` loopback server uses for dev. The
// authenticated, repo-scoped multi-tenant path is HandlerWithAuth.
func (s *Server) Handler() http.Handler {
	sdk := mcp.NewStreamableHTTPHandler(s.getServer, &mcp.StreamableHTTPOptions{
		Stateless: true,
	})
	// Back-fill the spec-2026-07-28 Mcp-Method/Mcp-Name mirror headers when a
	// client omits them (e.g. Claude Code's subscriptions/listen re-open), so
	// the SDK's strict header check doesn't reject the request -32020 and send
	// the client into a reconnect loop. Safe: the header the SDK demands is by
	// definition the body's own method, and a client-set header is never
	// overwritten. See header_backfill.go.
	return BackfillMirrorHeaders(sdk)
}

// HandlerWithAuth wraps Handler with signature-first JWT auth + repo-scoped
// tenant resolution (see AuthMiddleware). This is the Phase 3 entrypoint for the
// public Lambda: every request must present a valid bearer token authorizing
// the target repo, and the resolved tenant's partition prefix isolates its data
// in DynamoDB. There is no unauthenticated path through this handler.
func (s *Server) HandlerWithAuth(resolver *auth.Resolver) http.Handler {
	return AuthMiddleware(resolver, s.Handler())
}

// HandlerWithAuthChallenge is HandlerWithAuth that additionally advertises the
// RFC 9728 protected-resource metadata URL in the 401 `WWW-Authenticate`
// challenge (the MCP-OAuth discovery pointer, spec 2026-07-28). Pass the
// absolute URL of the metadata document
// (<McpApiUrl>/.well-known/oauth-protected-resource); an empty string falls
// back to the legacy realm challenge (== HandlerWithAuth).
//
// # RFC 8707 resource indicator / audience note
//
// Under MCP OAuth a client requests a token scoped to THIS resource server by
// sending `resource=<canonical MCP URL>` (RFC 8707) to the authorization
// server; the URL it uses is exactly the `resource` value published in the
// protected-resource metadata document (ResourceMetadataConfig.Resource). Some
// authorization servers reflect that resource indicator into the token's `aud`.
//
// cainban's validator (src/systems/auth) accepts a token whose `aud` contains
// any CONFIGURED app-client id (CAINBAN_AUTH_AUDIENCE). This step (a) does NOT
// weaken that check: it deliberately does NOT add the canonical MCP URL as an
// accepted audience, because with Cognito the tokens in play (the SPA PKCE
// client and the machine client) carry an app-client-id `aud`, and widening the
// accepted-audience set to a URL with no corresponding validation gain would be
// a change to the load-bearing audience binding for no benefit today. If a
// future authorization server (the step (b) proxy) issues tokens whose `aud` is
// the canonical MCP URL, add that URL to CAINBAN_AUTH_AUDIENCE at that point —
// the validator already accepts a LIST — rather than special-casing it here.
func (s *Server) HandlerWithAuthChallenge(resolver *auth.Resolver, resourceMetadataURL string) http.Handler {
	return AuthMiddlewareWithChallenge(resolver, resourceMetadataURL, s.Handler())
}

// ServeHTTP serves the stateless Streamable HTTP transport on addr, bound to
// loopback only. addr may be ":8080" or "127.0.0.1:8080"; a bare-port or
// wildcard host is rewritten to 127.0.0.1 so the server never binds a public
// interface (multi-user/public exposure is deferred to Phase 3).
func (s *Server) ServeHTTP(addr string) error {
	handler := s.Handler()

	loopbackAddr, err := loopbackOnly(addr)
	if err != nil {
		return err
	}

	httpServer := &http.Server{
		Addr:    loopbackAddr,
		Handler: handler,
	}
	fmt.Printf("cainban MCP server (stateless, Streamable HTTP) listening on http://%s\n", loopbackAddr)
	return httpServer.ListenAndServe()
}

// loopbackOnly forces the host portion of addr to 127.0.0.1, keeping the port.
func loopbackOnly(addr string) (string, error) {
	if addr == "" {
		return "", fmt.Errorf("empty address")
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		// addr had no host:port form (e.g. bare port "8080" or ":8080" already
		// handled by SplitHostPort). Treat a bare number as a port.
		return "", fmt.Errorf("invalid address %q (use host:port or :port): %w", addr, err)
	}
	if host == "" || host == "0.0.0.0" || host == "::" || host == "*" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port), nil
}

// resolveTaskSystem opens the correct board store for a single request and
// returns a store.TaskStore bound to it plus a close func the caller MUST
// defer.
//
// The concrete backend is chosen by store.OpenTask from CAINBAN_BACKEND
// (default sqlite for local dev; dynamodb for the Lambda/serverless path). The
// handlers below depend only on the store.TaskStore interface, so swapping the
// backend requires no handler change.
//
// # Per-request tenant (Phase 3)
//
// When the request context carries an AUTHORIZED tenant (set by AuthMiddleware
// after signature-first JWT validation), the DynamoDB store is built with that
// tenant's partition prefix ("REPO#<owner>/<repo>#") via
// store.OpenTaskForTenant. Every key the store touches is then under that
// prefix, so a request authorized for repo A can never read or write repo B's
// items — isolation is structural, not a filter. The DynamoDB client itself is
// cached and reused across requests; only the prefix varies per tenant.
//
// The stdio/local CLI path sets no tenant, so the prefix is empty and behavior
// is the single-tenant Phase 2 default (and SQLite ignores the prefix
// entirely).
//
// Board selection is fully explicit per request (no reliance on the
// ~/.cainban/current-board file):
//   - boardName selects which board DATABASE FILE to open under the SQLite
//     backend; empty means the default board. Under DynamoDB the path is
//     ignored (a single table holds every board partition).
//   - Each board DB has its own boards table whose primary board is id 1, so
//     the numeric board ROW defaults to 1 in the handlers.
func (s *Server) resolveTaskSystem(ctx context.Context, boardName string) (store.TaskStore, func(), error) {
	if boardName == "" {
		boardName = "default"
	}
	dbPath := s.boardSystem.GetBoardPath(boardName)

	partitionPrefix := ""
	if t, ok := tenantFromContext(ctx); ok && t != nil {
		// An UNSCOPED tenant authenticated but named no repo (and had no
		// default_repo). It is valid only for the MCP handshake, never for a
		// data operation: opening a store with an empty prefix would collapse
		// every tenant into one partition. Fail closed with a clear message the
		// client can act on (name a repo via the X-Cainban-Repo header or set a
		// default_repo). Handshake ops (initialize, tools/list) never reach here.
		if t.Unscoped {
			return nil, nil, fmt.Errorf("no target repo for this request: set the %s header or a default_repo claim before calling a tool", auth.HeaderTargetRepo)
		}
		partitionPrefix = t.PartitionPrefix
	}

	ts, closer, err := store.OpenTaskForTenant(context.Background(), dbPath, partitionPrefix)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open board %q: %w", boardName, err)
	}
	closeFn := func() { _ = closer() }
	return ts, closeFn, nil
}

// resolveBoardStore opens the per-request BoardStore, mirroring
// resolveTaskSystem EXACTLY — including the single most important rule: an
// UNSCOPED tenant (authenticated but naming no repo, with no default_repo) must
// fail closed BEFORE any store opens, or list_boards on an empty partition
// prefix would enumerate every tenant's boards. The guard here is identical to
// the task path's; the two must never drift.
//
// The board tools take no repo argument (list_boards takes nothing;
// change_board's board_name is a board selector), so this helper reads only the
// resolved tenant from context and passes its partition prefix. The repo was
// authorized once upstream in AuthMiddleware; no board-specific auth is added.
func (s *Server) resolveBoardStore(ctx context.Context) (store.BoardStore, func(), error) {
	dbPath := s.boardSystem.GetBoardPath("default")

	partitionPrefix := ""
	if t, ok := tenantFromContext(ctx); ok && t != nil {
		if t.Unscoped {
			return nil, nil, fmt.Errorf("no target repo for this request: set the %s header or a default_repo claim before calling a tool", auth.HeaderTargetRepo)
		}
		partitionPrefix = t.PartitionPrefix
	}

	bs, closer, err := store.OpenBoardForTenant(context.Background(), dbPath, partitionPrefix)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open board store: %w", err)
	}
	closeFn := func() { _ = closer() }
	return bs, closeFn, nil
}
func (s *Server) registerTools(mcpServer *mcp.Server) {
	mcp.AddTool(mcpServer, &mcp.Tool{
		Name:        "create_task",
		Description: "Create a new task in the kanban board",
		Annotations: writer("Create task", false /*idempotent*/, false /*destructive*/),
	}, s.handleCreateTask)

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name:        "list_tasks",
		Description: "List tasks from the kanban board",
		Annotations: readOnly("List tasks"),
	}, s.handleListTasks)

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name:        "update_task_status",
		Description: "Update the status of a task",
		Annotations: writer("Update task status", true /*idempotent*/, true /*destructive*/),
	}, s.handleUpdateTaskStatus)

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name:        "get_task",
		Description: "Get a specific task by ID",
		Annotations: readOnly("Get task"),
	}, s.handleGetTask)

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name:        "update_task_priority",
		Description: "Update the priority of a task",
		Annotations: writer("Update task priority", true /*idempotent*/, true /*destructive*/),
	}, s.handleUpdateTaskPriority)

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name:        "update_task",
		Description: "Update a task's title and description",
		Annotations: writer("Update task", true /*idempotent*/, true /*destructive*/),
	}, s.handleUpdateTask)

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name:        "list_boards",
		Description: "List all available kanban boards",
		Annotations: readOnly("List boards"),
	}, s.handleListBoards)

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name:        "change_board",
		Description: "Change the active kanban board",
		// Read-only in the multi-user design: it validates that a board exists
		// in scope and reports it, but board selection is per-request so it
		// mutates no shared state.
		Annotations: readOnly("Change board"),
	}, s.handleChangeBoard)

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name:        "list_activity",
		Description: "List recent task activity (who changed what), newest first",
		Annotations: readOnly("List activity"),
	}, s.handleListActivity)

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name:        "whoami",
		Description: "Report the repo and board scope the current token resolves to",
		Annotations: readOnly("Who am I (current scope)"),
	}, s.handleWhoami)

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name:        "delete_task",
		Description: "Delete a task. Soft-delete by default (recoverable with restore_task); pass hard=true to permanently remove it.",
		// Destructive (removes a task) and idempotent (deleting an already-
		// deleted task converges to the same state).
		Annotations: writer("Delete task", true /*idempotent*/, true /*destructive*/),
	}, s.handleDeleteTask)

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name:        "restore_task",
		Description: "Restore a soft-deleted task so it is visible again. Has no effect on a task that was never deleted or was hard-deleted.",
		// Non-destructive (brings a task back) and idempotent (restoring an
		// already-visible task converges to the same state).
		Annotations: writer("Restore task", true /*idempotent*/, false /*destructive*/),
	}, s.handleRestoreTask)

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name:        "search_tasks",
		Description: "Fuzzy-search tasks in the current board by title substring. Returns matching tasks newest-first.",
		Annotations: readOnly("Search tasks"),
	}, s.handleSearchTasks)

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name:        "link_tasks",
		Description: "Create a directional link between two tasks (blocks, blocked_by, related, depends_on).",
		Annotations: writer("Link tasks", false /*idempotent — link already exists is an error*/, false /*destructive*/),
	}, s.handleLinkTasks)

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name:        "unlink_tasks",
		Description: "Remove a link between two tasks.",
		Annotations: writer("Unlink tasks", true /*idempotent*/, true /*destructive*/),
	}, s.handleUnlinkTasks)

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name:        "get_task_links",
		Description: "List all links referencing a task (both directions).",
		Annotations: readOnly("Get task links"),
	}, s.handleGetTaskLinks)
}

// ptrBool returns a pointer to b, for the SDK annotation fields that are
// *bool (DestructiveHint, OpenWorldHint) and distinguish "unset" from "false".
func ptrBool(b bool) *bool { return &b }

// readOnly builds the annotations for a tool that does not modify its
// environment: ReadOnlyHint true and a closed interaction world (no external
// entities). DestructiveHint/IdempotentHint are meaningful only when a tool is
// NOT read-only, so they are left unset here.
func readOnly(title string) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{
		Title:         title,
		ReadOnlyHint:  true,
		OpenWorldHint: ptrBool(false),
	}
}

// writer builds the annotations for a tool that modifies its environment:
// ReadOnlyHint false, with explicit idempotent/destructive hints and a closed
// interaction world. create_task is non-idempotent (mints a new id each call)
// and additive (non-destructive); the update_* tools are idempotent (re-applying
// the same field value has no further effect) and destructive (they overwrite).
func writer(title string, idempotent, destructive bool) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{
		Title:           title,
		ReadOnlyHint:    false,
		IdempotentHint:  idempotent,
		DestructiveHint: ptrBool(destructive),
		OpenWorldHint:   ptrBool(false),
	}
}

// actorFromCtx returns the human-readable caller (email, else sub) recorded on
// the request's tenant for the activity feed. Empty on the local CLI path,
// which carries no tenant.
func actorFromCtx(ctx context.Context) string {
	if t, ok := tenantFromContext(ctx); ok && t != nil {
		return t.Actor
	}
	return ""
}

// repoFromCtx returns the resolved, AUTHORIZED repo for the request, or "" when
// the request carries no scoped tenant (the local CLI path, or an unscoped
// authenticated tenant). The value is the repo the caller's signed token
// authorized — never the raw untrusted header/arg.
func repoFromCtx(ctx context.Context) string {
	if t, ok := tenantFromContext(ctx); ok && t != nil && !t.Unscoped {
		return t.Repo
	}
	return ""
}

// scopeLine builds the one-line "where am I?" header prefixed to list_tasks /
// list_boards output so the caller can see the scope on calls they already
// make. The board is always id 1 today (single board per tenant). In single-user
// mode (no tenant) the repo reads "(local)". This and handleWhoami share this
// one formatter so their wording cannot drift.
func scopeLine(ctx context.Context) string {
	repo := repoFromCtx(ctx)
	if repo == "" {
		repo = "(local)"
	}
	return fmt.Sprintf("Scope — repo: %s · board: default (id 1)", repo)
}

// Tool handler argument types.

type CreateTaskArgs struct {
	Title       string      `json:"title" jsonschema:"the title of the task"`
	Description string      `json:"description,omitempty" jsonschema:"the description of the task"`
	BoardID     int         `json:"board_id,omitempty" jsonschema:"the board ID (defaults to 1)"`
	Priority    interface{} `json:"priority,omitempty" jsonschema:"priority level (none, low, medium, high, critical or 0-4)"`
}

type ListTasksArgs struct {
	BoardID int    `json:"board_id,omitempty" jsonschema:"the board ID (defaults to 1)"`
	Status  string `json:"status,omitempty" jsonschema:"filter by status (todo, doing, done)"`
}

type UpdateTaskStatusArgs struct {
	ID              int    `json:"id" jsonschema:"the task ID"`
	Status          string `json:"status" jsonschema:"the new status (todo, doing, done)"`
	ExpectedVersion int    `json:"expected_version,omitempty" jsonschema:"optimistic-concurrency guard: the task version you last read; the write fails with a version-conflict error if the task changed since. Omit (or 0) to force-write."`
}

type GetTaskArgs struct {
	ID int `json:"id" jsonschema:"the task ID"`
}

type UpdateTaskPriorityArgs struct {
	ID              int         `json:"id" jsonschema:"task ID to update"`
	Priority        interface{} `json:"priority" jsonschema:"priority level (none, low, medium, high, critical or 0-4)"`
	ExpectedVersion int         `json:"expected_version,omitempty" jsonschema:"optimistic-concurrency guard: the task version you last read; the write fails with a version-conflict error if the task changed since. Omit (or 0) to force-write."`
}

type UpdateTaskArgs struct {
	ID              int    `json:"id" jsonschema:"the task ID"`
	Title           string `json:"title" jsonschema:"the new title"`
	Description     string `json:"description,omitempty" jsonschema:"the new description"`
	ExpectedVersion int    `json:"expected_version,omitempty" jsonschema:"optimistic-concurrency guard: the task version you last read; the write fails with a version-conflict error if the task changed since. Omit (or 0) to force-write."`
}

type ListBoardsArgs struct{}

// WhoamiArgs is empty: the scope is derived entirely from the authenticated
// request, never from caller input.
type WhoamiArgs struct{}

type ChangeBoardArgs struct {
	BoardName string `json:"board_name" jsonschema:"the name of the board to switch to"`
}

type ListActivityArgs struct {
	TaskID int `json:"task_id,omitempty" jsonschema:"optional: only show activity for this board task ID; omit for the whole board"`
	Limit  int `json:"limit,omitempty" jsonschema:"max events to return, newest first (default 50, max 200)"`
}

type DeleteTaskArgs struct {
	ID   int  `json:"id" jsonschema:"the board task ID to delete"`
	Hard bool `json:"hard,omitempty" jsonschema:"permanently delete instead of soft-delete; a hard delete cannot be restored (default false)"`
}

type RestoreTaskArgs struct {
	ID int `json:"id" jsonschema:"the board task ID to restore"`
}

type SearchTasksArgs struct {
	Query   string `json:"query" jsonschema:"title substring to fuzzy-match against tasks in the board"`
	BoardID int    `json:"board_id,omitempty" jsonschema:"the board ID (defaults to 1)"`
}

type LinkTasksArgs struct {
	FromID   int    `json:"from_id" jsonschema:"board task ID of the source task"`
	ToID     int    `json:"to_id" jsonschema:"board task ID of the target task"`
	LinkType string `json:"type" jsonschema:"link type: blocks, blocked_by, related, depends_on"`
}

type UnlinkTasksArgs struct {
	FromID   int    `json:"from_id" jsonschema:"board task ID of the source task"`
	ToID     int    `json:"to_id" jsonschema:"board task ID of the target task"`
	LinkType string `json:"type" jsonschema:"link type: blocks, blocked_by, related, depends_on"`
}

type GetTaskLinksArgs struct {
	ID int `json:"id" jsonschema:"the board task ID to list links for"`
}

// Tool handlers. Each resolves its board database per request.

func (s *Server) handleCreateTask(ctx context.Context, req *mcp.CallToolRequest, args CreateTaskArgs) (*mcp.CallToolResult, any, error) {
	taskSystem, closeFn, err := s.resolveTaskSystem(ctx, "")
	if err != nil {
		return nil, nil, err
	}
	defer closeFn()

	boardID := args.BoardID
	if boardID == 0 {
		boardID = 1
	}

	var createdTask *task.Task
	if args.Priority != nil && task.IsValidPriority(args.Priority) {
		createdTask, err = taskSystem.CreateWithPriority(boardID, args.Title, args.Description, args.Priority)
	} else {
		createdTask, err = taskSystem.Create(boardID, args.Title, args.Description)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create task: %w", err)
	}

	// Best-effort append-only audit: never fail the tool call on a record error.
	_ = taskSystem.RecordActivity(task.ActivityEvent{
		BoardID:     boardID,
		BoardTaskID: createdTask.BoardTaskID,
		Action:      task.ActivityCreated,
		Actor:       actorFromCtx(ctx),
		Detail:      createdTask.Title,
	})

	priorityStr := ""
	if createdTask.Priority > 0 {
		priorityStr = fmt.Sprintf(" [%s]", task.GetPriorityName(createdTask.Priority))
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{
				Text: fmt.Sprintf("Created task #%d%s: %s", createdTask.BoardTaskID, priorityStr, createdTask.Title),
			},
		},
	}, createdTask, nil
}

func (s *Server) handleListTasks(ctx context.Context, req *mcp.CallToolRequest, args ListTasksArgs) (*mcp.CallToolResult, []*task.Task, error) {
	taskSystem, closeFn, err := s.resolveTaskSystem(ctx, "")
	if err != nil {
		return nil, nil, err
	}
	defer closeFn()

	boardID := args.BoardID
	if boardID == 0 {
		boardID = 1
	}

	var tasks []*task.Task
	if args.Status != "" {
		if !task.IsValidStatus(args.Status) {
			return toolError("invalid status %q; valid values are todo, doing, done", args.Status), nil, nil
		}
		tasks, err = taskSystem.ListByStatus(boardID, task.Status(args.Status))
	} else {
		tasks, err = taskSystem.List(boardID)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list tasks: %w", err)
	}

	if len(tasks) == 0 {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: scopeLine(ctx)},
				&mcp.TextContent{Text: "No tasks found in current board"},
			},
		}, tasks, nil
	}

	// Group by status for better display
	tasksByStatus := make(map[task.Status][]*task.Task)
	for _, t := range tasks {
		tasksByStatus[t.Status] = append(tasksByStatus[t.Status], t)
	}

	var content []mcp.Content
	content = append(content, &mcp.TextContent{Text: scopeLine(ctx)})
	statuses := []task.Status{task.StatusTodo, task.StatusDoing, task.StatusDone}
	for _, status := range statuses {
		if statusTasks, exists := tasksByStatus[status]; exists && len(statusTasks) > 0 {
			content = append(content, &mcp.TextContent{
				Text: fmt.Sprintf("\n%s:", string(status)),
			})
			for _, t := range statusTasks {
				priorityStr := ""
				if t.Priority > 0 {
					priorityStr = fmt.Sprintf(" [%s]", task.GetPriorityName(t.Priority))
				}
				content = append(content, &mcp.TextContent{
					Text: fmt.Sprintf("• #%d%s %s", t.BoardTaskID, priorityStr, t.Title),
				})
			}
		}
	}

	return &mcp.CallToolResult{Content: content}, tasks, nil
}

func (s *Server) handleUpdateTaskStatus(ctx context.Context, req *mcp.CallToolRequest, args UpdateTaskStatusArgs) (*mcp.CallToolResult, any, error) {
	if !task.IsValidStatus(args.Status) {
		return toolError("invalid status %q; valid values are todo, doing, done", args.Status), nil, nil
	}

	taskSystem, closeFn, err := s.resolveTaskSystem(ctx, "")
	if err != nil {
		return nil, nil, err
	}
	defer closeFn()

	// Board row 1: each board database has its own boards table with ID 1.
	boardID := 1

	t, err := taskSystem.GetByBoardTaskID(boardID, args.ID)
	if err != nil {
		return toolError("task #%d not found; call list_tasks to see valid task ids", args.ID), nil, nil
	}

	if args.ExpectedVersion > 0 {
		if err := taskSystem.UpdateStatusIfVersion(t.ID, task.Status(args.Status), args.ExpectedVersion); err != nil {
			if errors.Is(err, store.ErrVersionConflict) {
				return versionConflictResult(args.ID), nil, nil
			}
			return nil, nil, fmt.Errorf("failed to update task status: %w", err)
		}
	} else if err := taskSystem.UpdateStatus(t.ID, task.Status(args.Status)); err != nil {
		return nil, nil, fmt.Errorf("failed to update task status: %w", err)
	}

	// Best-effort append-only audit (success path only; t.Status is the old value).
	_ = taskSystem.RecordActivity(task.ActivityEvent{
		BoardID:     boardID,
		BoardTaskID: args.ID,
		Action:      task.ActivityStatusChanged,
		Actor:       actorFromCtx(ctx),
		Detail:      fmt.Sprintf("%s -> %s", t.Status, args.Status),
	})

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{
				Text: fmt.Sprintf("Updated task #%d status to %s", args.ID, args.Status),
			},
		},
	}, nil, nil
}

// versionConflictResult formats the clean, agent-facing tool error returned
// when an expected_version guard fails: it tells the caller to re-read and
// retry rather than surfacing the raw store error.
func versionConflictResult(id int) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{
			&mcp.TextContent{
				Text: fmt.Sprintf("task #%d changed since you read it (version conflict); re-read with get_task and retry", id),
			},
		},
	}
}

// toolError returns an MCP tool RESULT flagged isError (not a transport/JSON-RPC
// error) for an EXPECTED, model-recoverable domain outcome — a task that does
// not exist, an invalid status/priority value, a board not in scope. The model
// sees the message in the content stream and can recover (re-read, pick a valid
// value) instead of the call surfacing as a protocol fault. Reserve a transport
// error (return nil, nil, err) for true faults the model cannot fix by retrying
// with different args, e.g. a store/DynamoDB failure.
func toolError(format string, args ...any) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{
			&mcp.TextContent{Text: fmt.Sprintf(format, args...)},
		},
	}
}

func (s *Server) handleGetTask(ctx context.Context, req *mcp.CallToolRequest, args GetTaskArgs) (*mcp.CallToolResult, *task.Task, error) {
	taskSystem, closeFn, err := s.resolveTaskSystem(ctx, "")
	if err != nil {
		return nil, nil, err
	}
	defer closeFn()

	boardID := 1

	t, err := taskSystem.GetByBoardTaskID(boardID, args.ID)
	if err != nil {
		return toolError("task #%d not found; call list_tasks to see valid task ids", args.ID), nil, nil
	}

	text := fmt.Sprintf("#%d [%s] %s\n%s", t.BoardTaskID, t.Status, t.Title, t.Description)

	// Best-effort: append links if any exist. A link-read failure is not
	// worth failing the whole get_task call.
	if links, err := taskSystem.GetTaskLinks(t.ID); err == nil && len(links) > 0 {
		text += "\nLinks:"
		for _, l := range links {
			text += fmt.Sprintf("\n  • #%d %s #%d", l.FromTaskID, l.LinkType, l.ToTaskID)
		}
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: text},
		},
	}, t, nil
}

func (s *Server) handleUpdateTaskPriority(ctx context.Context, req *mcp.CallToolRequest, args UpdateTaskPriorityArgs) (*mcp.CallToolResult, any, error) {
	if !task.IsValidPriority(args.Priority) {
		return toolError("invalid priority; valid values are none, low, medium, high, critical (or 0-4)"), nil, nil
	}

	taskSystem, closeFn, err := s.resolveTaskSystem(ctx, "")
	if err != nil {
		return nil, nil, err
	}
	defer closeFn()

	boardID := 1

	t, err := taskSystem.GetByBoardTaskID(boardID, args.ID)
	if err != nil {
		return toolError("task #%d not found; call list_tasks to see valid task ids", args.ID), nil, nil
	}

	if args.ExpectedVersion > 0 {
		if err := taskSystem.UpdatePriorityIfVersion(t.ID, args.Priority, args.ExpectedVersion); err != nil {
			if errors.Is(err, store.ErrVersionConflict) {
				return versionConflictResult(args.ID), nil, nil
			}
			return nil, nil, fmt.Errorf("failed to update task priority: %w", err)
		}
	} else if err := taskSystem.UpdatePriority(t.ID, args.Priority); err != nil {
		return nil, nil, fmt.Errorf("failed to update task priority: %w", err)
	}

	priorityLevel, _ := task.ParsePriority(args.Priority)
	priorityName := task.GetPriorityName(priorityLevel)

	// Best-effort append-only audit (success path only; t.Priority is the old value).
	_ = taskSystem.RecordActivity(task.ActivityEvent{
		BoardID:     boardID,
		BoardTaskID: args.ID,
		Action:      task.ActivityPriorityChanged,
		Actor:       actorFromCtx(ctx),
		Detail:      fmt.Sprintf("%s -> %s", task.GetPriorityName(t.Priority), priorityName),
	})

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{
				Text: fmt.Sprintf("Task #%d priority updated to %s (%d)", args.ID, priorityName, priorityLevel),
			},
		},
	}, nil, nil
}

func (s *Server) handleUpdateTask(ctx context.Context, req *mcp.CallToolRequest, args UpdateTaskArgs) (*mcp.CallToolResult, any, error) {
	taskSystem, closeFn, err := s.resolveTaskSystem(ctx, "")
	if err != nil {
		return nil, nil, err
	}
	defer closeFn()

	boardID := 1

	t, err := taskSystem.GetByBoardTaskID(boardID, args.ID)
	if err != nil {
		return toolError("task #%d not found; call list_tasks to see valid task ids", args.ID), nil, nil
	}

	if args.ExpectedVersion > 0 {
		if err := taskSystem.UpdateIfVersion(t.ID, args.Title, args.Description, args.ExpectedVersion); err != nil {
			if errors.Is(err, store.ErrVersionConflict) {
				return versionConflictResult(args.ID), nil, nil
			}
			return nil, nil, fmt.Errorf("failed to update task: %w", err)
		}
	} else if err := taskSystem.Update(t.ID, args.Title, args.Description); err != nil {
		return nil, nil, fmt.Errorf("failed to update task: %w", err)
	}

	// Best-effort append-only audit (success path only).
	_ = taskSystem.RecordActivity(task.ActivityEvent{
		BoardID:     boardID,
		BoardTaskID: args.ID,
		Action:      task.ActivityUpdated,
		Actor:       actorFromCtx(ctx),
		Detail:      args.Title,
	})

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{
				Text: fmt.Sprintf("Updated task #%d: %s", args.ID, args.Title),
			},
		},
	}, nil, nil
}

func (s *Server) handleListBoards(ctx context.Context, req *mcp.CallToolRequest, args ListBoardsArgs) (*mcp.CallToolResult, []task.BoardSummary, error) {
	bs, closeFn, err := s.resolveBoardStore(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer closeFn()

	boards, err := bs.ListBoards()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list boards: %w", err)
	}

	if len(boards) == 0 {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: scopeLine(ctx)},
				&mcp.TextContent{Text: "No boards found"},
			},
		}, boards, nil
	}

	var content []mcp.Content
	content = append(content, &mcp.TextContent{Text: scopeLine(ctx)})
	content = append(content, &mcp.TextContent{Text: "Available boards:"})
	for _, b := range boards {
		content = append(content, &mcp.TextContent{
			Text: fmt.Sprintf("• %s", b.Name),
		})
	}

	return &mcp.CallToolResult{Content: content}, boards, nil
}

// handleChangeBoard validates that the requested board exists in the caller's
// scope and reports it. It resolves through the per-request BoardStore (the
// RIGHT backend), so it works on the deployed server instead of always failing
// against the local filesystem.
//
// In multi-user mode it is a NO-OP with respect to shared state — board
// selection is per-request, so it validates and reports, never mutating. In
// single-user mode it may additionally set the local current board for CLI/TUI
// parity, reached through a SetCurrent type assertion that only the SQLite
// adapter satisfies (so the DynamoDB store never mutates shared state and the
// handler needs no CAINBAN_BACKEND branch).
func (s *Server) handleChangeBoard(ctx context.Context, req *mcp.CallToolRequest, args ChangeBoardArgs) (*mcp.CallToolResult, any, error) {
	bs, closeFn, err := s.resolveBoardStore(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer closeFn()

	b, err := bs.ResolveBoard(args.BoardName)
	if err != nil {
		if errors.Is(err, store.ErrBoardNotFound) {
			return toolError("board %q not found in this scope; call list_boards to see valid boards", args.BoardName), nil, nil
		}
		return nil, nil, fmt.Errorf("failed to resolve board %q: %w", args.BoardName, err)
	}

	// Single-user parity only: set the local current board if the backend
	// supports it. Best-effort — a failure must not fail the tool.
	if sbs, ok := bs.(interface{ SetCurrent(string) error }); ok {
		_ = sbs.SetCurrent(b.Name)
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{
				Text: fmt.Sprintf("Board %q (id %d) exists. Note: board selection is now per-request (change_board no longer changes global state); pass the board explicitly on each call.", b.Name, b.ID),
			},
		},
	}, b, nil
}

// whoamiResult is the structured content of the whoami tool: the resolved repo
// and board scope the current token maps to, plus the human-readable actor. All
// fields come from the already-AUTHORIZED tenant and the per-request board
// store — nothing new is authorized here.
type whoamiResult struct {
	Repo         string              `json:"repo"`
	Actor        string              `json:"actor,omitempty"`
	Boards       []task.BoardSummary `json:"boards"`
	DefaultBoard *task.BoardSummary  `json:"default_board,omitempty"`
}

// handleWhoami reports the repo + board scope the current token resolves to, so
// a caller can answer "where am I?" without inferring the repo from their token
// and the board from list_tasks output. Read-only; adds no authorization.
//
// On an UNSCOPED tenant (authenticated but no repo named and no default_repo) it
// returns the no-scope message as NORMAL content (not isError): asking where you
// are with no scope is a valid question whose answer is "nowhere yet". It opens
// no store in that case, mirroring the fail-closed data path.
func (s *Server) handleWhoami(ctx context.Context, req *mcp.CallToolRequest, args WhoamiArgs) (*mcp.CallToolResult, *whoamiResult, error) {
	repo := repoFromCtx(ctx)
	actor := actorFromCtx(ctx)

	// Unscoped (or an authenticated tenant that named no repo): report the gap
	// and the remedy, open no store.
	if _, ok := tenantFromContext(ctx); ok && repo == "" {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: fmt.Sprintf("No repo in scope. Set the %s header or a default_repo claim before calling a tool.", auth.HeaderTargetRepo)},
			},
		}, nil, nil
	}

	// Local CLI (no tenant) reports a local scope; scoped multi-user reports the
	// resolved repo. Either way the board list comes from the per-request store.
	bs, closeFn, err := s.resolveBoardStore(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer closeFn()

	boards, err := bs.ListBoards()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list boards: %w", err)
	}

	shownRepo := repo
	if shownRepo == "" {
		shownRepo = "(local)"
	}

	res := whoamiResult{Repo: shownRepo, Actor: actor, Boards: boards}
	if len(boards) > 0 {
		res.DefaultBoard = &boards[0]
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Repo: %s", shownRepo)
	if actor != "" {
		fmt.Fprintf(&sb, " · Actor: %s", actor)
	}
	if len(boards) > 0 {
		sb.WriteString(" · Boards:")
		for _, b := range boards {
			fmt.Fprintf(&sb, " %s (id %d)", b.Name, b.ID)
		}
	} else {
		sb.WriteString(" · Boards: none yet")
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: sb.String()}},
	}, &res, nil
}

// handleListActivity returns the append-only activity feed for the current
// board (or a single task when task_id is given), newest first. The event store
// is pure audit and is never used to derive task/board state.
func (s *Server) handleListActivity(ctx context.Context, req *mcp.CallToolRequest, args ListActivityArgs) (*mcp.CallToolResult, []task.ActivityEvent, error) {
	taskSystem, closeFn, err := s.resolveTaskSystem(ctx, "")
	if err != nil {
		return nil, nil, err
	}
	defer closeFn()

	boardID := 1

	events, err := taskSystem.ListActivity(boardID, args.TaskID, args.Limit)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list activity: %w", err)
	}

	if len(events) == 0 {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: "No activity recorded"},
			},
		}, events, nil
	}

	var content []mcp.Content
	for _, ev := range events {
		content = append(content, &mcp.TextContent{
			Text: fmt.Sprintf("%s  #%d %s  %s  (%s)",
				ev.Timestamp.Format(time.RFC3339), ev.BoardTaskID, ev.Action, ev.Detail, ev.Actor),
		})
	}

	return &mcp.CallToolResult{Content: content}, events, nil
}

// handleDeleteTask removes a task from the current board. By default it
// soft-deletes (recoverable via restore_task); hard=true permanently removes it
// and its links. A missing task is an EXPECTED outcome returned as an isError
// tool result, not a transport error, so the model can recover.
func (s *Server) handleDeleteTask(ctx context.Context, req *mcp.CallToolRequest, args DeleteTaskArgs) (*mcp.CallToolResult, any, error) {
	taskSystem, closeFn, err := s.resolveTaskSystem(ctx, "")
	if err != nil {
		return nil, nil, err
	}
	defer closeFn()

	boardID := 1

	t, err := taskSystem.GetByBoardTaskID(boardID, args.ID)
	if err != nil {
		return toolError("task #%d not found; call list_tasks to see valid task ids", args.ID), nil, nil
	}

	if args.Hard {
		if err := taskSystem.HardDelete(t.ID); err != nil {
			return nil, nil, fmt.Errorf("failed to hard-delete task: %w", err)
		}
	} else if err := taskSystem.SoftDelete(t.ID); err != nil {
		return nil, nil, fmt.Errorf("failed to delete task: %w", err)
	}

	kind := "deleted"
	detail := "soft delete"
	if args.Hard {
		kind = "permanently deleted"
		detail = "hard delete"
	}

	// Best-effort append-only audit (success path only).
	_ = taskSystem.RecordActivity(task.ActivityEvent{
		BoardID:     boardID,
		BoardTaskID: args.ID,
		Action:      task.ActivityDeleted,
		Actor:       actorFromCtx(ctx),
		Detail:      detail,
	})

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: fmt.Sprintf("Task #%d %s (%q)", args.ID, kind, t.Title)},
		},
	}, nil, nil
}

// handleRestoreTask brings a soft-deleted task back into view. Because a
// soft-deleted task is excluded from GetByBoardTaskID, the restore is addressed
// by board task id and resolved through the store's own RestoreTask, which
// no-ops cleanly on a task that was never deleted.
func (s *Server) handleRestoreTask(ctx context.Context, req *mcp.CallToolRequest, args RestoreTaskArgs) (*mcp.CallToolResult, any, error) {
	taskSystem, closeFn, err := s.resolveTaskSystem(ctx, "")
	if err != nil {
		return nil, nil, err
	}
	defer closeFn()

	boardID := 1

	if err := taskSystem.RestoreTask(args.ID); err != nil {
		// A not-found / already-hard-deleted task is an expected outcome.
		return toolError("task #%d could not be restored; it may have been hard-deleted or never existed", args.ID), nil, nil
	}

	// Best-effort append-only audit (success path only).
	_ = taskSystem.RecordActivity(task.ActivityEvent{
		BoardID:     boardID,
		BoardTaskID: args.ID,
		Action:      task.ActivityRestored,
		Actor:       actorFromCtx(ctx),
		Detail:      "restored",
	})

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: fmt.Sprintf("Task #%d restored", args.ID)},
		},
	}, nil, nil
}

// handleSearchTasks fuzzy-matches tasks by title within the current board.
// Read-only; prepends the same scope header as list_tasks so the caller sees
// which repo/board the results belong to.
func (s *Server) handleSearchTasks(ctx context.Context, req *mcp.CallToolRequest, args SearchTasksArgs) (*mcp.CallToolResult, []*task.Task, error) {
	taskSystem, closeFn, err := s.resolveTaskSystem(ctx, "")
	if err != nil {
		return nil, nil, err
	}
	defer closeFn()

	boardID := args.BoardID
	if boardID == 0 {
		boardID = 1
	}

	results, err := taskSystem.SearchTasks(boardID, args.Query)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to search tasks: %w", err)
	}

	content := []mcp.Content{&mcp.TextContent{Text: scopeLine(ctx)}}
	if len(results) == 0 {
		content = append(content, &mcp.TextContent{Text: fmt.Sprintf("No tasks matching %q", args.Query)})
		return &mcp.CallToolResult{Content: content}, results, nil
	}

	for _, t := range results {
		content = append(content, &mcp.TextContent{
			Text: fmt.Sprintf("• #%d [%s] %s", t.BoardTaskID, t.Status, t.Title),
		})
	}

	return &mcp.CallToolResult{Content: content}, results, nil
}

// validLinkTypes lists the recognized link types for error messages.
const validLinkTypes = "blocks, blocked_by, related, depends_on"

// handleLinkTasks creates a directional link between two tasks.
func (s *Server) handleLinkTasks(ctx context.Context, req *mcp.CallToolRequest, args LinkTasksArgs) (*mcp.CallToolResult, any, error) {
	if !task.IsValidLinkType(args.LinkType) {
		return toolError("invalid link type %q; valid values are %s", args.LinkType, validLinkTypes), nil, nil
	}

	taskSystem, closeFn, err := s.resolveTaskSystem(ctx, "")
	if err != nil {
		return nil, nil, err
	}
	defer closeFn()

	boardID := 1

	from, err := taskSystem.GetByBoardTaskID(boardID, args.FromID)
	if err != nil {
		return toolError("from task #%d not found; call list_tasks to see valid task ids", args.FromID), nil, nil
	}
	to, err := taskSystem.GetByBoardTaskID(boardID, args.ToID)
	if err != nil {
		return toolError("to task #%d not found; call list_tasks to see valid task ids", args.ToID), nil, nil
	}

	if err := taskSystem.LinkTasks(from.ID, to.ID, task.LinkType(args.LinkType)); err != nil {
		return toolError("failed to link: %s", err), nil, nil
	}

	// Best-effort audit on both tasks.
	for _, id := range []int{args.FromID, args.ToID} {
		_ = taskSystem.RecordActivity(task.ActivityEvent{
			BoardID:     boardID,
			BoardTaskID: id,
			Action:      task.ActivityLinked,
			Actor:       actorFromCtx(ctx),
			Detail:      fmt.Sprintf("#%d %s #%d", args.FromID, args.LinkType, args.ToID),
		})
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: fmt.Sprintf("Linked: #%d %s #%d", args.FromID, args.LinkType, args.ToID)},
		},
	}, nil, nil
}

// handleUnlinkTasks removes a specific link between two tasks.
func (s *Server) handleUnlinkTasks(ctx context.Context, req *mcp.CallToolRequest, args UnlinkTasksArgs) (*mcp.CallToolResult, any, error) {
	if !task.IsValidLinkType(args.LinkType) {
		return toolError("invalid link type %q; valid values are %s", args.LinkType, validLinkTypes), nil, nil
	}

	taskSystem, closeFn, err := s.resolveTaskSystem(ctx, "")
	if err != nil {
		return nil, nil, err
	}
	defer closeFn()

	boardID := 1

	from, err := taskSystem.GetByBoardTaskID(boardID, args.FromID)
	if err != nil {
		return toolError("from task #%d not found; call list_tasks to see valid task ids", args.FromID), nil, nil
	}
	to, err := taskSystem.GetByBoardTaskID(boardID, args.ToID)
	if err != nil {
		return toolError("to task #%d not found; call list_tasks to see valid task ids", args.ToID), nil, nil
	}

	if err := taskSystem.UnlinkTasks(from.ID, to.ID, task.LinkType(args.LinkType)); err != nil {
		return toolError("no %s link found between #%d and #%d", args.LinkType, args.FromID, args.ToID), nil, nil
	}

	for _, id := range []int{args.FromID, args.ToID} {
		_ = taskSystem.RecordActivity(task.ActivityEvent{
			BoardID:     boardID,
			BoardTaskID: id,
			Action:      task.ActivityUnlinked,
			Actor:       actorFromCtx(ctx),
			Detail:      fmt.Sprintf("#%d %s #%d", args.FromID, args.LinkType, args.ToID),
		})
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: fmt.Sprintf("Unlinked: #%d %s #%d", args.FromID, args.LinkType, args.ToID)},
		},
	}, nil, nil
}

// handleGetTaskLinks returns all links referencing a task in both directions.
func (s *Server) handleGetTaskLinks(ctx context.Context, req *mcp.CallToolRequest, args GetTaskLinksArgs) (*mcp.CallToolResult, []task.TaskLink, error) {
	taskSystem, closeFn, err := s.resolveTaskSystem(ctx, "")
	if err != nil {
		return nil, nil, err
	}
	defer closeFn()

	boardID := 1

	t, err := taskSystem.GetByBoardTaskID(boardID, args.ID)
	if err != nil {
		return toolError("task #%d not found; call list_tasks to see valid task ids", args.ID), nil, nil
	}

	links, err := taskSystem.GetTaskLinks(t.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get task links: %w", err)
	}

	content := []mcp.Content{&mcp.TextContent{Text: scopeLine(ctx)}}
	if len(links) == 0 {
		content = append(content, &mcp.TextContent{Text: fmt.Sprintf("Task #%d has no links", args.ID)})
		return &mcp.CallToolResult{Content: content}, links, nil
	}

	for _, l := range links {
		content = append(content, &mcp.TextContent{
			Text: fmt.Sprintf("• #%d %s #%d", l.FromTaskID, l.LinkType, l.ToTaskID),
		})
	}

	return &mcp.CallToolResult{Content: content}, links, nil
}
