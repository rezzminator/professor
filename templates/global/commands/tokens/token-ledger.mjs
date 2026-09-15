#!/usr/bin/env node
// token-ledger — per-agent / per-operation token attribution for Claude Code sessions,
// and per-session attribution for Codex CLI sessions (--codex).
// Zero dependencies (node: builtins only). READ-ONLY over transcripts. No network.
//
// Truth source (Claude Code): sub-agent transcript JSONL written at
//   {root}/projects/{projectSlug}/{conversationId}/subagents/agent-{agentId}.jsonl
//   ...and nested workflow runs under .../subagents/workflows/wf_*/agent-*.jsonl
// The main loop is {root}/projects/{projectSlug}/{conversationId}.jsonl (sibling of the dir).
// Each assistant line carries message.usage + message.model; streaming writes multiple
// lines per API call, so we dedup before summing (see DEDUP note below).
//
// Truth source (Codex CLI): session rollouts under ~/.codex/ — see § CODEX MODE.

import fs from "node:fs";
import path from "node:path";
import os from "node:os";
import readline from "node:readline";

// ─── EDITABLE PRICING (USD per 1M tokens) ──────────────────────────────────────
// Match by substring on the model id (lowercased); first match wins, so keep more
// specific ids above broader ones. Update these as prices change. Unknown model →
// cost reported as "n/a" (never invented) + a warning, never a crash.
const PRICING = [
  // [substring, inputPerMTok, outputPerMTok, cachedInputPerMTok?]
  //
  // Claude models. Checked in order, most-specific first — the first substring match on the
  // lowercased model id wins, so "opus-4-1" must precede the "opus" catch-all. Omitting the
  // 4th column derives the cached rate at CACHE_READ_MULT.
  ["opus-4-1", 15.0, 75.0], // deprecated Opus 4.1/4.0-era pricing tier
  // Opus 4.0's id carries NO minor digit — it is claude-opus-4-<date> — so a
  // literal "opus-4-0" matches nothing and silently falls through to the 5/25
  // catch-all below at a third of the real rate. Match the date instead; every
  // published 4.0 build is claude-opus-4-20{yy}{mm}{dd}, and no current-tier id
  // (opus-4-5/4-6/4-7/4-8, opus-5) contains "opus-4-20".
  ["opus-4-20", 15.0, 75.0],
  ["opus", 5.0, 25.0], // current tier: opus-5, opus-4-8, opus-4-7, opus-4-6, opus-4-5
  ["sonnet-4", 3.0, 15.0], // sonnet-4-6, sonnet-4-5, sonnet-4-0
  ["sonnet-5", 2.0, 10.0], // permanent rate; the planned 2026-09-01 step to 3/15 was cancelled
  ["sonnet", 3.0, 15.0], // older sonnet catch-all (3.7 etc.)
  ["haiku-4-5", 1.0, 5.0],
  ["haiku", 0.8, 4.0], // haiku 3.5/3 catch-all (retired, kept for historical transcripts)
  ["fable", 10.0, 50.0],
  ["mythos", 10.0, 50.0],
  //
  // Codex CLI models. 4th column = the cached-input rate, billed separately because
  // Codex reports cached_input_tokens as a SUBSET of input_tokens (see § CODEX MODE).
  // Published standard-tier rates, developers.openai.com/api/docs/pricing (read
  // 2026-09-07): Astra in/cached/out 10/1/50; Sol 4/0.40/20 (promotional through
  // 2026-11-21); Luna 0.20/0.02/1.20. Reasoning bills as output. Fast mode (2x) and
  // Batch/Flex (0.5x) are not modelled; the >272K long-context overage never applies
  // while the Codex context window stays under that threshold.
  ["gpt-6-astra", 10.0, 50.0, 1.0],
  ["gpt-5.6-sol", 4.0, 20.0, 0.4],
  ["gpt-5.6-luna", 0.2, 1.2, 0.02],
];
const CACHE_WRITE_MULT = 1.25;
const CACHE_READ_MULT = 0.1;

function priceFor(model) {
  const id = String(model || "").toLowerCase();
  const row = PRICING.find(([sub]) => id.includes(sub));
  if (!row) return { in: 0, out: 0, cached: 0, found: false };
  return {
    in: row[1],
    out: row[2],
    cached: row[3] !== undefined ? row[3] : row[1] * CACHE_READ_MULT,
    found: true,
  };
}

const UNKNOWN_MODELS = new Set();
function noteUnknownModel(model) {
  if (model && !UNKNOWN_MODELS.has(model)) {
    UNKNOWN_MODELS.add(model);
    warn(`unknown model "${model}" — add it to PRICING`);
  }
}

function costUSD(model, u) {
  const p = priceFor(model);
  if (!p.found) noteUnknownModel(model);
  const inRate = p.in / 1e6;
  return (
    u.in * inRate +
    u.out * (p.out / 1e6) +
    u.cw * inRate * CACHE_WRITE_MULT +
    u.cr * inRate * CACHE_READ_MULT
  );
}

// Codex bills a different shape: cached_input is a SUBSET of input (charged at the
// cached rate), output already includes reasoning, and cache-write is always 0.
// Returns null — rendered "n/a" — when the model has no PRICING row, so an unpriced
// model reads as "we don't know", never as a $0 spend.
function codexCostUSD(model, u) {
  const p = priceFor(model);
  if (!p.found) {
    noteUnknownModel(model);
    return null;
  }
  const uncachedIn = Math.max(0, u.in - u.cached);
  return uncachedIn * (p.in / 1e6) + u.cached * (p.cached / 1e6) + u.out * (p.out / 1e6);
}

