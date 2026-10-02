// report.mjs — the default report: the aggregates, its sections, --family, the cross-check, --out, --view and --briefs.
import fs from "node:fs";
import path from "node:path";
import os from "node:os";
import { SCAN, G, RUNS, gapsLine } from "./scan.mjs";
import { RATE } from "./pricing.mjs";
import { CATS, C_PREFIX, C_READ1, C_READN, C_BREAD, C_BTEST, BANDS } from "./claude.mjs";
import { shortModel, $, pct as pctOf, Mt, pad, cut, ttlMix } from "./format.mjs";

const q = (arr, p) => { if (!arr.length) return 0; const s = [...arr].sort((a, b) => a - b); return s[Math.min(s.length - 1, Math.floor(p * s.length))]; };
const wallMin = (r) => (r.t1 - r.t0) / 60e3;

// ---------- aggregate
export function aggregate() {
  const total = Object.values(G.usd).reduce((a, b) => a + b, 0);
  const byProject = {}; for (const r of RUNS) { const p = (byProject[r.project] ??= { usd: 0, main: 0, agent: 0, runs: 0, rewrites: 0, cats: new Float64Array(CATS.length) }); p.usd += r.usd; p[r.kind] += r.usd; p.runs++; p.rewrites += r.rewrites.usd; r.cats.forEach((v, i) => (p.cats[i] += v)); }
  const families = {}; for (const r of RUNS) { const f = (families[r.sid] ??= { sid: r.sid, title: "", project: r.project, own: 0, agents: 0, nAgents: 0, rewrites: 0, calls: 0, peakK: 0, t0: Infinity, t1: 0, agentRuns: [] });
    if (r.kind === "main") { f.title = r.title; f.own += r.usd; f.peakK = Math.round(r.ctxPeak / 1000); f.project = r.project; } else { f.agents += r.usd; f.nAgents++; f.agentRuns.push(r); }
    f.rewrites += r.rewrites.usd; f.calls += r.calls; f.t0 = Math.min(f.t0, r.t0); f.t1 = Math.max(f.t1, r.t1); }
  const famList = Object.values(families).sort((a, b) => b.own + b.agents - (a.own + a.agents));
  const groups = {}; for (const r of RUNS) if (r.kind === "agent") { const k = `${path.basename(r.project)} · ${r.agentType || "(untyped)"} · ${shortModel(r.model)}`; (groups[k] ??= []).push(r); }
  const groupRows = Object.entries(groups).map(([k, rs]) => ({ k, n: rs.length, usd: rs.reduce((a, r) => a + r.usd, 0), med: q(rs.map((r) => r.usd), 0.5), p90: q(rs.map((r) => r.usd), 0.9), max: Math.max(...rs.map((r) => r.usd)),
    calls: q(rs.map((r) => r.calls), 0.5), peakK: q(rs.map((r) => r.ctxPeak / 1000), 0.5), wall: q(rs.map(wallMin), 0.5), errs: rs.reduce((a, r) => a + r.errs, 0) / rs.length,
    poll: rs.reduce((a, r) => a + r.pollUsd, 0), small: rs.reduce((a, r) => a + r.smallUsd, 0), tests: rs.reduce((a, r) => a + (r.bash[CATS[C_BTEST]]?.n || 0), 0) / rs.length, rereads: rs.reduce((a, r) => a + r.rereadN, 0) / rs.length,
    ttl: ttlMix(rs.reduce((a, r) => ({ cw5: a.cw5 + r.tok.cw5, cw1: a.cw1 + r.tok.cw1 }), { cw5: 0, cw1: 0 })) })).sort((a, b) => b.usd - a.usd);
  return { total, byProject, families, famList, groupRows };
}

