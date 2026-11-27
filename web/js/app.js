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
import { applyAppearance } from "./theme.js";
import { mountHome } from "./ui/home.js";
import { mountPages } from "./ui/pages.js";
import { showView, watchRoute } from "./views.js";

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

mountScenes(document, store);
export const home = mountHome(document, store, { sfx });
export const pages = mountPages(document, store, { sfx, applySettings: applyAppearance });
const desktop = initDesktop(store);

const bootEl = document.getElementById("boot");
const boot = runBoot(document.getElementById("boot-log"), { instant: reducedMotion() });

try {
  await connect();
  const info = await api("/api/info");
  store.set({ engine: { state: "online", version: info.version, device: info.device, features: info.features ?? [] } });
  await connectEvents(store, {
    onEvent: (ev) => {
      desktop.onEvent(ev);
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
