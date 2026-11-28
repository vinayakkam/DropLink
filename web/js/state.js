// Pure state updates from sidecar events. No DOM here, so every rule is
// unit-tested in node.

/**
 * @typedef {{ id: string, name?: string, avatar?: number, route?: string, trusted?: boolean }} Peer
 * @typedef {{ id: string, peer: Peer, outgoing: boolean, state: string, waiting?: boolean,
 *   files: number, names?: string[], text?: string, done: number, total: number,
 *   bytesPerSec: number, error?: string, sas?: number[], savedTo?: string, startedAt?: string }} Transfer
 */

export const FINISHED = new Set(["done", "failed", "cancelled", "rejected"]);

/** Peers sorted by name, then ID. @param {Peer[]} peers */
export function sortPeers(peers) {
  return [...peers].sort(
    (a, b) => (a.name || "").localeCompare(b.name || "", undefined, { sensitivity: "base" }) || a.id.localeCompare(b.id),
  );
}

/** Replaces or adds one transfer, newest first. */
export function upsertTransfer(list, t) {
  const i = list.findIndex((x) => x.id === t.id);
  if (i < 0) return [t, ...list];
  const next = list.slice();
  next[i] = t;
  return next;
}

/**
 * Applies one event to the state and returns the new state (or the same
 * object when nothing changed).
 * @param {any} state
 * @param {{ type: string, kind?: string, peer?: Peer, peers?: Peer[], transfer?: Transfer, transfers?: Transfer[], room?: any }} ev
 */
export function applyEvent(state, ev) {
  switch (ev.type) {
    case "hello": {
      const transfers = [...(ev.transfers ?? [])].reverse(); // server sends oldest first
      const peers = sortPeers(ev.peers ?? []);
      return { ...state, peers, transfers, selected: keepSelected(state.selected, peers) };
    }
    case "peer": {
      if (!ev.peer) return state;
      let peers = state.peers.filter((p) => p.id !== ev.peer.id);
      if (ev.kind !== "left") peers = sortPeers([...peers, ev.peer]);
      return { ...state, peers, selected: keepSelected(state.selected, peers) };
    }
    case "transfer":
      return ev.transfer ? { ...state, transfers: upsertTransfer(state.transfers, ev.transfer) } : state;
    case "room":
      return { ...state, room: ev.room && ev.room.code ? ev.room : null };
    default:
      return state;
  }
}

/** Drops selections of peers that are gone. */
function keepSelected(selected, peers) {
  if (!selected || selected.size === 0) return selected ?? new Set();
  const ids = new Set(peers.map((p) => p.id));
  const kept = new Set([...selected].filter((id) => ids.has(id)));
  return kept.size === selected.size ? selected : kept;
}

/** Toggles a peer in the selection (a new Set, so subscribers notice). */
export function toggleSelected(selected, id) {
  const next = new Set(selected);
  if (next.has(id)) next.delete(id);
  else next.add(id);
  return next;
}

/** Up and down speed over transfers that are moving. */
export function speeds(transfers) {
  let up = 0;
  let down = 0;
  for (const t of transfers) {
    if (t.state !== "active") continue;
    if (t.outgoing) up += t.bytesPerSec || 0;
    else down += t.bytesPerSec || 0;
  }
  return { up, down };
}

/** The offer that should be on screen: the oldest waiting incoming one. */
export function pendingOffer(transfers) {
  const waiting = transfers.filter((t) => t.waiting && !t.outgoing);
  return waiting.length ? waiting[waiting.length - 1] : null;
}

/** How many transfers are not finished yet. */
export function activeCount(transfers) {
  return transfers.filter((t) => !FINISHED.has(t.state)).length;
}

/** The flight-log filters, in the order the chips show. */
export const HISTORY_FILTERS = ["all", "sent", "received", "failed"];

/**
 * Keeps the history records a filter should show. Unknown filter → all.
 * @param {Array<{ outgoing?: boolean, state?: string }>} recs
 * @param {string} filter
 */
export function filterHistory(recs, filter) {
  switch (filter) {
    case "sent":
      return recs.filter((r) => r.outgoing);
    case "received":
      return recs.filter((r) => !r.outgoing);
    case "failed":
      return recs.filter((r) => r.state === "failed" || r.state === "cancelled" || r.state === "rejected");
    default:
      return recs;
  }
}
