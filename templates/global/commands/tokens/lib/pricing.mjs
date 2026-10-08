// pricing.mjs — the live `pfm model-cost` catalogs and the usage they price.
import fs from "node:fs";
import { spawnSync } from "node:child_process";

// The CLI owns provider fetching and its process-memory cache. Explicit saved catalogs
// are useful for reproducible estimates; there is no bundled/static fallback.
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
  return tableOf(r.stdout, `${cmd} returned an unreadable catalog: `);
}
function tableOf(text, fault) {
  let d; try { d = JSON.parse(text); } catch (e) { throw new Error(fault + e.message); }
  if (!Array.isArray(d?.catalogs) || d.catalogs.length !== 2) throw new Error(`${fault}catalogs must include both providers`);
  const providers = new Set();
  for (const catalog of d.catalogs) {
    if (!["anthropic", "openai"].includes(catalog?.provider) || providers.has(catalog.provider) || catalog.currency !== "USD") throw new Error(`${fault}invalid catalog provider/currency`);
    providers.add(catalog.provider);
    if (!Array.isArray(catalog.models) || !catalog.models.length || !Array.isArray(catalog.tables) || typeof catalog.page_context !== "string") throw new Error(`${fault}incomplete catalog models/tables/context`);
    if (!catalog.source || typeof catalog.source.url !== "string" || typeof catalog.source.fetched_at !== "string" || typeof catalog.source.markdown !== "string") throw new Error(`${fault}missing catalog source evidence`);
    const ids = new Set();
    for (const model of catalog.models) {
      if (typeof model?.id !== "string" || !model.id || ids.has(model.id) || !Array.isArray(model.tables) || !model.tables.length) throw new Error(`${fault}invalid/duplicate catalog model`);
      ids.add(model.id);
      for (const table of model.tables) {
        if (typeof table?.heading !== "string" || typeof table.context !== "string" || !Array.isArray(table.headers) || !table.headers.every((h) => typeof h === "string") || !Array.isArray(table.rows) || !table.rows.length) throw new Error(`${fault}invalid model table`);
        for (const row of table.rows) if (!Array.isArray(row) || row.length !== table.headers.length || !row.every((c) => typeof c?.text === "string")) throw new Error(`${fault}invalid model table cell text`);
      }
    }
  }
  if (d.override != null && (typeof d.override.path !== "string" || !Number.isInteger(d.override.rows) || d.override.rows < 0)) throw new Error(`${fault}invalid override metadata`);
  return { ...d, override: d.override ?? null };
}
const plain = (s) => s.replace(/<[^>]*>/g, "").replace(/[*`]/g, "").trim();
const amount = (s) => {
  // Footnotes are annotations: a trailing <sup>1</sup> is never another price.
  const text = s.replace(/<sup>[^<]*<\/sup>/gi, "").trim();
  const m = /^\$\s*([0-9]+(?:,[0-9]{3})*(?:\.[0-9]+)?)(?:\s*\/\s*(?:MTok|1M tokens))?$/i.exec(text);
  const n = m ? Number(m[1].replaceAll(",", "")) : NaN;
  return Number.isFinite(n) ? n : null;
};
const thresholdNumber = (s) => {
  const m = /([0-9]+(?:,[0-9]{3})*(?:\.[0-9]+)?)\s*([kKmM]?)/.exec(s);
  return m ? Number(m[1].replaceAll(",", "")) * ({ k: 1000, m: 1000000 }[m[2].toLowerCase()] || 1) : null;
};
// Only documented standard text-token tables feed transcript estimates. All other
// tariffs remain in the `pfm model-cost` catalog for callers whose usage identifies them.
function ratesOf(catalog, model) {
  const variants = [];
  for (const table of model.tables) {
    const heading = plain(table.heading).toLowerCase(), headers = table.headers.map((h) => plain(h).toLowerCase());
    const claude = catalog.provider === "anthropic";
    const serviceMode = [...table.context.matchAll(/^\s*(Standard|Batch|Fast|Ultrafast)\s*$/gm)].at(-1)?.[1];
    const specializedStandard = headers.includes("category") && headers.includes("input") && headers.includes("output") && serviceMode === "Standard";
    if (claude ? heading !== "model pricing" : heading !== "standard pricing data" && !(heading === "grouped pricing table data" && (headers.includes("short context input") || specializedStandard))) continue;
    const idx = (names) => headers.findIndex((h) => names.includes(h));
    const indices = {
      in: idx(claude ? ["base input tokens", "input tokens"] : ["short context input", "input"]),
      out: idx(claude ? ["output tokens"] : ["short context output", "output"]),
      rd: idx(claude ? ["cache hits and refreshes", "cache reads"] : ["short context cached input", "cached input"]),
      w5: idx(claude ? ["5m cache writes"] : ["short context cache writes", "cache writes"]),
      w1: idx(["1h cache writes"]),
    };
    if (indices.in < 0 || indices.out < 0 || (!claude && !/prices per 1m tokens/i.test(catalog.page_context))) continue;
    for (const cells of table.rows) {
      const rates = Object.fromEntries(Object.entries(indices).map(([key, i]) => [key, i < 0 ? null : amount(cells[i].text)]));
      let min = 0, max = Infinity, threshold = Infinity;
      const label = plain(cells[headers.indexOf("model")]?.text || "");
      const condition = /for prompts (up to|over) ([\d,]+) tokens/i.exec(label);
      if (condition) {
        threshold = thresholdNumber(condition[2]);
        if (condition[1].toLowerCase() === "over") min = threshold; else max = threshold;
      } else if (/for prompts/i.test(label)) continue;
      variants.push({ ...rates, min, max, threshold });
      const longs = Object.fromEntries(["in", "out", "rd", "w5"].map((key) => {
        const i = idx([`long context ${{ in: "input", out: "output", rd: "cached input", w5: "cache writes" }[key]}`]);
        return [key, i < 0 ? null : amount(cells[i].text)];
      }));
      if (!claude && headers.includes("long context input")) {
        const boundary = /long context:\s*>\s*([\d,.]+[kKmM]?)\s*input tokens/i.exec(catalog.page_context);
        const limit = boundary ? thresholdNumber(boundary[1]) : null;
        if (limit === null) { variants.pop(); continue; }
        variants[variants.length - 1].max = limit;
        variants[variants.length - 1].threshold = limit;
        variants.push({ ...rates, ...longs, min: limit, max: Infinity, threshold: limit });
      }
    }
  }
  return variants;
}
const normalizedID = (model) => {
  let id = String(model || "").trim().toLowerCase();
  const slash = id.indexOf("/");
  if (slash >= 0) {
    const provider = id.slice(0, slash); id = id.slice(slash + 1);
    if (!["anthropic", "openai"].includes(provider) || (provider === "anthropic") !== id.startsWith("claude-")) return "";
  }
  return id.startsWith("claude-") ? id.replaceAll(".", "-") : id;
};
const CANDIDATES = new WeakMap();
function rateOf(table, model, context = 0) {
  const id = normalizedID(model);
  let memo = CANDIDATES.get(table); if (!memo) { memo = new Map(); CANDIDATES.set(table, memo); }
  if (!memo.has(id)) {
    const variants = [];
    for (const catalog of table.catalogs) for (const entry of catalog.models) if (entry.id === id) variants.push(...ratesOf(catalog, entry));
    memo.set(id, variants);
  }
  const applicable = memo.get(id).filter((r) => (context > r.min || (context === 0 && r.min === 0)) && context <= r.max);
  if (!applicable.length) return null;
  const [r] = applicable;
  if (r.in === null || r.out === null || applicable.some((other) => ["in", "out", "rd", "w5", "w1"].some((key) => other[key] !== r[key]))) return null;
  return r;
}
let TABLE = null;
function useTable(table) { TABLE = table; }
const priceOverride = () => TABLE?.override ?? null;
const priceCatalog = () => TABLE ? { saved: TABLE.saved ?? null, sources: TABLE.catalogs.map((c) => ({ provider: c.provider, url: c.source.url, fetched_at: c.source.fetched_at })) } : null;
const RATE = (model, context = 0, usage = {}) => {
  if (!TABLE) throw new Error("no price catalog: useTable(loadTable(env)) runs before any rate is read");
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
