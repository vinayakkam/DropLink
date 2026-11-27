import assert from "node:assert/strict";
import { test } from "node:test";
import { href, parseRoute, showView, watchRoute } from "../js/views.js";

test("parseRoute reads view and query, unknown views go home", () => {
  assert.deepEqual(parseRoute("#/rooms?code=ABC234"), { view: "rooms", query: { code: "ABC234" } });
  assert.deepEqual(parseRoute(""), { view: "home", query: {} });
  assert.deepEqual(parseRoute("#/"), { view: "home", query: {} });
  assert.equal(parseRoute("#/nowhere").view, "home");
  assert.equal(parseRoute("#settings").view, "settings");
});

test("href builds hashes parseRoute understands", () => {
  assert.equal(href("home"), "#/");
  assert.equal(href("rooms", { code: "X2" }), "#/rooms?code=X2");
  assert.deepEqual(parseRoute(href("history", { q: "a b" })), { view: "history", query: { q: "a b" } });
});

function fakeEl(attrs) {
  return {
    hidden: false,
    attrs: { ...attrs },
    getAttribute(k) {
      return this.attrs[k] ?? null;
    },
    setAttribute(k, v) {
      this.attrs[k] = v;
    },
    removeAttribute(k) {
      delete this.attrs[k];
    },
  };
}

test("showView shows one section and marks one tab", () => {
  const views = ["home", "rooms", "settings"].map((v) => fakeEl({ "data-view": v }));
  const tabs = ["home", "rooms", "settings"].map((v) => fakeEl({ "data-tab": v }));
  const root = { querySelectorAll: (sel) => (sel === "[data-view]" ? views : tabs) };
  showView(root, { view: "rooms" });
  assert.deepEqual(views.map((v) => v.hidden), [true, false, true]);
  assert.deepEqual(tabs.map((t) => t.getAttribute("aria-current")), [null, "page", null]);
});

test("watchRoute follows hashchange and can stop", () => {
  const listeners = {};
  const win = {
    location: { hash: "#/history" },
    addEventListener: (e, fn) => (listeners[e] = fn),
    removeEventListener: (e) => delete listeners[e],
  };
  const sets = [];
  const stop = watchRoute(win, { set: (p) => sets.push(p.route.view) });
  win.location.hash = "#/rooms";
  listeners.hashchange();
  stop();
  assert.deepEqual(sets, ["history", "rooms"]);
  assert.equal(listeners.hashchange, undefined);
});
