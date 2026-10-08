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
import { fakePfm as fakePfmIn } from "./fake-pfm.mjs";
import { callCap } from "./lib/flight.mjs";

const HERE = path.dirname(fileURLToPath(import.meta.url));
const BIN = process.env.TOKEN_AUDIT_BIN || path.join(HERE, "token-audit.mjs");
const FIX = path.join(HERE, "fixtures");
const CLAUDE_ROOT = path.join(FIX, "claude", "projects");
const CODEX_ROOT = path.join(FIX, "codex");
const TMP = fs.mkdtempSync(path.join(os.tmpdir(), "token-audit-test-"));
// A saved synthetic `pfm model-cost --json --all` document keeps every test offline.
const PRICES = path.join(HERE, "fixtures/model-cost.json");

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

test("--flight: the call cap comes from the agent type name — executor 150, lander 200", () => {
  for (const [type, cap] of [["flights-mechanical-executor", 150], ["executor", 150], ["flights-foreman", 250], ["foreman", 250], ["flights-lander", 200], ["lander", 200]])
    assert.equal(callCap(type), cap, type);
  const f = flight("flight"), d = rowOf(f.json, "1-d"), e = rowOf(f.json, "1-e");
  assert.equal(d.calls, 85);
  assert.equal(d.overCap, false, "85 calls by an executor is under the 150 cap");
  assert.equal(e.overCap, false, "a lander is capped at 200, so 2 calls is not over");
  assert.match(f.md, /\| 85 \|.*\| ok\/150 \|/);
});

// The over-cap path needs runs past the caps. They are built in a temp copy of the fixtures,
// never in fixtures/ itself: other tests count the shared transcripts' calls.
function overCapFlight() {
  const root = path.join(fs.mkdtempSync(path.join(TMP, "overcap-")), "projects");
  fs.cpSync(CLAUDE_ROOT, root, { recursive: true });
  const dir = fs.mkdtempSync(path.join(TMP, "overcap-flight-"));
  fs.cpSync(flightDir("flight"), dir, { recursive: true });
  const sub = path.join(root, "-tmp-demo-proj", "sess-main", "subagents");
  const extend = (agent, n, model, start) => {
    const at = (i) => new Date(Date.parse(start) + i * 1000).toISOString();
    const lines = [];
    for (let i = 0; i < n; i++) {
      lines.push({ type: "assistant", timestamp: at(i), cwd: "/tmp/demo-proj", requestId: `ocr-${agent}-${i}`, message: { id: `ocm-${agent}-${i}`, model,
        content: [{ type: "tool_use", id: `oct-${agent}-${i}`, name: "Bash", input: { command: "true" } }],
        usage: { input_tokens: 10, output_tokens: 20, cache_read_input_tokens: 9000, cache_creation_input_tokens: 0, cache_creation: { ephemeral_5m_input_tokens: 0, ephemeral_1h_input_tokens: 0 } } } });
      lines.push({ type: "user", timestamp: at(i), cwd: "/tmp/demo-proj", message: { content: [{ type: "tool_result", tool_use_id: `oct-${agent}-${i}`, content: "done", is_error: false }] } });
    }
    fs.appendFileSync(path.join(sub, `agent-${agent}.jsonl`), lines.map((l) => JSON.stringify(l)).join("\n") + "\n");
  };
  extend("b2", 70, "claude-sonnet-5", "2026-09-20T10:29:00.000Z"); // executor 1-d: 85 + 70 = 155, over its 150
  extend("c3", 170, "claude-unobtanium-9", "2026-09-20T09:06:00.000Z"); // lander 1-e: 2 + 170 = 172, under its 200
  return { root, dir };
}

