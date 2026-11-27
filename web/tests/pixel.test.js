import assert from "node:assert/strict";
import { test } from "node:test";
import { artSize, dither, hash, rng } from "../js/pixel/canvas.js";
import { COUNT, SIZE, glyph, renderSAS } from "../js/pixel/glyphs.js";
import { createLoop } from "../js/pixel/loop.js";
import { overallProgress } from "../js/pixel/mount.js";
import { FALLBACK, KEYS } from "../js/pixel/palette.js";
import { CUBES, cubeSplit, droneX } from "../js/pixel/scenes/bay.js";
import { angleDiff, placeOf } from "../js/pixel/scenes/radar.js";
import { CUBE, DRONE, HULLS, drawSprite, parseSprite, ship } from "../js/pixel/sprites.js";

// A fake clock and frame source for the loop.
function harness({ hidden = false, reduced = false, idleMs = 1000 } = {}) {
  let t = 0;
  const queue = [];
  const listeners = {};
  const doc = {
    hidden,
    documentElement: { dataset: {} },
    addEventListener: (e, fn) => (listeners[e] = fn),
    removeEventListener: (e) => delete listeners[e],
  };
  const win = {
    addEventListener: (e, fn) => (listeners[e] = fn),
    removeEventListener: (e) => delete listeners[e],
  };
  const loop = createLoop({
    fps: 30,
    idleMs,
    now: () => t,
    raf: (cb) => (queue.push(cb), queue.length),
    caf: () => queue.splice(0),
    doc,
    win,
    reduced: () => reduced,
  });
  let draws = 0;
  let updates = 0;
  loop.add({ update: () => updates++, draw: () => draws++ });
  // Runs the frame source at 120 Hz for ms milliseconds.
  const run = (ms) => {
    const end = t + ms;
    while (t < end) {
      t += 1000 / 120;
      const cb = queue.shift();
      if (cb) cb(t);
    }
  };
  return { loop, doc, listeners, run, counts: () => ({ draws, updates }), pending: () => queue.length };
}

test("the loop is capped at 30 fps", () => {
  const h = harness({ idleMs: 10_000 });
  h.loop.start();
  h.run(1000);
  const { draws, updates } = h.counts();
  assert.ok(draws >= 30 && draws <= 32, `draws ${draws}`); // 1 initial + ~30
  assert.ok(updates >= 29 && updates <= 31, `updates ${updates}`);
});

test("the loop stops scheduling while hidden and resumes when visible", () => {
  const h = harness({ hidden: true, idleMs: 10_000 });
  h.loop.start();
  h.run(500);
  assert.equal(h.counts().updates, 0);
  assert.equal(h.pending(), 0, "no frames scheduled while hidden");
  assert.equal(h.doc.documentElement.dataset.still, "");
  h.doc.hidden = false;
  h.listeners.visibilitychange();
  h.run(500);
  assert.ok(h.counts().updates > 10);
  assert.equal(h.doc.documentElement.dataset.still, undefined);
});

test("the loop goes still after the idle time and wakes on input", () => {
  const h = harness({ idleMs: 300 });
  h.loop.start();
  h.run(1000);
  const afterIdle = h.counts().updates;
  assert.ok(afterIdle < 15, `kept animating while idle: ${afterIdle}`);
  assert.equal(h.pending(), 0);
  h.run(1000);
  assert.equal(h.counts().updates, afterIdle);
  h.listeners.pointermove();
  h.run(200);
  assert.ok(h.counts().updates > afterIdle);
});

test("reduced motion: no animation, but invalidate draws", () => {
  const h = harness({ reduced: true, idleMs: 10_000 });
  h.loop.start();
  h.run(1000);
  assert.equal(h.counts().updates, 0);
  const before = h.counts().draws;
  h.loop.invalidate();
  assert.equal(h.counts().draws, before + 1);
});

test("stop cancels frames and listeners", () => {
  const h = harness({ idleMs: 10_000 });
  h.loop.start();
  h.run(100);
  h.loop.stop();
  const n = h.counts().updates;
  h.run(500);
  assert.equal(h.counts().updates, n);
  assert.equal(h.listeners.pointermove, undefined);
});

function fakeCtx() {
  const calls = [];
  return {
    calls,
    fillStyle: "",
    globalAlpha: 1,
    fillRect(x, y, w, h) {
      calls.push([this.fillStyle, x, y, w, h]);
    },
    clearRect() {},
    drawImage() {},
  };
}

test("sprites parse, reject ragged rows and draw runs as one rect", () => {
  assert.throws(() => parseSprite(["ab", "a"]), /wide/);
  assert.throws(() => parseSprite([]), /no rows/);
  const s = parseSprite(["ccc.a", "....."]);
  const ctx = fakeCtx();
  drawSprite(ctx, s, 10, 20, { c: "#0ff", a: "#fa0" });
  assert.deepEqual(ctx.calls, [
    ["#0ff", 10, 20, 3, 1],
    ["#fa0", 14, 20, 1, 1],
  ]);
});

