import { MCP_API } from "./amplify";
import { getIdToken } from "./connectApi";

// A minimal browser-side MCP client for the ONE read-only tool the connect page
// needs: `list_activity`. The connect page's other calls go to the Connect REST
// API (connectApi.ts); this file is separate because the activity feed lives on
// a DIFFERENT endpoint — the MCP Streamable-HTTP JSON-RPC handler at MCP_API —
// and speaks JSON-RPC, not REST.
//
// Auth: the SAME Cognito ID token the Connect API uses. The MCP API Gateway
// authorizer's JwtAudience includes the SPA client id (see infra/stack.go), and
// the ID token carries the user's validated `repos` claim (via the pre-token
// trigger), so a SPA ID token both authenticates AND authorizes at the MCP
// endpoint. The target repo is named with the X-Cainban-Repo header, exactly as
// an agent would (see docs/agent-via-mcp.md).
//
// This is a stateless Streamable-HTTP MCP server (no session handshake needed):
// we POST a single `tools/call` JSON-RPC request and read the result. We ask for
// a JSON response (not SSE) via the Accept header; the server returns the tool
// result inline.

/** One activity event, mirroring task.ActivityEvent (no JSON tags on the Go
 * struct, so the server marshals the exact Go field names). */
export interface ActivityEvent {
  BoardID: number;
  BoardTaskID: number;
  Action: string;
  Actor: string;
  Detail: string;
  Timestamp: string;
}

export interface ListActivityResult {
  events: ActivityEvent[];
  /** True when the server replied "No activity recorded" (empty but valid). */
  empty: boolean;
}

/** One task as the MCP list_tasks tool returns it. The Go task.Task struct
 * marshals exact field names (no JSON tags), so field casing is PascalCase. */
export interface Task {
  BoardTaskID: number;
  Title: string;
  Status: "todo" | "doing" | "done";
  Priority: number; // 0..4 (none..critical)
  Description?: string;
  UpdatedAt?: string;
}

export interface ListTasksResult {
  tasks: Task[];
  /** True when the server replied "No tasks found" (empty but valid). */
  empty: boolean;
}

// A monotonically increasing JSON-RPC id for this page's MCP calls.
let rpcId = 0;

/**
 * Call the MCP `list_activity` tool for `repo`, optionally scoped to one task.
 * Throws on transport / auth / protocol errors so the caller can show a reason.
 */
export async function listActivity(
  repo: string,
  opts: { taskId?: number; limit?: number; since?: string } = {},
): Promise<ListActivityResult> {
  if (!MCP_API) {
    throw new Error("MCP API URL is not configured (VITE_MCP_API).");
  }
  const token = await getIdToken();

  const args: Record<string, unknown> = {};
  if (opts.taskId && opts.taskId > 0) args.task_id = opts.taskId;
  if (opts.limit && opts.limit > 0) args.limit = opts.limit;
  if (opts.since) args.since = opts.since;

  const body = {
    jsonrpc: "2.0",
    id: ++rpcId,
    method: "tools/call",
    params: { name: "list_activity", arguments: args },
  };

  const res = await fetch(MCP_API, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      // The stateless handler can reply as JSON or SSE; ask for both so a
      // compliant server returns an inline JSON result.
      Accept: "application/json, text/event-stream",
      Authorization: `Bearer ${token}`,
      "X-Cainban-Repo": repo,
    },
    body: JSON.stringify(body),
  });

  if (res.status === 401) {
    throw new Error(
      "Unauthorized (401) — your session token may have expired. Try reloading.",
    );
  }
  if (res.status === 403) {
    throw new Error(
      `Forbidden (403) — your token does not authorize ${repo}. Connect the repo first.`,
    );
  }
  if (!res.ok) {
    throw new Error(`MCP request failed (${res.status}).`);
  }

  const payload = await parseMcpResponse(res);

  if (payload.error) {
    throw new Error(payload.error.message || "MCP tool call returned an error.");
  }
  const result = payload.result;
  if (!result) {
    throw new Error("MCP response had no result.");
  }

  // Prefer the structured content (the tool's second return value — the typed
  // []ActivityEvent). Fall back to parsing the human text lines if a server
  // build does not emit structuredContent.
  const structured = extractStructuredEvents(result);
  if (structured) {
    return { events: structured, empty: structured.length === 0 };
  }

  const texts = extractTextLines(result);
  if (texts.length === 1 && /^no activity recorded$/i.test(texts[0].trim())) {
    return { events: [], empty: true };
  }
  const parsed = texts
    .map(parseTextLine)
    .filter((e): e is ActivityEvent => e !== null);
  return { events: parsed, empty: parsed.length === 0 };
}

