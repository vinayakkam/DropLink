// Desktop-only behaviour, active when the UI runs inside the Tauri shell:
// tray and close-to-tray settings, native notifications. In a plain
// browser (task web) none of this runs.
import { api } from "./api.js";
import { shortId } from "./format.js";

/** The Tauri global, or null in a browser. */
export const tauri = () => globalThis.__TAURI__ ?? null;

/**
 * Which notification an event deserves, if any. Offers always notify
 * (someone is waiting); finished transfers only when the window is not
 * focused, so nobody is pinged about what they are watching.
 * @param {any} ev  @param {boolean} focused
 * @returns {{ title: string, body: string } | null}
 */
export function notificationFor(ev, focused) {
  const t = ev?.type === "transfer" ? ev.transfer : null;
  if (!t) return null;
  const who = t.peer?.name || shortId(t.peer?.id);
  const what = t.files ? `${t.files} file${t.files === 1 ? "" : "s"}` : "a text message";
  if (t.waiting && !t.outgoing) return { title: "Incoming cargo", body: `${who} wants to send ${what}.` };
  if (focused) return null;
  if (t.state === "done") return { title: t.outgoing ? "Cargo delivered" : "Cargo received", body: `${what} ${t.outgoing ? "to" : "from"} ${who}.` };
  if (t.state === "failed") return { title: "Transfer failed", body: `${who}: ${t.error || "the link was lost"}` };
  return null;
}

/**
 * Normalizes what the dialog plugin returns (null, a path, or a list).
 * @param {null | string | string[]} r
 * @returns {string[] | null}
 */
export function toPaths(r) {
  if (!r) return null;
  const list = (Array.isArray(r) ? r : [r]).map((p) => (typeof p === "string" ? p : p?.path)).filter(Boolean);
  return list.length ? list : null;
}

/**
 * Turns a webview drag-drop event into a store change: hover on enter /
 * over, off on leave, and the dropped paths on drop.
 * @param {{ type: string, paths?: string[] }} payload
 * @returns {{ hover: boolean, paths: string[] | null }}
 */
export function dropAction(payload) {
  switch (payload?.type) {
    case "enter":
    case "over":
      return { hover: true, paths: null };
    case "drop":
      return { hover: false, paths: payload.paths?.length ? payload.paths : null };
    default:
      return { hover: false, paths: null };
  }
}

/**
 * @param {ReturnType<typeof import("./store.js").createStore>} store
 * @param {{ setPickers?: (p: any) => void, setCargo?: (c: any) => void, pagesOpts?: any, joinRoom?: (code: string) => Promise<void> }} [ui]
 */
export function initDesktop(store, ui = {}) {
  const T = tauri();
  if (!T) return { onEvent: () => {} };

  // Native pickers and clipboard replace the browser fallbacks.
  ui.setPickers?.({
    files: async () => toPaths(await T.dialog.open({ multiple: true, directory: false, title: "Pick cargo" })),
    folder: async () => toPaths(await T.dialog.open({ multiple: true, directory: true, title: "Pick folders" })),
    clipboard: async () => {
      const text = await T.clipboardManager.readText().catch(() => "");
      return text && text.trim() ? text : null;
    },
    copy: (text) => T.clipboardManager.writeText(text),
  });
  if (ui.pagesOpts) {
    ui.pagesOpts.browseFolder = async () => toPaths(await T.dialog.open({ directory: true, multiple: false, title: "Download folder" }))?.[0] ?? null;
  }

  // Files dragged onto the window arrive with real paths (folders too).
  T.webview?.getCurrentWebview?.().onDragDropEvent((e) => {
    const a = dropAction(e.payload);
    if (store.get().dropHover !== a.hover) store.set({ dropHover: a.hover });
    if (a.paths) {
      if (store.get().route.view !== "home") globalThis.location.hash = "#/";
      ui.setCargo?.({ paths: a.paths });
    }
  });

  // droplink://join/<CODE>: one that started the app, then any later ones.
  const joinFromLink = (code) => code && ui.joinRoom?.(code);
  T.core.invoke("take_pending_join").then(joinFromLink).catch(() => {});
  T.event?.listen("deep-link-join", (e) => {
    T.core.invoke("take_pending_join").catch(() => {}); // consumed here
    joinFromLink(e.payload);
  });

  // Settings the shell needs: close-to-tray and the tray's check mark.
  store.select(
    (s) => s.settings,
    (s) => {
      if (!s) return;
      T.core.invoke("sync_window_settings", { closeToTray: !!s.closeToTray, autoAccept: !!s.autoAcceptTrusted }).catch(() => {});
    },
  );
  // The tray's auto-accept toggle saves through the API like the CONFIG page.
  T.event?.listen("tray-auto-accept", async (e) => {
    try {
      const s = await api("/api/settings", { method: "PUT", body: { autoAcceptTrusted: !!e.payload } });
      store.set({ settings: s });
    } catch {
      // the next settings load corrects the tray
    }
  });

  const shown = new Set(); // one notification per transfer and state
  let allowed = null;
  async function notify(n, key) {
    if (shown.has(key)) return;
    shown.add(key);
    const N = T.notification;
    if (!N) return;
    allowed ??= (await N.isPermissionGranted()) || (await N.requestPermission()) === "granted";
    if (allowed) N.sendNotification(n);
  }

  return {
    onEvent(ev) {
      const n = notificationFor(ev, globalThis.document?.hasFocus?.() ?? true);
      if (n) notify(n, `${ev.transfer.id}:${ev.transfer.waiting ? "offer" : ev.transfer.state}`);
    },
  };
}
