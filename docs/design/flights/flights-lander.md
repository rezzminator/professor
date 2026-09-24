# flights-lander

`flights-lander` is the last step of a flight: one fresh agent per project the flight touched, which runs the project's checks, reviews the flight's whole diff, attacks the change with tests written to break it, and fixes what it finds itself. It is the flight's one review and its only independent tester; the executors write their own covering tests and run no review.

Decisions live in this file. The executable wording lives in [`templates/global/agents/flights-lander.md`](../../../templates/global/agents/flights-lander.md).

## Contents

- [Why it exists](#why-it-exists)
- [Input](#input)
- [The run](#the-run)
- [It fixes what it finds](#it-fixes-what-it-finds)
- [What it does not do](#what-it-does-not-do)
- [Bounds](#bounds)
- [The ledger and the return](#the-ledger-and-the-return)
- [Context budget](#context-budget)
- [The accepted risk](#the-accepted-risk)
- [Surfaces that stay in sync](#surfaces-that-stay-in-sync)
- [Evidence](#evidence)

## Why it exists

An executor that tests its own code is biased toward making it pass. The lander has a clean context and one mission: break the change. Measured on seven bug ledgers of a live adopter (63 findings, 25 fix diffs read): 43% of what the independent QA found were defects in production code, and 53% could be found only by running something — races, real queue and database wiring, the shape of data at runtime — which no reading review reaches. The review is kept beside it because it is 11 to 65 times cheaper per finding and the two barely overlap (6 shared findings of 104).

## Input

From the orchestrator's brief, and nothing more:

- the flight directory: `index.md` for each task's `files`, the task files' `Goal` and `Done when` as the statement of intent the attack reads against;
- the baseline sha from `run.md`'s header: the diff is `git diff {baseline}` over the union of the `DONE` tasks' `files`, plus the uncommitted tree;
- the project and the path of its [testing manual](testing-manual.md);
- the standing rules and the worktree.

The agent is pinned `model: opus`, `effort: high`.

## The run

1. Read the testing manual, the index and the diff. The task files are read for `Goal` and `Done when` only.
2. Open the gate: format, lint, type check and the full suite, once, each watched. What they report is fixed.
3. `/code-review {effort}` over the flight's diff, at a level the lander sizes itself from `git diff {baseline} --stat` over the flight's files: `low`, or `medium` beyond 15 files or 800 changed lines; a `smart` task changes nothing, and a level above `medium` runs only on the user's own order carried in the brief. The measured cases: a lander that stepped a `smart` task past `high` into `max` spent more on one review than on every executor of its flight together, and one capped at `xhigh` still launched `xhigh` on a small port because the diff it sized carried a pre-existing uncommitted feature. Absent that order, the brief names no effort. The review runs forked in the background: the lander ends its turn and takes the findings from the review's return; a lander that looped over transcript mtimes to guess the review's end waited on a heuristic any busy sibling agent defeats. Every finding inside the flight's files is fixed; one outside them is recorded untouched.

   The `{effort}` slot is also the Codex mapping's second source form: `pfm/internal/codexgen/review.go` compiles a written level into a `codex review` at that baked effort scoped to one task's files, and the slot form into a flight-scoped review whose `model_reasoning_effort` stays `{effort}` for the lander to fill at run time.
4. The attack map, closed-world: every changed hunk gets one line — an attack hypothesis (the real product traffic or state that could break this hunk and the wrong behaviour that results), an explicit no-attack justification, or `RETIRE: {tests}`. A hunk absent from the map is uncovered. Attacks drive states the product can reach, never inputs it cannot send.
5. The validity sweep: every test file the diff adds or touches, read against the manual's placement, tier, economy and validity laws. Each violation is a finding in its class.
6. Adversarial tests, written into the module that owns the contract — there is no lander-owned directory. A test is accepted only after it was watched failing against the code it attacks.
7. Fixes (next section), then the affected tests.
8. Close the gate: the full suite once more. Exactly two full runs per gate; a full run is never looped to chase a fix.

## It fixes what it finds

The lander owns defect resolution: every defect its attacks, the review or the checks expose is fixed by the lander — implementation and tests, surgical, root cause. A round trip through the orchestrator, the speccer and a new executor for each finding costs more than the finding. Two things go back instead of being fixed:

- a defect whose fix needs a design decision or crosses out of the flight's files: `FAIL` with the residual, which the orchestrator sends to `flights-speccer` as unspecified work;
- a defect in code the flight forbids touching: recorded, named in the return.

## What it does not do

- No nested copy of itself and no `Agent` tool. The adopter's QA handed its fixes to a fresh QA seat; here the closing full-suite run and the watched-failing proof of every new test are the judges of the lander's own fixes.
- No conformance audit against the spec. An executor that drifts from its spec breaks the task that needs it, which returns `SPEC-DRIFT` and wakes the speccer; checking it again at the end buys little and costs a read of every task file. The task files are read as the attack's statement of intent, not audited row by row.
- No commit. `gitter` commits after the lander returns.

## Bounds

- One lander per project per flight, all in one message.
- Two full-suite runs.
- 150 tool calls; past it, stop and return `FAIL {project}: cap` with the ledger as it stands.
- Every verdict names its executed artifact: the run log, the report file or the pinning test. A suite not watched running is not a pass.

## The ledger and the return

`{flight directory}/gate-{project}.md`: the attack map, then one row per finding — source (checks, review, attack, sweep) · area · failing test · reproduction · expected · status (fixed, residual, outside the flight).

The return's first line is `PASS {project}` (zero findings), `FIXED {project}: {n} defects fixed` or `FAIL {project}: {n} residuals`; then the two full-run verdict lines as printed, the review's counts, the residuals, and one `RETRO {lesson}` or `RETRO none` line ([Retro lines](flights-orchestrator.md#retro-lines)).

## Context budget

- `tools: Read, Write, Edit, Bash, Glob, Grep, Skill` — `Skill` is carried because `/code-review` needs it; nothing else beyond the executor's list.
- It reads the diff and the hunks' surroundings by range, never the task files' `Steps` or `Shapes`.
- One lander per project keeps each one's diff, manual and suite to a single project.

## The accepted risk

A lander certifies its own fixes. The research collected for this design condemns that shape (self-assessment accuracy of 49 to 54%, a self-improving verifier reading 3% error against a true 32%), and none of 17 surveyed frameworks re-spawns a tester over its own fixes — the one shipping a real adversarial tester forbids it to fix at all. The adopter's ledgers also show later rounds finding 30% of all findings, including a crash on an auth gate. The ruling is cost over independence: no nesting, the mechanical judges above, and the residual road for anything structural. The A/B that would settle it — a gate with and without a fresh verifying round, scored by mutating only the changed lines — has not been run.

## Surfaces that stay in sync

| Surface | File | Holds |
| --- | --- | --- |
| The agent | `templates/global/agents/flights-lander.md` | The executable wording |
| The orchestrator | [`flights-orchestrator`](flights-orchestrator.md) | The landing: landers, then `gitter`; the reaction to `FAIL` |
| The testing manual | [`testing-manual`](testing-manual.md) | The project law the sweep and the gate run against |
| The audit | [`flights-audit`](flights-audit.md) | `gate-{project}.md` as an anchor; the review read from the lander's transcript |
| The family | [`flights.md`](flights.md) | The third agent and the directory's fourth writer |

## Evidence

- Adopter QA ledgers: 63 findings, about 27 production defects; a QA run averages 168 calls and 30.8M input tokens; 59M to 155M tokens per production defect; one wave with none.
- Review against QA over three waves: 47 findings against 57, 6 in common; the review caught a critical crash the QA missed; one change passed both and still needed a production fix.
- 5 of 8 nested chains ended in a round that found nothing.
