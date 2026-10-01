#!/usr/bin/env node
// check-token-pricing.mjs — resolves the shipped PRICING table against real
// published model ids and asserts each lands on its intended rate.
//
// WHY THIS EXISTS: PRICING is matched by `PRICING.find(([sub]) => id.includes(sub))`
// — first substring hit on the lowercased id wins — so a row's correctness depends
// on BOTH its rate and its position, and a row whose substring never occurs in any
// real id is dead code that silently falls through to whatever catches it next.
// That is not a hypothetical: a literal "opus-4-0" row matched nothing, because
// Opus 4.0's id carries no minor digit (claude-opus-4-<date>), so every Opus 4.0
// transcript priced at the 5/25 catch-all instead of 15/75 — a 3x undercount that
// reading the table could not reveal, only resolving it could.
//
// WHAT THIS REPORTS WHEN IT IS ITSELF BROKEN: a table it cannot locate or parse
// exits 2 with PRICING-UNREADABLE, never a pass; an expectation naming a rate no
// row provides exits 1 naming both sides. "Every id resolved as intended" and
// "the table could not be read" are different results and never print the same.

import fs from "node:fs";
import path from "node:path";

const AUDIT = path.join("templates", "global", "commands", "tokens", "token-audit.mjs");

// Published ids -> [input, output, cache hit, 5m write, 1h write], USD per MTok, as
// published. Write rates are null where the engine bills no cache write (Codex). Add a row
// here whenever a tier is added or re-priced; every PRICING row must be reached by one.
const EXPECT = [
  ["gpt-6-astra", 10.0, 50.0, 1.0, null, null],
  ["gpt-6.1-sol", 2.0, 10.0, 0.1, null, null],
  ["gpt-6-sol", 2.0, 10.0, 0.2, null, null],
  ["gpt-5.6-sol", 4.0, 20.0, 0.4, null, null],
  ["gpt-5.6-luna", 0.2, 1.2, 0.02, null, null],
  ["gpt-5.6-terra", 2.0, 12.0, 0.2, null, null],
  ["gpt-5.4", 2.5, 15.0, 0.25, null, null],
  ["gpt-5.3-codex", 1.75, 14.0, 0.175, null, null],
  ["claude-fable-5-1", 10.0, 50.0, 0.25, 12.5, 20.0],
  ["claude-mythos-5-1", 10.0, 50.0, 0.25, 12.5, 20.0],
  ["claude-fable-5", 10.0, 50.0, 1.0, 12.5, 20.0],
  ["claude-mythos-5", 10.0, 50.0, 1.0, 12.5, 20.0],
  ["claude-opus-5-5", 4.0, 20.0, 0.2, 5.0, 8.0],
  ["claude-opus-5", 5.0, 25.0, 0.5, 6.25, 10.0],
  ["claude-opus-4-8", 5.0, 25.0, 0.5, 6.25, 10.0],
  ["claude-opus-4-7", 5.0, 25.0, 0.5, 6.25, 10.0],
  ["claude-opus-4-6", 5.0, 25.0, 0.5, 6.25, 10.0],
  ["claude-opus-4-5-20251101", 5.0, 25.0, 0.5, 6.25, 10.0],
  ["claude-opus-4-1-20250805", 15.0, 75.0, 1.5, 18.75, 30.0],
  ["claude-opus-4-20250514", 15.0, 75.0, 1.5, 18.75, 30.0],
  ["claude-opus-4@20250514", 15.0, 75.0, 1.5, 18.75, 30.0],
  ["claude-sonnet-5-5", 2.0, 10.0, 0.2, 2.5, 4.0],
  ["claude-sonnet-5", 2.0, 10.0, 0.2, 2.5, 4.0],
  ["claude-sonnet-4-6", 3.0, 15.0, 0.3, 3.75, 6.0],
  ["claude-sonnet-4-5-20250929", 3.0, 15.0, 0.3, 3.75, 6.0],
  ["claude-sonnet-4-20250514", 3.0, 15.0, 0.3, 3.75, 6.0],
  ["claude-3-7-sonnet-20250219", 3.0, 15.0, 0.3, 3.75, 6.0],
  ["claude-haiku-4-5-20251001", 1.0, 5.0, 0.1, 1.25, 2.0],
  ["claude-3-5-haiku-20241022", 0.8, 4.0, 0.08, 1.0, 1.6],
];

let source;
try {
  source = fs.readFileSync(AUDIT, "utf8");
} catch (error) {
  console.error(`PRICING-UNREADABLE cannot read ${AUDIT}: ${error.message}`);
  process.exit(2);
}

const block = source.match(/const PRICING = \[([\s\S]*?)\n\];/);
if (!block) {
  console.error(`PRICING-UNREADABLE no "const PRICING = [...]" block in ${AUDIT} — the table moved or was renamed; this check verified NOTHING`);
  process.exit(2);
}

let table;
try {
  // The table is literal rows of strings and numbers; drop the // comments and the
  // trailing comma and it is JSON — parsed as data, never executed as code.
  const rows = block[1].replace(/\/\/[^\n]*/g, "").trim().replace(/,$/, "");
  table = JSON.parse(`[${rows}]`);
} catch (error) {
  console.error(`PRICING-UNREADABLE table did not parse: ${error.message}`);
  process.exit(2);
}
if (!Array.isArray(table) || table.length === 0) {
  console.error("PRICING-UNREADABLE table parsed empty — refusing to report clean against zero rows");
  process.exit(2);
}

// The write rates below are derived exactly as token-audit's RATE derives them. If RATE stops
// deriving them that way, this check would verify writes the audit no longer bills: refuse.
if (!source.includes("w5: row[1] * 1.25, w1: row[1] * 2")) {
  console.error(`PRICING-UNREADABLE RATE in ${AUDIT} no longer derives cache writes as 1.25x / 2x input — this check's write rates verified NOTHING`);
  process.exit(2);
}

const priceFor = (model) => {
  const id = String(model || "").toLowerCase();
  return table.find(([sub]) => id.includes(sub)) || null;
};

const failures = [];
const eq = (a, b) => Math.abs(a - b) < 1e-9;
for (const [id, ...want] of EXPECT) {
  const row = priceFor(id);
  if (!row) {
    failures.push(`${id}: no PRICING row matches — would render cost "n/a"`);
    continue;
  }
  const got = [row[1], row[2], row[3], row[1] * 1.25, row[1] * 2];
  const bad = ["in", "out", "hit", "w5m", "w1h"].filter((_, k) => want[k] !== null && !eq(got[k], want[k]));
  if (bad.length) {
    failures.push(`${id}: matched "${row[0]}" -> ${bad.map((k) => { const i = ["in", "out", "hit", "w5m", "w1h"].indexOf(k); return `${k} ${got[i]} want ${want[i]}`; }).join(", ")}`);
  }
}

// A row no published id reaches is dead: it cannot price anything, and its
// presence reads as coverage the table does not have.
const reached = new Set(EXPECT.map(([id]) => (priceFor(id) || [])[0]).filter(Boolean));
const dead = table.filter(([sub]) => !reached.has(sub)).map(([sub]) => sub);

if (failures.length) {
  for (const line of failures) console.error(`PRICING-WRONG ${line}`);
  console.error(`token-pricing: ${failures.length} of ${EXPECT.length} id(s) priced wrong`);
  process.exit(1);
}

console.log(
  `token-pricing: ${EXPECT.length} published id(s) resolved against ${table.length} row(s), every in/out/hit/5m-write/1h-write rate as intended` +
    (dead.length ? `; ${dead.length} row(s) unreached by this fixture: ${dead.join(", ")}` : "")
);
