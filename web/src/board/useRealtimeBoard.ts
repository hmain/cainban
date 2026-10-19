import { useCallback, useEffect, useReducer, useRef, useState } from "react";
import { listTasks, listActivity } from "../mcpApi";
import { getIdToken } from "../connectApi";
import { boardReducer, initialBoardState, type BoardState } from "./reducer";
import { PollAdapter } from "./pollAdapter";
import { WebSocketAdapter, type ConnectionState } from "./wsAdapter";
import { MCP_API } from "../amplify";

export type TransportMode = "websocket" | "polling" | "auto";

export interface UseRealtimeBoardOptions {
  /** The repo to watch (owner/repo format) */
  repo: string;
  /** Transport mode: "websocket", "polling", or "auto" (default) */
  mode?: TransportMode;
  /** Callback when connection state changes */
  onConnectionChange?: (state: ConnectionState, degraded: boolean) => void;
}

export interface UseRealtimeBoardResult {
  /** Current board state */
  state: BoardState;
  /** Current connection state */
  connectionState: ConnectionState;
  /** Whether using degraded (polling) transport */
  isDegraded: boolean;
  /** Whether initial load is in progress */
  isLoading: boolean;
  /** Error message if any */
  error: string | null;
  /** Force a refresh of the board */
  refresh: () => Promise<void>;
  /** Retry connection after failure */
  retry: () => void;
}

/**
 * useRealtimeBoard provides real-time board updates via WebSocket when available,
 * with automatic fallback to polling. This is the primary hook for the "mission
 * control" experience where users watch agents work.
 *
 * Usage:
 * ```tsx
 * function Board({ repo }: { repo: string }) {
 *   const { state, connectionState, isLoading, error } = useRealtimeBoard({ repo });
 *
 *   if (isLoading) return <Loading />;
 *   if (error) return <Error message={error} />;
 *
 *   return (
 *     <div>
 *       <ConnectionChip state={connectionState} />
 *       <BoardColumns tasks={state.tasks} />
 *     </div>
 *   );
 * }
 * ```
 */
export function useRealtimeBoard(options: UseRealtimeBoardOptions): UseRealtimeBoardResult {
  const { repo, mode = "auto", onConnectionChange } = options;
  
  const [state, dispatch] = useReducer(boardReducer, undefined, initialBoardState);
  const [connectionState, setConnectionState] = useState<ConnectionState>("connecting");
  const [isDegraded, setIsDegraded] = useState(true);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  
  // Refs to avoid stale closures
  const cursorRef = useRef<string | null>(null);
  cursorRef.current = state.lastCursor;
  
  const pollAdapterRef = useRef<PollAdapter | null>(null);
  const wsAdapterRef = useRef<WebSocketAdapter | null>(null);

  // Determine which transport to use
  const shouldUseWebSocket = useCallback(() => {
    if (mode === "polling") return false;
    if (mode === "websocket") return WebSocketAdapter.isSupported();
    // "auto" mode: try WebSocket first
    return WebSocketAdapter.isSupported();
  }, [mode]);

  // Handle connection state changes
  const handleConnectionChange = useCallback((newState: ConnectionState, degraded: boolean) => {
    setConnectionState(newState);
    setIsDegraded(degraded);
    dispatch({ type: "connection", state: newState, degraded });
    onConnectionChange?.(newState, degraded);
  }, [onConnectionChange]);

  // Initial load and transport setup
  useEffect(() => {
    let mounted = true;
    
    const setup = async () => {
      setIsLoading(true);
      setError(null);
      
      try {
        // Initial snapshot to verify auth and get board state
        const res = await listTasks(repo);
        if (!mounted) return;
        
        // Get initial cursor from latest activity
        let cursor: string | null = null;
        try {
          const act = await listActivity(repo, { limit: 1 });
          cursor = act.events.length > 0 ? act.events[0].Timestamp : null;
        } catch {
          cursor = null;
        }
        
        dispatch({ type: "snapshot", tasks: res.tasks, cursor });
        setIsLoading(false);
        
        // Start real-time transport
        if (shouldUseWebSocket()) {
          // Try WebSocket
          const wsUrl = `wss://${MCP_API.replace(/^https?:\/\//, "")}/ws/${repo}`;
          const wsAdapter = new WebSocketAdapter({
            url: wsUrl,
            getToken: getIdToken,
            dispatch,
            onConnectionChange: (s, d) => {
              if (!mounted) return;
              handleConnectionChange(s, d);
              // If WebSocket fails completely, fall back to polling
              if (s === "stale") {
                wsAdapter.stop();
                wsAdapterRef.current = null;
                startPolling();
              }
            },
          });
          wsAdapterRef.current = wsAdapter;
          void wsAdapter.start();
        } else {
          startPolling();
        }
      } catch (e) {
        if (!mounted) return;
        const msg = String(e instanceof Error ? e.message : e);
        setError(msg);
        setIsLoading(false);
      }
    };
    
    const startPolling = () => {
      const pollAdapter = new PollAdapter(repo, dispatch, () => cursorRef.current);
      pollAdapterRef.current = pollAdapter;
      void pollAdapter.start();
      handleConnectionChange("live", true);
    };
    
    void setup();
    
    return () => {
      mounted = false;
      pollAdapterRef.current?.stop();
      pollAdapterRef.current = null;
      wsAdapterRef.current?.stop();
      wsAdapterRef.current = null;
    };
  }, [repo, shouldUseWebSocket, handleConnectionChange]);

  // Refresh function
  const refresh = useCallback(async () => {
    try {
      const res = await listTasks(repo);
      let cursor: string | null = null;
      try {
        const act = await listActivity(repo, { limit: 1 });
        cursor = act.events.length > 0 ? act.events[0].Timestamp : null;
      } catch {
        cursor = null;
      }
      dispatch({ type: "snapshot", tasks: res.tasks, cursor });
    } catch (e) {
      setError(String(e instanceof Error ? e.message : e));
    }
  }, [repo]);

  // Retry function
  const retry = useCallback(() => {
    if (wsAdapterRef.current) {
      wsAdapterRef.current.reconnect();
    } else if (pollAdapterRef.current) {
      pollAdapterRef.current.retry();
    }
  }, []);

  return {
    state,
    connectionState,
    isDegraded,
    isLoading,
    error,
    refresh,
    retry,
  };
}

export default useRealtimeBoard;
