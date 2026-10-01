// pricing.test.mjs — run with: node --test templates/global/commands/tokens/
// lib/pricing.mjs against pfm's own files, read where pfm keeps them: the shipped table
// pfm/internal/pricing/prices.json and the published-rates fixture beside it in testdata/.
import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import * as P from "./lib/pricing.mjs";

const HERE = path.dirname(fileURLToPath(import.meta.url));
const PRICES = path.join(HERE, "../../../../pfm/internal/pricing/prices.json");
const PUBLISHED = JSON.parse(fs.readFileSync(path.join(HERE, "../../../../pfm/internal/pricing/testdata/published-rates.json"), "utf8")).ids;
const SHIPPED = JSON.parse(fs.readFileSync(PRICES, "utf8")).rows;
const TMP = fs.mkdtempSync(path.join(os.tmpdir(), "token-audit-pricing-test-"));
const NO_PFM = path.join(TMP, "no-such-pfm");

// A stand-in pfm: a shell script written into TMP.
function fakePfm(body) {
  const p = path.join(fs.mkdtempSync(path.join(TMP, "pfm-")), "pfm");
  fs.writeFileSync(p, `#!/bin/sh\n${body}\n`);
  fs.chmodSync(p, 0o755);
  return p;
}
const file = (name, text) => { const p = path.join(fs.mkdtempSync(path.join(TMP, "table-")), name); fs.writeFileSync(p, text); return p; };
const row = (key, match, rate) => ({ key, engine: "claude", match, in: rate, out: 1, hit: 0, w5m: 0, w1h: 0, long_in: 1, long_out: 1 });

test("parity: every published id resolves over pfm's shipped table to its published rates", () => {
  const table = { rows: SHIPPED, override: null };
  for (const p of PUBLISHED) {
    const r = P.rateOf(table, p.id);
    assert.ok(r, `${p.id}: unpriced`);
    const want = "hit" in p ? { in: p.in, out: p.out, rd: p.hit, w5: p.w5m, w1: p.w1h } : { in: p.in, out: p.out, rd: p.cached };
    for (const [k, v] of Object.entries(want)) assert.equal(r[k], v, `${p.id}: ${k} is ${r[k]}, published ${v}`);
  }
});

test("rate shape: a Claude row maps hit/w5m/w1h, a Codex row derives writes from input, both carry the long-context multipliers", () => {
  const claude = { key: "c", engine: "claude", match: ["c-model"], in: 3, out: 15, hit: 0.3, w5m: 3.75, w1h: 6, long_in: 2, long_out: 1.5 };
  const codex = { key: "x", engine: "codex", match: ["x-model"], in: 2, out: 10, cached: 0.2, long_in: 1, long_out: 1 };
  const table = { rows: [claude, codex], override: null };
  assert.deepEqual(P.rateOf(table, "c-model"), { in: 3, out: 15, rd: 0.3, w5: 3.75, w1: 6, lcIn: 2, lcOut: 1.5 });
  assert.deepEqual(P.rateOf(table, "x-model"), { in: 2, out: 10, rd: 0.2, w5: 2 * 1.25, w1: 2 * 2, lcIn: 1, lcOut: 1 });
});

test("order-free: the shipped rows reversed or shuffled resolve every published id identically", () => {
  const base = { rows: SHIPPED, override: null };
  let seed = 7;
  const rand = () => ((seed = (seed * 1103515245 + 12345) % 2147483648) / 2147483648);
  const orders = [[...SHIPPED].reverse(), ...[1, 2, 3, 4, 5].map(() => [...SHIPPED].map((r) => [rand(), r]).sort((a, b) => a[0] - b[0]).map(([, r]) => r))];
  for (const rows of orders) for (const { id } of PUBLISHED) assert.deepEqual(P.rateOf({ rows, override: null }, id), P.rateOf(base, id), `${id} resolves differently with rows ${rows.map((r) => r.key).join(",")}`);
});

test("tie / case / unpriced: equal-length patterns go to the earliest start; matching ignores case; an unknown id is null", () => {
  for (const rows of [[row("beta", ["beta"], 2), row("alfa", ["alfa"], 1)], [row("alfa", ["alfa"], 1), row("beta", ["beta"], 2)]]) {
    const t = { rows, override: null };
    assert.equal(P.rateOf(t, "x-alfa-beta").in, 1, "alfa starts first in x-alfa-beta");
    assert.equal(P.rateOf(t, "x-beta-alfa").in, 2, "beta starts first in x-beta-alfa");
  }
  assert.equal(P.rateOf({ rows: SHIPPED, override: null }, "Claude-OPUS-5-5").in, 4, "a mixed-case id resolves like its lowercase form");
  assert.equal(P.rateOf({ rows: [row("m", ["Mixed-Pattern"], 9)], override: null }, "x-mixed-pattern-1").in, 9, "a mixed-case pattern matches its lowercase form");
  assert.equal(P.rateOf({ rows: SHIPPED, override: null }, "unobtanium-9"), null);
  assert.equal(P.rateOf({ rows: SHIPPED, override: null }, undefined), null);
});

