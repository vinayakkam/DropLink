// The ROOMS, LOG and CONFIG pages.
import { api } from "../api.js";
import { bytes, shortId } from "../format.js";
import { drawSprite, ship } from "../pixel/sprites.js";
import { readPalette } from "../pixel/palette.js";
import { THEMES } from "../theme.js";
import { h, render, toast } from "./dom.js";

/**
 * @param {Document} doc
 * @param {ReturnType<typeof import("../store.js").createStore>} store
 * @param {{ sfx?: (n: string) => void, applySettings: (s: any) => void, browseFolder?: () => Promise<string | null> }} opts
 */
export function mountPages(doc, store, opts) {
  const sfx = opts.sfx ?? (() => {});
  const features = () => store.get().engine.features ?? [];

  // Refresh a page's data whenever it is opened, and once the engine is
  // online (its feature list decides what the pages offer).
  const refresh = (view) => {
    if (store.get().engine.state !== "online") return;
    if (view === "history") loadHistory();
    if (view === "settings") loadSettings();
    if (view === "rooms") drawRooms();
  };
  store.select((s) => s.route.view, refresh);
  store.select(
    (s) => s.engine.state,
    (st) => {
      if (st !== "online") return;
      loadSettings();
      refresh(store.get().route.view);
    },
  );
  store.select((s) => s.room, () => store.get().route.view === "rooms" && drawRooms());

  // --- ROOMS ----------------------------------------------------------
  function drawRooms() {
    if (store.get().engine.state !== "online") return;
    const body = doc.getElementById("rooms-body");
    if (!features().includes("rooms")) {
      render(body, h("p", { class: "muted" }, "Rooms need a signaling hub. Start droplink-signal and set DROPLINK_SIGNAL_URL."));
      return;
    }
    const room = store.get().room;
    if (room) {
      render(
        body,
        h("p", { class: "muted" }, "Share this code with the other device. It joins from its ROOMS page."),
        h("div", { class: "room-share" }, h("div", { class: "room-code glow", "aria-label": "room code" }, room.code), features().includes("qr") ? qrTile(room.code) : null),
        h(
          "ul",
          { class: "list" },
          ...room.members.map((m) => h("li", { class: "list__item" }, h("span", { class: `avatar avatar--${m.avatar ?? 0}` }), h("span", { class: "list__title" }, m.name || shortId(m.id)), h("span", { class: "tag tag--webrtc" }, "ROOM"))),
        ),
        h("p", { class: "faint" }, "Members appear on the radar. Pick them there to send."),
        h("div", null, h("button", { class: "btn btn--ghost", onclick: leave }, "LEAVE ROOM")),
      );
      return;
    }
    const code = h("input", { class: "input room-input", maxlength: "6", placeholder: "ABC234", "aria-label": "room code", autocomplete: "off" });
    code.addEventListener("input", () => (code.value = code.value.toUpperCase().replace(/[^A-Z0-9]/g, "")));
    code.addEventListener("keydown", (e) => e.key === "Enter" && join(code.value));
    render(
      body,
      h("p", { class: "muted" }, "Not on the same network? Meet in a room: one device creates it, the other joins with the code. The hub only introduces you; files go device to device."),
      h("div", null, h("button", { class: "btn", onclick: () => join("") }, "CREATE ROOM")),
      h("div", { class: "field" }, h("span", { class: "field__label" }, "JOIN WITH CODE"), h("div", { class: "row" }, code, h("button", { class: "btn btn--ghost", onclick: () => join(code.value) }, "JOIN"))),
    );
  }

  /** A pixel-art QR of droplink://join/CODE, for a phone camera or a second screen. */
  function qrTile(code) {
    const canvas = h("canvas", { class: "qr", "aria-label": `QR code for room ${code}` });
    api(`/api/rooms/${encodeURIComponent(code)}/qr`)
      .then((q) => drawQR(canvas, q))
      .catch(() => canvas.remove());
    return canvas;
  }

  async function join(code) {
    try {
      const room = code ? await api(`/api/rooms/${encodeURIComponent(code)}/join`, { method: "POST" }) : await api("/api/rooms", { method: "POST", body: {} });
      store.set({ room });
      sfx("send");
    } catch (err) {
      sfx("error");
      toast(err.message, "error");
    }
  }

  async function leave() {
    await api("/api/rooms/current", { method: "DELETE" }).catch((e) => toast(e.message, "error"));
    store.set({ room: null });
  }

  // --- LOG ------------------------------------------------------------
  async function loadHistory() {
    const body = doc.getElementById("history-body");
    let recs = [];
    try {
      recs = await api("/api/history?limit=200");
    } catch (err) {
      render(body, h("p", { class: "muted" }, err.message));
      return;
    }
    const on = store.get().settings?.history;
    render(
      body,
      on ? null : h("p", { class: "muted" }, "The flight log is off. Turn it on in CONFIG to keep a record of transfers on this device."),
      recs.length
        ? h(
            "ul",
            { class: "list" },
            ...recs.map((r) =>
              h(
                "li",
                { class: `cargo cargo--${r.state}` },
                h("div", { class: "cargo__head" }, h("span", { class: "cargo__dir" }, r.outgoing ? "▲" : "▼"), h("span", { class: "list__title" }, r.peer?.name || shortId(r.peer?.id)), h("span", { class: `tag tag--state-${r.state}` }, r.state.toUpperCase())),
                h("div", { class: "list__sub" }, r.text ? "TEXT" : `${(r.files ?? []).length} file(s) · ${bytes(r.bytes)} · ${(r.files ?? []).slice(0, 3).map((f) => f.path).join(", ")}`),
                h("div", { class: "faint list__sub" }, new Date(r.endedAt || r.startedAt).toLocaleString()),
              ),
            ),
          )
        : h("p", { class: "list__empty" }, "NO FLIGHTS LOGGED"),
      recs.length ? h("div", null, h("button", { class: "btn btn--ghost", onclick: clearHistory }, "CLEAR LOG")) : null,
    );
  }

  async function clearHistory() {
    await api("/api/history", { method: "DELETE" }).catch((e) => toast(e.message, "error"));
    loadHistory();
  }

  // --- CONFIG ---------------------------------------------------------
  let saveTimer = 0;
  async function loadSettings() {
    try {
      const s = await api("/api/settings");
      store.set({ settings: s });
      opts.applySettings(s);
      if (store.get().route.view === "settings") await drawSettings(s);
    } catch (err) {
      toast(err.message, "error");
    }
  }

  function save(patch) {
    const next = { ...store.get().settings, ...patch };
    store.set({ settings: next });
    opts.applySettings(next);
    clearTimeout(saveTimer);
    saveTimer = setTimeout(async () => {
      try {
        const s = await api("/api/settings", { method: "PUT", body: next });
        store.set({ settings: s });
      } catch (err) {
        toast(err.message, "error");
        loadSettings();
      }
    }, 300);
  }

  async function drawSettings(s) {
    const body = doc.getElementById("settings-body");
    const name = h("input", { class: "input", value: s.name, maxlength: "64" });
    name.addEventListener("change", () => name.value.trim() && save({ name: name.value.trim() }));

    const avatars = h(
      "div",
      { class: "avatar-grid", role: "radiogroup", "aria-label": "avatar" },
      ...Array.from({ length: 16 }, (_, i) => {
        const c = h("canvas", { width: "11", height: "11" });
        const ctx = c.getContext("2d");
        const sp = ship(i);
        drawSprite(ctx, sp.sprite, 1, 1, readPalette(), sp.roles);
        return h("button", { class: "avatar-pick", role: "radio", "aria-checked": String(i === s.avatar), title: `ship ${i + 1}`, onclick: () => (save({ avatar: i }), drawSettings({ ...s, avatar: i })) }, c);
      }),
    );

    const dir = h("input", { class: "input", value: s.downloadDir });
    dir.addEventListener("change", () => save({ downloadDir: dir.value.trim() }));
    const browse = opts.browseFolder
      ? h("button", { class: "btn btn--ghost", onclick: async () => { const d = await opts.browseFolder(); if (d) { dir.value = d; save({ downloadDir: d }); } } }, "BROWSE")
      : null;

    const toggle = (key, label, hint) => {
      const box = h("input", { type: "checkbox", checked: !!s[key] });
      box.addEventListener("change", () => save({ [key]: box.checked }));
      return h("div", { class: "field" }, h("span", { class: "field__label" }, label), h("label", { class: "check" }, box, box.checked ? "ON" : "OFF"), hint ? h("span", { class: "field__hint" }, hint) : null);
    };

    const limit = h("input", { class: "input", type: "number", min: "0", step: "100", value: String(s.speedLimitKBps) });
    limit.addEventListener("change", () => save({ speedLimitKBps: Math.max(0, parseInt(limit.value, 10) || 0) }));

    const vol = h("input", { type: "range", min: "0", max: "100", value: String(s.volume), class: "range" });
    vol.addEventListener("change", () => {
      save({ volume: parseInt(vol.value, 10) });
      sfx("select");
    });

    const themes = h(
      "div",
      { class: "row", role: "radiogroup", "aria-label": "theme" },
      ...THEMES.map((t) => h("button", { class: `btn ${t.id === s.theme ? "" : "btn--ghost"}`, role: "radio", "aria-checked": String(t.id === s.theme), onclick: () => (save({ theme: t.id }), drawSettings({ ...s, theme: t.id })) }, t.name)),
    );

    let trusted = [];
    try {
      trusted = await api("/api/trust");
    } catch {
      // shown as empty
    }
    const trustList = trusted.length
      ? h("ul", { class: "list" }, ...trusted.map((d) => h("li", { class: "list__item" }, h("span", { class: "tag tag--trusted" }, "✓"), h("span", { class: "list__title", title: d.id }, d.name || shortId(d.id)), h("button", { class: "btn btn--ghost btn--sm", onclick: async () => { await api(`/api/trust/${d.id}`, { method: "DELETE" }); drawSettings(store.get().settings); } }, "REMOVE"))))
      : h("p", { class: "faint" }, "No trusted devices yet. Tick “trust this device” when you accept an offer.");

    render(
      body,
      h("h3", { class: "section" }, "IDENTITY"),
      h("div", { class: "field" }, h("span", { class: "field__label" }, "DEVICE NAME"), name, h("span", { class: "field__hint" }, "What others see on their radar.")),
      h("div", { class: "field" }, h("span", { class: "field__label" }, "SHIP"), avatars),
      h("h3", { class: "section" }, "CARGO"),
      h("div", { class: "field" }, h("span", { class: "field__label" }, "DOWNLOAD FOLDER"), h("div", { class: "row" }, dir, browse)),
      toggle("autoAcceptTrusted", "AUTO-ACCEPT TRUSTED", "Offers from trusted devices start without asking."),
      h("div", { class: "field" }, h("span", { class: "field__label" }, "SPEED LIMIT KB/S"), limit, h("span", { class: "field__hint" }, "0 = no limit (LAN).")),
      toggle("history", "FLIGHT LOG", "Keep a local list of transfers (names and sizes only)."),
      h("h3", { class: "section" }, "CONSOLE"),
      h("div", { class: "field" }, h("span", { class: "field__label" }, "THEME"), themes),
      toggle("motion", "ANIMATION", "Off: nothing moves (also follows the OS reduced-motion setting)."),
      h("div", { class: "field" }, h("span", { class: "field__label" }, "SOUND"), vol, h("span", { class: "field__hint" }, "Chiptune blips; 0 = silent.")),
      globalThis.__TAURI__ ? toggle("closeToTray", "CLOSE TO TRAY", "Keep receiving after the window closes.") : null,
      h("h3", { class: "section" }, "UPDATES"),
      toggle("autoUpdate", "CHECK AT START", "Look for a new signed release when DropLink starts."),
      opts.checkUpdates
        ? h("div", { class: "field" }, h("span", { class: "field__label" }, "VERSION"), h("div", { class: "row" }, h("span", null, `v${store.get().engine.version}`), h("button", { class: "btn btn--ghost", onclick: () => opts.checkUpdates() }, "CHECK NOW")))
        : null,
      h("h3", { class: "section" }, "TRUSTED DEVICES"),
      trustList,
    );
  }

  return {
    loadSettings,
    /** Joins a room (deep links use it) and shows the ROOMS page. */
    async joinRoom(code) {
      globalThis.location.hash = "#/rooms";
      await join(code);
    },
  };
}

/**
 * Draws QR modules dark on light (what cameras expect) with a 2-module
 * quiet zone; the canvas is scaled up crisply by CSS.
 * @param {HTMLCanvasElement} canvas
 * @param {{ size: number, bits: string }} q
 */
export function drawQR(canvas, q, dark = "#05070d", light = "#d8f6ff") {
  const quiet = 2;
  const n = q.size + quiet * 2;
  canvas.width = n;
  canvas.height = n;
  const ctx = canvas.getContext("2d");
  ctx.fillStyle = light;
  ctx.fillRect(0, 0, n, n);
  ctx.fillStyle = dark;
  for (let i = 0; i < q.bits.length; i++) {
    if (q.bits[i] === "1") ctx.fillRect(quiet + (i % q.size), quiet + Math.floor(i / q.size), 1, 1);
  }
  return n;
}
