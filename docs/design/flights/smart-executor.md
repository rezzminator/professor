# The smart executor

`flights-smart-executor` runs one task file rated `smart`. The base executor is the same as every tier's — [flights-executors](flights-executors.md) holds what every executor is, holds and returns; this tier's body is made for work whose spec leaves part of the design to the executor, or whose deliverable is a document, prompt, spec or report.

Decisions live in this file. The executable wording lives in [`templates/global/agents/flights-smart-executor.md`](../../../templates/global/agents/flights-smart-executor.md).

## Contents

- [What it is for](#what-it-is-for)
- [What the body adds](#what-the-body-adds)
- [Situations](#situations)
- [The general twin](#the-general-twin)
- [Pins and measurements](#pins-and-measurements)
- [Surfaces that stay in sync](#surfaces-that-stay-in-sync)

## What it is for

A task the speccer rates `smart` ([Rating](flights-speccer.md#rating)): more than three judgments, an interface the task touches left unpinned, a diagnosis of an unknown cause, or a written deliverable. The spec's pinning, not the task's size, separates it from `precise`.

The executor has an open hand inside `Files`, and reads wherever the design needs: its edit scope is `Files`, its read scope is the code the design depends on. It stops only when what it must build is wrong or unbuildable as written.

The friction below comes from a transcript audit of 158 smart-tier runs, a benchmark round on one goal-only task that left the design open, the `RETRO` and drift lines of 30 flights, and a later benchmark round on Codex.

## What the body adds

### Understand, then design

All of this comes before the first edit:

- trace each concrete example in the Goal (a path, a name, a value) and each "as today" in an error column through the live code path that produces it; a design proven only on fixtures that path never produces misses the Goal;
- follow how the code already decides the neighbouring case;
- per open judgment, take the candidate under which every `Done when` row and Goal example holds on live data; between equals, the smaller diff in the code's own pattern;
- state the design in a few lines (each judgment, its choice, its files), then edit; every changed line traces to the Goal or a row.

Friction: on the goal-only benchmark task Opus 5.5 at `high` scored 88 and Sonnet 5.5 at `xhigh` 66. The winning seat mapped the area and first wrote at call 43; it traced the Goal's own example through the code, found both named cases carried the same label, and derived what the design needed. The losing seat edited around call 27, decided the opposite from the same file, and its design passed only on fixtures the live path cannot produce: 19 of the 22 points between the seats. It also read "as today" from an ambient fallback rather than today's code.

### Every consumer is listed first

Before the first edit it lists every consumer of what it changes or removes, searching only the code that could break — callers, tests and fixtures pinning it, twins rendering or indexing the same data: file names first (`grep -rl`), then only the hits outside `Files` are read; never docs, which the speccer's sweep placed and the lander's whole-diff review reads. One outside `Files` its design would break gets a design that keeps it working, or `SPEC-DRIFT {id}` naming each, nothing changed. A twin showing or indexing what it changed moves with it.

Friction: one flight's task drifted over three rounds on consumers outside `Files`. The round that surveyed first returned `SPEC-DRIFT` at call 22 with nothing changed; the round that patched at call 13 found the ripple only after 89 calls, with the compile broken. The benchmark's winning seat lost its points on twin surfaces — a plain renderer and a search index still keyed on the old field — the one place the losing seat did better.

### Tests: one red run per task

Each decision is tested once, in the test of the unit whose code makes it, the unit its row names: a unit's rows are cases of one table-driven test, or assertions of one test, in its existing test file (a new one only where the source file has no test home, the manual's test home then); every assertion that fits one render or one call goes in one test; each `Given` line gets its test too; a caller's test covers only what the caller decides, against the real boundary where talking to it is the caller's job; user-visible text is asserted whole, once, where the copy is the behaviour; no assertion catches nothing. Before any test is written it reads every row and `Given` line: one that makes an error look like absence or contradicts the project contract returns `SPEC-DRIFT {id}: {row}: {why}`, nothing written for it, ahead of and over the rule for a row read two ways. Tests before code, never after: each row's case or assertion, and on the project's first task the project's integration test, is written from the row's example before any code, the example's inputs and expected values kept exactly; a judgment's test is written with them. No test is written after the code: a branch no example covers is deleted, or, when a `Done when` row needs it, returned as `SPEC-DRIFT` so the speccer adds the example. Every row's case or assertion is written first, the names the task creates stubbed so they compile; every new or extended test runs once against the unfixed tree, each test or table case failing on an assertion (a build error proves nothing), rows batched in one test sharing its failing line, the log kept, then once green after the fix. A row whose behaviour was in the tree before that red run gets no red proof: the return marks it `pre-existing, no red proof`, citing its test passing in the red log and the commit or `run.md` line that introduced the behaviour, and the orchestrator records `DONE` only with both cited; no executor re-breaks, stashes, reverts or mutates finished or committed code, even where the testing manual asks for a re-break or mutation proof; a new gate proves its bite on a fixture or a `mktemp` copy. Every build and test runs where the brief's standing rules say from the first run. A row with no behaviour change (a rename, a move, a deletion, the doc references one carries) is proven by its check line; a row a written deliverable meets, by the project's check for that file kind plus the quoted line that meets it. The affected tests run as it goes; the testing manual's static-check command runs once, as the last step after the work is finished and before the return is written, given the task's `Files` list, a red fixed and the same command run again; a manual naming no single command has its checks run as one command, same rule. A budget or ratchet its diff pushes over is its to bring back under, a split first when the design allows it.

Friction: the losing benchmark seat returned `DONE` with three tests never watched failing and one row with no new test, and ran a test on the host once before rereading the fence rule.

### A written deliverable

Before its first line it reads the writing law the project contract names for that file kind. Every quoted line, command, path and output is copied from the file itself or a run's log; a number names its run and the conditions it ran under. An edit keeps every decision it does not set out to change: each removed line is checked against the decision's source the task names. It states current behaviour only; a spec lead its reading contradicts goes in the return.

Friction: a written deliverable alone makes a task `smart`, yet the shared body had no rule for one. A design-doc edit dropped a settled decision; the seat never read the decisions file, and a second round restored it.

### Writing a file

The layout laws bind this tier through its body: reuse what a search for the concept finds; a deletion leaves nothing behind; one term per concept; the façade for a cross-cutting mechanism; a new file with the unit that changes with it. A file over the size ceiling is split before logic is added: a split's new file beside a `Files` entry, in the same unit, is in scope; a split needing an existing file outside `Files` returns `SPEC-DRIFT {id}` naming it.

Friction: in one flight two smart tasks returned `BLOCKED` on files already at their ceiling with no room in `Files`; both finished in round 2. The rule writes down which split is in scope.

### The cap is estimated before the edits

With the design stated, it estimates the calls its edits and each row's proof need; when the calls spent plus that estimate pass 150, it returns `SPEC-DRIFT {id}: too large` with the split it would make, nothing changed. The orchestrator records the return as `TOO-LARGE`, counted as no red, and a revising speccer call cuts the task. Past 150, `FAILED {id}: cap` with the handoff.

Friction: 6 of 158 transcripts ran past 80 calls, and only one of them named the cap; five Codex tasks of one flight failed round 1 on the cap. The winning benchmark seat returned `DONE` at 92 calls, 42 of them reading before its first edit. The estimate surfaces an overrun while nothing has changed.

### `DONE` is defined, and the first line holds only the token

`DONE` means every `Done when` row is met in full and has its test (red in the red log, or `pre-existing, no red proof` with its citation) or its check; a row not reached returns `FAILED`, a row the design cannot meet as written `SPEC-DRIFT`. The first line holds only the token, the id and its clause, and no `**Verdict:**` line follows `RETRO`.

Friction: a return opened "DONE 1-a, with two gaps", another carried a round note on its first line, and Codex seats ended their return with the main chat's Verdict line.

## Situations

| Situation | The smart executor's action |
| --- | --- |
| A `Progress dependency` whose failure changes the change | Change nothing; `SPEC-DRIFT {id}: {what it found}` |
| Spec and code disagree on a detail | Reach the Goal; say what it changed |
| A consumer outside `Files` its design would break | Pick a design that keeps it working, or `SPEC-DRIFT {id}` naming each, nothing changed |
| A test the Decisions list under `Temporary reds` | Neither `SPEC-DRIFT` nor `FAILED`: named in the return, the task continues; a red its change causes outside `Files` that the list does not name stays `SPEC-DRIFT` |
| An open judgment | The candidate every row and Goal example holds under on live data; between equals, the smaller diff |
| A row that reads two ways | The reading the Goal's example and today's code support, named in the return; neither settles it: `SPEC-DRIFT {id}` |
| The estimate passes the cap | `SPEC-DRIFT {id}: too large` with the split, nothing changed; recorded as `TOO-LARGE`, no red |
| A written deliverable | Read the writing law first; copy every quote; keep every decision it does not set out to change |
| A file over the size ceiling | Split into a new file beside a `Files` entry; a split needing an existing file outside `Files` → `SPEC-DRIFT {id}` naming it |
| Its own edit turns a check red inside `Files` | Fix it there, rerun |
| A branch no example covers | Deleted; when a `Done when` row needs it, `SPEC-DRIFT {id}` so the speccer adds the example |
| A red in an unreached test, a check rejecting what was there before, a stale comment or older defect | Outside defects; finish `DONE` |
| Any other red | Read to the cause; `FAILED` or `SPEC-DRIFT` with it, or "cause unknown" |
| A cause outside `Files` stops its own tests | Run what can still run past it; first line `FAILED {id}: blocked by {file}, {file}…` naming every file, then one `{file}: {error line}` line per cause |
| The Goal cannot be reached | Stop; `SPEC-DRIFT {id}` with what it found and what is done |
| A decision it cannot make | `BLOCKED {id}: {question}` |
| A row that makes an error look like absence or contradicts the project contract, read before any test | `SPEC-DRIFT {id}: {row}: {why}`; nothing written for it |
| 150 tool calls | `FAILED {id}: cap` with the handoff |

## The general twin

The general family has no tier twin: its one hand, [`general-executor`](../general/general-executor.md), builds a change its [`general-foreman`](../general/general-foreman.md) or its orchestrator's caller already decided, on one body.

## Pins and measurements

| Engine | Pin | Measured |
| --- | --- | --- |
| Claude | `opus` at `high` | 75 against `medium`'s 65; on the goal-only task, 88 against Sonnet 5.5 at `xhigh`'s 66 |
| Codex | `gpt-6.1-sol` at `high` | Below |

On the shared body, Codex seats were blind-judged with the Claude seats as anchors: `gpt-6-sol` at `high` scored 87 against `xhigh`'s 85, at about 70% of the cost; Opus 5.5 at `high` scored 86.

The tier body won that round clearly: `gpt-6-sol` at `high` scored 93 and at `xhigh` 94, 6 and 9 points over the shared body and above the Opus anchor, at $2.139 and $2.985. `high` tied `xhigh`, so a smart seat always runs `high`; the orchestrator reads no task file to choose an effort.

The final body then ran on the same task, fenced-graded, two seats: `gpt-6.1-sol` at `high` passed every gate in both seats, the full suite included — the first smart seats to pass the architecture ratchet — at $1.46 and $1.53; `gpt-6-sol` at `high` failed the ratchet in 3 of 3 runs, at $1.88 to $2.14. Per 1M tokens, from OpenAI's API pricing page on 2026-09-29, `gpt-6.1-sol` costs $2 in, $0.10 cached and $10 out; `gpt-6-sol`'s cached input is $0.20.

Blind-judged together with the Claude anchor: `gpt-6.1-sol` 96 and 91; `gpt-6-sol` 90 and 78 on the specialized bodies and 72 on the old shared body; Opus 5.5 at `high` 89. `gpt-6.1-sol` produced the two best smart seats of every round at about 25% less cost, so it is the pin. One of its seats lost two honesty points for attributing an instruction from its brief to the user.

## Surfaces that stay in sync

| Surface | File | Holds |
| --- | --- | --- |
| The agent | `templates/global/agents/flights-smart-executor.md` | The executable wording and the pins |
| The base | [flights-executors](flights-executors.md) | What every tier holds; the tier table |
| The speccer | [flights-speccer](flights-speccer.md) | The `smart` rating; sizing a task to the cap; cutting a task returned too large |
| The orchestrator | [flights-orchestrator](flights-orchestrator.md) | `SPEC-DRIFT {id}: too large` recorded as `{id} TOO-LARGE · {the split it proposes}`, no round, no red, no transcript; a revising speccer call cuts the task |
| The cross-harness seat | `templates/global/commands/flights/orchestrate-cross-harness.md` | `gpt-6.1-sol` at `high` on every smart seat's launch line |
