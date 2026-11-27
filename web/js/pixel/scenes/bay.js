// The docking bay: a cargo drone between two landing pads in the bottom
// band of the drop zone. Idle, it hovers. While transfers run, a beam
// joins the pads and the drone ferries data cubes across: its position
// and the cube stacks follow the overall progress. While files are dragged
// over the bay, the pads blink and tractor rings pulse.
import { fitCanvas } from "../canvas.js";
import { CUBE, DRONE, DRONE_ROLES, drawSprite } from "../sprites.js";

export const CUBES = 8;

/**
 * Cubes left at the source pad and delivered to the target for a
 * progress of 0..1.
 * @param {number} p
 */
export function cubeSplit(p) {
  const delivered = Math.max(0, Math.min(CUBES, Math.floor(p * CUBES + 1e-9)));
  return { left: CUBES - delivered, delivered };
}

/**
 * Drone x (art px) between the pads for progress p; it shuttles: each
 * cube is one trip out and back.
 * @param {number} p progress 0..1
 * @param {number} x0 left pad x  @param {number} x1 right pad x
 */
export function droneX(p, x0, x1) {
  const trip = (p * CUBES) % 1; // 0..1 within the current cube
  const out = trip < 0.5 ? trip * 2 : 2 - trip * 2; // there and back
  return x0 + (x1 - x0) * (p >= 1 ? 1 : out);
}

/**
 * @param {HTMLCanvasElement} canvas
 * @param {{
 *   palette: () => Record<string,string>,
 *   progress: () => number | null,   // null when nothing is moving
 *   hover: () => boolean,
 * }} opts
 */
export function bay(canvas, opts) {
  let t = 0;
  let frame = 0;

  function pad(ctx, pal, x, y, lit) {
    ctx.fillStyle = pal.e;
    ctx.fillRect(x - 10, y, 20, 2);
    ctx.fillStyle = pal.C;
    ctx.fillRect(x - 8, y + 2, 16, 1);
    ctx.fillStyle = lit ? pal.a : pal.A;
    ctx.fillRect(x - 10, y - 1, 2, 1);
    ctx.fillRect(x + 8, y - 1, 2, 1);
  }

  function stack(ctx, pal, x, y, n) {
    for (let i = 0; i < n; i++) {
      const col = i % 4;
      const row = Math.floor(i / 4);
      drawSprite(ctx, CUBE, x - 9 + col * 5, y - 5 - row * 5, pal);
    }
  }

  return {
    resize() {
      fitCanvas(canvas);
    },
    update(dt) {
      t += dt;
      frame = Math.floor(t * 8) % 2;
    },
    draw() {
      const ctx = canvas.getContext("2d");
      const w = canvas.width;
      const h = canvas.height;
      ctx.clearRect(0, 0, w, h);
      if (w < 80 || h < 60) return;
      const pal = opts.palette();
      const p = opts.progress();
      const hover = opts.hover();
      const ground = h - 14;
      const x0 = Math.floor(w * 0.2);
      const x1 = Math.floor(w * 0.8);
      const blink = Math.floor(t * 3) % 2 === 0;

      pad(ctx, pal, x0, ground, hover ? blink : true);
      pad(ctx, pal, x1, ground, hover ? !blink : p !== null);

      if (hover) {
        // Tractor rings rising from both pads.
        ctx.fillStyle = pal.c;
        for (const x of [x0, x1]) {
          for (let k = 0; k < 3; k++) {
            const r = ((t * 12 + k * 6) % 18) | 0;
            ctx.globalAlpha = 1 - r / 18;
            ctx.fillRect(x - r, ground - 4 - r, r * 2, 1);
          }
        }
        ctx.globalAlpha = 1;
      }

      let dx = (x0 + x1) / 2;
      let dy = ground - 22 + Math.round(Math.sin(t * 2) * 2); // idle hover
      if (p !== null) {
        const { left, delivered } = cubeSplit(p);
        stack(ctx, pal, x0, ground, left);
        stack(ctx, pal, x1, ground, delivered);
        // Beam: dashes scrolling toward the target.
        ctx.fillStyle = pal.C;
        const shift = Math.floor(t * 20) % 6;
        for (let x = x0 + 12 + shift; x < x1 - 12; x += 6) ctx.fillRect(x, ground - 30, 3, 1);
        dx = droneX(p, x0, x1);
        dy = ground - 26 + Math.round(Math.sin(t * 6));
        if (p < 1) drawSprite(ctx, CUBE, Math.round(dx) - 2, dy + 9, pal); // cargo in tow
      }
      const sprite = DRONE[frame];
      drawSprite(ctx, sprite, Math.round(dx - sprite.w / 2), dy, pal, DRONE_ROLES);
    },
  };
}
