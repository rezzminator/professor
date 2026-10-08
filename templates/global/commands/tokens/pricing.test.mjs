// The `pfm model-cost --json --all` document, its resolution rule and refusals; every source is synthetic, no network.
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
const DOC = JSON.parse(fs.readFileSync(PRICES, "utf8"));
// The resolution cases pfm's Go resolver is tested against: one rule, two implementations.
const CASES = JSON.parse(fs.readFileSync(path.join(HERE, "../../../../pfm/internal/pricing/testdata/resolve-cases.json"), "utf8"));
const TMP = fs.mkdtempSync(path.join(os.tmpdir(), "token-audit-pricing-test-"));
const fakePfm = (body) => fakePfmIn(TMP, body);
const file = (doc) => { const p = path.join(fs.mkdtempSync(path.join(TMP, "table-")), "prices.json"); fs.writeFileSync(p, typeof doc === "string" ? doc : JSON.stringify(doc)); return p; };
const loaded = (env) => { let result; assert.doesNotThrow(() => { result = P.loadTable(env); }, "a valid price document must load"); return result; };
const table = (doc = DOC) => loaded({ TOKEN_AUDIT_PRICES: file(doc) });
const copy = () => structuredClone(DOC);
const withRows = (rows) => ({ ...copy(), rows });
const rates = (r) => r && [r.in, r.out, r.rd, r.w5, r.w1];

test("resolution: every shared case resolves to its key or stays unpriced", () => {
  const t = table(withRows(CASES.keys.map((key) => ({ key, engine: key.startsWith("claude-") ? "claude" : "codex", in: 1, out: 2 }))));
  for (const c of CASES.cases) assert.equal(P.rateOf(t, c.id)?.key ?? null, c.key, JSON.stringify(c.id));
});

test("rows: claude hit/w5m/w1h and codex cached map onto the read and write rates; an unpublished rate is null", () => {
  const t = table();
  for (const [id, expected] of [
    ["claude-opus-5-5", [4, 20, .2, 5, 8]], ["claude-fable-5-1", [10, 50, .25, 12.5, 20]],
    ["claude-haiku-4-5-20251001", [1, 5, .1, 1.25, 2]], ["gpt-5.6-sol", [4, 20, .4, null, null]],
  ]) assert.deepEqual(rates(P.rateOf(t, id)), expected, id);
  assert.equal(P.rateOf(t, "claude-unobtanium-9"), null);
});

test("long tier: above the threshold only the long rates apply, at or below it the base rates", () => {
  const t = table(withRows([
    { key: "claude-haiku-5-5", engine: "claude", in: .1, out: .5, hit: .01, w5m: .125, w1h: .2, long: { above: 100000, in: .5, out: 2.5, hit: .05, w5m: .625, w1h: 1 } },
    { key: "gpt-5.5-pro", engine: "codex", in: 30, out: 180, cached: 3, long: { above: 272000, in: 60, out: 270 } },
  ]));
  assert.deepEqual(rates(P.rateOf(t, "claude-haiku-5-5", 100000)), [.1, .5, .01, .125, .2]);
  assert.deepEqual(rates(P.rateOf(t, "claude-haiku-5-5", 100001)), [.5, 2.5, .05, .625, 1]);
  assert.deepEqual(rates(P.rateOf(t, "claude-haiku-5-5")), [.1, .5, .01, .125, .2], "no context is the base tier");
  assert.deepEqual(rates(P.rateOf(t, "gpt-5.5-pro", 272001)), [60, 270, null, null, null], "a long tier never inherits the base cached rate");
  P.useTable(t);
  assert.equal(P.RATE("gpt-5.5-pro", 272001, { in: 10, rd: 5 }), null, "a used rate the long tier does not publish is unpriced");
  assert.equal(P.RATE("gpt-5.5-pro", 272001, { in: 10, rd: 0, out: 1 }).in, 60, "an unused unpublished rate leaves the call priced");
  assert.equal(P.RATE("gpt-5.5-pro", 272000, { rd: 5 }).rd, 3);
});

test("useTable: rates never outlive the loaded table", () => {
  P.useTable(table()); assert.equal(P.RATE("gpt-5.6-sol").in, 4);
  const doc = copy(); doc.rows.find((r) => r.key === "gpt-5.6-sol").in = 9;
  P.useTable(table(doc)); assert.equal(P.RATE("gpt-5.6-sol").in, 9);
});

test("loadTable: the tracked prices.json file itself loads, with no override", () => {
  const { version, fetched_at, sources, rows } = DOC;
  const t = table({ version, fetched_at, sources, rows: rows.map(({ source, ...row }) => row) });
  assert.equal(t.override, null);
  assert.equal(P.rateOf(t, "claude-sonnet-5").in, 2);
});

