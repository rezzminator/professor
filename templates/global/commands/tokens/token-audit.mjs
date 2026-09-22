#!/usr/bin/env node
// token-audit.mjs — read-only audit of agent transcripts: where did the tokens go?
// Claude Code JSONL transcripts AND Codex CLI rollouts, one pricing table for both.
// Zero dependencies (node: builtins only). No network. Bounded text report; --out FILE writes JSON.
//
//   node token-audit.mjs [--since 24h|3d] [--root DIR]... [--project SUBSTR]
//                        [--family SUBSTR] [--session SID] [--top N] [--out FILE]
//   node token-audit.mjs --codex [--since 24h|3d] [--project SUBSTR] [--codex-root DIR] [--top N]
//   node token-audit.mjs --flight DIR [--metrics-out FILE] [--out FILE]
//
// Unit of analysis: a RUN = one transcript file = one main chat loop, one sub-agent,
// or one Codex rollout thread. A FAMILY = a main chat plus every sub-agent it spawned.
// Dollars are list-price estimates: they rank and compare, they are not a bill. A model
// with no PRICING row renders "n/a" — never $0 — and its tokens still count.
import fs from "node:fs";
import path from "node:path";
import os from "node:os";
import { StringDecoder } from "node:string_decoder";

const argv = process.argv.slice(2);
const die = (msg) => { console.error("token-audit: " + msg); process.exit(2); };
const opt = (k, d) => { const i = argv.indexOf(k); return i >= 0 && argv[i + 1] !== undefined ? argv[i + 1] : d; };
const optAll = (k) => argv.flatMap((a, i) => (a === k && argv[i + 1] ? [argv[i + 1]] : []));
const sm = /^(\d+(?:\.\d+)?)([hd])$/.exec(opt("--since", "24h"));
if (!sm) die("--since wants a number plus h or d, e.g. 24h or 3d");
let HOURS = sm[2] === "d" ? +sm[1] * 24 : +sm[1];
const NOW = Date.now();
let SINCE = NOW - HOURS * 3600e3;
const TOP = +opt("--top", 12), PROJECT = opt("--project", ""), FAMILY = opt("--family", ""), SESSION = opt("--session", ""), OUT = opt("--out", ""), VIEW = opt("--view", ""), BRIEFS = opt("--briefs", ""), FAMRX = new RegExp(opt("--family-rx", "."), "i");
const FLIGHT = opt("--flight", ""), METRICS_OUT = opt("--metrics-out", ""), CODEX = argv.includes("--codex"), CODEX_ROOT = opt("--codex-root", "");

// ─── EDITABLE PRICING (USD per 1M tokens) ─────────────────────────────────────
// Matched by substring on the lowercased model id; FIRST match wins, so keep more
// specific ids above broader ones. A row whose substring no real id contains is dead
// code — scripts/check-token-pricing.mjs resolves published ids against this table.
// Columns: [substring, input, output, cachedInput, longCtxInMult?, longCtxOutMult?]
//   cachedInput  — Claude: the cache-READ rate. Codex: the cached_input rate (cached
//                  input is a SUBSET of input there); Claude cache WRITES bill at
//                  1.25x input (5-minute TTL) or 2x (1-hour TTL), computed below.
//   longCtx*     — ESTIMATE, not a published per-model number: the multiplier applied
//                  to a call whose context exceeds LONG_CTX_TOKENS. Claude publishes a
//                  2x-input / 1.5x-output long-context tier; this table carries it per
//                  model so a re-priced tier is a one-row edit. It feeds the CROSS-CHECK
//                  line only — the headline totals never include it.
const PRICING = [
  ["opus-4-1", 15.0, 75.0, 1.5, 2, 1.5], // deprecated Opus 4.1-era tier
  ["opus-4-20", 15.0, 75.0, 1.5, 2, 1.5], // Opus 4.0 ids carry no minor digit: claude-opus-4-<date>
  ["opus", 5.0, 25.0, 0.5, 2, 1.5], // current tier: opus-5, opus-4-8 … opus-4-5
  ["sonnet-4", 3.0, 15.0, 0.3, 2, 1.5],
  ["sonnet-5", 2.0, 10.0, 0.2, 2, 1.5],
  ["sonnet", 3.0, 15.0, 0.3, 2, 1.5], // older sonnet catch-all (3.7 etc.)
  ["haiku-4-5", 1.0, 5.0, 0.1, 2, 1.5],
  ["haiku", 0.8, 4.0, 0.08, 2, 1.5], // haiku 3.5/3 catch-all
  ["fable-5-1", 10.0, 50.0, 0.25, 2, 1.5], // 5.1 cache reads bill at 0.025x input, not 0.1x
  ["mythos-5-1", 10.0, 50.0, 0.25, 2, 1.5],
  ["fable", 10.0, 50.0, 1.0, 2, 1.5],
  ["mythos", 10.0, 50.0, 1.0, 2, 1.5],
  // Codex CLI. Published standard-tier rates; reasoning bills as output; cache-write is
  // always 0. Fast mode (2x) and Batch/Flex (0.5x) are not modelled, and the long-context
  // overage never applies while the Codex window stays under its 272K threshold — hence 1/1.
  ["gpt-6-astra", 10.0, 50.0, 1.0, 1, 1],
  ["gpt-5.6-sol", 4.0, 20.0, 0.4, 1, 1], // promotional rate
  ["gpt-5.6-luna", 0.2, 1.2, 0.02, 1, 1],
];
const LONG_CTX_TOKENS = 200000;
const RATE = (m) => { const id = String(m || "").toLowerCase(); const row = PRICING.find(([sub]) => id.includes(sub)); if (!row) return null;
  return { in: row[1], out: row[2], rd: row[3], w5: row[1] * 1.25, w1: row[1] * 2, lcIn: row[4], lcOut: row[5] }; };
const shortModel = (m) => (m || "?").replace(/^claude-/, "").replace(/-\d{8}$/, "");

// ---------- categories: what a carried token is made of
const CATS = ["fixed prefix (system prompt, tool schemas, CLAUDE.md)", "prompts + briefs", "task notifications (async agent reports)",
  "own earlier output (text, tool inputs)", "Read · first time", "Read · same file again", "Bash · test/build/docker runs",
  "Bash · reading (cat/grep/find…)", "Bash · git", "Bash · other", "Grep/Glob", "Edit/Write acks", "sub-agent reports (sync)",
  "skill bodies", "MCP + web", "other tools", "harness injections (reminders, memory, listings)", "post-compaction summary",
  "unexplained delta", "OUTPUT generated (text + thinking + tool inputs)"];
const C = Object.fromEntries(CATS.map((c, i) => [c, i]));
const [C_PREFIX, C_PROMPT, C_NOTIF, C_OWN, C_READ1, C_READN, C_BTEST, C_BREAD, C_BGIT, C_BOTHER, C_GREP, C_EDIT, C_AGENT, C_SKILL, C_MCP, C_OTHER, C_HARNESS, C_SUMMARY, C_UNEXPL, C_OUT] = CATS.map((_, i) => i);
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
const BANDS = [[50, "under 50K"], [100, "50–100K"], [150, "100–150K"], [200, "150–200K"], [400, "200–400K"], [Infinity, "over 400K"]];

