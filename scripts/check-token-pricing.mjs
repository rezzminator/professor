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

// Published ids -> the rate the shipped table intends for them, per the comments
// in PRICING itself. Add a row here whenever a tier is added or re-priced.
const EXPECT = [
  ["gpt-6-astra", 10.0, 50.0],
  ["gpt-5.6-sol", 4.0, 20.0],
  ["gpt-5.6-luna", 0.2, 1.2],
  ["claude-opus-4-1-20250805", 15.0, 75.0],
  ["claude-opus-4-20250514", 15.0, 75.0],
  ["claude-opus-4-5-20260101", 5.0, 25.0],
  ["claude-opus-5", 5.0, 25.0],
  ["claude-sonnet-4-5-20250929", 3.0, 15.0],
  ["claude-sonnet-5", 2.0, 10.0],
  ["claude-3-7-sonnet-20250219", 3.0, 15.0],
  ["claude-haiku-4-5", 1.0, 5.0],
  ["claude-3-5-haiku-20241022", 0.8, 4.0],
  ["claude-fable-5-1", 10.0, 50.0],
  ["claude-mythos-1", 10.0, 50.0],
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

const priceFor = (model) => {
  const id = String(model || "").toLowerCase();
  return table.find(([sub]) => id.includes(sub)) || null;
};

const failures = [];
for (const [id, wantIn, wantOut] of EXPECT) {
  const row = priceFor(id);
  if (!row) {
    failures.push(`${id}: no PRICING row matches — would render cost "n/a"`);
    continue;
  }
  if (row[1] !== wantIn || row[2] !== wantOut) {
    failures.push(`${id}: matched "${row[0]}" -> ${row[1]}/${row[2]}, want ${wantIn}/${wantOut}`);
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
  `token-pricing: ${EXPECT.length} published id(s) resolved against ${table.length} row(s), every rate as intended` +
    (dead.length ? `; ${dead.length} row(s) unreached by this fixture: ${dead.join(", ")}` : "")
);
