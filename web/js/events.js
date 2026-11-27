// Live updates from the sidecar over server-sent events. The first event
// of every connection is a full snapshot, so a reconnect never leaves the
// UI out of step. EventSource cannot send headers, so the token goes in
// the query (the API accepts it there for this reason only).
import { connect } from "./api.js";
import { applyEvent } from "./state.js";

/**
 * @param {{ get: () => any, set: (p: any) => void }} store
 * @param {{ onEvent?: (ev: any) => void, EventSourceImpl?: typeof EventSource }} [opts]
 */
export async function connectEvents(store, opts = {}) {
  const ES = opts.EventSourceImpl ?? globalThis.EventSource;
  const s = await connect();
  let delay = 500;
  let es = null;
  let stopped = false;

  const open = () => {
    if (stopped) return;
    es = new ES(`${s.base}/api/events?token=${encodeURIComponent(s.token)}`);
    es.onopen = () => {
      delay = 500;
      store.set({ link: "up" });
    };
    es.onmessage = (m) => {
      let ev;
      try {
        ev = JSON.parse(m.data);
      } catch {
        return;
      }
      store.set((st) => applyEvent(st, ev));
      opts.onEvent?.(ev);
    };
    es.onerror = () => {
      es.close();
      store.set({ link: "down" });
      setTimeout(open, delay);
      delay = Math.min(delay * 2, 8000);
    };
  };
  open();
  return () => {
    stopped = true;
    es?.close();
  };
}
