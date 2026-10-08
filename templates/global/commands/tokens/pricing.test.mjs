// Live catalog projection and refusals; every source is synthetic, no network.
import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import * as P from "./lib/pricing.mjs";
import { fakePfm as fakePfmIn } from "./fake-pfm.mjs";

const HERE = path.dirname(fileURLToPath(import.meta.url));
const PRICES = path.join(HERE, "fixtures/model-cost.json");
const CATALOG = JSON.parse(fs.readFileSync(PRICES, "utf8"));
const TMP = fs.mkdtempSync(path.join(os.tmpdir(), "token-audit-pricing-test-"));
const fakePfm = (body) => fakePfmIn(TMP, body);
const file = (doc) => { const p = path.join(fs.mkdtempSync(path.join(TMP, "catalog-")), "prices.json"); fs.writeFileSync(p, typeof doc === "string" ? doc : JSON.stringify(doc)); return p; };
const loaded = (env) => { let result; assert.doesNotThrow(() => { result = P.loadTable(env); }, "a complete live catalog must load"); return result; };
const table = (doc = CATALOG) => loaded({ TOKEN_AUDIT_PRICES: file(doc) });
const copy = () => structuredClone(CATALOG);
const cells = (values) => values.map((text) => ({ text, numbers: [] }));
const openaiModel = (doc) => doc.catalogs[1].models[0];

test("live catalogs: exact model IDs preserve both Claude cache TTLs and OpenAI cache-write prices", () => {
  const t = table();
  for (const [id, expected] of [
    ["claude-opus-5-5", [4, 20, .2, 5, 8]], ["claude-fable-5-1", [10, 50, .25, 12.5, 20]],
    ["gpt-5.6-sol", [4, 20, .4, 5, null]],
  ]) {
    const r = P.rateOf(t, id);
    assert.ok(r, id);
    assert.deepEqual([r.in, r.out, r.rd, r.w5, r.w1], expected, id);
  }
  assert.equal(P.rateOf(t, "Anthropic/Claude-OPUS-5-5").in, 4);
  for (const id of ["opus", "claude-opus-5-5-20270101", "gpt-5.6-sol-pro", "gpt-5.6-sol-20990101", undefined]) assert.equal(P.rateOf(t, id), null, String(id));
});

test("live catalogs: table dimensions select Standard, not Batch, audio, training or tool tariffs", () => {
  const doc = copy(), m = openaiModel(doc), base = m.tables[0];
  m.tables.unshift({ ...base, heading: "Batch pricing data", rows: [cells([m.id, "$1", "$0.1", "$1.25", "$5", "$2", "$0.2", "$2.5", "$7.5"])] });
  m.tables.push({ ...base, heading: "Grouped Pricing Table data", headers: ["Model", "Input", "Cached input", "Output", "Training"], rows: [cells([m.id, "$99", "$9", "$199", "$299"])] });
  assert.equal(P.rateOf(table(doc), m.id).in, 4);
  doc.catalogs[1].models = [{ id: "gpt-audio", tables: [{ ...base, heading: "Audio tokens", rows: base.rows }] }];
  assert.equal(P.rateOf(table(doc), "gpt-audio"), null);
});

test("live catalogs: specialized Standard text-token rows price Codex without importing Fast or modality tariffs", () => {
  const doc = copy(), c = doc.catalogs[1];
  const base = { heading: "Grouped Pricing Table data", context: "Specialized models\nPrices per 1M tokens.\nStandard", headers: ["Category", "Model", "Input", "Cached input", "Output"], rows: [cells(["Codex", "gpt-5.3-codex", "$1.75", "$0.175", "$14.00"])] };
  c.models = [{ id: "gpt-5.3-codex", tables: [base, { ...base, context: "Fast", rows: [cells(["Codex", "gpt-5.3-codex", "$3.50", "$0.35", "$28.00"])] }] }];
  const r = P.rateOf(table(doc), "gpt-5.3-codex", 1000);
  assert.ok(r, "the published specialized Standard Codex row must be priced");
  assert.deepEqual([r.in, r.rd, r.out], [1.75, .175, 14]);
  c.models[0].tables = [{ ...base, context: "Standard\nPrior mode explanation\nFast" }];
  assert.equal(P.rateOf(table(doc), "gpt-5.3-codex"), null);
});

test("live catalogs: published long context columns use the exact boundary and each rate", () => {
  const doc = copy(), m = openaiModel(doc);
  m.tables[0].rows = [cells([m.id, "$2.000", "$0.10", "$2.500", "$10.00", "$4.00", "$0.20", "$5.00", "$15.00"])];
  const t = table(doc);
  const short = P.rateOf(t, m.id, 272000), long = P.rateOf(t, m.id, 272001);
  assert.deepEqual([short.in, short.rd, short.w5, short.out], [2, .1, 2.5, 10]);
  assert.deepEqual([long.in, long.rd, long.w5, long.out], [4, .2, 5, 15]);
  assert.equal(long.threshold, 272000);
  assert.equal(P.rateOf(t, m.id, 200001).in, 2, "the old 200K constant must not select a 272K premium");
  m.tables[0].rows[0][5].text = "-";
  assert.equal(P.rateOf(table(doc), m.id, 272001), null, "a missing long rate is unavailable");
});