// ---------- discovery
function roots() {
  const given = optAll("--root");
  let cc = []; try { const d = path.join(os.homedir(), ".cc"); cc = fs.readdirSync(d).map((n) => path.join(d, n, "projects")); } catch { /* no ~/.cc: fine */ }
  const cand = given.length ? given : [process.env.CLAUDE_CONFIG_DIR && path.join(process.env.CLAUDE_CONFIG_DIR, "projects"), path.join(os.homedir(), ".claude/projects"), ...cc].filter(Boolean);
  const seen = new Set(), out = [];
  for (const c of cand) { let r; try { r = fs.realpathSync(c); } catch (e) { if (given.length) die(`--root ${c} is not readable: ${e.message}`); continue; }
    if (!seen.has(r)) { seen.add(r); out.push(r); } }
  if (!out.length) die("no transcript root found; pass --root DIR");
  return out;
}
const SCAN = { roots: [], files: 0, skippedOld: 0, dupFiles: 0, badLines: 0, noTimestamp: 0, unpricedCalls: 0, unpricedModels: {}, tierUnknownCalls: 0, syntheticCalls: 0, readErrors: [], notes: [] };
// Every dropped, unpriced or unreadable thing reaches the reader on ONE line. A count
// that exists only in --out JSON is a gap the text report claims not to have; the
// synthetic-call drop used to be exactly that.
function gapsLine() {
  const loud = [];
  if (SCAN.badLines) loud.push(`${SCAN.badLines} malformed lines`);
  if (SCAN.noTimestamp) loud.push(`${SCAN.noTimestamp} calls without a timestamp (dropped)`);
  if (SCAN.syntheticCalls) loud.push(`${SCAN.syntheticCalls} synthetic/zero-usage calls (dropped: the harness billed nothing for them)`);
  if (SCAN.unpricedCalls) loud.push(`${SCAN.unpricedCalls} UNPRICED calls ${JSON.stringify(SCAN.unpricedModels)} — tokens counted, dollars "n/a"; add the model to PRICING`);
  if (SCAN.tierUnknownCalls) loud.push(`${SCAN.tierUnknownCalls} cache writes with no 5m/1h split (priced as 5m)`);
  if (SCAN.dupFiles) loud.push(`${SCAN.dupFiles} duplicate files skipped`);
  // notes are bounded: a per-row note on a 200-agent flight must not become the report
  for (const n of SCAN.notes.slice(0, 4)) loud.push(n);
  if (SCAN.notes.length > 4) loud.push(`${SCAN.notes.length - 4} further notes (see --out JSON scan.notes)`);
  if (SCAN.readErrors.length) loud.push(`${SCAN.readErrors.length} READ ERRORS: ${SCAN.readErrors.slice(0, 3).join(" | ")}`);
  return loud.length ? "data gaps: " + loud.join(" · ") : "data gaps: none";
}
function walk(dir, out) {
  let ents; try { ents = fs.readdirSync(dir, { withFileTypes: true }); } catch (e) { SCAN.readErrors.push(`${dir}: ${e.message}`); return; }
  for (const e of ents) { const p = path.join(dir, e.name);
    if (e.isDirectory()) walk(p, out);
    else if (e.name.endsWith(".jsonl")) { let st; try { st = fs.statSync(p); } catch (er) { SCAN.readErrors.push(`${p}: ${er.message}`); continue; }
      if (st.mtimeMs >= SINCE) out.push(p); else SCAN.skippedOld++; } }
}
function* linesOf(file) {
  const fd = fs.openSync(file, "r"), buf = Buffer.allocUnsafe(1 << 23), dec = new StringDecoder("utf8"); let carry = "";
  try { for (;;) { const n = fs.readSync(fd, buf, 0, buf.length, null); if (!n) break;
      const parts = (carry + dec.write(buf.subarray(0, n))).split("\n"); carry = parts.pop(); for (const p of parts) if (p) yield p; }
    carry += dec.end(); if (carry) yield carry;
  } finally { fs.closeSync(fd); }
}
const contentChars = (c) => typeof c === "string" ? c.length : Array.isArray(c) ? c.reduce((a, b) => a + (b.type === "text" ? (b.text || "").length : b.type === "image" ? 6000 : JSON.stringify(b).length), 0) : c ? JSON.stringify(c).length : 0;
const foldCwd = (cwd) => (cwd || "").replace(/\/\.worktrees\/.*$/, "").replace(/\/+$/, "");
const isHome = (p) => /^(\/home\/[^/]+|\/Users\/[^/]+|\/root|\/)$/.test(p);

// ---------- global accumulators
const bump = (o, k, v) => { o[k] = (o[k] || 0) + v; };
const bump2 = (o, k, f, v) => { (o[k] ??= {})[f] = (o[k][f] || 0) + v; };
const G = { usd: { in: 0, out: 0, cw5: 0, cw1: 0, cr: 0 }, tok: { in: 0, out: 0, cw5: 0, cw1: 0, cr: 0, think: 0 }, calls: 0,
  cats: new Float64Array(CATS.length), bands: {}, modelEffort: {}, hourly: {}, rewrites: {}, rewriteTool: {}, small: {}, poll: {}, callIdx: {}, tier: {}, tools: {}, bash: {}, attach: {}, repeats: {}, rereads: {}, landings: [] };
const RUNS = [];

