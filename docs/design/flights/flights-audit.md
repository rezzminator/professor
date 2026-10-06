# /flights:audit

`/flights:audit` is the skeptic over a flight, running or landed. It believes artifacts: `run.md`, git, the task files, the executor transcripts, the checks' output, the seats' state. It never believes a message, a return, a recap or its own earlier picture. It finds defects and inefficiencies and routes them; it fixes nothing. It descends from `/wave:ccc` with the seat-holding removed: an audit is a pass, not a post.

Decisions live in this file. The executable wording lives in [`templates/global/commands/flights/audit.md`](../../../templates/global/commands/flights/audit.md).

## Contents

- [Input](#input)
- [The anchors](#the-anchors)
- [The audit, piece by piece](#the-audit-piece-by-piece)
- [The report](#the-report)
- [Writes](#writes)
- [Rules](#rules)
- [Surfaces that stay in sync](#surfaces-that-stay-in-sync)

## Input

`/flights:audit {flight directory}`; absent, the newest directory under `$HOME/.local/state/pfm/flights/{project}/`. The audit runs in the main chat at the chat's model: its judgment is the product.

## The anchors

Every artifact the workflow produces, where it lives, and what it proves. An anchor the audit cannot read is a finding ("failed to look"), never an absence ("nothing there").

| Anchor | Where | Proves |
| --- | --- | --- |
| `index.md` | the flight directory | The graph: ids, `needs`, `shares`, ratings, `reads`; the `files` each diff must stay inside |
| Task files `{level}-{letter}.md`, shared files `0-*.md` | the flight directory | What each executor was told; the `Done when` rows every test must cover; the `Files` each diff must stay inside |
| `run.md` | the flight directory | The header's baseline sha and date; one line per event with its token; the resume point; the `RETRO` lines — a lesson recorded and then repeated by a later executor's transcript is a finding, as is a lesson in a return that never reached `run.md` |
| `briefs/{id}-r{round}.md`, `briefs/gate-r{round}.md` | the flight directory | What each spawn was told beside its task file: the pasted `run.md` lines, the standing rules, the `RETRO` lines. A spawn in `agents.tsv` without its brief file is a finding; Codex stores the spawn message encrypted, so the file is the only readable brief |
| `returns/{id}-r{round}.md`, `returns/gate-r{round}.md` | the flight directory (a seat's: cross-harness; the gate's: a flight whose orchestrator transcript holds the gate's `returns/gate-r` watch, an older flight judged without it) | Each seat's and each lander's return as written; a verdict line in `run.md`, a seat's or a `gate` line, without its return file is a finding |
| `audit.md` | the flight directory | The previous audit, for a delta |
| Git | `git diff {baseline} --stat`, `git diff {baseline} -- {files}`, `git log {baseline}..HEAD`, `git status --porcelain` | What actually changed, per task and outside every task |
| Executor transcripts, nested and live | `$CLAUDE_CONFIG_DIR/projects/{cwd slug}/{session id}/subagents/agent-*.jsonl` (default `~/.claude`), one flat directory whatever the depth; the `.meta.json` beside each names its `agentType` and `spawnDepth` | Calls per executor against its 150-call cap, peak context, files read outside the spec, reporting cadence, poll chains |
| The orchestrator's transcript | the same directory (nested: the file whose `.meta.json` says `flights-orchestrator`), or the main chat's own history (live, cross-harness) | Its calls per task, the briefs it sent, whether it opened task files, whether it held a ready task |
| Chat seats, cross-harness | `chat_ls`, `chat_status`, `chat_last`, `chat_read` | Liveness and the seat's own account of its task, its review run included |
| The landing checks | each command the orchestrator's input names (its brief, or the run command's arguments), and its log | Whether the landing's checks ran and what they printed |
| The gate | `gate.md` in the directory and the `flights-lander` transcript of each gate round: each project's two full runs, the `/code-review` run and its effort, the attack map, the tests it wrote; the return's `GATE` row is the claim checked against them | Every changed hunk mapped; findings and their terminal state: fixed, residual, or outside the flight |
| `REVIEW.md` | the flight directory, for a flight that reached Phase MERGE | The merge-gating report the git writer read at the landing: every `gate.md` finding as `F{n}` with `status: resolved @{sha}` (the flight's commit before landing; the `LANDED` row names where it went) or `status: waived — outside the flight`; a `gate.md` finding missing from it, or a status `gate.md` contradicts, is a finding |
| Spend | The audit's own run of `token-audit.mjs --flight {directory} --metrics-out {a scratch file}`, over the transcripts `agents.tsv` selects. The orchestrator's `metrics.md` and `COST` row are claims compared against that run, never its source: the audit and the orchestrator stay untied | Per agent: calls, start and peak context, growth per call, tokens, price, failed commands, poll calls, re-reads, contract-file reads, compactions, over cap; an `UNMATCHED` row is a finding |

## The audit, piece by piece

1. Index integrity: every task file has an index row and every row a file; `needs` is acyclic and every id it names exists; `level` is one above the deepest need; no file appears in two tasks unless one needs the other. Grep, never trust.
2. Ledger truth: every `DONE` line's task shows changed files inside its index `files` in `git diff {baseline}`, and nothing outside; the covering test per `Done when` row exists, was read, and its run is in a log or a transcript, not in a claim; a `CLAIMED` line with no verdict is in flight (a live executor) or lost (named); a `FAILED` or `SPEC-DRIFT` line carries its round, the executor's cause and its transcript path, and has its revising round (new or changed task files, a rebuilt index); a `TOO-LARGE` line carries the executor's split, no round and no transcript, and has its revising round behind it; a `WAIT` line names tasks in flight whose `files` hold every file of its `blocked by` return, and the task's next `CLAIMED` follows their verdicts; a `MAIN-CHAT` line is followed by `DONE · applied by the main chat` or carried in the return's `MAIN-CHAT` row; a `COMA` line, cross-harness only, has the watch's `error=` line behind it in the orchestrator chat's history and a re-prompt or re-dispatch after it.
3. Faults handled the only legal way: a task file changed after the header date only by a revising `flights-speccer` call (the orchestrator's transcript shows the call); no rulings, notes or patches written beside the directory; no task file re-dispatched unchanged after `FAILED`, a `WAIT` re-dispatch excepted; a `CLAIMED` task's file unchanged while its executor ran; the second red of one id (`FAILED` or `SPEC-DRIFT`; `WAIT` and `TOO-LARGE` are not reds) followed by a task file that names the cause in `Decisions` and a speccer transcript that read the whole unit and the executor transcripts, never by a rewrite alone; a third red of one id followed by `BLOCKED`, never a rewrite; no `Done when` row carrying a value one run printed. The measured case: five rounds on one task, each spec cut from the previous red line, 180 executor calls and four hours — the one-fault-per-run loop one level up.
4. Conformance spot-check on the highest-stakes tasks (protected-data channels, contracts, a `smart` rating): each `Done when` row against the code and its test, mechanically where it can be (grep an export into existence, diff a contract), by reading where it cannot.
5. Cost and cadence, from the transcripts: calls per executor against the healthy band (about 40 to 120); peak context; the orchestrator's calls against about two per task plus the landing; any poll chain (`sleep`, `echo idle`, a repeated log peek); any per-step report in a return; any ready task that waited for a sibling; executors dispatched against returns received.
6. Liveness, in flight only: a transcript still growing, a seat whose `WORKING` or `SEEN … working` line shows in the orchestrator chat's history (cross-harness), a pane captured full-screen and judged from process evidence; an empty capture is a failed probe, never a quiet seat.
7. Landing: the checks were watched (the transcript shows the command and its output, not a summary); the flight has a gate line and a `gate.md` holding both full runs of every project touched, each new test of the lander was watched failing, and every finding is terminal (fixed, a residual sent to `flights-speccer`, or carried to `NOTES`); the commit sha exists and its diff matches the flight's diff; nothing outside the flight's files changed on the branch; a `LANDED` row's sha is on the integration branch it names and carries the flight's diff, its worktree and branch gone; a `NOT LANDED` row's worktree still stands, and `unfinished` names every task without `DONE`.

## The report

One table — `task · claimed · evidence found · gaps` — then findings ranked most severe first, each with its artifact quoted and its prescription: a revising `flights-speccer` call, an orchestrator action, `gitter`, or the user when only the user can. All clear: the table plus its coverage line, what was probed and what would have failed the probe.

## Writes

`{flight directory}/audit.md`, the report, overwritten on each run. Nothing else: `run.md` is the orchestrator's, the task files are `flights-speccer`'s, the code is the executors'.

## Rules

- The judge is never the thing being judged: read the artifact, never the verdict asserted about it.
- A clean grep proves the pattern absent, never the flight clean; every claim is scoped to what was measured.
- An error is never reported as absence: "could not read the transcripts" and "no transcripts" are two findings.
- Findings are defects to route, never fixes to make by hand.
- User contact only for the physically user-only, opening with why it is.

## Surfaces that stay in sync

| Surface | File | Holds |
| --- | --- | --- |
| The command | `templates/global/commands/flights/audit.md` | The anchors, the seven pieces, the report |
| The orchestrator | [`flights-orchestrator`](flights-orchestrator.md) | The `run.md` shape and tokens the audit reads; the return the audit checks, its `GATE` and `RETRO` rows included |
| The family | [`flights.md`](flights.md) | The directory and the tokens |
