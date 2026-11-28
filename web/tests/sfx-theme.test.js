import assert from "node:assert/strict";
import { test } from "node:test";
import { SOUNDS, createSfx } from "../js/sfx.js";
import { THEMES, applyAppearance } from "../js/theme.js";

function fakeAudio() {
  const made = { contexts: 0, oscillators: 0, peak: 0 };
  const param = () => ({
    setValueAtTime() {},
    linearRampToValueAtTime(v) {
      made.peak = Math.max(made.peak, v);
    },
  });
  const ctx = {
    currentTime: 0,
    destination: {},
    createOscillator() {
      made.oscillators++;
      return { type: "", frequency: param(), connect: (g) => g, start() {}, stop() {} };
    },
    createGain() {
      return { gain: param(), connect: (d) => d };
    },
  };
  return { made, make: () => (made.contexts++, ctx) };
}

test("each sound plays its notes at the saved volume", () => {
  const a = fakeAudio();
  let volume = 50;
  const play = createSfx(() => volume, a.make);
  assert.equal(play("done"), SOUNDS.done.length);
  assert.equal(a.made.oscillators, SOUNDS.done.length);
  assert.ok(a.made.peak > 0 && a.made.peak <= 0.12 * 0.5 + 1e-9);
  play("select");
  assert.equal(a.made.contexts, 1, "one audio context is reused");
  assert.equal(play("no-such-sound"), 0);
  volume = 0;
  assert.equal(play("send"), 0);
});

test("volume 0 never creates an audio context", () => {
  const a = fakeAudio();
  const play = createSfx(() => 0, a.make);
  play("incoming");
  assert.equal(a.made.contexts, 0);
});

test("no audio device is not an error", () => {
  const play = createSfx(() => 80, () => {
    throw new Error("no device");
  });
  assert.equal(play("error"), 0);
});

test("appearance sets theme and motion on <html>", () => {
  const root = { dataset: {} };
  applyAppearance({ theme: "amber", motion: false }, root);
  assert.deepEqual(root.dataset, { theme: "amber", motion: "off" });
  applyAppearance({ theme: "cyan", motion: true }, root);
  assert.deepEqual(root.dataset, {});
  applyAppearance({ theme: "neon" }, root);
  assert.deepEqual(root.dataset, {}, "unknown themes fall back to cyan");
  assert.deepEqual(THEMES.map((t) => t.id), ["cyan", "amber", "magenta", "light"]);
});

test("light theme is applied like any other", () => {
  const root = { dataset: {} };
  applyAppearance({ theme: "light" }, root);
  assert.deepEqual(root.dataset, { theme: "light" });
});
