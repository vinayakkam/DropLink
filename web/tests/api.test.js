import assert from "node:assert/strict";
import { test } from "node:test";
import { api, reset } from "../js/api.js";

// A restarted sidecar hands out a fresh token, so the cached one 401s.
// api() should forget it, fetch a new session and retry once.
test("api retries once with a fresh token after a 401", async () => {
  const prevFetch = globalThis.fetch;
  const prevTauri = globalThis.__TAURI__;
  delete globalThis.__TAURI__;
  reset();

  let issued = 0;
  const sent = [];
  globalThis.fetch = async (url, opts = {}) => {
    if (String(url).endsWith("/dev/session")) {
      issued++;
      return { ok: true, status: 200, json: async () => ({ token: `tok${issued}` }) };
    }
    sent.push(opts.headers.Authorization);
    if (opts.headers.Authorization === "Bearer tok1") {
      return { ok: false, status: 401, json: async () => ({ error: "missing or wrong token" }) };
    }
    return { ok: true, status: 200, json: async () => ({ ok: true }) };
  };

  try {
    const data = await api("/api/history");
    assert.deepEqual(data, { ok: true });
    assert.deepEqual(sent, ["Bearer tok1", "Bearer tok2"], "stale token, then a fresh one");
  } finally {
    reset();
    globalThis.fetch = prevFetch;
    if (prevTauri !== undefined) globalThis.__TAURI__ = prevTauri;
  }
});

// A 401 that is not a stale token (both attempts fail) still surfaces.
test("api gives up after one retry", async () => {
  const prevFetch = globalThis.fetch;
  const prevTauri = globalThis.__TAURI__;
  delete globalThis.__TAURI__;
  reset();

  let attempts = 0;
  globalThis.fetch = async (url) => {
    if (String(url).endsWith("/dev/session")) {
      return { ok: true, status: 200, json: async () => ({ token: "tok" }) };
    }
    attempts++;
    return { ok: false, status: 401, json: async () => ({ error: "missing or wrong token" }) };
  };

  try {
    await assert.rejects(() => api("/api/history"), /missing or wrong token/);
    assert.equal(attempts, 2, "one try plus one retry");
  } finally {
    reset();
    globalThis.fetch = prevFetch;
    if (prevTauri !== undefined) globalThis.__TAURI__ = prevTauri;
  }
});