test("--flight: an executor past 150 calls reads OVER 150, a lander at 172 reads ok/200, and the totals count one over", () => {
  const { root, dir } = overCapFlight();
  // only the copied root: the shared fixture root holds the same agent ids with their original calls
  const md = path.join(dir, "metrics.md"), js = path.join(dir, "report.json");
  const r = run(["--flight", dir, "--root", root, "--codex-root", CODEX_ROOT, "--metrics-out", md, "--out", js]);
  assert.equal(r.code, 0, r.err);
  const f = { md: fs.readFileSync(md, "utf8"), json: JSON.parse(fs.readFileSync(js, "utf8")) };
  const d = rowOf(f.json, "1-d"), e = rowOf(f.json, "1-e");
  assert.equal(d.calls, 155);
  assert.equal(d.overCap, true, "155 calls by an executor is over the 150 cap");
  assert.equal(e.calls, 172);
  assert.equal(e.overCap, false, "172 calls by a lander is under the 200 cap");
  assert.match(f.md, /\| 155 \|.*\| OVER 150 \|/);
  assert.match(f.md, /\| 172 \|.*\| ok\/200 \|/);
  assert.match(f.md, /over cap 1 of \d+/);
});

test("--flight: an unpriced model renders n/a with its tokens still counted, never $0", () => {
  const f = flight("flight");
  const e = rowOf(f.json, "1-e");
  assert.equal(e.usd, null, "an unpriced run's dollars are unknown, not zero");
  assert.ok(e.tok.in + e.tok.cr + e.tok.out > 0, "its tokens must still be counted");
  assert.match(f.md, /\| n\/a \|/, "the $ column must read n/a");
  assert.doesNotMatch(f.md.split("## totals")[0], /\| \$0\.00 \|/, "no row may render an unknown price as $0.00");
  assert.match(f.md, /data gaps:.*UNPRICED calls \{[^}]*unobtanium[^}]*\} — tokens counted, dollars "n\/a"; check exact IDs and available rates with pfm model-cost --json --all/);
});

test("--flight: the report stays bounded and carries the gaps and cross-check lines", () => {
  const f = flight("flight");
  assert.ok(f.md.split("\n").length < 200, `report is ${f.md.split("\n").length} lines`);
  assert.match(f.md, /^data gaps: /m);
  assert.match(f.md, /^cross-check: /m);
});

