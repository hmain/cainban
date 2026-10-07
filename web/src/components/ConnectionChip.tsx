import type { ConnectionState } from "../board/reducer";

const META: Record<
  ConnectionState,
  { dot: string; label: string; cls: string }
> = {
  connecting: { dot: "◌", label: "Connecting", cls: "connecting" },
  live: { dot: "●", label: "Live", cls: "live" },
  paused: { dot: "⏸", label: "Paused", cls: "paused" },
  reconnecting: { dot: "◌", label: "Reconnecting", cls: "reconnecting" },
  stale: { dot: "⚠", label: "Stale", cls: "stale" },
};

// ConnectionChip shows the board's connection health. States are distinguished
// by icon + label (not colour alone). When the SPA is on the degraded polling
// transport but healthy, it reads "Live (polling)" rather than hiding the mode.
// Stale offers a manual Retry.
export function ConnectionChip({
  state,
  degraded,
  onRetry,
}: {
  state: ConnectionState;
  degraded: boolean;
  onRetry: () => void;
}) {
  const m = META[state];
  const live = state === "live";
  const cls = live && degraded ? "degraded" : m.cls;
  const label = live && degraded ? "Live (polling)" : m.label;
  return (
    <span className={`conn-chip ${cls}`} role="status" aria-live="polite">
      <span className="conn-dot" aria-hidden="true">
        {m.dot}
      </span>
      {label}
      {state === "stale" && (
        <button className="link" onClick={onRetry}>
          Retry
        </button>
      )}
    </span>
  );
}
