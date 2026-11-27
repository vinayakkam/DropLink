// The radar: rings and crosshair drawn once to an offscreen layer; per
// frame a pixel sweep line with a fading trail, and one ship per peer.
// A peer's position comes from a hash of its ID, so it stays put across
// restarts and on every device.
import { fitCanvas, hash, offscreen } from "../canvas.js";
import { drawSprite, ship } from "../sprites.js";

const SWEEP_SPEED = 1.1; // radians per second
const TRAIL = 14;

/**
 * Where a peer sits on a radar of radius R around (0, 0).
 * @param {string} id
 * @param {number} R
 */
export function placeOf(id, R) {
  const h = hash(String(id));
  const angle = ((h % 3600) / 3600) * Math.PI * 2;
  const dist = R * (0.35 + (((h >>> 12) % 1000) / 1000) * 0.5);
  return { x: Math.cos(angle) * dist, y: Math.sin(angle) * dist, angle };
}

/** Smallest absolute difference between two angles. */
export function angleDiff(a, b) {
  const d = Math.abs(a - b) % (Math.PI * 2);
  return d > Math.PI ? Math.PI * 2 - d : d;
}

/**
 * @param {HTMLCanvasElement} canvas
 * @param {{
 *   palette: () => Record<string,string>,
 *   peers: () => Array<{ id: string, avatar?: number }>,
 *   selected: () => Set<string>,
 * }} opts
 */
export function radar(canvas, opts) {
  let layer = null;
  let sweep = 0;
  let cx = 0;
  let cy = 0;
  let R = 0;
  let pal = opts.palette();

  function plot(ctx, x, y) {
    ctx.fillRect(Math.round(x), Math.round(y), 1, 1);
  }

  function rebuild() {
    pal = opts.palette();
    const w = canvas.width;
    const h = canvas.height;
    cx = Math.floor(w / 2);
    cy = Math.floor(h / 2);
    R = Math.floor(Math.min(w, h) / 2) - 2;
    const { canvas: c, ctx } = offscreen(w, h);
    // Rings: plotted point by point so they stay one art pixel wide.
    for (const [frac, color] of [[1, pal.C], [0.66, pal.e], [0.33, pal.e]]) {
      ctx.fillStyle = color;
      const r = R * frac;
      const steps = Math.ceil(r * 8);
      for (let i = 0; i < steps; i++) {
        const a = (i / steps) * Math.PI * 2;
        plot(ctx, cx + Math.cos(a) * r, cy + Math.sin(a) * r);
      }
    }
    // Crosshair, dashed.
    ctx.fillStyle = pal.e;
    for (let d = -R; d <= R; d += 3) {
      plot(ctx, cx + d, cy);
      plot(ctx, cx, cy + d);
    }
    // Ticks every 30 degrees.
    ctx.fillStyle = pal.C;
    for (let i = 0; i < 12; i++) {
      const a = (i / 12) * Math.PI * 2;
      for (let k = 0; k < 3; k++) plot(ctx, cx + Math.cos(a) * (R - k), cy + Math.sin(a) * (R - k));
    }
    // Our own ship marker at the center.
    ctx.fillStyle = pal.c;
    ctx.fillRect(cx - 1, cy - 1, 3, 3);
    layer = c;
  }

  function brackets(ctx, x, y, size) {
    const s = size;
    ctx.fillStyle = pal.c;
    for (const [dx, dy, sx, sy] of [[-1, -1, 1, 1], [1, -1, -1, 1], [-1, 1, 1, -1], [1, 1, -1, -1]]) {
      const bx = x + dx * s;
      const by = y + dy * s;
      ctx.fillRect(bx, by, 1, 1);
      ctx.fillRect(bx + sx, by, 1, 1);
      ctx.fillRect(bx, by + sy, 1, 1);
    }
  }

  return {
    resize() {
      if (fitCanvas(canvas) || !layer) rebuild();
    },
    retheme: () => rebuild(),
    update(dt) {
      sweep = (sweep + dt * SWEEP_SPEED) % (Math.PI * 2);
    },
    draw() {
      if (!layer) this.resize();
      const ctx = canvas.getContext("2d");
      ctx.clearRect(0, 0, canvas.width, canvas.height);
      ctx.drawImage(layer, 0, 0);

      // Sweep with a fading trail.
      ctx.fillStyle = pal.c;
      for (let k = TRAIL - 1; k >= 0; k--) {
        ctx.globalAlpha = k === 0 ? 0.9 : 0.35 * (1 - k / TRAIL);
        const a = sweep - k * 0.045;
        const ca = Math.cos(a);
        const sa = Math.sin(a);
        for (let r = 2; r < R; r++) plot(ctx, cx + ca * r, cy + sa * r);
      }
      ctx.globalAlpha = 1;

      const sel = opts.selected();
      for (const p of opts.peers()) {
        const pos = placeOf(p.id, R);
        const { sprite, roles } = ship(p.avatar ?? 0);
        const x = Math.round(cx + pos.x - sprite.w / 2);
        const y = Math.round(cy + pos.y - sprite.h / 2);
        // A blip flares as the sweep passes over it.
        const lit = angleDiff(sweep, pos.angle) < 0.35;
        if (lit) {
          ctx.globalAlpha = 0.35;
          ctx.fillStyle = pal.c;
          ctx.fillRect(x - 1, y - 1, sprite.w + 2, sprite.h + 2);
          ctx.globalAlpha = 1;
        }
        drawSprite(ctx, sprite, x, y, pal, roles);
        if (sel.has(p.id)) brackets(ctx, Math.round(cx + pos.x), Math.round(cy + pos.y), Math.ceil(sprite.w / 2) + 2);
      }
    },
  };
}
