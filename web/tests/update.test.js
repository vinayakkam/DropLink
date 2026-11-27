import assert from "node:assert/strict";
import { test } from "node:test";
import { updateLabel } from "../js/ui/update.js";

test("the status bar follows an update", () => {
  assert.equal(updateLabel({ phase: "downloading", done: 50, total: 200 }, "0.2.0"), "UPDATE v0.2.0 · 25%");
  assert.equal(updateLabel({ phase: "ready" }, "0.2.0"), "UPDATE v0.2.0 READY");
  assert.equal(updateLabel({ phase: "failed", error: "x" }, "0.2.0"), "UPDATE FAILED");
  assert.equal(updateLabel(null, "0.2.0"), "");
});
