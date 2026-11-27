// Chiptune sound effects, synthesized with WebAudio: no audio files, a
// few oscillators per sound. Volume 0 is silent and creates no audio
// context at all.

/** Each sound: [frequency Hz, start s, length s, wave]. */
export const SOUNDS = {
  select: [[880, 0, 0.05, "square"]],
  send: [
    [523, 0, 0.07, "square"],
    [659, 0.07, 0.07, "square"],
    [784, 0.14, 0.1, "square"],
  ],
  incoming: [
    [988, 0, 0.09, "triangle"],
    [740, 0.11, 0.09, "triangle"],
    [988, 0.22, 0.12, "triangle"],
  ],
  done: [
    [523, 0, 0.08, "triangle"],
    [659, 0.08, 0.08, "triangle"],
    [784, 0.16, 0.08, "triangle"],
    [1047, 0.24, 0.16, "triangle"],
  ],
  error: [
    [196, 0, 0.12, "sawtooth"],
    [147, 0.12, 0.18, "sawtooth"],
  ],
};

/**
 * @param {() => number} volume 0..100
 * @param {() => AudioContext} [makeContext]
 */
export function createSfx(volume, makeContext = () => new AudioContext()) {
  let ctx = null;
  return function play(name) {
    const v = Math.max(0, Math.min(100, volume())) / 100;
    const notes = SOUNDS[name];
    if (!notes || v === 0) return 0;
    try {
      ctx ??= makeContext();
    } catch {
      return 0; // no audio device
    }
    const t0 = ctx.currentTime;
    for (const [freq, start, len, wave] of notes) {
      const osc = ctx.createOscillator();
      const gain = ctx.createGain();
      osc.type = wave;
      osc.frequency.setValueAtTime(freq, t0 + start);
      // Quick attack and release so notes click like a console, not pop.
      gain.gain.setValueAtTime(0, t0 + start);
      gain.gain.linearRampToValueAtTime(0.12 * v, t0 + start + 0.005);
      gain.gain.linearRampToValueAtTime(0, t0 + start + len);
      osc.connect(gain).connect(ctx.destination);
      osc.start(t0 + start);
      osc.stop(t0 + start + len + 0.01);
    }
    return notes.length;
  };
}
