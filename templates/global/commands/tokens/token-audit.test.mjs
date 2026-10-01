// token-audit.test.mjs — run with: node --test templates/global/commands/tokens/
// Every fixture under fixtures/ is SYNTHETIC JSONL written for this suite: no real
// transcript, no prompt text, no host path. Set TOKEN_AUDIT_BIN to point the suite at a
// different build of the script (used to watch a test fail against the unfixed code).
import test from "node:test";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

const HERE = path.dirname(fileURLToPath(import.meta.url));
const BIN = process.env.TOKEN_AUDIT_BIN || path.join(HERE, "token-audit.mjs");
const FIX = path.join(HERE, "fixtures");
const CLAUDE_ROOT = path.join(FIX, "claude", "projects");
const CODEX_ROOT = path.join(FIX, "codex");
const TMP = fs.mkdtempSync(path.join(os.tmpdir(), "token-audit-test-"));
// pfm's shipped table, read where pfm keeps it: every run prices from it unless env says otherwise.
const PRICES = path.join(HERE, "../../../../pfm/internal/pricing/prices.json");

function run(args, env = {}) {
  const r = spawnSync(process.execPath, [BIN, ...args], { encoding: "utf8", cwd: HERE, maxBuffer: 64 << 20, env: { ...process.env, TOKEN_AUDIT_PRICES: PRICES, ...env } });
  if (r.error) assert.fail(`could not run ${BIN}: ${r.error.message}`);
  return { code: r.status, out: r.stdout || "", err: r.stderr || "" };
}
const flightDir = (name) => (path.isAbsolute(name) ? name : path.join(FIX, name));
const flight = (dir, extra = [], codexRoot = CODEX_ROOT) => {
  const md = path.join(TMP, `report-${Math.random().toString(36).slice(2)}.md`), js = md.replace(/\.md$/, ".json");
  const r = run(["--flight", flightDir(dir), "--root", CLAUDE_ROOT, "--codex-root", codexRoot, "--metrics-out", md, "--out", js, ...extra]);
  return { ...r, md: fs.readFileSync(md, "utf8"), json: JSON.parse(fs.readFileSync(js, "utf8")) };
};
// A flight whose only ledger is run.md. It is built here, not under fixtures/, because every
// .md below templates/global/commands/** compiles into a slash command in all three engine
// registries — a fixture there would ship as a bogus command.
function runMdFlight() {
  const dir = fs.mkdtempSync(path.join(TMP, "runmd-"));
  fs.writeFileSync(path.join(dir, "run.md"), [
    "flight $HOME/.local/state/pfm/flights/professor/demo · baseline 0000000000000000000000000000000000000000 · 2026-09-20T09:00:00.000Z",
    "1-a CLAIMED · executor · requested model claude-sonnet-5 · 2026-09-20T09:01:00.000Z",
    "1-a DONE · executor returned",
  ].join("\n") + "\n");
  return dir;
}
const rowOf = (j, id) => j.rows.find((x) => x.taskId === id);

