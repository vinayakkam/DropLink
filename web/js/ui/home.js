// The home view: radar contacts, the docking bay send flow, the cargo log
// and the incoming-offer dialog.
import { api } from "../api.js";
import { bytes, duration, eta, percent, shortId, speed } from "../format.js";
import { renderSAS } from "../pixel/glyphs.js";
import { readPalette } from "../pixel/palette.js";
import { activeCount, FINISHED, pendingOffer, speeds, toggleSelected } from "../state.js";
import { h, render, toast } from "./dom.js";
import { closeModal, openModal } from "./modal.js";

/** @type {{ files: () => Promise<string[] | null>, folder: () => Promise<string[] | null>, clipboard: () => Promise<string | null>, copy: (t: string) => Promise<void> }} */
const pickers = {
  files: () => askPaths("PICK FILES", "One file path per line."),
  folder: () => askPaths("PICK FOLDER", "Folder path (everything inside is sent)."),
  clipboard: () => askText(),
  copy: (t) => navigator.clipboard.writeText(t),
};

/** The desktop shell swaps in native pickers (p1/desktop-integration). */
export function setPickers(p) {
  Object.assign(pickers, p);
}

async function askPaths(title, hint) {
  const area = h("textarea", { class: "input", rows: "4", placeholder: "C:\\Users\\me\\Pictures\\trip" });
  const ok = await openModal({
    title,
    body: [h("p", { class: "muted" }, hint), area],
    actions: [
      { label: "CANCEL", value: false, kind: "ghost" },
      { label: "LOAD CARGO", value: true, primary: true },
    ],
    cancelValue: false,
  });
  if (!ok) return null;
  const paths = area.value.split(/\r?\n/).map((s) => s.trim().replace(/^"(.*)"$/, "$1")).filter(Boolean);
  return paths.length ? paths : null;
}

async function askText() {
  let initial = "";
  try {
    initial = await navigator.clipboard.readText();
  } catch {
    // no clipboard permission in a plain browser: type it instead
  }
  const area = h("textarea", { class: "input", rows: "5", placeholder: "Text or a link to send" });
  area.value = initial;
  const ok = await openModal({
    title: "SEND TEXT",
    body: [area],
    actions: [
      { label: "CANCEL", value: false, kind: "ghost" },
      { label: "LOAD", value: true, primary: true },
    ],
    cancelValue: false,
  });
  return ok && area.value.trim() ? area.value : null;
}

/**
 * @param {Document} doc
 * @param {ReturnType<typeof import("../store.js").createStore>} store
 * @param {{ sfx?: (name: string) => void }} [opts]
 */