test("--flight: the long-context premium is a per-model price-table rate, not a flat constant", () => {
  const f = flight("flight");
  const m = /per-model rate in the pfm model-cost table — an estimate\) this flight reads \$([\d.]+) \(([\d.]+)x the headline\)/.exec(f.md);
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
  assert.equal(bad.err.trim(), "token-audit: --session no-such-session matched none of the 5 transcripts in this window; pass a session-id prefix as it appears in the transcript path");
  // a selector that matched a transcript holding no call yet names its match, never "matched none"
  const quiet = run(["--since", "99999d", "--root", priceRoot("quiet", { "sess-q": [{ type: "user", timestamp: "2026-09-20T09:00:00.000Z", message: { role: "user", content: "hi" } }] }), "--session", "sess-q"]);
  assert.notEqual(quiet.code, 0, "a selector whose transcripts hold no call must not report a clean $0");
  assert.equal(quiet.err.trim(), "token-audit: --session sess-q matched 1 transcripts in this window but read no call from them");
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
    const lines = [];
    calls.forEach((c, i) => {
      if (c.type && c.type !== "assistant") { lines.push(c); return; }
      const blocks = c.blocks || [{ type: "text", text: "step" }], reqs = c.reqs || blocks.map(() => `req-${c.id}`);
      blocks.forEach((b, k) => lines.push({ type: "assistant", timestamp: c.timestamp || `2026-09-20T09:0${i}:00.000Z`, cwd: "/tmp/price-proj", requestId: reqs[k], effort: "medium",
        uuid: c.uuid || `${sid}-${i}-${k}`, sessionId: c.sessionId || sid, session_id: c.session_id, forkedFrom: c.forkedFrom,
        message: { id: c.id, model: c.model, content: [b], usage: c.usage } }));
    });
    lines.push({ type: "custom-title", customTitle: `price ${sid}`, cwd: "/tmp/price-proj" });
    fs.writeFileSync(path.join(dir, `${sid}.jsonl`), lines.map((l) => JSON.stringify(l)).join("\n") + "\n");
  }
  return root;
}
function priced(root, extra = []) {
  const js = path.join(TMP, `price-${Math.random().toString(36).slice(2)}.json`);
  const r = run(["--since", "99999d", "--root", root, "--out", js, ...extra]);
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

function forkPriceRoot(copiesOnly = false, forked = true) {
  const shared = { id: "msg-shared", uuid: "uuid-shared", session_id: "sess-b", timestamp: "2026-09-20T09:00:00.000Z",
    model: "claude-sonnet-5", usage: { input_tokens: 1000, output_tokens: 100, cache_read_input_tokens: 0, cache_creation_input_tokens: 0 } };
  const own = (id, n, extra = {}) => ({ id, model: "claude-sonnet-5", usage: { input_tokens: n, output_tokens: 0, cache_read_input_tokens: 0, cache_creation_input_tokens: 0 }, ...extra });
  const marker = { type: "history-suppression", sessionId: "sess-a", cause: "fork_inherit", ts: "2026-09-20T09:00:00.000Z" };
  // As Claude Code writes a fork: the inherited line carries forkedFrom, and the fork's own live call still names the origin in session_id.
  const inherited = forked ? { ...shared, forkedFrom: { sessionId: "sess-b", messageUuid: "uuid-shared" } } : shared;
  return priceRoot("fork", { "sess-a": [...(forked ? [marker] : []), inherited, own("msg-a", 2000, forked ? { session_id: "sess-b" } : {})], "sess-b": [shared, own("msg-b", 3000)],
    ...(copiesOnly ? { "sess-c": [shared] } : {}) });
}

test("pricing: a response copied into a forked session's transcript is billed once, to its origin even when the fork sorts first", () => {
  const { j, out } = priced(forkPriceRoot());
  // (1000 + 2000 + 3000)×$2 + 100×$10 = $0.013; counting the copy again would read $0.016
  cols(j, { in: 0.012, out: 0.001 });
  assert.equal(j.calls, 3);
  near(j.runs.find((r) => r.sid === "sess-b").usd, 0.009, "origin spend");
  near(j.runs.find((r) => r.sid === "sess-a").usd, 0.004, "fork spend");
  assert.equal(out.split("\n").find((l) => l.startsWith("data gaps:")),
    "data gaps: 1 calls copied from another transcript (forked/resumed session) — billed once within the ownership scan (named origin, else first unmarked holder in path order)");
});

test("pricing: a resumed copy is identified by the same call in its named origin, without a fork marker", () => {
  const { j } = priced(forkPriceRoot(false, false));
  near(j.runs.find((r) => r.sid === "sess-b").usd, 0.009, "origin spend");
  near(j.runs.find((r) => r.sid === "sess-a").usd, 0.004, "resumed spend");
  assert.equal(j.scan.copiedCalls, 1);
});

test("pricing: a call two transcripts hold with no origin evidence is billed once, to the first in path order", () => {
  // Forked sub-agents copy their shared prefix with neither session_id nor forkedFrom; a message.id is one API response.
  const call = { id: "msg-dup", sessionId: "sess-p", model: "claude-sonnet-5", usage: { input_tokens: 1000, output_tokens: 0, cache_read_input_tokens: 0, cache_creation_input_tokens: 0 } };
  const { j } = priced(priceRoot("dup", { "agent-y": [call], "agent-x": [call] }));
  cols(j, { in: 0.002 });
  assert.equal(j.calls, 1);
  assert.equal(j.scan.copiedCalls, 1);
  assert.deepEqual(j.runs.map((r) => r.sid), ["agent-x"]);
});

test("pricing: a live session_id mismatch alone never marks a copy", () => {
  const { j } = priced(priceRoot("live", { "sess-live": [{ id: "msg-live", session_id: "unrelated-session", model: "claude-sonnet-5", usage: { input_tokens: 1000, output_tokens: 0 } }] }));
  cols(j, { in: 0.002 });
  assert.equal(j.calls, 1);
  assert.equal(j.scan.copiedCalls, 0);
});

test("--session: totals and scan count only the selected session", () => {
  const root = priceRoot("two", {
    "sess-a": [{ id: "msg-a", model: "claude-sonnet-5", usage: { input_tokens: 2000000, output_tokens: 0, cache_read_input_tokens: 0, cache_creation_input_tokens: 0 } }],
    "sess-b": [{ id: "msg-b", model: "claude-sonnet-5", usage: { input_tokens: 3000000, output_tokens: 0, cache_read_input_tokens: 0, cache_creation_input_tokens: 0 } }],
  });
  const { j, out } = priced(root, ["--session", "sess-a"]);
  assert.equal(j.total, 4.0);
  assert.equal(j.usd.in, 4.0);
  assert.equal(j.calls, 1);
  assert.deepEqual(j.runs.map((r) => r.sid), ["sess-a"]);
  assert.equal(j.scan.files, 1);
  assert.match(out, /== 1 · TOTAL \$4\.00/);
  assert.equal(out.split("\n").find((l) => l.startsWith("data gaps:")),
    "data gaps: --session sess-a: 1 transcripts outside that session not replayed; ownership read 2 transcripts");
});

test("--session: a transcript holding only copies exits zero and names their origin", () => {
  const { j, out } = priced(forkPriceRoot(true), ["--session", "sess-c"]);
  assert.equal(j.runs.length, 0);
  assert.equal(j.scan.copiesOnly.length, 1);
  assert.equal(j.scan.copiesOnly[0].sid, "sess-c");
  assert.equal(out.split("\n").find((l) => l.startsWith("data gaps:")),
    "data gaps: 1 calls copied from another transcript (forked/resumed session) — billed once within the ownership scan (named origin, else first unmarked holder in path order) · 1 transcripts hold only copied calls: sess-c · --session sess-c: matched only transcripts holding copied calls (billed to their origin): sess-c");
});

test("--timeline: a fork or resumed file prices only its own call, like the default report", () => {
  for (const forked of [true, false]) {
    const root = forkPriceRoot(false, forked), report = priced(root), file = path.join(root, "-tmp-price-proj", "sess-a.jsonl");
    const r = runTl(["--timeline", file]);
    assert.equal(r.code, 0, r.err);
    assert.match(tlHeader(r.out), /1 calls .*\$0\.0040/);
    assert.equal(tlRows(r.out).length, 1);
    near(Number(tlHeader(r.out).match(/\$([0-9.]+)/)[1]), report.j.runs.find((x) => x.sid === "sess-a").usd, "timeline equals fork run");
  }
});

test("--timeline: a forkedFrom mark proves an inherited call even when its origin file is absent", () => {
  const root = forkPriceRoot(), dir = path.join(root, "-tmp-price-proj");
  fs.renameSync(path.join(dir, "sess-b.jsonl"), path.join(dir, "origin.hidden"));
  const r = runTl(["--timeline", path.join(dir, "sess-a.jsonl")]);
  assert.equal(r.code, 0, r.err);
  assert.match(tlHeader(r.out), /1 calls .*\$0\.0040/);
});

test("--timeline: copied-call and duplicate discovery gaps belong to each named file", () => {
  const root = forkPriceRoot(), dir = path.join(root, "-tmp-price-proj");
  const first = priceRoot("duplicate-discovery", { s: [{ id: "msg-first", model: "claude-sonnet-5", usage: { input_tokens: 1000, output_tokens: 0 } }] });
  const second = priceRoot("clean-discovery", { s: [{ id: "msg-second", model: "claude-sonnet-5", usage: { input_tokens: 1000, output_tokens: 0 } }] });
  const rel = path.join("-tmp-price-proj", "s.jsonl");
  // ownershipFiles deduplicates the final three path components.
  fs.cpSync(path.join(first, "-tmp-price-proj"), path.join(first, "alias", path.basename(first), "-tmp-price-proj"), { recursive: true });
  for (const [files, gap] of [
    [[path.join(dir, "sess-a.jsonl"), path.join(dir, "sess-b.jsonl")],
      /^data gaps: 1 calls copied from another transcript \(forked\/resumed session\) — billed once within the ownership scan \(named origin, else first unmarked holder in path order\)$/],
    [[path.join(first, rel), path.join(second, rel)], /^data gaps: 1 duplicate files skipped$/],
  ]) {
    const r = runTl(files.flatMap((file) => ["--timeline", file]));
    assert.equal(r.code, 0, r.err);
    const gaps = r.out.split("\n").filter((l) => l.startsWith("data gaps:"));
    assert.equal(gaps.length, 2);
    assert.match(gaps[0], gap);
    assert.equal(gaps[1], "data gaps: none");
  }
});

test("pricing: streamed lines without message.id share the namespaced requestId call key", () => {
  const { j } = priced(priceRoot("request", { s: [{ model: "claude-sonnet-5", blocks: [{ type: "thinking", thinking: "" }, { type: "text", text: "step" }],
    reqs: ["req-s1", "req-s1"], usage: { input_tokens: 1000, output_tokens: 0, cache_read_input_tokens: 0, cache_creation_input_tokens: 0 } }] }));
  assert.equal(j.calls, 1);
  cols(j, { in: 0.002 });
});

test("pricing: a requestId key never collides with a message.id string", () => {
  const { j } = priced(priceRoot("request-namespace", { s: [
    { reqs: ["req-s1"], model: "claude-sonnet-5", usage: { input_tokens: 1000, output_tokens: 0 } },
    { id: "requestId:req-s1", model: "claude-sonnet-5", usage: { input_tokens: 2000, output_tokens: 0 } },
  ] }));
  assert.equal(j.calls, 2);
  cols(j, { in: 0.006 });
});

test("pricing: content-block lines of one message.id are one call even when a line lacks its requestId", () => {
  const { j } = priced(priceRoot("noreq", { s: [{ id: "msg-h35", model: "claude-3-5-haiku-20241022", blocks: [{ type: "thinking", thinking: "" }, { type: "text", text: "a" }, { type: "text", text: "b" }],
    reqs: ["req-h35", "req-h35", undefined],
    usage: { input_tokens: 1000, output_tokens: 1000, cache_read_input_tokens: 10000, cache_creation_input_tokens: 1000, cache_creation: { ephemeral_5m_input_tokens: 500, ephemeral_1h_input_tokens: 500 } } }] }));
  // Haiku 3.5: 1000×$0.80 + 1000×$4 + 500×$1 + 500×$1.60 + 10000×$0.08 = $0.0069
  cols(j, { in: 0.0008, out: 0.004, cw5: 0.0005, cw1: 0.0008, cr: 0.0008 });
  assert.equal(j.calls, 1);
});

// ---------- prices: one `pfm model-cost --json --all` per run. These runs clear TOKEN_AUDIT_PRICES and
// name a stand-in pfm, a shell script written into TMP.
const fakePfm = (body) => fakePfmIn(TMP, body);
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
  assert.equal(r.err, `token-audit: pfm not found (${bin}) — prices come from \`pfm model-cost --json --all\`; install pfm or set TOKEN_AUDIT_PFM\n`);
});