function auditFile(file) {
  const isSub = file.includes(`${path.sep}subagents${path.sep}`);
  const segsOf = file.split(path.sep), si = segsOf.lastIndexOf("subagents");
  const sid = isSub ? segsOf[si - 1] : path.basename(file, ".jsonl");
  let meta = {}; if (isSub) { const mp = file.replace(/\.jsonl$/, ".meta.json");
    try { meta = JSON.parse(fs.readFileSync(mp, "utf8")); } catch (e) { if (e.code !== "ENOENT") SCAN.readErrors.push(`${mp}: ${e.message}`); } }
  const seq = [], usage = new Map(), toolUses = new Map(), seenReads = new Map(), allCmd = new Map();
  let title = "", aiTitle = "", cwd0 = "", harnessUsd = null, compactPending = false, brief = null;
  const B = { tests: 0, testFails: 0, testCmd: {}, readsBeforeEdit: 0, firstEditCall: 0, edits: 0, editFiles: {} };
  for (const ln of linesOf(file)) {
    let o; try { o = JSON.parse(ln); } catch { SCAN.badLines++; continue; }
    const ts = o.timestamp ? Date.parse(o.timestamp) : NaN;
    if (!cwd0 && o.cwd) cwd0 = o.cwd;
    if (o.type === "custom-title") { title = o.customTitle || title; continue; }
    if (o.type === "agent-name") { title ||= o.agentName || ""; continue; }
    if (o.type === "ai-title") { aiTitle = o.aiTitle || aiTitle; continue; }
    if (o.type === "cost-state") { if (typeof o.totalCostUSD === "number") harnessUsd = o.totalCostUSD; continue; }
    if (o.type === "system" && o.subtype === "compact_boundary") { seq.push({ compact: true }); continue; }
    if (o.type === "assistant" && o.message?.usage) {
      const u = o.message.usage, mdl = o.message.model;
      if (mdl === "<synthetic>" || !((u.input_tokens || 0) + (u.output_tokens || 0) + (u.cache_read_input_tokens || 0) + (u.cache_creation_input_tokens || 0))) { SCAN.syntheticCalls++; continue; }
      if (Number.isNaN(ts)) { SCAN.noTimestamp++; continue; }
      const id = o.message.id + "|" + (o.requestId || "");
      if (!usage.has(id)) seq.push({ call: id });
      usage.set(id, { u, m: mdl, ts, effort: o.effort ?? o.perTurnEffort ?? "-" });
      for (const b of o.message.content || []) if (b.type === "tool_use") toolUses.set(b.id, { name: b.name, input: b.input || {}, ts });
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
        } else if (b.type === "text") seq.push({ cat: textCat, chars: (b.text || "").length, ts, trig: notif ? "agent report" : "user prompt" });
        else if (b.type === "image") seq.push({ cat: textCat, chars: 6000, ts, trig: "user prompt" });
      }
    } else if (o.type === "attachment" && o.attachment?.type !== "prompt_snapshot") {
      const chars = JSON.stringify(o.attachment).length; seq.push({ cat: C_HARNESS, chars, ts, att: o.attachment?.type || "?" });
    }
  }
  const project = foldCwd(cwd0);
  if (PROJECT && !project.includes(PROJECT)) return null;

  // ---------- replay: ordered segments; each call reads [0,cr) at 0.1x, writes [cr,cr+cw) at W, pays 1x for the rest
  const R = { file, kind: isSub ? "agent" : "main", sid, project, engine: "claude", agentId: isSub ? path.basename(file, ".jsonl").replace(/^agent-/, "") : sid,
    tok: { in: 0, out: 0, cw5: 0, cw1: 0, cr: 0 }, contractReads: 0, failedCmds: 0, unpriced: false,
    title: isSub ? (meta.description || "") : (title || aiTitle || ""), agentType: meta.agentType || "", depth: meta.spawnDepth || 0,
    calls: 0, usd: 0, usdBy: { in: 0, out: 0, cw5: 0, cw1: 0, cr: 0 }, models: {}, efforts: {}, t0: 0, t1: 0, activeMs: 0, toolWaitMs: 0, ctxFirst: 0, ctxPeak: 0, ctxSum: 0,
    bands: {}, rewrites: { n: 0, usd: 0, by: {}, nBy: {} }, resets: 0, tools: {}, bash: {}, errs: 0, rereadN: 0, rereadChars: 0, cats: new Float64Array(CATS.length), series: [], harnessUsd, topRepeat: null, brief, B, distinctRead: 0, land: new Float64Array(CATS.length), smallUsd: 0, smallN: 0, pollUsd: 0, pollN: 0, usdLC: 0, f: [0, 0, 0, 0, 0, 0], attach: {}, repeats: [], tl: new Float64Array(48), tlf: Array.from({ length: 5 }, () => new Float64Array(48)) };
  let segs = [], catTok = new Float64Array(CATS.length), have = 0, pending = [], prevOut = 0, first = true, prefixTok = 0, prev = null;
  const push = (c, t) => { if (t <= 0) return; const l = segs[segs.length - 1]; if (l && l.c === c) l.t += t; else segs.push({ c, t }); catTok[c] += t; have += t; R.land[c] += t; };
  const trim = (x) => { for (const ownOnly of [true, false]) for (let i = segs.length - 1; i >= 0 && x > 0; i--) { const s = segs[i]; if (ownOnly && s.c !== C_OWN) continue;
      const d = Math.min(s.t, x); s.t -= d; catTok[s.c] -= d; have -= d; x -= d; } segs = segs.filter((s) => s.t > 0); };
  const landed = [], cmdCount = new Map();
  for (const e of seq) {
    if (e.compact) { compactPending = true; continue; }
    if (!e.call) { pending.push(e);
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
    if (R.firstTs === undefined) R.firstTs = ts;
    const cc = u.cache_creation, cwAll = u.cache_creation_input_tokens || 0, cw1 = cc?.ephemeral_1h_input_tokens || 0, cw5 = Math.max(0, cwAll - cw1);
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
    const trigger = pending.length ? pending[pending.length - 1] : null, longest = pending.reduce((a, p) => (p.dur > (a?.dur || 0) ? p : a), null);
    const pendChars = pending.reduce((a, p) => a + p.chars, 0), onlyTools = pending.length > 0 && pending.every((p) => p.tool || p.att);
    pending = []; prevOut = out;
    if (ts < SINCE) { prev = { ctx, ts, m }; continue; }
    // An unpriced model is IGNORANCE, not a $0 spend: the call's tokens stay in every token
    // total and its run renders "n/a" in the $ column, exactly as the Codex side does.
    if (!r) { SCAN.unpricedCalls++; bump(SCAN.unpricedModels, m || "(none)", 1); R.unpriced = true;
      R.calls++; G.calls++; bump(R.models, m, 0); bump(R.efforts, effort, 0);
      R.tok.in += inp; R.tok.out += out; R.tok.cw5 += cw5; R.tok.cw1 += cw1; R.tok.cr += cr;
      G.tok.in += inp; G.tok.out += out; G.tok.cw5 += cw5; G.tok.cw1 += cw1; G.tok.cr += cr;
      if (!R.t0) { R.t0 = ts; R.ctxFirst = ctx; } R.t1 = ts; R.ctxPeak = Math.max(R.ctxPeak, ctx); R.ctxSum += ctx;
      prev = { ctx, ts, m }; continue; }
    if (cwAll && !cc) SCAN.tierUnknownCalls++;
    const ri = r.in / 1e6, rm = r.rd / r.in, W = cwAll ? (cw5 * 1.25 + cw1 * 2) / cwAll : 1.25;
    const parts = { in: inp * ri, out: out * r.out / 1e6, cw5: cw5 * 1.25 * ri, cw1: cw1 * 2 * ri, cr: cr * rm * ri }, usd = parts.in + parts.out + parts.cw5 + parts.cw1 + parts.cr;
    // attribute: charge everything as a cache read, then correct the tail beyond cr
    const read0 = R.cats[C_READ1] + R.cats[C_READN] + R.cats[C_BREAD], inj0 = R.cats[C_HARNESS];
    for (let k = 0; k < CATS.length; k++) if (catTok[k]) { const v = catTok[k] * rm * ri; R.cats[k] += v; G.cats[k] += v; }
    for (let i = segs.length - 1, pos = have; i >= 0 && pos > cr; i--) { const s = segs[i], a = pos - s.t, b = pos; pos = a;
      const rd = Math.max(0, Math.min(b, cr) - a), wr = Math.max(0, Math.min(b, cr + cwAll) - Math.max(a, cr)), un = Math.max(0, b - Math.max(a, cr + cwAll));
      const v = ((rd * rm + wr * W + un) - s.t * rm) * ri; R.cats[s.c] += v; G.cats[s.c] += v; }
    R.cats[C_OUT] += parts.out; G.cats[C_OUT] += parts.out;
    for (const k in parts) { R.usdBy[k] += parts[k]; G.usd[k] += parts[k]; }
    G.tok.in += inp; G.tok.out += out; G.tok.cw5 += cw5; G.tok.cw1 += cw1; G.tok.cr += cr; G.tok.think += u.output_tokens_details?.thinking_tokens || 0; G.calls++;
    R.tok.in += inp; R.tok.out += out; R.tok.cw5 += cw5; R.tok.cw1 += cw1; R.tok.cr += cr;
    R.calls++; R.usd += usd; R.usdLC += ctx > LONG_CTX_TOKENS ? (usd - parts.out) * r.lcIn + parts.out * r.lcOut : usd;
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

// ---------- --flight DIR: the ledger a flight leaves behind, parsed BEFORE the scan
// because it, not --since, sets the window. agents.tsv is the primary key — append-only,
// tab-separated, one row per spawn: task-id · agent-type · agent-id · round · spawn-time
// (ISO) · engine (claude|codex|seat); an optional header line is tolerated. With no
// agents.tsv, run.md's header instant and its "{id} CLAIMED · {agent} · {time}" lines are
// the fallback, and EVERY row it yields is marked matched=window: run.md carries no agent id.
const FLIGHT_PLAN = { rows: [], start: 0, baseline: "", source: "", gaps: [] };
if (FLIGHT) {
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
  SINCE = FLIGHT_PLAN.start - 10 * 60e3; // 10 minutes of slack: a transcript's first call precedes its ledger row
  HOURS = Math.max(1, (NOW - SINCE) / 3600e3);
}

// ---------- Codex CLI rollouts (~/.codex/sessions/**/rollout-*.jsonl)
// COUNTING RULE: token_count carries a CUMULATIVE total_token_usage that RESETS on resume
// and on compaction, and duplicate events re-emit an identical cumulative. So: dedupe on the
// cumulative, split a thread into segments wherever it drops, and sum each segment's PEAK.
// Reading only the final counter undercounts a resumed thread by orders of magnitude; summing
// per-turn deltas double-counts. Invariants held on every sampled event: total = input + output;
// cached_input ⊆ input; reasoning_output ⊆ output; cache_write = 0.
const CODEX_TOKEN_PAT = Buffer.from('"type":"token_count"'), CODEX_TURNCTX_PAT = Buffer.from('"type":"turn_context"');
const NEWLINE = 0x0a, SCAN_CHUNK = 4 << 20, SCAN_OVERLAP = 1 << 16, CODEX_META_BYTES = 1 << 19;
const codexRoot = () => CODEX_ROOT || path.join(os.homedir(), ".codex");

// Decode ONLY the lines matching a pattern: rollouts reach 110 MB of tool output, and decoding
// every line costs ~15x what decoding the matches costs for an identical result.
function scanCodexRollout(file, patterns, onLine) {
  let fd; try { fd = fs.openSync(file, "r"); } catch (e) { SCAN.readErrors.push(`${file}: ${e.message}`); return; }
  try { const size = fs.fstatSync(fd).size, buf = Buffer.allocUnsafe(SCAN_CHUNK + SCAN_OVERLAP), seen = new Set(); let pos = 0;
    while (pos < size) { const start = pos === 0 ? 0 : pos - SCAN_OVERLAP, want = Math.min(buf.length, size - start);
      const n = fs.readSync(fd, buf, 0, want, start); if (n <= 0) break; const view = buf.subarray(0, n);
      for (const pat of patterns) { let i = 0;
        while ((i = view.indexOf(pat, i)) !== -1) { const e = view.indexOf(NEWLINE, i);
          if (e === -1) break; // truncated at the window end; the overlap holds it whole next time
          let s = view.lastIndexOf(NEWLINE, i);
          if (s === -1) { if (start !== 0) { i += pat.length; continue; } s = 0; } else s += 1;
          const abs = start + s; if (e > s && !seen.has(abs)) { seen.add(abs); onLine(view.toString("utf8", s, e)); }
          i += pat.length; } }
      pos = start + n; if (n < want) break; }
  } catch (e) { SCAN.readErrors.push(`${file}: ${e.message}`); } finally { try { fs.closeSync(fd); } catch { /* already closed */ } }
}
// Line 1 is the session_meta payload. Unreadable → null, surfaced by the caller, never dropped.
function readCodexMeta(file) {
  let fd; try { fd = fs.openSync(file, "r"); } catch (e) { SCAN.readErrors.push(`${file}: ${e.message}`); return null; }
  try { const buf = Buffer.allocUnsafe(CODEX_META_BYTES), n = fs.readSync(fd, buf, 0, CODEX_META_BYTES, 0); if (n <= 0) return null;
    const view = buf.subarray(0, n), nl = view.indexOf(NEWLINE);
    const d = JSON.parse(view.toString("utf8", 0, nl === -1 ? n : nl));
    return d && d.type === "session_meta" ? d.payload || null : null;
  } catch (e) { SCAN.readErrors.push(`${file} session_meta: ${e.message}`); return null; } finally { try { fs.closeSync(fd); } catch { /* already closed */ } }
}
function findCodexRollouts(root) {
  const out = []; // sessions/ + archived_sessions/ only: the ~/.codex root also holds
  for (const base of [path.join(root, "sessions"), path.join(root, "archived_sessions")]) { // rollout-backup-* copies
    if (!fs.existsSync(base)) continue; const stack = [base]; // that would double-count a thread
    while (stack.length) { const d = stack.pop(); let ents;
      try { ents = fs.readdirSync(d, { withFileTypes: true }); } catch (e) { SCAN.readErrors.push(`${d}: ${e.message}`); continue; }
      for (const e of ents) { const full = path.join(d, e.name);
        if (e.isDirectory()) stack.push(full); else if (e.name.startsWith("rollout-") && e.name.endsWith(".jsonl")) out.push(full); } } }
  return out;
}
// rollout-<YYYY-MM-DD>T<HH-MM-SS>-<threadId>.jsonl — the stamp is LOCAL time, the payload's UTC.
const parseRolloutName = (file) => { const m = path.basename(file).match(/^rollout-(\d{4}-\d{2}-\d{2})T(\d{2})-(\d{2})-(\d{2})-(.+)\.jsonl$/);
  return m ? { at: Date.parse(`${m[1]}T${m[2]}:${m[3]}:${m[4]}`), id: m[5] } : null; };
const codexShort = (m) => String(m || "?").replace(/^gpt-/, "");
// session_meta.source is a dict on a sub-agent thread ({subagent: "<role>"}) and a bare string
// ("user") on a main one; older CLI builds put the same shape on thread_source. Read both, and
// never let an object reach the report as "[object Object]".
function codexRole(meta) { for (const v of [meta?.source, meta?.thread_source]) {
    if (typeof v === "string" && v) return v;
    if (v && typeof v === "object") { const k = v.subagent ?? Object.values(v)[0] ?? Object.keys(v)[0]; if (typeof k === "string" && k) return k; } }
  return ""; }
// Codex bills a different shape: cached_input is a SUBSET of input, output already includes
// reasoning, cache-write is always 0. null (→ "n/a") when unpriced, never an invented $0.
function codexCostUSD(model, a) { const p = RATE(model);
  if (!p) { SCAN.unpricedCalls++; bump(SCAN.unpricedModels, model || "(none)", 1); return null; }
  return Math.max(0, a.in - a.cached) * (p.in / 1e6) + a.cached * (p.rd / 1e6) + a.out * (p.out / 1e6); }
// The segment-peak fold, shared by the fast scan and the full per-agent audit.
function codexPeaks() { const peaks = []; let prev = null, seg = null;
  return { add(t) { if (prev !== null && t.total_tokens < prev) { if (seg) peaks.push(seg); seg = null; } prev = t.total_tokens;
      if (!seg || t.total_tokens > seg.total_tokens) seg = t; },
    done() { if (seg) peaks.push(seg); const a = { in: 0, cached: 0, out: 0, reasoning: 0, total: 0 };
      for (const p of peaks) { a.in += p.input_tokens || 0; a.cached += p.cached_input_tokens || 0; a.out += p.output_tokens || 0; a.reasoning += p.reasoning_output_tokens || 0; a.total += p.total_tokens || 0; }
      return { agg: a, segments: peaks.length }; } }; }

// Fast path for the --codex table: usage + models only, matched lines decoded.
function codexUsageFast(file) { const models = new Set(), pk = codexPeaks(); let events = 0, last = null;
  scanCodexRollout(file, [CODEX_TOKEN_PAT, CODEX_TURNCTX_PAT], (line) => {
    let d; try { d = JSON.parse(line); } catch { SCAN.badLines++; return; }
    if (d.type === "turn_context") { if (d.payload?.model) models.add(d.payload.model); return; }
    const p = d.payload; if (!p || p.type !== "token_count" || !p.info) return;
    const t = p.info.total_token_usage; if (!t || t.total_tokens === last) return;
    last = t.total_tokens; events++; pk.add(t); });
  return { ...pk.done(), models, events }; }

// Full path for --flight: one rollout → one RUN-shaped row, every measure a Claude row carries.
const RX_CX_READ = /(?:sed\s+-n\s+\S+|cat|nl(?:\s+-ba)?|head|tail|bat)\s+(?:-\w+\s+\S+\s+)*["']?([\w./@+-]+\.[\w]{1,6})["']?/g;
function codexAuditRun(file, meta) {
  const pk = codexPeaks(), models = new Set(), calls = new Map(), seenRead = new Map();
  const R = { file, kind: "agent", engine: "codex", sid: meta?.parent_thread_id || meta?.session_id || "", agentId: meta?.id || parseRolloutName(file)?.id || path.basename(file),
    project: foldCwd(meta?.cwd || ""), agentType: codexRole(meta) || (meta?.thread_source === "subagent" ? "subagent" : "main"),
    title: codexRole(meta) || "", calls: 0, t0: 0, t1: 0, ctxFirst: 0, ctxPeak: 0, ctxSum: 0, resets: 0,
    tok: { in: 0, out: 0, cw5: 0, cw1: 0, cr: 0 }, errs: 0, failedCmds: 0, pollN: 0, rereadN: 0, contractReads: 0, tools: {}, usd: 0, usdLC: 0, unpriced: false };
  let last = null, pendingPoll = false, malformed = 0;
  for (const ln of linesOf(file)) {
    let o; try { o = JSON.parse(ln); } catch { malformed++; continue; }
    const p = o.payload || {};
    if (o.type === "compacted" || p.type === "compacted") { R.resets++; continue; }
    if (o.type === "turn_context") { if (p.model) models.add(p.model); continue; }
    if (p.type === "custom_tool_call" || p.type === "function_call") {
      const input = String(p.input ?? p.arguments ?? ""), name = p.name || "?";
      calls.set(p.call_id, { name, input });
      const t = (R.tools[name] ??= { n: 0, err: 0 }); t.n++;
      // the cx-measured poll rule: a write_stdin that sends NOTHING, or an explicit wait/sleep
      if ((/write_stdin/.test(input) && /chars:\s*""/.test(input)) || name === "wait" || /\bsleep\s+\d/.test(input)) pendingPoll = true;
      for (const m of input.matchAll(RX_CX_READ)) { const f = m[1];
        seenRead.set(f, (seenRead.get(f) || 0) + 1); if (seenRead.get(f) > 1) R.rereadN++;
        if (/(?:CLAUDE|AGENTS)\.md$/.test(f)) R.contractReads++; }
      continue; }
    if (p.type === "custom_tool_call_output" || p.type === "function_call_output") {
      const c = calls.get(p.call_id), out = p.output;
      const text = typeof out === "string" ? out : Array.isArray(out) ? (out.find((b) => b?.text)?.text || "") : JSON.stringify(out || "");
      if (/^Script (failed|error)/m.test(text)) { R.failedCmds++; R.errs++; if (c) (R.tools[c.name] ??= { n: 0, err: 0 }).err++; }
      continue; }
    if (p.type === "token_count" && p.info) {
      const t = p.info.total_token_usage; if (!t || t.total_tokens === last) continue;
      last = t.total_tokens; pk.add(t);
      const ctx = p.info.last_token_usage?.input_tokens || 0, ts = Date.parse(o.timestamp || "");
      R.calls++; if (pendingPoll) { R.pollN++; pendingPoll = false; }
      if (!R.ctxFirst) R.ctxFirst = ctx; R.ctxPeak = Math.max(R.ctxPeak, ctx); R.ctxSum += ctx;
      if (!Number.isNaN(ts)) { if (!R.t0) R.t0 = ts; R.t1 = ts; } else SCAN.noTimestamp++;
      continue; } }
  if (malformed) SCAN.badLines += malformed;
  const { agg, segments } = pk.done();
  R.segments = segments; R.agg = agg;
  R.tok.in = Math.max(0, agg.in - agg.cached); R.tok.cr = agg.cached; R.tok.out = agg.out;
  R.models = [...models]; R.model = R.models[R.models.length - 1] || "unknown";
  if (models.size > 1) SCAN.notes.push(`${path.basename(file)} ran ${models.size} models (${R.models.join(", ")}) — priced entirely at "${R.model}"`);
  const usd = codexCostUSD(R.model, agg);
  if (usd === null) { R.unpriced = true; R.usd = 0; R.usdLC = 0; } else { R.usd = usd; const p = RATE(R.model); R.usdLC = R.ctxPeak > LONG_CTX_TOKENS ? usd * p.lcIn : usd; }
  return R;
}

// ---------- --codex: one bounded row per Codex thread
if (CODEX) {
  const root = codexRoot(), all = findCodexRollouts(root);
  if (!all.length) die(`no Codex rollouts under ${root}/sessions or ${root}/archived_sessions`);
  const rows = [], byId = new Set(); let old = 0, offProject = 0;
  for (const file of all) { let st; try { st = fs.statSync(file); } catch (e) { SCAN.readErrors.push(`${file}: ${e.message}`); continue; }
    if (st.mtimeMs < SINCE) { old++; continue; }
    const meta = readCodexMeta(file), cwd = meta?.cwd || "";
    if (PROJECT && !cwd.includes(PROJECT)) { offProject++; continue; }
    const u = codexUsageFast(file); if (!u.events) continue;
    const id = meta?.id || parseRolloutName(file)?.id || file; if (byId.has(id)) { SCAN.dupFiles++; continue; } byId.add(id);
    const model = [...u.models].pop() || "unknown";
    if (u.models.size > 1) SCAN.notes.push(`${path.basename(file)} ran ${u.models.size} models (${[...u.models].join(", ")}) — priced entirely at "${model}"`);
    rows.push({ id, at: Date.parse(meta?.timestamp || "") || parseRolloutName(file)?.at || st.mtimeMs, model, cwd, label: meta?.thread_source === "subagent" ? `subagent (${codexRole(meta) || "?"})` : codexRole(meta) || "main",
      agg: u.agg, segments: u.segments, cost: codexCostUSD(model, u.agg) }); }
  SCAN.files = rows.length; if (old) SCAN.notes.push(`${old} rollouts older than the window`); if (offProject) SCAN.notes.push(`${offProject} rollouts outside --project ${PROJECT}`);
  rows.sort((a, b) => (b.cost || 0) - (a.cost || 0) || b.agg.total - a.agg.total);
  const tot = { in: 0, cached: 0, out: 0, reasoning: 0, total: 0 }; let cost = 0, unpriced = 0;
  for (const r of rows) { for (const k of Object.keys(tot)) tot[k] += r.agg[k]; if (r.cost === null) unpriced++; else cost += r.cost; }
  const n = (v) => v.toLocaleString("en-US"), cash = (v) => (v === null ? "n/a" : "$" + v.toFixed(2));
  console.log(`TOKEN AUDIT · codex · last ${HOURS}h · ${root} · ${rows.length} threads of ${all.length} rollouts`);
  console.log(gapsLine());
  console.log(`\n${"STARTED".padEnd(17)}${"THREAD".padEnd(14)}${"MODEL".padEnd(13)}${"LABEL".padEnd(22)}${"IN".padStart(12)}${"CACHED".padStart(12)}${"OUT".padStart(11)}${"EST USD".padStart(10)}`);
  for (const r of rows.slice(0, TOP)) console.log(`${new Date(r.at).toISOString().slice(0, 16).replace("T", " ").padEnd(17)}${String(r.id).slice(-12).padEnd(14)}${codexShort(r.model).slice(0, 12).padEnd(13)}${r.label.slice(0, 21).padEnd(22)}${n(r.agg.in).padStart(12)}${n(r.agg.cached).padStart(12)}${n(r.agg.out).padStart(11)}${cash(r.cost).padStart(10)}`);
  if (rows.length > TOP) console.log(`  … ${rows.length - TOP} further threads folded into the total below`);
  console.log(`\nTOTAL ${n(tot.total)} tok · uncached in ${n(tot.in - tot.cached)} · cached in ${n(tot.cached)} · out ${n(tot.out)} (incl. ${n(tot.reasoning)} reasoning) · ${cash(cost)}`);
  const resumed = rows.filter((r) => r.segments > 1).length;
  if (resumed) console.log(`${resumed} thread(s) resumed or compacted mid-run; each segment's peak is summed — the final counter alone would undercount them.`);
  if (unpriced) console.log(`${unpriced} thread(s) ran an unpriced model and show "n/a" — their TOKENS are in the total, their DOLLARS are not.`);
  process.exit(0);
}

// ---------- run
SCAN.roots = roots(); const files = []; for (const r of SCAN.roots) walk(r, files);
const seenKey = new Set();
for (const f of files) { const key = f.split(path.sep).slice(-3).join("/"); if (seenKey.has(key)) { SCAN.dupFiles++; continue; } seenKey.add(key);
  SCAN.files++; let R; try { R = auditFile(f); } catch (e) { SCAN.readErrors.push(`${f}: ${e.stack || e.message}`); continue; } if (R) RUNS.push(R); }

// --session: the selector a sub-agent-orchestrated run HAS. A chat title exists only on a
// main chat, so --family alone cannot select a family whose work an agent orchestrated.
if (SESSION) { const kept = RUNS.filter((r) => r.sid.startsWith(SESSION)); const dropped = RUNS.length - kept.length;
  if (!kept.length) die(`--session ${SESSION} matched none of the ${RUNS.length} runs in this window; pass a session-id prefix as it appears in the transcript path`);
  SCAN.notes.push(`--session ${SESSION}: ${dropped} runs outside that session excluded`); RUNS.length = 0; RUNS.push(...kept); }

// fold sub-folder chats onto the shortest enclosing project root (never onto a bare home dir)
const projRoots = [...new Set(RUNS.map((r) => r.project))].filter((p) => p && !isHome(p)).sort((a, b) => a.length - b.length);
for (const r of RUNS) { const root = projRoots.find((p) => r.project === p || r.project.startsWith(p + "/")); if (root) r.project = root; }

// ---------- aggregate
const total = Object.values(G.usd).reduce((a, b) => a + b, 0);
const q = (arr, p) => { if (!arr.length) return 0; const s = [...arr].sort((a, b) => a - b); return s[Math.min(s.length - 1, Math.floor(p * s.length))]; };
const wallMin = (r) => (r.t1 - r.t0) / 60e3;
const byProject = {}; for (const r of RUNS) { const p = (byProject[r.project] ??= { usd: 0, main: 0, agent: 0, runs: 0, rewrites: 0, cats: new Float64Array(CATS.length) }); p.usd += r.usd; p[r.kind] += r.usd; p.runs++; p.rewrites += r.rewrites.usd; r.cats.forEach((v, i) => (p.cats[i] += v)); }
const families = {}; for (const r of RUNS) { const f = (families[r.sid] ??= { sid: r.sid, title: "", project: r.project, own: 0, agents: 0, nAgents: 0, rewrites: 0, calls: 0, peakK: 0, t0: Infinity, t1: 0, agentRuns: [] });
  if (r.kind === "main") { f.title = r.title; f.own += r.usd; f.peakK = Math.round(r.ctxPeak / 1000); f.project = r.project; } else { f.agents += r.usd; f.nAgents++; f.agentRuns.push(r); }
  f.rewrites += r.rewrites.usd; f.calls += r.calls; f.t0 = Math.min(f.t0, r.t0); f.t1 = Math.max(f.t1, r.t1); }
const famList = Object.values(families).sort((a, b) => b.own + b.agents - (a.own + a.agents));
const groups = {}; for (const r of RUNS) if (r.kind === "agent") { const k = `${path.basename(r.project)} · ${r.agentType || "(untyped)"} · ${shortModel(r.model)}`; (groups[k] ??= []).push(r); }
const groupRows = Object.entries(groups).map(([k, rs]) => ({ k, n: rs.length, usd: rs.reduce((a, r) => a + r.usd, 0), med: q(rs.map((r) => r.usd), 0.5), p90: q(rs.map((r) => r.usd), 0.9), max: Math.max(...rs.map((r) => r.usd)),
  calls: q(rs.map((r) => r.calls), 0.5), peakK: q(rs.map((r) => r.ctxPeak / 1000), 0.5), wall: q(rs.map(wallMin), 0.5), errs: rs.reduce((a, r) => a + r.errs, 0) / rs.length,
  poll: rs.reduce((a, r) => a + r.pollUsd, 0), small: rs.reduce((a, r) => a + r.smallUsd, 0), tests: rs.reduce((a, r) => a + (r.bash[CATS[C_BTEST]]?.n || 0), 0) / rs.length, rereads: rs.reduce((a, r) => a + r.rereadN, 0) / rs.length })).sort((a, b) => b.usd - a.usd);

const $ = (v) => (v >= 100 ? "$" + v.toFixed(0) : "$" + v.toFixed(2)), pct = (v, t = total) => (t ? (100 * v / t).toFixed(1) : "0.0") + "%";

// ---------- --flight: one row per agent the flight spawned, bounded whatever its size
if (FLIGHT) {
  const dir = path.resolve(FLIGHT);
  const MAX_ROWS = 60, MAX_UNMATCHED = 15, WINDOW_BEFORE = 10 * 60e3, WINDOW_AFTER = 180 * 60e3;
  const norm = (s) => String(s || "").toLowerCase().replace(/[^a-z0-9]+/g, " ").trim();
  const roleMatch = (a, b) => { const x = norm(a), y = norm(b); if (!x || !y) return false;
    return x.includes(y) || y.includes(x) || x.split(" ").pop() === y.split(" ").pop(); };
  const capFor = (type) => (/gater/i.test(type) ? 150 : 80); // executor 80, gater 150, from the type name
  const claudeAgents = RUNS.filter((r) => r.kind === "agent");
  const byAgentId = new Map(); for (const r of claudeAgents) byAgentId.set(r.agentId, r);

  // Codex candidates: meta only. A full audit costs a whole-file decode, so it runs on matches.
  const cxRoot = codexRoot(), cxCand = [];
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
  // A transcript inside the window with no ledger row is a hole in the ledger, never a drop.
  const parents = new Set(rows.map((x) => x.run.sid).filter(Boolean));
  for (const r of claudeAgents) if (!takenClaude.has(r.agentId) && parents.has(r.sid) && r.t0 >= SINCE) unmatched.push(`TRANSCRIPT ${r.agentType || "agent"} · claude · ${r.agentId.slice(0, 12)} · ${new Date(r.t0).toISOString().slice(0, 16)} · ran under this flight's session with no ledger row`);
  for (const c of cxCand) if (!takenCodex.has(c.file) && parents.has(String(c.meta?.parent_thread_id || c.meta?.session_id || "")) ) unmatched.push(`TRANSCRIPT ${c.role || "codex"} · codex · ${String(c.ids[0]).slice(-12)} · ${new Date(c.at).toISOString().slice(0, 16)} · ran under this flight's thread with no ledger row`);

  const K = (v) => (v >= 1e6 ? (v / 1e6).toFixed(1) + "M" : v >= 1000 ? Math.round(v / 1000) + "K" : String(Math.round(v)));
  const cash = (r) => (r.unpriced ? "n/a" : "$" + r.usd.toFixed(2));
  const mins = (r) => (r.t1 && r.t0 ? Math.round((r.t1 - r.t0) / 60e3) + "m" : "?");
  const growth = (r) => (r.calls ? K(Math.max(0, r.ctxPeak - r.ctxFirst) / r.calls) : "?");
  const out = [];
  const startISO = new Date(FLIGHT_PLAN.start).toISOString(), endISO = new Date(Math.max(FLIGHT_PLAN.start, ...rows.map((x) => x.run.t1 || 0))).toISOString();
  out.push(`# ${path.basename(dir)} · metrics`, "",
    `source ${FLIGHT_PLAN.source}${FLIGHT_PLAN.baseline ? ` · baseline ${FLIGHT_PLAN.baseline}` : ""} · window ${startISO} → ${endISO}`,
    `${rows.length} matched of ${FLIGHT_PLAN.rows.length} ledger rows · claude ${rows.filter((x) => x.run.engine === "claude").length} · codex ${rows.filter((x) => x.run.engine === "codex").length} · by id ${rows.filter((x) => x.how === "id").length} · by agent path ${rows.filter((x) => x.how === "path").length} · by window ${rows.filter((x) => x.how === "window").length}`, "");
  out.push("| task | agent | engine | model | calls | wall | ctx0 | peak | +/call | input | cached | output | $ | fail | poll | reread | contract | compact | cap | matched |",
    "|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|");
  const sorted = [...rows].sort((a, b) => b.run.usd - a.run.usd);
  for (const { led, run, how } of sorted.slice(0, MAX_ROWS)) {
    const cap = capFor(led.agentType || run.agentType);
    out.push(`| ${led.taskId} | ${led.agentType || run.agentType || "?"} | ${run.engine} | ${shortModel(codexShort(run.model))} | ${run.calls} | ${mins(run)} | ${K(run.ctxFirst)} | ${K(run.ctxPeak)} | ${growth(run)} | ${K(run.tok.in + run.tok.cw5 + run.tok.cw1)} | ${K(run.tok.cr)} | ${K(run.tok.out)} | ${cash(run)} | ${run.failedCmds} | ${run.pollN} | ${run.rereadN} | ${run.contractReads} | ${run.resets} | ${run.calls > cap ? `OVER ${cap}` : `ok/${cap}`} | ${how} |`);
  }
  if (sorted.length > MAX_ROWS) out.push(`| … | ${sorted.length - MAX_ROWS} further agents folded into the totals below | | | | | | | | | | | | | | | | | | |`);
  const fold = (rs) => rs.reduce((a, x) => ({ n: a.n + 1, calls: a.calls + x.run.calls, usd: a.usd + x.run.usd, unpriced: a.unpriced + (x.run.unpriced ? 1 : 0),
    in: a.in + x.run.tok.in + x.run.tok.cw5 + x.run.tok.cw1, cr: a.cr + x.run.tok.cr, out: a.out + x.run.tok.out, fail: a.fail + x.run.failedCmds, poll: a.poll + x.run.pollN,
    reread: a.reread + x.run.rereadN, contract: a.contract + x.run.contractReads, compact: a.compact + x.run.resets, over: a.over + (x.run.calls > capFor(x.led.agentType || x.run.agentType) ? 1 : 0),
    peak: Math.max(a.peak, x.run.ctxPeak), lc: a.lc + (x.run.usdLC || x.run.usd) }), { n: 0, calls: 0, usd: 0, unpriced: 0, in: 0, cr: 0, out: 0, fail: 0, poll: 0, reread: 0, contract: 0, compact: 0, over: 0, peak: 0, lc: 0 });
  const byType = {}; for (const x of rows) (byType[x.led.agentType || x.run.agentType || "(untyped)"] ??= []).push(x);
  out.push("", "## totals by agent type", "", "| agent type | n | calls | input | cached | output | $ | fail | poll | reread | contract | compact | over cap |", "|---|---|---|---|---|---|---|---|---|---|---|---|---|");
  for (const [k, rs] of Object.entries(byType).sort((a, b) => fold(b[1]).usd - fold(a[1]).usd).slice(0, 12)) { const f = fold(rs);
    out.push(`| ${k} | ${f.n} | ${f.calls} | ${K(f.in)} | ${K(f.cr)} | ${K(f.out)} | ${f.unpriced ? `$${f.usd.toFixed(2)} + ${f.unpriced} n/a` : "$" + f.usd.toFixed(2)} | ${f.fail} | ${f.poll} | ${f.reread} | ${f.contract} | ${f.compact} | ${f.over} |`); }
  const T = fold(rows);
  out.push("", "## flight total", "",
    `${T.n} agents · ${T.calls} calls · peak context ${K(T.peak)} · input ${K(T.in)} · cached ${K(T.cr)} · output ${K(T.out)} · **$${T.usd.toFixed(2)}**${T.unpriced ? ` + ${T.unpriced} unpriced agent(s) at "n/a" (their tokens ARE above, their dollars are NOT)` : ""}`,
    `failed commands ${T.fail} · poll calls ${T.poll} · re-reads ${T.reread} · contract-file reads ${T.contract} · compactions ${T.compact} · over cap ${T.over} of ${T.n}`, "",
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
    `cross-check: at the long-context premium (>${K(LONG_CTX_TOKENS)} context, per-model rate in PRICING — an estimate) this flight reads $${T.lc.toFixed(2)} (${T.usd ? (T.lc / T.usd).toFixed(2) : "n/a"}x the headline). ` +
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
        overCap: run.calls > capFor(led.agentType || run.agentType) })), unmatched }, null, 1));
    console.log(`full data → ${OUT}`); }
  // UNMATCHED is a reported condition, not a failure. A read error is: it means we failed
  // to LOOK, and a report built over a root we could not read must not read as success.
  process.exit(SCAN.readErrors.length ? 1 : 0);
}

