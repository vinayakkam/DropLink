// Custom title bar: the window has no OS frame (tauri.conf.json sets
// decorations: false), so these buttons drive it through Tauri's window
// API. Dragging uses data-tauri-drag-region, which Tauri handles itself.

/** @returns {any | null} the current Tauri window, or null in a browser */
export function tauriWindow() {
  const w = globalThis.__TAURI__?.window;
  return w?.getCurrentWindow ? w.getCurrentWindow() : null;
}

/**
 * Wires the minimize / maximize / close buttons inside root.
 * @param {ParentNode} root
 * @param {any} [win] injected in tests
 */
export function initTitlebar(root, win = tauriWindow()) {
  if (!win) return false;
  document.documentElement.dataset.tauri = "";
  const on = (sel, fn) => root.querySelector(sel)?.addEventListener("click", fn);
  on("[data-win=min]", () => win.minimize());
  on("[data-win=max]", () => win.toggleMaximize());
  on("[data-win=close]", () => win.close());
  return true;
}
