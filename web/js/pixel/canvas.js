// Crisp pixel canvases. Scenes draw in "art pixels"; the canvas backing
// store is exactly one art pixel per device-pixel block, and CSS scales it
// up with image-rendering: pixelated. Smaller backing stores also mean
// less memory and less fill work every frame.

/** CSS pixels per art pixel. Matches --px in tokens.css. */
export const ART = 2;

/**
 * Size of the art grid for a box of cssW x cssH CSS pixels.
 * @param {number} cssW @param {number} cssH @param {number} [art]
 */
export function artSize(cssW, cssH, art = ART) {
  return { w: Math.max(1, Math.floor(cssW / art)), h: Math.max(1, Math.floor(cssH / art)) };
}

/**
 * Sizes a canvas to its CSS box in art pixels. Returns true when the
 * size changed (static layers must then be redrawn).
 * @param {HTMLCanvasElement} canvas
 */
export function fitCanvas(canvas, art = ART) {
  const rect = canvas.getBoundingClientRect();
  const { w, h } = artSize(rect.width, rect.height, art);
  if (canvas.width === w && canvas.height === h) return false;
  canvas.width = w;
  canvas.height = h;
  const ctx = canvas.getContext("2d");
  if (ctx) ctx.imageSmoothingEnabled = false;
  return true;
}

/**
 * An offscreen layer for scenery that is drawn once and blitted every
 * frame (stars, grids). Falls back to a detached canvas.
 * @param {number} w @param {number} h
 */
export function offscreen(w, h) {
  const c =
    typeof OffscreenCanvas !== "undefined"
      ? new OffscreenCanvas(w, h)
      : Object.assign(globalThis.document.createElement("canvas"), { width: w, height: h });
  const ctx = c.getContext("2d");
  ctx.imageSmoothingEnabled = false;
  return { canvas: c, ctx };
}

/** 4x4 Bayer matrix, thresholds in 0..1, for ordered dithering. */
export const BAYER = [0, 8, 2, 10, 12, 4, 14, 6, 3, 11, 1, 9, 15, 7, 13, 5].map((v) => (v + 0.5) / 16);

/** True when an art pixel at (x, y) should be lit for coverage 0..1. */
export const dither = (x, y, coverage) => coverage > BAYER[((y & 3) << 2) | (x & 3)];

/** Small deterministic PRNG (mulberry32) so scenery is the same every run. */
export function rng(seed) {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

/** FNV-1a hash of a string, for placing things by ID. */
export function hash(str) {
  let h = 0x811c9dc5;
  for (let i = 0; i < str.length; i++) {
    h ^= str.charCodeAt(i);
    h = Math.imul(h, 0x01000193);
  }
  return h >>> 0;
}
