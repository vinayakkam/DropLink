// Self-update in the UI: check (at start when enabled, or from CONFIG),
// offer the new version, download it through the sidecar (which verifies
// the signature), show progress in the status bar, then ask the desktop
// shell to install and restart.
import { api } from "../api.js";
import { bytes, percent } from "../format.js";
import { h, toast } from "./dom.js";
import { openModal } from "./modal.js";

/**
 * Text for the status bar while an update moves.
 * @param {{ phase: string, done?: number, total?: number, error?: string }} u
 * @param {string} version
 */
export function updateLabel(u, version) {
  switch (u?.phase) {
    case "downloading":
      return `UPDATE v${version} · ${percent(u.done ?? 0, u.total ?? 0)}%`;
    case "ready":
      return `UPDATE v${version} READY`;
    case "failed":
      return "UPDATE FAILED";
    default:
      return "";
  }
}

/**
 * @param {ReturnType<typeof import("../store.js").createStore>} store
 * @param {{ sfx?: (n: string) => void, invoke?: (cmd: string) => Promise<any> }} [opts]
 */
export function initUpdates(store, opts = {}) {
  const sfx = opts.sfx ?? (() => {});
  const invoke = opts.invoke ?? globalThis.__TAURI__?.core?.invoke;
  let offered = null; // the version being downloaded / installed
  let checkedAtStart = false;

  const label = () => document.getElementById("update-state");

  async function check({ quiet } = { quiet: false }) {
    if (!(store.get().engine.features ?? []).includes("update")) return null;
    let res;
    try {
      res = await api("/api/update");
    } catch (err) {
      if (!quiet) toast(`Update check failed: ${err.message}`, "error");
      return null;
    }
    store.set({ update: res });
    if (!res.available) {
      if (!quiet) toast(`DropLink v${res.current} is the latest version.`);
      return res;
    }
    offer(res);
    return res;
  }

  async function offer(res) {
    sfx("incoming");
    const canInstall = typeof invoke === "function";
    const answer = await openModal({
      title: "NEW VERSION AVAILABLE",
      body: [
        h("p", { class: "offer__from glow" }, `DropLink v${res.latest} is ready (you have v${res.current}).`),
        res.notes ? h("p", { class: "muted offer__names" }, res.notes) : null,
        h("p", { class: "faint" }, `${bytes(res.size)} · signed by the DropLink release key, checked before it runs.`),
        canInstall ? null : h("p", { class: "faint" }, "Install it from the desktop app."),
      ],
      actions: [
        { label: "LATER", value: "later", kind: "ghost" },
        ...(canInstall ? [{ label: "DOWNLOAD & INSTALL", value: "go", primary: true }] : []),
      ],
      cancelValue: "later",
    });
    if (answer !== "go") return;
    offered = res.latest;
    try {
      await api("/api/update/download", { method: "POST" });
      if (label()) label().textContent = updateLabel({ phase: "downloading", done: 0, total: res.size }, offered);
    } catch (err) {
      toast(err.message, "error");
    }
  }

  async function onEvent(ev) {
    if (ev.type !== "update" || !ev.update) return;
    const u = ev.update;
    if (label()) label().textContent = updateLabel(u, offered ?? "");
    if (u.phase === "failed") {
      sfx("error");
      toast(`Update failed: ${u.error}`, "error");
    }
    if (u.phase === "ready") {
      sfx("done");
      const go = await openModal({
        title: "UPDATE READY",
        body: [h("p", null, `DropLink v${offered} is downloaded and verified. Install now? DropLink restarts in a few seconds; transfers in flight resume afterwards.`)],
        actions: [
          { label: "LATER", value: false, kind: "ghost" },
          { label: "INSTALL & RESTART", value: true, primary: true },
        ],
        cancelValue: false,
      });
      if (go) {
        try {
          await invoke("install_update");
        } catch (err) {
          toast(String(err), "error");
        }
      }
    }
  }

  // Check once at start, when the setting allows it.
  store.select(
    (s) => s.settings,
    (s) => {
      if (!s || checkedAtStart || store.get().engine.state !== "online") return;
      checkedAtStart = true;
      if (s.autoUpdate) check({ quiet: true });
    },
  );

  return { check, onEvent };
}