export function mountHome(doc, store, opts = {}) {
  const sfx = opts.sfx ?? (() => {});
  const $ = (id) => doc.getElementById(id);
  /** @type {{ paths?: string[], text?: string } | null} */
  let cargo = null;

  // --- Radar contacts --------------------------------------------------
  const drawContacts = () => {
      const { peers, selected } = store.get();
      $("peer-count").textContent = `${peers.length} CONTACT${peers.length === 1 ? "" : "S"}`;
      const n = selected.size;
      $("bay-target").textContent = n === 0 ? "NO TARGET" : n === 1 ? `TARGET: ${nameOf(peers, [...selected][0])}` : `${n} TARGETS`;
      if (!peers.length) {
        render($("peer-list"), h("li", { class: "list__empty" }, "SCANNING FOR CONTACTS…"));
        return;
      }
      render(
        $("peer-list"),
        ...peers.map((p) =>
          h(
            "li",
            {
              class: "list__item",
              role: "option",
              tabindex: "0",
              "aria-selected": selected.has(p.id) ? "true" : "false",
              title: shortId(p.id),
              onclick: () => select(p.id),
              onkeydown: (e) => (e.key === "Enter" || e.key === " ") && (e.preventDefault(), select(p.id)),
            },
            h("span", { class: `avatar avatar--${(p.avatar ?? 0) & 15}` }),
            h("span", { class: "list__title" }, p.name || shortId(p.id)),
            h(
              "span",
              { class: "row-tags" },
              p.trusted ? h("span", { class: "tag tag--trusted" }, "TRUSTED") : null,
              h("span", { class: `tag tag--${p.route}` }, p.route === "webrtc" ? "ROOM" : "LAN"),
            ),
          ),
        ),
      );
  };
  drawContacts();
  store.subscribe((st, prev) => {
    if (st.peers !== prev.peers || st.selected !== prev.selected) drawContacts();
  });

  function select(id) {
    store.set((s) => ({ ...s, selected: toggleSelected(s.selected, id) }));
    sfx("select");
  }

  // --- Docking bay -----------------------------------------------------
  const zone = $("dropzone");
  zone.addEventListener("dragenter", (e) => {
    e.preventDefault();
    store.set({ dropHover: true });
  });
  zone.addEventListener("dragover", (e) => e.preventDefault());
  zone.addEventListener("dragleave", (e) => {
    if (!zone.contains(/** @type {Node} */ (e.relatedTarget))) store.set({ dropHover: false });
  });
  zone.addEventListener("drop", (e) => {
    e.preventDefault();
    store.set({ dropHover: false });
    // Browsers hide file paths; the desktop shell delivers real paths
    // through setCargo instead.
    if (!globalThis.__TAURI__) toast("Browsers hide file paths: use PICK FILES here, or drop into the desktop app.");
  });

  $("pick-files").addEventListener("click", async () => setCargo({ paths: await pickers.files() }));
  $("pick-folder").addEventListener("click", async () => setCargo({ paths: await pickers.folder() }));
  $("send-clip").addEventListener("click", async () => setCargo({ text: await pickers.clipboard() }));
  $("manifest-clear").addEventListener("click", () => setCargo(null));
  $("launch").addEventListener("click", launch);

  /** @param {{ paths?: string[] | null, text?: string | null } | null} c */
  function setCargo(c) {
    cargo = c && ((c.paths && c.paths.length) || c.text) ? c : null;
    $("manifest").hidden = !cargo;
    if (!cargo) return;
    $("manifest-text").textContent = cargo.text
      ? `TEXT · ${cargo.text.length} chars`
      : `${cargo.paths.length} ITEM${cargo.paths.length === 1 ? "" : "S"} · ${cargo.paths.map((p) => p.split(/[\\/]/).pop()).join(", ")}`;
    sfx("select");
  }

  async function launch() {
    const { selected } = store.get();
    if (!cargo) return;
    if (selected.size === 0) {
      toast("Pick a contact on the radar first.", "warn");
      return;
    }
    const body = { peers: [...selected], ...(cargo.text ? { text: cargo.text } : { paths: cargo.paths }) };
    try {
      await api("/api/transfers", { method: "POST", body });
      sfx("send");
      setCargo(null);
    } catch (err) {
      sfx("error");
      toast(err.message, "error");
    }
  }

  // --- Cargo log -------------------------------------------------------
  store.select(
    (s) => s.transfers,
    (transfers) => {
      const n = activeCount(transfers);
      $("xfer-count").textContent = n ? `${n} IN FLIGHT` : "IDLE";
      const sp = speeds(transfers);
      $("speed-up").textContent = `▲ ${speed(sp.up)}`;
      $("speed-down").textContent = `▼ ${speed(sp.down)}`;
      const moving = transfers.find((t) => t.state === "active");
      $("route-state").textContent = moving ? `ROUTE ${moving.peer?.route === "webrtc" ? "ROOM" : "LAN"}` : "ROUTE —";
      if (!transfers.length) {
        render($("transfers"), h("li", { class: "list__empty" }, "NO CARGO IN FLIGHT"));
        return;
      }
      render($("transfers"), ...transfers.slice(0, 60).map((t) => transferRow(t)));
    },
  );

  function transferRow(t) {
    const pct = percent(t.done, t.total);
    const what = t.files ? `${t.files} FILE${t.files === 1 ? "" : "S"} · ${bytes(t.total)}` : "TEXT";
    const live = t.state === "active" || t.state === "paused";
    const act = (action) => async () => {
      try {
        await api(`/api/transfers/${t.id}/${action}`, { method: "POST" });
      } catch (err) {
        toast(err.message, "error");
      }
    };
    return h(
      "li",
      { class: `cargo cargo--${t.state}`, dataset: { id: t.id } },
      h(
        "div",
        { class: "cargo__head" },
        h("span", { class: "cargo__dir", title: t.outgoing ? "outgoing" : "incoming" }, t.outgoing ? "▲" : "▼"),
        h("span", { class: "list__title" }, t.peer?.name || shortId(t.peer?.id)),
        h("span", { class: `tag tag--state-${t.state}` }, t.waiting ? "AWAITING" : t.state.toUpperCase()),
      ),
      h("div", { class: "list__sub" }, (t.names ?? []).join(", ") || what),
      t.outgoing && t.sas?.length === 4 && (t.state === "offered" || t.state === "queued") ? sasStrip(t.sas) : null,
      t.files ? h("div", { class: "bar", style: `--pct:${pct}%` }, h("span", { class: "bar__fill" })) : null,
      h(
        "div",
        { class: "cargo__meta list__sub" },
        t.files ? `${pct}% · ${what}` : what,
        t.state === "active" ? ` · ${speed(t.bytesPerSec)} · ETA ${eta(t.done, t.total, t.bytesPerSec)}` : "",
        t.error ? ` · ${t.error}` : "",
      ),
      h(
        "div",
        { class: "cargo__actions" },
        t.state === "active" ? h("button", { class: "btn btn--ghost btn--sm", onclick: act("pause") }, "PAUSE") : null,
        t.state === "paused" ? h("button", { class: "btn btn--ghost btn--sm", onclick: act("resume") }, "RESUME") : null,
        live || t.state === "queued" ? h("button", { class: "btn btn--ghost btn--sm", onclick: act("cancel") }, "ABORT") : null,
        t.text
          ? h("button", { class: "btn btn--sm", onclick: () => pickers.copy(t.text).then(() => toast("Copied.")) }, "COPY TEXT")
          : null,
        FINISHED.has(t.state) && t.endedAt && t.startedAt
          ? h("span", { class: "faint" }, duration((Date.parse(t.endedAt) - Date.parse(t.startedAt)) / 1000))
          : null,
      ),
    );
  }

  // --- Incoming offers -------------------------------------------------
  let shown = null;
  store.subscribe((s, prev) => {
    if (s.transfers === prev.transfers) return;
    const offer = pendingOffer(s.transfers);
    if (offer?.id === shown) return;
    if (!offer) {
      if (shown) closeModal("gone");
      shown = null;
      return;
    }
    shown = offer.id;
    sfx("incoming");
    showOffer(offer).finally(() => {
      if (shown === offer.id) shown = null;
    });
  });

  async function showOffer(t) {
    const sasCanvas = h("canvas", { "aria-hidden": "true" });
    if (t.sas?.length === 4) renderSAS(sasCanvas, t.sas, readPalette().c);
    const trust = h("input", { type: "checkbox", id: "trust-sender" });
    const what = t.files ? `${t.files} file${t.files === 1 ? "" : "s"}, ${bytes(t.total)}` : "a text message";
    const answer = await openModal({
      title: "INCOMING CARGO",
      className: "offer",
      body: [
        h("p", { class: "offer__from glow" }, `${t.peer?.name || shortId(t.peer?.id)} wants to send ${what}`),
        t.names?.length ? h("p", { class: "muted offer__names" }, t.names.join(", ")) : null,
        t.sas?.length === 4
          ? h(
              "div",
              { class: "offer__sas" },
              h("span", { class: "sas" }, sasCanvas),
              h("p", { class: "faint" }, "Same 4 glyphs on both screens? Then nobody is in between."),
            )
          : null,
        t.peer?.trusted ? null : h("label", { class: "check" }, trust, " Trust this device (accept automatically next time)"),
      ],
      actions: [
        { label: "REJECT", value: "reject", kind: "ghost" },
        { label: "ACCEPT", value: "accept", primary: true },
      ],
      cancelValue: "later",
    });
    if (answer === "accept" || answer === "reject") {
      try {
        await api(`/api/transfers/${t.id}/${answer}`, { method: "POST", body: answer === "accept" ? { trust: trust.checked } : undefined });
        if (answer === "accept") sfx("send");
      } catch (err) {
        toast(err.message, "error");
      }
    }
  }

  return { setCargo };
}

/** The 4 glyphs, small, for the sender to compare with the receiver. */
function sasStrip(sas) {
  const c = h("canvas", { "aria-label": "verification glyphs" });
  renderSAS(c, sas, readPalette().c);
  return h("div", { class: "cargo__sas" }, h("span", { class: "faint" }, "CHECK:"), h("span", { class: "sas" }, c));
}

function nameOf(peers, id) {
  const p = peers.find((x) => x.id === id);
  return (p?.name || shortId(id)).toUpperCase();
}