test("useTable: RATE(m) consults the table set last", () => {
  P.useTable({ rows: [row("a", ["model-a"], 1)], override: null });
  assert.equal(P.RATE("model-a").in, 1);
  P.useTable({ rows: [row("a", ["model-a"], 5)], override: null });
  assert.equal(P.RATE("model-a").in, 5, "a cached rate must not outlive its table");
  assert.equal(P.RATE("model-b"), null);
});

test("loadTable: TOKEN_AUDIT_PRICES reads the shipped prices.json or a saved `pfm price --json` document, and pfm is not spawned", () => {
  const spawns = path.join(TMP, "spawns-file"), bin = fakePfm(`echo x >> '${spawns}'; exit 1`);
  const shipped = P.loadTable({ TOKEN_AUDIT_PRICES: PRICES, TOKEN_AUDIT_PFM: bin });
  assert.deepEqual(shipped, { rows: SHIPPED, override: null });
  const override = { path: "/cfg/pfm.prices.json", rows: 1 };
  const doc = file("price.json", JSON.stringify({ version: 1, override, rows: [{ ...row("a", ["model-a"], 1), source: "override" }] }));
  assert.deepEqual(P.loadTable({ TOKEN_AUDIT_PRICES: doc, TOKEN_AUDIT_PFM: bin }).override, override);
  assert.equal(fs.existsSync(spawns), false, "a table file must keep pfm unspawned");
});

test("loadTable: an unreadable TOKEN_AUDIT_PRICES names the variable, the path and the fault", () => {
  const missing = path.join(TMP, "no-such-table.json");
  assert.throws(() => P.loadTable({ TOKEN_AUDIT_PRICES: missing, TOKEN_AUDIT_PFM: NO_PFM }), { message: new RegExp(`^TOKEN_AUDIT_PRICES ${missing}: .*ENOENT`) });
  const notJson = file("bad.json", "{ not json");
  assert.throws(() => P.loadTable({ TOKEN_AUDIT_PRICES: notJson, TOKEN_AUDIT_PFM: NO_PFM }), { message: new RegExp(`^TOKEN_AUDIT_PRICES ${notJson}: \\S`) });
  const v2 = file("v2.json", JSON.stringify({ version: 2, rows: [] }));
  assert.throws(() => P.loadTable({ TOKEN_AUDIT_PRICES: v2, TOKEN_AUDIT_PFM: NO_PFM }), { message: new RegExp(`^TOKEN_AUDIT_PRICES ${v2}: .*version`) });
});

test("loadTable: pfm's `price --json` document is the table, override included", () => {
  const override = { path: "/cfg/pfm.prices.json", rows: 2 };
  const doc = file("doc.json", JSON.stringify({ version: 1, override, rows: SHIPPED.map((r) => ({ ...r, source: "shipped" })) }));
  const t = P.loadTable({ TOKEN_AUDIT_PFM: fakePfm(`[ "$1 $2" = "price --json" ] || exit 9; cat '${doc}'`) });
  assert.deepEqual(t.override, override);
  assert.equal(t.rows.length, SHIPPED.length);
});

test("loadTable: a missing pfm is named, with no fallback table", () => {
  assert.throws(() => P.loadTable({ TOKEN_AUDIT_PRICES: "", TOKEN_AUDIT_PFM: NO_PFM }),
    { message: `pfm not found (${NO_PFM}) — prices come from \`pfm price --json\`; install pfm or set TOKEN_AUDIT_PFM` });
});

test("loadTable: a pfm that exits non-zero carries its exit code and trimmed stderr", () => {
  const bin = fakePfm(`echo "  pfm: pfm.prices.json: unknown field  " >&2; exit 1`);
  assert.throws(() => P.loadTable({ TOKEN_AUDIT_PFM: bin }), { message: `\`${bin} price --json\` failed (exit 1): pfm: pfm.prices.json: unknown field` });
});

test("loadTable: not JSON, version 2, or rows not an array is an unreadable table", () => {
  for (const [out, fault] of [["price table", /\S/], ['{"version":2,"rows":[]}', /version/], ['{"version":1,"rows":{}}', /rows/]]) {
    const bin = fakePfm(`echo '${out}'`);
    assert.throws(() => P.loadTable({ TOKEN_AUDIT_PFM: bin }), (e) => {
      const head = `\`${bin} price --json\` returned an unreadable table: `;
      assert.ok(e.message.startsWith(head), `${out}: ${e.message}`);
      assert.match(e.message.slice(head.length), fault, `${out}: ${e.message}`);
      return true;
    });
  }
});
