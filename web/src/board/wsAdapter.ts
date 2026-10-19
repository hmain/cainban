import type { BoardEvent } from "./reducer";
import type { Status } from "./types";
import { classifyActor } from "./actor";

/**
 * WebSocketAdapter connects to a cainban WebSocket endpoint for real-time
 * board updates. It dispatches the same BoardEvent types as PollAdapter,
 * allowing seamless switching between polling and WebSocket transports.
 *
 * The WebSocket protocol is:
 * - Connect: wss://<MCP_API>/ws/<owner>/<repo>
 * - Auth: Send { type: "auth", token: "<jwt>" } after connect
 * - Events: Server sends { type: "event", event: <ActivityEvent> }
 * - Heartbeat: Server sends { type: "ping" }, client responds { type: "pong" }
 *
 * When WebSocket is unavailable or fails, falls back to PollAdapter.
 */

export type ConnectionState = 
  | "connecting" 
  | "live" 
  | "reconnecting" 
  | "paused" 
  | "stale";

export interface WSAdapterOptions {
  /** WebSocket endpoint URL (wss://...) */
  url: string;
  /** JWT token for authentication */
  getToken: () => Promise<string>;
  /** Event dispatcher */
  dispatch: (ev: BoardEvent) => void;
  /** Called when connection state changes */
  onConnectionChange?: (state: ConnectionState, degraded: boolean) => void;
  /** Reconnect delay in ms (default 1000) */
  reconnectDelay?: number;
  /** Max reconnect attempts before giving up (default 5) */
  maxReconnectAttempts?: number;
}

export class WebSocketAdapter {
  private url: string;
  private getToken: () => Promise<string>;
  private dispatch: (ev: BoardEvent) => void;
  private onConnectionChange?: (state: ConnectionState, degraded: boolean) => void;
  private reconnectDelay: number;
  private maxReconnectAttempts: number;

  private ws: WebSocket | null = null;
  private stopped = false;
  private reconnectCount = 0;
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private pingTimer: ReturnType<typeof setInterval> | null = null;
  private lastPong = Date.now();

  constructor(options: WSAdapterOptions) {
    this.url = options.url;
    this.getToken = options.getToken;
    this.dispatch = options.dispatch;
    this.onConnectionChange = options.onConnectionChange;
    this.reconnectDelay = options.reconnectDelay ?? 1000;
    this.maxReconnectAttempts = options.maxReconnectAttempts ?? 5;
  }

  /** Start the WebSocket connection */
  async start(): Promise<void> {
    this.stopped = false;
    this.reconnectCount = 0;
    this.setConnectionState("connecting", false);
    await this.connect();
  }

  /** Stop the WebSocket connection and cleanup */
  stop(): void {
    this.stopped = true;
    this.cleanup();
  }

  /** Force reconnection (e.g., after auth refresh) */
  reconnect(): void {
    if (this.stopped) return;
    this.cleanup();
    this.reconnectCount = 0;
    void this.connect();
  }

  private async connect(): Promise<void> {
    if (this.stopped) return;

    try {
      const token = await this.getToken();
      
      // Create WebSocket connection
      this.ws = new WebSocket(this.url);
      
      this.ws.onopen = () => {
        // Send auth message
        this.ws?.send(JSON.stringify({ type: "auth", token }));
        this.reconnectCount = 0;
        this.lastPong = Date.now();
        this.startPingMonitor();
        this.setConnectionState("live", false);
      };

      this.ws.onmessage = (event) => {
        this.handleMessage(event.data);
      };

      this.ws.onerror = () => {
        // Error is followed by close, handle reconnect there
      };

      this.ws.onclose = () => {
        this.cleanup();
        this.scheduleReconnect();
      };
    } catch (error) {
      this.cleanup();
      this.scheduleReconnect();
    }
  }

  private handleMessage(data: string): void {
    try {
      const msg = JSON.parse(data) as WSMessage;
      
      switch (msg.type) {
        case "ping":
          this.ws?.send(JSON.stringify({ type: "pong" }));
          this.lastPong = Date.now();
          break;
        
        case "pong":
          this.lastPong = Date.now();
          break;
        
        case "event":
          if (msg.event) {
            const boardEvent = mapWSEventToBoardEvent(msg.event);
            if (boardEvent) {
              this.dispatch(boardEvent);
            }
          }
          break;
        
        case "snapshot":
          // Full board snapshot (initial or recovery)
          if (msg.tasks) {
            this.dispatch({
              type: "snapshot",
              tasks: msg.tasks,
              cursor: msg.cursor ?? null,
            });
          }
          break;
        
        case "error":
          console.error("WebSocket error from server:", msg.message);
          break;
      }
    } catch {
      // Ignore malformed messages
    }
  }

