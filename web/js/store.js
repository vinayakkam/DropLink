// A tiny observable store: one immutable state object, replaced on every
// change. Views subscribe to the slice they draw and redraw only when that
// slice changes, so nothing re-renders every frame.

/**
 * @template S
 * @param {S} initial
 */
export function createStore(initial) {
  let state = initial;
  /** @type {Set<(s: S, prev: S) => void>} */
  const subs = new Set();

  return {
    /** @returns {S} */
    get: () => state,

    /**
     * Merges a patch, or replaces state with fn(state). Equal results
     * notify nobody.
     * @param {Partial<S> | ((s: S) => S)} patch
     */
    set(patch) {
      const next = typeof patch === "function" ? patch(state) : { ...state, ...patch };
      if (next === state) return;
      const prev = state;
      state = next;
      for (const fn of [...subs]) fn(state, prev);
    },

    /** @param {(s: S, prev: S) => void} fn  @returns {() => void} unsubscribe */
    subscribe(fn) {
      subs.add(fn);
      return () => subs.delete(fn);
    },

    /**
     * Calls fn(value, prev) when sel(state) changes (Object.is), and once
     * now with the current value.
     * @template T
     * @param {(s: S) => T} sel
     * @param {(v: T, prev: T | undefined) => void} fn
     */
    select(sel, fn) {
      let cur = sel(state);
      fn(cur, undefined);
      return this.subscribe((s) => {
        const next = sel(s);
        if (!Object.is(next, cur)) {
          const prev = cur;
          cur = next;
          fn(next, prev);
        }
      });
    },
  };
}
