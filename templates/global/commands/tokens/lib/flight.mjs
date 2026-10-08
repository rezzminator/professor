// flight.mjs — --flight DIR: the flight's ledger, the window it sets, and the per-agent metrics report.
import fs from "node:fs";
import path from "node:path";
import { die } from "./options.mjs";
import { SCAN, RUNS, gapsLine } from "./scan.mjs";
import { codexRoot, findCodexRollouts, readCodexMeta, parseRolloutName, codexShort, codexRole, codexAuditRun } from "./codex.mjs";
import { shortModel, ttlMix } from "./format.mjs";

// ---------- --flight DIR: the ledger a flight leaves behind, parsed BEFORE the scan
// because it, not --since, sets the window. agents.tsv is the primary key — append-only,
// tab-separated, one row per spawn: task-id · agent-type · agent-id · round · spawn-time
// (ISO) · engine (claude|codex|seat); an optional header line is tolerated. With no
// agents.tsv, run.md's header instant and its "{id} CLAIMED · {agent} · {time}" lines are
// the fallback, and EVERY row it yields is marked matched=window: run.md carries no agent id.
export const FLIGHT_PLAN = { rows: [], start: 0, baseline: "", source: "", gaps: [] };
export function loadFlightPlan(opts) {
  const { flight: FLIGHT } = opts;
  const dir = path.resolve(FLIGHT);
  let stat; try { stat = fs.statSync(dir); } catch (e) { die(`--flight ${FLIGHT} is not readable: ${e.message}`); }
  if (!stat.isDirectory()) die(`--flight ${FLIGHT} is not a directory`);
  const tsvPath = path.join(dir, "agents.tsv");
  let tsv = null;
  try { tsv = fs.readFileSync(tsvPath, "utf8"); }
  catch (e) { if (e.code !== "ENOENT") die(`${tsvPath} is not readable: ${e.message}`); }
  if (tsv !== null) {
    FLIGHT_PLAN.source = "agents.tsv";
    const lines = tsv.split("\n");
    for (let i = 0; i < lines.length; i++) { const ln = lines[i]; if (!ln.trim()) continue;
      const f = ln.split("\t").map((s) => s.trim());
      if (i === 0 && /^task[-_ ]?id$/i.test(f[0])) continue; // optional header
      if (f.length < 3 || !f[0] || !f[2]) { FLIGHT_PLAN.gaps.push(`agents.tsv line ${i + 1}: ${f.length} field(s), want task-id/agent-type/agent-id/round/spawn/engine — row skipped`); continue; }
      const at = f[4] ? Date.parse(f[4]) : NaN;
      if (f[4] && Number.isNaN(at)) FLIGHT_PLAN.gaps.push(`agents.tsv line ${i + 1}: spawn time "${f[4]}" did not parse`);
      // A date without a clock parses to midnight: it may date the flight, never open a match window.
      const dateOnly = Boolean(f[4]) && !/T\d{2}:\d{2}/.test(f[4]);
      if (dateOnly && !Number.isNaN(at)) FLIGHT_PLAN.gaps.push(`agents.tsv line ${i + 1}: spawn time "${f[4]}" has no clock — the row matches by id or agent path only, never by window`);
      FLIGHT_PLAN.rows.push({ taskId: f[0], agentType: f[1] || "", agentId: f[2], round: f[3] || "", at: Number.isNaN(at) ? 0 : at, dateOnly, engine: (f[5] || "").toLowerCase() || "?" }); }
    if (!FLIGHT_PLAN.rows.length) die(`${tsvPath} yielded no usable rows (${lines.filter((l) => l.trim()).length} non-empty lines) — it exists but this report would cover NOTHING`);
  } else {
    const runPath = path.join(dir, "run.md");
    let run; try { run = fs.readFileSync(runPath, "utf8"); } catch (e) { die(`${dir}: no agents.tsv, and ${runPath} is not readable either: ${e.message}`); }
    FLIGHT_PLAN.source = "run.md (no agents.tsv — every row matched by window)";
    const lines = run.split("\n"), head = lines[0] || "";
    const hb = /baseline\s+([0-9a-f]{7,40})/.exec(head), ht = /(\d{4}-\d{2}-\d{2}T[\d:.]+(?:Z|[+-]\d{2}:?\d{2})?)/.exec(head);
    if (!ht) die(`${runPath} line 1 carries no parseable flight instant — refusing to invent a window`);
    FLIGHT_PLAN.start = Date.parse(ht[1]); FLIGHT_PLAN.baseline = hb ? hb[1] : "";
    if (Number.isNaN(FLIGHT_PLAN.start)) die(`${runPath} line 1 instant "${ht[1]}" did not parse`);
    for (const ln of lines) { const m = /^(\S+)\s+CLAIMED\s+·\s+(.*)$/.exec(ln.trim()); if (!m) continue;
      const rest = m[2], tm = /(\d{4}-\d{2}-\d{2}T[\d:.]+(?:Z|[+-]\d{2}:?\d{2})?)/.exec(rest);
      FLIGHT_PLAN.rows.push({ taskId: m[1], agentType: rest.split("·")[0].trim().replace(/\s+requested.*$/, ""), agentId: "", round: "", at: tm ? Date.parse(tm[1]) : FLIGHT_PLAN.start, engine: "?" }); }
    if (!FLIGHT_PLAN.rows.length) die(`${runPath} has a header but no "{id} CLAIMED · {agent}" line — this report would cover NOTHING`);
  }
  const times = [FLIGHT_PLAN.start, ...FLIGHT_PLAN.rows.map((r) => r.at)].filter((t) => t > 0);
  if (!times.length) die(`${dir}: no parseable spawn time in agents.tsv and no run.md header instant — the flight cannot be dated`);
  FLIGHT_PLAN.start = Math.min(...times);
  opts.since = FLIGHT_PLAN.start - 10 * 60e3; // 10 minutes of slack: a transcript's first call precedes its ledger row
  opts.hours = Math.max(1, (opts.now - opts.since) / 3600e3);
}

