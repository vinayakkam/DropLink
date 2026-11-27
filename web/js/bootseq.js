// The boot sequence: a few typed console lines while the engine starts.
// It never delays the app: the last line waits for the real engine state,
// and with reduced motion every line appears at once.

export const BOOT_LINES = [
  "DROPLINK FLIGHT SYSTEM v0.1",
  "> CALIBRATING SCANNERS ......... OK",
  "> PRESSURIZING CARGO BAY ....... OK",
  "> LINKING ENGINE ..............",
];

/**
 * Types lines into el. Returns finish(ok, text) which completes the last
 * line with the engine's answer.
 * @param {HTMLElement} el
 * @param {{ lines?: string[], charMs?: number, instant?: boolean, sleep?: (ms: number) => Promise<void> }} [opts]
 */
export function runBoot(el, opts = {}) {
  const lines = opts.lines ?? BOOT_LINES;
  const charMs = opts.charMs ?? 12;
  const instant = opts.instant ?? false;
  const sleep = opts.sleep ?? ((ms) => new Promise((r) => setTimeout(r, ms)));
  let stopped = false;

  const typed = (async () => {
    for (const line of lines) {
      if (stopped) return;
      if (instant) {
        el.textContent += `${line}\n`;
        continue;
      }
      for (const ch of line) {
        if (stopped) return;
        el.textContent += ch;
        await sleep(charMs);
      }
      el.textContent += "\n";
    }
  })();

  return {
    typed,
    /** @param {boolean} ok @param {string} text */
    async finish(ok, text) {
      await typed;
      el.textContent = el.textContent.replace(/\n$/, "");
      el.textContent += ` ${ok ? "OK" : "FAIL"}\n> ${text}\n`;
      if (!ok) el.classList.add("err");
    },
    stop() {
      stopped = true;
    },
  };
}

/** True when the person asked for less motion (OS setting or app setting). */
export function reducedMotion(win = globalThis) {
  return (
    win.document?.documentElement?.dataset?.motion === "off" ||
    !!win.matchMedia?.("(prefers-reduced-motion: reduce)").matches
  );
}
