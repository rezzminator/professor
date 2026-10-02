// claude.mjs — Claude Code transcripts: the context categories, the per-file replay, and the run collection.
import fs from "node:fs";
import path from "node:path";
import { die } from "./options.mjs";
import { SCAN, G, RUNS, roots, walk, linesOf, bump, bump2, foldCwd, isHome } from "./scan.mjs";
import { LONG_CTX_TOKENS, RATE, TOP4, usageOf, writesOf } from "./pricing.mjs";
import { shortModel } from "./format.mjs";

// ---------- categories: what a carried token is made of
export const CATS = ["fixed prefix (system prompt, tool schemas, CLAUDE.md)", "prompts + briefs", "task notifications (async agent reports)",
  "own earlier output (text, tool inputs)", "Read · first time", "Read · same file again", "Bash · test/build/docker runs",
  "Bash · reading (cat/grep/find…)", "Bash · git", "Bash · other", "Grep/Glob", "Edit/Write acks", "sub-agent reports (sync)",
  "skill bodies", "MCP + web", "other tools", "harness injections (reminders, memory, listings)", "post-compaction summary",
  "unexplained delta", "OUTPUT generated (text + thinking + tool inputs)"];
const C = Object.fromEntries(CATS.map((c, i) => [c, i]));
export const [C_PREFIX, C_PROMPT, C_NOTIF, C_OWN, C_READ1, C_READN, C_BTEST, C_BREAD, C_BGIT, C_BOTHER, C_GREP, C_EDIT, C_AGENT, C_SKILL, C_MCP, C_OTHER, C_HARNESS, C_SUMMARY, C_UNEXPL, C_OUT] = CATS.map((_, i) => i);
G.cats = new Float64Array(CATS.length);
// A test/build RUN, not merely a command that names a build tool: `dev.sh status` reports
// state and runs nothing, and a bare `tsc` inside prose is not an invocation — both were
// counted here before and inflated every "tests per run" figure.
const RX_TEST = /\b(go\s+(test|build|vet)|pytest|vitest|jest|playwright|cypress|(npm|pnpm|yarn|bun)\s+(run\s+)?(test|e2e|check|lint|typecheck|build)\S*|cargo\s+(test|build|check)|make\s+\S*(test|check|build|verify)\S*|dev\.sh\s+(all|build|test|typecheck|verify|install)\b|golangci-lint|eslint|mvn\s|gradlew?\s|phpunit|rspec|docker\s+(compose\s+)?(build|run|exec|up)|npx\s+(vitest|jest|playwright|tsc)|TEST_[A-Z_]+=|run-?lanes?|test\S*\.sh)/;
// A wait is a poll even when the command never repeats: one `sleep 300` costs a whole
// re-read of the context for nothing said.
const RX_POLL = /(^|[;&|(]\s*)(sleep|wait)\s|tail\s+-f\b|--watch\b/;
const RX_GIT = /(^|[;&|(]\s*|\s)(git|gh)\s/;
const RX_READ = /(^|[;&|(]\s*)(cat|sed|head|tail|rg|grep|find|ls|tree|wc|awk|jq|less|bat|fd|stat|diff)\b/;
const bashCat = (cmd) => (RX_TEST.test(cmd) ? C_BTEST : RX_GIT.test(cmd) ? C_BGIT : RX_READ.test(cmd) ? C_BREAD : C_BOTHER);
export const BANDS = [[50, "under 50K"], [100, "50–100K"], [150, "100–150K"], [200, "150–200K"], [400, "200–400K"], [Infinity, "over 400K"]];
const contentChars = (c) => typeof c === "string" ? c.length : Array.isArray(c) ? c.reduce((a, b) => a + (b.type === "text" ? (b.text || "").length : b.type === "image" ? 6000 : JSON.stringify(b).length), 0) : c ? JSON.stringify(c).length : 0;
// --timeline: what a tool call aimed at, one line, whatever the tool
const tlTarget = (inp) => { const t = String(inp.file_path ?? inp.command ?? inp.pattern ?? inp.path ?? inp.subagent_type ?? inp.skill ?? inp.url ?? inp.query ?? Object.values(inp).find((v) => typeof v === "string") ?? "").replace(/\s+/g, " ").trim();
  return t.length > 100 ? t.slice(0, 99) + "…" : t; };
const TL_BIG = 20 * 1024; // --timeline: a tool result above this many chars counts as big
// message.ids already billed by an earlier transcript in this scan (forked/resumed sessions copy them)
const PRICED_IDS = new Set();

export function auditFile(file, opts) {
  const { tl: TL, since: SINCE, hours: HOURS, project: PROJECT } = opts;
  const isSub = file.includes(`${path.sep}subagents${path.sep}`);
  const segsOf = file.split(path.sep), si = segsOf.lastIndexOf("subagents");
  const sid = isSub ? segsOf[si - 1] : path.basename(file, ".jsonl");
  let meta = {}; if (isSub) { const mp = file.replace(/\.jsonl$/, ".meta.json");
    try { meta = JSON.parse(fs.readFileSync(mp, "utf8")); } catch (e) { if (e.code !== "ENOENT") SCAN.readErrors.push(`${mp}: ${e.message}`); } }
  const seq = [], usage = new Map(), toolUses = new Map(), seenReads = new Map(), allCmd = new Map();
  let title = "", aiTitle = "", cwd0 = "", harnessUsd = null, compactPending = false, brief = null;
  const B = { tests: 0, testFails: 0, testCmd: {}, readsBeforeEdit: 0, firstEditCall: 0, edits: 0, editFiles: {} };
  // --timeline only: each call's issued tools (callId → slots), each slot by tool_use id, the record span
  const tlIssued = new Map(), tlSlot = new Map(); let nLines = 0, nBad = 0, recT0 = Infinity, recT1 = 0, tlBig = 0;
  for (const ln of linesOf(file)) { nLines++;
    let o; try { o = JSON.parse(ln); } catch { SCAN.badLines++; nBad++; continue; }
    const ts = o.timestamp ? Date.parse(o.timestamp) : NaN;
    if (TL && !Number.isNaN(ts)) { recT0 = Math.min(recT0, ts); recT1 = Math.max(recT1, ts); }
    if (!cwd0 && o.cwd) cwd0 = o.cwd;
    if (o.type === "custom-title") { title = o.customTitle || title; continue; }
    if (o.type === "agent-name") { title ||= o.agentName || ""; continue; }
    if (o.type === "ai-title") { aiTitle = o.aiTitle || aiTitle; continue; }
    if (o.type === "cost-state") { if (typeof o.totalCostUSD === "number") harnessUsd = o.totalCostUSD; continue; }
    if (o.type === "system" && o.subtype === "compact_boundary") { seq.push({ compact: true }); continue; }
    if (o.type === "assistant" && o.message?.usage) {
      const u = usageOf(o.message.usage), mdl = o.message.model, wr = writesOf(u);
      if (mdl === "<synthetic>" || !(TOP4(u) + wr.cw5 + wr.cw1)) { SCAN.syntheticCalls++; continue; }
      if (Number.isNaN(ts)) { SCAN.noTimestamp++; continue; }
      // one API response = one message.id, however many content-block lines repeat its usage
      const id = o.message.id || o.uuid || `${file}:${nLines}`;
      if (!usage.has(id)) seq.push({ call: id });
      usage.set(id, { u, m: mdl, ts, effort: o.effort ?? o.perTurnEffort ?? "-" });
      for (const b of o.message.content || []) if (b.type === "tool_use") { toolUses.set(b.id, { name: b.name, input: b.input || {}, ts });
        if (TL) { const slot = { name: b.name, target: tlTarget(b.input || {}), chars: null, err: false, dur: 0 }; tlSlot.set(b.id, slot);
          if (!tlIssued.has(id)) tlIssued.set(id, []); tlIssued.get(id).push(slot); } }
    } else if (o.type === "user") {
      const c = o.message?.content, notif = o.origin?.kind === "task-notification";
      const textCat = notif ? C_NOTIF : o.isCompactSummary ? C_SUMMARY : o.isMeta ? C_HARNESS : C_PROMPT;
      if (brief === null && isSub && !notif && !o.isMeta) brief = typeof c === "string" ? c : Array.isArray(c) && c.some((b) => b.type === "text") ? c.filter((b) => b.type === "text").map((b) => b.text).join("\n") : null;
      if (typeof c === "string") seq.push({ cat: textCat, chars: c.length, ts, trig: notif ? "agent report" : "user prompt" });
      else if (Array.isArray(c)) for (const b of c) {
        if (b.type === "tool_result") {
          const tu = toolUses.get(b.tool_use_id), chars = contentChars(b.content), name = tu?.name || "?";
          let cat = C_OTHER, target = "", bc = null;
          if (name === "Read") { target = tu.input.file_path || ""; if (!B.firstEditCall) B.readsBeforeEdit++; cat = seenReads.has(target) ? C_READN : C_READ1; seenReads.set(target, (seenReads.get(target) || 0) + 1); }
          else if (name === "Bash") { const full = String(tu.input.command || "").replace(/\s+/g, " "); target = full.slice(0, 140); cat = bc = bashCat(full);
            if (bc === C_BTEST) { B.tests++; const txt = (typeof b.content === "string" ? b.content : JSON.stringify(b.content || "")).slice(0, 40000); if (b.is_error || /\b(FAIL|FAILED|failed|Error:|✗|×)\b|exit code [1-9]/.test(txt)) B.testFails++; B.testCmd[full.replace(/^cd \S+ (&&|;) /, "").slice(0, 200)] = (B.testCmd[full.replace(/^cd \S+ (&&|;) /, "").slice(0, 200)] || 0) + 1; }
            if (bc === C_BREAD && !B.firstEditCall) B.readsBeforeEdit++; allCmd.set(target, (allCmd.get(target) || 0) + 1); }
          else if (["Edit", "Write", "MultiEdit", "NotebookEdit"].includes(name)) { cat = C_EDIT; target = tu.input.file_path || ""; B.edits++; B.editFiles[target] = (B.editFiles[target] || 0) + 1; if (!B.firstEditCall) B.firstEditCall = usage.size; }
          else if (["Grep", "Glob", "LS"].includes(name)) { if (!B.firstEditCall) B.readsBeforeEdit++; cat = C_GREP; target = tu.input.pattern || tu.input.path || ""; }
          else if (name === "Agent" || name === "Task") { cat = C_AGENT; target = tu.input.subagent_type || tu.input.description || ""; }
          else if (name === "Skill") { cat = C_SKILL; target = tu.input.skill || ""; }
          else if (name.startsWith("mcp__") || name.startsWith("Web")) cat = C_MCP;
          seq.push({ cat, chars, ts, trig: "tool result", tool: name, target, bc, err: !!b.is_error, dur: tu && !Number.isNaN(ts) ? Math.max(0, ts - tu.ts) : 0 });
          if (TL) { const slot = tlSlot.get(b.tool_use_id); if (chars > TL_BIG) tlBig++; if (slot) Object.assign(slot, { chars, err: !!b.is_error, dur: seq[seq.length - 1].dur }); }
        } else if (b.type === "text") seq.push({ cat: textCat, chars: (b.text || "").length, ts, trig: notif ? "agent report" : "user prompt" });
        else if (b.type === "image") seq.push({ cat: textCat, chars: 6000, ts, trig: "user prompt" });
      }
    } else if (o.type === "attachment" && o.attachment?.type !== "prompt_snapshot") {
      const chars = JSON.stringify(o.attachment).length; seq.push({ cat: C_HARNESS, chars, ts, att: o.attachment?.type || "?" });
    }
  }
  // a timeline over a file with no parseable line is a failure to read it, never an empty run
  if (TL && nLines && nBad === nLines) throw new Error(`no line parsed as JSON (${nBad} malformed)`);
  const project = foldCwd(cwd0);
  if (PROJECT && !project.includes(PROJECT)) return null;

  // ---------- replay: ordered segments; each call reads [0,cr) at the row's read rate, writes [cr,cr+cw) at W, pays 1x for the rest
  const R = { file, kind: isSub ? "agent" : "main", sid, project, engine: "claude", agentId: isSub ? path.basename(file, ".jsonl").replace(/^agent-/, "") : sid,
    tok: { in: 0, out: 0, cw5: 0, cw1: 0, cr: 0 }, contractReads: 0, failedCmds: 0, unpriced: false,
    title: isSub ? (meta.description || "") : (title || aiTitle || ""), agentType: meta.agentType || "", depth: meta.spawnDepth || 0,
    calls: 0, usd: 0, usdBy: { in: 0, out: 0, cw5: 0, cw1: 0, cr: 0 }, models: {}, efforts: {}, t0: 0, t1: 0, activeMs: 0, toolWaitMs: 0, ctxFirst: 0, ctxPeak: 0, ctxSum: 0,
    bands: {}, rewrites: { n: 0, usd: 0, by: {}, nBy: {} }, resets: 0, tools: {}, bash: {}, errs: 0, rereadN: 0, rereadChars: 0, cats: new Float64Array(CATS.length), series: [], harnessUsd, topRepeat: null, brief, B, distinctRead: 0, land: new Float64Array(CATS.length), smallUsd: 0, smallN: 0, pollUsd: 0, pollN: 0, usdLC: 0, f: [0, 0, 0, 0, 0, 0], attach: {}, repeats: [], tl: new Float64Array(48), tlf: Array.from({ length: 5 }, () => new Float64Array(48)) };
  let segs = [], catTok = new Float64Array(CATS.length), have = 0, pending = [], prevOut = 0, first = true, prefixTok = 0, prev = null;
  const push = (c, t) => { if (t <= 0) return; const l = segs[segs.length - 1]; if (l && l.c === c) l.t += t; else segs.push({ c, t }); catTok[c] += t; have += t; R.land[c] += t; };
  const trim = (x) => { for (const ownOnly of [true, false]) for (let i = segs.length - 1; i >= 0 && x > 0; i--) { const s = segs[i]; if (ownOnly && s.c !== C_OWN) continue;
      const d = Math.min(s.t, x); s.t -= d; catTok[s.c] -= d; have -= d; x -= d; } segs = segs.filter((s) => s.t > 0); };
  const landed = [], cmdCount = new Map(), tlRows = []; let lastToolTs = NaN;
  for (const e of seq) {
    if (e.compact) { compactPending = true; continue; }
    if (!e.call) { pending.push(e); if (e.tool && !Number.isNaN(e.ts)) lastToolTs = e.ts;
      if (e.ts >= SINCE && e.tool) { const t = (R.tools[e.tool] ??= { n: 0, chars: 0, err: 0 }); t.n++; t.chars += e.chars; if (e.err) { t.err++; R.errs++; }
        if (e.bc !== null && e.bc !== undefined) { const b = (R.bash[CATS[e.bc]] ??= { n: 0, chars: 0, err: 0, waitMs: 0 }); b.n++; b.chars += e.chars; b.waitMs += e.dur; if (e.err) b.err++; cmdCount.set(e.target, (cmdCount.get(e.target) || 0) + 1); }
        if (e.cat === C_READN) { R.rereadN++; R.rereadChars += e.chars; bump2(G.rereads, project + " · " + e.target.replace(project + "/", ""), "n", 1); bump2(G.rereads, project + " · " + e.target.replace(project + "/", ""), "chars", e.chars); }
        // the project contract the agent re-opens: by Read, or by a shell read of it
        if (/(?:CLAUDE|AGENTS)\.md/.test(String(e.target)) && (e.tool === "Read" || e.bc === C_BREAD)) R.contractReads++;
        if (e.tool === "Bash" && e.err) R.failedCmds++;
        R.toolWaitMs += e.dur; }
      if (e.ts >= SINCE && e.att) { bump2(G.attach, e.att, "n", 1); bump2(G.attach, e.att, "chars", e.chars); bump2(R.attach, e.att, "n", 1); bump2(R.attach, e.att, "chars", e.chars); }
      continue; }
    const { u, m, ts, effort } = usage.get(e.call), r = RATE(m);
    const tlPush = (usd) => { if (TL) tlRows.push({ n: R.calls, ts, gap: Number.isNaN(lastToolTs) ? null : ts - lastToolTs, ctx, out, usd, tools: tlIssued.get(e.call) || [] }); };
    if (R.firstTs === undefined) R.firstTs = ts;
    const { cw5, cw1, split } = writesOf(u), cwAll = cw5 + cw1;
    const cr = u.cache_read_input_tokens || 0, inp = u.input_tokens || 0, out = u.output_tokens || 0, ctx = inp + cr + cwAll;
    let reset = false;
    if (first) { const est = pending.map((p) => p.chars / 4), tot = est.reduce((a, b) => a + b, 0), f = tot > ctx * 0.8 ? ctx * 0.8 / tot : 1;
      prefixTok = ctx - tot * f; push(C_PREFIX, prefixTok); pending.forEach((p, i) => push(p.cat, est[i] * f)); first = false; }
    else { let fresh = ctx - have;
      if (compactPending || ctx < have * 0.6) { reset = true; R.resets++; segs = []; catTok = new Float64Array(CATS.length); have = 0; seenReads.clear();
        push(C_PREFIX, Math.min(prefixTok, ctx)); push(C_SUMMARY, ctx - have); }
      else if (fresh < 0) trim(-fresh);
      else { const o2 = Math.min(prevOut, fresh); push(C_OWN, o2); const rest = fresh - o2, tot = pending.reduce((a, p) => a + p.chars, 0);
        if (rest > 0 && tot > 0) for (const p of pending) { const t = rest * p.chars / tot; push(p.cat, t); if (p.tool && t > 8000) landed.push({ tool: p.tool, target: p.target, tok: t, at: R.calls, m }); }
        else if (rest > 0) push(C_UNEXPL, rest); } }
    compactPending = false;
    // a harness attachment (total_tokens_reminder) lands after most tool results: context, never the trigger
    const trigger = pending.findLast((p) => !p.att) || null, longest = pending.reduce((a, p) => (p.dur > (a?.dur || 0) ? p : a), null);
    const pendChars = pending.reduce((a, p) => a + p.chars, 0), onlyTools = pending.length > 0 && pending.every((p) => p.tool || p.att);
    pending = []; prevOut = out;
    if (ts < SINCE) { prev = { ctx, ts, m }; continue; }
    // A forked or resumed session copies earlier responses, message.id and all, into a new
    // transcript: the first transcript scanned bills the response, every copy counts nothing.
    if (!TL && PRICED_IDS.has(e.call)) { SCAN.copiedCalls++; prev = { ctx, ts, m }; continue; }
    if (!TL) PRICED_IDS.add(e.call);
    // An unpriced model is IGNORANCE, not a $0 spend: the call's tokens stay in every token
    // total and its run renders "n/a" in the $ column, exactly as the Codex side does.
    if (!r) { SCAN.unpricedCalls++; bump(SCAN.unpricedModels, m || "(none)", 1); R.unpriced = true;
      R.calls++; G.calls++; bump(R.models, m, 0); bump(R.efforts, effort, 0); tlPush(null);
      R.tok.in += inp; R.tok.out += out; R.tok.cw5 += cw5; R.tok.cw1 += cw1; R.tok.cr += cr;
      G.tok.in += inp; G.tok.out += out; G.tok.cw5 += cw5; G.tok.cw1 += cw1; G.tok.cr += cr;
      if (!R.t0) { R.t0 = ts; R.ctxFirst = ctx; } R.t1 = ts; R.ctxPeak = Math.max(R.ctxPeak, ctx); R.ctxSum += ctx;
      prev = { ctx, ts, m }; continue; }
    if (cwAll && !split) SCAN.tierUnknownCalls++;
    // Rates are taken as multiples of `base`, the input rate; a free model (`in: 0`, valid in
    // pfm.prices.json) uses base 1 so no ratio divides by zero; the uncached tail weighs un1 = in/base.
    const base = r.in || 1, ri = base / 1e6, rm = r.rd / base, un1 = r.in / base, W = cwAll ? (cw5 * r.w5 + cw1 * r.w1) / (cwAll * base) : r.w5 / base;
    const parts = { in: inp * r.in / 1e6, out: out * r.out / 1e6, cw5: cw5 * r.w5 / 1e6, cw1: cw1 * r.w1 / 1e6, cr: cr * r.rd / 1e6 }, usd = parts.in + parts.out + parts.cw5 + parts.cw1 + parts.cr;
    // attribute: charge everything as a cache read, then correct the tail beyond cr
    const read0 = R.cats[C_READ1] + R.cats[C_READN] + R.cats[C_BREAD], inj0 = R.cats[C_HARNESS];
    for (let k = 0; k < CATS.length; k++) if (catTok[k]) { const v = catTok[k] * rm * ri; R.cats[k] += v; G.cats[k] += v; }
    for (let i = segs.length - 1, pos = have; i >= 0 && pos > cr; i--) { const s = segs[i], a = pos - s.t, b = pos; pos = a;
      const rd = Math.max(0, Math.min(b, cr) - a), wr = Math.max(0, Math.min(b, cr + cwAll) - Math.max(a, cr)), un = Math.max(0, b - Math.max(a, cr + cwAll));
      const v = ((rd * rm + wr * W + un * un1) - s.t * rm) * ri; R.cats[s.c] += v; G.cats[s.c] += v; }
    R.cats[C_OUT] += parts.out; G.cats[C_OUT] += parts.out;
    for (const k in parts) { R.usdBy[k] += parts[k]; G.usd[k] += parts[k]; }
    G.tok.in += inp; G.tok.out += out; G.tok.cw5 += cw5; G.tok.cw1 += cw1; G.tok.cr += cr; G.tok.think += u.output_tokens_details?.thinking_tokens || 0; G.calls++;
    R.tok.in += inp; R.tok.out += out; R.tok.cw5 += cw5; R.tok.cw1 += cw1; R.tok.cr += cr;
    R.calls++; R.usd += usd; tlPush(usd); R.usdLC += ctx > LONG_CTX_TOKENS ? (usd - parts.out) * r.lcIn + parts.out * r.lcOut : usd;
    const ib = R.calls <= 50 ? "1 · calls 1–50" : R.calls <= 150 ? "2 · calls 51–150" : R.calls <= 300 ? "3 · calls 151–300" : "4 · calls 301+";
    bump2(G.callIdx, `${R.kind} ${ib}`, "usd", usd); bump2(G.callIdx, `${R.kind} ${ib}`, "calls", 1); bump2(G.callIdx, `${R.kind} ${ib}`, "ctx", ctx);
    bump2(G.tier, R.kind, "cw5", parts.cw5); bump2(G.tier, R.kind, "cw1", parts.cw1);
    const isSmall = onlyTools && pendChars < 1600 && out < 250;
    const isPoll = trigger?.tool === "Bash" && (allCmd.get(trigger.target) >= 8 || RX_POLL.test(trigger.target));
    if (isSmall) { R.smallUsd += usd; R.smallN++; bump2(G.small, R.kind, "usd", usd); bump2(G.small, R.kind, "n", 1); }
    if (isPoll) { R.pollUsd += usd; R.pollN++; bump2(G.poll, R.kind, "usd", usd); bump2(G.poll, R.kind, "n", 1); } bump(R.models, m, usd); bump(R.efforts, effort, usd);
    if (!R.t0) { R.t0 = ts; R.ctxFirst = ctx; } R.t1 = ts; R.ctxPeak = Math.max(R.ctxPeak, ctx); R.ctxSum += ctx;
    const gap = prev ? ts - prev.ts : 0; if (prev && gap < 5 * 60e3) R.activeMs += gap;
    const band = BANDS.find((b) => ctx / 1000 < b[0])[1]; bump(R.bands, band, usd); bump2(G.bands, band, "usd", usd); bump2(G.bands, band, "calls", 1);
    const mek = `${R.kind} · ${shortModel(m)} · effort ${effort}`; bump2(G.modelEffort, mek, "usd", usd); bump2(G.modelEffort, mek, "calls", 1); bump2(G.modelEffort, mek, "out", out);
    bump2(G.hourly, new Date(ts).toISOString().slice(0, 13), project, usd);
    let bust = 0, cause = "";
    if (prev && !reset && prev.ctx > 20000 && cr < prev.ctx * 0.5 && cwAll > prev.ctx * 0.5) {
      bust = parts.cw5 + parts.cw1;
      cause = prev.m !== m ? "model switched mid-run" : gap <= 5 * 60e3 ? "no idle gap: prefix invalidated"
        : trigger?.trig === "tool result" ? (longest && longest.dur > 5 * 60e3 ? `a ${longest.tool} call ran longer than the cache lives` : "waited >5m on a tool or permission")
        : trigger?.trig === "agent report" ? "idle >5m, woken by an agent report" : R.kind === "agent" ? "idle >5m in the middle of the run" : "idle >5m, then the user came back";
      R.rewrites.n++; R.rewrites.usd += bust; bump(R.rewrites.by, cause, bust); bump(R.rewrites.nBy, cause, 1);
      bump2(G.rewrites, `${R.kind} · ${cause}`, "usd", bust); bump2(G.rewrites, `${R.kind} · ${cause}`, "n", 1); bump2(G.rewrites, `${R.kind} · ${cause}`, "tokK", cwAll / 1000);
      if (gap > 60 * 60e3) bump2(G.rewrites, `${R.kind} · ${cause}`, "over60m", 1);
      if (cause.startsWith("a ")) { bump2(G.rewriteTool, longest.tool === "Bash" ? CATS[longest.bc] : longest.tool, "usd", bust); bump2(G.rewriteTool, longest.tool === "Bash" ? CATS[longest.bc] : longest.tool, "n", 1); } }
    const F = [ctx > 200000 ? usd : 0, isSmall || isPoll ? usd : 0, bust, R.cats[C_READ1] + R.cats[C_READN] + R.cats[C_BREAD] - read0, R.cats[C_HARNESS] - inj0];
    const tb = Math.max(0, Math.min(47, Math.floor((ts - SINCE) / (HOURS * 3600e3 / 48)))); R.tl[tb] += usd; F.forEach((v, k) => { R.f[k] += v; R.tlf[k][tb] += v; });
    R.series.push([Math.round(ctx / 1000), +(usd * 1000).toFixed(1), ...F.map((v) => +(v * 1000).toFixed(1))]);
    prev = { ctx, ts, m };
  }
  if (!R.calls) return null;
  if (TL) R.timeline = { rows: tlRows, t0: recT0, t1: recT1, big: tlBig };
  R.distinctRead = seenReads.size;
  for (const l of landed) { const after = R.calls - l.at, rr = RATE(l.m); if (after > 0 && rr) G.landings.push({ usd: l.tok * after * rr.rd / 1e6, tokK: Math.round(l.tok / 1000), after, tool: l.tool, target: String(l.target).slice(0, 90), run: RUNS.length }); }
  G.landings.sort((a, b) => b.usd - a.usd); G.landings.length = Math.min(G.landings.length, 200);
  for (const [cmd, n] of cmdCount) if (n >= 4) { const k = project + " · " + cmd; const g = (G.repeats[k] ??= { n: 0, maxInRun: 0, runs: 0 }); g.n += n; g.runs++; g.maxInRun = Math.max(g.maxInRun, n);
    if (!R.topRepeat || n > R.topRepeat.n) R.topRepeat = { cmd: cmd.slice(0, 90), n }; if (n >= 8) R.repeats.push([cmd.slice(0, 110), n]); }
  for (const [name, t] of Object.entries(R.tools)) { const g = (G.tools[name] ??= { n: 0, chars: 0, err: 0 }); g.n += t.n; g.chars += t.chars; g.err += t.err; }
  for (const [name, t] of Object.entries(R.bash)) { const g = (G.bash[name] ??= { n: 0, chars: 0, err: 0, waitMs: 0 }); g.n += t.n; g.chars += t.chars; g.err += t.err; g.waitMs += t.waitMs; }
  R.model = Object.entries(R.models).sort((a, b) => b[1] - a[1])[0][0]; R.effort = Object.entries(R.efforts).sort((a, b) => b[1] - a[1])[0][0];
  return R;
}

// ---------- run: every transcript in the window, deduped, replayed, then the --session filter and the project-root fold
export function collectRuns(opts) {
  const { session: SESSION } = opts;
  SCAN.roots = roots(opts); const files = []; for (const r of SCAN.roots) walk(r, files, opts);
  const seenKey = new Set();
  for (const f of files) { const key = f.split(path.sep).slice(-3).join("/"); if (seenKey.has(key)) { SCAN.dupFiles++; continue; } seenKey.add(key);
    SCAN.files++; let R; try { R = auditFile(f, opts); } catch (e) { SCAN.readErrors.push(`${f}: ${e.stack || e.message}`); continue; } if (R) RUNS.push(R); }

  // --session: the selector a sub-agent-orchestrated run HAS. A chat title exists only on a
  // main chat, so --family alone cannot select a family whose work an agent orchestrated.
  if (SESSION) { const kept = RUNS.filter((r) => r.sid.startsWith(SESSION)); const dropped = RUNS.length - kept.length;
    if (!kept.length) die(`--session ${SESSION} matched none of the ${RUNS.length} runs in this window; pass a session-id prefix as it appears in the transcript path`);
    SCAN.notes.push(`--session ${SESSION}: ${dropped} runs outside that session excluded`); RUNS.length = 0; RUNS.push(...kept); }

  // fold sub-folder chats onto the shortest enclosing project root (never onto a bare home dir)
  const projRoots = [...new Set(RUNS.map((r) => r.project))].filter((p) => p && !isHome(p)).sort((a, b) => a.length - b.length);
  for (const r of RUNS) { const root = projRoots.find((p) => r.project === p || r.project.startsWith(p + "/")); if (root) r.project = root; }
}