  private startPingMonitor(): void {
    // Check connection health every 30 seconds
    this.pingTimer = setInterval(() => {
      const elapsed = Date.now() - this.lastPong;
      if (elapsed > 60000) {
        // No pong in 60s, connection is dead
        this.cleanup();
        this.scheduleReconnect();
      } else if (elapsed > 30000) {
        // Send a ping to check
        this.ws?.send(JSON.stringify({ type: "ping" }));
      }
    }, 30000);
  }

  private scheduleReconnect(): void {
    if (this.stopped) return;
    
    this.reconnectCount++;
    
    if (this.reconnectCount > this.maxReconnectAttempts) {
      this.setConnectionState("stale", false);
      return;
    }

    this.setConnectionState("reconnecting", false);
    
    const delay = this.reconnectDelay * Math.pow(2, this.reconnectCount - 1);
    this.reconnectTimer = setTimeout(() => {
      void this.connect();
    }, Math.min(delay, 30000));
  }

  private cleanup(): void {
    if (this.ws) {
      this.ws.onopen = null;
      this.ws.onmessage = null;
      this.ws.onerror = null;
      this.ws.onclose = null;
      if (this.ws.readyState === WebSocket.OPEN) {
        this.ws.close();
      }
      this.ws = null;
    }
    if (this.pingTimer) {
      clearInterval(this.pingTimer);
      this.pingTimer = null;
    }
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
  }

  private setConnectionState(state: ConnectionState, degraded: boolean): void {
    this.dispatch({ type: "connection", state, degraded });
    this.onConnectionChange?.(state, degraded);
  }

  /** Check if WebSocket is supported in this environment */
  static isSupported(): boolean {
    return typeof WebSocket !== "undefined";
  }
}

// WebSocket message types
interface WSMessage {
  type: "ping" | "pong" | "event" | "snapshot" | "error" | "auth";
  event?: WSActivityEvent;
  tasks?: WSTask[];
  cursor?: string;
  message?: string;
}

interface WSActivityEvent {
  BoardTaskID: number;
  Action: string;
  Actor: string;
  Detail: string;
  Timestamp: string;
}

interface WSTask {
  BoardTaskID: number;
  Title: string;
  Status: "todo" | "doing" | "done";
  Priority: number;
  Description?: string;
}

/**
 * Maps a WebSocket activity event to a BoardEvent.
 * Returns null for events that don't affect board state.
 */
function mapWSEventToBoardEvent(ev: WSActivityEvent): BoardEvent | null {
  const actor = classifyActor(ev.Actor);
  const id = ev.BoardTaskID;
  const ts = ev.Timestamp;

  switch (ev.Action) {
    case "created":
      return {
        type: "task_created",
        task: { BoardTaskID: id, Title: ev.Detail, Status: "todo", Priority: 0 },
        actor,
        ts,
      };

    case "status_changed": {
      const to = parseStatusTarget(ev.Detail);
      if (!to) return null;
      return { type: "status_changed", id, to, actor, ts };
    }

    case "priority_changed": {
      const p = parsePriorityTarget(ev.Detail);
      if (p == null) return null;
      return { type: "priority_changed", id, priority: p, actor, ts };
    }

    case "updated":
      return { type: "task_edited", id, title: ev.Detail, actor, ts };

    case "deleted":
      return { type: "task_deleted", id, actor, ts };

    case "restored":
      return { type: "task_restored", id, actor, ts };

    // Link events don't affect board columns
    case "linked":
    case "unlinked":
      return null;

    default:
      return null;
  }
}

function parseStatusTarget(detail: string): Status | null {
  const m = detail.match(/->\s*(todo|doing|done)\s*$/i);
  if (!m) return null;
  return m[1].toLowerCase() as Status;
}

function parsePriorityTarget(detail: string): number | null {
  const m = detail.match(/->\s*(none|low|medium|high|critical)\s*$/i);
  if (!m) return null;
  const names: Record<string, number> = {
    none: 0,
    low: 1,
    medium: 2,
    high: 3,
    critical: 4,
  };
  return names[m[1].toLowerCase()] ?? null;
}
