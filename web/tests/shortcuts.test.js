import assert from "node:assert/strict";
import { test } from "node:test";
import { keyToAction } from "../js/shortcuts.js";

test("digits switch sections", () => {
  assert.equal(keyToAction({ key: "1" }), "view:home");
  assert.equal(keyToAction({ key: "2" }), "view:rooms");
  assert.equal(keyToAction({ key: "3" }), "view:history");
  assert.equal(keyToAction({ key: "4" }), "view:settings");
});

test("letter keys trigger cargo actions, any case", () => {
  assert.equal(keyToAction({ key: "f" }), "pick-files");
  assert.equal(keyToAction({ key: "D" }), "pick-folder");
  assert.equal(keyToAction({ key: "v" }), "send-clip");
  assert.equal(keyToAction({ key: "c" }), "clear");
  assert.equal(keyToAction({ key: "Enter" }), "launch");
});

test("? opens help, via ? or shift+/", () => {
  assert.equal(keyToAction({ key: "?" }), "help");
  assert.equal(keyToAction({ key: "/", shiftKey: true }), "help");
});

test("shortcuts yield while typing or in a modal", () => {
  assert.equal(keyToAction({ key: "1" }, { typing: true }), null);
  assert.equal(keyToAction({ key: "f" }, { modal: true }), null);
});

test("modified keys are left to the browser and OS", () => {
  assert.equal(keyToAction({ key: "1", ctrlKey: true }), null);
  assert.equal(keyToAction({ key: "f", metaKey: true }), null);
  assert.equal(keyToAction({ key: "4", altKey: true }), null);
});

test("unmapped keys do nothing", () => {
  assert.equal(keyToAction({ key: "z" }), null);
  assert.equal(keyToAction({ key: "9" }), null);
});
