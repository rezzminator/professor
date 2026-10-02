// format.mjs — pure string helpers shared by the report modes.
export const shortModel = (m) => (m || "?").replace(/^claude-/, "").replace(/-\d{8}$/, "");
export const $ = (v) => (v >= 100 ? "$" + v.toFixed(0) : "$" + v.toFixed(2)), pct = (v, t) => (t ? (100 * v / t).toFixed(1) : "0.0") + "%";
export const Mt = (v) => (v / 1e6).toFixed(1) + "M", pad = (s, n) => String(s).padStart(n), cut = (s, n) => (s.length > n ? s.slice(0, n - 1) + "…" : s).padEnd(n);
// The cache a run's writes went to: 5m, 1h, or the 1-hour share when it wrote both; — with no writes.
export function ttlMix(t) { const w = t.cw5 + t.cw1; return !w ? "—" : !t.cw1 ? "5m" : !t.cw5 ? "1h" : `1h ${Math.min(99, Math.max(1, Math.round(100 * t.cw1 / w)))}%`; }