// the call cap comes from the agent type name: a foreman 250, a lander 200, everything else 150
export const callCap = (type) => (/foreman/i.test(type) ? 250 : /lander/i.test(type) ? 200 : 150);

// ---------- --flight: one row per agent the flight spawned, bounded whatever its size
export function runFlight(opts) {
  const { flight: FLIGHT, metricsOut: METRICS_OUT, out: OUT, since: SINCE } = opts;
  const dir = path.resolve(FLIGHT);
  const MAX_ROWS = 60, MAX_UNMATCHED = 15, WINDOW_BEFORE = 10 * 60e3, WINDOW_AFTER = 180 * 60e3;
  const norm = (s) => String(s || "").toLowerCase().replace(/[^a-z0-9]+/g, " ").trim();
  const roleMatch = (a, b) => { const x = norm(a), y = norm(b); if (!x || !y) return false;
    return x.includes(y) || y.includes(x) || x.split(" ").pop() === y.split(" ").pop(); };
  const claudeAgents = RUNS.filter((r) => r.kind === "agent");
  const byAgentId = new Map(); for (const r of claudeAgents) byAgentId.set(r.agentId, r);

  // Codex candidates: meta only. A full audit costs a whole-file decode, so it runs on matches.
  const cxRoot = codexRoot(opts), cxCand = [];
  for (const file of findCodexRollouts(cxRoot)) { const nm = parseRolloutName(file); let st;
    try { st = fs.statSync(file); } catch (e) { SCAN.readErrors.push(`${file}: ${e.message}`); continue; }
    if (st.mtimeMs < SINCE && (nm?.at ?? 0) < SINCE) continue;
    const meta = readCodexMeta(file); const at = Date.parse(meta?.timestamp || "") || nm?.at || st.mtimeMs;
    // ids are THIS thread's own: session_id and parent_thread_id name the parent on a sub-agent thread, and would hand a child the orchestrator's row.
    cxCand.push({ file, meta, at, role: codexRole(meta), agentPath: String(meta?.agent_path || meta?.source?.subagent?.thread_spawn?.agent_path || ""), ids: [meta?.id, meta?.context_window?.window_id, nm?.id].filter(Boolean).map(String) }); }
  const cxById = new Map(); for (const c of cxCand) for (const id of c.ids) if (!cxById.has(id)) cxById.set(id, c);
  // A Codex orchestrator knows its sub-agent by agent path ("/root/fix_1a"), never by thread id. A path is
  // unique inside one thread tree only, so among the same path the spawn nearest the row's time wins.
  const cxByPath = (led) => cxCand.filter((c) => c.agentPath && c.agentPath === led.agentId && !takenCodex.has(c.file))
    .sort((a, b) => (led.at && !led.dateOnly ? Math.abs(a.at - led.at) - Math.abs(b.at - led.at) : b.at - a.at))[0];

  const takenClaude = new Set(), takenCodex = new Set(), rows = [], unmatched = [], roleMismatch = [];
  const cxAudit = new Map(); const auditCx = (c) => { if (!cxAudit.has(c.file)) cxAudit.set(c.file, codexAuditRun(c.file, c.meta)); return cxAudit.get(c.file); };

  for (const led of FLIGHT_PLAN.rows) {
    const wantClaude = led.engine === "claude" || led.engine === "?" || led.engine === "seat";
    const wantCodex = led.engine === "codex" || led.engine === "?" || led.engine === "seat";
    let run = null, how = "";
    if (led.agentId && wantClaude && byAgentId.has(led.agentId) && !takenClaude.has(led.agentId)) { run = byAgentId.get(led.agentId); takenClaude.add(led.agentId); how = "id"; }
    if (!run && led.agentId && wantCodex && cxById.has(led.agentId) && !takenCodex.has(cxById.get(led.agentId).file)) { const c = cxById.get(led.agentId); takenCodex.add(c.file); run = auditCx(c); how = "id"; }
    if (!run && led.agentId.startsWith("/") && wantCodex) { const c = cxByPath(led); if (c) { takenCodex.add(c.file); run = auditCx(c); how = "path"; } }
    if (!run && led.at && !led.dateOnly) { // the id form did not match (or there is none): fall back to THIS row's window + type
      const lo = led.at - WINDOW_BEFORE, hi = led.at + WINDOW_AFTER;
      const cc = wantClaude ? claudeAgents.filter((r) => !takenClaude.has(r.agentId) && r.t0 >= lo && r.t0 <= hi) : [];
      const cx = wantCodex ? cxCand.filter((c) => !takenCodex.has(c.file) && c.at >= lo && c.at <= hi) : [];
      const pickC = cc.filter((r) => roleMatch(r.agentType || r.title, led.agentType)).sort((a, b) => Math.abs(a.t0 - led.at) - Math.abs(b.t0 - led.at))[0] || cc.sort((a, b) => Math.abs(a.t0 - led.at) - Math.abs(b.t0 - led.at))[0];
      const pickX = cx.filter((c) => roleMatch(c.role, led.agentType)).sort((a, b) => Math.abs(a.at - led.at) - Math.abs(b.at - led.at))[0] || cx.sort((a, b) => Math.abs(a.at - led.at) - Math.abs(b.at - led.at))[0];
      const useX = pickX && (!pickC || Math.abs(pickX.at - led.at) < Math.abs(pickC.t0 - led.at));
      if (useX) { takenCodex.add(pickX.file); run = auditCx(pickX); how = "window"; if (!roleMatch(pickX.role, led.agentType)) roleMismatch.push(`${led.taskId}→${pickX.role || "?"}`); }
      else if (pickC) { takenClaude.add(pickC.agentId); run = pickC; how = "window"; if (!roleMatch(pickC.agentType || pickC.title, led.agentType)) roleMismatch.push(`${led.taskId}→${pickC.agentType || "?"}`); } }
    if (!run) { unmatched.push(`LEDGER ROW ${led.taskId} · ${led.agentType || "?"} · id ${led.agentId || "(none)"} · engine ${led.engine} · no transcript found in the window`); continue; }
    rows.push({ led, run, how });
  }
  const K = (v) => (v >= 1e6 ? (v / 1e6).toFixed(1) + "M" : v >= 1000 ? Math.round(v / 1000) + "K" : String(Math.round(v)));
  const cash = (r) => (r.unpriced ? "n/a" : "$" + r.usd.toFixed(2));
  // A transcript inside the window with no ledger row is a hole in the ledger, never a drop:
  // it is priced, so a child foreman, a lander or a skill-spawned review never vanishes from the spend.
  const parents = new Set(rows.map((x) => x.run.sid).filter(Boolean)), unledgered = [];
  for (const r of claudeAgents) if (!takenClaude.has(r.agentId) && parents.has(r.sid) && r.t0 >= SINCE) { unledgered.push(r);
    unmatched.push(`TRANSCRIPT ${r.agentType || "agent"} · claude · ${r.agentId.slice(0, 12)} · ${new Date(r.t0).toISOString().slice(0, 16)} · ${cash(r)} · ran under this flight's session with no ledger row`); }
  for (const c of cxCand) if (!takenCodex.has(c.file) && parents.has(String(c.meta?.parent_thread_id || c.meta?.session_id || "")) ) { const r = auditCx(c); unledgered.push(r);
    unmatched.push(`TRANSCRIPT ${c.role || "codex"} · codex · ${String(c.ids[0]).slice(-12)} · ${new Date(c.at).toISOString().slice(0, 16)} · ${cash(r)} · ran under this flight's thread with no ledger row`); }
  const U = { n: unledgered.length, usd: unledgered.reduce((a, r) => a + (r.unpriced ? 0 : r.usd), 0), unpriced: unledgered.filter((r) => r.unpriced).length };
  const mins = (r) => (r.t1 && r.t0 ? Math.round((r.t1 - r.t0) / 60e3) + "m" : "?");
  const growth = (r) => (r.calls ? K(Math.max(0, r.ctxPeak - r.ctxFirst) / r.calls) : "?");
  const out = [];
  const startISO = new Date(FLIGHT_PLAN.start).toISOString(), endISO = new Date(Math.max(FLIGHT_PLAN.start, ...rows.map((x) => x.run.t1 || 0))).toISOString();
  out.push(`# ${path.basename(dir)} · metrics`, "",
    `source ${FLIGHT_PLAN.source}${FLIGHT_PLAN.baseline ? ` · baseline ${FLIGHT_PLAN.baseline}` : ""} · window ${startISO} → ${endISO}`,
    `${rows.length} matched of ${FLIGHT_PLAN.rows.length} ledger rows · claude ${rows.filter((x) => x.run.engine === "claude").length} · codex ${rows.filter((x) => x.run.engine === "codex").length} · by id ${rows.filter((x) => x.how === "id").length} · by agent path ${rows.filter((x) => x.how === "path").length} · by window ${rows.filter((x) => x.how === "window").length}`, "");
  out.push("| task | agent | engine | model | calls | wall | ctx0 | peak | +/call | input | cached | output | ttl | $ | fail | poll | reread | contract | compact | cap | matched |",
    "|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|");
  const sorted = [...rows].sort((a, b) => b.run.usd - a.run.usd);
  for (const { led, run, how } of sorted.slice(0, MAX_ROWS)) {
    const cap = callCap(led.agentType || run.agentType);
    out.push(`| ${led.taskId} | ${led.agentType || run.agentType || "?"} | ${run.engine} | ${shortModel(codexShort(run.model))} | ${run.calls} | ${mins(run)} | ${K(run.ctxFirst)} | ${K(run.ctxPeak)} | ${growth(run)} | ${K(run.tok.in + run.tok.cw5 + run.tok.cw1)} | ${K(run.tok.cr)} | ${K(run.tok.out)} | ${ttlMix(run.tok)} | ${cash(run)} | ${run.failedCmds} | ${run.pollN} | ${run.rereadN} | ${run.contractReads} | ${run.resets} | ${run.calls > cap ? `OVER ${cap}` : `ok/${cap}`} | ${how} |`);
  }
  if (sorted.length > MAX_ROWS) out.push(`| … | ${sorted.length - MAX_ROWS} further agents folded into the totals below | | | | | | | | | | | | | | | | | | | |`);
  const fold = (rs) => rs.reduce((a, x) => ({ n: a.n + 1, calls: a.calls + x.run.calls, usd: a.usd + x.run.usd, unpriced: a.unpriced + (x.run.unpriced ? 1 : 0),
    in: a.in + x.run.tok.in + x.run.tok.cw5 + x.run.tok.cw1, cr: a.cr + x.run.tok.cr, out: a.out + x.run.tok.out, fail: a.fail + x.run.failedCmds, poll: a.poll + x.run.pollN,
    reread: a.reread + x.run.rereadN, contract: a.contract + x.run.contractReads, compact: a.compact + x.run.resets, over: a.over + (x.run.calls > callCap(x.led.agentType || x.run.agentType) ? 1 : 0),
    peak: Math.max(a.peak, x.run.ctxPeak), lc: a.lc + (x.run.usdLC || x.run.usd) }), { n: 0, calls: 0, usd: 0, unpriced: 0, in: 0, cr: 0, out: 0, fail: 0, poll: 0, reread: 0, contract: 0, compact: 0, over: 0, peak: 0, lc: 0 });
  const byType = {}; for (const x of rows) (byType[x.led.agentType || x.run.agentType || "(untyped)"] ??= []).push(x);
  out.push("", "## totals by agent type", "", "| agent type | n | calls | input | cached | output | $ | fail | poll | reread | contract | compact | over cap |", "|---|---|---|---|---|---|---|---|---|---|---|---|---|");
  for (const [k, rs] of Object.entries(byType).sort((a, b) => fold(b[1]).usd - fold(a[1]).usd).slice(0, 12)) { const f = fold(rs);
    out.push(`| ${k} | ${f.n} | ${f.calls} | ${K(f.in)} | ${K(f.cr)} | ${K(f.out)} | ${f.unpriced ? `$${f.usd.toFixed(2)} + ${f.unpriced} n/a` : "$" + f.usd.toFixed(2)} | ${f.fail} | ${f.poll} | ${f.reread} | ${f.contract} | ${f.compact} | ${f.over} |`); }
  const T = fold(rows);
  out.push("", "## flight total", "",
    `${T.n} agents · ${T.calls} calls · peak context ${K(T.peak)} · input ${K(T.in)} · cached ${K(T.cr)} · output ${K(T.out)} · **$${T.usd.toFixed(2)}**${T.unpriced ? ` + ${T.unpriced} unpriced agent(s) at "n/a" (their tokens ARE above, their dollars are NOT)` : ""}`,
    `failed commands ${T.fail} · poll calls ${T.poll} · re-reads ${T.reread} · contract-file reads ${T.contract} · compactions ${T.compact} · over cap ${T.over} of ${T.n}`,
    ...(U.n ? [`unledgered ${U.n} transcript(s) under this flight's session · $${U.usd.toFixed(2)}${U.unpriced ? ` + ${U.unpriced} n/a` : ""} — the flight's spend is $${(T.usd + U.usd).toFixed(2)} (see the unmatched section)`] : []), "",
    "## three most expensive agents", "");
  for (const { led, run } of sorted.slice(0, 3)) out.push(`- ${cash(run)} · ${led.taskId} · ${led.agentType || run.agentType || "?"} · ${run.engine} · ${run.calls} calls · peak ${K(run.ctxPeak)} · ${mins(run)}`);
  out.push("", "## unmatched", "");
  if (!unmatched.length) out.push("none — every ledger row found a transcript and every transcript in the window has a ledger row");
  else { for (const u of unmatched.slice(0, MAX_UNMATCHED)) out.push(`- ${u}`);
    if (unmatched.length > MAX_UNMATCHED) out.push(`- … and ${unmatched.length - MAX_UNMATCHED} more`); }
  // one aggregate note, not one per row: a 200-agent flight must not bury the gaps line
  if (roleMismatch.length) FLIGHT_PLAN.gaps.push(`${roleMismatch.length} window match(es) whose transcript role differs from the ledger's agent type (${roleMismatch.slice(0, 5).join(", ")}${roleMismatch.length > 5 ? ", …" : ""})`);
  for (const g of FLIGHT_PLAN.gaps) SCAN.notes.push(g);
  if (unmatched.length) SCAN.notes.push(`${unmatched.length} UNMATCHED (see the unmatched section)`);
  out.push("", gapsLine(), "",
    `cross-check: at the published context tiers (per-model rate in the live pfm model-cost catalog — an estimate) this flight reads $${T.lc.toFixed(2)} (${T.usd ? (T.lc / T.usd).toFixed(2) : "n/a"}x the headline). ` +
    `The harness writes its own cost-state line only for a main chat, and a flight's agents are sub-runs, so these dollars are UNCHECKED against the harness on this host.`);
  const dest = METRICS_OUT ? path.resolve(METRICS_OUT) : path.join(dir, "metrics.md");
  fs.mkdirSync(path.dirname(dest), { recursive: true });
  fs.writeFileSync(dest, out.join("\n") + "\n");
  console.log(out.join("\n"));
  console.log(`\nmetrics → ${dest} (${out.length} lines)`);
  if (OUT) { fs.mkdirSync(path.dirname(path.resolve(OUT)), { recursive: true });
    fs.writeFileSync(OUT, JSON.stringify({ v: 1, flight: path.basename(dir), source: FLIGHT_PLAN.source, baseline: FLIGHT_PLAN.baseline, window: [startISO, endISO], scan: SCAN, total: T,
      rows: sorted.map(({ led, run, how }) => ({ ...led, how, engine: run.engine, model: run.model, calls: run.calls, wallMs: run.t1 - run.t0, ctxFirst: run.ctxFirst, ctxPeak: run.ctxPeak,
        tok: run.tok, usd: run.unpriced ? null : +run.usd.toFixed(4), failedCmds: run.failedCmds, pollN: run.pollN, rereadN: run.rereadN, contractReads: run.contractReads, compactions: run.resets,
        overCap: run.calls > callCap(led.agentType || run.agentType) })), unmatched, unledgered: { n: U.n, usd: +U.usd.toFixed(4), unpriced: U.unpriced } }, null, 1));
    console.log(`full data → ${OUT}`); }
  // UNMATCHED is a reported condition, not a failure. A read error is: it means we failed
  // to LOOK, and a report built over a root we could not read must not read as success.
  return SCAN.readErrors.length ? 1 : 0;
}
