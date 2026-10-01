// pricing.mjs — pfm's price table, read once per run, and the per-response usage it prices.
import fs from "node:fs";
import { spawnSync } from "node:child_process";

// The table is pfm's (`pfm price --json`); this file carries no rates of its own. A missing,
// failing or unreadable table is an Error whose message follows "token-audit: ", never a fallback.
// TOKEN_AUDIT_PRICES names a saved `pfm price --json` document or the shipped prices.json and
// keeps pfm unspawned; otherwise TOKEN_AUDIT_PFM, or `pfm` on PATH, is run.
function loadTable(env) {
  const file = env.TOKEN_AUDIT_PRICES;
  if (file) {
    let text; try { text = fs.readFileSync(file, "utf8"); } catch (e) { throw new Error(`TOKEN_AUDIT_PRICES ${file}: ${e.message}`); }
    return tableOf(text, `TOKEN_AUDIT_PRICES ${file}: `);
  }
  const bin = env.TOKEN_AUDIT_PFM || "pfm", cmd = `\`${bin} price --json\``;
  const r = spawnSync(bin, ["price", "--json"], { encoding: "utf8" });
  if (r.error?.code === "ENOENT") throw new Error(`pfm not found (${bin}) — prices come from \`pfm price --json\`; install pfm or set TOKEN_AUDIT_PFM`);
  if (r.error) throw new Error(`${cmd} failed: ${r.error.message}`);
  if (r.status !== 0) throw new Error(`${cmd} failed (exit ${r.status ?? r.signal}): ${(r.stderr || "").trim()}`);
  return tableOf(r.stdout, `${cmd} returned an unreadable table: `);
}
// Only the envelope is checked here; validating rows is pfm's. The shipped prices.json has no
// `override`: it reads as null.
function tableOf(text, fault) {
  let d; try { d = JSON.parse(text); } catch (e) { throw new Error(fault + e.message); }
  if (d?.version !== 1) throw new Error(`${fault}version ${JSON.stringify(d?.version)}, want 1`);
  if (!Array.isArray(d.rows)) throw new Error(`${fault}rows is not an array`);
  return { rows: d.rows, override: d.override ?? null };
}

// Resolution, the one rule pfm uses too: of every row pattern found in the lowercased id, the
// longest wins and, at equal length, the one starting earlier; row order never matters. No
// pattern found: null, which every caller renders "n/a", never $0.
// A rate is USD per 1M tokens: rd is the Claude cache read or the Codex cached input (a SUBSET
// of input there); w5/w1 the 5-minute and 1-hour cache writes; lcIn/lcOut the multipliers for a
// call whose context passes LONG_CTX_TOKENS, feeding the CROSS-CHECK line only. A Codex rollout
// has no cache writes; a Claude-format transcript naming a Codex model writes at 1.25x / 2x input.
function rateOf(table, model) {
  const id = String(model || "").toLowerCase();
  let best = null, len = -1, at = 0;
  for (const row of table.rows) for (const m of row.match || []) {
    const p = String(m).toLowerCase(), i = id.indexOf(p);
    if (i >= 0 && (p.length > len || (p.length === len && i < at))) { best = row; len = p.length; at = i; }
  }
  if (!best) return null;
  const r = best;
  return r.engine === "codex"
    ? { in: r.in, out: r.out, rd: r.cached, w5: r.in * 1.25, w1: r.in * 2, lcIn: r.long_in, lcOut: r.long_out }
    : { in: r.in, out: r.out, rd: r.hit, w5: r.w5m, w1: r.w1h, lcIn: r.long_in, lcOut: r.long_out };
}

// The table RATE(m) consults, set once by the entry; a rate is resolved once per model id.
let TABLE = null, MEMO = new Map();
function useTable(table) { TABLE = table; MEMO = new Map(); }
const priceOverride = () => TABLE?.override ?? null;
const RATE = (m) => {
  if (!TABLE) throw new Error("no price table: useTable(loadTable(env)) runs before any rate is read");
  const id = String(m || "").toLowerCase();
  if (!MEMO.has(id)) MEMO.set(id, rateOf(TABLE, id));
  return MEMO.get(id);
};
const LONG_CTX_TOKENS = 200000;
// One response's usage as billed. A response can carry zeros in every top-level count and its
// real counts only in usage.iterations[] (seen on claude-opus-5-5, 2026-09); otherwise the top
// level already equals the iterations' sum and is used as is.
const TOP4 = (u) => (u.input_tokens || 0) + (u.output_tokens || 0) + (u.cache_read_input_tokens || 0) + (u.cache_creation_input_tokens || 0);
function usageOf(u) {
  if (TOP4(u) || !Array.isArray(u.iterations) || !u.iterations.length) return u;
  const s = { ...u, input_tokens: 0, output_tokens: 0, cache_read_input_tokens: 0, cache_creation_input_tokens: 0, cache_creation: { ephemeral_5m_input_tokens: 0, ephemeral_1h_input_tokens: 0 } };
  for (const it of u.iterations) { s.input_tokens += it.input_tokens || 0; s.output_tokens += it.output_tokens || 0; s.cache_read_input_tokens += it.cache_read_input_tokens || 0;
    s.cache_creation_input_tokens += it.cache_creation_input_tokens || 0; s.cache_creation.ephemeral_5m_input_tokens += it.cache_creation?.ephemeral_5m_input_tokens || 0; s.cache_creation.ephemeral_1h_input_tokens += it.cache_creation?.ephemeral_1h_input_tokens || 0; }
  return s; }
// Cache writes by TTL: ephemeral_5m and ephemeral_1h are read separately; any part of
// cache_creation_input_tokens the breakdown does not cover (no cache_creation object, or a
// short one) is the default 5-minute TTL. split=false: a write that carried no breakdown.
function writesOf(u) { const all = u.cache_creation_input_tokens || 0, b5 = u.cache_creation?.ephemeral_5m_input_tokens || 0, b1 = u.cache_creation?.ephemeral_1h_input_tokens || 0;
  return { cw5: b5 + Math.max(0, all - b5 - b1), cw1: b1, split: b5 + b1 > 0 }; }

export { loadTable, rateOf, useTable, priceOverride, LONG_CTX_TOKENS, RATE, TOP4, usageOf, writesOf };
