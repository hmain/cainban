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

// A monotonically increasing JSON-RPC id for this page's MCP calls.
let rpcId = 0;

/**
 * Call the MCP `list_activity` tool for `repo`, optionally scoped to one task.
 * Throws on transport / auth / protocol errors so the caller can show a reason.
 */
export async function listActivity(
  repo: string,
  opts: { taskId?: number; limit?: number } = {},
): Promise<ListActivityResult> {
  if (!MCP_API) {
    throw new Error("MCP API URL is not configured (VITE_MCP_API).");
  }
  const token = await getIdToken();

  const args: Record<string, unknown> = {};
  if (opts.taskId && opts.taskId > 0) args.task_id = opts.taskId;
  if (opts.limit && opts.limit > 0) args.limit = opts.limit;

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
