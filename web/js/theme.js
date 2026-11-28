// Applies appearance settings to <html>: the theme swaps CSS variables
// (pixel scenes re-read them), motion off stops every animation.

export const THEMES = [
  { id: "cyan", name: "CYAN NEBULA" },
  { id: "amber", name: "AMBER TERMINAL" },
  { id: "magenta", name: "MAGENTA VOID" },
  { id: "light", name: "DAYLIGHT" },
];

/** @param {{ theme?: string, motion?: boolean }} s @param {HTMLElement} [root] */
export function applyAppearance(s, root = globalThis.document?.documentElement) {
  if (!root) return;
  const theme = THEMES.some((t) => t.id === s.theme) ? s.theme : "cyan";
  if (theme === "cyan") delete root.dataset.theme;
  else root.dataset.theme = theme;
  if (s.motion === false) root.dataset.motion = "off";
  else delete root.dataset.motion;
}