/**
 * Call the MCP `list_tasks` tool for `repo` and return the current board tasks.
 * Reuses the exact transport listActivity uses (POST tools/call, bearer ID
 * token, X-Cainban-Repo, SSE-or-JSON response). Prefers structuredContent (the
 * handler's typed []*task.Task second return value); throws on auth/transport
 * errors with the same 401/403 mapping so the board can show a reason.
 */
export async function listTasks(
  repo: string,
  opts: { status?: "todo" | "doing" | "done" } = {},
): Promise<ListTasksResult> {
  if (!MCP_API) {
    throw new Error("MCP API URL is not configured (VITE_MCP_API).");
  }
  const token = await getIdToken();

  const args: Record<string, unknown> = {};
  if (opts.status) args.status = opts.status;

  const body = {
    jsonrpc: "2.0",
    id: ++rpcId,
    method: "tools/call",
    params: { name: "list_tasks", arguments: args },
  };

  const res = await fetch(MCP_API, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      Accept: "application/json, text/event-stream",
      Authorization: `Bearer ${token}`,
      "X-Cainban-Repo": repo,
    },
    body: JSON.stringify(body),
  });

  if (res.status === 401) {
    throw new Error(
      "Unauthorized (401) — your session token may have expired. Try reloading.",
    );
  }
  if (res.status === 403) {
    throw new Error(
      `Forbidden (403) — your token does not authorize ${repo}. Connect the repo first.`,
    );
  }
  if (!res.ok) {
    throw new Error(`MCP request failed (${res.status}).`);
  }

  const payload = await parseMcpResponse(res);
  if (payload.error) {
    throw new Error(payload.error.message || "MCP tool call returned an error.");
  }
  const result = payload.result;
  if (!result) {
    throw new Error("MCP response had no result.");
  }

  const structured = extractStructuredTasks(result);
  if (structured) {
    return { tasks: structured, empty: structured.length === 0 };
  }

  // Fallback: the text content starts with a scope line, then either
  // "No tasks found in current board" or grouped "• #N [pri] title" lines.
  // Without structuredContent we cannot reliably recover status, so treat as
  // empty rather than guess (the real server always emits structuredContent).
  const texts = extractTextLines(result);
  const empty = texts.some((t) => /no tasks found/i.test(t));
  return { tasks: [], empty };
}

// extractStructuredTasks reads result.structuredContent as the typed task list
// the list_tasks handler returns ([]*task.Task). Accepts an array directly, or
// an object whose first array-valued field holds the tasks (mirrors the
// activity extractor's tolerance of SDK wrapping).
function extractStructuredTasks(result: McpToolResult): Task[] | null {
  const sc = result.structuredContent;
  if (sc == null) return null;
  const arr = asTaskArray(sc);
  if (arr) return arr;
  if (typeof sc === "object") {
    for (const v of Object.values(sc as Record<string, unknown>)) {
      const inner = asTaskArray(v);
      if (inner) return inner;
    }
  }
  return null;
}

function asTaskArray(v: unknown): Task[] | null {
  if (!Array.isArray(v)) return null;
  return v.map((raw) => {
    const o = (raw ?? {}) as Record<string, unknown>;
    // task.Task marshals with snake_case JSON tags (board_task_id, title,
    // status, priority, description, updated_at) — see src/systems/task/task.go.
    // Read those; keep a PascalCase fallback so a tagless build still parses.
    const pick = (snake: string, pascal: string): unknown =>
      o[snake] !== undefined ? o[snake] : o[pascal];
    const status = str(pick("status", "Status"));
    const description = pick("description", "Description");
    const updatedAt = pick("updated_at", "UpdatedAt");
    return {
      BoardTaskID: num(pick("board_task_id", "BoardTaskID")),
      Title: str(pick("title", "Title")),
      Status:
        status === "doing" || status === "done"
          ? (status as Task["Status"])
          : "todo",
      Priority: num(pick("priority", "Priority")),
      Description: description == null ? undefined : str(description),
      UpdatedAt: updatedAt == null ? undefined : str(updatedAt),
    };
  });
}