test("--flight: agents.tsv rows match by agent id, and the report names the engine per row", () => {
  const f = flight("flight");
  assert.equal(f.code, 0, f.err);
  assert.equal(rowOf(f.json, "1-a").how, "id");
  assert.equal(rowOf(f.json, "1-a").engine, "claude");
  assert.equal(rowOf(f.json, "1-d").how, "id");
  assert.match(f.md, /^# flight · metrics$/m);
});

test("--flight: an agent id Codex does not expose falls back to THIS row's window and says so", () => {
  const b = rowOf(flight("flight").json, "1-b");
  assert.equal(b.how, "window", "1-b's ledger id matches no rollout id form; it must degrade to a window match, not vanish");
  assert.equal(b.engine, "codex");
  assert.equal(b.model, "gpt-5.6-sol");
});

// A Codex root holding the fixture rollout re-labelled as a sub-agent spawned at an agent path,
// and a flight whose ledger knows that agent only by its path, with a clockless spawn time.
function agentPathFlight(ledgerId, spawnedAt = "2026-09-20T09:05:00.000Z") {
  const root = fs.mkdtempSync(path.join(TMP, "cxpath-")), day = path.join(root, "sessions", "2026", "09", "20");
  fs.mkdirSync(day, { recursive: true });
  const src = path.join(CODEX_ROOT, "sessions", "2026", "09", "20", "rollout-2026-09-20T09-05-00-cx-thread-1.jsonl");
  const lines = fs.readFileSync(src, "utf8").split("\n"), meta = JSON.parse(lines[0]);
  Object.assign(meta.payload, { id: "cx-thread-9", timestamp: spawnedAt, agent_path: "/root/fix_1p", source: { subagent: { thread_spawn: { parent_thread_id: "cx-parent-1", agent_path: "/root/fix_1p", agent_role: "executor" } } } });
  lines[0] = JSON.stringify(meta);
  fs.writeFileSync(path.join(day, "rollout-2026-09-20T09-05-00-cx-thread-9.jsonl"), lines.join("\n"));
  const dir = fs.mkdtempSync(path.join(TMP, "pathflight-"));
  fs.writeFileSync(path.join(dir, "agents.tsv"), `1-p\texecutor\t${ledgerId}\t1\t2026-09-20\tcodex\n`);
  return { dir, root };
}

test("--flight: a Codex ledger row carrying an agent path matches the rollout spawned at that path", () => {
  const { dir, root } = agentPathFlight("/root/fix_1p");
  const f = flight(dir, [], root);
  assert.equal(f.code, 0, f.err);
  assert.equal(rowOf(f.json, "1-p")?.how, "path", "the orchestrator knows a Codex sub-agent by agent path, never by thread id");
  assert.match(f.md, /by agent path 1/);
});

test("--flight: a spawn time with no clock never opens a match window", () => {
  // The rollout starts half an hour past midnight: inside the window a clockless "2026-09-20" would open.
  const { dir, root } = agentPathFlight("/root/some_other_agent", "2026-09-20T00:30:00.000Z");
  const f = flight(dir, [], root);
  assert.equal(rowOf(f.json, "1-p"), undefined, "midnight is not a spawn time: the row must stay UNMATCHED, not grab the nearest rollout");
  assert.match(f.md, /LEDGER ROW 1-p/);
  assert.match(f.out + f.md, /has no clock/);
});

test("--flight: a ledger row with no transcript AND a transcript with no ledger row are both UNMATCHED", () => {
  const f = flight("flight");
  assert.equal(rowOf(f.json, "1-c"), undefined, "1-c has no transcript, so it must not appear as a measured row");
  assert.ok(f.json.unmatched.some((u) => u.startsWith("LEDGER ROW 1-c")), `no LEDGER ROW 1-c in ${JSON.stringify(f.json.unmatched)}`);
  assert.ok(f.json.unmatched.some((u) => u.startsWith("TRANSCRIPT") && u.includes("d4")), `no UNMATCHED transcript for d4 in ${JSON.stringify(f.json.unmatched)}`);
  assert.match(f.md, /## unmatched/);
});

test("--flight: an UNMATCHED transcript carries its price, and the total names the unledgered spend", () => {
  const f = flight("flight");
  const d4 = f.json.unmatched.find((u) => u.startsWith("TRANSCRIPT") && u.includes("d4"));
  assert.match(d4 || "", /\$\d+\.\d{2}/, `the unmatched d4 line must carry its dollars: ${d4}`);
  assert.ok(f.json.unledgered && f.json.unledgered.n >= 1 && f.json.unledgered.usd > 0, `unledgered spend missing from the JSON: ${JSON.stringify(f.json.unledgered)}`);
  assert.match(f.md, /unledgered \d+ transcript\(s\) under this flight's session · \$\d+\.\d{2} — the flight's spend is \$\d+\.\d{2}/);
});

// A sub-agent that spins on `true` while Claude Code writes a total_tokens_reminder attachment
// after every tool result: the attachment must never hide the Bash call that triggered the next one.
function attachmentPollFlight() {
  const root = fs.mkdtempSync(path.join(TMP, "att-root-")), sub = path.join(root, "-tmp-att-proj", "sess-att", "subagents");
  fs.mkdirSync(sub, { recursive: true });
  fs.writeFileSync(path.join(root, "-tmp-att-proj", "sess-att.jsonl"), JSON.stringify({ type: "user", timestamp: "2026-09-20T09:00:00.000Z", cwd: "/tmp/att-proj", message: { content: "run the flight" } }) + "\n");
  fs.writeFileSync(path.join(sub, "agent-p1.meta.json"), JSON.stringify({ agentType: "executor", description: "task 1-p", spawnDepth: 1 }));
  const at = (s) => new Date(Date.parse("2026-09-20T09:01:00.000Z") + s * 1000).toISOString();
  const lines = [{ type: "user", timestamp: at(0), cwd: "/tmp/att-proj", message: { content: "task 1-p brief" } }];
  for (let i = 0; i < 10; i++) {
    const cmd = i === 0 ? "go -C pfm test ./internal/doctor/ -run TestX" : "true";
    lines.push({ type: "assistant", timestamp: at(10 + i * 2), cwd: "/tmp/att-proj", requestId: `rq${i}`, message: { id: `m${i}`, model: "claude-sonnet-5",
      content: [{ type: "tool_use", id: `t${i}`, name: "Bash", input: { command: cmd } }],
      usage: { input_tokens: 10, output_tokens: 20, cache_read_input_tokens: 5000 + i * 100, cache_creation_input_tokens: 100, cache_creation: { ephemeral_5m_input_tokens: 100, ephemeral_1h_input_tokens: 0 } } } });
    lines.push({ type: "user", timestamp: at(11 + i * 2), cwd: "/tmp/att-proj", message: { content: [{ type: "tool_result", tool_use_id: `t${i}`, content: "", is_error: false }] } });
    lines.push({ type: "attachment", timestamp: at(11 + i * 2), cwd: "/tmp/att-proj", attachment: { type: "total_tokens_reminder", content: "tokens left" } });
  }
  fs.writeFileSync(path.join(sub, "agent-p1.jsonl"), lines.map((l) => JSON.stringify(l)).join("\n") + "\n");
  const dir = fs.mkdtempSync(path.join(TMP, "att-flight-"));
  fs.writeFileSync(path.join(dir, "agents.tsv"), "1-p\texecutor\tp1\t1\t2026-09-20T09:01:00.000Z\tclaude\n");
  return { root, dir };
}

test("--flight: a Bash poll is counted even when a harness attachment follows every tool result", () => {
  const { root, dir } = attachmentPollFlight();
  const f = flight(dir, ["--root", root]);
  assert.equal(f.code, 0, f.err);
  const p = rowOf(f.json, "1-p");
  assert.ok(p, `1-p must match its transcript: ${JSON.stringify(f.json.unmatched)}`);
  // a poll is billed on the call its result triggers: nine `true` results, the last one ends the run
  assert.equal(p.pollN, 8, `the eight calls triggered by a repeated \`true\` are polls, got pollN ${p.pollN}`);
});

test("--flight: the call cap comes from the agent type name — executor 80, lander 150", () => {
  const j = flight("flight").json;
  const d = rowOf(j, "1-d"), e = rowOf(j, "1-e");
  assert.equal(d.calls, 85);
  assert.equal(d.overCap, true, "85 calls by an executor is over the 80 cap");
  assert.equal(e.overCap, false, "a lander is capped at 150, so 2 calls is not over");
  assert.match(flight("flight").md, /OVER 80/);
});

test("--flight: an unpriced model renders n/a with its tokens still counted, never $0", () => {
  const f = flight("flight");
  const e = rowOf(f.json, "1-e");
  assert.equal(e.usd, null, "an unpriced run's dollars are unknown, not zero");
  assert.ok(e.tok.in + e.tok.cr + e.tok.out > 0, "its tokens must still be counted");
  assert.match(f.md, /\| n\/a \|/, "the $ column must read n/a");
  assert.doesNotMatch(f.md.split("## totals")[0], /\| \$0\.00 \|/, "no row may render an unknown price as $0.00");
  assert.match(f.md, /data gaps:.*UNPRICED calls \{[^}]*unobtanium[^}]*\} — tokens counted, dollars "n\/a"; add the model to pfm\.prices\.json/);
});

