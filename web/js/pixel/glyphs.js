// SAS glyphs: the 4 symbols both screens show so people can check they
// are talking to each other and not to a man in the middle. Glyph n
// (0..63) is a 5 x 5 pattern, mirror-symmetric like an alien rune. The set
// is generated from a fixed seed, so every device draws the same 64, and
// patterns that are too sparse, too dense or duplicates are skipped.
import { rng } from "./canvas.js";

export const SIZE = 5;
export const COUNT = 64;

/** @type {Uint8Array[]} */
const TABLE = build();

function build() {
  const next = rng(0xd201);
  const seen = new Set();
  /** @type {Uint8Array[]} */
  const out = [];
  while (out.length < COUNT) {
    // 15 bits: columns 0..2 of 5 rows; columns 3..4 mirror 1..0.
    const bits = Math.floor(next() * 0x8000);
    let lit = 0;
    for (let b = 0; b < 15; b++) lit += (bits >> b) & 1;
    const middle = [0, 1, 2, 3, 4].some((r) => (bits >> (r * 3 + 2)) & 1);
    const top = bits & 0b111;
    const bottom = (bits >> 12) & 0b111;
    if (lit < 6 || lit > 11 || !middle || !top || !bottom || seen.has(bits)) continue;
    seen.add(bits);
    const g = new Uint8Array(SIZE * SIZE);
    for (let r = 0; r < SIZE; r++) {
      for (let c = 0; c < 3; c++) {
        const on = (bits >> (r * 3 + c)) & 1;
        g[r * SIZE + c] = on;
        g[r * SIZE + (SIZE - 1 - c)] = on;
      }
    }
    out.push(g);
  }
  return out;
}

/** The 5 x 5 pattern of glyph n (row-major, 1 = lit). */
export function glyph(n) {
  return TABLE[(((n | 0) % COUNT) + COUNT) % COUNT];
}

/**
 * Draws glyph n at art pixel (x, y), each cell scale x scale.
 * @param {CanvasRenderingContext2D} ctx
 */
export function drawGlyph(ctx, n, x, y, color, scale = 1) {
  const g = glyph(n);
  ctx.fillStyle = color;
  for (let i = 0; i < g.length; i++) {
    if (g[i]) ctx.fillRect(x + (i % SIZE) * scale, y + Math.floor(i / SIZE) * scale, scale, scale);
  }
}

/**
 * Renders the 4 glyphs of a SAS into a canvas, sized to fit.
 * @param {HTMLCanvasElement} canvas
 * @param {number[]} sas
 */
export function renderSAS(canvas, sas, color, gap = 2, scale = 2) {
  const cell = SIZE * scale;
  canvas.width = sas.length * cell + (sas.length - 1) * gap * scale;
  canvas.height = cell;
  const ctx = canvas.getContext("2d");
  ctx.clearRect(0, 0, canvas.width, canvas.height);
  sas.forEach((n, i) => drawGlyph(ctx, n, i * (cell + gap * scale), 0, color, scale));
}
