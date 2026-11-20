// Boot check: connects to the engine and shows its state.
// p1/ui-shell replaces this page with the real interface.
import { api } from "./api.js";

const status = document.getElementById("status");

try {
  const info = await api("/api/info");
  status.dataset.state = "ready";
  status.textContent = `ENGINE: ONLINE · v${info.version} · ${info.device}`;
} catch (err) {
  status.dataset.state = "failed";
  status.textContent = `ENGINE: OFFLINE · ${err.message}`;
}