test("--flight: the report stays bounded and carries the gaps and cross-check lines", () => {
  const f = flight("flight");
  assert.ok(f.md.split("\n").length < 200, `report is ${f.md.split("\n").length} lines`);
  assert.match(f.md, /^data gaps: /m);
  assert.match(f.md, /^cross-check: /m);
});

test("--flight: the long-context premium is a per-model price-table rate, not a flat constant", () => {
  const f = flight("flight");
  const m = /per-model rate in pfm's price table — an estimate\) this flight reads \$([\d.]+) \(([\d.]+)x the headline\)/.exec(f.md);
  assert.ok(m, `no cross-check ratio in:\n${f.md}`);
  // claude-sonnet-5 and gpt-5.6-sol bill their whole window at standard rates (long_in/long_out 1/1),
  // so a run past 200K reads 1.00x; a flat long-context constant would lift it above.
  assert.equal(m[2], "1.00", `a 1/1 price-table row must leave the long-context number at 1.00x, got ${m[2]}x`);
});

test("--flight: with no agents.tsv, run.md is the fallback and every row is marked window", () => {
  const f = flight(runMdFlight());
  assert.match(f.json.source, /run\.md/);
  assert.ok(f.json.rows.length > 0);
  assert.ok(f.json.rows.every((r) => r.how === "window"), "run.md carries no agent id, so no row may claim an id match");
});

test("--flight: a directory with neither agents.tsv nor run.md fails loudly", () => {
  const empty = fs.mkdtempSync(path.join(TMP, "empty-"));
  const r = run(["--flight", empty, "--root", CLAUDE_ROOT]);
  assert.notEqual(r.code, 0);
  assert.match(r.err, /no agents\.tsv/);
});

test("Codex: the cumulative counter resets after a compaction, so each segment's peak is summed", () => {
  const r = run(["--codex", "--since", "99999d", "--codex-root", CODEX_ROOT]);
  assert.equal(r.code, 0, r.err);
  // segment 1 peaks at 70,000 total tokens, segment 2 at 21,000 → 91,000, not 21,000
  assert.match(r.out, /TOTAL 91,000 tok/, r.out);
  assert.match(r.out, /resumed or compacted mid-run/);
  assert.match(r.out, /subagent \(executor\)/, "a Codex sub-agent is attributed from session_meta.source");
});

test("Codex: empty write_stdin polls, a failed command, a re-read and a contract read are all counted", () => {
  const b = rowOf(flight("flight").json, "1-b");
  assert.equal(b.pollN, 3, "three write_stdin calls sent chars: \"\" — pure polls");
  assert.equal(b.failedCmds, 1, "one exec output began 'Script failed'");
  assert.equal(b.rereadN, 1, "the same file was read twice in one thread");
  assert.equal(b.contractReads, 1, "AGENTS.md was read once");
  assert.equal(b.compactions, 1);
});

test("default report: synthetic / zero-usage calls are named in the data-gaps line", () => {
  const r = run(["--since", "99999d", "--root", CLAUDE_ROOT]);
  assert.equal(r.code, 0, r.err);
  assert.match(r.out, /^data gaps:.*synthetic\/zero-usage calls/m, "a dropped call that appears only in --out JSON is a gap the text report claims not to have");
});

test("default report: an unpriced run prints n/a in the run table, never $0.0000", () => {
  const js = path.join(TMP, "default.json");
  const r = run(["--since", "99999d", "--root", CLAUDE_ROOT, "--out", js]);
  assert.equal(r.code, 0, r.err);
  const j = JSON.parse(fs.readFileSync(js, "utf8"));
  const un = j.runs.find((x) => x.unpriced);
  assert.ok(un, "the unpriced fixture run must still be in RUNS");
  assert.ok(un.calls > 0 && un.tok.in > 0, "its calls and tokens must be counted");
  const section = r.out.split("SINGLE RUNS")[1] || "";
  assert.match(section, /\bn\/a\b/, "the unpriced run must render n/a in the run table");
});

test("Bash classification: `dev.sh status` reports state and is not a test/build run", () => {
  const js = path.join(TMP, "cats.json");
  const r = run(["--since", "99999d", "--root", CLAUDE_ROOT, "--out", js]);
  assert.equal(r.code, 0, r.err);
  const j = JSON.parse(fs.readFileSync(js, "utf8"));
  // the fixtures run exactly three real builds: `go test`, `go build`, `dev.sh test templates`
  assert.equal(j.bash["Bash · test/build/docker runs"].n, 3, "`dev.sh status` and `dev.sh install`-less verbs must not count as runs");
});

