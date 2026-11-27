// Formatting for sizes, speeds and times, in the UI's terse style.

const UNITS = ["B", "KB", "MB", "GB", "TB"];

/** 1536 → "1.5 KB" (binary steps, one decimal below 10). */
export function bytes(n) {
  if (!Number.isFinite(n) || n < 0) return "—";
  let i = 0;
  while (n >= 1024 && i < UNITS.length - 1) {
    n /= 1024;
    i++;
  }
  const s = i === 0 || n >= 10 ? Math.round(n).toString() : n.toFixed(1);
  return `${s} ${UNITS[i]}`;
}

/** Bytes per second → "2.4 MB/s". */
export function speed(bps) {
  return Number.isFinite(bps) && bps > 0 ? `${bytes(bps)}/s` : "0 B/s";
}

/** Seconds → "45s", "3m 05s", "1h 02m". */
export function duration(sec) {
  if (!Number.isFinite(sec) || sec < 0) return "—";
  sec = Math.round(sec);
  if (sec < 60) return `${sec}s`;
  const m = Math.floor(sec / 60);
  if (m < 60) return `${m}m ${String(sec % 60).padStart(2, "0")}s`;
  return `${Math.floor(m / 60)}h ${String(m % 60).padStart(2, "0")}m`;
}

/** Time left at the current speed, or "—" when it cannot be known. */
export function eta(done, total, bps) {
  if (!(bps > 0) || total <= done) return total <= done ? "0s" : "—";
  return duration((total - done) / bps);
}

/** 0..100, safe for empty transfers. */
export function percent(done, total) {
  if (!(total > 0)) return done > 0 ? 100 : 0;
  return Math.max(0, Math.min(100, Math.floor((done / total) * 100)));
}

/** "a1b2c3d4…" style short device ID for tooltips. */
export function shortId(id) {
  return id ? `${String(id).slice(0, 8)}…` : "";
}
