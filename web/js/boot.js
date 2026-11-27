// Boot check: connects to the engine and shows its state.
// p1/ui-shell's next step replaces this with the app's entry point.
import { api } from "./api.js";
import { initTitlebar } from "./titlebar.js";

initTitlebar(document);
const led = document.getElementById("engine-led");
const state = document.getElementById("engine-state");

try {
  const info = await api("/api/info");
  led.dataset.state = "online";
  state.textContent = "ENGINE ONLINE";
  document.getElementById("version").textContent = `v${info.version}`;
  document.getElementById("sector").textContent = `SECTOR: ${info.device.toUpperCase()}`;
} catch (err) {
  led.dataset.state = "offline";
  state.textContent = `ENGINE OFFLINE · ${err.message}`;
}
document.getElementById("boot").dataset.done = "";
