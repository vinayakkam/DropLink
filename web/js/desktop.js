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
 * @param {ReturnType<typeof import("./store.js").createStore>} store
 */
export function initDesktop(store) {
  const T = tauri();
  if (!T) return { onEvent: () => {} };

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