// ---------- report
const Mt = (v) => (v / 1e6).toFixed(1) + "M", pad = (s, n) => String(s).padStart(n), cut = (s, n) => (s.length > n ? s.slice(0, n - 1) + "…" : s).padEnd(n);
const H = (t) => console.log("\n== " + t), L = (s) => console.log(s);
const base = (p) => path.basename(p) || p;
L(`TOKEN AUDIT · last ${HOURS}h · host ${os.hostname()} · ${new Date(NOW).toISOString()}`);
L(`scanned ${SCAN.roots.join(", ")} · ${SCAN.files} transcripts (${RUNS.filter((r) => r.kind === "main").length} main, ${RUNS.filter((r) => r.kind === "agent").length} agent runs with priced calls) · ${G.calls} API calls`);
L(gapsLine());

H(`1 · TOTAL ${$(total)} by billing class`);
for (const [k, n] of [["cr", "re-reading cached context (0.1x)"], ["cw5", "writing context to the 5-minute cache (1.25x)"], ["cw1", "writing context to the 1-hour cache (2x)"], ["out", "output tokens"], ["in", "uncached input (1x)"]])
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
L(`  ${pad("total", 9)} ${pad("n", 4)} ${pad("median", 8)} ${pad("p90", 8)} ${pad("max", 8)} ${pad("calls", 6)} ${pad("peakK", 6)} ${pad("wall m", 7)} ${pad("errs", 5)} ${pad("tests", 6)} ${pad("reread", 7)} ${pad("poll$", 6)} ${pad("small$", 6)}  group (calls/peak/wall = median, errs/tests/reread = per run, poll/small = share of group $)`);
for (const g of groupRows.slice(0, TOP + 6)) L(`  ${pad($(g.usd), 9)} ${pad(g.n, 4)} ${pad($(g.med), 8)} ${pad($(g.p90), 8)} ${pad($(g.max), 8)} ${pad(g.calls, 6)} ${pad(Math.round(g.peakK), 6)} ${pad(Math.round(g.wall), 7)} ${pad(g.errs.toFixed(1), 5)} ${pad(g.tests.toFixed(1), 6)} ${pad(g.rereads.toFixed(1), 7)} ${pad(pct(g.poll, g.usd), 6)} ${pad(pct(g.small, g.usd), 6)}  ${g.k}`);

