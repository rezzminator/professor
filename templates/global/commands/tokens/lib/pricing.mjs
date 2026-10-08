// pricing.mjs — the `pfm model-cost` price table and the usage it prices.
import fs from "node:fs";
import { spawnSync } from "node:child_process";

// pfm owns the table, its daily refresh and its override. A saved `pfm model-cost --json --all`
// document or pfm's prices.json file gives reproducible estimates; there is no bundled/static fallback.
function loadTable(env, timeoutMs = 95000) {
  const file = env.TOKEN_AUDIT_PRICES;
  if (file) {
    let text; try { text = fs.readFileSync(file, "utf8"); } catch (e) { throw new Error(`TOKEN_AUDIT_PRICES ${file}: ${e.message}`); }
    const table = tableOf(text, `TOKEN_AUDIT_PRICES ${file}: `);
    table.saved = file;
    return table;
  }
  const bin = env.TOKEN_AUDIT_PFM || "pfm", cmd = `\`${bin} model-cost --json --all\``;
  const r = spawnSync(bin, ["model-cost", "--json", "--all"], { encoding: "utf8", timeout: timeoutMs, maxBuffer: 16 << 20 });
  if (r.error?.code === "ENOENT") throw new Error(`pfm not found (${bin}) — prices come from \`pfm model-cost --json --all\`; install pfm or set TOKEN_AUDIT_PFM`);
  if (r.error?.code === "ETIMEDOUT") throw new Error(`${cmd} timed out after ${timeoutMs / 1000} s`);
  if (r.error) throw new Error(`${cmd} failed: ${r.error.message}`);
  if (r.status !== 0) {
    const stderr = (r.stderr || "").trim(), older = stderr.includes('unknown command "model-cost"');
    throw new Error(`${cmd} failed (exit ${r.status ?? r.signal}): ${stderr}${older ? " — this pfm predates `pfm model-cost`; update pfm" : ""}`);
  }
  return tableOf(r.stdout, `${cmd} returned an unreadable table: `);
}
// The rate columns each engine may carry; `in` and `out` are required on a row, optional in `long`.
const COLUMNS = { claude: ["in", "out", "hit", "w5m", "w1h"], codex: ["in", "out", "cached"] };
const ROW_KEYS = new Set(["key", "engine", "long", "source"]);
const rate = (v) => typeof v === "number" && Number.isFinite(v) && v >= 0;
function rowFault(row, keys) {
  if (!row || typeof row !== "object" || Array.isArray(row)) return "a row is not an object";
  if (typeof row.key !== "string" || !row.key) return "a row has no key";
  const cols = COLUMNS[row.engine];
  if (!cols) return `row ${row.key}: engine ${JSON.stringify(row.engine)} is neither claude nor codex`;
  if (keys.has(row.key)) return `row ${row.key} appears twice`;
  for (const [k, v] of Object.entries(row)) {
    if (ROW_KEYS.has(k)) continue;
    if (!cols.includes(k)) return `row ${row.key}: column ${k} is not a ${row.engine} rate`;
    if (!rate(v)) return `row ${row.key}: ${k} is not a non-negative number`;
  }
  if (!rate(row.in) || !rate(row.out)) return `row ${row.key}: in and out are required`;
  if (row.source !== undefined && !["published", "override"].includes(row.source)) return `row ${row.key}: source ${JSON.stringify(row.source)} is neither published nor override`;
  if (row.long !== undefined) {
    const long = row.long;
    if (!long || typeof long !== "object" || Array.isArray(long) || !Number.isInteger(long.above) || long.above <= 0) return `row ${row.key}: long needs a positive integer above`;
    for (const [k, v] of Object.entries(long)) {
      if (k === "above") continue;
      if (!cols.includes(k)) return `row ${row.key}: long column ${k} is not a ${row.engine} rate`;
      if (!rate(v)) return `row ${row.key}: long ${k} is not a non-negative number`;
    }
  }
  return "";
}
function tableOf(text, fault) {
  let d; try { d = JSON.parse(text); } catch (e) { throw new Error(fault + e.message); }
  if (d?.version !== 2) throw new Error(`${fault}version ${JSON.stringify(d?.version ?? null)}, want the version 2 price table — update pfm`);
  if (d.model !== undefined) throw new Error(`${fault}a single-model lookup (${d.model}); prices come from \`pfm model-cost --json --all\``);
  if (typeof d.fetched_at !== "string" || !d.fetched_at) throw new Error(`${fault}no fetched_at`);
  if (!Array.isArray(d.sources) || !d.sources.length || !d.sources.every((s) => typeof s?.provider === "string" && typeof s.url === "string")) throw new Error(`${fault}sources must list each provider and url`);
  if (!Array.isArray(d.rows) || !d.rows.length) throw new Error(`${fault}no rows`);
  const keys = new Set();
  for (const row of d.rows) { const why = rowFault(row, keys); if (why) throw new Error(fault + why); keys.add(row.key); }
  if (d.override != null && (typeof d.override.path !== "string" || !Number.isInteger(d.override.rows) || d.override.rows < 0)) throw new Error(`${fault}invalid override metadata`);
  return { ...d, override: d.override ?? null, byKey: new Map(d.rows.map((row) => [row.key, row])) };
}
// The resolution rule pfm's Go resolver applies too (cases: pfm/internal/pricing/testdata/resolve-cases.json):
// normalize the id, then the exact key, else the key without a trailing snapshot date; no family fallback.
function keyOf(table, model) {
  let id = String(model ?? "").trim().toLowerCase();
  if (id.startsWith("anthropic/")) { id = id.slice(10); if (!id.startsWith("claude-")) return null; }
  else if (id.startsWith("openai/")) { id = id.slice(7); if (id.startsWith("claude-")) return null; }
  id = id.replace(/^(?:[a-z0-9-]+\.)?anthropic\./, "").replace(/\[[^\]]*\]$/, "").replace(/@.*$/, "");
  if (id.startsWith("claude-")) id = id.replace(/-v\d+(?::\d+)?$/, "").replaceAll(".", "-");
  if (table.byKey.has(id)) return id;
  const undated = id.replace(/-(?:\d{8}|\d{4}-\d{2}-\d{2})$/, "");
  return undated !== id && table.byKey.has(undated) ? undated : null;
}
// One row's rates at a call's context (input + cache read + cache write): above `long.above` the long
// tier's own rates, else the base ones. An unpublished rate is null, never 0.
function rateOf(table, model, context = 0) {
  const key = keyOf(table, model);
  if (!key) return null;
  const row = table.byKey.get(key), long = row.long && context > row.long.above, src = long ? row.long : row;
  const r = (k) => (rate(src[k]) ? src[k] : null);
  return { key, in: r("in"), out: r("out"), rd: r(row.engine === "codex" ? "cached" : "hit"), w5: r("w5m"), w1: r("w1h") };
}
let TABLE = null;
function useTable(table) { TABLE = table; }
const priceOverride = () => TABLE?.override ?? null;
const priceCatalog = () => TABLE ? { saved: TABLE.saved ?? null, fetched_at: TABLE.fetched_at, sources: TABLE.sources, served_from: TABLE.served_from ?? null, refresh: TABLE.refresh ?? null } : null;
const RATE = (model, context = 0, usage = {}) => {
  if (!TABLE) throw new Error("no price table: useTable(loadTable(env)) runs before any rate is read");
  const r = rateOf(TABLE, model, context);
  if (!r || Object.entries(usage).some(([key, count]) => count > 0 && !Number.isFinite(r[key]))) return null;
  return r;
};
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

export { loadTable, rateOf, useTable, priceOverride, priceCatalog, RATE, TOP4, usageOf, writesOf };
