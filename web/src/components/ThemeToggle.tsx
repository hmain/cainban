import { useState } from "react";
import {
  applyThemePref,
  nextThemePref,
  readThemePref,
  type ThemePref,
} from "../theme";

// ThemeToggle is a single button that cycles System → Light → Dark and persists
// the choice. "System" follows the OS (prefers-color-scheme); Light/Dark force
// a scheme via the data-theme attribute (see theme.ts + styles.css). The icon +
// label name the ACTIVE preference, and the title tells the user what the next
// click does, so the three-state cycle is discoverable.
const META: Record<ThemePref, { icon: string; label: string }> = {
  system: { icon: "🖥", label: "System" },
  light: { icon: "☀", label: "Light" },
  dark: { icon: "🌙", label: "Dark" },
};

export function ThemeToggle() {
  const [pref, setPref] = useState<ThemePref>(() => readThemePref());
  const next = nextThemePref(pref);
  const m = META[pref];

  return (
    <button
      type="button"
      className="theme-toggle"
      onClick={() => {
        const p = nextThemePref(pref);
        applyThemePref(p);
        setPref(p);
      }}
      title={`Theme: ${m.label}. Click for ${META[next].label}.`}
      aria-label={`Theme: ${m.label}. Click to switch to ${META[next].label}.`}
    >
      <span aria-hidden="true">{m.icon}</span>
      <span className="theme-toggle-label">{m.label}</span>
    </button>
  );
}
