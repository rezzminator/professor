// scan.mjs — the shared scan state: transcript discovery, the line reader, and the counters every mode fills.
import fs from "node:fs";
import path from "node:path";
import os from "node:os";
import { StringDecoder } from "node:string_decoder";
import { die } from "./options.mjs";
import { priceOverride } from "./pricing.mjs";

// ---------- discovery
export function roots(opts) {
  const given = opts.root;
  let cc = []; try { const d = path.join(os.homedir(), ".cc"); cc = fs.readdirSync(d).map((n) => path.join(d, n, "projects")); } catch { /* no ~/.cc: fine */ }
  const cand = given.length ? given : [process.env.CLAUDE_CONFIG_DIR && path.join(process.env.CLAUDE_CONFIG_DIR, "projects"), path.join(os.homedir(), ".claude/projects"), ...cc].filter(Boolean);
  const seen = new Set(), out = [];
  for (const c of cand) { let r; try { r = fs.realpathSync(c); } catch (e) { if (given.length) die(`--root ${c} is not readable: ${e.message}`); continue; }
    if (!seen.has(r)) { seen.add(r); out.push(r); } }
  if (!out.length) die("no transcript root found; pass --root DIR");
  return out;
}
export const SCAN = { roots: [], files: 0, skippedOld: 0, dupFiles: 0, badLines: 0, noTimestamp: 0, unpricedCalls: 0, unpricedModels: {}, tierUnknownCalls: 0, syntheticCalls: 0, copiedCalls: 0, copiesOnly: [], readErrors: [], notes: [] };
// Every dropped, unpriced or unreadable thing reaches the reader on ONE line. A count
// that exists only in --out JSON is a gap the text report claims not to have; the
// synthetic-call drop used to be exactly that.
export function gapsLine() {
  const loud = [], ov = priceOverride();
  if (ov) loud.push(`price override active: ${ov.rows} rows from ${ov.path}`);
  if (SCAN.badLines) loud.push(`${SCAN.badLines} malformed lines`);
  if (SCAN.noTimestamp) loud.push(`${SCAN.noTimestamp} calls without a timestamp (dropped)`);
  if (SCAN.syntheticCalls) loud.push(`${SCAN.syntheticCalls} synthetic/zero-usage calls (dropped: the harness billed nothing for them)`);
  if (SCAN.unpricedCalls) loud.push(`${SCAN.unpricedCalls} UNPRICED calls ${JSON.stringify(SCAN.unpricedModels)} — tokens counted, dollars "n/a"; add the model to pfm.prices.json`);
  if (SCAN.tierUnknownCalls) loud.push(`${SCAN.tierUnknownCalls} cache writes with no 5m/1h split (priced as 5m)`);
  if (SCAN.copiedCalls) loud.push(`${SCAN.copiedCalls} calls copied from another transcript (forked/resumed session) — billed once, to the transcript that made them`);
  if (SCAN.copiesOnly.length) loud.push(`${SCAN.copiesOnly.length} transcripts hold only copied calls: ${SCAN.copiesOnly.slice(0, 3).map((r) => r.sid.slice(0, 12)).join(", ")}`);
  if (SCAN.dupFiles) loud.push(`${SCAN.dupFiles} duplicate files skipped`);
  // notes are bounded: a per-row note on a 200-agent flight must not become the report
  for (const n of SCAN.notes.slice(0, 4)) loud.push(n);
  if (SCAN.notes.length > 4) loud.push(`${SCAN.notes.length - 4} further notes (see --out JSON scan.notes)`);
  if (SCAN.readErrors.length) loud.push(`${SCAN.readErrors.length} READ ERRORS: ${SCAN.readErrors.slice(0, 3).join(" | ")}`);
  return loud.length ? "data gaps: " + loud.join(" · ") : "data gaps: none";
}
export function walk(dir, out, opts) {
  let ents; try { ents = fs.readdirSync(dir, { withFileTypes: true }); } catch (e) { SCAN.readErrors.push(`${dir}: ${e.message}`); return; }
  for (const e of ents) { const p = path.join(dir, e.name);
    if (e.isDirectory()) walk(p, out, opts);
    else if (e.name.endsWith(".jsonl")) { let st; try { st = fs.statSync(p); } catch (er) { SCAN.readErrors.push(`${p}: ${er.message}`); continue; }
      if (st.mtimeMs >= opts.since) out.push(p); else SCAN.skippedOld++; } }
}
export function* linesOf(file) {
  const fd = fs.openSync(file, "r"), buf = Buffer.allocUnsafe(1 << 23), dec = new StringDecoder("utf8"); let carry = "";
  try { for (;;) { const n = fs.readSync(fd, buf, 0, buf.length, null); if (!n) break;
      const parts = (carry + dec.write(buf.subarray(0, n))).split("\n"); carry = parts.pop(); for (const p of parts) if (p) yield p; }
    carry += dec.end(); if (carry) yield carry;
  } finally { fs.closeSync(fd); }
}
export const foldCwd = (cwd) => (cwd || "").replace(/\/\.worktrees\/.*$/, "").replace(/\/+$/, "");
export const isHome = (p) => /^(\/home\/[^/]+|\/Users\/[^/]+|\/root|\/)$/.test(p);

// ---------- global accumulators
export const bump = (o, k, v) => { o[k] = (o[k] || 0) + v; };
export const bump2 = (o, k, f, v) => { (o[k] ??= {})[f] = (o[k][f] || 0) + v; };
export const G = { usd: { in: 0, out: 0, cw5: 0, cw1: 0, cr: 0 }, tok: { in: 0, out: 0, cw5: 0, cw1: 0, cr: 0, think: 0 }, calls: 0,
  cats: null /* one Float64Array slot per category, sized by claude.mjs, which owns CATS */, bands: {}, modelEffort: {}, hourly: {}, rewrites: {}, rewriteTool: {}, small: {}, poll: {}, callIdx: {}, tier: {}, tools: {}, bash: {}, attach: {}, repeats: {}, rereads: {}, landings: [] };
export const RUNS = [];
