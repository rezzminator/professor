# general-orchestrator

`general-orchestrator` runs a batch of problems to done without a flight's ceremony: the caller hands it everything it knows, it cuts the batch into problems with a dependency tree, dispatches one [`general-foreman`](general-foreman.md) per problem, or a [`general-executor`](general-executor.md) per change its caller already decided, verifies each return, and reports once. No speccer, no task files, no lander, no spec: each foreman works its problem out live. This file holds the family's decisions and the orchestrator's; the foreman's live in [general-foreman.md](general-foreman.md), its hand's in [general-executor.md](general-executor.md).

A change lands in this design doc first, then in the template, then in every surface listed under [Surfaces that stay in sync](#surfaces-that-stay-in-sync).

## Contents

- [Why it exists](#why-it-exists)
- [The family](#the-family)
- [The boundary](#the-boundary)
- [Input](#input)
- [The run](#the-run)
- [The brief](#the-brief)
- [Reactions](#reactions)
- [The 45-call law and the batch size](#the-45-call-law-and-the-batch-size)
- [What it does not do](#what-it-does-not-do)
- [The return](#the-return)
- [The descriptions](#the-descriptions)
- [The bench](#the-bench)
- [Surfaces that stay in sync](#surfaces-that-stay-in-sync)
- [Evidence](#evidence)

## Why it exists

Most work the main chat hands out is neither one task nor a flight: a batch of problems, each one agent's size, too many for one agent and too live for a speccer. With no road for it, the main chat either sends one agent at the whole batch or dispatches by hand, and both run long. A week of transcripts shows it (Evidence): the sub-agents past 45 calls are mostly untyped hand dispatches, while every agent with a narrow protocol stays short. This family is the road, and the enforcement arm of the 45-call law: work bigger than one short agent goes here or to a flight, never to a longer agent.

## The family

| Member | Kind | Does | Runs at |
| --- | --- | --- | --- |
| `general-orchestrator` | agent | Cuts a batch into problems with a dependency tree, dispatches one foreman each (an executor for a change already decided), verifies, closes, returns once | `opus`, effort `high` |
| `general-foreman` | agent | Works one problem out live: digs to the cause, decides, builds or briefs, proves at the Goal | `opus`, effort `high` |
| `general-executor` | agent, `GENERAL-ONLY` | Builds one decided change: a foreman's, or one the orchestrator's caller already decided | `claude-sonnet-5-5`, effort `high` |

Judgment never delegates downward: the orchestrator cuts, the foreman decides, the executor only builds what the decision fixed. A change the caller already decided — the files and the exact edit stated — needs no foreman, and the orchestrator passes it to an executor as given. The general family keeps its own tiers; the flight executors' tier table does not bind it.

## The boundary

The ladder lives in the fleet prompt's § Orchestration for a main chat and in the project contract's sub-agent first move for a sub-agent, which never sees the fleet prompt. The orchestrator checks its own rung before it dispatches:

1. One or two problems are the caller's to send straight to `general-foreman`: the orchestrator returns `BLOCKED {batch}: direct` with the cut named.
2. Many problems in one domain, each one foreman's size — its own 45 calls plus two executors — run here. ✓ "Fix these nine reported bugs in the installer." ✓ "Add the timeout flag to each of the 12 subcommands." ✓ "Update every doc that names the renamed flag."
3. A problem no foreman finishes alone — a design spanning many units that must be specced whole before anything is built — goes to a flight: `BLOCKED {batch}: flights` with the cut named. A batch past the orchestrator's size is split, never a flight.

A rename across many modules is the trap case. Every module's importers are shared files, so parallel problems collide; the tool call that renames them all at once is the answer, and it belongs to one foreman.

## Input

From the caller, in the prompt:

- the work, in the caller's words, and everything the caller already holds: files, findings, constraints, rulings;
- the acceptance: the check that proves the batch done (a command, a test run, a grep that must come back empty);
- the standing rules, the project's testing manual path when code changes, and the worktree when one exists.

No directory, no index, no task files. The orchestrator's own context is the ledger; a batch is short enough to need no other.

## The run

1. Route, before anything is dispatched (§ The boundary); past about fifteen problems, `BLOCKED {batch}: split` with the cut into batches of about fifteen, each its own `general-orchestrator`.
2. Survey only what the cut needs: one search per problem for its files, never a read of the area around them — the foremen dig, and a survey that reads the code pays for the same reading twice. Every search and `git status --short`, recorded before the first dispatch, go out in one message.
3. Cut. Each problem is one deliverable with its own area and its own check. The dependency tree has two edges: a problem that consumes another's output needs it, and two problems whose areas share a file never run at once.
4. Dispatch every problem whose needs are done: one `general-foreman` call per problem, or one `general-executor` call for a change the caller already decided, all in the same message, no model override; each prompt opens with its effort line (§ The brief). Calls sent in separate messages start the foremen one wait apart. At most six in flight: a foreman may run two executors, and Claude Code admits about twenty sub-agents at once and refuses the rest silently.
5. Wait: end the turn; each return arrives on its own. The agent's frontmatter hook `pfm internal orchestrator-wait` denies a Bash call that only waits.
6. Verify each return against the disk, every return in hand in one chained call: the first-line token, `git diff --stat` showing the change inside the problem's files, no changed file outside every problem's files and the step-2 record (parallel problems and a tree already dirty both change files the one problem never touched), the Goal's check line quoted from what ran. `UNPROVEN` and `OUTSIDE` lines are carried into the return; an `UNPROVEN` one is never counted as verified.
7. React (below), then dispatch what the return made ready.
8. Close: the caller's acceptance check once, over the whole batch, watched. No review and no full suite unless the caller ordered one; a commit the caller asked for goes to `gitter`.
9. Return once.

## The brief

Inline in the spawn prompt, a problem and never a solution, and nothing the foreman's body holds: its law and its return shape reach it through its own file, and a brief that restates them pays for them twice.

1. Its effort, the prompt's first line: `[effort: medium]` when the survey puts the problem inside one package or module and no invariant is held at several doors; `[effort: high]` otherwise. Thinking is billed as output and stays in context for every later call: on the bench batch, foremen at `high` thought about twice as much as the `general-purpose` forks their parent had started at `medium`, for the same local fixes, and cost up to half again as much. The line is a required item rather than a condition in the dispatch step: written as a condition there, the orchestrator never weighed it, while its briefs reproduced this list item by item.
2. The problem and its Goal: what is observably true once it is solved.
3. Its area — the files the survey found, the boundary of its changes and never a reading list — and explicitly what is not its.
4. What the survey found: facts with `path:line`, never a fix.
5. The check that proves the Goal, and the testing manual path when code changes.
6. The standing rules the caller passed, the worktree, what already landed that this problem needs, and the sibling problems running in the same worktree with their areas. A foreman whose build breaks on a sibling's half-written file reads that red as its own unless the brief tells it who is next door; the foreman's body leaves such a red alone and reruns after its next change.

## Reactions

| Return | Reaction |
| --- | --- |
| `DONE` verified | Record it; dispatch what it made ready |
| `DONE` not verified: a file outside the problem, a check line missing | Treated as `FAILED` with that cause |
| `FAILED {id}: cap` | A fresh foreman continues from the handoff; a second cap on one problem means it was cut too big: re-cut it once |
| `FAILED` with a cause | One re-dispatch with the cause in a changed brief, never the same brief twice; a second red returns to the caller as `BLOCKED` with both causes |
| `SPEC-DRIFT` from an executor | The change was not decided after all: it goes to a foreman as a problem, with what the executor found |
| `BLOCKED` with a question | Answered by `SendMessage` to the same foreman or executor from what the caller handed over, else carried to the caller; the rest of the batch keeps running |

## The 45-call law and the batch size

Every sub-agent finishes within 45 calls (the fleet prompt's Orchestration law), the orchestrator included. The survey and the close take about ten; each problem costs about two more (its share of a dispatch message, and a verification). So one orchestrator carries about fifteen problems. A larger batch is split, never sent to a flight: the orchestrator returns `BLOCKED {batch}: split` before dispatching, naming the cut into batches of about fifteen.

## What it does not do

- No speccer, no task file and no spec: the brief is a problem, and the foreman works it out.
- No lander and no review: the caller's acceptance check is the landing. A caller that needs an independent review orders one, and it runs once, over the whole batch.
- No edits of its own and no git writes. The orchestrator fixes nothing by hand; `gitter` commits when the caller asks.
- No nested orchestrator: a foreman dispatches only its own executors, and at its cap returns to the orchestrator; at its own cap the orchestrator returns to its caller.

## The return

First line `DONE {batch}`, `PARTIAL {batch}: {n} blocked` or `BLOCKED {batch}: {question}`. Then one row per problem (problem · verdict · files), every `UNPROVEN` line, every `OUTSIDE` line (a defect a foreman found beyond its problem and left untouched), the acceptance check's verdict line as printed, every blocked problem with its cause, and the foremen's `RETRO` lines deduplicated. Dispatched against returned is stated as a count.

## The descriptions

A description routes a main chat only: a sub-agent's Agent tool lists no roster, so a sub-agent reaches another agent only by a name its own file or the project contract gives it. The family's descriptions carry one job, taking repo work from `general-purpose`, whose own description claims "executing multi-step tasks":

- `general-foreman` names `general-purpose` as what it replaces for any repo fix or build, and claims the single sweep ("rename X everywhere"), which is one foreman's work and never a batch.
- `general-orchestrator` claims three or more separate problems, and its examples name separate problems, never one change repeated across files: with "add X to every Y" as its example, a cross-file rename routed to the orchestrator, which would have bounced it back as `BLOCKED: direct`.
- `general-executor` is `GENERAL-ONLY` and points a main chat's change to `general-foreman`.

A routing probe measured them: twelve asks, each to a fresh agent holding the main chat's roster as a file and naming the agent it would pick. The earlier descriptions routed 11 of 12, the rename going to `general-purpose`; the current ones route 12 of 12, with questions, searches and research still going to `tracer`, `Explore` and `rr`.

## The bench

Three replayed pfm fixes in one worktree (`mcp-ledger-link`, `tmux-seat`, `wayback-label`), each in its own package, briefed as symptoms only, with the identical prompt to `general-purpose`. Priced as in [general-foreman.md](general-foreman.md#the-bench).

| Runner | Runs | Cost | Wall | Peak context, largest agent | Correct |
| --- | --- | --- | --- | --- | --- |
| `general-purpose`: solved one problem itself, forked two | 2 | $2.03, $2.72 | 251s, 356s | 88K, 94K | 3 of 3, both runs |
| `general-orchestrator`, its last two revisions | 2 | $2.49, $2.92 | 266s, 352s | 79K, 84K | 3 of 3, both runs |
| `general-orchestrator`, its first three runs | 3 | $3.51, $3.42, $3.26 | 401s, 408s, 366s | up to 90K | 3 of 3, every run |

The last two revisions differ by one change, the effort line moved into the brief, and both predate the foreman's per-hit `DOORS` line, so the final wording has not run the batch.

On a three-problem batch the family costs about 14% more than `general-purpose`, the orchestrator's own share (about $0.43 across 11 requests); wall time is even and every agent's context stays smaller. The `general-purpose` parent spent its coordination on real work by solving one problem itself; the orchestrator builds nothing, and in exchange verifies every return against the disk, watches the acceptance once and carries every `UNPROVEN` and `OUTSIDE` line. The survey rule, the one-message dispatch, the effort line and a brief free of the foreman's own law all came out of these runs (§ The run, § The brief).

## Surfaces that stay in sync

| Surface | File | Holds |
| --- | --- | --- |
| The agent | `templates/global/agents/general-orchestrator.md` | The executable wording |
| The wait guard | `pfm internal orchestrator-wait`, attached in the agent's frontmatter | A Bash call that only waits (`echo`, `printf`, `true`, `:`, `sleep N`) is denied; design in [hooks.md](../hooks/hooks.md#agent-attached-hooks-not-machine-global) |
| The foreman | [general-foreman.md](general-foreman.md) | One problem worked out live, its hand, its return |
| The fleet prompt | `pfm/harness-prompts/share/tail.md` | § Orchestration: the 45-call law and the three-rung ladder, for a main chat |
| The project contract | `CLAUDE.md`, `templates/project/CLAUDE.md` | The sub-agent first move: the same ladder and the 45-call cap, for a sub-agent |
| The roster | `docs/BLUEPRINT.md`, `docs/SETUP.md`, `scripts/check-agent-roster.mjs` | The agents listed and checked |

## Evidence

One week of sub-agent transcripts across every account, counting model requests per agent:

- 2,962 sub-agents; 270 past 45 calls, 86 past 80.
- 194 of the 270 were untyped hand dispatches: `general-purpose` 94 (longest 188 calls, peak context 465K), `fork` 66, `claude` 34 (longest 200).
- Narrow protocols stayed short without a cap: `sub-rr` median 5 (longest 20), `scribe` 7 (26), `tracer` 14 (42), `sub-tracer` 13 (24).