// ─── ARG PARSING ───────────────────────────────────────────────────────────────
function parseArgs(argv) {
  const a = {
    roots: [],
    session: null,
    all: false,
    project: null,
    detail: null,
    json: false,
    byWorkflow: false,
    filter: null,
    codex: false,
    codexRoot: null,
    since: null,
    byDay: false,
    top: null,
  };
  for (let i = 0; i < argv.length; i++) {
    const v = argv[i];
    if (v === "--all") a.all = true;
    else if (v === "--json") a.json = true;
    else if (v === "--session") a.session = argv[++i];
    else if (v === "--root") a.roots.push(argv[++i]);
    else if (v === "--project") a.project = argv[++i];
    else if (v === "--detail") a.detail = argv[++i];
    else if (v === "--by-workflow") a.byWorkflow = true;
    else if (v === "--filter") a.filter = argv[++i];
    else if (v === "--codex") a.codex = true;
    else if (v === "--codex-root") a.codexRoot = argv[++i];
    else if (v === "--since") a.since = argv[++i];
    else if (v === "--by-day") a.byDay = true;
    else if (v === "--top") a.top = Number(argv[++i]);
    else if (v === "-h" || v === "--help") a.help = true;
    else warn(`unknown arg ignored: ${v}`);
  }
  if (a.since && !/^\d{4}-\d{2}-\d{2}$/.test(a.since)) {
    console.error(`--since expects YYYY-MM-DD, got "${a.since}"`);
    process.exit(1);
  }
  if (a.top !== null && (!Number.isFinite(a.top) || a.top < 0)) {
    console.error(`--top expects a non-negative integer, got "${a.top}"`);
    process.exit(1);
  }
  return a;
}

const WARNINGS = [];
function warn(m) {
  WARNINGS.push(m);
}

