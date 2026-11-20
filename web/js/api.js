// Client for the Go sidecar's loopback API.
//
// In the desktop app the port and token come from the Tauri command
// `sidecar_info`. In a plain browser (sidecar started with -web-dir web) the
// UI is same-origin with the API and reads the token from /dev/session.

const POLL_MS = 150;
const START_TIMEOUT_MS = 15000;

let session = null;

/** @returns {Promise<{base: string, token: string, version: string}>} */
export async function connect() {
  if (session) return session;
  const tauri = globalThis.__TAURI__;
  if (tauri?.core?.invoke) {
    const deadline = Date.now() + START_TIMEOUT_MS;
    for (;;) {
      const info = await tauri.core.invoke("sidecar_info");
      if (info.state === "ready") {
        session = { base: `http://127.0.0.1:${info.port}`, token: info.token, version: info.version };
        return session;
      }
      if (info.state === "failed") throw new Error(info.error || "engine failed to start");
      if (Date.now() > deadline) throw new Error("engine did not start in time");
      await new Promise((r) => setTimeout(r, POLL_MS));
    }
  }
  const res = await fetch("/dev/session");
  if (!res.ok) throw new Error("no engine session (start the sidecar with -web-dir web)");
  const { token } = await res.json();
  session = { base: "", token, version: "dev" };
  return session;
}

/**
 * Calls the API and returns parsed JSON, or throws Error(data.error).
 * @param {string} path
 * @param {{method?: string, body?: unknown, signal?: AbortSignal}} [opts]
 */
export async function api(path, { method = "GET", body, signal } = {}) {
  const s = await connect();
  const headers = { Authorization: `Bearer ${s.token}` };
  if (body !== undefined) headers["Content-Type"] = "application/json";
  const res = await fetch(s.base + path, {
    method,
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
    signal,
  });
  const data = res.status === 204 ? null : await res.json().catch(() => null);
  if (!res.ok) throw new Error(data?.error || `HTTP ${res.status}`);
  return data;
}

/** Forgets the session, e.g. after `restart_sidecar`. */
export function reset() {
  session = null;
}
