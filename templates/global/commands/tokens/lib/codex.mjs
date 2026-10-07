// codex.mjs — Codex CLI rollouts: the fast usage scan, the per-agent audit, and the --codex table.
import fs from "node:fs";
import path from "node:path";
import os from "node:os";
import { die } from "./options.mjs";
import { SCAN, gapsLine, linesOf, bump, foldCwd } from "./scan.mjs";
import { LONG_CTX_TOKENS, RATE } from "./pricing.mjs";

// ---------- Codex CLI rollouts (~/.codex/sessions/**/rollout-*.jsonl)
// COUNTING RULE: token_count carries a CUMULATIVE total_token_usage that RESETS on resume
// and on compaction, and duplicate events re-emit an identical cumulative. So: dedupe on the
// cumulative, split a thread into segments wherever it drops, and sum each segment's PEAK.
// Reading only the final counter undercounts a resumed thread by orders of magnitude; summing
// per-turn deltas double-counts. Invariants held on every sampled event: total = input + output;
// cached_input ⊆ input; reasoning_output ⊆ output; cache_write = 0.
const CODEX_TOKEN_PAT = Buffer.from('"type":"token_count"'), CODEX_TURNCTX_PAT = Buffer.from('"type":"turn_context"');
const NEWLINE = 0x0a, SCAN_CHUNK = 4 << 20, SCAN_OVERLAP = 1 << 16, CODEX_META_BYTES = 1 << 19;
export const codexRoot = (opts) => opts.codexRoot || path.join(os.homedir(), ".codex");

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
export function readCodexMeta(file) {
  let fd; try { fd = fs.openSync(file, "r"); } catch (e) { SCAN.readErrors.push(`${file}: ${e.message}`); return null; }
  try { const buf = Buffer.allocUnsafe(CODEX_META_BYTES), n = fs.readSync(fd, buf, 0, CODEX_META_BYTES, 0); if (n <= 0) return null;
    const view = buf.subarray(0, n), nl = view.indexOf(NEWLINE);
    const d = JSON.parse(view.toString("utf8", 0, nl === -1 ? n : nl));
    return d && d.type === "session_meta" ? d.payload || null : null;
  } catch (e) { SCAN.readErrors.push(`${file} session_meta: ${e.message}`); return null; } finally { try { fs.closeSync(fd); } catch { /* already closed */ } }
}
export function findCodexRollouts(root) {
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
export const parseRolloutName = (file) => { const m = path.basename(file).match(/^rollout-(\d{4}-\d{2}-\d{2})T(\d{2})-(\d{2})-(\d{2})-(.+)\.jsonl$/);
  return m ? { at: Date.parse(`${m[1]}T${m[2]}:${m[3]}:${m[4]}`), id: m[5] } : null; };
export const codexShort = (m) => String(m || "?").replace(/^gpt-/, "");
// session_meta.source is a dict on a sub-agent thread ({subagent: "<role>"}) and a bare string
// ("user") on a main one; older CLI builds put the same shape on thread_source. Read both, and
// never let an object reach the report as "[object Object]".
export function codexRole(meta) { for (const v of [meta?.source, meta?.thread_source]) {
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
export function codexAuditRun(file, meta) {
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
export function runCodex(opts) {
  const { since: SINCE, hours: HOURS, project: PROJECT, top: TOP } = opts;
  const root = codexRoot(opts), all = findCodexRollouts(root);
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
  return 0;
}
