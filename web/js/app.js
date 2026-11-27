// Entry point: boot sequence, engine connection, routing and the status
// bar. Feature modules (radar, bay, cargo log, pages) subscribe to the
// same store.
import { api, connect } from "./api.js";
import { reducedMotion, runBoot } from "./bootseq.js";
import { createStore } from "./store.js";
import { initTitlebar } from "./titlebar.js";
import { showView, watchRoute } from "./views.js";

export const store = createStore({
  engine: { state: "starting", version: "", device: "", features: [] },
  route: { view: "home", query: {} },
});

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

const bootEl = document.getElementById("boot");
const boot = runBoot(document.getElementById("boot-log"), { instant: reducedMotion() });

try {
  await connect();
  const info = await api("/api/info");
  store.set({ engine: { state: "online", version: info.version, device: info.device, features: info.features ?? [] } });
  await boot.finish(true, `ENGINE ONLINE · ${info.device}`);
  setTimeout(() => (bootEl.dataset.done = ""), reducedMotion() ? 0 : 350);
} catch (err) {
  store.set({ engine: { ...store.get().engine, state: "offline" } });
  await boot.finish(false, `ENGINE OFFLINE · ${err.message}`);
}
