import assert from "node:assert/strict";
import { test } from "node:test";
import { initTitlebar } from "../js/titlebar.js";

test("without Tauri the title bar does nothing", () => {
  assert.equal(initTitlebar({ querySelector: () => null }, null), false);
});

test("buttons drive the window", () => {
  globalThis.document = { documentElement: { dataset: {} } };
  const handlers = {};
  const root = {
    querySelector: (sel) => ({ addEventListener: (_, fn) => (handlers[sel] = fn) }),
  };
  const calls = [];
  const win = {
    minimize: () => calls.push("min"),
    toggleMaximize: () => calls.push("max"),
    close: () => calls.push("close"),
  };
  assert.equal(initTitlebar(root, win), true);
  handlers["[data-win=min]"]();
  handlers["[data-win=max]"]();
  handlers["[data-win=close]"]();
  assert.deepEqual(calls, ["min", "max", "close"]);
  assert.equal(globalThis.document.documentElement.dataset.tauri, "");
  delete globalThis.document;
});
