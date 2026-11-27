import assert from "node:assert/strict";
import { test } from "node:test";
import { bytes, duration, eta, percent, shortId, speed } from "../js/format.js";

test("bytes", () => {
  assert.equal(bytes(0), "0 B");
  assert.equal(bytes(1023), "1023 B");
  assert.equal(bytes(1536), "1.5 KB");
  assert.equal(bytes(10 * 1024 * 1024), "10 MB");
  assert.equal(bytes(3.25 * 1024 ** 3), "3.3 GB");
  assert.equal(bytes(-1), "—");
  assert.equal(bytes(NaN), "—");
});

test("speed", () => {
  assert.equal(speed(0), "0 B/s");
  assert.equal(speed(2.5 * 1024 * 1024), "2.5 MB/s");
  assert.equal(speed(undefined), "0 B/s");
});

test("duration and eta", () => {
  assert.equal(duration(45), "45s");
  assert.equal(duration(185), "3m 05s");
  assert.equal(duration(3720), "1h 02m");
  assert.equal(eta(50, 100, 10), "5s");
  assert.equal(eta(50, 100, 0), "—");
  assert.equal(eta(100, 100, 0), "0s");
});

test("percent never leaves 0..100", () => {
  assert.equal(percent(0, 0), 0);
  assert.equal(percent(5, 0), 100);
  assert.equal(percent(1, 3), 33);
  assert.equal(percent(7, 5), 100);
});

test("shortId", () => {
  assert.equal(shortId("0123456789abcdef"), "01234567…");
  assert.equal(shortId(""), "");
});