interface McpEnvelope {
  error?: { message?: string };
  result?: McpToolResult;
}

interface McpToolResult {
  content?: Array<{ type?: string; text?: string }>;
  structuredContent?: unknown;
}

// The stateless server may answer with a single JSON object, or (if it chose
// SSE) a text/event-stream whose data: lines carry the JSON-RPC frames. Handle
// both so we are robust to the transport the server picks.
async function parseMcpResponse(res: Response): Promise<McpEnvelope> {
  const ct = res.headers.get("content-type") || "";
  const raw = await res.text();
  if (ct.includes("text/event-stream") || raw.startsWith("event:") || raw.startsWith("data:")) {
    // Concatenate the JSON from the last `data:` frame that parses as a
    // JSON-RPC envelope with a result/error.
    const frames = raw
      .split(/\n\n+/)
      .map((block) =>
        block
          .split("\n")
          .filter((l) => l.startsWith("data:"))
          .map((l) => l.slice(5).trim())
          .join(""),
      )
      .filter(Boolean);
    for (let i = frames.length - 1; i >= 0; i--) {
      try {
        const obj = JSON.parse(frames[i]) as McpEnvelope;
        if (obj.result || obj.error) return obj;
      } catch {
        // not this frame
      }
    }
    throw new Error("Could not parse MCP SSE response.");
  }
  try {
    return JSON.parse(raw) as McpEnvelope;
  } catch {
    throw new Error("Could not parse MCP JSON response.");
  }
}

// extractStructuredEvents reads result.structuredContent when present. The Go
// handler returns `events` ([]task.ActivityEvent) as the structured value; the
// SDK may wrap it directly or under a key. We accept an array, or an object
// whose first array-valued field holds the events.
function extractStructuredEvents(result: McpToolResult): ActivityEvent[] | null {
  const sc = result.structuredContent;
  if (sc == null) return null;
  const arr = asEventArray(sc);
  if (arr) return arr;
  if (typeof sc === "object") {
    for (const v of Object.values(sc as Record<string, unknown>)) {
      const inner = asEventArray(v);
      if (inner) return inner;
    }
  }
  return null;
}

function asEventArray(v: unknown): ActivityEvent[] | null {
  if (!Array.isArray(v)) return null;
  return v.map((raw) => {
    const o = (raw ?? {}) as Record<string, unknown>;
    return {
      BoardID: num(o.BoardID),
      BoardTaskID: num(o.BoardTaskID),
      Action: str(o.Action),
      Actor: str(o.Actor),
      Detail: str(o.Detail),
      Timestamp: str(o.Timestamp),
    };
  });
}

function extractTextLines(result: McpToolResult): string[] {
  if (!Array.isArray(result.content)) return [];
  return result.content
    .filter((c) => (c.type === undefined || c.type === "text") && typeof c.text === "string")
    .map((c) => c.text as string);
}

// parseTextLine parses the server's text format:
//   "<RFC3339>  #<id> <action>  <detail>  (<actor>)"
// It is a fallback only (structuredContent is preferred). Detail may itself
// contain spaces; actor is the trailing parenthesized token.
function parseTextLine(line: string): ActivityEvent | null {
  const m = line.match(/^(\S+)\s+#(\d+)\s+(\S+)\s+(.*?)\s*\(([^)]*)\)\s*$/);
  if (!m) return null;
  return {
    BoardID: 0,
    BoardTaskID: Number(m[2]),
    Action: m[3],
    Detail: m[4].trim(),
    Actor: m[5],
    Timestamp: m[1],
  };
}

function num(v: unknown): number {
  return typeof v === "number" ? v : Number(v ?? 0) || 0;
}
function str(v: unknown): string {
  return typeof v === "string" ? v : v == null ? "" : String(v);
}