// ─── DISCOVERY ──────────────────────────────────────────────────────────────────
// Claude Code slugifies the cwd by replacing every "/" with "-" → the project dir name.
function projectSlug(cwd) {
  return cwd.replace(/\//g, "-");
}

function defaultRoots() {
  const home = os.homedir();
  return [
    path.join(home, ".claude"),
    path.join(home, ".claude-sessions"),
  ].filter((p) => fs.existsSync(p));
}

// A "project dir" is any .../projects/{slug} directory across all roots (including the
// multi-account ~/.claude-sessions/sNNN/projects/{slug} layout — we glob for it).
function findProjectDirs(roots, slug) {
  const dirs = [];
  for (const root of roots) {
    // root may itself be a projects/ parent, OR a sessions parent holding many sNNN/projects.
    const candidates = [path.join(root, "projects", slug)];
    const sessionsBase = root; // e.g. ~/.claude-sessions
    if (fs.existsSync(sessionsBase) && fs.statSync(sessionsBase).isDirectory()) {
      for (const ent of safeReaddir(sessionsBase)) {
        candidates.push(path.join(sessionsBase, ent, "projects", slug));
      }
    }
    for (const c of candidates) {
      if (fs.existsSync(c) && fs.statSync(c).isDirectory()) dirs.push(c);
    }
  }
  return [...new Set(dirs)];
}

function safeReaddir(p) {
  try {
    return fs.readdirSync(p);
  } catch {
    return [];
  }
}

// A "session" here = one main-conversation JSONL + its sibling {id}/subagents tree.
// Returns {conversationId, mainFile, subagentsDir, mtime} per conversation found.
function listSessions(projectDirs) {
  const sessions = [];
  for (const dir of projectDirs) {
    for (const ent of safeReaddir(dir)) {
      if (!ent.endsWith(".jsonl")) continue;
      const id = ent.slice(0, -6);
      const mainFile = path.join(dir, ent);
      const subDir = path.join(dir, id, "subagents");
      let mtime = 0;
      try {
        mtime = fs.statSync(mainFile).mtimeMs;
      } catch {}
      sessions.push({
        conversationId: id,
        mainFile,
        subagentsDir: fs.existsSync(subDir) ? subDir : null,
        mtime,
      });
    }
  }
  return sessions;
}

// All agent-*.jsonl under a subagents dir, including nested workflows/wf_*/.
function findAgentFiles(subagentsDir) {
  const out = [];
  if (!subagentsDir) return out;
  const stack = [subagentsDir];
  while (stack.length) {
    const d = stack.pop();
    for (const ent of safeReaddir(d)) {
      const full = path.join(d, ent);
      let st;
      try {
        st = fs.statSync(full);
      } catch {
        continue;
      }
      if (st.isDirectory()) stack.push(full);
      else if (ent.startsWith("agent-") && ent.endsWith(".jsonl")) out.push(full);
    }
  }
  return out;
}

// ─── PARSING ────────────────────────────────────────────────────────────────────
// DEDUP: streaming writes multiple assistant lines per API call sharing the same
// message.id. We key on (message.id, requestId) and keep the LAST occurrence — the
// final line carries the complete cumulative usage for that call. Verified on real
// files: 43 assistant lines collapsed to 18 distinct calls. Summing raw lines would
// overcount 2-3x. (requestId rarely splits a message.id but is kept for safety.)
async function parseTranscript(file) {
  const calls = new Map(); // key -> { usage, model, ts, hint }
  let malformed = 0;
  let stream;
  try {
    stream = fs.createReadStream(file, { encoding: "utf8" });
  } catch (e) {
    warn(`cannot open ${file}: ${e.message}`);
    return { calls, malformed, meta: null };
  }
  const rl = readline.createInterface({ input: stream, crlfDelay: Infinity });
  for await (const line of rl) {
    if (!line.trim()) continue;
    let d;
    try {
      d = JSON.parse(line);
    } catch {
      malformed++;
      continue;
    }
    if (d.type !== "assistant") continue;
    const m = d.message || {};
    const u = m.usage;
    if (!u) continue;
    const key = `${m.id || ""}|${d.requestId || ""}`;
    calls.set(key, {
      usage: {
        in: u.input_tokens || 0,
        out: u.output_tokens || 0,
        cw: u.cache_creation_input_tokens || 0,
        cr: u.cache_read_input_tokens || 0,
      },
      model: m.model || "unknown",
      ts: d.timestamp || null,
      hint: contentHint(m.content),
      attributionAgent: d.attributionAgent || null,
      attributionSkill: d.attributionSkill || null,
    });
  }
  return { calls, malformed };
}

// Short operation hint: first ~80 chars of assistant text, or the tool being called.
function contentHint(content) {
  if (typeof content === "string") return trunc(content);
  if (!Array.isArray(content)) return "";
  for (const b of content) {
    if (b && b.type === "text" && b.text) return trunc(b.text);
  }
  for (const b of content) {
    if (b && b.type === "tool_use") {
      const t = b.name || "tool";
      const inp = b.input || {};
      const arg = inp.file_path || inp.path || inp.command || inp.pattern || inp.description || "";
      return trunc(`[${t}] ${typeof arg === "string" ? arg : ""}`.trim());
    }
  }
  return "";
}

function trunc(s) {
  s = String(s).replace(/\s+/g, " ").trim();
  return s.length > 80 ? s.slice(0, 80) + "…" : s;
}

// The first user line of an agent transcript holds its task prompt — the best
// human label when meta.json is generic. Returns ~80-char snippet.
function firstUserSnippet(file) {
  try {
    const fd = fs.openSync(file, "r");
    const buf = Buffer.alloc(4096);
    const n = fs.readSync(fd, buf, 0, 4096, 0);
    fs.closeSync(fd);
    const firstLine = buf.toString("utf8", 0, n).split("\n")[0];
    const d = JSON.parse(firstLine);
    const c = d.message && d.message.content;
    if (typeof c === "string") return trunc(c);
    if (Array.isArray(c)) {
      for (const b of c) if (b.type === "text") return trunc(b.text);
    }
  } catch {}
  return "";
}

// meta.json sidecar (agent-{id}.meta.json) carries {agentType, description?}.
// description is the richest label for /wave:builder sub-agents ("BE developer", "gitter SETUP").
function readMeta(agentFile) {
  const metaFile = agentFile.replace(/\.jsonl$/, ".meta.json");
  try {
    return JSON.parse(fs.readFileSync(metaFile, "utf8"));
  } catch {
    return null;
  }
}

function agentIdOf(file) {
  const m = path.basename(file).match(/^agent-([^.]+)\.jsonl$/);
  return m ? m[1] : path.basename(file);
}

// Label priority: meta.description → meta.agentType → attributionAgent → prompt snippet → id.
function labelFor(agentFile, meta, sampleCall, snippet) {
  if (meta && meta.description) return meta.description;
  if (meta && meta.agentType) return meta.agentType;
  if (sampleCall && sampleCall.attributionAgent) return sampleCall.attributionAgent;
  if (snippet) return snippet.slice(0, 50);
  return agentIdOf(agentFile);
}

// ─── AGGREGATION ─────────────────────────────────────────────────────────────────
function emptyAgg() {
  return { calls: 0, in: 0, out: 0, cw: 0, cr: 0, cost: 0, models: new Set() };
}
function foldCall(agg, c) {
  agg.calls++;
  agg.in += c.usage.in;
  agg.out += c.usage.out;
  agg.cw += c.usage.cw;
  agg.cr += c.usage.cr;
  agg.cost += costUSD(c.model, c.usage);
  agg.models.add(c.model);
}
// Fold one row's aggregate into another (for grouping rows into runs/totals).
function mergeAgg(into, agg) {
  into.calls += agg.calls;
  into.in += agg.in;
  into.out += agg.out;
  into.cw += agg.cw;
  into.cr += agg.cr;
  into.cost += agg.cost;
  for (const m of agg.models) into.models.add(m);
}

// Extract the wf_* run id from an agent file path, or null if not under one.
function workflowIdOf(file) {
  const m = file.match(/\/workflows\/(wf_[^/]+)\//);
  return m ? m[1] : null;
}

async function buildLedger(session) {
  const rows = [];
  let totalMalformed = 0;

  // Main conversation loop as its own row.
  if (fs.existsSync(session.mainFile)) {
    const { calls, malformed } = await parseTranscript(session.mainFile);
    totalMalformed += malformed;
    const agg = emptyAgg();
    const ordered = [...calls.values()];
    for (const c of ordered) foldCall(agg, c);
    if (agg.calls > 0) {
      rows.push({
        id: session.conversationId.slice(0, 8),
        label: "MAIN (conversation loop)",
        agg,
        detail: ordered,
        wf: null,
        conv: session.conversationId,
        mtime: session.mtime,
      });
    }
  }

  // Each sub-agent file → one row.
  const agentFiles = findAgentFiles(session.subagentsDir);
  for (const file of agentFiles) {
    const { calls, malformed } = await parseTranscript(file);
    totalMalformed += malformed;
    const ordered = [...calls.values()];
    if (ordered.length === 0) continue;
    const meta = readMeta(file);
    const snippet = meta && meta.description ? "" : firstUserSnippet(file);
    const agg = emptyAgg();
    for (const c of ordered) foldCall(agg, c);
    let mtime = 0;
    try {
      mtime = fs.statSync(file).mtimeMs;
    } catch {}
    rows.push({
      id: agentIdOf(file),
      label: labelFor(file, meta, ordered[0], snippet),
      agg,
      detail: ordered,
      file,
      wf: workflowIdOf(file),
      conv: session.conversationId,
      mtime,
    });
  }
  return { rows, totalMalformed };
}

// ─── CODEX MODE ──────────────────────────────────────────────────────────────────
// Truth source: Codex CLI session rollouts, one file per thread —
//   ~/.codex/sessions/YYYY/MM/DD/rollout-<local-ISO-ts>-<threadId>.jsonl
//   ~/.codex/archived_sessions/rollout-*.jsonl
// Line 1 is {type:"session_meta"} (cwd, thread id, agent role/nickname for subagent
// threads); {type:"turn_context"} carries the model; token accounting rides on
//   {type:"event_msg", payload:{type:"token_count", info:{total_token_usage, last_token_usage}}}
// where info is null on older/idle events (skipped).
//
// COUNTING RULE — sum the SEGMENT PEAKS of total_token_usage. Never the last value,
// never the sum of last_token_usage deltas. Verified against a local corpus:
//   · total_token_usage is cumulative and monotonic WITHIN a segment — a cumulative of
//     19,575 plus a 22,531 delta is followed by a cumulative of 42,106.
//   · It RESETS to ~0 when a thread resumes or compacts. One 110 MB rollout resets 3x;
//     its four segment peaks are 108.1M / 77.2M / 353.2M / 384K, so reading only the
//     last value reports 384,439 instead of 538,898,717 — a 1400x undercount.
//   · Duplicate token_count events re-emit an IDENTICAL cumulative total, so summing
//     last_token_usage deltas overcounts (observed exactly 2x on a 2-event rollout).
// Invariants that held on every event sampled, and that the arithmetic here relies on:
// total = input + output; cached_input ⊆ input; reasoning_output ⊆ output; cache_write = 0.
//
// Only sessions/ and archived_sessions/ are scanned — never the ~/.codex/ root, which
// holds rollout-backup-*.jsonl copies that would double-count a session.

const CODEX_TOKEN_PAT = Buffer.from('"type":"token_count"');
const CODEX_TURNCTX_PAT = Buffer.from('"type":"turn_context"');
const NEWLINE = 0x0a;
const SCAN_CHUNK = 4 << 20; // 4 MB read window
const SCAN_OVERLAP = 1 << 16; // 64 KB — far longer than any token_count/turn_context
// line, which guarantees every such line lands wholly inside at least one window.
const CODEX_META_BYTES = 1 << 19; // 512 KB — line 1 carries the full base_instructions

// Stream a rollout, decoding ONLY the lines that match one of `patterns`. Rollouts run
// to 110 MB of tool output; decoding every line costs ~5.3s per such file, decoding just
// the matches costs ~0.35s for an identical result (measured, 3976 events).
function scanCodexRollout(file, patterns, onLine) {
  let fd;
  try {
    fd = fs.openSync(file, "r");
  } catch (e) {
    warn(`cannot open ${file}: ${e.message}`);
    return;
  }
  try {
    const size = fs.fstatSync(fd).size;
    const buf = Buffer.allocUnsafe(SCAN_CHUNK + SCAN_OVERLAP);
    const seen = new Set(); // absolute line-start offsets already emitted
    let pos = 0;
    while (pos < size) {
      const start = pos === 0 ? 0 : pos - SCAN_OVERLAP;
      const want = Math.min(buf.length, size - start);
      const n = fs.readSync(fd, buf, 0, want, start);
      if (n <= 0) break;
      const view = buf.subarray(0, n);
      for (const pat of patterns) {
        let i = 0;
        while ((i = view.indexOf(pat, i)) !== -1) {
          const e = view.indexOf(NEWLINE, i);
          // Line truncated at the window end — the overlap puts it whole in the next one.
          if (e === -1) break;
          let s = view.lastIndexOf(NEWLINE, i);
          if (s === -1) {
            // Line began before this window; a previous window held it whole.
            if (start !== 0) {
              i += pat.length;
              continue;
            }
            s = 0;
          } else s += 1;
          const abs = start + s;
          if (e > s && !seen.has(abs)) {
            seen.add(abs);
            onLine(view.toString("utf8", s, e));
          }
          i += pat.length;
        }
      }
      pos = start + n;
      if (n < want) break;
    }
  } finally {
    try {
      fs.closeSync(fd);
    } catch {}
  }
}

// Line 1 of a rollout = the session_meta payload. Returns null when unreadable, which
// the caller surfaces as an explicit "?" project rather than dropping the session.
function readCodexMeta(file) {
  let fd;
  try {
    fd = fs.openSync(file, "r");
  } catch {
    return null;
  }
  try {
    const buf = Buffer.allocUnsafe(CODEX_META_BYTES);
    const n = fs.readSync(fd, buf, 0, CODEX_META_BYTES, 0);
    if (n <= 0) return null;
    const view = buf.subarray(0, n);
    const nl = view.indexOf(NEWLINE);
    const d = JSON.parse(view.toString("utf8", 0, nl === -1 ? n : nl));
    return d && d.type === "session_meta" ? d.payload || null : null;
  } catch {
    return null;
  } finally {
    try {
      fs.closeSync(fd);
    } catch {}
  }
}

function codexRootDir(args) {
  return args.codexRoot || path.join(os.homedir(), ".codex");
}

function findCodexRollouts(root) {
  const out = [];
  for (const base of [path.join(root, "sessions"), path.join(root, "archived_sessions")]) {
    if (!fs.existsSync(base)) continue;
    const stack = [base];
    while (stack.length) {
      const d = stack.pop();
      for (const ent of safeReaddir(d)) {
        const full = path.join(d, ent);
        let st;
        try {
          st = fs.statSync(full);
        } catch {
          continue;
        }
        if (st.isDirectory()) stack.push(full);
        else if (ent.startsWith("rollout-") && ent.endsWith(".jsonl")) out.push(full);
      }
    }
  }
  return out;
}

// rollout-<YYYY-MM-DD>T<HH-MM-SS>-<threadId>.jsonl → {date, time, id}
// The filename stamp is LOCAL time (payload.timestamp is UTC) — --since filters on it,
// so "--since <day>" means the operator's own calendar day.
function parseRolloutName(file) {
  const m = path
    .basename(file)
    .match(/^rollout-(\d{4}-\d{2}-\d{2})T(\d{2})-(\d{2})-(\d{2})-(.+)\.jsonl$/);
  if (!m) return null;
  return { date: m[1], time: `${m[2]}:${m[3]}:${m[4]}`, id: m[5] };
}

// cwd → the project it belongs to: "$HOME/<group>/<repo>/<subdir>" → "<repo>".
// The "work/" grouping dir is a checkout-layout convention — adjust it to yours.
function codexProject(cwd) {
  if (!cwd) return "?";
  const home = os.homedir();
  let p = cwd.startsWith(home) ? cwd.slice(home.length) : cwd;
  p = p.replace(/^\/+/, "").replace(/^work\//, "");
  return p.split("/")[0] || cwd;
}

function codexLabel(meta) {
  if (!meta) return "?";
  const role = meta.agent_role || (meta.thread_source === "subagent" ? "subagent" : null);
  if (!role) return "main";
  return meta.agent_nickname ? `${role} (${meta.agent_nickname})` : role;
}

// One rollout → one usage row. Segment-peak summing per the COUNTING RULE above.
function codexSessionUsage(file) {
  const models = new Set();
  const peaks = [];
  let prev = null;
  let segPeak = null;
  let events = 0;
  let malformed = 0;
  scanCodexRollout(file, [CODEX_TOKEN_PAT, CODEX_TURNCTX_PAT], (line) => {
    let d;
    try {
      d = JSON.parse(line);
    } catch {
      malformed++;
      return;
    }
    if (d.type === "turn_context") {
      if (d.payload && d.payload.model) models.add(d.payload.model);
      return;
    }
    if (d.type !== "event_msg") return;
    const p = d.payload;
    if (!p || p.type !== "token_count" || !p.info) return;
    const t = p.info.total_token_usage;
    if (!t) return;
    events++;
    if (prev !== null && t.total_tokens < prev) {
      peaks.push(segPeak);
      segPeak = null;
    }
    prev = t.total_tokens;
    if (!segPeak || t.total_tokens > segPeak.total_tokens) segPeak = t;
  });
  if (segPeak) peaks.push(segPeak);
  const agg = { in: 0, cached: 0, out: 0, reasoning: 0, total: 0 };
  for (const p of peaks) {
    agg.in += p.input_tokens || 0;
    agg.cached += p.cached_input_tokens || 0;
    agg.out += p.output_tokens || 0;
    agg.reasoning += p.reasoning_output_tokens || 0;
    agg.total += p.total_tokens || 0;
  }
  return { agg, models, events, segments: peaks.length, malformed };
}

function buildCodexLedger(args) {
  const root = codexRootDir(args);
  const files = findCodexRollouts(root);
  if (files.length === 0) {
    console.error(`No Codex rollouts found under ${root}/sessions or ${root}/archived_sessions`);
    process.exit(1);
  }
  const cwd = process.cwd();
  const rows = [];
  const byId = new Map();
  let malformed = 0;
  let skippedSince = 0;
  let skippedProject = 0;
  let unreadableMeta = 0;

  for (const file of files) {
    const name = parseRolloutName(file);
    if (name && args.since && name.date < args.since) {
      skippedSince++;
      continue;
    }
    const meta = readCodexMeta(file);
    if (!meta) unreadableMeta++;
    const rolloutCwd = meta && meta.cwd ? meta.cwd : null;
    // Scope: default to the repo we're standing in; --all spans every project. A session
    // whose meta is unreadable is always kept — an unknown cwd is not evidence of absence.
    if (!args.all && rolloutCwd && !(rolloutCwd === cwd || rolloutCwd.startsWith(cwd + "/"))) {
      skippedProject++;
      continue;
    }
    const usage = codexSessionUsage(file);
    malformed += usage.malformed;
    if (usage.events === 0) continue; // a rollout that never billed a turn
    const model = [...usage.models].pop() || "unknown";
    if (usage.models.size > 1) {
      warn(
        `${path.basename(file)} used ${usage.models.size} models (${[...usage.models].join(", ")}) — priced entirely at "${model}"`
      );
    }
    const id = (meta && meta.id) || (name && name.id) || path.basename(file);
    if (byId.has(id)) continue; // sessions/ and archived_sessions/ holding the same thread
    byId.set(id, true);
    rows.push({
      id,
      idTail: String(id).slice(-12),
      date: name ? name.date : "?",
      time: name ? name.time : "?",
      model,
      cwd: rolloutCwd,
      project: codexProject(rolloutCwd),
      label: codexLabel(meta),
      agg: usage.agg,
      cost: codexCostUSD(model, usage.agg),
      segments: usage.segments,
      events: usage.events,
      file,
    });
  }
  return { rows, malformed, skippedSince, skippedProject, unreadableMeta, root, scanned: files.length };
}

function emptyCodexAgg() {
  return { in: 0, cached: 0, out: 0, reasoning: 0, total: 0 };
}
function foldCodexAgg(into, a) {
  into.in += a.in;
  into.cached += a.cached;
  into.out += a.out;
  into.reasoning += a.reasoning;
  into.total += a.total;
}
// Trim the vendor prefix off a Codex model id so the MODEL column stays narrow.
function codexModelShort(m) {
  return String(m).replace(/^gpt-/, "");
}
// Unpriced rows carry cost null; they are summed as tokens but never as dollars, and the
// footer says how many so a total is never quietly short.
function sumCodexCost(rows) {
  let cost = 0;
  let unpriced = 0;
  for (const r of rows) {
    if (r.cost === null) unpriced++;
    else cost += r.cost;
  }
  return { cost, unpriced };
}
function fmtCost(c) {
  return c === null ? "n/a" : fmtUSD(c);
}

function codexFooterNote(rows, total, unpriced) {
  const lines = [
    `\nToken definitions  —  uncached input: ${fmtInt(total.in - total.cached)}` +
      `  ·  cached input: ${fmtInt(total.cached)}` +
      `  ·  output: ${fmtInt(total.out)} (incl. ${fmtInt(total.reasoning)} reasoning)` +
      `  ·  grand total: ${fmtInt(total.total)}`,
  ];
  const resumed = rows.filter((r) => r.segments > 1).length;
  if (resumed) {
    lines.push(
      `${resumed} session(s) resumed/compacted mid-thread; each segment's peak is summed — reading only the final counter would undercount them.`
    );
  }
  if (unpriced) {
    lines.push(
      `${unpriced} session(s) ran an unpriced model and show cost "n/a" — their tokens ARE in the totals, their dollars are NOT. Add the model to PRICING.`
    );
  }
  lines.push(
    `Cached input is a subset of input and is billed at the cached rate; output already includes reasoning.`
  );
  return lines.join("\n");
}

function printCodexTable(rows, args) {
  rows.sort((a, b) => (b.cost || 0) - (a.cost || 0) || b.agg.total - a.agg.total);
  const total = emptyCodexAgg();
  for (const r of rows) foldCodexAgg(total, r.agg);
  const { cost: totalCost, unpriced } = sumCodexCost(rows);

  const limit = args.top === null ? 25 : args.top;
  const shown = limit > 0 ? rows.slice(0, limit) : rows;
  const wide = args.all;
  const H = ["STARTED", "SESSION", "MODEL", ...(wide ? ["PROJECT"] : []), "LABEL", "IN", "CACHED", "OUT", "TOTAL", "EST USD"];
  const data = shown.map((r) => [
    `${r.date} ${r.time}`,
    r.idTail,
    codexModelShort(r.model),
    ...(wide ? [r.project] : []),
    r.label.length > 28 ? r.label.slice(0, 27) + "…" : r.label,
    fmtInt(r.agg.in),
    fmtInt(r.agg.cached),
    fmtInt(r.agg.out),
    fmtInt(r.agg.total),
    fmtCost(r.cost),
  ]);
  data.push([
    "TOTAL",
    `${rows.length} sessions`,
    "",
    ...(wide ? [""] : []),
    "",
    fmtInt(total.in),
    fmtInt(total.cached),
    fmtInt(total.out),
    fmtInt(total.total),
    fmtUSD(totalCost),
  ]);
  const leftCount = wide ? 5 : 4; // STARTED..LABEL are left-aligned
  renderGrid(H, data, new Set([...Array(leftCount).keys()]), new Set([data.length - 1]));
  if (limit > 0 && rows.length > limit) {
    console.log(`\n… ${rows.length - limit} more session(s) not shown — use --top 0 for the full list.`);
  }
  console.log(codexFooterNote(rows, total, unpriced));
}

function printCodexByDay(rows) {
  const days = new Map();
  for (const r of rows) {
    let g = days.get(r.date);
    if (!g) {
      g = { date: r.date, sessions: 0, agg: emptyCodexAgg(), rows: [] };
      days.set(r.date, g);
    }
    g.sessions++;
    g.rows.push(r);
    foldCodexAgg(g.agg, r.agg);
  }
  const list = [...days.values()].sort((a, b) => a.date.localeCompare(b.date));
  const total = emptyCodexAgg();
  const H = ["DATE", "SESSIONS", "IN", "CACHED", "OUT", "TOTAL", "EST USD"];
  const data = list.map((g) => {
    foldCodexAgg(total, g.agg);
    return [
      g.date,
      String(g.sessions),
      fmtInt(g.agg.in),
      fmtInt(g.agg.cached),
      fmtInt(g.agg.out),
      fmtInt(g.agg.total),
      fmtUSD(sumCodexCost(g.rows).cost),
    ];
  });
  const { cost: totalCost, unpriced } = sumCodexCost(rows);
  data.push([
    "TOTAL",
    String(rows.length),
    fmtInt(total.in),
    fmtInt(total.cached),
    fmtInt(total.out),
    fmtInt(total.total),
    fmtUSD(totalCost),
  ]);
  renderGrid(H, data, new Set([0]), new Set([data.length - 1]));
  console.log(codexFooterNote(rows, total, unpriced));
}

function codexToJSON(rows) {
  return rows
    .map((r) => ({
      session_id: r.id,
      started: `${r.date}T${r.time}`,
      model: r.model,
      project: r.project,
      cwd: r.cwd,
      label: r.label,
      input_tokens: r.agg.in,
      cached_input_tokens: r.agg.cached,
      output_tokens: r.agg.out,
      reasoning_output_tokens: r.agg.reasoning,
      total_tokens: r.agg.total,
      segments: r.segments,
      est_cost_usd: r.cost === null ? null : Number(r.cost.toFixed(6)),
    }))
    .sort((a, b) => (b.est_cost_usd || 0) - (a.est_cost_usd || 0));
}

function runCodex(args) {
  // Claude-only flags would otherwise be a silent no-op here.
  for (const [flag, on] of [
    ["--by-workflow", args.byWorkflow],
    ["--detail", args.detail],
    ["--session", args.session],
    ["--project", args.project],
  ]) {
    if (on) warn(`${flag} has no meaning in --codex mode — ignored (see --help)`);
  }
  const built = buildCodexLedger(args);
  let rows = built.rows;
  let matched = null;
  if (args.filter) {
    const q = args.filter.toLowerCase();
    rows = rows.filter((r) =>
      [r.label, r.model, r.project, r.cwd || "", r.id].some((f) =>
        String(f).toLowerCase().includes(q)
      )
    );
    matched = rows.length;
  }
  if (args.json) {
    console.log(
      JSON.stringify(
        {
          source: "codex",
          root: built.root,
          rollouts_scanned: built.scanned,
          rows: codexToJSON(rows),
          malformed_lines: built.malformed,
          unreadable_meta: built.unreadableMeta,
        },
        null,
        2
      )
    );
  } else {
    const scopeDesc = args.all ? "all projects" : `cwd ${process.cwd()}`;
    const sinceDesc = args.since ? ` · since ${args.since}` : "";
    const filterDesc = matched !== null ? ` · filter "${args.filter}" matched ${matched}` : "";
    console.log(
      `token-ledger · codex · ${scopeDesc}${sinceDesc} · ${rows.length} session(s) of ${built.scanned} rollout(s)${filterDesc}\n`
    );
    if (rows.length === 0) {
      // Distinguish "nothing matched" from "we failed to look".
      console.log(
        `No sessions in scope. Skipped: ${built.skippedSince} before --since, ${built.skippedProject} in another project (use --all), ${built.unreadableMeta} with unreadable metadata.`
      );
    } else if (args.byDay) printCodexByDay(rows);
    else printCodexTable(rows, args);
  }
  if (built.unreadableMeta && !args.json) {
    warn(`${built.unreadableMeta} rollout(s) had unreadable session_meta — kept, project shown as "?"`);
  }
  return built.malformed;
}

// ─── OUTPUT ──────────────────────────────────────────────────────────────────────
function fmtInt(n) {
  return n.toLocaleString("en-US");
}
function fmtUSD(n) {
  return "$" + n.toFixed(4);
}
function modelShort(models) {
  return [...models].map((m) => m.replace(/^claude-/, "")).join(",") || "—";
}

// Render an aligned text grid. `leftCols` = set of column indices left-aligned
// (the rest right-align); `sepBefore` = set of row indices to print a separator above.
function renderGrid(headers, data, leftCols, sepBefore = new Set()) {
  const widths = headers.map((h, i) =>
    Math.max(h.length, ...data.map((r) => String(r[i] || "").length))
  );
  const pad = (s, i) =>
    leftCols.has(i) ? String(s).padEnd(widths[i]) : String(s).padStart(widths[i]);
  const sep = widths.map((w) => "─".repeat(w)).join("─┼─");
  console.log(headers.map((h, i) => pad(h, i)).join(" │ "));
  console.log(sep);
  data.forEach((row, ri) => {
    if (sepBefore.has(ri)) console.log(sep);
    console.log(row.map((c, i) => pad(c, i)).join(" │ "));
  });
}

function printTable(rows) {
  rows.sort((a, b) => b.agg.cost - a.agg.cost);
  const total = emptyAgg();
  for (const r of rows) {
    total.calls += r.agg.calls;
    total.in += r.agg.in;
    total.out += r.agg.out;
    total.cw += r.agg.cw;
    total.cr += r.agg.cr;
    total.cost += r.agg.cost;
  }
  const H = ["AGENT / OPERATION", "MODEL", "CALLS", "IN", "OUT", "CACHE-W", "CACHE-R", "EST USD"];
  const data = rows.map((r) => [
    r.label.length > 38 ? r.label.slice(0, 37) + "…" : r.label,
    modelShort(r.agg.models),
    String(r.agg.calls),
    fmtInt(r.agg.in),
    fmtInt(r.agg.out),
    fmtInt(r.agg.cw),
    fmtInt(r.agg.cr),
    fmtUSD(r.agg.cost),
  ]);
  data.push([
    "TOTAL",
    "",
    String(total.calls),
    fmtInt(total.in),
    fmtInt(total.out),
    fmtInt(total.cw),
    fmtInt(total.cr),
    fmtUSD(total.cost),
  ]);
  renderGrid(H, data, new Set([0, 1]), new Set([data.length - 1]));
  // Calibration line: the four token definitions.
  const fresh = total.in + total.out + total.cw;
  console.log(
    `\nToken definitions  —  output-only: ${fmtInt(total.out)}` +
      `  ·  in+out (no cache): ${fmtInt(total.in + total.out)}` +
      `  ·  in+out+cache-write (fresh/billed-new): ${fmtInt(fresh)}` +
      `  ·  +cache-read (grand total): ${fmtInt(fresh + total.cr)}`
  );
}

function printDetail(rows, query) {
  const q = query.toLowerCase();
  const hit = rows.find(
    (r) => r.id.toLowerCase().includes(q) || r.label.toLowerCase().includes(q)
  );
  if (!hit) {
    console.log(`No agent matched "${query}".`);
    return;
  }
  console.log(`Detail for: ${hit.label}  [${hit.id}]  (${hit.agg.calls} calls)\n`);
  const H = ["#", "TIMESTAMP", "MODEL", "IN", "OUT", "C-W", "C-R", "USD", "HINT"];
  const data = hit.detail.map((c, i) => [
    String(i + 1),
    (c.ts || "").replace("T", " ").replace(/\.\d+Z$/, ""),
    (c.model || "").replace(/^claude-/, ""),
    fmtInt(c.usage.in),
    fmtInt(c.usage.out),
    fmtInt(c.usage.cw),
    fmtInt(c.usage.cr),
    fmtUSD(costUSD(c.model, c.usage)),
    c.hint || "",
  ]);
  // left-align timestamp(1), model(2), hint(8); right-align the numeric columns
  renderGrid(H, data, new Set([1, 2, 8]));
}

// Group rows by their wf_* run dir → one row per workflow run. Rows not under a
// wf_* dir (session-level /wave:builder agents, MAIN loops) fold into one trailing summary
// row labeled "(non-workflow agents)" — never silently dropped.
function printByWorkflow(rows) {
  const groups = new Map(); // wf id -> { wf, conv, agentCount, agg, mtime }
  const nonWf = { agg: emptyAgg(), agentCount: 0, mtime: 0 };
  for (const r of rows) {
    if (r.wf) {
      let g = groups.get(r.wf);
      if (!g) {
        g = { wf: r.wf, conv: r.conv, agentCount: 0, agg: emptyAgg(), mtime: 0 };
        groups.set(r.wf, g);
      }
      g.agentCount++;
      g.mtime = Math.max(g.mtime, r.mtime || 0);
      mergeAgg(g.agg, r.agg);
    } else {
      nonWf.agentCount++;
      nonWf.mtime = Math.max(nonWf.mtime, r.mtime || 0);
      mergeAgg(nonWf.agg, r.agg);
    }
  }
  const list = [...groups.values()].sort((a, b) => b.agg.cost - a.agg.cost);
  const total = emptyAgg();
  const fmtDate = (ms) => (ms ? new Date(ms).toISOString().slice(0, 10) : "—");
  const mkRow = (date, wf, conv, count, agg) => {
    mergeAgg(total, agg);
    const fresh = agg.in + agg.out + agg.cw;
    return [date, wf, conv, String(count), fmtInt(fresh), fmtInt(fresh + agg.cr), fmtUSD(agg.cost)];
  };
  const H = ["DATE", "WORKFLOW RUN", "PARENT CONV", "AGENTS", "FRESH", "GRAND TOTAL", "EST USD"];
  const data = list.map((g) =>
    mkRow(fmtDate(g.mtime), g.wf, g.conv.slice(0, 8), g.agentCount, g.agg)
  );
  if (nonWf.agentCount > 0) {
    data.push(mkRow(fmtDate(nonWf.mtime), "(non-workflow agents)", "—", nonWf.agentCount, nonWf.agg));
  }
  const fresh = total.in + total.out + total.cw;
  data.push(["TOTAL", "", "", "", fmtInt(fresh), fmtInt(fresh + total.cr), fmtUSD(total.cost)]);
  renderGrid(H, data, new Set([0, 1, 2]), new Set([data.length - 1]));
  console.log(
    "\nFRESH = in+out+cache-write (the harness's subagent_tokens definition); GRAND TOTAL adds cache-read." +
      "\nA wf_* row exists only for a Workflow-engine run — a script under the repo's workflows/ or a skill-embedded" +
      "\nengine (/deep-rr). /wave:orchestrator, /wave:builder and /wave:walker run in their chats' main" +
      "\nsessions and land in (non-workflow agents) instead. The TOTAL row sums both —" +
      "\na wave's end-to-end chat cost. Total a wave with --filter <wave-label>."
  );
}

function toJSON(rows) {
  return rows
    .map((r) => ({
      id: r.id,
      label: r.label,
      model: modelShort(r.agg.models),
      calls: r.agg.calls,
      input_tokens: r.agg.in,
      output_tokens: r.agg.out,
      cache_creation: r.agg.cw,
      cache_read: r.agg.cr,
      est_cost_usd: Number(r.agg.cost.toFixed(6)),
    }))
    .sort((a, b) => b.est_cost_usd - a.est_cost_usd);
}

// ─── MAIN ────────────────────────────────────────────────────────────────────────
const HELP = `token-ledger — per-agent token attribution from Claude Code JSONL transcripts

Usage: node token-ledger.mjs [options]
  (default)              most recent session for the current project (cwd-derived)
  --all                  every session for the current project
  --session <id|path>    a specific conversationId or a path to a project dir / main .jsonl
  --project <slug>       project slug override (default: slugified cwd)
  --root <dir>           extra transcript root (repeatable)
  --detail <id|substr>   list one agent's individual API calls in order
  --by-workflow          group by workflow run (wf_*) instead of by agent — one row
                         per run + a "(non-workflow agents)" summary row + TOTAL.
                         wf_* = a Workflow-engine run (workflows/* or /deep-rr).
                         /wave:orchestrator, /wave:builder and /wave:walker land in
                         (non-workflow agents) instead — total a wave with
                         --filter <wave-label>.
  --filter <substr>      restrict the per-agent table + totals to rows whose label or
                         model id contains <substr> (case-insensitive); prints match
                         count. Composes with --all / --session / --json.
  --since <YYYY-MM-DD>   drop sessions older than that calendar day
  --json                 machine-readable output
  -h, --help             this help

Discovery roots (auto): ~/.claude  and  ~/.claude-sessions/*/

Codex mode — per-session spend for the Codex CLI:
  --codex                read ~/.codex/sessions/**/rollout-*.jsonl + archived_sessions/
                         instead of Claude transcripts. One row per session thread
                         (a Codex subagent writes its own rollout, so subagents are
                         attributed individually via their agent role).
                         Scope defaults to the repo you are standing in; --all spans
                         every project and adds a PROJECT column.
  --since <YYYY-MM-DD>   keep rollouts stamped on or after that day (filename = local time)
  --by-day               one row per calendar day instead of per session
  --top <n>              cap the session table (default 25; 0 = every row)
  --filter <substr>      match on label, model, project, cwd, or session id
  --codex-root <dir>     override ~/.codex
  --json / -h            as above

  Codex counting: total_token_usage is cumulative but RESETS on resume/compaction, so
  each segment's peak is summed. cached_input is a subset of input (billed at the cached
  rate); output already includes reasoning; an unpriced model reports cost "n/a".`;

async function main() {
  const args = parseArgs(process.argv.slice(2));
  if (args.help) {
    console.log(HELP);
    return;
  }

  if (args.codex) {
    const malformed = runCodex(args);
    if (WARNINGS.length) {
      console.error(`\n[${WARNINGS.length} warning(s)]`);
      for (const w of [...new Set(WARNINGS)].slice(0, 10)) console.error("  " + w);
    }
    if (malformed && !args.json) console.error(`\n[skipped ${malformed} malformed line(s)]`);
    return;
  }

  const roots = [...defaultRoots(), ...args.roots];
  const slug = args.project || projectSlug(process.cwd());
  const projectDirs = findProjectDirs(roots, slug);

  if (projectDirs.length === 0) {
    console.error(`No transcript dirs found for project slug "${slug}" under roots:\n  ${roots.join("\n  ")}`);
    process.exit(1);
  }

  let sessions = listSessions(projectDirs);

  // DEDUP across roots: ~/.claude and ~/.claude-sessions are HARDLINKS to the same
  // inodes for shared conversations. Collapse by real inode of the main file so we
  // never double-count the same session discovered under two roots.
  const byInode = new Map();
  for (const s of sessions) {
    let key = s.mainFile;
    try {
      const st = fs.statSync(s.mainFile);
      key = `${st.dev}:${st.ino}`;
    } catch {}
    // keep the one whose subagents dir actually exists, else the first seen
    const prev = byInode.get(key);
    if (!prev || (!prev.subagentsDir && s.subagentsDir)) byInode.set(key, s);
  }
  sessions = [...byInode.values()];

  // --since drops sessions whose transcript last changed before that calendar day.
  if (args.since) {
    const before = sessions.length;
    sessions = sessions.filter(
      (s) => s.mtime && new Date(s.mtime).toISOString().slice(0, 10) >= args.since
    );
    if (sessions.length === 0) {
      console.error(`No sessions on or after ${args.since} (filtered out all ${before}).`);
      process.exit(1);
    }
  }

  // Scope selection.
  let scope = [];
  if (args.session) {
    const sel = args.session;
    scope = sessions.filter(
      (s) => s.conversationId === sel || s.mainFile.includes(sel)
    );
    if (scope.length === 0) {
      // Maybe a direct path to a project dir or main file.
      if (fs.existsSync(sel)) {
        let dir = sel,
          id = null;
        if (sel.endsWith(".jsonl")) {
          id = path.basename(sel, ".jsonl");
          dir = path.dirname(sel);
        }
        scope = listSessions([dir]).filter((s) => !id || s.conversationId === id);
      }
    }
    if (scope.length === 0) {
      console.error(`No session matched "${sel}".`);
      process.exit(1);
    }
  } else if (args.all) {
    scope = sessions;
  } else {
    // Default: the single most recent session that actually has sub-agents.
    const withAgents = sessions.filter((s) => s.subagentsDir);
    const pool = withAgents.length ? withAgents : sessions;
    pool.sort((a, b) => b.mtime - a.mtime);
    scope = pool.slice(0, 1);
  }

  // Build ledger across scope, merging rows by id when --all spans sessions.
  const allRows = [];
  let malformed = 0;
  for (const s of scope) {
    const { rows, totalMalformed } = await buildLedger(s);
    malformed += totalMalformed;
    allRows.push(...rows);
  }

  // --filter restricts the per-agent table + its totals (and json/detail) to rows
  // whose label OR model id contains the substring (case-insensitive).
  let viewRows = allRows;
  let matched = null;
  if (args.filter) {
    const q = args.filter.toLowerCase();
    viewRows = allRows.filter(
      (r) =>
        r.label.toLowerCase().includes(q) ||
        [...r.agg.models].some((m) => String(m).toLowerCase().includes(q))
    );
    matched = viewRows.length;
  }

  if (args.byWorkflow) {
    const scopeDesc = args.all ? `${scope.length} sessions` : `${scope.length} session(s)`;
    console.log(`token-ledger · by-workflow · project "${slug}" · scope: ${scopeDesc}\n`);
    printByWorkflow(viewRows);
  } else if (args.detail) {
    printDetail(viewRows, args.detail);
  } else if (args.json) {
    console.log(JSON.stringify({ rows: toJSON(viewRows), malformed_lines: malformed }, null, 2));
  } else {
    const scopeDesc = args.all
      ? `${scope.length} sessions`
      : args.session
        ? `session ${scope.map((s) => s.conversationId.slice(0, 8)).join(", ")}`
        : `latest session ${scope[0]?.conversationId.slice(0, 8) || "?"}`;
    const filterDesc = matched !== null ? ` · filter "${args.filter}" matched ${matched} row(s)` : "";
    console.log(`token-ledger · project "${slug}" · scope: ${scopeDesc} · ${viewRows.length} rows${filterDesc}\n`);
    printTable(viewRows);
  }

  if (WARNINGS.length) {
    const unknownModels = WARNINGS.filter((w) => w.includes("unknown model"));
    console.error(`\n[${WARNINGS.length} warning(s)]`);
    for (const w of [...new Set(WARNINGS)].slice(0, 10)) console.error("  " + w);
  }
  if (malformed && !args.json) console.error(`\n[skipped ${malformed} malformed line(s)]`);
}

main().catch((e) => {
  console.error("token-ledger fatal:", e.stack || e.message);
  process.exit(1);
});