// ---------- report
export function runReport(opts, agg) {
  const { now: NOW, hours: HOURS, since: SINCE, top: TOP, family: FAMILY, out: OUT, view: VIEW, briefs: BRIEFS, famrx: FAMRX } = opts;
  const { total, byProject, families, famList, groupRows } = agg;
  const pct = (v, t = total) => pctOf(v, t);
  const H = (t) => console.log("\n== " + t), L = (s) => console.log(s);
  const base = (p) => path.basename(p) || p;
  L(`TOKEN AUDIT · last ${HOURS}h · host ${os.hostname()} · ${new Date(NOW).toISOString()}`);
  L(`scanned ${SCAN.roots.join(", ")} · ${SCAN.files} transcripts (${RUNS.filter((r) => r.kind === "main").length} main, ${RUNS.filter((r) => r.kind === "agent").length} agent runs with priced calls) · ${G.calls} API calls`);
  L(gapsLine());

  H(`1 · TOTAL ${$(total)} by billing class`);
  for (const [k, n] of [["cr", "re-reading cached context (per-model read rate)"], ["cw5", "writing context to the 5-minute cache (1.25x)"], ["cw1", "writing context to the 1-hour cache (2x)"], ["out", "output tokens"], ["in", "uncached input (1x)"]])
    L(`  ${pad($(G.usd[k]), 9)} ${pad(pct(G.usd[k]), 6)}  ${n} · ${Mt(G.tok[k])} tok`);
  const mainUsd = RUNS.filter((r) => r.kind === "main").reduce((a, r) => a + r.usd, 0);
  L(`  main chat loops ${$(mainUsd)} (${pct(mainUsd)}) · sub-agents ${$(total - mainUsd)} (${pct(total - mainUsd)}) · thinking ${Mt(G.tok.think)} of ${Mt(G.tok.out)} output tok`);

  for (const [k, v] of Object.entries(G.tier)) L(`  cache writes by ${k} runs: 5-minute ${$(v.cw5)} · 1-hour ${$(v.cw1)}`);

  H("2 · BY PROJECT");
  for (const [p, v] of Object.entries(byProject).sort((a, b) => b[1].usd - a[1].usd).slice(0, 8)) L(`  ${pad($(v.usd), 9)} ${pad(pct(v.usd), 6)}  ${cut(p, 44)} main ${$(v.main)} · agents ${$(v.agent)} · ${v.runs} runs · rewrites ${$(v.rewrites)}`);

  H("3 · BY KIND · MODEL · EFFORT");
  for (const [k, v] of Object.entries(G.modelEffort).sort((a, b) => b[1].usd - a[1].usd).slice(0, TOP)) L(`  ${pad($(v.usd), 9)} ${pad(pct(v.usd), 6)}  ${pad(v.calls, 6)} calls · ${pad($(v.usd / v.calls * 100) + "/100 calls", 16)} · ${k}`);

  H("4 · WHAT THE MONEY PAID FOR (every dollar traced to the context that caused it)");
  const catRows = CATS.map((c, i) => [c, G.cats[i]]).filter((x) => x[1] > total * 0.002).sort((a, b) => b[1] - a[1]);
  for (const [c, v] of catRows) L(`  ${pad($(v), 9)} ${pad(pct(v), 6)}  ${c}`);
  for (const [p, v] of Object.entries(byProject).sort((a, b) => b[1].usd - a[1].usd).slice(0, 3)) { const t = v.cats.reduce((a, b) => a + b, 0);
    L(`  · ${p}: ` + CATS.map((c, i) => [c, v.cats[i]]).sort((a, b) => b[1] - a[1]).slice(0, 5).map(([c, x]) => `${c.split(" (")[0]} ${pct(x, t)}`).join(" · ")); }

  H("5 · HOW BIG WAS THE CONTEXT WHEN THE MONEY WAS SPENT");
  for (const [, name] of BANDS) { const b = G.bands[name]; if (b) L(`  ${pad($(b.usd), 9)} ${pad(pct(b.usd), 6)}  ${pad(b.calls, 6)} calls · ${pad($(b.usd / b.calls * 100), 7)}/100 calls · context ${name}`); }

  L("  and how deep into a run it was spent (a run's context only grows, so late calls are the dear ones):");
  for (const [k, v] of Object.entries(G.callIdx).sort((a, b) => a[0].localeCompare(b[0]))) L(`  ${pad($(v.usd), 9)} ${pad(pct(v.usd), 6)}  ${pad(v.calls, 6)} calls · mean context ${pad(Math.round(v.ctx / v.calls / 1000), 4)}K · ${k.replace(/ \d · /, " · ")}`);
  L("  calls that moved almost nothing yet re-read the whole context:");
  for (const [k, v] of Object.entries(G.small)) L(`  ${pad($(v.usd), 9)} ${pad(pct(v.usd), 6)}  ${pad(v.n, 6)} calls · ${k} · small steps (under ~400 tokens came in, under 250 went out)`);
  for (const [k, v] of Object.entries(G.poll)) L(`  ${pad($(v.usd), 9)} ${pad(pct(v.usd), 6)}  ${pad(v.n, 6)} calls · ${k} · answering a shell command the run repeated 8+ times (polling, log-peeking)`);

  H("6 · FULL CACHE REWRITES (the whole context re-billed at write price)");
  const rwTotal = Object.values(G.rewrites).reduce((a, v) => a + v.usd, 0); L(`  ${$(rwTotal)} total (${pct(rwTotal)})`);
  for (const [k, v] of Object.entries(G.rewrites).sort((a, b) => b[1].usd - a[1].usd)) L(`  ${pad($(v.usd), 9)}  ${pad(v.n, 4)}× · avg ${pad(Math.round(v.tokK / v.n) + "K", 5)} tok${v.over60m ? ` · ${v.over60m} after >60m` : ""} · ${k}`);
  for (const [k, v] of Object.entries(G.rewriteTool).sort((a, b) => b[1].usd - a[1].usd).slice(0, 5)) L(`    the long call was: ${k} · ${v.n}× · ${$(v.usd)}`);

  H(`7 · TOP ${TOP} CHAT FAMILIES (a chat + the agents it spawned)`);
  for (const f of famList.slice(0, TOP)) L(`  ${pad($(f.own + f.agents), 9)} ${pad(pct(f.own + f.agents), 6)}  ${cut(f.title || f.sid.slice(0, 8), 34)} ${cut(base(f.project), 12)} own ${pad($(f.own), 8)} · ${pad(f.nAgents, 3)} agents ${pad($(f.agents), 8)} · rewrites ${pad($(f.rewrites), 7)} · peak ${f.peakK}K`);

  H("8 · AGENT GROUPS (project · agent type · model) — same job, different cost?");
  L(`  ${pad("total", 9)} ${pad("n", 4)} ${pad("median", 8)} ${pad("p90", 8)} ${pad("max", 8)} ${pad("calls", 6)} ${pad("peakK", 6)} ${pad("wall m", 7)} ${pad("errs", 5)} ${pad("tests", 6)} ${pad("reread", 7)} ${pad("poll$", 6)} ${pad("small$", 6)} ${pad("ttl", 7)}  group (calls/peak/wall = median, errs/tests/reread = per run, poll/small = share of group $, ttl = where its cache writes went)`);
  for (const g of groupRows.slice(0, TOP + 6)) L(`  ${pad($(g.usd), 9)} ${pad(g.n, 4)} ${pad($(g.med), 8)} ${pad($(g.p90), 8)} ${pad($(g.max), 8)} ${pad(g.calls, 6)} ${pad(Math.round(g.peakK), 6)} ${pad(Math.round(g.wall), 7)} ${pad(g.errs.toFixed(1), 5)} ${pad(g.tests.toFixed(1), 6)} ${pad(g.rereads.toFixed(1), 7)} ${pad(pct(g.poll, g.usd), 6)} ${pad(pct(g.small, g.usd), 6)} ${pad(g.ttl, 7)}  ${g.k}`);

  H(`9 · TOP ${TOP + 3} SINGLE RUNS`);
  const topRuns = [...RUNS].sort((a, b) => b.usd - a.usd);
  const USD = (r) => (r.unpriced ? "n/a" : $(r.usd)); // never render ignorance as zero dollars
  for (const r of topRuns.slice(0, TOP + 3)) L(`  ${pad(USD(r), 9)}  ${cut(r.kind === "main" ? "MAIN " + (r.title || r.sid.slice(0, 8)) : `${r.agentType || "agent"}: ${r.title}`, 44)} ${cut(shortModel(r.model), 12)} ${pad(r.calls, 5)} calls · ctx ${pad(Math.round(r.ctxFirst / 1000), 3)}→${pad(Math.round(r.ctxPeak / 1000), 3)}K · ${pad(Math.round(wallMin(r)), 4)}m wall/${pad(Math.round(r.toolWaitMs / 60e3), 4)}m in tools · rw ${pad($(r.rewrites.usd), 6)} · errs ${pad(r.errs, 3)} · reread ${pad(r.rereadN, 3)} · poll ${pad($(r.pollUsd), 6)} · ttl ${ttlMix(r.tok)}${r.topRepeat ? ` · ${r.topRepeat.n}× "${r.topRepeat.cmd.slice(0, 40)}"` : ""}`);
  const ag = RUNS.filter((r) => r.kind === "agent"), agUsd = ag.reduce((a, r) => a + r.usd, 0), agSorted = [...ag].sort((a, b) => b.usd - a.usd), top10n = Math.max(1, Math.round(ag.length * 0.1));
  if (ag.length) L(`  concentration: the costliest 10% of agent runs (${top10n} of ${ag.length}) spent ${pct(agSorted.slice(0, top10n).reduce((a, r) => a + r.usd, 0), agUsd)} of all agent dollars · runs over 150 calls: ${ag.filter((r) => r.calls > 150).length} spending ${pct(ag.filter((r) => r.calls > 150).reduce((a, r) => a + r.usd, 0), agUsd)}`);

  H("10 · TOOLS (results entering context; ~4 chars per token)");
  for (const [k, v] of Object.entries(G.tools).sort((a, b) => b[1].chars - a[1].chars).slice(0, 10)) L(`  ${pad(Mt(v.chars / 4), 7)} tok ${pad(v.n, 6)}× · ${pad(Math.round(v.chars / 4 / v.n), 6)} tok each · ${pad(pct(v.err, v.n), 6)} failed · ${k}`);
  for (const [k, v] of Object.entries(G.bash).sort((a, b) => b[1].chars - a[1].chars)) L(`    ${pad(Mt(v.chars / 4), 7)} tok ${pad(v.n, 6)}× · ${pad(pct(v.err, v.n), 6)} failed · ${pad(Math.round(v.waitMs / 3600e3 * 10) / 10 + "h", 7)} waiting · ${k}`);

  L("  harness injections by type:");
  for (const [k, v] of Object.entries(G.attach).sort((a, b) => b[1].chars - a[1].chars).slice(0, 7)) L(`    ${pad(Mt(v.chars / 4), 7)} tok ${pad(v.n, 6)}× · ${pad(Math.round(v.chars / 4 / v.n), 6)} tok each · ${k}`);

  H("11 · LOOPS");
  L("  the same shell command, repeated inside one run:");
  for (const [k, v] of Object.entries(G.repeats).sort((a, b) => b[1].n - a[1].n).slice(0, 8)) L(`  ${pad(v.n, 5)}× in ${pad(v.runs, 3)} runs (max ${pad(v.maxInRun, 3)} in one) · ${cut(k.replace(/^\S*\//, ""), 110)}`);
  L("  the same file, read again while already in context:");
  for (const [k, v] of Object.entries(G.rereads).sort((a, b) => b[1].chars - a[1].chars).slice(0, 8)) L(`  ${pad(v.n, 5)} re-reads · ${pad(Math.round(v.chars / 4000) + "K", 6)} tok · ${cut(k.replace(/^\S*\//, ""), 100)}`);
  L("  single tool results that were then carried the longest (estimate: tokens × later calls × read price):");
  for (const l of G.landings.slice(0, 8)) L(`  ${pad($(l.usd), 8)} · ${pad(l.tokK + "K", 5)} tok carried ${pad(l.after, 4)} calls · ${l.tool} · ${cut(l.target, 80)}`);

  H("12 · BUSIEST HOURS (UTC)");
  for (const [h, v] of Object.entries(G.hourly).map(([h, v]) => [h, Object.values(v).reduce((a, b) => a + b, 0), v]).sort((a, b) => b[1] - a[1]).slice(0, 6))
    L(`  ${h}h ${pad($(v), 8)}`);

  // --family matches a chat title OR a session id OR any sub-agent's spawn description, so a
  // family an agent orchestrated — which has no title at all — is still reachable by name.
  if (FAMILY) { const needle = FAMILY.toLowerCase();
    const f = famList.find((x) => (x.title || "").toLowerCase().includes(needle) || x.sid.toLowerCase().startsWith(needle) || x.agentRuns.some((r) => (r.title || "").toLowerCase().includes(needle) || (r.agentType || "").toLowerCase().includes(needle)));
    H(`13 · FAMILY DRILL-DOWN "${FAMILY}"`);
    if (!f) L(`  NOT FOUND among ${famList.length} families — titles seen: ${famList.slice(0, 15).map((x) => x.title || x.sid.slice(0, 8)).join(" | ")}`);
    else { L(`  ${f.title} · ${f.project} · own ${$(f.own)} · ${f.nAgents} agents ${$(f.agents)} · rewrites ${$(f.rewrites)}`);
      const mainRun = RUNS.find((r) => r.kind === "main" && r.sid === f.sid);
      if (mainRun) { L(`  main loop: ${mainRun.calls} calls · ctx ${Math.round(mainRun.ctxFirst / 1000)}→${Math.round(mainRun.ctxPeak / 1000)}K · mean ${Math.round(mainRun.ctxSum / mainRun.calls / 1000)}K · resets ${mainRun.resets} · rewrites ${mainRun.rewrites.n}× ${$(mainRun.rewrites.usd)} ${JSON.stringify(Object.fromEntries(Object.entries(mainRun.rewrites.by).map(([k, v]) => [k, +v.toFixed(2)])))}`);
        L("  main loop paid for: " + CATS.map((c, i) => [c, mainRun.cats[i]]).sort((a, b) => b[1] - a[1]).slice(0, 6).map(([c, x]) => `${c.split(" (")[0]} ${pct(x, mainRun.usd)}`).join(" · ")); }
      const byModel = {}; for (const r of f.agentRuns) { const k = `${r.agentType || "agent"} · ${shortModel(r.model)}`; (byModel[k] ??= []).push(r); }
      for (const [k, rs] of Object.entries(byModel).sort((a, b) => b[1].reduce((x, r) => x + r.usd, 0) - a[1].reduce((x, r) => x + r.usd, 0))) { const t = rs.reduce((a, r) => a + r.usd, 0), cats = new Float64Array(CATS.length); rs.forEach((r) => r.cats.forEach((v, i) => (cats[i] += v)));
        L(`  ${k}: ${rs.length} runs · ${$(t)} · median ${$(q(rs.map((r) => r.usd), 0.5))} · median calls ${q(rs.map((r) => r.calls), 0.5)} · median peak ${Math.round(q(rs.map((r) => r.ctxPeak / 1000), 0.5))}K · median wall ${Math.round(q(rs.map(wallMin), 0.5))}m`);
        L("     paid for: " + CATS.map((c, i) => [c, cats[i]]).sort((a, b) => b[1] - a[1]).slice(0, 6).map(([c, x]) => `${c.split(" (")[0]} ${pct(x, t)}`).join(" · ")); }
      L("  costliest agents:");
      for (const r of [...f.agentRuns].sort((a, b) => b.usd - a.usd).slice(0, 14)) L(`  ${pad($(r.usd), 8)} ${cut(shortModel(r.model), 11)} ${new Date(r.t0).toISOString().slice(5, 16)} ${pad(r.calls, 5)} calls · ctx ${pad(Math.round(r.ctxFirst / 1000), 3)}→${pad(Math.round(r.ctxPeak / 1000), 3)}K · ${pad(Math.round(wallMin(r)), 4)}m wall/${pad(Math.round(r.toolWaitMs / 60e3), 4)}m tools · tests ${pad(r.bash[CATS[C_BTEST]]?.n || 0, 3)} · errs ${pad(r.errs, 3)} · reread ${pad(r.rereadN, 3)} · rw ${pad($(r.rewrites.usd), 6)} · ${cut(r.title, 40)}`); } }

  // cross-check against the harness's own running total — only for chats that lie wholly inside the window
  const hc = famList.map((f) => ({ f, r: RUNS.find((r) => r.kind === "main" && r.sid === f.sid) })).filter((x) => x.r && x.r.harnessUsd != null && x.r.firstTs >= SINCE && x.f.agentRuns.every((a) => a.firstTs >= SINCE));
  H("CROSS-CHECK (this estimate vs the harness's own cost-state line, chats wholly inside the window)");
  if (!hc.length) L("  no chat qualifies — the estimate is UNCHECKED on this host");
  else { const mine = hc.reduce((a, x) => a + x.f.own + x.f.agents, 0), mineOwn = hc.reduce((a, x) => a + x.f.own, 0), theirs = hc.reduce((a, x) => a + x.r.harnessUsd, 0);
    const lc = hc.reduce((a, x) => a + x.r.usdLC + x.f.agentRuns.reduce((b, r) => b + r.usdLC, 0), 0);
    L(`  if calls over 200K context were billed at the long-context premium (per-model rate in pfm's price table — an estimate): ${$(lc)} (${(lc / theirs).toFixed(2)}x)`);
    L(`  ${hc.length} chats · harness says ${$(theirs)} · this audit says ${$(mine)} with agents (${(mine / theirs).toFixed(2)}x) / ${$(mineOwn)} main loops only (${(mineOwn / theirs).toFixed(2)}x)`); }

  if (OUT) { const slim = (r, i) => ({ ...r, i, file: path.relative(SCAN.roots[0], r.file), cats: Object.fromEntries(CATS.map((c, k) => [c, +r.cats[k].toFixed(4)]).filter((x) => x[1] > 0)), series: i < 25 ? r.series : undefined, tl: undefined, tlf: undefined, brief: undefined, land: undefined, usd: +r.usd.toFixed(4) });
    const ranked = topRuns.map(slim);
    fs.mkdirSync(path.dirname(path.resolve(OUT)), { recursive: true });
    fs.writeFileSync(OUT, JSON.stringify({ v: 1, host: os.hostname(), at: new Date(NOW).toISOString(), hours: HOURS, scan: SCAN, total, usd: G.usd, tok: G.tok, calls: G.calls, cats: Object.fromEntries(CATS.map((c, i) => [c, G.cats[i]])), bands: G.bands, modelEffort: G.modelEffort,
      rewrites: G.rewrites, rewriteTool: G.rewriteTool, tools: G.tools, bash: G.bash, attach: G.attach, hourly: G.hourly, repeats: Object.fromEntries(Object.entries(G.repeats).sort((a, b) => b[1].n - a[1].n).slice(0, 60)),
      rereads: Object.fromEntries(Object.entries(G.rereads).sort((a, b) => b[1].chars - a[1].chars).slice(0, 60)), landings: G.landings, byProject: Object.fromEntries(Object.entries(byProject).map(([k, v]) => [k, { ...v, cats: [...v.cats] }])),
      families: famList.map(({ agentRuns, ...f }) => f), groups: groupRows, runs: ranked }));
    L(`\nfull data → ${OUT} (${(fs.statSync(OUT).size / 1e6).toFixed(1)} MB)`); }

  // ---------- --view FILE: the compact per-project dataset the page draws (findings, runs, timeline)
  if (VIEW) {
    const FK = ["fat", "small", "rw", "read", "inj", "model"], price = (m) => RATE(m)?.in ?? 0, r2 = (v) => +v.toFixed(2);
    const bucketize = (series, B = 80) => { const n = series.length, nb = Math.min(n, B), out = [];
      for (let b = 0; b < nb; b++) { const a = Math.floor(b * n / nb), z = Math.floor((b + 1) * n / nb); let ctx = 0; const acc = [0, 0, 0, 0, 0, 0];
        for (let i = a; i < z; i++) { ctx = Math.max(ctx, series[i][0]); for (let k = 0; k < 6; k++) acc[k] += series[i][k + 1]; }
        out.push([ctx, ...acc.map(Math.round)]); } return out; };
    const view = { v: 2, host: opts.hostLabel, at: NOW, hours: HOURS, tl0: SINCE, stepMin: HOURS * 60 / 48, total: r2(total), projects: [], hidden: [] };
    for (const [pPath, pv] of Object.entries(byProject).sort((a, b) => b[1].usd - a[1].usd)) {
      if (pv.usd < total * 0.03) { view.hidden.push([pPath, r2(pv.usd)]); continue; }
      const runs = RUNS.filter((r) => r.project === pPath), agents = runs.filter((r) => r.kind === "agent");
      // same job, two models: flag when the cheaper-per-token model has the dearer median run
      const byType = {}; for (const r of agents) ((byType[r.agentType || "(untyped)"] ??= {})[shortModel(r.model)] ??= []).push(r);
      const compare = [];
      for (const [ty, models] of Object.entries(byType)) { const ms = Object.entries(models).filter(([, rs]) => rs.length >= 5);
        for (const [ma, ra] of ms) for (const [mb, rb] of ms) { if (!(price(ma) < price(mb))) continue;
          const stat = (m, rs) => ({ model: m, n: rs.length, med: r2(q(rs.map((r) => r.usd), 0.5)), sum: r2(rs.reduce((a, r) => a + r.usd, 0)), calls: q(rs.map((r) => r.calls), 0.5), peakK: Math.round(q(rs.map((r) => r.ctxPeak / 1000), 0.5)), wall: Math.round(q(rs.map(wallMin), 0.5)) });
          const A = stat(ma, ra), Bm = stat(mb, rb), flagged = A.med >= 1.5 * Bm.med, excess = flagged ? r2(A.sum - A.n * Bm.med) : 0;
          if (flagged) for (const r of ra) { r.f[5] = Math.max(0, r.usd - Bm.med); r.cmp = ty; } if (flagged) for (const r of rb) r.cmp = ty;
          compare.push({ type: ty, cheap: A, dear: Bm, flagged, excess }); } }
      compare.sort((a, b) => b.excess - a.excess || b.cheap.sum + b.dear.sum - a.cheap.sum - a.dear.sum);
      const fin = FK.map((_, k) => runs.reduce((a, r) => a + r.f[k], 0));
      const tl = new Array(48).fill(0), tlf = FK.map(() => new Array(48).fill(0));
      for (const r of runs) for (let b = 0; b < 48; b++) { tl[b] += r.tl[b]; for (let k = 0; k < 5; k++) tlf[k][b] += r.tlf[k][b]; if (r.f[5] && r.usd) tlf[5][b] += r.tl[b] * r.f[5] / r.usd; }
      // evidence
      const depth = [[50, "calls 1–50"], [150, "calls 51–150"], [300, "calls 151–300"], [Infinity, "calls 301+"]].map(([lim, name]) => ({ name, lim, usd: 0, calls: 0, ctx: 0 }));
      for (const r of agents) r.series.forEach((c, i) => { const d = depth.find((x) => i < x.lim); d.usd += c[1] / 1000; d.calls++; d.ctx += c[0]; });
      const reps = {}; for (const r of runs) for (const [cmd, n] of r.repeats) { const g = (reps[cmd] ??= { n: 0, runs: 0 }); g.n += n; g.runs++; }
      const att = {}; for (const r of runs) for (const [t, v] of Object.entries(r.attach)) { const g = (att[t] ??= { n: 0, chars: 0 }); g.n += v.n; g.chars += v.chars; }
      const causes = {}; for (const r of runs) for (const [c, v] of Object.entries(r.rewrites.by)) { const g = (causes[`${r.kind} · ${c}`] ??= { usd: 0, n: 0 }); g.usd += v; g.n += r.rewrites.nBy[c] || 0; }
      const rel = (t) => String(t).replace(pPath + "/", "").replace(/^\.worktrees\/[^/]+\//, ""), short = (t) => (t.length <= 64 ? t : /\s/.test(t) ? t.slice(0, 63) + "…" : "…" + t.slice(-63));
      const ev = { depth: depth.map((d) => ({ name: d.name, usd: r2(d.usd), calls: d.calls, meanK: d.calls ? Math.round(d.ctx / d.calls) : 0 })), over200Calls: runs.reduce((a, r) => a + r.series.filter((c) => c[0] > 200).length, 0), calls: runs.reduce((a, r) => a + r.calls, 0),
        runsOver200: runs.filter((r) => r.ctxPeak > 200000).length, smallN: runs.reduce((a, r) => a + r.smallN, 0), pollUsd: r2(runs.reduce((a, r) => a + r.pollUsd, 0)), pollN: runs.reduce((a, r) => a + r.pollN, 0),
        repeats: Object.entries(reps).sort((a, b) => b[1].n - a[1].n).slice(0, 4).map(([cmd, g]) => [short(rel(cmd)), g.n, g.runs]),
        files: G.landings.filter((l) => RUNS[l.run]?.project === pPath && (l.tool === "Read" || l.tool === "Bash")).slice(0, 5).map((l) => [short(rel(l.target)), r2(l.usd), l.tokK, l.after, l.tool]),
        rereadUsd: r2(runs.reduce((a, r) => a + r.cats[C_READN], 0)), rereadN: runs.reduce((a, r) => a + r.rereadN, 0), readUsd: [C_READ1, C_READN, C_BREAD].map((k) => r2(runs.reduce((a, r) => a + r.cats[k], 0))),
        attach: Object.entries(att).sort((a, b) => b[1].chars - a[1].chars).slice(0, 5).map(([t, g]) => [t, g.n, Math.round(g.chars / 4 / g.n)]), perAgentInjK: agents.length ? Math.round(Object.values(att).reduce((a, g) => a + g.chars, 0) / 4 / runs.length / 100) / 10 : 0,
        causes: Object.entries(causes).sort((a, b) => b[1].usd - a[1].usd).slice(0, 4).map(([c, g]) => [c, r2(g.usd), g.n]), compare: compare.slice(0, 4) };
      // which runs travel with the page: the costliest, the worst per finding, and both sides of a flagged comparison
      const pick = new Set([...runs].sort((a, b) => b.usd - a.usd).slice(0, 30));
      for (let k = 0; k < 6; k++) [...runs].sort((a, b) => b.f[k] - a.f[k]).slice(0, 8).forEach((r) => r.f[k] > 0 && pick.add(r));
      for (const c of compare.filter((x) => x.flagged)) for (const m of [c.cheap.model, c.dear.model]) agents.filter((r) => r.cmp === c.type && shortModel(r.model) === m).sort((a, b) => b.usd - a.usd).slice(0, 10).forEach((r) => pick.add(r));
      const out = [...pick].sort((a, b) => b.usd - a.usd).map((r) => ({ k: r.kind, ty: r.agentType || (r.kind === "main" ? "chat" : "agent"), m: shortModel(r.model), ef: r.effort, ti: (r.title || "").slice(0, 80), fam: r.kind === "agent" ? (families[r.sid]?.title || r.sid.slice(0, 8)) : "",
        usd: r2(r.usd), f: r.f.map(r2), poll: r2(r.pollUsd), calls: r.calls, pk: Math.round(r.ctxPeak / 1000), c0: Math.round(r.ctxFirst / 1000), mn: Math.round(r.ctxSum / r.calls / 1000), wall: Math.round(wallMin(r)), tool: Math.round(r.toolWaitMs / 60e3),
        t0: +((r.t0 - SINCE) / (HOURS * 3600e3) * 48).toFixed(2), t1: +((r.t1 - SINCE) / (HOURS * 3600e3) * 48).toFixed(2), at: r.t0, errs: r.errs, tests: r.bash[CATS[C_BTEST]]?.n || 0, rer: r.rereadN, rw: r.rewrites.n, resets: r.resets, cmp: r.cmp || "",
        rep: r.topRepeat ? [short(rel(r.topRepeat.cmd)), r.topRepeat.n] : null, cats: CATS.map((c, i) => [c.split(" (")[0], r2(r.cats[i])]).sort((a, b) => b[1] - a[1]).slice(0, 5), prof: bucketize(r.series) }));
      view.projects.push({ path: pPath, name: path.basename(pPath), usd: r2(pv.usd), mainUsd: r2(pv.main), agentUsd: r2(pv.agent), nMain: runs.length - agents.length, nAgent: agents.length, fin: fin.map(r2), tl: tl.map(r2), tlf: tlf.map((a) => a.map(r2)), ev, runs: out,
        rest: { n: runs.length - out.length, usd: r2(pv.usd - out.reduce((a, r) => a + r.usd, 0)) }, maxCalls: Math.max(...out.map((r) => r.calls)), maxPk: Math.max(...out.map((r) => r.pk)) });
    }
    fs.mkdirSync(path.dirname(path.resolve(VIEW)), { recursive: true }); fs.writeFileSync(VIEW, JSON.stringify(view));
    L(`page data → ${VIEW} (${(fs.statSync(VIEW).size / 1e3).toFixed(0)} KB · ${view.projects.map((p) => `${p.name} ${p.runs.length} runs`).join(" · ")})`);
  }

  // ---------- --briefs FILE: every brief the matching chat families wrote, and what each agent then did with it
  if (BRIEFS) {
    const items = (t) => (t.match(/^\s*(\d+[.)]|[-*•]|#{1,4})\s+\S/gm) || []).length, paths = (t) => new Set(t.match(/[\w.@-]+(?:\/[\w.@\[\]-]+)+\.\w{1,5}\b/g) || []).size;
    const fams = famList.filter((f) => FAMRX.test(f.title || "")), rowsB = [];
    for (const f of fams) for (const r of f.agentRuns) { const t = r.brief || "", land = [...r.land], grow = land.reduce((a, b) => a + b, 0) - land[C_PREFIX];
      rowsB.push({ fam: f.title, famSid: f.sid.slice(0, 8), project: path.basename(r.project), ty: r.agentType || "(untyped)", m: shortModel(r.model), depth: r.depth, ti: r.title, at: new Date(r.t0).toISOString().slice(0, 16), usd: +r.usd.toFixed(2), calls: r.calls, pkK: Math.round(r.ctxPeak / 1000), wall: Math.round(wallMin(r)),
        briefChars: t.length, briefItems: items(t), briefPaths: paths(t), briefFences: (t.match(/```/g) || []).length / 2, briefAccept: /accept|done when|definition of done|must pass|verify/i.test(t), briefNoBrief: r.brief === null,
        distinctRead: r.distinctRead, readsBeforeEdit: r.B.readsBeforeEdit, firstEditCall: r.B.firstEditCall, edits: r.B.edits, filesEdited: Object.keys(r.B.editFiles).length, maxEditsOneFile: Math.max(0, ...Object.values(r.B.editFiles)), testFilesEdited: Object.keys(r.B.editFiles).filter((p) => /test|spec/i.test(p)).length,
        tests: r.B.tests, testFails: r.B.testFails, rereads: r.rereadN, errs: r.errs, small: +r.smallUsd.toFixed(2), poll: +r.pollUsd.toFixed(2),
        growK: Math.round(grow / 1000), growBy: Object.fromEntries(CATS.map((c, i) => [c.split(" (")[0], Math.round(land[i] / 1000)]).filter((x, i) => i !== C_PREFIX && x[1] > 0).sort((a, b) => b[1] - a[1]).slice(0, 6)),
        testCmds: Object.entries(r.B.testCmd).sort((a, b) => b[1] - a[1]).slice(0, 3), brief: t.slice(0, 6000) }); }
    const med = (a) => q(a, 0.5), G2 = {}; for (const x of rowsB) (G2[`${x.fam}[${x.famSid}] · ${x.ty} · ${x.m}`] ??= []).push(x);
    H(`14 · BRIEFS AND BEHAVIOUR · families matching /${FAMRX.source}/ · ${fams.length} families · ${rowsB.length} agent runs` + (rowsB.some((x) => x.briefNoBrief) ? ` · ${rowsB.filter((x) => x.briefNoBrief).length} runs with NO BRIEF FOUND` : ""));
    L(`  ${pad("total", 8)} ${pad("n", 3)} ${pad("med$", 7)} ${pad("calls", 5)} ${pad("peakK", 5)} | brief: ${pad("chars", 6)} ${pad("items", 5)} ${pad("paths", 5)} ${pad("code", 4)} | ${pad("filesRd", 7)} ${pad("rdB4edit", 8)} ${pad("1stEdit", 7)} ${pad("edits", 5)} ${pad("files", 5)} ${pad("maxOne", 6)} ${pad("tests", 5)} ${pad("fail%", 5)}  group (medians per run)`);
    for (const [k, xs] of Object.entries(G2).sort((a, b) => b[1].reduce((s2, x) => s2 + x.usd, 0) - a[1].reduce((s2, x) => s2 + x.usd, 0)).slice(0, 22)) { const c = (f) => med(xs.map(f)), T = xs.reduce((a, x) => a + x.tests, 0), TF = xs.reduce((a, x) => a + x.testFails, 0);
      L(`  ${pad($(xs.reduce((a, x) => a + x.usd, 0)), 8)} ${pad(xs.length, 3)} ${pad($(c((x) => x.usd)), 7)} ${pad(c((x) => x.calls), 5)} ${pad(c((x) => x.pkK), 5)} |        ${pad(c((x) => x.briefChars), 6)} ${pad(c((x) => x.briefItems), 5)} ${pad(c((x) => x.briefPaths), 5)} ${pad(c((x) => x.briefFences), 4)} | ${pad(c((x) => x.distinctRead), 7)} ${pad(c((x) => x.readsBeforeEdit), 8)} ${pad(c((x) => x.firstEditCall), 7)} ${pad(c((x) => x.edits), 5)} ${pad(c((x) => x.filesEdited), 5)} ${pad(c((x) => x.maxEditsOneFile), 6)} ${pad(c((x) => x.tests), 5)} ${pad(pct(TF, T), 5)}  ${k}`); }
    L("  where the context growth came from (tokens landed after the fixed prefix, all matching runs, by model):");
    for (const m of [...new Set(rowsB.map((x) => x.m))]) { const xs = rowsB.filter((x) => x.m === m), tot = {}; let all = 0; for (const x of xs) for (const [c, v] of Object.entries(x.growBy)) { tot[c] = (tot[c] || 0) + v; all += v; }
      L(`    ${cut(m, 10)} ${pad(xs.length, 4)} runs · ` + Object.entries(tot).sort((a, b) => b[1] - a[1]).slice(0, 6).map(([c, v]) => `${c} ${pct(v, all)}`).join(" · ")); }
    L("  does a bigger brief make a dearer run? (all matching runs, by number of list items in the brief):");
    for (const [lo, hi, name] of [[0, 3, "0–3 items"], [4, 9, "4–9"], [10, 19, "10–19"], [20, 39, "20–39"], [40, 1e9, "40+"]]) { const xs = rowsB.filter((x) => x.briefItems >= lo && x.briefItems <= hi); if (xs.length) L(`    ${pad(name, 10)} ${pad(xs.length, 4)} runs · median ${pad($(med(xs.map((x) => x.usd))), 7)} · median calls ${pad(med(xs.map((x) => x.calls)), 4)} · median peak ${pad(med(xs.map((x) => x.pkK)), 4)}K · median brief ${pad(med(xs.map((x) => x.briefChars)), 6)} chars`); }
    fs.mkdirSync(path.dirname(path.resolve(BRIEFS)), { recursive: true }); fs.writeFileSync(BRIEFS, JSON.stringify(rowsB.sort((a, b) => b.usd - a.usd)));
    L(`briefs → ${BRIEFS} (${(fs.statSync(BRIEFS).size / 1e6).toFixed(1)} MB · ${rowsB.length} runs)`);
  }

  // A root, directory or file we could not READ is not an empty result — it is a failure to
  // look, and a report built over one must never exit as though it had covered everything.
  if (SCAN.readErrors.length) { console.error(`token-audit: ${SCAN.readErrors.length} read error(s); this report is INCOMPLETE — ${SCAN.readErrors.slice(0, 5).join(" | ")}`); return 1; }
  return 0;
}