test("poll detection: a one-off `sleep` is a poll even though the command never repeats", () => {
  const js = path.join(TMP, "poll.json");
  run(["--since", "99999d", "--root", CLAUDE_ROOT, "--out", js]);
  const j = JSON.parse(fs.readFileSync(js, "utf8"));
  const d4 = j.runs.find((x) => x.file.includes("agent-d4"));
  assert.ok(d4, "agent-d4 must be in the report");
  assert.ok(d4.pollN >= 1, `a lone 'sleep 120' must register as a poll, got ${d4.pollN}`);
});

test("--family selects a family by agent type when no main chat title carries the name", () => {
  const r = run(["--since", "99999d", "--root", CLAUDE_ROOT, "--family", "lander"]);
  assert.equal(r.code, 0, r.err);
  assert.match(r.out, /FAMILY DRILL-DOWN "lander"/);
  assert.doesNotMatch(r.out, /NOT FOUND among/, "a flight orchestrated by a sub-agent has no chat title; the selector must still find it");
});

test("--session selects one session and refuses a prefix that matches nothing", () => {
  const ok = run(["--since", "99999d", "--root", CLAUDE_ROOT, "--session", "sess-main"]);
  assert.equal(ok.code, 0, ok.err);
  assert.match(ok.out, /--session sess-main/);
  const bad = run(["--since", "99999d", "--root", CLAUDE_ROOT, "--session", "no-such-session"]);
  assert.notEqual(bad.code, 0, "a selector that matched nothing must not report a clean $0");
  assert.match(bad.err, /matched none of the/);
});

test("an unreadable root is a failure to look, not an empty result", () => {
  const r = run(["--root", path.join(FIX, "no-such-root"), "--since", "24h"]);
  assert.notEqual(r.code, 0);
  assert.match(r.err, /is not readable/);
});