test("live catalogs: Claude prompt length rows select the correct cache and output rates", () => {
  const doc = copy(), m = doc.catalogs[0].models[0];
  m.id = "claude-haiku-5-5";
  m.tables[0].rows = [cells(["Claude Haiku 5.5 (for prompts up to 100,000 tokens)", "$0.10 / MTok", "$0.125 / MTok", "$0.20 / MTok", "$0.01 / MTok", "$0.50 / MTok"]), cells(["Claude Haiku 5.5 (for prompts over 100,000 tokens)", "$0.50 / MTok", "$0.625 / MTok", "$1 / MTok", "$0.05 / MTok", "$2.50 / MTok"])];
  const t = table(doc);
  for (const [context, expected] of [[100000, [.1, .125, .2, .01, .5]], [100001, [.5, .625, 1, .05, 2.5]]]) {
    const r = P.rateOf(t, m.id, context);
    assert.deepEqual([r.in, r.w5, r.w1, r.rd, r.out], expected);
  }
});

test("live catalogs: missing or malformed amounts are unavailable, footnote numbers are not prices", () => {
  for (const text of ["-", "N/A", "", "$0.10–$0.20 / MTok", "$1 per image", "$oops", "$2 / 1K tokens"]) {
    const doc = copy(); doc.catalogs[0].models[0].tables[0].rows[0][1].text = text;
    assert.equal(P.rateOf(table(doc), "claude-sonnet-5"), null, text);
  }
  const doc = copy(); doc.catalogs[0].models[0].tables[0].rows[0][4].text = "$0.25 / MTok<sup>1</sup>";
  assert.equal(P.rateOf(table(doc), "claude-sonnet-5").rd, .25);
  doc.catalogs[0].models[0].tables[0].rows[0][4].text = "-";
  P.useTable(table(doc));
  assert.equal(P.RATE("claude-sonnet-5", 100, { rd: 1 }), null, "used missing cache rate must be n/a");
  assert.equal(P.RATE("claude-sonnet-5", 100, { in: 100 }).in, 2, "uncached usage can use published input/output");
});

test("live catalogs: duplicate conflicting Standard rows are unavailable, never chosen by order", () => {
  const doc = copy(), m = openaiModel(doc);
  for (const text of ["$99", "-", "$oops"]) {
    const variant = structuredClone(doc), conflictingModel = openaiModel(variant);
    const conflict = structuredClone(m.tables[0]); conflict.rows[0][1].text = text; conflictingModel.tables.push(conflict);
    assert.equal(P.rateOf(table(variant), m.id), null, text);
  }
});

test("useTable: cached model rates never outlive the loaded catalog", () => {
  P.useTable(table()); assert.equal(P.RATE("gpt-5.6-sol").in, 4);
  const doc = copy(); openaiModel(doc).tables[0].rows[0][1].text = "$9";
  P.useTable(table(doc)); assert.equal(P.RATE("gpt-5.6-sol").in, 9);
});

test("loadTable: pfm model-cost --json --all runs once and preserves its sources", () => {
  const spawns = path.join(TMP, "spawns");
  const bin = fakePfm(`[ "$1 $2 $3" = "model-cost --json --all" ] || exit 9; echo x >> '${spawns}'; cat '${PRICES}'`);
  const t = loaded({ TOKEN_AUDIT_PFM: bin });
  assert.equal(fs.readFileSync(spawns, "utf8"), "x\n");
  assert.equal(t.override, null);
  assert.equal(t.catalogs[0].source.fetched_at, CATALOG.catalogs[0].source.fetched_at);
  assert.equal(P.rateOf(t, "gpt-5.6-sol").in, 4);
});

test("loadTable: a saved catalog keeps pfm unspawned and exposes its override path", () => {
  const bin = fakePfm("exit 9"), t = loaded({ TOKEN_AUDIT_PRICES: PRICES, TOKEN_AUDIT_PFM: bin });
  assert.equal(t.saved, PRICES);
  assert.equal(t.override, null);
});

test("loadTable: failures, timeouts and old pfm are visible errors, never static fallback", () => {
  const missing = path.join(TMP, "no-such-pfm");
  assert.throws(() => P.loadTable({ TOKEN_AUDIT_PFM: missing }), /pfm not found.*model-cost --json --all/);
  assert.throws(() => P.loadTable({ TOKEN_AUDIT_PFM: fakePfm('echo "model-cost: fetch HTTP 503" >&2; exit 3') }), /failed \(exit 3\): model-cost: fetch HTTP 503/);
  assert.throws(() => P.loadTable({ TOKEN_AUDIT_PFM: fakePfm('echo \'unknown command "model-cost"\' >&2; exit 2') }), /predates `pfm model-cost`; update pfm/);
  assert.throws(() => P.loadTable({ TOKEN_AUDIT_PFM: fakePfm("exec sleep 5") }, 200), /timed out after 0.2 s/);
  assert.throws(() => P.loadTable({ TOKEN_AUDIT_PRICES: missing }), /TOKEN_AUDIT_PRICES.*ENOENT/);
});

test("loadTable: malformed or incomplete catalogs fail visibly instead of empty prices", () => {
  const malformed = ["{ not JSON", { catalogs: [] }, { catalogs: CATALOG.catalogs.slice(0, 1) }, { version: 1, rows: [] }];
  for (const field of ["models", "tables", "source"]) { const doc = copy(); doc.catalogs[0][field] = null; malformed.push(doc); }
  const cellsBad = copy(); cellsBad.catalogs[0].models[0].tables[0].rows[0][1] = { numbers: ["1"] }; malformed.push(cellsBad);
  for (const doc of malformed) assert.throws(() => P.loadTable({ TOKEN_AUDIT_PRICES: file(doc) }), /TOKEN_AUDIT_PRICES.*(catalog|source|table|model|JSON|Unexpected|text)/i);
});
