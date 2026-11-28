// Applies appearance settings to <html>: the theme swaps CSS variables
// (pixel scenes re-read them), motion off stops every animation. "auto"
// resolves to Daylight or Cyan Nebula from the OS color scheme, live.

export const THEMES = [
  { id: "cyan", name: "CYAN NEBULA" },
  { id: "amber", name: "AMBER TERMINAL" },
  { id: "magenta", name: "MAGENTA VOID" },
  { id: "light", name: "DAYLIGHT" },
];

// What the CONFIG picker shows: the themes plus Auto, which follows the OS.
export const APPEARANCES = [{ id: "auto", name: "AUTO (SYSTEM)" }, ...THEMES];

/** True when the OS asks for a light color scheme. */
function prefersLight() {
  try {
    return globalThis.matchMedia?.("(prefers-color-scheme: light)").matches ?? false;
  } catch {
    return false;
  }
}

/** The concrete theme a settings value maps to; unknown → cyan. */
export function resolveTheme(id) {
  if (id === "auto") return prefersLight() ? "light" : "cyan";
  return THEMES.some((t) => t.id === id) ? id : "cyan";
}

/** @param {{ theme?: string, motion?: boolean }} s @param {HTMLElement} [root] */
export function applyAppearance(s, root = globalThis.document?.documentElement) {
  if (!root) return;
  const theme = resolveTheme(s?.theme);
  if (theme === "cyan") delete root.dataset.theme;
  else root.dataset.theme = theme;
  if (s?.motion === false) root.dataset.motion = "off";
  else delete root.dataset.motion;
}

/**
 * Reapplies the appearance when the OS color scheme changes, but only while
 * the chosen appearance is "auto". Returns a function that stops watching.
 * @param {() => ({ theme?: string, motion?: boolean } | null)} getSettings
 * @param {HTMLElement} [root]
 */
export function watchSystemAppearance(getSettings, root = globalThis.document?.documentElement) {
  let mql;
  try {
    mql = globalThis.matchMedia?.("(prefers-color-scheme: light)");
  } catch {
    mql = null;
  }
  if (!mql) return () => {};
  const onChange = () => {
    const s = getSettings();
    if (s?.theme === "auto") applyAppearance(s, root);
  };
  mql.addEventListener("change", onChange);
  return () => mql.removeEventListener("change", onChange);
}