test("role letters map to palette colors", () => {
  const ctx = fakeCtx();
  drawSprite(ctx, parseSprite(["h"]), 0, 0, FALLBACK, { h: "m" });
  assert.equal(ctx.calls[0][0], FALLBACK.m);
});

test("16 ship avatars: 4 hulls x 4 liveries, all 9 x 9", () => {
  for (const hull of HULLS) assert.deepEqual([hull.w, hull.h], [9, 9]);
  assert.equal(ship(0).roles.h, "c");
  assert.equal(ship(5).roles.h, "m");
  assert.equal(ship(5).sprite, HULLS[1]);
  assert.equal(ship(15).roles.h, "l");
  assert.equal(ship(16).sprite, ship(0).sprite, "avatars wrap at 16");
  assert.equal(DRONE[0].w, DRONE[1].w);
  assert.equal(DRONE[0].h, DRONE[1].h);
  assert.deepEqual([CUBE.w, CUBE.h], [4, 4]);
});

test("every sprite letter has a palette color", () => {
  const roles = new Set(["h", "s", "t", "T", "."]);
  for (const s of [...HULLS, ...DRONE, CUBE]) {
    for (const row of s.rows) {
      for (const ch of row) assert.ok(roles.has(ch) || ch in KEYS, `unknown color ${ch}`);
    }
  }
  for (const k of Object.keys(KEYS)) assert.match(FALLBACK[k], /^#[0-9a-f]{6}$/);
});

test("64 distinct, mirror-symmetric SAS glyphs", () => {
  const seen = new Set();
  for (let n = 0; n < COUNT; n++) {
    const g = glyph(n);
    assert.equal(g.length, SIZE * SIZE);
    for (let r = 0; r < SIZE; r++) {
      for (let c = 0; c < SIZE; c++) assert.equal(g[r * SIZE + c], g[r * SIZE + SIZE - 1 - c]);
    }
    const lit = g.reduce((a, b) => a + b, 0);
    assert.ok(lit >= 8 && lit <= 20, `glyph ${n} has ${lit} lit cells`);
    seen.add(g.join(""));
  }
  assert.equal(seen.size, COUNT);
  assert.equal(glyph(64), glyph(0));
  assert.equal(glyph(-1), glyph(63));
});

test("renderSAS sizes its canvas for 4 glyphs", () => {
  const ctx = fakeCtx();
  const canvas = { width: 0, height: 0, getContext: () => ctx };
  renderSAS(canvas, [1, 2, 3, 4], "#fff");
  assert.deepEqual([canvas.width, canvas.height], [4 * 10 + 3 * 4, 10]);
  assert.ok(ctx.calls.length > 20);
});

test("peers keep their place on the radar", () => {
  const a = placeOf("a1b2c3d4e5f60718293a4b5c6d7e8f90", 50);
  assert.deepEqual(placeOf("a1b2c3d4e5f60718293a4b5c6d7e8f90", 50), a);
  for (const id of ["x", "y", "zz", "0123456789abcdef0123456789abcdef"]) {
    const p = placeOf(id, 50);
    const d = Math.hypot(p.x, p.y);
    assert.ok(d >= 17 && d <= 43, `distance ${d}`);
  }
  assert.ok(Math.abs(angleDiff(0.1, Math.PI * 2 - 0.1) - 0.2) < 1e-9);
  assert.ok(Math.abs(angleDiff(1, 2) - 1) < 1e-9);
});

test("cargo cubes and the drone follow progress", () => {
  assert.deepEqual(cubeSplit(0), { left: CUBES, delivered: 0 });
  assert.deepEqual(cubeSplit(0.5), { left: 4, delivered: 4 });
  assert.deepEqual(cubeSplit(1), { left: 0, delivered: CUBES });
  assert.deepEqual(cubeSplit(2), { left: 0, delivered: CUBES });
  for (let p = 0; p < 1; p += 0.01) {
    const x = droneX(p, 10, 90);
    assert.ok(x >= 10 && x <= 90);
  }
  assert.equal(droneX(1, 10, 90), 90);
});

test("overall progress counts only moving transfers", () => {
  assert.equal(overallProgress([]), null);
  assert.equal(overallProgress([{ state: "done", done: 5, total: 5 }]), null);
  assert.equal(
    overallProgress([
      { state: "active", done: 25, total: 100 },
      { state: "paused", done: 25, total: 100 },
      { state: "done", done: 999, total: 999 },
    ]),
    0.25,
  );
  assert.equal(overallProgress([{ state: "active", done: 0, total: 0 }]), 1);
});

test("canvas helpers", () => {
  assert.deepEqual(artSize(300, 201), { w: 150, h: 100 });
  assert.deepEqual(artSize(0, 0), { w: 1, h: 1 });
  const a = rng(42);
  const b = rng(42);
  assert.equal(a(), b());
  assert.equal(hash("abc"), hash("abc"));
  assert.notEqual(hash("abc"), hash("abd"));
  let lit = 0;
  for (let y = 0; y < 4; y++) for (let x = 0; x < 4; x++) lit += dither(x, y, 0.5) ? 1 : 0;
  assert.equal(lit, 8, "50 % coverage lights half of a 4x4 block");
});
