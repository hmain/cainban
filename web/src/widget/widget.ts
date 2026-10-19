import { createRoot } from "react-dom/client";
import { createElement } from "react";
import { WidgetApp } from "./WidgetApp";
import type { WidgetAppProps } from "./WidgetApp";
// The CSS is imported as a raw string (see vite.config.widget.ts ?inline) and
// injected into each widget's Shadow DOM, so nothing touches the host page's
// global stylesheet.
import widgetCss from "./widget.css?inline";

// Entry point for the embeddable cainban widget (built as an IIFE bundle).
//
// Usage on any page:
//   <script src="cainban-widget.js"></script>
//   <div
//     data-cainban-widget
//     data-repo="owner/repo"
//     data-api="https://…/mcp"      (MCP endpoint; optional if a default is set)
//     data-token="ro-token"          (optional read-only bearer)
//     data-board-url="https://…"     (optional; defaults to the cainban app)
//     data-theme="light|dark"        (optional; omit to follow the OS)
//     data-poll="30"                 (optional seconds; 0 disables; default 30)
//     data-bar="true"                (optional mini-board bar; default true)
//   ></div>
//
// Each matching element is upgraded exactly once into its own Shadow DOM so
// multiple widgets on one page stay isolated from each other and from the host.

const MOUNTED = "data-cainban-mounted";

// Default public cainban app origin; data-board-url overrides it per widget, and
// data-api can default from it too when the embedder omits the MCP endpoint.
const DEFAULT_APP = "https://cainban.com";

interface Settings extends WidgetAppProps {
  theme?: "light" | "dark";
}

function readSettings(el: HTMLElement): Settings | null {
  const repo = (el.getAttribute("data-repo") || "").trim();
  if (!repo) {
    // Nothing to render without a target board; log once and skip.
    console.warn("[cainban-widget] missing data-repo; skipping element.", el);
    return null;
  }
  const appBase = (el.getAttribute("data-board-url") || DEFAULT_APP).replace(
    /\/+$/,
    "",
  );
  const api = (el.getAttribute("data-api") || `${appBase}/mcp`).replace(
    /\/+$/,
    "",
  );
  const token = (el.getAttribute("data-token") || "").trim() || undefined;

  const boardUrl =
    el.getAttribute("data-board-url")?.trim() ||
    `${DEFAULT_APP}/board?repo=${encodeURIComponent(repo)}`;

  const themeAttr = (el.getAttribute("data-theme") || "").trim().toLowerCase();
  const theme =
    themeAttr === "light" || themeAttr === "dark" ? themeAttr : undefined;

  const pollRaw = el.getAttribute("data-poll");
  const pollSecs = pollRaw == null ? 30 : Number(pollRaw);
  const pollMs = Number.isFinite(pollSecs) && pollSecs > 0 ? pollSecs * 1000 : 0;

  const barAttr = (el.getAttribute("data-bar") || "true").toLowerCase();
  const showBar = barAttr !== "false" && barAttr !== "0";

  return { api, repo, token, boardUrl, pollMs, showBar, theme };
}

function mount(el: HTMLElement): void {
  if (el.hasAttribute(MOUNTED)) return;
  const settings = readSettings(el);
  if (!settings) return;
  el.setAttribute(MOUNTED, "");

  // The host element carries the data-theme so widget.css's :host rules apply.
  const host = document.createElement("div");
  if (settings.theme) host.setAttribute("data-theme", settings.theme);
  const shadow = host.attachShadow({ mode: "open" });

  const style = document.createElement("style");
  style.textContent = widgetCss;
  shadow.appendChild(style);

  const container = document.createElement("div");
  shadow.appendChild(container);
  el.appendChild(host);

  const { theme: _theme, ...appProps } = settings;
  createRoot(container).render(createElement(WidgetApp, appProps));
}

function mountAll(): void {
  const nodes = document.querySelectorAll<HTMLElement>("[data-cainban-widget]");
  nodes.forEach(mount);
}

// Auto-mount when the DOM is ready; also expose a manual hook for SPAs that
// inject widgets after load.
if (document.readyState === "loading") {
  document.addEventListener("DOMContentLoaded", mountAll);
} else {
  mountAll();
}

// Public API: window.cainbanWidget.mount() re-scans for new elements.
declare global {
  interface Window {
    cainbanWidget?: { mount: () => void };
  }
}
window.cainbanWidget = { mount: mountAll };
