import assert from "node:assert/strict";
import { test } from "node:test";
import { BOOT_LINES, reducedMotion, runBoot } from "../js/bootseq.js";

function el() {
  return {
    textContent: "",
    classList: {
      added: [],
      add(c) {
        this.added.push(c);
      },
    },
  };
}

test("types every line, then finishes the last with the engine's answer", async () => {
  const e = el();
  const boot = runBoot(e, { sleep: async () => {} });
  await boot.finish(true, "ENGINE ONLINE · vega");
  const lines = e.textContent.trim().split("\n");
  assert.equal(lines.length, BOOT_LINES.length + 1);
  assert.match(lines[BOOT_LINES.length - 1], /LINKING ENGINE \.+ OK$/);
  assert.equal(lines.at(-1), "> ENGINE ONLINE · vega");
});

test("instant mode needs no timers; failures are marked", async () => {
  const e = el();
  const boot = runBoot(e, {
    instant: true,
    sleep: () => {
      throw new Error("slept");
    },
  });
  await boot.finish(false, "ENGINE OFFLINE · refused");
  assert.match(e.textContent, /FAIL\n> ENGINE OFFLINE · refused/);
  assert.deepEqual(e.classList.added, ["err"]);
});

test("stop ends typing early", async () => {
  const e = el();
  let n = 0;
  const boot = runBoot(e, {
    sleep: async () => {
      if (++n === 3) boot.stop();
    },
  });
  await boot.typed;
  assert.ok(e.textContent.length <= 3);
});

test("reducedMotion follows the OS and the app setting", () => {
  const win = (motion, media) => ({
    document: { documentElement: { dataset: motion ? { motion } : {} } },
    matchMedia: () => ({ matches: media }),
  });
  assert.equal(reducedMotion(win(null, false)), false);
  assert.equal(reducedMotion(win(null, true)), true);
  assert.equal(reducedMotion(win("off", false)), true);
});
