// timeline.mjs — --timeline FILE: one run, every model call a row, priced by the same replay.
import path from "node:path";
import fs from "node:fs";
import { SCAN, gapsLine } from "./scan.mjs";
import { auditFile, callOrigins } from "./claude.mjs";
import { shortModel } from "./format.mjs";

export function runTimeline(opts) {
  const K = (v) => (v >= 1e6 ? (v / 1e6).toFixed(1) + "M" : v >= 1000 ? (v / 1000).toFixed(1) + "K" : String(Math.round(v)));
  const cash = (v) => (v === null ? "n/a" : "$" + v.toFixed(4)), secs = (ms) => (ms / 1000).toFixed(1) + "s", clock = (t) => new Date(t).toISOString().slice(11, 19);
  let failed = 0;
  opts.timeline.forEach((given, i) => {
    // the gaps line speaks for THIS file only
    Object.assign(SCAN, { badLines: 0, noTimestamp: 0, unpricedCalls: 0, unpricedModels: {}, tierUnknownCalls: 0, syntheticCalls: 0, copiedCalls: 0, copiesOnly: [], readErrors: [], notes: [] });
    if (i) console.log("");
    let R; try { const file = path.resolve(given), dir = path.dirname(file);
      // Resumed copies need their named origin's call as evidence; only inspect this directory.
      const siblings = fs.readdirSync(dir, { withFileTypes: true }).filter((e) => e.isFile() && e.name.endsWith(".jsonl")).map((e) => path.join(dir, e.name));
      R = auditFile(file, opts, callOrigins(siblings));
    } catch (e) { failed++; console.log(`UNREADABLE — ${given}: ${e.message}`); return; }
    if (!R) { console.log(`NO CALLS — ${given}`); console.log(gapsLine()); if (SCAN.readErrors.length) failed++; return; }
    const T = R.timeline, who = R.kind === "main" ? "main" : R.agentType || "agent (type unknown: no .meta.json)";
    console.log(`TIMELINE ${who} · ${Object.keys(R.models).map(shortModel).join("+")} · effort ${Object.keys(R.efforts).join("/")} · ${R.calls} calls · wall ${Math.round((T.t1 - T.t0) / 1000)}s · peak ctx ${K(R.ctxPeak)} · out ${R.tok.out} tok · tool errors ${R.errs} · results >20KB ${T.big} · ${R.unpriced ? "n/a" : cash(R.usd)} · ${given}`);
    for (const c of T.rows) {
      const tools = c.tools.map((t) => `${t.name}: ${t.target} ${t.chars === null ? "(no result)" : `${t.chars}ch${t.err ? " ERR" : ""} ${secs(t.dur)}`}`);
      console.log(`  #${c.n} ${clock(c.ts)} after ${c.gap === null ? "-" : secs(c.gap)} · ctx ${K(c.ctx)} · out ${c.out} · ${cash(c.usd)} · ${tools.join(" | ") || "(no tools)"}`); }
    console.log(gapsLine());
    if (SCAN.readErrors.length) failed++;
  });
  return failed ? 1 : 0;
}