test("prices: a pfm that exits non-zero stops the run with its exit code and trimmed stderr", () => {
  const bin = fakePfm(`echo "pfm model-cost: pricing source HTTP 503: \\"rate\\"" >&2; exit 3`);
  const r = run(MODES.report(FIX), viaPfm(bin));
  assert.equal(r.code, 2, r.out);
  assert.equal(r.out, "");
  assert.equal(r.err, `token-audit: \`${bin} model-cost --json --all\` failed (exit 3): pfm model-cost: pricing source HTTP 503: "rate"\n`);
});

test("prices: a pfm that prints no JSON is an unreadable table", () => {
  const bin = fakePfm(`echo "price table"`);
  const r = run(MODES.report(FIX), viaPfm(bin));
  assert.equal(r.code, 2, r.out);
  assert.equal(r.out, "");
  assert.ok(r.err.startsWith(`token-audit: \`${bin} model-cost --json --all\` returned an unreadable table: `), r.err);
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

test("prices: every mode runs `pfm model-cost --json --all` once, before any transcript is read", () => {
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

test("prices: a refresh pfm could not complete is named on the data-gaps line, never a silent stale table", () => {
  const doc = path.join(TMP, "refresh-failed.json");
  fs.writeFileSync(doc, JSON.stringify({ ...JSON.parse(fs.readFileSync(PRICES, "utf8")), refresh: { status: "failed", error: "openai pricing page: HTTP 503" } }));
  const bin = fakePfm(`cat '${doc}'`);
  const r = run(MODES.report(FIX), viaPfm(bin));
  assert.equal(r.code, 0, r.err);
  assert.match(r.out, /^pricing: current API list prices; .*table fetched 2026-01-01T00:00:00Z from anthropic, openai; not historical billing$/m);
  assert.match(r.out, /^data gaps: price refresh failed \(openai pricing page: HTTP 503\) — served the table fetched 2026-01-01T00:00:00Z/m);
});

test("prices: an active override leads every data-gaps line, naming its file and row count", () => {
  const doc = path.join(TMP, "override-table.json");
  fs.writeFileSync(doc, JSON.stringify({ ...JSON.parse(fs.readFileSync(PRICES, "utf8")), override: { path: doc, rows: 8 } }));
  const bin = fakePfm("exit 9");
  for (const [mode, args] of Object.entries(MODES)) {
    const md = path.join(fs.mkdtempSync(path.join(TMP, "override-")), "metrics.md");
    const r = run(args(FIX, md), { TOKEN_AUDIT_PRICES: doc, TOKEN_AUDIT_PFM: bin, HOME: path.dirname(md), CLAUDE_CONFIG_DIR: "" });
    assert.equal(r.code, 0, `${mode}: ${r.err}`);
    const gaps = (r.out + (fs.existsSync(md) ? fs.readFileSync(md, "utf8") : "")).split("\n").filter((l) => l.startsWith("data gaps:"));
    assert.ok(gaps.length, `${mode}: no data-gaps line in:\n${r.out}`);
    for (const g of gaps) assert.ok(g.startsWith(`data gaps: price override active: 8 rows from ${doc}`), `${mode}: ${g}`);
  }
});


test("--timeline: ownership across project directories matches aggregate within --root", () => {
  const usage = { input_tokens: 1000, output_tokens: 0 };
  const root = priceRoot("ownership-domain", { "sess-a": [{ id: "shared-domain", model: "claude-sonnet-5", usage }],
    "sess-b": [{ id: "shared-domain", model: "claude-sonnet-5", usage }, { id: "own-domain", model: "claude-sonnet-5", usage }] });
  const later = path.join(root, "-tmp-z-other-proj", "sess-b.jsonl");
  fs.mkdirSync(path.dirname(later));
  fs.renameSync(path.join(root, "-tmp-price-proj", "sess-b.jsonl"), later);
  const report = priced(root), want = report.j.runs.find((x) => x.sid === "sess-b");
  for (const scope of [[], ["--root", root]]) {
    const r = runTl(["--timeline", later, ...scope]);
    assert.equal(r.code, 0, r.err);
    near(Number(tlHeader(r.out).match(/\$([0-9.]+)/)[1]), want.usd, "shared ownership domain");
  }
});

test("pricing: missing response identity exposes per-record billing uncertainty", () => {
  const root = priceRoot("identity-unknown", { "sess-a": [{ model: "claude-sonnet-5", usage: { input_tokens: 1000, output_tokens: 0 },
    reqs: [null, null], blocks: [{ type: "text", text: "a" }, { type: "text", text: "b" }] }] });
  const { out } = priced(root);
  assert.match(out, /2 assistant records without message.id or requestId.*billing may include streamed copies/);
});


test("pricing: published prompt tiers price every cache column at the request context", () => {
  const doc = JSON.parse(fs.readFileSync(PRICES, "utf8"));
  doc.rows.push({ key: "claude-haiku-5-5", engine: "claude", in: .1, out: .5, hit: .01, w5m: .125, w1h: .2, long: { above: 100000, in: .5, out: 2.5, hit: .05, w5m: .625, w1h: 1 } });
  const saved = path.join(TMP, "tier-table.json"), out = path.join(TMP, "tier-report.json");
  fs.writeFileSync(saved, JSON.stringify(doc));
  const root = priceRoot("live-tiers", { s: [{ id: "tier", model: "claude-haiku-5-5", usage: { input_tokens: 1, output_tokens: 2, cache_read_input_tokens: 100000, cache_creation_input_tokens: 3, cache_creation: { ephemeral_5m_input_tokens: 1, ephemeral_1h_input_tokens: 2 } } }] });
  const r = run(["--root", root, "--since", "99999d", "--out", out], { TOKEN_AUDIT_PRICES: saved });
  assert.equal(r.code, 0, r.err);
  cols(JSON.parse(fs.readFileSync(out, "utf8")), { in: .0000005, out: .000005, cr: .005, cw5: .000000625, cw1: .000002 });
});

test("pricing: a published model with an unavailable used rate keeps tokens and renders n/a", () => {
  const doc = JSON.parse(fs.readFileSync(PRICES, "utf8")); delete doc.rows.find((row) => row.key === "claude-sonnet-5").hit;
  const saved = path.join(TMP, "missing-cache-table.json"), out = path.join(TMP, "missing-cache-report.json");
  fs.writeFileSync(saved, JSON.stringify(doc));
  const root = priceRoot("missing-cache", { s: [{ id: "missing", model: "claude-sonnet-5", usage: { input_tokens: 1, output_tokens: 2, cache_read_input_tokens: 100 } }] });
  const r = run(["--root", root, "--since", "99999d", "--out", out], { TOKEN_AUDIT_PRICES: saved });
  assert.equal(r.code, 0, r.err);
  const j = JSON.parse(fs.readFileSync(out, "utf8"));
  assert.equal(j.runs[0].unpriced, true);
  assert.equal(j.runs[0].usd, null);
  assert.equal(j.tok.cr, 100);
  assert.match(r.out, /UNPRICED calls.*claude-sonnet-5/);
  assert.match(r.out, /n\/a/);
});


test("pricing: unavailable read prices render tool-carry estimates n/a while uncached calls stay priced", () => {
  const doc = JSON.parse(fs.readFileSync(PRICES, "utf8")); delete doc.rows.find((row) => row.key === "claude-sonnet-5").hit;
  const saved = path.join(TMP, "missing-tool-read-table.json"), out = path.join(TMP, "missing-tool-read-report.json");
  fs.writeFileSync(saved, JSON.stringify(doc));
  const root = priceRoot("missing-tool-read", { s: [
    { id: "tool-before", model: "claude-sonnet-5", blocks: [{ type: "tool_use", id: "read-big", name: "Read", input: { file_path: "/tmp/demo-proj/large.go" } }], usage: { input_tokens: 1000, output_tokens: 10 } },
    { type: "user", timestamp: "2026-09-20T09:00:01.000Z", message: { role: "user", content: [{ type: "tool_result", tool_use_id: "read-big", content: "x".repeat(40000) }] } },
    { id: "tool-after", model: "claude-sonnet-5", usage: { input_tokens: 30000, output_tokens: 10 } },
  ] });
  const r = run(["--root", root, "--since", "99999d", "--out", out], { TOKEN_AUDIT_PRICES: saved });
  assert.equal(r.code, 0, r.err);
  const j = JSON.parse(fs.readFileSync(out, "utf8"));
  assert.equal(j.runs[0].unpriced, false);
  assert.ok(j.landings.length, "the large tool result must produce a carry estimate");
  assert.equal(j.landings[0].usd, null);
  assert.match(r.out, /n\/a.*tok carried.*Read/);
  assert.match(r.out, /tool-carry read-price estimate unavailable.*claude-sonnet-5/);
});

test("pricing: exported page and brief rows preserve unavailable model dollars as null", () => {
  const view = path.join(TMP, "unpriced-view.json"), briefs = path.join(TMP, "unpriced-briefs.json");
  const r = run(["--root", CLAUDE_ROOT, "--since", "99999d", "--view", view, "--briefs", briefs]);
  assert.equal(r.code, 0, r.err);
  for (const rows of [JSON.parse(fs.readFileSync(view, "utf8")).projects.flatMap((p) => p.runs), JSON.parse(fs.readFileSync(briefs, "utf8"))]) {
    const unpriced = rows.find((row) => row.m.includes("unobtanium"));
    assert.ok(unpriced, "the unavailable fixture model must remain in both exported artifacts");
    assert.equal(unpriced.usd, null);
  }
});