test("priceCatalog: the saved path, fetch time, sources and refresh outcome reach the report", () => {
  const doc = copy(); doc.refresh = { status: "failed", error: "anthropic pricing page: HTTP 503" }; doc.override = { path: "/tmp/demo/pfm.prices.json", rows: 2 };
  const saved = file(doc);
  P.useTable(loaded({ TOKEN_AUDIT_PRICES: saved }));
  assert.deepEqual(P.priceCatalog(), { saved, fetched_at: DOC.fetched_at, sources: DOC.sources, served_from: "embedded", refresh: doc.refresh });
  assert.deepEqual(P.priceOverride(), doc.override);
});

test("loadTable: pfm model-cost --json --all runs once and its document is read", () => {
  const spawns = path.join(TMP, "spawns");
  const bin = fakePfm(`[ "$1 $2 $3" = "model-cost --json --all" ] || exit 9; echo x >> '${spawns}'; cat '${PRICES}'`);
  const t = loaded({ TOKEN_AUDIT_PFM: bin });
  assert.equal(fs.readFileSync(spawns, "utf8"), "x\n");
  assert.equal(t.override, null);
  assert.equal(t.saved, undefined);
  assert.equal(P.rateOf(t, "gpt-5.6-sol").in, 4);
});

test("loadTable: a saved document keeps pfm unspawned and records its path", () => {
  const bin = fakePfm("exit 9"), t = loaded({ TOKEN_AUDIT_PRICES: PRICES, TOKEN_AUDIT_PFM: bin });
  assert.equal(t.saved, PRICES);
});

test("loadTable: failures, timeouts and old pfm are visible errors, never static fallback", () => {
  const missing = path.join(TMP, "no-such-pfm");
  assert.throws(() => P.loadTable({ TOKEN_AUDIT_PFM: missing }), /pfm not found.*model-cost --json --all/);
  assert.throws(() => P.loadTable({ TOKEN_AUDIT_PFM: fakePfm('echo "model-cost: invalid override" >&2; exit 1') }), /failed \(exit 1\): model-cost: invalid override/);
  assert.throws(() => P.loadTable({ TOKEN_AUDIT_PFM: fakePfm('echo \'unknown command "model-cost"\' >&2; exit 2') }), /predates `pfm model-cost`; update pfm/);
  assert.throws(() => P.loadTable({ TOKEN_AUDIT_PFM: fakePfm("exec sleep 5") }, 200), /timed out after 0.2 s/);
  assert.throws(() => P.loadTable({ TOKEN_AUDIT_PRICES: missing }), /TOKEN_AUDIT_PRICES.*ENOENT/);
});

test("loadTable: a document from a pfm before the version 2 table names the update", () => {
  for (const doc of [{ catalogs: [], coverage: "x" }, { version: 1, rows: [] }]) {
    assert.throws(() => P.loadTable({ TOKEN_AUDIT_PRICES: file(doc) }), /TOKEN_AUDIT_PRICES.*version 2.*update pfm/, JSON.stringify(doc));
  }
});

test("loadTable: malformed documents and rows fail visibly instead of empty prices", () => {
  const row = (patch) => withRows([{ key: "claude-sonnet-5", engine: "claude", in: 2, out: 10, ...patch }]);
  const cases = {
    "not JSON": "{ not JSON",
    "no rows": { ...copy(), rows: undefined },
    "empty rows": withRows([]),
    "no fetched_at": { ...copy(), fetched_at: undefined },
    "bad sources": { ...copy(), sources: [{ provider: "anthropic" }] },
    "a single-model lookup": { ...copy(), model: "claude-sonnet-5" },
    "bad override": { ...copy(), override: { path: "/tmp/x", rows: -1 } },
    "missing in": row({ in: undefined }),
    "negative out": row({ out: -1 }),
    "string rate": row({ hit: "0.2" }),
    "codex column on a claude row": row({ cached: .1 }),
    "claude column on a codex row": withRows([{ key: "gpt-5.5", engine: "codex", in: 1, out: 2, w5m: 1 }]),
    "unknown engine": row({ engine: "gemini" }),
    "unknown column": row({ batch: 1 }),
    "duplicate key": withRows([{ key: "gpt-5.5", engine: "codex", in: 1, out: 2 }, { key: "gpt-5.5", engine: "codex", in: 1, out: 2 }]),
    "long without above": row({ long: { in: 4 } }),
    "long foreign column": row({ long: { above: 10, cached: 1 } }),
  };
  for (const [name, doc] of Object.entries(cases)) {
    assert.throws(() => P.loadTable({ TOKEN_AUDIT_PRICES: file(doc) }), /^Error: TOKEN_AUDIT_PRICES .*prices\.json: ./, name);
  }
});
