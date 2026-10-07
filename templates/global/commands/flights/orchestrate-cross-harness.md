---
name: flights:orchestrate-cross-harness
description: 'Child foremen on another engine — /flights:orchestrate-cross-harness {requirements.md or work} [engine claude|codex] [worktree {path}] [commit]: this chat acts as the root flights-foreman, one chat seat per child unit instead of a sub-agent, default engine codex. /flights:spec → here → child foreman seats, flights-lander.'
argument-hint: <requirements.md | work> [engine <claude|codex>] [worktree <path>] [commit]
---

# Orchestrate, cross-harness — child foremen on chat seats

Read the `flights-foreman` agent body from the registry (`~/.claude/agents/flights-foreman.md`) and be the root foreman for the rest of this flight, with the transport substituted and nothing else. Input: $ARGUMENTS — a `requirements.md` path or the work, the engine (default `codex`), the worktree, and `commit` when the user ordered the landing's commit.

Role-bearing seats support `claude` and `codex`. An `opencode` request returns `BLOCKED: OpenCode chat new rejects --agent-role`; resolve the engine choice before spawning.

| In the manual | Here |
| --- | --- |
| Spawn a child `Agent(subagent_type: "flights-foreman")` | A seat of the engine, named `{flight}-{unit}`, in the project directory or the worktree, born with the foreman role: the shell `pfm chat new --name {flight}-{unit} --engine {engine} --cwd {dir} --agent-role flights-foreman --prompt-file {flight directory}/briefs/{unit}-r{round}.md` (on `codex` add `--model gpt-6.1-sol --effort high`; another engine keeps the role's own pin) — the MCP verb carries no role. Its exit 0 is the brief confirmed as the seat's first turn; write `{unit} CLAIMED · sid {session id}` only once the Claim call (§ Monitors) exits 0. Any other exit is no claim: `chat_kill` the seat it left, if any; one retry, then the unit holds and the return names it |
| Spawn the lander, or a mechanical executor | Unchanged: a sub-agent of this chat, never a seat |
| The child's brief file | The same file, verbatim, as the seat's first turn through `--prompt-file`, never a later `chat_inject`. Its last item becomes: "your last act writes your return to `{flight directory}/returns/{unit}-r{round}.md` and sends it with `pfm chat inject {this chat's name} --file {path}`" — your name from `chat_whoami`; the return file is the return, and a trailing `**Verdict:**` line in it is ignored |
| The return watch per child | The returns Monitor's `RETURN {path}` line; the seat's inject is a bonus, never the signal |
| Verify from the return and § Change inventory | The same tracked and untracked inventory; `chat_last` on the seat when the inject arrived cut short |
| A red back to the child by `SendMessage` | `chat_inject` on the same seat, closing with "continue, then write your return once more" |
| The child's session | `pfm chat resolve {flight}-{unit}`, third column, read right after birth while the name resolves; on the `CLAIMED` line and on each verdict line as `transcript {session id}`, always before `chat_kill`. No third column: `transcript UNRESOLVED · {what resolve printed}` |
| A question only the user can answer → `BLOCKED` | `AskUserQuestion` now; the answer appended to `requirements.md` `## Rulings`, the seat re-briefed by inject |
| The caller hears from you once | The user is the caller: the return at the end |
| After a child's verdict is recorded | `chat_kill` the seat; the return's `UNITS` row names the seat's session |

Every seat is one-shot: born for one unit, killed after its verdict. Waiting is the two Monitors below; a seat is never polled by `chat_status`.

## Monitors

Arm both at the first spawn, each `timeout_ms: 1800000`, and re-arm each on its expiry; a re-armed watch prints one `SEEN` line per seat, a snapshot, not news. Keep `grep --line-buffered`, never a `head` or `tail` stage.

- Returns: `bash -c 'G="$1"; seen=$(ls -1 $G 2>/dev/null); while :; do now=$(ls -1 $G 2>/dev/null); comm -13 <(printf "%s\n" "$seen") <(printf "%s\n" "$now") | sed -u "s/^/RETURN /"; seen=$now; sleep 1; done' _ "{flight directory}/returns/*"`
- Seats: `cd /tmp && pfm chat watch '{flight}-*' --transitions --quiet-after 900 --poll 15 2>&1 | grep --line-buffered -vE '^WORKING '`
- Claim, one call after `pfm chat new` exits 0: `bash -c 'exec 3< <(exec timeout 120 pfm chat watch "$1" --transitions --poll 5 2>&1); w=$!; while IFS= read -r l <&3; do case $l in "SEEN "*" working"*|"WORKING "*) r=0;; "SEEN "*" idle"*|"IDLE "*) r=1;; "SEEN "*|"BLOCKED "*|"EXIT "*|"DEAD "*) continue;; *) r=2;; esac; echo "$l"; kill $w; exit $r; done; echo "NO CLAIM in 120s"; exit 3' _ '{flight}-{unit}'`, by exit code: 0 write `CLAIMED`; 1 its row below; 2 a failed probe — capture the pane, never kill; 3 a full-screen `chat_capture`, judged by the rows below.

A seat that stopped without finishing, by the watch line:

| Line | Action |
| --- | --- |
| `IDLE {seat} … error={kind}`, or `SEEN {seat} error … error={kind}` | `chat_inject` the seat: "The model hit a {kind} error and your turn stopped. Continue where you were; finish and write your return to `{flight directory}/returns/{unit}-r{round}.md`."; `{unit} COMA · {kind} · re-prompted · {time}` in `run.md`. A second coma within 10 minutes: `chat_kill` it and spawn the unit as a `flights-foreman` sub-agent instead; `{unit} COMA · {kind} · re-spawned as sub-agent · {time}` |
| `IDLE {seat}`, no return file | A full-screen `chat_capture`: an unsent message or `Queued follow-up inputs` → `pfm chat keys {seat} Enter` once, then the Claim call again; a question asked → answer it; still idle → `chat_kill` and re-spawn |
| `QUIET {seat} quiet_seconds=N` | Capture and judge: a long run under `write_stdin` is legitimate; a stuck spinner or a reconnect loop is a coma: `chat_inject … force_now` with the continue message |
| `BLOCKED {seat}` | Capture and answer the dialog |
| `EXIT` or `DEAD`, no return file | The seat died: re-spawn |
| `ERROR {seat} …` | The watch could not look: a failed probe, never a quiet seat; capture the pane by hand |

A re-spawn is a fresh seat, the same brief file, the next round.
