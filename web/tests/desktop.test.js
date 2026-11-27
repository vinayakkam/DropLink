import assert from "node:assert/strict";
import { test } from "node:test";
import { dropAction, initDesktop, notificationFor, toPaths } from "../js/desktop.js";
import { drawQR } from "../js/ui/pages.js";

const tr = (over) => ({ type: "transfer", transfer: { id: "t", peer: { id: "p", name: "Vega" }, files: 2, outgoing: false, state: "active", ...over } });

test("offers always notify; finished transfers only in the background", () => {
  assert.deepEqual(notificationFor(tr({ waiting: true, state: "offered" }), true), {
    title: "Incoming cargo",
    body: "Vega wants to send 2 files.",
  });
  assert.equal(notificationFor(tr({ state: "done" }), true), null);
  assert.deepEqual(notificationFor(tr({ state: "done" }), false), { title: "Cargo received", body: "2 files from Vega." });
  assert.deepEqual(notificationFor(tr({ state: "done", outgoing: true, files: 1 }), false), { title: "Cargo delivered", body: "1 file to Vega." });
  assert.match(notificationFor(tr({ state: "failed", error: "link lost" }), false).body, /link lost/);
  assert.equal(notificationFor(tr({ state: "active" }), false), null);
  assert.equal(notificationFor({ type: "peer" }, false), null);
  assert.equal(notificationFor(tr({ waiting: true, outgoing: true }), false), null, "our own offers do not notify");
  assert.match(notificationFor(tr({ waiting: true, files: 0 }), true).body, /a text message/);
});

test("dialog results become path lists", () => {
  assert.equal(toPaths(null), null);
  assert.deepEqual(toPaths("C:\\a.txt"), ["C:\\a.txt"]);
  assert.deepEqual(toPaths(["/a", "/b"]), ["/a", "/b"]);
  assert.deepEqual(toPaths([{ path: "/c" }]), ["/c"]);
  assert.equal(toPaths([]), null);
});

test("drag-drop events light the bay and deliver paths", () => {
  assert.deepEqual(dropAction({ type: "enter", paths: ["/a"] }), { hover: true, paths: null });
  assert.deepEqual(dropAction({ type: "over" }), { hover: true, paths: null });
  assert.deepEqual(dropAction({ type: "leave" }), { hover: false, paths: null });
  assert.deepEqual(dropAction({ type: "drop", paths: ["/x", "/y"] }), { hover: false, paths: ["/x", "/y"] });
  assert.deepEqual(dropAction({ type: "drop", paths: [] }), { hover: false, paths: null });
  assert.deepEqual(dropAction(undefined), { hover: false, paths: null });
});

test("outside Tauri the desktop layer does nothing", () => {
  const d = initDesktop({ select() { throw new Error("used the store"); } });
  d.onEvent(tr({ waiting: true }));
});

test("QR codes are drawn dark on light with a quiet zone", () => {
  const rects = [];
  const ctx = {
    fillStyle: "",
    fillRect(x, y, w, h) {
      rects.push([this.fillStyle, x, y, w, h]);
    },
  };
  const canvas = { width: 0, height: 0, getContext: () => ctx };
  const n = drawQR(canvas, { size: 2, bits: "1001" }, "#000", "#fff");
  assert.equal(n, 6);
  assert.deepEqual([canvas.width, canvas.height], [6, 6]);
  assert.deepEqual(rects, [
    ["#fff", 0, 0, 6, 6],
    ["#000", 2, 2, 1, 1],
    ["#000", 3, 3, 1, 1],
  ]);
});
