// Entry point: boot sequence, engine connection, routing and the status
// bar. Feature modules (radar, bay, cargo log, pages) subscribe to the
// same store.
import { api, connect } from "./api.js";
import { reducedMotion, runBoot } from "./bootseq.js";
import { initDesktop } from "./desktop.js";
import { connectEvents } from "./events.js";
import { mountScenes } from "./pixel/mount.js";
import { createStore } from "./store.js";
import { initTitlebar } from "./titlebar.js";
import { createSfx } from "./sfx.js";
import { applyAppearance, watchSystemAppearance } from "./theme.js";
import { mountHome, setPickers } from "./ui/home.js";
import { mountPages } from "./ui/pages.js";
import { initUpdates } from "./ui/update.js";
import { href, showView, watchRoute } from "./views.js";
import { SHORTCUTS, bindShortcuts } from "./shortcuts.js";
import { h } from "./ui/dom.js";
import { openModal } from "./ui/modal.js";

export const store = createStore({
  engine: { state: "starting", version: "", device: "", features: [] },
  route: { view: "home", query: {} },
  peers: [],
  selected: new Set(),
  transfers: [],
  dropHover: false,
  room: null,
  link: "down",
  settings: null,
});

const sfx = createSfx(() => store.get().settings?.volume ?? 0);

initTitlebar(document);
watchRoute(window, store);
store.select((s) => s.route, (route) => showView(document, route));

store.select(
  (s) => s.engine,
  (engine) => {
    const led = document.getElementById("engine-led");
    led.dataset.state = engine.state === "online" ? "online" : engine.state === "offline" ? "offline" : "";
    document.getElementById("engine-state").textContent =
      engine.state === "online" ? "ENGINE ONLINE" : engine.state === "offline" ? "ENGINE OFFLINE" : "ENGINE STARTING";
    if (engine.version) document.getElementById("version").textContent = `v${engine.version}`;
    if (engine.device) document.getElementById("sector").textContent = `SECTOR: ${engine.device.toUpperCase()}`;
  },
);

// While the appearance is "auto", follow the OS light/dark switch live.
watchSystemAppearance(() => store.get().settings);

mountScenes(document, store);
export const home = mountHome(document, store, { sfx });
const pagesOpts = { sfx, applySettings: applyAppearance };
export const pages = mountPages(document, store, pagesOpts);
const updates = initUpdates(store, { sfx });
pagesOpts.checkUpdates = () => updates.check();
const desktop = initDesktop(store, { setPickers, setCargo: home.setCargo, pagesOpts, joinRoom: (code) => pages.joinRoom(code) });

// Keyboard shortcuts. A click on the matching control reuses its flow, so
// nothing is duplicated; navigation just moves the hash.
const go = (view) => () => (window.location.hash = href(view));
const click = (id) => () => document.getElementById(id)?.click();
let helpOpen = false;
async function showHelp() {
  if (helpOpen) return;
  helpOpen = true;
  await openModal({
    title: "KEYBOARD SHORTCUTS",
    body: [h("ul", { class: "list" }, ...SHORTCUTS.map((s) => h("li", { class: "list__item" }, h("span", { class: "tag" }, s.keys), h("span", { class: "list__title" }, s.does))))],
    actions: [{ label: "CLOSE", value: true, primary: true }],
    cancelValue: true,
  });
  helpOpen = false;
}
bindShortcuts(document, window, {
  "view:home": go("home"),
  "view:rooms": go("rooms"),
  "view:history": go("history"),
  "view:settings": go("settings"),
  "pick-files": click("pick-files"),
  "pick-folder": click("pick-folder"),
  "send-clip": click("send-clip"),
  "launch": click("launch"),
  "clear": click("manifest-clear"),
  "help": showHelp,
});

const bootEl = document.getElementById("boot");
const boot = runBoot(document.getElementById("boot-log"), { instant: reducedMotion() });

try {
  await connect();
  const info = await api("/api/info");
  store.set({ engine: { state: "online", version: info.version, device: info.device, features: info.features ?? [] } });
  await connectEvents(store, {
    onEvent: (ev) => {
      desktop.onEvent(ev);
      updates.onEvent(ev);
      const t = ev.transfer;
      if (ev.type !== "transfer" || !t) return;
      if (t.state === "done" && !t.outgoing) sfx("done");
      if (t.state === "failed") sfx("error");
    },
  });
  await boot.finish(true, `ENGINE ONLINE · ${info.device}`);
  setTimeout(() => (bootEl.dataset.done = ""), reducedMotion() ? 0 : 350);
  // Remove it outright once the fade is over, even if a throttled
  // background tab never finishes the transition.
  setTimeout(() => (bootEl.hidden = true), reducedMotion() ? 0 : 800);
} catch (err) {
  store.set({ engine: { ...store.get().engine, state: "offline" } });
  await boot.finish(false, `ENGINE OFFLINE · ${err.message}`);
}
