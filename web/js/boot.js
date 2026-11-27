// Boot check: connects to the engine and shows its state.
import { api } from "./api.js";
import { initTitlebar } from "./titlebar.js";

initTitlebar(document);
const status = document.getElementById("status");

try {
  const info = await api("/api/info");
  status.dataset.state = "ready";
  status.textContent = `ENGINE: ONLINE · v${info.version} · ${info.device}`;
  document.getElementById("sector").textContent = `SECTOR: ${info.device.toUpperCase()}`;
} catch (err) {
  status.dataset.state = "failed";
  status.textContent = `ENGINE: OFFLINE · ${err.message}`;
}
