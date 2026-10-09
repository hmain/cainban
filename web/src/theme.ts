// Theme preference: a three-state choice persisted in localStorage.
//
//   "system" (default) — follow the OS via prefers-color-scheme; no data-theme
//                         attribute is set, so the CSS :root:not([data-theme])
//                         dark branch applies when the OS prefers dark.
//   "light" / "dark"    — force that scheme by setting data-theme on <html>,
//                         which the CSS [data-theme=…] rules honour over the OS.
//
// The actual palette lives entirely in styles.css; this module only sets/reads
// the attribute and the stored choice.

export type ThemePref = "system" | "light" | "dark";

const STORAGE_KEY = "cainban:theme";

export function readThemePref(): ThemePref {
  try {
    const v = localStorage.getItem(STORAGE_KEY);
    if (v === "light" || v === "dark" || v === "system") return v;
  } catch {
    // localStorage unavailable (private mode / blocked) — fall back to system.
  }
  return "system";
}

// applyThemePref sets or clears the data-theme attribute on <html> to match the
// preference, and persists the choice. "system" clears the attribute so the OS
// media query drives the palette.
export function applyThemePref(pref: ThemePref): void {
  const root = document.documentElement;
  if (pref === "system") {
    root.removeAttribute("data-theme");
  } else {
    root.setAttribute("data-theme", pref);
  }
  try {
    localStorage.setItem(STORAGE_KEY, pref);
  } catch {
    // Non-fatal: the attribute is still applied for this session.
  }
}

// nextThemePref cycles System → Light → Dark → System.
export function nextThemePref(pref: ThemePref): ThemePref {
  switch (pref) {
    case "system":
      return "light";
    case "light":
      return "dark";
    case "dark":
      return "system";
  }
}

// initTheme applies the stored preference as early as possible (called from
// main.tsx before React mounts, to avoid a flash of the wrong theme).
export function initTheme(): void {
  applyThemePref(readThemePref());
}
