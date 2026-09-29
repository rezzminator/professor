# The mechanical executor

`flights-mechanical-executor` runs one task file rated `mechanical`. The base executor is the same as every tier's — [flights-executors](flights-executors.md) holds what every executor is, holds and returns; this tier's body is made for work that needs no judgment: the task file's Steps, Decisions and Shapes already hold every edit, and the executor applies them in order and proves them.

Decisions live in this file. The executable wording lives in [`templates/global/agents/flights-mechanical-executor.md`](../../../templates/global/agents/flights-mechanical-executor.md).

## Contents

- [What it is for](#what-it-is-for)
- [What the body adds](#what-the-body-adds)
- [What the body leaves out](#what-the-body-leaves-out)
- [Situations](#situations)
- [The general twin](#the-general-twin)
- [Pins and measurements](#pins-and-measurements)
- [Surfaces that stay in sync](#surfaces-that-stay-in-sync)

## What it is for

A task the speccer rates `mechanical`: no execution judgment at all — repetitive, straightforward work that needs no reasoning to do right ([Rating](flights-speccer.md#rating)). Each local choice such a task still carries, a helper's name included, is written as a `Decisions` line, so the executor never meets a name the task does not give. Renames, moves, deletions, splits, formatter output and pinned one-place edits are its work.

The body is a numbered procedure rather than a set of principles: every stop is one the executor can observe.

The friction below comes from a transcript audit of 130 flight runs and 21 general runs of the shared body, plus a benchmark round. Those runs predate the three-tier rating, when `mechanical` meant "not smart" and carried up to three judgments, so their call counts overstate what a zero-judgment task costs; the body-level defects hold whatever the rating.

## What the body adds

### The Steps are the change

The executor applies the Steps and makes only four adaptations without asking: a quoted line found at another place, an import the edit needs, the formatter's output, a fix for its own red. Another approach, a name the task does not give or an edit outside `Files` is a judgment: it stops and returns `SPEC-DRIFT {id}: {what the task file lacks}` with what landed, leaving every touched file building. It makes no design choice.

Friction: "the Goal wins over a detail" invited the executor to adapt, which is judgment. The 9 audited `SPEC-DRIFT` runs took a median of 92 calls before returning; one first edited at call 76 and returned at 158.

### The map is checked before the first edit

Before the first edit it searches the project for every line the Steps quote, every new function, type or file name the task gives, and, for a rename, move or deletion, the old name. It changes nothing and returns `SPEC-DRIFT {id}` naming each hit when:

- a quote is found nowhere, or at more than one place the Steps could mean;
- a new name is already taken;
- a build, test or caller outside `Files` would break;
- an edit would push a file over the project's size ceiling — a split is a design act, so it is the speccer's.

Friction: `Files` lists missed callers, and the drift was found mid-edit, with a partial diff left behind; one flight recorded that 2 of 2 drifts so far were `Files` lists missing test callers of a changed symbol. In a benchmark round 5 of 6 seats named a new helper with a name another package already exported. A search a small model can run literally catches both; whether a concept already exists under another name is a judgment, so the speccer quotes reuse targets as `EXISTING` shapes.

### Tests in a fixed red-green order

One test per `Done when` row, a row being a matrix row or a `Given` line, in the pattern of the testing manual the brief names:

- a row on existing behaviour: write the test, watch its assertion fail, apply the Steps, run it green;
- a row on code the task creates: apply the Steps, run it green, copy the file to the task's scratch directory, break that row's behaviour with one Edit, watch it fail, copy the file back, run it green;
- a task with no behaviour change (a rename, a move, a deletion, a doc) writes no test: the build and the affected tests green, plus the pre-edit search finding the old name only in history, are its proof.

Friction: 12 of 130 runs used git writes (`stash`, `checkout --`) to re-break code for the watched-failing proof; one reverted to the last commit in a worktree holding sibling tasks' uncommitted work, and one temporary break of a shared file turned a sibling's run red. The fixed order keeps the proof and removes git from it.

### Reds are bounded

- A red its own edit caused inside `Files` (a typo, a missing import, a formatter complaint): fix its cause there and rerun; never a stop.
- A cause outside `Files` (a sibling task's half-edited file in the shared worktree), a red in a test the diff does not reach, or a check rejecting what was there before the edit (a missing ToC, a file already over size): named under Outside defects, and the task finishes `DONE`. Only when it stops the executor's own tests: `FAILED {id}: blocked by {file}: {error line}`, which the orchestrator holds for the sibling's verdict instead of sending to the speccer.
- Any other red: read the error and the lines it names, then return `SPEC-DRIFT` or `FAILED` with the cause, or what was read and "cause unknown". Deeper diagnosis is the `smart` tier's work.

Friction: the base body's unbounded read-to-cause produced diagnosis spirals of 150 calls and more. Sibling reds were rediscovered flight after flight in `RETRO` lines. In the benchmark round the first tier body stopped as `SPEC-DRIFT` on a pre-existing missing ToC after finishing all its work; the outside-defect rule separates "report it and finish" from "stop".

### A literal command recipe

Every test, build and check runs in the foreground as one call at the tool's longest timeout (`600000` ms on Claude `Bash`), redirected to a log in the task's scratch directory, with `echo rc=$?`, then read through `tail` or a search. Never `&`, a background run, `sleep` or a poll. File lists are written out, never passed through a shell variable, and no variable is named `status` or `path`.

Friction: 36 of 130 runs slept or polled (102 `sleep` calls) and 12 used background runs; a notification arriving after the return produced extra final messages. The executors did not know that the `Bash` default of 120 s backgrounds a longer command. The shell traps (zsh does not word-split a variable; `status` and `path` are reserved) were rediscovered in a dozen `RETRO` lines.

### Its own checks, the formatter included

It runs the affected tests of its `Files`, then the formatter, linter and type check on each changed file and the static check the testing manual names, its architecture ratchet included, and fixes what its edits caused there. A brief naming the full suite, a whole-tree format sweep or a review as its run is refused and named in the return.

Friction: 3 of 4 benchmark seats left formatter and lint findings on their own files; "format" appeared in the base body only in the refused list.

### Scratch and reading

Every log, backup and script sits in `{scratch}/{id}/`; a backup ends in `.bak`. An Edit's result is its proof: it rereads only lines not yet in its context.

Friction: siblings overwrote each other's generic scratch files, and a backup named like a standard-library module shadowed it. 79 runs made 312 re-reads, 123 of them a file just edited.

### The return as a template

The return is a fixed template: the token line first, the `RETRO` line last, nothing before or after, with a contrastive ✗/✓ example of the first line.

Friction: 47 of 130 flight runs and 18 of 21 general runs put a sentence ("All green.") before the token the orchestrator matches by position; 11 of 130 had no `RETRO` line.

### The cap

80 calls, as for every flight tier. 41 of 130 audited runs passed it, used 68% of all input tokens, and none returned `FAILED {id}: cap`: a count the model must hold in its head is not held. The body answers with stops it can observe — the pre-edit search, the bounded reds — and never spawns a sub-agent.

## What the body leaves out

The task file pins these on this tier, so the body does not carry them: the façade list, the directories named by negation, versioned scripts under `scripts/`, and the Goal-wins clause. The deletion rule survives as the pre-edit search for the old name; one test home per source file and the scratch-root helper live in the testing step.

## Situations

| Situation | The mechanical executor's action |
| --- | --- |
| A `Progress dependency` does not hold in a way that changes an edit | Change nothing; `SPEC-DRIFT {id}: {the fact}: {what it found}` |
| A quote found nowhere or at more than one place, a new name taken, a break outside `Files`, a file pushed over the ceiling | Change nothing; `SPEC-DRIFT {id}` naming each |
| A quoted line found at another place, an import the edit needs, the formatter's output | Adapt and list it under Adapted |
| An edit the Steps do not settle (another approach, a name not given, a file outside `Files`) | Stop, every touched file building; `SPEC-DRIFT {id}: {what the task file lacks}` with what landed |
| Its own edit turns a check red inside `Files` | Fix the cause there, rerun |
| A sibling's file, an unreached test or a pre-existing finding is red | Outside defects; finish `DONE` — or `FAILED {id}: blocked by {file}: {error line}` when it stops its own tests |
| Any other red | Read the error and the lines it names; `SPEC-DRIFT` or `FAILED` with the cause or "cause unknown" |
| A row that reads two ways | The reading today's code and the Steps support, named in the return; `SPEC-DRIFT` only when neither settles it and they build different code |
| A brief naming the full suite, a whole-tree sweep or a review | Refused, named in the return |
| A decision it cannot make | `BLOCKED {id}: {question}` |
| 80 calls | `FAILED {id}: cap` with what landed, what is left, the next step |

## The general twin

`general-mechanical-executor` is the same procedure over an inline brief ([general-executors](../general/general-executors.md)). It differs in what a general task carries:

- the brief holds every edit and names the task's `{id}`; a premise the brief states replaces `Progress dependency`;
- one covering test in the project's pattern, with the same red-green order, instead of one per row;
- it runs the brief's check first, and the testing manual's static check only when the brief names one;
- the cap is 45 calls, with the handoff;
- the return carries a `Check:` line with the brief's verdict line as printed and a single `Test:` line; a refused gate run goes under Adapted.

## Pins and measurements

| Engine | Pin | Measured |
| --- | --- | --- |
| Claude | `claude-sonnet-5-5` at `high` | 83.5 against `medium`'s 74.5, two seats per configuration, blind-judged |
| Codex | `gpt-6-luna` at `xhigh` | On the shared body: 87 against `high`'s 85 and `medium`'s 76, at under $0.10 a task; Sonnet 5.5 at `high` scored 86 and 87 |

The full ID is pinned because the `sonnet` alias resolved to different models on different accounts of one host.

A benchmark round then ran the first tier bodies against the shared one on Codex, one seat per configuration, blind-judged with the Claude seats as anchors: `gpt-6-luna` at `xhigh` scored 90 against the shared body's 87, at `medium` 84 against 76, and at `high` 84 against 85. `xhigh` stayed the best on both bodies. The `xhigh` seat on the tier body spawned a stray sub-agent that cost $0.598 beside its own $0.155; the body now names that it spawns none.

The `gpt-6.1-sol` round covered the `precise` and `smart` tiers only; this tier stays on `gpt-6-luna`.

## Surfaces that stay in sync

| Surface | File | Holds |
| --- | --- | --- |
| The agent | `templates/global/agents/flights-mechanical-executor.md` | The executable wording and the pins |
| The general twin | `templates/global/agents/general-mechanical-executor.md` | The same procedure over an inline brief |
| The base | [flights-executors](flights-executors.md) | What every tier holds; the tier table |
| The speccer | [flights-speccer](flights-speccer.md) | The `mechanical` rating; local choices as `Decisions` lines; reuse targets and the façade as `EXISTING` shapes |
| The orchestrator | [flights-orchestrator](flights-orchestrator.md) | `blocked by {file}` held for the sibling's verdict |
| The cross-harness seat | `templates/global/commands/flights/orchestrate-cross-harness.md` | The Codex model and effort on the seat's launch line |
