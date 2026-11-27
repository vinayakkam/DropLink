// Mounts the pixel scenes into the page and keeps them in step with the
// store: one loop, canvases resized by ResizeObserver, palettes re-read
// on theme change, redraws on state changes even while the loop sleeps.
import { reducedMotion } from "../bootseq.js";
import { createLoop } from "./loop.js";
import { readPalette } from "./palette.js";
import { bay } from "./scenes/bay.js";
import { radar } from "./scenes/radar.js";
import { starfield } from "./scenes/starfield.js";

/**
 * @param {Document} doc
 * @param {ReturnType<typeof import("../store.js").createStore>} store
 */
export function mountScenes(doc, store) {
  let palette = readPalette(doc.documentElement);
  const getPalette = () => palette;
  const loop = createLoop({ reduced: () => reducedMotion() });
  /** @type {Array<{ el: HTMLCanvasElement, scene: any }>} */
  const mounted = [];

  function mount(id, make) {
    const el = /** @type {HTMLCanvasElement | null} */ (doc.getElementById(id));
    if (!el) return null;
    const scene = make(el);
    scene.resize();
    mounted.push({ el, scene });
    loop.add(scene);
    return scene;
  }

  mount("stars", (el) => starfield(el, { palette: getPalette }));
  mount("radar-canvas", (el) =>
    radar(el, {
      palette: getPalette,
      peers: () => store.get().peers ?? [],
      selected: () => store.get().selected ?? new Set(),
    }),
  );

  mount("bay-canvas", (el) =>
    bay(el, {
      palette: getPalette,
      progress: () => overallProgress(store.get().transfers ?? []),
      hover: () => !!store.get().dropHover,
    }),
  );

  const ro = new ResizeObserver(() => {
    for (const m of mounted) m.scene.resize();
    loop.invalidate();
  });
  for (const m of mounted) ro.observe(m.el);

  // Theme switches change CSS variables: re-read and redraw static layers.
  const mo = new MutationObserver(() => {
    palette = readPalette(doc.documentElement);
    for (const m of mounted) m.scene.retheme?.();
    loop.invalidate();
  });
  mo.observe(doc.documentElement, { attributes: true, attributeFilter: ["data-theme"] });

  store.subscribe((s, prev) => {
    if (s.peers !== prev.peers || s.selected !== prev.selected || s.transfers !== prev.transfers || s.dropHover !== prev.dropHover) {
      loop.invalidate();
    }
  });

  loop.start();
  return { loop, mount };
}

/**
 * Overall progress of the transfers that are moving, 0..1, or null when
 * none is (the bay drone then idles).
 * @param {Array<{ state: string, done: number, total: number }>} transfers
 */
export function overallProgress(transfers) {
  const moving = transfers.filter((x) => x.state === "active" || x.state === "paused");
  if (!moving.length) return null;
  let done = 0;
  let total = 0;
  for (const x of moving) {
    done += x.done;
    total += x.total;
  }
  return total > 0 ? Math.min(1, done / total) : 1;
}
