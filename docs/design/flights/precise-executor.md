# The precise executor

`flights-precise-executor` runs one task file rated `precise`. The base executor is the same as every tier's — [flights-executors](flights-executors.md) holds what every executor is, holds and returns; this tier's body is made for work whose interfaces are pinned and whose risk sits in its `Execution judgments`, its failure paths and its error rows.

Decisions live in this file. The executable wording lives in [`templates/global/agents/flights-precise-executor.md`](../../../templates/global/agents/flights-precise-executor.md).

## Contents

- [What it is for](#what-it-is-for)
- [What the body adds](#what-the-body-adds)
- [Situations](#situations)
- [The general twin](#the-general-twin)
- [Pins and measurements](#pins-and-measurements)
- [Surfaces that stay in sync](#surfaces-that-stay-in-sync)

## What it is for

A task the speccer rates `precise` ([Rating](flights-speccer.md#rating)): one to three judgments with every interface the task touches pinned in `Shapes`, or no judgment at all where the difficulty is the implementation itself — concurrency, failure paths, many error rows. It is a lateral tier, not a cheaper one: the work is exact rather than open.

The executor has an open hand inside `Files`: where the spec and the code disagree, it reaches the Goal and says what it changed.

The friction below comes from a transcript audit of a six-seat benchmark round on one precise task (two judgments, eleven error rows), two Codex precise seats from a real flight, the `RETRO` and drift lines of 30 flights, and a later benchmark round on Codex.

## What the body adds

### The blast radius is searched before the first edit

Before step 1, after the `Progress dependency`, it searches the repository for every test, caller and doc pinning a name, behaviour or text it changes. A build, test or caller outside `Files` that its change would break returns `SPEC-DRIFT {id}` with nothing changed and every hit at once. A stale comment or doc, an older defect, anything its diff leaves no worse is a defect line in the return, and the task finishes. A Goal that turns unreachable mid-task stops with every touched file building.

Friction: in one adopter flight the drifts came after the edits, each naming tests outside `Files` that pinned the changed behaviour; one seat ran the wide suite after its edits, stopped with a package half-changed, and broke its sibling's test collection. On the benchmark round's first tier body the consumer rule fired on a still-true comment and returned an unjustified `SPEC-DRIFT` that scored 4; the rule now keys on what breaks a build, a test or a caller.

### A red its own edit caused is iteration

A red its own edit caused inside `Files` is fixed there. Every other red is read to its cause and returned, or, when it lies in an unreached test or a pre-existing finding, named as an outside defect while the task finishes `DONE`. Never an unchanged rerun. A cause outside `Files` that stops its own tests returns after what can still run past it ran: every outside cause at once, first line `FAILED {id}: blocked by {file}, {file}…` naming every file, then one `{file}: {error line}` line per cause.

Friction: on the benchmark round's first tier body a `gpt-6-sol` seat returned `FAILED` with its work about 90% done, on new log keys the project's log scrubber redacted — a red its own change caused in its own file; both seats on the shared body adapted in-file. It scored 71. The body carries that case as its ✗/✓ example.

### Judgments are pinned, and nothing is added unasked

Each `Execution judgment` is the executor's inside `Decisions` and `Shapes`: decided by the rows it can break, pinned with a test, named with its test in the return. It builds what a row, Decision or Step asks; a guard none asks for is a defect line, never code.

Friction: three of six benchmark seats added mechanisms no row asked for (an unasked callback, an unasked helper, a second off-site parse) and reported them as adaptations.

### Reuse is searched repository-wide

Before creating a function, type, constant, test fake or fixture, it searches the whole repository, case-folded, for the concept and for the name it would give; it calls or extends what exists, and names in the return anything it could not import, with why.

Friction: 6 of 6 benchmark seats, and the reference diff, wrote a second implementation of a helper another package already exported; every seat scoped its search to the task's own package. All six also added a fake clock beside the existing one in the same test file. On the later round a seat searched repository-wide, found the helper and stated why it did not reuse it (the semantics differ): the rule works.

### A test per row and per `Given` line

One covering test per `Done when` row and per `Given` line. A row's alternatives and its error-handling column are one case each; user-visible text is asserted whole, every variant; a log line is read back through the project's logging façade as production emits it. Every branch it adds that stops, raises, retries, waits or logs is reached by a test, or reported as a missed row, or deleted. A row with no behaviour change (a rename, a move, a deletion, the doc references one carries) is proven by its check line. The return maps every row and `Given` line to its test.

Friction: the Sonnet-at-`high` seat that scored 77 against its twin's 95 left a `Given` line untested and shipped log keys the scrubber redacts in production; the five seats that tested the log line found the redaction through that test. Partly asserted alternatives and stop texts missing a clause cost the others points. The `xhigh` seats did these unprompted; the rules make it independent of the draw.

### Its own checks, the formatter included

The affected tests plus the type check, lint and formatter of its own files, and the static check the testing manual names, its architecture ratchet included; a ratchet its diff pushes over is its to bring back under. A split's new file beside a `Files` entry, in the same unit, is in scope; a split needing an existing file outside `Files` returns `SPEC-DRIFT {id}` naming it.

Friction: a lander formatted the two Codex precise seats' files after the flight, as it did every flight: "the format sweep belongs to the gate" was read as "never format".

### Scratch, reading and the return

Scratch files sit in a directory named for the task id. Searches are its own, never a sub-agent's, and it reads a hit's range, never the area around it. Line one of the final message is the status token, with a ✗/✓ example.

Friction: siblings overwrote each other's scratch files. Across 401 executor transcripts, 68 final messages carried the status token below a line such as "All green. Task complete."

### Writing a file

The layout laws bind this tier through its body: a deletion leaves nothing behind, proven by one search for the name; one term per concept; the project's façade for a cross-cutting mechanism; a new file with the unit that changes with it; versioned scripts; one test home per source file.

## Situations

| Situation | The precise executor's action |
| --- | --- |
| A `Progress dependency` whose failure changes the change | Change nothing; `SPEC-DRIFT {id}: {what it found}` |
| A build, test or caller outside `Files` the change would break | Change nothing; `SPEC-DRIFT {id}`, every hit at once |
| A stale comment or doc, an older defect | A defect line in the return; finish |
| Spec and code disagree on a detail | Reach the Goal; say what it changed |
| The Goal turns unreachable mid-task | Stop, every touched file building; `SPEC-DRIFT {id}` with what it found and what landed |
| Its own edit turns a check red inside `Files` | Fix it there, rerun |
| A red in an unreached test, or a check rejecting what was there before | Outside defects; finish `DONE` |
| Any other red | Read to the cause; `FAILED` or `SPEC-DRIFT` with it, or "cause unknown" |
| A cause outside `Files` stops its own tests | Run what can still run past it; first line `FAILED {id}: blocked by {file}, {file}…` naming every file, then one `{file}: {error line}` line per cause |
| An `Execution judgment` | Decide it by the rows it can break, pin it with a test, name both |
| A guard no row asks for | A defect line, never code |
| A helper, fake or fixture it would create | Search the repository first; reuse, or name why not |
| A file over the size ceiling | Split into a new file beside a `Files` entry; a split needing an existing file outside `Files` → `SPEC-DRIFT {id}` naming it |
| A decision it cannot make | `BLOCKED {id}: {question}` |
| 80 calls | `FAILED {id}: cap` with the handoff |

## The general twin

The general family has no tier twin: its one hand, [`general-executor`](../general/general-executor.md), builds a change its [`general-foreman`](../general/general-foreman.md) or its orchestrator's caller already decided, on one body.

## Pins and measurements

| Engine | Pin | Measured |
| --- | --- | --- |
| Claude | `claude-sonnet-5-5` at `xhigh` | On two pinned-but-hard tasks, 92.5 on average against Opus 5.5 at `high`'s 84 and Sonnet 5.5 at `high`'s 78.5, whose seats ranged from 64 to 95 |
| Codex | `gpt-6.1-sol` at `high` | Below |

On the shared body, Codex seats were blind-judged with the Claude seats as anchors: `gpt-6-sol` at `high` scored 94, beside Sonnet 5.5 at `xhigh`'s 95 and 97; `gpt-6-luna` scored 81 at `xhigh` and 67 at `high`, both shipping log keys the scrubber redacts.

The first tier body lost on that round: `gpt-6-sol` at `high` scored 71 (the own-red over-stop) and `gpt-6-luna` at `xhigh` scored 4 (the stale-comment drift); where it ran through, `gpt-6-luna` at `high` scored 85, 18 above the shared body. The body was adopted only after the own-red and outside-defect rules separated "stop" from "report and finish".

The final body then ran on the same task, fenced-graded, two seats: `gpt-6.1-sol` at `high` passed every gate and 19 of 19 hidden tests in both seats, at $0.61 and $0.69; `gpt-6-sol` at `high` on the same body also passed every gate and 19 of 19, at $0.77. Per 1M tokens, from OpenAI's API pricing page on 2026-09-29, `gpt-6.1-sol` costs $2 in, $0.10 cached and $10 out; `gpt-6-sol`'s cached input is $0.20.

Blind-judged together with the Claude anchor: `gpt-6.1-sol` 94 and 95, `gpt-6-sol` 95 on this body and 84 on the old shared body, Sonnet 5.5 at `xhigh` 97. A tie on quality at about 15% less cost, so `gpt-6.1-sol` is the pin. The one label with a red gate, the old-body seat, named a helper that collides with an existing export under the case-folded ratchet — the case this body's repository-wide reuse search exists for.

## Surfaces that stay in sync

| Surface | File | Holds |
| --- | --- | --- |
| The agent | `templates/global/agents/flights-precise-executor.md` | The executable wording and the pins |
| The base | [flights-executors](flights-executors.md) | What every tier holds; the tier table |
| The speccer | [flights-speccer](flights-speccer.md) | The `precise` rating; `Execution judgments`; reuse targets as `EXISTING` shapes |
| The orchestrator | [flights-orchestrator](flights-orchestrator.md) | The verification of each row's test |
| The cross-harness seat | `templates/global/commands/flights/orchestrate-cross-harness.md` | The Codex model and effort on the seat's launch line |
