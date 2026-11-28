import assert from "node:assert/strict";
import { test } from "node:test";
import { activeCount, applyEvent, filterHistory, pendingOffer, sortPeers, speeds, toggleSelected, upsertTransfer } from "../js/state.js";

const base = () => ({ peers: [], transfers: [], selected: new Set(), room: null });

test("hello replaces peers and transfers (newest first) and keeps live selections", () => {
  const st = { ...base(), selected: new Set(["b", "gone"]) };
  const next = applyEvent(st, {
    type: "hello",
    peers: [{ id: "b", name: "zeta" }, { id: "a", name: "Alpha" }],
    transfers: [{ id: "old" }, { id: "new" }],
  });
  assert.deepEqual(next.peers.map((p) => p.id), ["a", "b"]);
  assert.deepEqual(next.transfers.map((t) => t.id), ["new", "old"]);
  assert.deepEqual([...next.selected], ["b"]);
});

test("hello with lists left out means empty lists", () => {
  const next = applyEvent({ ...base(), peers: [{ id: "x" }] }, { type: "hello" });
  assert.deepEqual(next.peers, []);
  assert.deepEqual(next.transfers, []);
});

test("peer joined, updated and left", () => {
  let st = base();
  st = applyEvent(st, { type: "peer", kind: "joined", peer: { id: "a", name: "Nova" } });
  st = applyEvent(st, { type: "peer", kind: "updated", peer: { id: "a", name: "Nova II", trusted: true } });
  assert.equal(st.peers.length, 1);
  assert.equal(st.peers[0].name, "Nova II");
  st = { ...st, selected: new Set(["a"]) };
  st = applyEvent(st, { type: "peer", kind: "left", peer: { id: "a" } });
  assert.equal(st.peers.length, 0);
  assert.equal(st.selected.size, 0, "a departed peer is deselected");
});

test("transfer events upsert in place; rooms set and clear", () => {
  let st = base();
  st = applyEvent(st, { type: "transfer", transfer: { id: "t1", state: "offered" } });
  st = applyEvent(st, { type: "transfer", transfer: { id: "t2", state: "active" } });
  st = applyEvent(st, { type: "transfer", transfer: { id: "t1", state: "done" } });
  assert.deepEqual(st.transfers.map((t) => `${t.id}:${t.state}`), ["t2:active", "t1:done"]);
  st = applyEvent(st, { type: "room", room: { code: "ABC234", members: [] } });
  assert.equal(st.room.code, "ABC234");
  st = applyEvent(st, { type: "room", room: { code: "", members: [] } });
  assert.equal(st.room, null);
});

test("unknown or empty events change nothing", () => {
  const st = base();
  assert.equal(applyEvent(st, { type: "mystery" }), st);
  assert.equal(applyEvent(st, { type: "transfer" }), st);
  assert.equal(applyEvent(st, { type: "peer" }), st);
});

test("helpers", () => {
  assert.deepEqual(sortPeers([{ id: "2", name: "b" }, { id: "1", name: "B" }, { id: "3", name: "a" }]).map((p) => p.id), ["3", "1", "2"]);
  const sel = toggleSelected(new Set(["a"]), "b");
  assert.deepEqual([...sel].sort(), ["a", "b"]);
  assert.deepEqual([...toggleSelected(sel, "a")], ["b"]);
  assert.deepEqual(upsertTransfer([{ id: "x" }], { id: "y" }).map((t) => t.id), ["y", "x"]);

  const ts = [
    { id: "1", outgoing: true, state: "active", bytesPerSec: 100 },
    { id: "2", outgoing: false, state: "active", bytesPerSec: 50 },
    { id: "3", outgoing: true, state: "paused", bytesPerSec: 999 },
    { id: "4", outgoing: false, state: "offered", waiting: true },
    { id: "5", outgoing: false, state: "offered", waiting: true },
    { id: "6", outgoing: false, state: "done" },
  ];
  assert.deepEqual(speeds(ts), { up: 100, down: 50 });
  assert.equal(pendingOffer(ts).id, "5", "the oldest waiting offer is shown first (list is newest first)");
  assert.equal(pendingOffer([]), null);
  assert.equal(activeCount(ts), 5);
});

test("filterHistory keeps the records a filter should show", () => {
  const recs = [
    { outgoing: true, state: "done" },
    { outgoing: false, state: "done" },
    { outgoing: true, state: "failed" },
    { outgoing: false, state: "cancelled" },
  ];
  assert.equal(filterHistory(recs, "all").length, 4);
  assert.deepEqual(filterHistory(recs, "sent").map((r) => r.state), ["done", "failed"]);
  assert.deepEqual(filterHistory(recs, "received").map((r) => r.state), ["done", "cancelled"]);
  assert.deepEqual(filterHistory(recs, "failed").map((r) => r.state), ["failed", "cancelled"]);
  assert.equal(filterHistory(recs, "bogus").length, 4, "unknown filter shows all");
});
