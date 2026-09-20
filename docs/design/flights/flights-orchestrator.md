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
- [The universal laws](#the-universal-laws)
- [Not part of the design](#not-part-of-the-design)
- [What the evidence says](#what-the-evidence-says)
- [Measuring a run](#measuring-a-run)
- [Surfaces that stay in sync](#surfaces-that-stay-in-sync)
- [Open items](#open-items)

## Why one manual, held by a sub-agent

A main chat outlives every agent, so its context is the dearest in the family. The orchestration of a flight is a loop of dispatch, wait, verify, react, dispatch that runs for the whole flight; run in the main chat it re-sends the chat's entire history on every step. The loop therefore lives in a sub-agent at spec-execution tier (`sonnet`) that holds only the index and the verdicts, and the main chat hears from it once.

The same body is the only description of the protocol. A second copy for the live or cross-harness container would drift; the commands for those containers substitute the transport (how an executor is spawned, briefed, waited for, questioned, stopped) and read everything else from the agent file.

## Input

| Input | Rule |
| --- | --- |
| The flight directory | Required. Work that arrives without one goes to `flights-speccer` first, and its return is the index |
| Standing rules the executors work under | What the project contract does not carry: the worktree, the fenced build command, the checks by command, anything the caller adds for this flight. Pasted into every executor brief, never into a task file. The `CLAUDE.md` / `AGENTS.md` contract reaches every executor from the harness and is never pasted or named |
| The executor agent type | Used when the project has one; otherwise `general-purpose`. It must carry the Skill tool, or the review the brief orders cannot run |
| A worktree | Used when the flight runs outside the checkout; otherwise the checkout |
| The cap | Tool calls and minutes per executor, and executors in flight at once; absent, the first values (120 calls, 60 minutes, ten at once) |
| The landing | Which checks run after the last task, whether gitter commits. Absent: the standing checks once, no commit |

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
- the cap: "about {n} tool calls or {m} minutes; past either, stop and return `FAILED {id}: cap` with what landed";
- the tests: "write the covering tests yourself, one per `Done when` row, whatever your agent card says about who writes tests; this brief is the ask";
- the review: "your last step before the return is `/code-review low` over your own change; fix every finding inside your task's files, and report a finding outside them untouched";
- the cadence: "Report once, when done: first line `DONE {id}`, `FAILED {id}: {why}`, `SPEC-DRIFT {id}: {what}` or `BLOCKED {id}: {question}`; then the files changed, the test that covers each `Done when` row and the proof they ran, the review's findings and what you fixed, what you adapted, defects found, what you could not reach. The only other message is a real question or a blocker. Never routine progress, never a diff, a log or a file's contents in a message.";
- "Where the spec and the code disagree on a detail, reach the Goal and say what you changed; where you cannot proceed without a decision, ask for it instead of guessing.";
- "Git is read-only for you."

Nothing else: the task file is the spec, and the brief never restates it. The executor runs at the model its agent type pins; with no pin, `mechanical` runs at spec-execution (`sonnet`) and `hard` at frontier-judgment (`opus`).

## Verdicts are evidence, not truth

An executor's return is a claim. The orchestrator matches the first line's token and then verifies:

- `DONE`: the return names what changed, a covering test per `Done when` row and the proof they ran, the review's findings and what was fixed, and `git diff {baseline} --stat -- {the index's files}` shows a change: the files come from the index row, never from the return, so the judge is never the judged. A return that claims done with no proof, or with nothing changed, gets one question back to the same executor; a second such return is recorded `FAILED`.
- `FAILED`, `SPEC-DRIFT`, `BLOCKED`: recorded as returned; the reaction is in [Situations](#situations).
- No token on the first line: one question back asking for the return in shape; a second shapeless return is `FAILED`.

The baseline is the commit recorded in the `run.md` header, so a resumed flight judges against the same tree the flight started from.

## Situations

Every situation the manual answers, with who acts. The orchestrator fixes nothing itself and patches no task file.

| Situation | Reaction |
| --- | --- |
| `DONE`, verified | `run.md` line names what was adapted or `as specified`; dispatch what it unblocked |
| `DONE` without proof or without a change in git | one question back to the same executor; a second such return → `FAILED` |
| `FAILED` (including a cap) | start nothing that needs it; send `flights-speccer` the report, what already landed and the completed ids; its rewritten index cuts the task smaller or re-approaches it; dispatch from the new index. The same task file is never re-run unchanged |
| `SPEC-DRIFT` | the same road as `FAILED`, with the drift report as the reason |
| `BLOCKED` with a question only the user can answer | record `BLOCKED`; every other task continues; the question travels in the return. The ruling comes back as a revising `flights-speccer` call: the live container makes it on the answer; the nested container's caller makes it after the return, then resumes the run naming the revised ids |
| A question from an executor the index or the brief can answer | answer it by message to the same executor |
| A question from an executor nobody but the user can answer | `BLOCKED` for that task, as above |
| The concurrency cap is reached | never reported by the harness: the count of in-flight executors is the only guard. Hold the task; dispatch it as the next return lands |
| An executor never returns | seen only when something wakes the loop (a sibling's return; in the live and cross-harness containers, the user): a `CLAIMED` line older than the cap's minutes with no verdict. Named in `DISPATCHED` as a missing return; its task stays `CLAIMED`; the flight lands without it and the return says so. When it was the last executor, nothing wakes a nested orchestrator: the user re-runs the container, and the resume rule treats the task as not started |
| A silent seat past the stale bound (cross-harness only) | `STALE {id}` in `run.md` and the return; the seat is never killed or re-dispatched blind |
| A standing check fails at landing | unspecified work: a revising `flights-speccer` call with the check's output and the completed ids; its task files dispatch like any other |
| A review finding in a return outside the task's files | `NOTES`; never a fix by the orchestrator |
| A spec fault the orchestrator can see (two decisions contradict, an index row without a file) | a revising `flights-speccer` call; never a patch, never a ruling written beside the directory |

## Review

The review is each executor's last step, not the orchestrator's. The brief orders `/code-review low` over the executor's own change before it returns: `/code-review` spawns its own fresh reviewers, so the eye is cold, while the fix stays with the hands that made the change and still hold its context. Nothing in the flight is reviewed by an agent the orchestrator briefs, and nothing is graded by the orchestrator.

- The executor fixes every finding inside its task's files before it returns, and reports a finding outside them untouched: a sibling may be editing that file, and the executor's scope is its `Files`.
- The return carries the findings and what was fixed; the orchestrator verifies a `DONE` against that line like any other claim and carries findings outside the flight to `NOTES`.
- A finding whose fix is to edit the spec is a `SPEC-DRIFT` from the executor, never a patch: the spec is `flights-speccer`'s.
- `hard` and `mechanical` differ only in the executor's model; the review is the same pass for both, and the landing runs no review of its own.
- The executor's agent type must carry the Skill tool; a project executor without it runs no review, and its return says so.

## `run.md`

One file, `{flight directory}/run.md`. A header line on creation — `flight {directory} · baseline {sha} · {date}` — then one line per event: `{id} {CLAIMED|DONE|FAILED|SPEC-DRIFT|BLOCKED} · {one line}`; the cross-harness container adds `STALE` as a substitution, and the manual never names it. A `CLAIMED` line names the executor and the time; a `DONE` line names what the executor adapted, or `as specified`, and is what downstream briefs carry; a `FAILED` or `SPEC-DRIFT` line names the reason and the revising round it triggered. The file is the resume point and the ledger an audit reads. Nothing else is written by the orchestrator: no reports, no per-step lines, no rulings.

## Landing

After the last verdict the standing checks run once, and the result recorded is the one the orchestrator watched print; a command emitted is not a check run. A failing check is unspecified work and goes to `flights-speccer` as a revising call. When the brief asks for a commit, `gitter` makes it: Phase COMMIT in the checkout the flight ran in (the worktree, or the project), the files named as the union of the index's `files` over the `DONE` tasks, the message summarising the flight; the return carries the sha. The landing never merges: a worktree flight reaches `develop` by the user's own gitter MERGE order after the return. The orchestrator runs no fix itself.

## The return

```text
FLIGHT {directory}
{id} · {verdict} · {one line}          one row per task
CHECKS {command → result} | none
REVIEW {n} findings, {m} fixed, {k} left, from the returns | none
COMMIT {sha} | none
DISPATCHED {n} executors, {m} returns, {k} revising rounds
BLOCKED {id}: {question} | none
NOTES {up to five lines} | none
```

`DISPATCHED` reconciles agents sent against returns received; a missing return is named, never silent. `REVIEW` sums the executors' own review lines; the audit checks it against their transcripts. `NOTES` carries the findings the executors reported outside their files and anything the user should know that fits no row.

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
| A cold reviewer agent per `hard` task, briefed by the orchestrator and graded by it | Replaced by `/code-review low` in the executor: fresh reviewers for the cold eye, the fix with the hands that hold the context, nothing for the orchestrator to grade and no report to route |
| Trains, waves, a worktree per wave, BUILD-GREEN handshakes, an entry-point census, a conformance pass | The pipeline this agent replaces |

## What the evidence says

The rulings above rest on measured results, collected in the runtime research of 2026-09-20:

- Fan out only file-disjoint work: multi-agent gains +80.8% on decomposable work and loses 70% on sequential work; cross-agent pull requests conflict at 41.7% against 19.8% for one agent. The index's `needs` and `shares` are that rule made mechanical.
- Keep every dispatch single-turn and self-contained: models lose 39% on average when a task is spread over turns.
- Completion is a token matched in code, never text interpreted: every mature runtime (a finish action, a literal sentinel, a terminal event) does this; every runtime that scrapes text calls it a heuristic.
- Every cap in the field is hand-picked; the useful ones name their exit instead of dying as a generic error. The cap here returns `FAILED {id}: cap` with what landed.
- Decompose on failure rather than up front: +33 points over fixed decomposition. `FAILED` goes to `flights-speccer` to be cut, never retried as is.
- Reviewers habituate to agent output (approval +14.5 points, inline comments −22% over ten deciles of exposure) and human detection collapses past about 400 lines: the unit of review is one task's change, read by `/code-review`'s fresh agents, never the flight's whole diff.
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
| The agent | `templates/global/agents/flights-orchestrator.md` | The manual; runs at spec-execution (`sonnet`), effort `high` |
| The containers | `templates/global/commands/flights/orchestrate-{nested,live,cross-harness}.md` | The substitutions, nothing of the manual restated |
| The fleet prompt | `templates/harness-prompts/share/tail.md` § Orchestration | A batch goes to this agent; the universal laws; the hand's laws, `/code-review low` last, for chat seats |
| The spec writer | [`flights-speccer`](flights-speccer.md) | The index this agent dispatches from (`files` included), the `DISPATCH` line of its return, the revising call |
| The adopter contract | `CLAUDE.md` and `templates/project/CLAUDE.md` | The executor's first move on a brief naming a task file; the fenced-flight paragraph under § Process |
| The container commands | `templates/global/commands/flights/orchestrate-{nested,live,cross-harness}.md` | The substitutions; the nested command's road for a `BLOCKED` ruling |
| The executor's card | `dev` and any project executor | The Skill tool, so `/code-review low` can run |

## Open items

- The cap's first values (120 tool calls, 60 minutes per executor; ten executors in flight at once) are guesses. Measure against the next flight; a hard cap would be a `PreToolUse` hook counting an agent's calls and denying past the bound with the same return order.
- The stale bound for the cross-harness container (first value 20 minutes without a status change) is agentmux's number, not ours.
- Whether a task's outcome includes its tests, making one executor write both, or a `qa` pass follows; today the executor writes both, because the covering test per `Done when` row is the proof it must return.
- Whether `/code-review low` runs inside a sub-agent executor (the Skill tool at depth); verify on the next nested flight, and measure its cost per executor.
- A worktree flight's merge: gitter's MERGE Flight mode expects one merge-gating review report with per-finding statuses, and a flight carries its review findings in the executors' returns instead; reconcile in the gitter pass.
