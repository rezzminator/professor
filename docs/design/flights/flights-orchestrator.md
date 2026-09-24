# flights-orchestrator

`flights-orchestrator` is the one manual of running a flight: a spec directory written by [`flights-speccer`](flights-speccer.md), one fresh executor per task file, none of them executed by the orchestrator itself. It holds the index and the verdicts and nothing else. Its body is read three ways: as a sub-agent (the nested container), by a main chat acting as it (live), and by a main chat driving chat seats (cross-harness); the substitutions are in [`flights.md`](flights.md#three-containers-one-manual), and the manual never mentions them.

Decisions live in this file. The executable wording lives in [`templates/global/agents/flights-orchestrator.md`](../../../templates/global/agents/flights-orchestrator.md). A change lands here first, then in the template, then in every surface listed under [Surfaces that stay in sync](#surfaces-that-stay-in-sync).

## Contents

- [Why one manual, held by a sub-agent](#why-one-manual-held-by-a-sub-agent)
- [Input](#input)
- [The run](#the-run)
- [The executor brief](#the-executor-brief)
- [Verdicts are evidence, not truth](#verdicts-are-evidence-not-truth)
- [Situations](#situations)
- [Review](#review)
- [`run.md`](#runmd)
- [Landing](#landing)
- [The return](#the-return)
- [Retro lines](#retro-lines)
- [The universal laws](#the-universal-laws)
- [Not part of the design](#not-part-of-the-design)
- [What the evidence says](#what-the-evidence-says)
- [Measuring a run](#measuring-a-run)
- [Surfaces that stay in sync](#surfaces-that-stay-in-sync)
- [Open items](#open-items)

## Why one manual, held by a sub-agent

A main chat outlives every agent, so its context is the dearest in the family. The orchestration of a flight is a loop of dispatch, wait, verify, react, dispatch that runs for the whole flight; run in the main chat it re-sends the chat's entire history on every step. The loop therefore lives in a sub-agent at the mechanical tier (`sonnet`) that holds only the index and the verdicts, and the main chat hears from it once.

The same body is the only description of the protocol. A second copy for the live or cross-harness container would drift; the commands for those containers substitute the transport (how an executor is spawned, briefed, waited for, questioned, stopped) and read everything else from the agent file.

## Input

| Input | Rule |
| --- | --- |
| The flight directory | Required. Work that arrives without one goes to `flights-speccer` first, and its return is the index |
| Standing rules the executors work under | What the project contract does not carry: the worktree, the fenced command that runs one package's affected tests, the checks by command, anything the caller adds for this flight. Pasted into every executor brief, never into a task file; the orchestrator authors none. A second measured case: a brief whose rule read "every build/test runs … `dev.sh iso test pfm`" meant the script's path but named the full suite, and the orchestrator added "launch it backgrounded and wait once"; 146 of the six executors' 201 minutes went to full-suite waits, so the refusal keys on the full-suite command itself, however the rule frames it. A standing rule says where and with what an agent works, never what steps it runs: one that adds, drops or replaces a step of a role is refused and named under `NOTES`. The measured case: a launch message written by a chat born before the redesign ordered "every executor's self-review is a review over its own change"; the orchestrator pasted it as "overriding the role's no-review rule", and six executors ran 23 review processes. The fleet prompt carries the same law for every manual. The `CLAUDE.md` / `AGENTS.md` contract reaches every executor from the harness and is never pasted or named |
| The projects and their testing manuals | Each project the flight touches, with its manual's path: it travels in every executor's and lander's brief. A project without one is a `NOTES` line |
| A worktree | Used when the flight runs outside the checkout; otherwise the checkout |
| The cap | Executors in flight at once; absent, ten. An executor's own cap (80 calls) lives in its agent; a `CLAIMED` line is stale after 60 minutes |
| The landing | Which checks run after the gate, whether gitter commits. Absent: the standing checks once, no commit. The gate itself is never optional and its review effort is the lander's to size, unless the user ordered a level above `medium`: that order travels to the lander as given |

## The run

1. Read `index.md` and `run.md`. On a fresh flight write the `run.md` header: the directory, the baseline commit (`git rev-parse HEAD` at this moment), the date. A task with a `DONE` line is done; a task with a `CLAIMED` line and no verdict was in flight when the last run stopped: treat it as not started, and say so in the return. A task with a `BLOCKED` line and no later verdict goes out only when the brief names it revised (its file rewritten by a revising call after the user's ruling); otherwise it stays `BLOCKED` in the return.
2. Dispatch every task whose `needs` are all done, in one message, as many at once as its `shares` and the concurrency cap admit. The harness reports nothing when its cap is hit, a spawn past it does not happen: count the executors in flight (`CLAIMED` lines without a verdict) and stay under the cap. Write one `CLAIMED` line per task before the spawn. Holding a ready task for a sibling is a violation; holding it for a free slot is the one legal hold.
3. Wait by ending the message with one line and no tool call; each executor's return arrives on its own.
4. On each return: read its first line, verify it, append its verdict line to `run.md`, react per [Situations](#situations), dispatch whatever it unblocked.
5. After the last verdict: the landing.
6. Return once.

The orchestrator never opens a task file and never reads a repository file to judge a task. What it must know about a task is in the index; what it must check is in the executor's return and in git.

## The executor brief

The brief carries, and nothing more:

- the task file path and the paths its index row `reads`, with the instruction to open them together in the first message;
- the `run.md` lines of the tasks it `needs`, pasted, so an upstream adaptation reaches it without a spec rewrite;
- the standing rules, and the worktree when one exists;
- the path of the testing manual of the project the task changes.

Nothing else: the task file is the spec, and the [executor's agent](flights-executors.md) holds what the brief used to restate — the cap, the tests it writes, the open hand, the return's shape, git read-only. The index row's `rating` picks the agent type: `mechanical` → `flights-mechanical-executor` (`sonnet`), `smart` → `flights-smart-executor` (`opus`); the spawn carries no model override.

## Verdicts are evidence, not truth

An executor's return is a claim. The orchestrator matches the first line's token and then verifies:

- `DONE`: the return names what changed, a covering test per `Done when` row and the proof they ran and were watched failing, and `git diff {baseline} --stat -- {the index's files}` shows a change: the files come from the index row, never from the return, so the judge is never the judged. A return that claims done with no proof, or with nothing changed, gets one question back to the same executor; a second such return is recorded `FAILED`.
- `FAILED`, `SPEC-DRIFT`: the return names a cause, or names what was read and says the cause is unknown; a red with neither is shapeless. Recorded as returned with the executor's transcript named on the line; the reaction is in [Situations](#situations).
- `BLOCKED`: recorded as returned; the reaction is in [Situations](#situations).
- No token on the first line, or a red with no cause and no reading named: one question back asking for the return in shape; a second shapeless return is `FAILED`.

The baseline is the commit recorded in the `run.md` header, so a resumed flight judges against the same tree the flight started from.

## Situations

Every situation the manual answers, with who acts. The orchestrator fixes nothing itself and patches no task file.

| Situation | Reaction |
| --- | --- |
| `DONE`, verified | `run.md` line names what was adapted or `as specified`; dispatch what it unblocked |
| `DONE` without proof or without a change in git | one question back to the same executor; a second such return → `FAILED` |
| `FAILED` (including a cap) | start nothing that needs it; send `flights-speccer` the report, what already landed, the completed ids and the executor's transcript — nested and live: the sub-agent transcript path (`$CLAUDE_CONFIG_DIR/projects/{cwd slug}/{session id}/subagents/agent-{id}.jsonl`, the id from the spawn's task id); cross-harness: the seat's name and its transcript id; its rewritten index cuts the task smaller or re-approaches it; dispatch from the new index. The same task file is never re-run unchanged |
| `SPEC-DRIFT` | the same road as `FAILED`, with the drift report as the reason |
| Any revising round | the speccer is pinned at `opus`; every revising round — a red, a failed landing check, a lander residual, the diagnose-first round included — is a fresh `Agent(subagent_type: "flights-speccer", model: "opus")` handed the directory and the reason, since a `SendMessage` keeps the running speccer's model; only a speccer already on `opus` is revised by `SendMessage` |
| A return carries `RETRO {lesson}` | [Retro lines](#retro-lines) |
| A second `FAILED` or `SPEC-DRIFT` of the same id | the revising call is marked diagnose-first and carries every transcript of that id; `flights-speccer` names the cause in the task file before rewriting (its § Drift). The `run.md` line carries the round: `{id} SPEC-DRIFT · round 2 · …` |
| A third red of the same id | `{id} BLOCKED · {the executor's cause line}`; no third revising call. The question travels in the return like any `BLOCKED`; the flight lands without the task |
| `BLOCKED` with a question only the user can answer | record `BLOCKED`; every other task continues; the question travels in the return. The ruling comes back as a revising `flights-speccer` call: the live container makes it on the answer; the nested container's caller makes it after the return, then resumes the run naming the revised ids |
| A question from an executor the index or the brief can answer | answer it by message to the same executor |
| A question from an executor nobody but the user can answer | `BLOCKED` for that task, as above |
| The concurrency cap is reached | never reported by the harness: the count of in-flight executors is the only guard. Hold the task; dispatch it as the next return lands |
| An executor never returns | seen only when something wakes the loop (a sibling's return; in the live and cross-harness containers, the user): a `CLAIMED` line older than 60 minutes with no verdict. Named in `DISPATCHED` as a missing return; its task stays `CLAIMED`; the flight lands without it and the return says so. When it was the last executor, nothing wakes a nested orchestrator: the user re-runs the container, and the resume rule treats the task as not started |
| A silent seat past the stale bound (cross-harness only) | `STALE {id}` in `run.md` and the return; the seat is never killed or re-dispatched blind |
| A standing check fails at landing | unspecified work: a revising `flights-speccer` call with the check's output and the completed ids; its task files dispatch like any other |
| A lander returns `PASS {project}` | the gate line in `run.md`; when every lander has returned, the standing checks, then the commit |
| A lander returns `FIXED {project}` | the gate line in `run.md`; the files it changed join the commit |
| A lander returns `FAIL {project}`, a cap included | the residuals are unspecified work: a revising `flights-speccer` call, its task files dispatch like any other, then that project's gate runs again; a second `FAIL` of one project travels in the return and the flight lands without a commit |
| A defect a return names outside the task's files, or a lander's finding outside the flight | `NOTES`; never a fix by the orchestrator |
| A spec fault the orchestrator can see (two decisions contradict, an index row without a file) | a revising `flights-speccer` call; never a patch, never a ruling written beside the directory |

## Review

Executors run no review. The flight is reviewed once, over its whole diff, by [`flights-lander`](flights-lander.md): after the last verdict the orchestrator spawns one lander per project touched, all in one message, each briefed with the flight directory, the project and its testing manual's path, the standing rules and the worktree — and no review effort, which the lander sizes from the diff, unless the caller's brief carries the user's own order for a level above `medium`, passed on as given.

- The per-executor review was the largest single cost of the audited flights (67 review sessions, 182M tokens, 38 of them whole-branch reviews of one growing diff); one review per flight sees the same diff once.
- The lander fixes what it finds; the orchestrator grades nothing and fixes nothing. Its return is a claim verified like any other: `gate-{project}.md` exists and the return quotes two full-run verdict lines.
- `FIXED` adds the lander's files to the commit; `FAIL` sends the residuals to `flights-speccer`; a finding outside the flight goes to `NOTES`.

## `run.md`

One file, `{flight directory}/run.md`. A header line on creation — `flight {directory} · baseline {sha} · {date}` — then one line per event: `{id} {CLAIMED|DONE|FAILED|SPEC-DRIFT|BLOCKED} · {one line}`, `gate {project} {CLAIMED|PASS|FIXED|FAIL} · {one line}` per lander, and `{id} RETRO · {lesson}` for a lesson a return carried ([Retro lines](#retro-lines)); the cross-harness container adds `STALE` as a substitution, and the manual never names it. A `CLAIMED` line names the executor and the time; a `DONE` line names what the executor adapted, or `as specified`, and is what downstream briefs carry; a `FAILED` or `SPEC-DRIFT` line names the round for that id, the cause the executor gave and the executor's transcript, so the revising call and the audit can read how it got where it got. The file is the resume point and the ledger an audit reads.

Beside it, `{flight directory}/agents.tsv`: one tab-separated row per spawn, appended the moment the spawn returns its agent id — task id (or `gate-{project}`, or `spec`), agent type, agent id, round, ISO time, engine (`claude`, `codex`, `seat`) — for every executor, lander and speccer. It is append-only and the orchestrator never reads it back, so it costs the orchestrator's context nothing; `run.md` is re-read on resume and pasted into briefs, which is why the ids stay out of it. The ledger is what lets the metrics script and the audit open exactly a flight's transcripts instead of guessing them from a time window. The id is whatever the spawn returned, verbatim: an agent id on Claude; on Codex the agent path (`/root/{name}`), which the rollout's `session_meta.agent_path` repeats — a Codex orchestrator never sees a thread id, and the first two measured Codex flights matched 0 of 10 rows while the design assumed one. The time is printed by `date -u` inside the appending command, never typed: a measured ledger carried `19:00:00` for a spawn at 18:49, and a clockless date opens a match window at midnight. A voided claim keeps its row, because the agent ran and its spend is the flight's.

Beside both, `{flight directory}/briefs/`: one file per spawn, `{id}-r{round}.md` for an executor and `gate-{project}.md` for a lander, written by the command that appends the `CLAIMED` line. The spawn message carries only paths: the brief file, the task file, its `reads`. Codex stores a spawn message encrypted in both rollouts, so on that engine a pasted brief can never be audited; the file costs no extra call and makes the brief an artifact on every engine. Nothing else is written by the orchestrator: no reports, no per-step lines, no rulings.

## Landing

After the last verdict the gate runs ([Review](#review)), every project `PASS` or `FIXED`; then the standing checks run once, and the result recorded is the one the orchestrator watched print; a command emitted is not a check run. A check the lander's closing full run already ran on the unchanged tree is quoted from the lander's log, never run a third time: the fenced suite takes about thirteen minutes. A failing check is unspecified work and goes to `flights-speccer` as a revising call. When the brief asks for a commit, `gitter` makes it: Phase COMMIT in the checkout the flight ran in (the worktree, or the project), the files named as the union of the index's `files` over the `DONE` tasks plus the files each lander's return names, the message summarising the flight; the return carries the sha. The landing never merges: a worktree flight reaches `develop` by the user's own gitter MERGE order after the return. The orchestrator runs no fix itself.

The landing's last act is the measurement: `token-audit.mjs --flight {flight directory}` writes `metrics.md` — one row per agent with calls, wall time, start and peak context, growth per call, tokens, price, failed commands, poll calls, re-reads, contract-file reads, compactions, over cap and how the row was matched — and the return's `COST` row quotes its totals and its most expensive agent. A failed run is reported as failed, never omitted. Hooks were rejected for this: the Codex compile drops an agent's hooks, Codex hooks see only shell commands, and no hook on either engine receives token counts; every measure lives in the transcripts. `/flights:audit` runs the script itself and treats `metrics.md` as a claim.

## The return

```text
FLIGHT {directory}
{id} · {verdict} · {one line}          one row per task
GATE {project} · {PASS|FIXED|FAIL} · {n} findings, {m} fixed, {k} residual | none
CHECKS {command → result} | none
COMMIT {sha} | none
DISPATCHED {n} executors, {g} landers, {m} returns, {k} revising rounds
COST {calls} calls · {tokens} · {price} · worst {agent}: {price}, {calls} calls | failed: {error}
BLOCKED {id}: {question} | none
RETRO {id}: {lesson} [MANUAL] | none
NOTES {up to five lines} | none
```

`DISPATCHED` reconciles agents sent against returns received; a missing return is named, never silent. `GATE` repeats each lander's first line and counts; the audit checks it against `gate-{project}.md` and the lander's transcript. `NOTES` carries the findings reported outside the flight's files and anything the user should know that fits no row.

## Retro lines

A flight learns while it runs. Every executor and lander return may carry one line `RETRO {lesson}`: something about the environment, the tooling, the project law or the testing manual that cost it calls and would cost the next agent the same. The measured case: every agent of one flight hit `pnpm: command not found` and burned six command rounds each, and nobody told the next one or the user.

- One line per agent, at most 200 characters: a fact plus the working alternative. Never a progress note, never about its own task's content; `none` is the normal case.
- The orchestrator stays `run.md`'s only writer: it appends `{id} RETRO · {line}` and skips a lesson already recorded (the same cause).
- Every RETRO line of the flight is pasted into each later brief under the standing rules; an environment or tooling lesson also goes by one `SendMessage` to the executors still in flight.
- All of them travel in the return's `RETRO` row, so the user hears what is broken on the host. A lesson that proposes a testing-manual addition is marked `MANUAL`: the manual is guarded, and `/pcm` folds it.
- A revising `flights-speccer` round reads the RETRO lines of `run.md` with the transcript.
- The cap that keeps it small: one line per agent, deduplicated, no second file.

## The universal laws

These hold for every orchestrator and every executor on every harness, and belong in the harness prompt's orchestration section as well as here:

- An executor reports once, when done, plus a real question or blocker; an orchestrator reports once to its caller, plus a question only the user can answer. Never routine progress, never a diff, a log or a file's contents in a message: progress lives in the artifacts.
- Waiting is one call or none: end the turn and let the return arrive. A poll, a sleep chain or a log peek re-bills the whole context each time.
- Only `flights-speccer` changes a task file. A fault goes back to it as a revising call; nobody patches a spec or writes rulings beside it.

## Not part of the design

| Left out | Reason |
| --- | --- |
| The orchestrator executing a task itself | Its context would carry the work of one task into the dispatch of every other |
| Opening a task file in the orchestrator | The index carries what dispatch needs; the file is the executor's |
| A per-step ledger | One verdict line per task is the resume point; more is chatter |
| Patching a task file or writing rulings beside the directory | A second spec over the first; `flights-speccer` alone edits |
| Stuck detection by pattern (repeated actions, monologues) | No published accuracy; a token, a cap and a stale bound cover the cases |
| Re-running a failed task file unchanged | The same spec fails the same way; the fix is a smaller or different task |
| Summarising an executor's context to keep it going | Executors are fresh per task; a task too big for one context is cut |
| A stuck-agent kill from inside the nested container | Not possible through the Agent tool; the missing return is named |
| A cold reviewer agent per `smart` task, briefed by the orchestrator and graded by it | Replaced by [`flights-lander`](flights-lander.md): one `/code-review` of the whole diff per flight, the fix with the agent that found it, nothing for the orchestrator to grade and no report to route |
| Trains, waves, a worktree per wave, BUILD-GREEN handshakes, an entry-point census, a conformance pass | The pipeline this agent replaces |

## What the evidence says

The rulings above rest on measured results, collected in the runtime research of 2026-09-20:

- Fan out only file-disjoint work: multi-agent gains +80.8% on decomposable work and loses 70% on sequential work; cross-agent pull requests conflict at 41.7% against 19.8% for one agent. The index's `needs` and `shares` are that rule made mechanical.
- Keep every dispatch single-turn and self-contained: models lose 39% on average when a task is spread over turns.
- Completion is a token matched in code, never text interpreted: every mature runtime (a finish action, a literal sentinel, a terminal event) does this; every runtime that scrapes text calls it a heuristic.
- Every cap in the field is hand-picked; the useful ones name their exit instead of dying as a generic error. The cap here returns `FAILED {id}: cap` with what landed.
- Decompose on failure rather than up front: +33 points over fixed decomposition. `FAILED` goes to `flights-speccer` to be cut, never retried as is.
- Reviewers habituate to agent output (approval +14.5 points, inline comments −22% over ten deciles of exposure) and human detection collapses past about 400 lines. The ruling still reviews the flight's whole diff once, on cost: a review per executor re-read one growing diff 38 times in one flight. The lander raises its review effort with the diff's size, and a flight too large to review in one pass is a flight to cut.
- A fresh sub-agent costs about 54K tokens of cold start; siblings dispatched in one message share the cached prefix at a tenth of the price. One-message dispatch is also the cache law.

## Measuring a run

| Measure | Healthy | A broken run shows |
| --- | --- | --- |
| Orchestrator peak context | Tens of K: the index, the briefs, the verdicts | Task file content or repository files read into it |
| Orchestrator calls | About two per task plus the landing | Polling, or per-step reads |
| Executors dispatched vs returns | Equal | A silent loss |
| Ready tasks left waiting | None, except for a free slot | A batch that waited for a sibling |
| Executor calls | Between about 40 and 80 | Under: tasks cut too small; over: a task that should have been cut, or a cap that never fired |
| Revising rounds | Rare | Specs that contradict themselves, or a stale directory |
| Review findings left in a return | Few, all outside the task's files | A finding inside the task's files left unfixed |

## Surfaces that stay in sync

| Surface | File | Holds |
| --- | --- | --- |
| The agent | `templates/global/agents/flights-orchestrator.md` | The manual; runs at mechanical (`sonnet`), effort `high` |
| The containers | `templates/global/commands/flights/orchestrate-{nested,live,cross-harness}.md` | The substitutions, nothing of the manual restated; the nested command's road for a `BLOCKED` ruling |
| The fleet prompt | `pfm/harness-prompts/share/tail.md` § Orchestration | The ladder's third rung ends here; the universal laws; the hand's laws for chat seats; the lander as the only review |
| The spec writer | [`flights-speccer`](flights-speccer.md) | The index this agent dispatches from (`files` included), the `DISPATCH` line of its return, the revising call |
| The adopter contract | `CLAUDE.md` and `templates/project/CLAUDE.md` | The executor's first move on a brief naming a task file; in this repository's `CLAUDE.md` also the fenced-flight paragraph under § Process |
| The executors and the lander | [`flights-executors`](flights-executors.md), [`flights-lander`](flights-lander.md) | What the brief no longer restates; the `RETRO` line in every return |

## Open items

- The caps (80 tool calls per executor, 150 per lander, ten executors in flight at once) are first values, held by prompt alone: no hook enforces them, by ruling. Measure against the next flight.
- The stale bound for the cross-harness container (first value 20 minutes without a status change) is agentmux's number, not ours.
- Whether `/code-review` runs inside a `flights-lander` sub-agent (the Skill tool at depth); verify on the next nested flight, and measure the gate's cost per flight.
- Landers carry no `shares`: each flight has its own worktree or runs on the main branch, so two landers of one flight never contend. No gate points inside a large flight: the gate runs once, at the landing.
- A worktree flight's merge: gitter's MERGE Flight mode expects one merge-gating review report with per-finding statuses, and a flight carries its review findings in `gate-{project}.md` instead; reconcile in the gitter pass.