H(`9 · TOP ${TOP + 3} SINGLE RUNS`);
const topRuns = [...RUNS].sort((a, b) => b.usd - a.usd);
const USD = (r) => (r.unpriced ? "n/a" : $(r.usd)); // never render ignorance as zero dollars
for (const r of topRuns.slice(0, TOP + 3)) L(`  ${pad(USD(r), 9)}  ${cut(r.kind === "main" ? "MAIN " + (r.title || r.sid.slice(0, 8)) : `${r.agentType || "agent"}: ${r.title}`, 44)} ${cut(shortModel(r.model), 12)} ${pad(r.calls, 5)} calls · ctx ${pad(Math.round(r.ctxFirst / 1000), 3)}→${pad(Math.round(r.ctxPeak / 1000), 3)}K · ${pad(Math.round(wallMin(r)), 4)}m wall/${pad(Math.round(r.toolWaitMs / 60e3), 4)}m in tools · rw ${pad($(r.rewrites.usd), 6)} · errs ${pad(r.errs, 3)} · reread ${pad(r.rereadN, 3)} · poll ${pad($(r.pollUsd), 6)}${r.topRepeat ? ` · ${r.topRepeat.n}× "${r.topRepeat.cmd.slice(0, 40)}"` : ""}`);
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
  L(`  if calls over 200K context were billed at the long-context premium (2x in, 1.5x out): ${$(lc)} (${(lc / theirs).toFixed(2)}x)`);
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
  const view = { v: 2, host: opt("--host-label", os.hostname()), at: NOW, hours: HOURS, tl0: SINCE, stepMin: HOURS * 60 / 48, total: r2(total), projects: [], hidden: [] };
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
if (SCAN.readErrors.length) { console.error(`token-audit: ${SCAN.readErrors.length} read error(s); this report is INCOMPLETE — ${SCAN.readErrors.slice(0, 5).join(" | ")}`); process.exit(1); }