// --timeline runs with an empty HOME and no CLAUDE_CONFIG_DIR: it reads the named file and
// nothing else, so a build that ignored the flag and fell back to root discovery fails fast.
const TL_ROOT = path.join(FIX, "timeline"), TL_SUB = path.join(TL_ROOT, "-tmp-tl-proj", "sess-tl", "subagents");
const TL_PRICED = path.join(TL_SUB, "agent-t1.jsonl"), TL_UNPRICED = path.join(TL_SUB, "agent-t2.jsonl");
function runTl(args) {
  return run(args, { HOME: fs.mkdtempSync(path.join(TMP, "tl-home-")), CLAUDE_CONFIG_DIR: "" });
}
const tlRows = (out) => out.split("\n").filter((l) => /^ {2}#\d+ /.test(l));
const tlHeader = (out) => out.split("\n").find((l) => l.startsWith("TIMELINE ")) || "";

test("--timeline: one row per distinct model call, however many assistant lines a call spans", () => {
  const lines = fs.readFileSync(TL_PRICED, "utf8").split("\n").filter(Boolean).map((l) => JSON.parse(l)).filter((o) => o.type === "assistant");
  const calls = new Set(lines.map((o) => o.message.id)).size;
  assert.ok(lines.length > calls, "the fixture must split at least one call across several assistant lines");
  const r = runTl(["--timeline", TL_PRICED]);
  assert.equal(r.code, 0, r.err);
  assert.equal(tlRows(r.out).length, calls, `want ${calls} rows, one per call:\n${r.out}`);
  assert.match(tlHeader(r.out), new RegExp(`· ${calls} calls ·`));
  assert.match(r.out, /^data gaps: none$/m);
});

test("--timeline: the header's USD equals the default report's USD for the same file, and the rows sum to it", () => {
  const js = path.join(TMP, "tl-default.json");
  const d = run(["--since", "99999d", "--root", TL_ROOT, "--out", js]);
  assert.equal(d.code, 0, d.err);
  const want = JSON.parse(fs.readFileSync(js, "utf8")).runs.find((x) => x.file.includes("agent-t1"));
  assert.ok(want && want.usd > 0, "the default report must price the fixture run");
  const r = runTl(["--timeline", TL_PRICED]);
  const m = / · \$(\d+\.\d{4}) · /.exec(tlHeader(r.out));
  assert.ok(m, `no USD in the timeline header:\n${r.out}`);
  assert.equal(m[1], want.usd.toFixed(4), "one replay, one price table: the timeline must not re-price the run");
  const rows = tlRows(r.out).map((l) => +/ · \$(\d+\.\d{4}) · /.exec(l)[1]);
  assert.ok(Math.abs(rows.reduce((a, b) => a + b, 0) - want.usd) <= rows.length * 0.00005 + 1e-9, `rows sum ${rows} vs ${want.usd}`);
});

test("--timeline: an is_error tool result renders ERR on its call and counts in the header", () => {
  const r = runTl(["--timeline", TL_PRICED]);
  assert.equal(r.code, 0, r.err);
  const bad = tlRows(r.out).filter((l) => / ERR /.test(l));
  assert.equal(bad.length, 1, `exactly one row carries the failed Bash:\n${r.out}`);
  assert.match(bad[0], /Bash: go test \S+ -run TestAlpha \d+ch ERR/);
  assert.match(tlHeader(r.out), /· tool errors 1 ·/);
  assert.match(tlHeader(r.out), /· results >20KB 1 ·/, "the 25,000-char Read result is over 20 KB");
});

test("--timeline: an unpriced model renders n/a in the header and every row, never $0", () => {
  const r = runTl(["--timeline", TL_UNPRICED]);
  assert.equal(r.code, 0, r.err);
  assert.match(tlHeader(r.out), / · n\/a · /);
  assert.ok(tlRows(r.out).length > 0 && tlRows(r.out).every((l) => / · n\/a · /.test(l)), r.out);
  assert.doesNotMatch(r.out, /\$0\.0000/);
  assert.match(r.out, /^data gaps:.*UNPRICED calls.*unobtanium/m);
});

test("--timeline: a missing path prints UNREADABLE and exits non-zero, never an empty run", () => {
  const missing = path.join(TMP, "no-such-transcript.jsonl");
  const r = runTl(["--timeline", missing]);
  assert.notEqual(r.code, 0, "a file we failed to read must not exit clean");
  assert.match(r.out + r.err, new RegExp(`UNREADABLE — ${missing.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}: ENOENT`));
  assert.doesNotMatch(r.out, /NO CALLS/);
});

test("--timeline: a bare flag with no path exits 2 naming the missing path", () => {
  const r = runTl(["--timeline"]);
  assert.equal(r.code, 2, r.out + r.err);
  assert.match(r.err, /token-audit: --timeline wants a transcript path/);
});

test("--timeline: combined with --flight, --codex, --project or --since is refused", () => {
  for (const extra of [["--flight", "x"], ["--codex"], ["--project", "x"], ["--since", "1d"]]) {
    const r = runTl(["--timeline", TL_PRICED, ...extra]);
    assert.equal(r.code, 2, `${extra.join(" ")}: ${r.out + r.err}`);
    assert.match(r.err, /--timeline reads whole files: it takes no --flight, --codex, --project or --since/, `${extra.join(" ")}: ${r.err}`);
  }
});

const TL_BAD = path.join(TL_SUB, "agent-t3.jsonl"), TL_EMPTY = path.join(TL_SUB, "agent-t4.jsonl"), TL_NOMETA = path.join(TL_SUB, "agent-t5.jsonl");

test("--timeline: a transcript where every line is malformed JSON prints UNREADABLE and exits 1", () => {
  const r = runTl(["--timeline", TL_BAD]);
  assert.equal(r.code, 1, r.out + r.err);
  assert.match(r.out, new RegExp(`^UNREADABLE — ${TL_BAD.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}: `, "m"));
  assert.doesNotMatch(r.out, /NO CALLS/);
});

test("--timeline: an empty transcript file prints NO CALLS and exits 0", () => {
  const r = runTl(["--timeline", TL_EMPTY]);
  assert.equal(r.code, 0, r.out + r.err);
  assert.match(r.out, new RegExp(`^NO CALLS — ${TL_EMPTY.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}$`, "m"));
});

test("--timeline: a transcript with no .meta.json beside it names the agent type unknown", () => {
  const r = runTl(["--timeline", TL_NOMETA]);
  assert.equal(r.code, 0, r.out + r.err);
  assert.match(tlHeader(r.out), /^TIMELINE agent \(type unknown: no \.meta\.json\) /);
});

test.after(() => fs.rmSync(TMP, { recursive: true, force: true }));

// A copy of the fixture root where a1 writes only to the 1-hour cache, c3 writes one call's
// context there and the rest to 5 minutes, and b2 stays 5-minute: the TTL label per agent.
function ttlRoot() {
  const root = fs.mkdtempSync(path.join(TMP, "ttl-"));
  fs.cpSync(CLAUDE_ROOT, root, { recursive: true });
  const sub = path.join(root, "-tmp-demo-proj", "sess-main", "subagents");
  const to1h = /"ephemeral_5m_input_tokens":(\d+),"ephemeral_1h_input_tokens":0/;
  const a1 = path.join(sub, "agent-a1.jsonl"), c3 = path.join(sub, "agent-c3.jsonl");
  fs.writeFileSync(a1, fs.readFileSync(a1, "utf8").replace(new RegExp(to1h.source, "g"), '"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":$1'));
  fs.writeFileSync(c3, fs.readFileSync(c3, "utf8").replace(to1h, '"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":$1'));
  return root;
}

test("--flight: each agent row names its cache TTL — 5m, 1h, or the 1-hour share when mixed", () => {
  const md = path.join(TMP, "ttl-flight.md");
  const r = run(["--flight", flightDir("flight"), "--root", ttlRoot(), "--codex-root", CODEX_ROOT, "--metrics-out", md]);
  assert.equal(r.code, 0, r.err);
  const text = fs.readFileSync(md, "utf8"), row = (id) => text.split("\n").find((l) => l.startsWith(`| ${id} |`)) || "";
  assert.match(text, /^\| task \|.*\| ttl \|/m, "the per-agent table needs a ttl column");
  assert.match(row("1-a"), /\| 1h \|/, "a1 wrote only to the 1-hour cache");
  assert.match(row("1-d"), /\| 5m \|/, "b2 wrote only to the 5-minute cache");
  assert.match(row("1-e"), /\| 1h \d+% \|/, "c3 mixed both: the 1-hour share");
});

test("default report: agent groups and single runs carry the cache TTL label", () => {
  const r = run(["--since", "99999d", "--root", ttlRoot()]);
  assert.equal(r.code, 0, r.err);
  const groups = (r.out.split("AGENT GROUPS")[1] || "").split("\n== ")[0], runs = (r.out.split("SINGLE RUNS")[1] || "").split("\n== ")[0];
  assert.match(groups, /\bttl\b/, "the group header names the ttl column");
  assert.match(groups.split("\n").find((l) => / executor · /.test(l)) || "", /1h \d+%/, "the executor group mixes a1's 1h with b2's 5m");
  assert.match(runs.split("\n").find((l) => /task 1-a/.test(l)) || "", /ttl 1h\b/, "a1's run line says 1h");
  assert.match(runs.split("\n").find((l) => /task 1-b/.test(l)) || "", /ttl 5m\b/, "b2's run line says 5m");
});

// A minimal fixture root where each session's single call writes near-all-5m or
// near-all-1h: a naive `Math.round` renders 0%/100%, which reads as "not mixed" —
// the clamp keeps a mixed run visibly mixed.
function clampRoot() {
  const root = fs.mkdtempSync(path.join(TMP, "clamp-"));
  const dir = path.join(root, "-tmp-clamp-proj");
  fs.mkdirSync(dir, { recursive: true });
  const usage = (cw5, cw1) => JSON.stringify({
    type: "assistant", timestamp: "2026-09-20T09:00:00.000Z", cwd: "/tmp/clamp-proj",
    requestId: `req-${cw5}-${cw1}`, effort: "medium",
    message: {
      id: `msg-${cw5}-${cw1}`, model: "claude-sonnet-5", content: [{ type: "text", text: "step" }],
      usage: {
        input_tokens: 10, output_tokens: 10, cache_read_input_tokens: 0,
        cache_creation_input_tokens: cw5 + cw1,
        cache_creation: { ephemeral_5m_input_tokens: cw5, ephemeral_1h_input_tokens: cw1 },
      },
    },
  });
  fs.writeFileSync(
    path.join(dir, "sess-lo.jsonl"),
    '{"type":"custom-title","customTitle":"clamp lo chat","cwd":"/tmp/clamp-proj"}\n' + usage(999, 1) + "\n",
  );
  fs.writeFileSync(
    path.join(dir, "sess-hi.jsonl"),
    '{"type":"custom-title","customTitle":"clamp hi chat","cwd":"/tmp/clamp-proj"}\n' + usage(1, 999) + "\n",
  );
  return root;
}

test("default report: a mixed run's 1-hour share clamps to 1..99, never 0% or 100%", () => {
  const r = run(["--since", "99999d", "--root", clampRoot()]);
  assert.equal(r.code, 0, r.err);
  const runs = (r.out.split("SINGLE RUNS")[1] || "").split("\n== ")[0];
  const lo = runs.split("\n").find((l) => /clamp lo chat/.test(l)) || "";
  const hi = runs.split("\n").find((l) => /clamp hi chat/.test(l)) || "";
  assert.match(lo, /ttl 1h 1%/, `near-all-5m run rounds to 0% unclamped: ${JSON.stringify(lo)}`);
  assert.match(hi, /ttl 1h 99%/, `near-all-1h run rounds to 100% unclamped: ${JSON.stringify(hi)}`);
  assert.doesNotMatch(lo, /ttl 1h 0%/);
  assert.doesNotMatch(hi, /ttl 1h 100%/);
});

// ---------- pricing: every Claude token column at its published per-MTok rate
// cost = input×In + output×Out + ephemeral_5m×W5m + ephemeral_1h×W1h + cache_read×Hit, ÷1e6,
// once per message.id, at that response's own model. Each case is one synthetic root; the
// --out JSON's usd/tok carry the five billing columns (in, out, cw5, cw1, cr) for the scan.
function priceRoot(name, sessions) {
  const root = fs.mkdtempSync(path.join(TMP, `price-${name}-`)), dir = path.join(root, "-tmp-price-proj");
  fs.mkdirSync(dir, { recursive: true });
  for (const [sid, calls] of Object.entries(sessions)) {
    const lines = [{ type: "custom-title", customTitle: `price ${sid}`, cwd: "/tmp/price-proj" }];
    calls.forEach((c, i) => {
      const blocks = c.blocks || [{ type: "text", text: "step" }], reqs = c.reqs || blocks.map(() => `req-${c.id}`);
      blocks.forEach((b, k) => lines.push({ type: "assistant", timestamp: `2026-09-20T09:0${i}:00.000Z`, cwd: "/tmp/price-proj", requestId: reqs[k], effort: "medium",
        message: { id: c.id, model: c.model, content: [b], usage: c.usage } }));
    });
    fs.writeFileSync(path.join(dir, `${sid}.jsonl`), lines.map((l) => JSON.stringify(l)).join("\n") + "\n");
  }
  return root;
}
function priced(root) {
  const js = path.join(TMP, `price-${Math.random().toString(36).slice(2)}.json`);
  const r = run(["--since", "99999d", "--root", root, "--out", js]);
  assert.equal(r.code, 0, r.err);
  return { ...r, j: JSON.parse(fs.readFileSync(js, "utf8")) };
}
const near = (got, want, what) => assert.ok(Math.abs(got - want) < 1e-9, `${what}: got ${got}, want ${want}`);
const cols = (j, want) => { for (const k of ["in", "out", "cw5", "cw1", "cr"]) near(j.usd[k], want[k] ?? 0, `usd.${k}`); near(j.total, Object.values(want).reduce((a, b) => a + b, 0), "total"); };

test("pricing: an Opus 5.5 response writing both TTLs bills each column at its own rate, once per message.id", () => {
  const { j } = priced(priceRoot("opus55", { s: [{ id: "msg-o55", model: "claude-opus-5-5", blocks: [{ type: "thinking", thinking: "" }, { type: "text", text: "done" }],
    usage: { input_tokens: 1000, output_tokens: 2000, cache_read_input_tokens: 100000, cache_creation_input_tokens: 30000,
      cache_creation: { ephemeral_5m_input_tokens: 10000, ephemeral_1h_input_tokens: 20000 } } }] }));
  // 1000×$4 + 2000×$20 + 10000×$5 + 20000×$8 + 100000×$0.20 = $0.274, not doubled by the second block line
  cols(j, { in: 0.004, out: 0.04, cw5: 0.05, cw1: 0.16, cr: 0.02 });
  assert.equal(j.calls, 1);
  assert.equal(j.tok.cw5, 10000);
  assert.equal(j.tok.cw1, 20000);
});

test("pricing: a Fable 5.1 cache read bills $0.25/MTok, a Fable 5 read $1.00/MTok", () => {
  const usage = { input_tokens: 100, output_tokens: 500, cache_read_input_tokens: 1000000, cache_creation_input_tokens: 0, cache_creation: { ephemeral_5m_input_tokens: 0, ephemeral_1h_input_tokens: 0 } };
  cols(priced(priceRoot("fable51", { s: [{ id: "msg-f51", model: "claude-fable-5-1", usage }] })).j, { in: 0.001, out: 0.025, cr: 0.25 });
  cols(priced(priceRoot("fable5", { s: [{ id: "msg-f5", model: "claude-fable-5", usage }] })).j, { in: 0.001, out: 0.025, cr: 1.0 });
});

test("pricing: a cache write with no 5m/1h breakdown bills as 5-minute and the gaps line says so", () => {
  const { j, out } = priced(priceRoot("nosplit", { s: [{ id: "msg-h45", model: "claude-haiku-4-5-20251001",
    usage: { input_tokens: 200, output_tokens: 300, cache_read_input_tokens: 0, cache_creation_input_tokens: 40000 } }] }));
  // 200×$1 + 300×$5 + 40000×$1.25 = $0.0517
  cols(j, { in: 0.0002, out: 0.0015, cw5: 0.05 });
  assert.equal(j.tok.cw5, 40000);
  assert.equal(j.tok.cw1, 0);
  assert.match(out, /^data gaps:.*1 cache writes with no 5m\/1h split \(priced as 5m\)/m);
});

test("pricing: ephemeral_5m_input_tokens is read on its own, not derived from the total", () => {
  const { j, out } = priced(priceRoot("b5only", { s: [{ id: "msg-s46", model: "claude-sonnet-4-6",
    usage: { input_tokens: 10, output_tokens: 10, cache_read_input_tokens: 0, cache_creation: { ephemeral_5m_input_tokens: 8000, ephemeral_1h_input_tokens: 2000 } } }] }));
  // 10×$3 + 10×$15 + 8000×$3.75 + 2000×$6 = $0.04218; total minus 1h would drop the 5m $0.03
  cols(j, { in: 0.00003, out: 0.00015, cw5: 0.03, cw1: 0.012 });
  assert.doesNotMatch(out, /no 5m\/1h split/);
});

test("pricing: a response with zero top-level counts is priced from usage.iterations, never dropped as synthetic", () => {
  const { j, out } = priced(priceRoot("iters", { s: [{ id: "msg-it", model: "claude-opus-5-5",
    usage: { input_tokens: 0, output_tokens: 0, cache_read_input_tokens: 0, cache_creation_input_tokens: 0, cache_creation: { ephemeral_5m_input_tokens: 0, ephemeral_1h_input_tokens: 1045 },
      iterations: [{ type: "message", input_tokens: 2, output_tokens: 1098, cache_read_input_tokens: 453328, cache_creation_input_tokens: 1045, cache_creation: { ephemeral_5m_input_tokens: 0, ephemeral_1h_input_tokens: 1045 } }] } }] }));
  // 2×$4 + 1098×$20 + 1045×$8 + 453328×$0.20 = $0.1209936
  cols(j, { in: 0.000008, out: 0.02196, cw1: 0.00836, cr: 0.0906656 });
  assert.doesNotMatch(out, /synthetic/);
});

test("pricing: a response copied into a forked session's transcript is billed once", () => {
  const shared = { id: "msg-shared", model: "claude-sonnet-5", usage: { input_tokens: 1000, output_tokens: 100, cache_read_input_tokens: 0, cache_creation_input_tokens: 0 } };
  const own = (id, n) => ({ id, model: "claude-sonnet-5", usage: { input_tokens: n, output_tokens: 0, cache_read_input_tokens: 0, cache_creation_input_tokens: 0 } });
  const { j, out } = priced(priceRoot("fork", { "sess-a": [shared, own("msg-a", 2000)], "sess-b": [shared, own("msg-b", 3000)] }));
  // (1000 + 2000 + 3000)×$2 + 100×$10 = $0.013; counting the copy again would read $0.016
  cols(j, { in: 0.012, out: 0.001 });
  assert.equal(j.calls, 3);
  assert.match(out, /^data gaps:.*1 calls copied from another transcript/m);
});

test("pricing: content-block lines of one message.id are one call even when a line lacks its requestId", () => {
  const { j } = priced(priceRoot("noreq", { s: [{ id: "msg-h35", model: "claude-3-5-haiku-20241022", blocks: [{ type: "thinking", thinking: "" }, { type: "text", text: "a" }, { type: "text", text: "b" }],
    reqs: ["req-h35", "req-h35", undefined],
    usage: { input_tokens: 1000, output_tokens: 1000, cache_read_input_tokens: 10000, cache_creation_input_tokens: 1000, cache_creation: { ephemeral_5m_input_tokens: 500, ephemeral_1h_input_tokens: 500 } } }] }));
  // Haiku 3.5: 1000×$0.80 + 1000×$4 + 500×$1 + 500×$1.60 + 10000×$0.08 = $0.0069
  cols(j, { in: 0.0008, out: 0.004, cw5: 0.0005, cw1: 0.0008, cr: 0.0008 });
  assert.equal(j.calls, 1);
});

// ---------- prices: one `pfm price --json` per run. These runs clear TOKEN_AUDIT_PRICES and
// name a stand-in pfm, a shell script written into TMP.
function fakePfm(body) {
  const p = path.join(fs.mkdtempSync(path.join(TMP, "pfm-")), "pfm");
  fs.writeFileSync(p, `#!/bin/sh\n${body}\n`);
  fs.chmodSync(p, 0o755);
  return p;
}
const viaPfm = (bin) => ({ TOKEN_AUDIT_PRICES: "", TOKEN_AUDIT_PFM: bin });
// Every pricing mode over the fixtures rooted at `fix`; flight output goes to TMP, never the fixture.
const MODES = {
  report: (fix) => ["--since", "99999d", "--root", path.join(fix, "claude", "projects")],
  codex: (fix) => ["--codex", "--since", "99999d", "--codex-root", path.join(fix, "codex")],
  timeline: (fix) => ["--timeline", path.join(fix, "timeline", "-tmp-tl-proj", "sess-tl", "subagents", "agent-t2.jsonl")],
  flight: (fix, md) => ["--flight", path.join(fix, "flight"), "--root", path.join(fix, "claude", "projects"), "--codex-root", path.join(fix, "codex"), "--metrics-out", md, "--out", md.replace(/\.md$/, ".json")],
};

test("prices: a missing pfm exits 2 naming it, with nothing on stdout", () => {
  const bin = path.join(TMP, "no-such-pfm");
  const r = run(MODES.report(FIX), viaPfm(bin));
  assert.equal(r.code, 2, r.out);
  assert.equal(r.out, "");
  assert.equal(r.err, `token-audit: pfm not found (${bin}) — prices come from \`pfm price --json\`; install pfm or set TOKEN_AUDIT_PFM\n`);
});

test("prices: a pfm that exits non-zero stops the run with its exit code and trimmed stderr", () => {
  const bin = fakePfm(`echo "pfm: pfm.prices.json: row 3: unknown field \\"rate\\"" >&2; exit 3`);
  const r = run(MODES.report(FIX), viaPfm(bin));
  assert.equal(r.code, 2, r.out);
  assert.equal(r.out, "");
  assert.equal(r.err, `token-audit: \`${bin} price --json\` failed (exit 3): pfm: pfm.prices.json: row 3: unknown field "rate"\n`);
});

test("prices: a pfm that prints no JSON is an unreadable table", () => {
  const bin = fakePfm(`echo "price table"`);
  const r = run(MODES.report(FIX), viaPfm(bin));
  assert.equal(r.code, 2, r.out);
  assert.equal(r.out, "");
  assert.ok(r.err.startsWith(`token-audit: \`${bin} price --json\` returned an unreadable table: `), r.err);
});

test("prices: an unreadable TOKEN_AUDIT_PRICES exits 2 naming it", () => {
  const missing = path.join(TMP, "no-such-table.json");
  const r = run(MODES.report(FIX), { TOKEN_AUDIT_PRICES: missing, TOKEN_AUDIT_PFM: path.join(TMP, "no-such-pfm") });
  assert.equal(r.code, 2, r.out);
  assert.match(r.err, new RegExp(`^token-audit: TOKEN_AUDIT_PRICES ${missing}: .*ENOENT`));
});

test("prices: a bad flag is refused before pfm is looked for", () => {
  const r = run(["--since", "3x"], viaPfm(path.join(TMP, "no-such-pfm")));
  assert.equal(r.code, 2);
  assert.equal(r.err, "token-audit: --since wants a number plus h or d, e.g. 24h or 3d\n");
});

test("prices: every mode runs `pfm price --json` once, before any transcript is read", () => {
  for (const [mode, args] of Object.entries(MODES)) {
    const dir = fs.mkdtempSync(path.join(TMP, "spawn-")), fix = path.join(dir, "fix"), spawns = path.join(dir, "spawns");
    // The fixtures appear only when pfm runs: a transcript read before the spawn finds nothing.
    const bin = fakePfm(`echo x >> '${spawns}'; [ -e '${fix}' ] || ln -s '${FIX}' '${fix}'; cat '${PRICES}'`);
    const r = run(args(fix, path.join(dir, "metrics.md")), { ...viaPfm(bin), HOME: dir, CLAUDE_CONFIG_DIR: "" });
    assert.equal(r.code, 0, `${mode}: ${r.err}`);
    assert.doesNotMatch(r.out, /READ ERRORS|NO CALLS/, `${mode}: a transcript was read before pfm ran`);
    assert.equal(fs.existsSync(spawns) ? fs.readFileSync(spawns, "utf8") : "", "x\n", `${mode}: pfm must run exactly once`);
  }
});

test("prices: an active override leads every data-gaps line, naming its file and row count", () => {
  const doc = path.join(TMP, "override-table.json");
  fs.writeFileSync(doc, JSON.stringify({ ...JSON.parse(fs.readFileSync(PRICES, "utf8")), override: { path: "/cfg/pfm.prices.json", rows: 2 } }));
  const bin = fakePfm(`cat '${doc}'`);
  for (const [mode, args] of Object.entries(MODES)) {
    const md = path.join(fs.mkdtempSync(path.join(TMP, "override-")), "metrics.md");
    const r = run(args(FIX, md), { ...viaPfm(bin), HOME: path.dirname(md), CLAUDE_CONFIG_DIR: "" });
    assert.equal(r.code, 0, `${mode}: ${r.err}`);
    const gaps = (r.out + (fs.existsSync(md) ? fs.readFileSync(md, "utf8") : "")).split("\n").filter((l) => l.startsWith("data gaps:"));
    assert.ok(gaps.length, `${mode}: no data-gaps line in:\n${r.out}`);
    for (const g of gaps) assert.match(g, /^data gaps: price override active: 2 rows from \/cfg\/pfm\.prices\.json( · |$)/, `${mode}: ${g}`);
  }
});
