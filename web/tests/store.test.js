import assert from "node:assert/strict";
import { test } from "node:test";
import { createStore } from "../js/store.js";

test("set merges and notifies with the previous state", () => {
  const s = createStore({ a: 1, b: { x: 1 } });
  const seen = [];
  s.subscribe((next, prev) => seen.push([next.a, prev.a]));
  s.set({ a: 2 });
  assert.deepEqual(s.get(), { a: 2, b: { x: 1 } });
  assert.deepEqual(seen, [[2, 1]]);
});

test("a function patch that returns the same state notifies nobody", () => {
  const s = createStore({ a: 1 });
  let calls = 0;
  s.subscribe(() => calls++);
  s.set((st) => st);
  assert.equal(calls, 0);
});

test("select fires once now and then only when its slice changes", () => {
  const s = createStore({ engine: { state: "starting" }, other: 0 });
  const seen = [];
  s.select((st) => st.engine, (v) => seen.push(v.state));
  s.set({ other: 1 }); // engine object unchanged
  s.set({ engine: { state: "online" } });
  assert.deepEqual(seen, ["starting", "online"]);
});

test("unsubscribing during a notification is safe", () => {
  const s = createStore({ n: 0 });
  let a = 0;
  let b = 0;
  const offB = s.subscribe(() => b++);
  const offA = s.subscribe(() => {
    a++;
    offB();
  });
  s.set({ n: 1 });
  s.set({ n: 2 });
  offA();
  s.set({ n: 3 });
  assert.equal(a, 2);
  assert.equal(b, 1);
});
