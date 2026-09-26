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

function run(args) {
  const r = spawnSync(process.execPath, [BIN, ...args], { encoding: "utf8", cwd: HERE, maxBuffer: 64 << 20 });
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
  assert.match(f.md, /data gaps:.*UNPRICED calls.*unobtanium/);
});

test("--flight: the report stays bounded and carries the gaps and cross-check lines", () => {
  const f = flight("flight");
  assert.ok(f.md.split("\n").length < 200, `report is ${f.md.split("\n").length} lines`);
  assert.match(f.md, /^data gaps: /m);
  assert.match(f.md, /^cross-check: /m);
});

test("--flight: the long-context premium is a per-model PRICING rate, not a flat constant", () => {
  const f = flight("flight");
  const m = /this flight reads \$([\d.]+) \(([\d.]+)x the headline\)/.exec(f.md);
  assert.ok(m, `no cross-check ratio in:\n${f.md}`);
  assert.ok(+m[2] > 1, `a run whose context passed 200K must lift the long-context number above 1.00x, got ${m[2]}x`);
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

test.after(() => fs.rmSync(TMP, { recursive: true, force: true }));
