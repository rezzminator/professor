# general-orchestrator

`general-orchestrator` runs a batch of clear work to done without a flight's ceremony: the caller hands it everything it knows, it cuts the batch into tasks with a dependency tree, dispatches one short executor per task, verifies each return, and reports once. No speccer, no task files, no lander. This file holds the family's decisions and the orchestrator's; the executors' live in [general-executors.md](general-executors.md).

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
- [Surfaces that stay in sync](#surfaces-that-stay-in-sync)
- [Evidence](#evidence)

## Why it exists

The sub-agent rule of the project contract offered three roads: a brief naming a task file, one task the agent can see how to do, or a flight. Most work the main chat hands out is none of them: a batch of tasks, each already clear, too many for one agent and too simple for a speccer. With no road for it, the main chat either sends one agent at the whole batch or dispatches by hand, and both run long. A week of transcripts shows it (Evidence): the sub-agents past 45 calls are mostly untyped hand dispatches, while every agent with a narrow protocol stays short. This family is the missing road, and the enforcement arm of the 45-call law: work bigger than one short agent goes here or to a flight, never to a longer agent.

## The family

| Member | Kind | Does | Runs at |
| --- | --- | --- | --- |
| `general-orchestrator` | agent | Cuts a batch into tasks with a dependency tree, dispatches, verifies, closes, returns once | smart (`opus`), effort `high` |
| `general-mechanical-executor`, `general-smart-executor` | agents, one body | One task each, from an inline brief; picked by the task's rating | `sonnet` and `opus`, effort `medium` |

The orchestrator runs at smart, where `flights-orchestrator` runs at mechanical: the flights orchestrator executes a plan the speccer made, while this one makes the plan itself, and cutting work is judgment. Judgment never delegates downward.

## The boundary

Work takes the lowest of three rungs that fits, and a higher rung needs its named reason. The ladder lives in the fleet prompt's § Orchestration for a main chat and in the project contract's sub-agent first move for a sub-agent, which never sees the fleet prompt.

1. Direct: the solution is in hand and the work fits about 80 calls — the caller does it by hand when it is small, otherwise sends one or two agents of at most 45 calls each, in sequence or in parallel. A change one tool call performs — a codemod, `gopls rename`, one `sed` over a named file list followed by the build — is direct work, however many files it touches. A small failure an agent can read to its cause is direct work too.
2. `general-orchestrator`: the solution is in hand, but the volume is more than one agent finishes in 45 calls — many tasks in one domain, each with nameable files. ✓ "Add the timeout flag to each of the 12 subcommands." ✓ "Port these 8 test files to the new helper." ✓ "Update every doc that names the renamed flag."
3. A flight: the solution is not in hand — a design to choose, a failure whose cause is unknown, a contract crossing projects — and the work is large. A clear batch past the orchestrator's size (below) is split, never a flight. ✗ "Take the four red lanes to green" here: each red needs a diagnosis first.

Flights is the top rung, not the default: the measured failure this family answers is a dozen known edits sent through a speccer, an orchestrator, executors and a lander for hours. The orchestrator checks the rung before it dispatches: work that fits the direct rung goes back to the caller with the one- or two-agent cut named, work whose solution is not in hand goes back as a flight.

A rename across many modules is the trap case. Every module's importers are shared files, so parallel tasks collide; the tool call that renames them all at once is the answer, and the orchestrator's first question is always whether one call does the whole batch.

## Input

From the caller, in the prompt:

- the work, in the caller's words, and everything the caller already holds: files, findings, constraints, rulings;
- the acceptance: the check that proves the batch done (a command, a test run, a grep that must come back empty);
- the standing rules, the project's testing manual path when code changes, and the worktree when one exists.

No directory, no index, no task files. The orchestrator's own context is the ledger; a batch is short enough to need no other.

## The run

1. Route, before anything is dispatched: work on the direct rung (it fits about 80 calls, or one tool call does it whole) returns `BLOCKED {batch}: direct` with the one- or two-agent cut named; work whose solution is not in hand returns `BLOCKED {batch}: flights` with the cut it would make; clear work past about fifteen tasks returns `BLOCKED {batch}: split` with the cut into batches of about fifteen, each its own `general-orchestrator`.
2. Survey. Read what the caller named; find each task's files with a search, never a read of the area around them. Record `git status --short` before the first dispatch.
3. Cut. Each task is one deliverable with its own files and its own check, sized to finish within an executor's 45 calls. The dependency tree has two edges: a task that consumes another's output needs it, and two tasks that touch one file never run at once. Rate each task: `mechanical` only for repetitive, straightforward work that needs no reasoning to do right (the same known edit across files, a rename, a move, a named command run, code whose every line the brief fixes); `smart` for any bounded judgment inside the task's own files, and for every task whose deliverable is a document, a prompt, a spec or a report, however exactly the brief words it. A written document always carries reasoning its reader acts on; an exact brief does not make it mechanical. A task that needs a design or a diagnosis is not this family's: it goes back to the caller as `BLOCKED`, and the rest of the batch runs.
4. Dispatch every task whose needs are done, in one message, as many at once as the harness admits. The task's rating picks the agent type; the spawn carries no model override.
5. Wait: end the turn; each return arrives on its own. The agent's frontmatter hook `pfm internal orchestrator-wait` denies a Bash call that only waits.
6. Verify each return against the disk: the first-line token, `git diff --stat` showing the change inside the task's files, no changed file outside every task's files and the step-2 record (parallel tasks and a tree already dirty both change files the one task never touched), the task's check line quoted from what ran.
7. React (below), then dispatch what the return made ready.
8. Close: the caller's acceptance check once, over the whole batch, watched. No review and no full suite unless the caller ordered one; a commit the caller asked for goes to `gitter`.
9. Return once.

## The brief

Inline in the spawn prompt, the five parts of the briefing contract and nothing the executor's body already holds:

1. The task's goal in one sentence and the artifact it returns.
2. Its files, and explicitly what is not its.
3. The exact change: the symbols, the lines, the command; for a `smart` task, the judgment it owns and its bounds.
4. The check that proves it, and the testing manual path when code changes.
5. The standing rules the caller passed, and what already landed that this task needs.

## Reactions

| Return | Reaction |
| --- | --- |
| `DONE` verified | Record it; dispatch what it made ready |
| `DONE` not verified: a file outside the task, a check line missing | Treated as `FAILED` with that cause |
| `FAILED {id}: cap` | A fresh executor of the same rating continues from the handoff; a second cap on one task means the task was cut too big: re-cut it once |
| `FAILED` with a cause | One re-dispatch with the cause in a changed brief, never the same brief twice; a second red returns to the caller as `BLOCKED` with both causes |
| `SPEC-DRIFT` | The premise was wrong: re-cut the task once from what the executor found; a second drift returns to the caller |
| `BLOCKED` with a question | Answered from what the caller handed over, else carried to the caller; the rest of the batch keeps running |

## The 45-call law and the batch size

Every sub-agent finishes within 45 calls (the fleet prompt's Orchestration law), the orchestrator included. The survey and the close take about ten; each task costs about two more (its share of a dispatch message, and a verification). So one orchestrator carries about fifteen tasks. A larger batch of clear work is split, never sent to a flight: the orchestrator returns `BLOCKED {batch}: split` before dispatching, naming the cut into batches of about fifteen.

## What it does not do

- No speccer and no task file: the brief is the spec, written by a smart-tier agent that just surveyed the files.
- No lander and no review: the caller's acceptance check is the landing. A caller that needs an independent review orders one, and it runs once, over the whole batch.
- No edits of its own and no git writes. The orchestrator fixes nothing by hand; `gitter` commits when the caller asks.
- No nested orchestrator and no executor that dispatches: at its cap an executor returns to the orchestrator, and at its own cap the orchestrator returns to its caller.

## The return

First line `DONE {batch}`, `PARTIAL {batch}: {n} blocked` or `BLOCKED {batch}: {question}`. Then one row per task (task · rating · verdict · files), the acceptance check's verdict line as printed, every blocked task with its cause, and the executors' `RETRO` lines deduplicated. Dispatched against returned is stated as a count.

## Surfaces that stay in sync

| Surface | File | Holds |
| --- | --- | --- |
| The agent | `templates/global/agents/general-orchestrator.md` | The executable wording |
| The wait guard | `pfm internal orchestrator-wait`, attached in the agent's frontmatter | A Bash call that only waits (`echo`, `printf`, `true`, `:`, `sleep N`) is denied; design in [hooks.md](../hooks/hooks.md#agent-attached-hooks-not-machine-global) |
| The executors | [general-executors.md](general-executors.md) | The hands and their tiers |
| The fleet prompt | `pfm/harness-prompts/share/tail.md` | § Orchestration: the 45-call law and the three-rung ladder, for a main chat |
| The project contract | `CLAUDE.md`, `templates/project/CLAUDE.md` | The sub-agent first move: the same ladder and the 45-call cap, for a sub-agent |
| The roster | `docs/BLUEPRINT.md`, `docs/SETUP.md`, `scripts/check-agent-roster.mjs` | The agents listed and checked |

## Evidence

One week of sub-agent transcripts across every account, counting model requests per agent:

- 2,962 sub-agents; 270 past 45 calls, 86 past 80.
- 194 of the 270 were untyped hand dispatches: `general-purpose` 94 (longest 188 calls, peak context 465K), `fork` 66, `claude` 34 (longest 200).
- Narrow protocols stayed short without a cap: `sub-rr` median 5 (longest 20), `scribe` 7 (26), `tracer` 14 (42), `sub-tracer` 13 (24).
