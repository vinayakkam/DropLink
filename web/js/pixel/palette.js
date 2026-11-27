// Sprite colors come from the theme's CSS variables, so a theme switch
// recolors every scene. One letter per color in sprite strings.

export const KEYS = {
  k: "--void",
  K: "--void-2",
  p: "--panel",
  e: "--panel-edge",
  g: "--grid",
  c: "--cyan",
  C: "--cyan-dim",
  d: "--cyan-deep",
  m: "--magenta",
  M: "--magenta-dim",
  a: "--amber",
  A: "--amber-dim",
  l: "--lime",
  r: "--danger",
  w: "--ink",
  i: "--ink-dim",
  f: "--ink-faint",
};

/** The Cyan Nebula values, used when CSS is not available (tests). */
export const FALLBACK = {
  k: "#05070d",
  K: "#0a0f1c",
  p: "#0d1424",
  e: "#1d2b48",
  g: "#16233b",
  c: "#3ef2ff",
  C: "#138a9e",
  d: "#0b4a5a",
  m: "#ff3ea5",
  M: "#8f1f60",
  a: "#ffb63e",
  A: "#8a5a12",
  l: "#7dff6a",
  r: "#ff5a5a",
  w: "#d8f6ff",
  i: "#7fa3b8",
  f: "#4a6378",
};

/**
 * Reads the current theme's colors.
 * @param {Element} [el]
 * @returns {Record<string, string>}
 */
export function readPalette(el = globalThis.document?.documentElement) {
  if (!el || typeof getComputedStyle === "undefined") return { ...FALLBACK };
  const cs = getComputedStyle(el);
  /** @type {Record<string, string>} */
  const out = {};
  for (const [k, v] of Object.entries(KEYS)) out[k] = cs.getPropertyValue(v).trim() || FALLBACK[k];
  return out;
}
