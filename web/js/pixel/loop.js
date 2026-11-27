// One animation loop for every scene, built to cost nothing when nobody
// is looking:
//   - capped at `fps` (30): frames that come early are skipped;
//   - paused while the document is hidden;
//   - paused after `idleMs` (60 s) with no input, until the next input
//     (PrivMeet's "stillness" idea); <html data-still> is set meanwhile so
//     CSS animations stop too;
//   - with reduced motion, scenes are drawn once per change (invalidate),
//     never animated.

/**
 * @typedef {{ update?: (dt: number, t: number) => void, draw: () => void }} Scene
 * @param {{
 *   fps?: number, idleMs?: number,
 *   now?: () => number,
 *   raf?: (cb: (t: number) => void) => number, caf?: (id: number) => void,
 *   doc?: any, win?: any, reduced?: () => boolean,
 * }} [opts]
 */
export function createLoop(opts = {}) {
  const fps = opts.fps ?? 30;
  const idleMs = opts.idleMs ?? 60_000;
  const now = opts.now ?? (() => performance.now());
  const raf = opts.raf ?? ((cb) => requestAnimationFrame(cb));
  const caf = opts.caf ?? ((id) => cancelAnimationFrame(id));
  const doc = opts.doc ?? globalThis.document;
  const win = opts.win ?? globalThis.window;
  const reduced = opts.reduced ?? (() => false);
  const frameMs = 1000 / fps;

  /** @type {Set<Scene>} */
  const scenes = new Set();
  let handle = 0;
  let last = 0;
  let lastInput = now();
  let running = false;
  let frames = 0;

  const still = () => (doc?.hidden ?? false) || now() - lastInput > idleMs;

  function setStillFlag(on) {
    const root = doc?.documentElement;
    if (!root) return;
    if (on) root.dataset.still = "";
    else delete root.dataset.still;
  }

  function drawAll() {
    for (const s of scenes) s.draw();
    frames++;
  }

  function tick(t) {
    handle = 0;
    if (!running) return;
    if (still() || reduced()) {
      setStillFlag(true);
      return; // stop scheduling; input or visibility restarts us
    }
    if (t - last >= frameMs - 1) {
      const dt = last ? Math.min(t - last, 250) / 1000 : 0;
      last = t;
      for (const s of scenes) s.update?.(dt, t / 1000);
      drawAll();
    }
    handle = raf(tick);
  }

  function wake() {
    lastInput = now();
    if (!running || handle) return;
    if (reduced()) return;
    setStillFlag(false);
    last = 0;
    handle = raf(tick);
  }

  const onInput = () => wake();
  const onVisibility = () => (doc?.hidden ? undefined : wake());
  const inputs = ["pointermove", "pointerdown", "keydown", "wheel", "dragenter"];

  return {
    /** @param {Scene} s */
    add(s) {
      scenes.add(s);
      s.draw();
      return () => scenes.delete(s);
    },
    start() {
      if (running) return;
      running = true;
      for (const e of inputs) win?.addEventListener?.(e, onInput, { passive: true });
      doc?.addEventListener?.("visibilitychange", onVisibility);
      wake();
    },
    stop() {
      running = false;
      if (handle) caf(handle);
      handle = 0;
      for (const e of inputs) win?.removeEventListener?.(e, onInput);
      doc?.removeEventListener?.("visibilitychange", onVisibility);
    },
    /** Redraw now (state changed); also counts as activity. */
    invalidate() {
      drawAll();
      wake();
    },
    /** For tests and the debug overlay. */
    stats: () => ({ frames, running: !!handle }),
  };
}
