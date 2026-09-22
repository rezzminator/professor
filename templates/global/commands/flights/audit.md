---
name: flights:audit
description: USER-ONLY — /flights:audit {directory}, default the newest under /tmp/{project}/flights/. The skeptic over a flight, running or landed — every DONE, check, review and commit claim verified against run.md, git, the task files, the executor transcripts and the seats; findings routed, nothing fixed. Writes {directory}/audit.md and nothing else.
disable-model-invocation: true
---

# Audit — doubt the flight

Ground truth only: `run.md`, git, the task files, the executor transcripts, the checks' own output, the seats' state. Never a return, a recap, a message, or your own earlier picture. You find defects and inefficiencies and route them; you fix nothing. Input: $ARGUMENTS — the flight directory, else the newest under `/tmp/{project}/flights/`.

## The anchors

An anchor you cannot read is a finding ("failed to look"), never an absence ("nothing there").

| Anchor | Where | Proves |
| --- | --- | --- |
| `index.md` | the directory | The graph: ids, `needs`, `shares`, ratings, `reads`; the `files` each diff must stay inside |
| `{level}-{letter}.md`, `0-*.md` | the directory | What each executor was told; the `Done when` rows every test must cover; the `Files` each diff must stay inside |
| `run.md` | the directory | The header's baseline sha and date; one line per event; the resume point; the `RETRO` lines — a lesson recorded and then repeated by a later executor's transcript is a finding, as is a lesson in a return that never reached `run.md` |
| `briefs/{id}-r{round}.md`, `briefs/gate-{project}.md` | the directory | What each spawn was told beside its task file: the pasted `run.md` lines, the standing rules, the `RETRO` lines. A spawn in `agents.tsv` without its brief file is a finding; on Codex the spawn message is stored encrypted, so the file is the only readable brief |
| `audit.md` | the directory | The previous audit, for a delta |
| Git | `git diff {baseline} --stat`, `git diff {baseline} -- {files}`, `git log {baseline}..HEAD`, `git status --porcelain` | What changed, per task and outside every task |
| Executor transcripts (nested, live) | `$CLAUDE_CONFIG_DIR/projects/{cwd slug}/{session id}/subagents/agent-*.jsonl`, default `~/.claude`; one flat directory whatever the depth; the `.meta.json` beside each names its `agentType` and `spawnDepth` | Calls per executor against its 80-call cap, peak context, files read outside the spec, cadence, poll chains |
| The orchestrator's transcript | the same directory (nested: the file whose `.meta.json` says `flights-orchestrator`) or this chat's history (live, cross-harness) | Its calls per task, the briefs, whether it opened a task file, whether it held a ready task |
| Chat seats (cross-harness) | `chat_ls`, `chat_status`, `chat_last`, `chat_read` | Liveness and the seat's own account, its review run included |
| The standing checks | the command and its log, named in the brief or a `0-` file | Whether the landing's checks ran and what they printed |
| The gate | `gate-{project}.md` in the directory and each `flights-gater` transcript: the two full runs, the `/code-review` run and its effort, the attack map, the tests it wrote; the return's `GATE` row is the claim checked against them | Every changed hunk mapped; findings and their terminal state: fixed, residual, or outside the flight |
| Spend | your own run of `node ~/.claude/commands/tokens/token-audit.mjs --flight {directory} --metrics-out {a scratch file}`, over the transcripts; `agents.tsv` in the directory selects them. The orchestrator's `metrics.md` and `COST` row are claims you compare against your run, never your source | Per agent: calls, start and peak context, growth per call, tokens, price, failed commands, poll calls, re-reads, contract-file reads, compactions, over cap; an `UNMATCHED` row (a ledger row without a transcript, a transcript without a row) is a finding |

## The audit, piece by piece

1. Index integrity: every task file has a row and every row a file; `needs` is acyclic and names only existing ids; `level` is one above the deepest need; no file appears in two tasks unless one needs the other. Grep, never trust.
2. Ledger truth: every `DONE` line's task shows changed files inside its index `files` in `git diff {baseline}` and nothing outside; the covering test per `Done when` row exists, was read by you, and its run is in a log or a transcript, not in a claim; a `CLAIMED` line without a verdict is in flight (a live executor) or lost (named); a `FAILED` or `SPEC-DRIFT` line carries its round, the executor's cause and its transcript path, and has its revising round behind it (new or changed task files, a rebuilt index); a `STALE` line has a seat status behind it.
3. Faults handled the only legal way: a task file changed after the header date only by a revising `flights-speccer` call visible in the orchestrator's transcript; no rulings, notes or patches beside the directory; no task file re-dispatched unchanged after `FAILED`; a `CLAIMED` task's file unchanged while its executor ran; the second red of one id followed by a task file that names the cause in `Decisions` and a speccer transcript that read the whole unit and the executor transcripts, never by a rewrite alone; a third red of one id followed by `BLOCKED`, never a rewrite; no `Done when` row carrying a value one run printed.
4. Conformance spot-check on the highest-stakes tasks (protected-data channels, contracts, a `hard` rating): each `Done when` row against the code and its test — mechanically where it can be (grep an export into existence, diff a contract), by reading where it cannot.
5. Cost and cadence, from the transcripts: calls per executor against the healthy band (about 40 to 80); peak context; the orchestrator's calls against about two per task plus the landing; any poll chain (`sleep`, `echo idle`, a repeated log peek); any per-step report in a return; any ready task that waited for a sibling; executors dispatched against returns received.
6. Liveness, in flight only: a transcript still growing, a seat whose status changed within the stale bound, a pane captured full-screen and judged from process evidence; an empty capture is a failed probe, never a quiet seat.
7. Landing: the checks were watched (the transcript shows the command and its output, not a summary); every project touched has a gate line and a `gate-{project}.md`, each new test of the gater was watched failing, and every finding is terminal (fixed, a residual sent to `flights-speccer`, or carried to `NOTES`); the commit sha exists and its diff matches the flight's; nothing outside the flight's files changed on the branch.

## Report

One table — `task · claimed · evidence found · gaps` — then findings ranked most severe first, each with its artifact quoted and its prescription: a revising `flights-speccer` call, an orchestrator action, `gitter`, or the user when only the user can. All clear: the table plus its coverage line, what was probed and what would have failed the probe.

## Writes

`{directory}/audit.md`, this report, overwritten each run. Nothing else: `run.md` is the orchestrator's, the task files are `flights-speccer`'s, the code is the executors'.

## Rules

- The judge is never the thing being judged: read the artifact, never the verdict asserted about it.
- A clean grep proves the pattern absent, never the flight clean; scope every claim to what was measured.
- An error is never reported as absence: "could not read the transcripts" and "no transcripts" are two findings.
- Findings are defects to route, never fixes to make by hand.
- User contact only for the physically user-only, opening with why it is.
