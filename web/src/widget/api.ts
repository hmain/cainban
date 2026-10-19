// Dependency-free MCP client for the embeddable widget.
//
// This is a trimmed, self-contained sibling of web/src/mcpApi.ts: it speaks the
// exact same stateless Streamable-HTTP JSON-RPC protocol to the cainban MCP
// endpoint, but imports NOTHING from the SPA (no aws-amplify, no shared config)
// so the widget bundle carries no runtime dependencies. The embedder supplies
// the endpoint and target repo; auth is optional (a read-only bearer token) for
// a server that permits public reads, otherwise the token is required.

export type Status = "todo" | "doing" | "done";

export interface Counts {
  todo: number;
  doing: number;
  done: number;
  total: number;
}

export interface WidgetConfig {
  /** MCP Streamable-HTTP endpoint (JSON-RPC tools/call). */
  api: string;
  /** Target board as owner/repo. */
  repo: string;
  /** Optional read-only bearer token for authed MCP servers. */
  token?: string;
}

// A monotonically increasing JSON-RPC id, module-scoped like the SPA client.
let rpcId = 0;

/**
 * Call the MCP `list_tasks` tool and reduce the result to status counts.
 * Throws a human-readable Error on transport / auth / protocol failure so the
 * widget can render a reason. Mirrors mcpApi.ts's transport (POST tools/call,
 * Accept json-or-SSE, X-Cainban-Repo header, optional bearer).
 */
export async function fetchCounts(cfg: WidgetConfig): Promise<Counts> {
  if (!cfg.api) throw new Error("No API endpoint configured.");
  if (!cfg.repo) throw new Error("No repo configured.");

  const body = {
    jsonrpc: "2.0",
    id: ++rpcId,
    method: "tools/call",
    params: { name: "list_tasks", arguments: {} },
  };

  const headers: Record<string, string> = {
    "Content-Type": "application/json",
    Accept: "application/json, text/event-stream",
    "X-Cainban-Repo": cfg.repo,
  };
  if (cfg.token) headers.Authorization = `Bearer ${cfg.token}`;

  let res: Response;
  try {
    res = await fetch(cfg.api, {
      method: "POST",
      headers,
      body: JSON.stringify(body),
    });
  } catch (e) {
    throw new Error(`Network error: ${String(e)}`);
  }

  if (res.status === 401) throw new Error("Unauthorized (401).");
  if (res.status === 403) throw new Error(`Forbidden (403) for ${cfg.repo}.`);
  if (!res.ok) throw new Error(`Request failed (${res.status}).`);

  const payload = await parseResponse(res);
  if (payload.error) {
    throw new Error(payload.error.message || "MCP tool call returned an error.");
  }
  const result = payload.result;
  if (!result) throw new Error("MCP response had no result.");

  const tasks = extractTasks(result);
  return countByStatus(tasks);
}

interface Envelope {
  error?: { message?: string };
  result?: ToolResult;
}

interface ToolResult {
  content?: Array<{ type?: string; text?: string }>;
  structuredContent?: unknown;
}

// The stateless server may answer with a single JSON object or an SSE stream
// whose `data:` frames carry JSON-RPC envelopes. Handle both (same logic as the
// SPA client), reading the last frame that parses as a result/error.
async function parseResponse(res: Response): Promise<Envelope> {
  const ct = res.headers.get("content-type") || "";
  const raw = await res.text();
  if (
    ct.includes("text/event-stream") ||
    raw.startsWith("event:") ||
    raw.startsWith("data:")
  ) {
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
        const obj = JSON.parse(frames[i]) as Envelope;
        if (obj.result || obj.error) return obj;
      } catch {
        // not this frame
      }
    }
    throw new Error("Could not parse MCP SSE response.");
  }
  try {
    return JSON.parse(raw) as Envelope;
  } catch {
    throw new Error("Could not parse MCP JSON response.");
  }
}

// Reduce the tool result to the list of task statuses we need. Prefers the typed
// structuredContent (handler's []*task.Task); we only read the `status` field.
function extractTasks(result: ToolResult): Status[] {
  const sc = result.structuredContent;
  const arr = asArray(sc);
  if (arr) return arr;
  if (sc && typeof sc === "object") {
    for (const v of Object.values(sc as Record<string, unknown>)) {
      const inner = asArray(v);
      if (inner) return inner;
    }
  }
  return [];
}

function asArray(v: unknown): Status[] | null {
  if (!Array.isArray(v)) return null;
  return v.map((raw) => {
    const o = (raw ?? {}) as Record<string, unknown>;
    const s = o.status ?? o.Status;
    return s === "doing" || s === "done" ? (s as Status) : "todo";
  });
}

function countByStatus(statuses: Status[]): Counts {
  const c: Counts = { todo: 0, doing: 0, done: 0, total: statuses.length };
  for (const s of statuses) c[s] += 1;
  return c;
}
