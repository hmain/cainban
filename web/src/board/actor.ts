import type { Actor } from "./reducer";

// classifyActor maps an activity event's Actor string to agent / human /
// unknown. A human signs in with Entra, so their actor is an email (contains
// "@"). An agent authenticates with the MCP CLI client-id, which surfaces as an
// opaque principal (no "@") — the same opaque-sub shape formatActor shortens.
// This is the single definition the board and the activity feed share, so the
// two cannot drift.
export function classifyActor(actor: string): Actor {
  if (!actor) return "unknown";
  if (actor.includes("@")) return "human";
  // Opaque principal (sub / CLI client-id): treat as agent.
  if (/^[0-9a-f-]{20,}$/i.test(actor)) return "agent";
  // Any other non-email principal (e.g. a named client id) is an agent too.
  return "agent";
}

// displayActor renders an actor string for the UI: email as-is, opaque sub
// shortened to a prefix. Shared with the activity feed's formatActor.
export function displayActor(actor: string): string {
  if (!actor) return "unknown";
  if (actor.includes("@")) return actor;
  if (/^[0-9a-f-]{20,}$/i.test(actor)) return `${actor.slice(0, 8)}…`;
  return actor;
}
