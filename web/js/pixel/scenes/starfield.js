// Deep-space backdrop. The nebula and far stars are drawn once into an
// offscreen layer (redrawn only on resize or theme change); per frame only
// a few dozen near stars drift and twinkle on top.
import { dither, fitCanvas, offscreen, rng } from "../canvas.js";

/**
 * @param {HTMLCanvasElement} canvas
 * @param {{ palette: () => Record<string,string>, seed?: number, near?: number }} opts
 */
export function starfield(canvas, opts) {
  const seed = opts.seed ?? 7;
  const nearCount = opts.near ?? 36;
  let layer = null;
  let stars = [];
  let w = 0;
  let h = 0;
  let t = 0;

  function rebuild() {
    w = canvas.width;
    h = canvas.height;
    const pal = opts.palette();
    const off = offscreen(w, h);
    const ctx = off.ctx;
    ctx.fillStyle = pal.k;
    ctx.fillRect(0, 0, w, h);
    const r = rng(seed);
    // Two nebula clouds, dithered so they stay pixel art.
    const clouds = [
      { x: w * 0.78, y: h * 0.22, rad: Math.max(w, h) * 0.35, color: pal.d },
      { x: w * 0.15, y: h * 0.85, rad: Math.max(w, h) * 0.3, color: pal.M },
    ];
    for (const c of clouds) {
      ctx.fillStyle = c.color;
      const x0 = Math.max(0, Math.floor(c.x - c.rad));
      const y0 = Math.max(0, Math.floor(c.y - c.rad));
      for (let y = y0; y < Math.min(h, c.y + c.rad); y++) {
        for (let x = x0; x < Math.min(w, c.x + c.rad); x++) {
          const d = Math.hypot(x - c.x, y - c.y) / c.rad;
          if (d < 1 && dither(x, y, (1 - d) * 0.35)) ctx.fillRect(x, y, 1, 1);
        }
      }
    }
    // Far stars: fixed, faint.
    for (let i = 0; i < (w * h) / 180; i++) {
      ctx.fillStyle = r() < 0.8 ? pal.f : pal.i;
      ctx.fillRect(Math.floor(r() * w), Math.floor(r() * h), 1, 1);
    }
    layer = off.canvas;
    stars = Array.from({ length: nearCount }, () => ({
      x: r() * w,
      y: r() * h,
      v: 1 + r() * 4, // art px per second
      c: r() < 0.7 ? pal.w : r() < 0.5 ? pal.c : pal.a,
      phase: r() * Math.PI * 2,
    }));
  }

  return {
    resize() {
      if (fitCanvas(canvas) || !layer) rebuild();
    },
    retheme: () => rebuild(),
    update(dt) {
      t += dt;
      for (const s of stars) {
        s.x -= s.v * dt;
        if (s.x < 0) s.x += w;
      }
    },
    draw() {
      if (!layer) this.resize();
      const ctx = canvas.getContext("2d");
      ctx.drawImage(layer, 0, 0);
      for (const s of stars) {
        const tw = Math.sin(t * 2 + s.phase);
        if (tw < -0.8) continue; // twinkle off
        ctx.fillStyle = s.c;
        ctx.fillRect(Math.floor(s.x), Math.floor(s.y), 1, 1);
        if (tw > 0.9 && s.v > 3.5) {
          // the brightest near stars get a tiny cross
          ctx.fillRect(Math.floor(s.x) - 1, Math.floor(s.y), 3, 1);
          ctx.fillRect(Math.floor(s.x), Math.floor(s.y) - 1, 1, 3);
        }
      }
    },
  };
}
