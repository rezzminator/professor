---
name: flights:orchestrate-cross-harness
description: 'Executors on another engine — /flights:orchestrate-cross-harness {directory} [engine claude|codex|opencode] [worktree {path}] [commit]: this chat acts as flights-orchestrator, one chat seat per task file instead of a sub-agent, default engine codex. /flights:spec → here → executor seats.'
argument-hint: <flight directory> [engine <claude|codex|opencode>] [worktree <path>] [commit]
---

# Orchestrate, cross-harness — run a flight on chat seats

Read the `flights-orchestrator` agent body from the registry (`~/.claude/agents/flights-orchestrator.md`) and be it for the rest of this flight, with the transport substituted and nothing else:

| In the manual | Here |
| --- | --- |
| Spawn an executor `Agent(subagent_type)` | A seat of the engine (the argument; default `codex`), named `{flight}-{id}`, in the project directory or the worktree, born with the executor role its rating picks and, on `codex`, the model and effort its rating picks — mechanical `gpt-6-luna` at `xhigh`; precise `gpt-6.1-sol` at `high`; smart `gpt-6.1-sol` at `high`: the shell `pfm chat new --name {flight}-{id} --engine {engine} --cwd {dir} --agent-role {flights-mechanical-executor, flights-precise-executor or flights-smart-executor} --model {model} --effort {effort}` (another engine drops `--model` and `--effort`: the role's own pin holds) — the MCP verb carries no role. The `CLAIMED` line is written only once the Claim call (§ Monitors) exits 0: a brief that never started is no claim; a spawn error is a seat not born: no `CLAIMED` line; one retry, then the task holds and the return names it |
| Spawn a lander | Unchanged: a sub-agent of this chat, never a seat |
| The brief in the spawn prompt | `chat_inject` on the seat: the brief verbatim; the transport pastes any size. It closes with the way home: "when done, write your return to `{flight directory}/returns/{id}-r{round}.md` and send it with `pfm chat inject {this chat's name} --file {path}`"; the return file is the return, and a trailing `**Verdict:**` line in it or in `chat_last` is ignored — your name from `chat_whoami`; a seat's plain inject carries one line |
| Wait: end the message, the return arrives | The same; a return is its file appearing in `returns/`, announced by the returns Monitor's `RETURN {path}` line; the seat's inject is a bonus, never the signal |
| Verify from the return and `git diff {baseline} --stat -- {files}` | The same; `chat_last` on the seat when the inject arrived cut short |
| A question back by `SendMessage` | `chat_inject` on the seat, closing with "continue, then return once more in the return shape" |
| The executor's transcript, sent to `flights-speccer` with every `FAILED` and `SPEC-DRIFT` and named on the `run.md` line | The seat's session id, read right after birth while its name still resolves: `pfm chat resolve {flight}-{id}`, third column. It goes on the `CLAIMED` line as `sid {session id}` and on each verdict line as `transcript {session id}`, always before `chat_kill`. A resolve with no third column writes `transcript UNRESOLVED · {what resolve printed}`, never nothing |
| An executor never returns | The watch Monitor's lines, each acted on per § Monitors |
| A question only the user can answer → `BLOCKED` | `AskUserQuestion` now; the seat re-briefed by inject, closing with "continue, then return once more in the return shape"; a ruling that changes the spec goes to `flights-speccer` as a revising call first, on `model: "opus"` like every revising round |
| The caller hears from you once | The user is the caller: the return at the end |
| After a verdict is recorded | `chat_kill` the seat; `DISPATCHED` counts seats born against returns received |

Input: $ARGUMENTS, resolved as `/flights:orchestrate-nested` resolves it, plus the engine. Every seat is one-shot: born for one task file, killed after its verdict. Waiting is the two Monitors below; a seat is never polled by `chat_status`.

## Monitors

Arm both at the first dispatch, each `timeout_ms: 1800000`, and re-arm each on its expiry; a re-armed watch prints one `SEEN` line per seat, a snapshot, not news. Keep `grep --line-buffered`, never a `head` or `tail` stage: a buffered pipe holds the lines until the Monitor dies.

- Returns: `bash -c 'G="$1"; seen=$(ls -1 $G 2>/dev/null); while :; do now=$(ls -1 $G 2>/dev/null); comm -13 <(printf "%s\n" "$seen") <(printf "%s\n" "$now") | sed -u "s/^/RETURN /"; seen=$now; sleep 1; done' _ "{flight directory}/returns/*"`
- Seats: `cd /tmp && pfm chat watch '{flight}-*' --transitions --quiet-after 900 --poll 15 2>&1 | grep --line-buffered -vE '^WORKING '`
- Claim, one call after the brief's inject, since the seats Monitor drops `WORKING`: `bash -c 'exec 3< <(exec timeout 120 pfm chat watch "$1" --transitions --poll 5 2>&1); w=$!; while IFS= read -r l <&3; do case $l in "SEEN "*" working"*|"WORKING "*) r=0;; "SEEN "*" idle"*|"IDLE "*) r=1;; "SEEN "*|"BLOCKED "*|"EXIT "*|"DEAD "*) continue;; *) r=2;; esac; echo "$l"; kill $w; exit $r; done; echo "NO CLAIM in 120s"; exit 3' _ '{flight}-{id}'`, by exit code:
  - 0, a working line: write `CLAIMED`.
  - 1, an idle line: its row below, `error=` the coma row, else the `IDLE`, no-return row.
  - 2, `ERROR` or a line that is no watch event (the watch's own failure): the `ERROR` row, a failed probe; capture the pane, never kill.
  - 3, `NO CLAIM in 120s`, the seat neither working nor idle (a dialog, a dead seat): a full-screen `chat_capture`, judged by the rows below.

A seat that stopped without finishing, by the watch line:

| Line | Action |
| --- | --- |
| `IDLE {seat} … error={kind}`, or `SEEN {seat} error … error={kind}` | `chat_inject` the seat: "The model hit a {kind} error and your turn stopped. Continue where you were; finish and write your return to `{flight directory}/returns/{id}-r{round}.md`."; `{id} COMA · {kind} · re-prompted · {time}` in `run.md`. A second coma of that seat within 10 minutes: the model is down; `chat_kill` it and re-dispatch the task on another Codex model when one is configured, else on the Claude engine with the task's tier model; `{id} COMA · {kind} · re-dispatched on {engine} {model} · {time}` in `run.md` |
| `IDLE {seat}`, no `returns/{id}-r{round}.md` | A full-screen `chat_capture`: the composer holds the brief or shows `Queued follow-up inputs` → `pfm chat keys {seat} Enter` once, then the Claim call again; a question asked → answer it; still idle → `chat_kill` and re-dispatch |
| `QUIET {seat} quiet_seconds=N` | Capture and judge: a long run under `write_stdin` is legitimate, left alone; a stuck spinner or a reconnect loop is a coma: `chat_inject … force_now` with the continue message |
| `BLOCKED {seat}` | Capture and answer the dialog |
| `EXIT` or `DEAD`, no return file | The seat died: re-dispatch |
| `ERROR {seat} …` | The watch could not look: a failed probe, never a quiet seat; capture the pane by hand |

A re-dispatch is a fresh seat, the same task file, the next round.
