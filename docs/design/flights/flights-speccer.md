# flights-speccer

`flights-speccer` is the one agent that writes execution specs. It turns one flight into self-contained task files, each run by a fresh executor that reads only its own file, and it tells the dispatcher the order, the dependencies, the contention and the difficulty. It works for any project, any code, any size of flight, and it never knows who called it. What the whole family shares (the flight directory, the verdict tokens, the containers) lives in [`flights.md`](flights.md).

Decisions live in this file. The executable wording lives in [`templates/global/agents/flights-speccer.md`](../../../templates/global/agents/flights-speccer.md). A change lands here first, then in the template, then in every surface listed under [Surfaces that stay in sync](#surfaces-that-stay-in-sync).

## Contents

- [The cost model it serves](#the-cost-model-it-serves)
- [The two flows](#the-two-flows)
- [Input](#input)
- [Output: three readers, three artifacts](#output-three-readers-three-artifacts)
- [The spec directory](#the-spec-directory)
- [`index.md`](#indexmd)
- [The task file](#the-task-file)
- [Altitude: what is pinned, what is described](#altitude-what-is-pinned-what-is-described)
- [Rating](#rating)
- [The run](#the-run)
- [Drift: adapt, or `SPEC-DRIFT`](#drift-adapt-or-spec-drift)
- [`BLOCKED`: no open decisions, one question](#blocked-no-open-decisions-one-question)
- [The return](#the-return)
- [Not part of the design](#not-part-of-the-design)
- [Measuring a spec](#measuring-a-spec)
- [Surfaces that stay in sync](#surfaces-that-stay-in-sync)
- [Open items](#open-items)

## The cost model it serves

Cost = calls × context. An agent's context only grows, and every call re-sends all of it. Measured over one long orchestration session, 97% of all tokens were context being re-sent and about 2% of the spend was file content written into the repository. The design therefore optimises one thing: how small each executor's context stays while it works.

Three consequences shape everything below.

- Many short runs beat one long run, so one task file is one fresh executor. Feeding one executor its spec in pieces saves about 1% of its cost, because the spec text is a small share of its context; starting a fresh executor per task saves about a third.
- A fresh executor is only cheap when its task file is self-contained. Under specs that pointed at shapes instead of carrying them, executors spent 35% of their calls re-reading code before their first edit and reached a median context of 91K by that edit.
- Every task has a fixed price whatever its size: a task file written at frontier-judgment, and a fresh executor that opens its spec and re-reads the files it will change before its first edit. Executors that finished in 17 to 24 calls stayed under 90K of context, far below the range where growth hurts. A flight is therefore cut only as far as each cut pays for itself, and every part of the design that asks for more (more tasks, more decisions, more text, more quotes) carries a budget.

## The two flows

`flights-speccer` is called in two ways and behaves identically in both. The only difference is who supplies the decisions.

### Human in the loop: `/flights:spec`

1. The user calls `/flights:spec`.
2. The command sends `tracer` and `mapper` agents to map the area.
3. The command asks the user its questions, technical and product.
4. The command hands `flights-speccer` the decisions, the maps, the requirements and the flight directory.
5. `flights-speccer` writes the flight directory and returns.
6. A `BLOCKED` item carrying a question goes to the user once, and the answer returns as a [revising](#drift-adapt-or-spec-drift) call. The command then presents the index; running it is the user's separate decision.

### Model caller: work handed to a sub-agent

1. A main chat hands work to a sub-agent of any type.
2. Handed a batch, or work it cannot see how to do, the sub-agent's first tool call spawns `flights-speccer` at its pin — apex (`fable`), or `model: "opus"` on the spawn when the caller judges the flight small (a few tasks, no unknown cause, no design to choose) — handing it the work, everything it holds and a directory under `tmp/flights/`.
3. `flights-speccer` writes the flight directory and returns the path, the index and the dispatch order.
4. A directory holding a single task the sub-agent executes itself; one holding several it hands to `flights-orchestrator`, which dispatches one fresh executor per task file by the [ready rule](#the-ready-rule) and executes none itself.

Ordering between flights belongs to the main chat that runs them, one after another. `flights-speccer` sees one flight and nothing beside it.

## Input

Only the first item is required. Whatever is absent, `flights-speccer` derives from the code and decides. A caller sees nothing of the agent but its `description`, so the description carries this list as what to pass; the body keeps only what `flights-speccer` does with each item.

| Input | Rule |
| --- | --- |
| What needs doing, and where | Required |
| Requirements | Applied as given |
| Acceptance criteria | When absent, `flights-speccer` derives each task's outcome |
| Decisions and rulings already made | Binding, never re-decided |
| What the caller already knows: maps, findings, names | The starting point; `flights-speccer` reads only what they leave out |
| Boundaries: out of scope, files another owner holds | No task touches them |
| Standing rules the executors work under | Specs stay inside them. The project's contract (the `CLAUDE.md` files) reaches every executor from the harness, and the flight's own rules (worktree, fence, checks, cap) travel in the dispatcher's brief; a task file carries neither, even when a caller's rule asks for them there |
| The [testing manual](testing-manual.md) of each project touched | Opened at intake: Tiers, Where a test lives, Lanes and registries, Gates and floors, the removal clause of What not to test. Its facts enter a task as `Decisions`, `Files` and `Done when` lines — a test row names its tier, `Files` lists the test home and every registry file — and the manual is never a `reads` entry. A project without one is a `NOTES` line |
| The flight directory | One directory under `tmp/flights/`; `flights-speccer` writes everything there and nowhere else |

### What a spec never restates

Anything the [executor](flights-executors.md) or the [gater](flights-gater.md) agent holds is never written into a task file or a `0-` file: how to run or wait for a command in general, read discipline, the watched-failing proof, the return format, the review, the cap, the layout laws, the testing manual's content. A `0-` file carries project facts several tasks need; an instruction every executor follows on every task belongs to the executor agent, and a project testing rule belongs to the testing manual — finding one missing is a `NOTES` line, never a paragraph in the spec. The task file's fixed lines went the same way: the closing line of `Done when` and the opening line of `Progress dependency` restated executor rules and now live in the executor's body; a task file keeps only what is a fact about this task. Reconcile's restatement check enforces it. The measured cost of the old way: 4.3 KB and 12.3 KB of generic instruction per executor in two audited flights, rewritten every revising round.

### Layout laws at design time

Four laws of `/quality:llm-codebase` bind when tasks are cut, and live in the Design phase: a task cuts along a unit of change, never across one; the unit's fixed file set decides `Files`; no new hand-kept parallel list and no directory named by negation, a registry being a hot file; a change crossing a wire boundary starts at the contract package's consumer index, and `needs` follows it. The laws that bind while a file is written live in the executor.

### The model by round

The first spec of a flight runs at the agent's pin (`fable`), or on `opus` when the caller judged the flight small. Every revising round runs on `opus`, spawned fresh by the orchestrator, and reads the `RETRO` lines of `run.md` with the executor's transcript.

With `/flights:spec` the fourth row arrives full, because the user answered the questions. From a model caller it arrives thin, and `flights-speccer` decides.

A caller brings content and never a format. File names, index columns, extra sections and the shape of the return belong to `flights-speccer` alone and are the same in every project: a format asked for in the spawn prompt is ignored, and so is the style of spec directories already in the repository. The dispatcher and every tool built on a spec directory rely on that one shape. The agent's `description` tells callers the same.

## Output: three readers, three artifacts

Each reader reads only its own artifact, and nothing is stated twice.

| Reader | Artifact | Carries |
| --- | --- | --- |
| The executor | One task file plus the shared files it names | What to do, how, the shapes it types against, the steps in order, its outcome |
| The dispatcher | `index.md`, repeated in the return | Per task: what it needs, what it shares, its rating, what it reads |
| The caller | The return message | The directory path, the index, the dispatch order, any `BLOCKED` task, notes |

## The spec directory

```text
tmp/flights/{flight}/
  index.md      one row per task
  0-{topic}.md  a shared file, only when two or more tasks need the same content
  1-a.md        a task file: {level}-{letter}.md
  2-a.md
  3-a.md  3-b.md  3-c.md
```

- One task file is one task, one fresh executor and one outcome.
- Content one task needs lives in that task. Content two or more tasks need lives once in a `0-` file, and each task names only the `0-` files it uses. This is the only pointer a task file holds that is not a code path, and it points into the spec directory, which nobody edits while executors run. A `0-` file is never the place for every shape of every task: each executor would pay for all of it to use a fraction.
- The reading budget: a task file plus the `0-` files it reads stays under 16,000 characters. `flights-speccer` measures it before returning; over budget, it cuts words first and the task second.
- Discover once: whatever every executor would otherwise find out alone (how to run the checks, where failures are logged, a house convention) is found in the map round and written in one `0-` file.
- The dispatcher's brief names the task file and its `0-` files together, and the executor opens them all in its first message.

## `index.md`

| Column | Meaning |
| --- | --- |
| `id` | The task file name, `{level}-{letter}` |
| `needs` | The ids this task waits for; empty means none |
| `rating` | `mechanical` or `hard` |
| `shares` | Named shared resources the task contends for: a test database, a lock, a port |
| `reads` | The `0-` files its executor opens with the task file |
| `files` | Every path the task creates, edits or deletes, from its frontmatter; the dispatcher verifies a `DONE` against them and the landing's commit names them |
| `title` | A few words, for the dispatcher's spawn label |

- The order of execution lives in `needs`, never in the file names. Dependencies form a graph, and a list of batches cannot hold a graph: a batch boundary makes a task wait for siblings it has nothing to do with.
- `level` is 1 when `needs` is empty, otherwise one above the deepest task it needs. Sorted file names are therefore always a valid serial order.
- The letter separates tasks of one level. A task continuing a single predecessor keeps its letter so a chain reads at a glance. The dispatcher never relies on the letter.
- `files` is in the index for verification and the commit, never for scheduling. Inside a flight, `flights-speccer` has already resolved every file overlap into one task or a `needs` chain, so the dispatcher schedules on `needs` and `shares` only; it reads `files` to check that a `DONE` changed them, and hands their union to `gitter`. Without the column the dispatcher would take the file list from the return it is verifying: the judge would be the judged.
- Each task file's frontmatter is the source of truth; the index is those fields collected into one table. On disagreement the task file wins.

### The ready rule

> A task starts the moment every id in its `needs` is done, as many at once as its `shares` admit. Holding a ready task for a sibling is a violation.

`flights-speccer` names a shared resource and never counts its capacity; the dispatcher applies the capacity at run time.

## The task file

Frontmatter carries `id`, `title`, `rating`, `needs`, `shares`, `reads` and `files` (the paths of the `Files` section, actions stripped). Eight sections follow, always in this order; `none` is a valid body.

| Section | Holds | Prevents |
| --- | --- | --- |
| `Goal` | The deliverable and why it exists, two sentences at most, then a `Never:` line: what is out of scope and which approaches are forbidden | An executor wandering before it knows the finish line, or past the fence |
| `Done when` | A matrix of scenarios (scenario · input or state · expected behaviour · error handling), then Given/When/Then lines for what the matrix cannot hold; behaviour only, never a command; closed by the fixed audit line | A task with no finish line; an acceptance command predicted for code that does not exist yet |
| `Progress dependency` | The drift instruction, then what the needed tasks must have landed | A whole directory drifting on a false premise |
| `Files` | Every file created, edited or deleted, with its action; a rename or deletion lists every reference, docs and tests included | Out-of-scope edits, half-finished renames and deletions |
| `Decisions` | Every design decision as one line of fact: mechanism, placement, names, failure behaviour, user-visible text | The executor re-deciding the design |
| `Shapes` | `EXISTING`: what the executor types against, quoted with its file path. `NEW`: what the task creates, by name, inputs, outputs and behaviour | Re-reading the code to learn a shape |
| `Steps` | Numbered in the order they run, inside-out; each names the file, the place as a quoted line of code, and the change as behaviour | The executor assembling an order from scattered constraints |
| `Execution judgments` | Every implementation call left to the executor | A hidden hole; it also computes the rating |

Reuse targets and the target directory's conventions are `EXISTING` shapes: a helper to import is quoted by its signature, and a house convention is stated where the executor will meet it.

A task file instructs and never argues. After its research `flights-speccer` holds the reasons, the rejected options and the history; an executor needs none of them and would re-read them on every call. They stay with `flights-speccer`, and the `NOTES` lines of the return are the only place a reason travels.

`Done when` is data, not a script. Each matrix row and each Given/When/Then line is an assertion the executor turns into a test (tests carried in the spec measured large gains in pass rate in the published ablations, with no failing-test loop needed). An exact command with predicted output, written for code that does not exist, is a guess that reads as verified; an executor rightly refuses to rewrite its own acceptance line, so each wrong guess stops the flight. The section closes with one fixed line in every task file: each row is covered by a test that ran and passed; a test that exists but did not run counts as missing; a test that disagrees with a row means the code is wrong, never the row; a row that can be read two ways returns `SPEC-DRIFT`. The project's standing checks (the test suite, the linter) arrive through a `0-` file or the dispatcher's brief.

## Altitude: what is pinned, what is described

Pin what crosses a boundary; describe what stays inside one.

| Pinned exactly | Described as behaviour, written by the executor |
| --- | --- |
| Existing shapes: table columns, types, helper signatures, API fields | Function bodies |
| What another task consumes: name, inputs, outputs | Queries |
| What crosses a layer or a project | Control flow |
| What the user sees: copy text, error codes | Local names |
| Where things live: paths, the directory's convention | |

- A task file carries no code bodies. A body in a spec is written twice, and the first writer has no compiler or test run to check it.
- `flights-speccer` has no compiler, so every invented detail is a chance to be wrong, and an early wrong detail bends every task after it. At a boundary it either copies what exists or defines something new, which keeps it where it is reliable.
- A place is a file path plus a quoted line of code. A quote survives edits by other tasks and fails loudly when stale, because the search finds zero matches or two. The path is the executor's fallback when the quote has moved.
- A task that depends on an earlier task names what that task creates by its decided name and signature; it never quotes code an earlier task is about to change.
- Exact text appears only where `flights-speccer` copied it from the code or saw a command print it during the run. A command, a flag, an exit code or an output it has not observed is written as intent. A shape or a literal written from memory is a defect: precision that was never run reads as verified. A value a run printed about its data — a count, a selection size, an id — is evidence for the return, never a `Done when` row: the state a run sees is dynamic, and a row written from one run is a coincidence written as a contract.

## Rating

The rating is computed from `Execution judgments`, not asserted.

- More than three judgments: `hard`.
- Any judgment that is a diagnosis of an unknown cause: `hard`, whatever the count.
- Otherwise: `mechanical`.

The rating picks the executor: `mechanical` → `flights-mechanical-executor` (`sonnet`), `hard` → `flights-hard-executor` (`opus`); the spawn carries no model override.

## The run

Seven phases, each producing one thing. The order keeps `flights-speccer`'s own context small: the first reading of an area goes to probes, the design is settled before any shape is collected, and writing comes last. A phase with nothing to ask is skipped; a one-task flight whose caller supplied the maps runs intake, design, one shapes round and write.

1. Intake. No reading. The input becomes a numbered list of requested changes, and the absent inputs are noted.
2. Map, probe round one. One message of `tracer` agents, one per area. Each returns a map of about one page with no file content quoted: where the area lives, who writes and reads it, which files would change, where this codebase already solves a similar problem and what can be reused from it, and how the area is checked (the command, where failures are logged; a suite runs once at most). A removal or a rename adds one probe that returns every place mentioning the thing. What the caller handed over is not asked again.
3. Design. Per change: mechanism, placement, names, the contracts that cross a boundary, failure behaviour, the outcome. Two rules. Right-size: the smallest design that delivers the numbered changes; every mechanism names the change it serves, a pattern borrowed from a reference is scaled to the project borrowing it, and what `flights-speccer` finds wise but nobody asked for is one line in the return's notes, never a task. Reuse: look for a similar problem this codebase already solves, follow its pattern, reuse whatever exists, and invent nothing that already exists. A decision a human would want the chance to overrule also gets one notes line; it is still decided.
4. Form tasks, as laid out below. It follows design because `needs` edges come from who creates and who consumes each contract.
5. Collect shapes, probe round two. Only now is it known which existing shapes each executor types against, which is why the rounds are two: quoting before designing means quoting everything. One message of collector agents (`haiku`) carries exact extraction orders for the shapes the design calls for: these columns, this signature, the line after which the insert goes. A shape in a task file comes only from this round; the caller's maps give direction, never a quote.
6. Write. Shared files first, then each task file inside a budget computed before it is written (16,000 characters minus the measured size of its `reads`), then the index, all files of a level in one message. Several writes in one message are one model call, so the context is re-sent once; writing over budget and trimming afterwards costs a call per trim at the run's largest context.
7. Reconcile. Six checks only `flights-speccer` is placed to make, each with a definite answer, fixed before returning. Restatement: no task file or `0-` file carries an instruction the executor or the gater agent already holds, or a rule of the testing manual ([What a spec never restates](#what-a-spec-never-restates)). Coverage: every numbered change from intake, and every mention of a removed or renamed thing, lands in exactly one task or in `BLOCKED`; a dropped change is invisible downstream, because an executor sees one file and the dispatcher opens none. Collision: no file appears in two tasks unless one needs the other. Size: the reading budget, measured. Sense: given its decisions, each task's outcome can come true; no two decisions contradict; nothing a task changes breaks something outside its `Files` that runs or reads it. Names: of the files `flights-speccer` writes, the directory holds `index.md`, `0-*.md` and `{level}-{letter}.md` and nothing else, whatever the caller asked for; `run.md` and `audit.md` belong to other writers and are never touched. The numbers travel in the return's `RECONCILED` line, so the caller sees that the checks ran.

A look smaller than a probe (one file, a listing, a search) `flights-speccer` does itself; mapping a whole area is what the first round's tracers are for. The template names each probe by its literal spawn parameters (`subagent_type: "tracer"`; `subagent_type: "general-purpose"` with `model: "haiku"`): named only by role, a model reaches for the harness's default search agent.

Probes run in the background. After spawning a round `flights-speccer` ends its message with one line and no tool call, and the harness delivers each report as it lands. A watch loop costs a call per look and can watch for a signal that never comes.

The map budget is also what keeps `flights-speccer` cheap to wake: every report lands in its context and is re-sent on every later call, including a [revising](#drift-adapt-or-spec-drift) call long after the run.

### Forming tasks

A task is one goal: one cohesive change, even when it spans layers and files. Two tasks exist only where two top-level deliverables could each be reviewed, tested and merged without the other. Verbs, "and"s and noun phrases are never counted, and the layers of one goal are never split: "add the export button and show its progress" is one task; "add the export button, move auth to tokens, and build the admin page" is three. Joining is the default and a cut has to pay for itself, because every cut costs twice: the fixed price of one more task, and one more in-between state of the code that never ships and still has to be designed correctly.

1. List every requested change with what it touches (files and shared resources) and what it needs to exist first.
2. Start from one task holding every change.
3. Cut only for a reason: the parts can run at the same time and the flight finishes sooner, or the task is too large for one executor (more than about 20 files or 25 steps). Cut only on a condition: each piece's outcome can be verified on its own. Pieces of a too-large task still overlap, so they form a chain, each needing the one before.
4. Absorb: a piece under about 5 files or 8 steps joins a neighbour, unless it runs beside something.
5. No filler: no task exists only to keep the state between two other tasks tidy, and none for work nobody asked for.
6. Extract hot files: a file nearly every parallel task would add a line to (a registry, a routes table, a barrel) gets all its edits in one task, placed before the others when they need it and after them when they do not.
7. Relate: `needs` when a task uses what another creates; `shares` when tasks contend for a resource and either order works. Contention written as `needs` forces an order nobody requires and stops a free slot from taking whichever task is ready.
8. Name each task by level and letter.

A flight whose parts cannot run side by side stays one task or a short chain. A wide flight comes out as small shared foundations first, a wide middle of tasks that do not overlap, and the hot-file task last.

## Drift: adapt, or `SPEC-DRIFT`

An executor keeps the work moving. The rule lives in the executor's body, never in a task file:

> Check the task's `Progress dependency` before step 1. If one does not hold, change nothing and return `SPEC-DRIFT {id}: {what you found}`. Anything else that differs from this spec: reach the Goal your own way and say what you changed in your return. If the Goal itself cannot be reached, stop and return `SPEC-DRIFT {id}` with what you found and what already landed. A `SPEC-DRIFT` or `FAILED` return names its cause — the line, the value and the code path that produced the red — or names what you read and says the cause is unknown; reading is never forbidden, a rerun and a fix outside this spec are.

- `Progress dependency` lists only facts whose absence breaks the rest of the directory. A detail the executor can adapt to stays out of it.
- The cause line exists because the executor is the one agent standing at the red with the logs open. A return that hands back a symptom and an artefact path moves the whole diagnosis to a reader who was not there. Measured once: five rounds on one task, each round's spec cut from the previous round's red line, 180 executor calls and four hours, because the executor's stop rule was read as "do not look" and nobody downstream looked either.
- The executor has an open hand inside its task: where a detail of the spec and the code disagree, the Goal wins and the return says so. `SPEC-DRIFT` is for a changed world and for a spec fault that puts the Goal out of reach (a contradiction, an outcome the decisions make impossible).
- A small adaptation upstream reaches the downstream executor through its brief: the orchestrator pastes the `run.md` lines of the tasks it needs, and each line names what its executor adapted. `SPEC-DRIFT` remains for what a line cannot carry: a needed task that landed nothing, or a contract that changed shape rather than name.
- On `SPEC-DRIFT` the dispatcher starts nothing that needs that task, sends the report, what already landed, the completed ids and the executor's transcript (its path, or the seat's name and transcript id) back to the same `flights-speccer`, and dispatches again from the rewritten index. The report is the executor's conclusion; the transcript is how it got there — what it read, what it ran, what each run printed — and the revising call reads it before rewriting a line.
- The dispatcher counts reds per id. The second `SPEC-DRIFT` or `FAILED` of one id means the cause is unknown, whatever the reports say: the revising call is diagnose-first (below). A third red of the same id is returned `BLOCKED {id}` with the executor's cause line as the question; there is no third rewrite. One fault per run, prescribed from the last red line, is the loop the executor's stop rule exists to prevent, and it reappears one level up the moment the spec writer prescribes without diagnosing.
- `FAILED` takes the same road with a different brief: the spec stood and the executor could not reach the Goal (tests that would not pass, a cap hit). The revising call cuts the failed task smaller or re-approaches it; the same task file is never sent out again unchanged. Decomposing on failure measured +33 points over decomposing up front.
- Only `flights-speccer` changes a task file. A dispatcher that patches one, or writes rulings beside the directory, makes a second spec over the first: the task files stop being the truth, every executor reads one more file, and the longest-lived context in the family does design work.
- Revising: given an existing spec directory, a reason (`SPEC-DRIFT`, `FAILED`, a failing check, a ruling on a `BLOCKED` question, a refinement), what already landed and the completed ids, `flights-speccer` leaves the completed task files as they are, rewrites, adds or removes the remaining ones, rebuilds the index, and returns as usual with the changed ids in its notes. The fix goes into the task file itself.
- A revising call often reaches a fresh `flights-speccer`: the one that wrote the directory was another agent's child, and a sub-agent cannot address it. The fresh one reads the directory as its map — the index, and the task files the reason names — and probes only for what the reason requires; the shapes already quoted are its own earlier work, and re-mapping the area would pay the whole run again.
- Diagnose-first, on the second red of one id: before any rewrite, `flights-speccer` reads the whole unit the task changes — the entire test, beat or module, never the window around the failing line — and the runtime path it exercises, through one `tracer` probe when the path leaves the unit, and writes the cause as a `Decisions` line in the task file. A rewrite without a named cause is the previous round again with new words. The executor transcripts of every round on that id are part of the reading.
- A red in production code the flight forbids fixing (a report-only flight, a fenced module) is not a spec fault and does not cycle: the same revising call records it where the project keeps known defects (a registry row, an owed line), narrows the task's `Done when` to what the executor may prove, and names the defect in `NOTES` for the return.
- A revising call leaves the task files of `CLAIMED` tasks untouched: their executors have read them, and a file rewritten under a live executor is two specs for one task. Its findings reach that task, if at all, through the next round.
- A value one run printed — a count, a selection size, an id — never enters a `Done when` row, even when `flights-speccer` watched it print: the state a run sees is dynamic, and a row written from one run is a coincidence written as a contract. Observed values are evidence in the return.

## `BLOCKED`: no open decisions, one question

`flights-speccer` decides everything the flight leaves open. It returns no open decisions: when a weaker model called it, an open decision would hand judgment downward.

A task that cannot be specified at all, because the input contradicts itself or a fact lives in neither the code nor the input, gets no file. It is returned as `BLOCKED` with what is missing and the one question whose answer would unblock it, phrased for the owner of the answer, together with every task that needs it. A spec directory holds only complete specs.

`/flights:spec` puts at most one such question to the user per flight before dispatch and sends the answer back as a revising call; a model caller records the item as `BLOCKED` in its own return and runs the rest. One clarifying question before generation measured about ten points of pass rate in the published work; a spec that guesses instead loses them silently.

## The return

```text
SPEC {spec directory}
{the index table, verbatim}
DISPATCH {the ready rule, one executor per task file briefed with its task file and reads files, the pin-then-rating model rule, the SPEC-DRIFT procedure}
RECONCILED {n} changes in {m} tasks, {k} blocked, {c} file collisions, largest read {x} chars
BLOCKED {id or item}: {what is missing} · {the one question} | none
NOTES {up to five lines} | none
```

- `NOTES` carries the decisions a human would want the chance to overrule, what `flights-speccer` found wise but nobody asked for, and after a revision the changed ids.
- `largest read` is the biggest task file plus its `reads`, measured; it shows the reading budget was counted.
- The index travels in the return so the caller spends no call reading it and cannot forget to. It is the same table as on disk, not a thinner one: two formats would be a second thing to keep in sync.
- The `DISPATCH` line is an order. The dispatcher is often a sub-agent that never sees the fleet prompt, so the return carries the rule.

## Not part of the design

| Left out | Reason |
| --- | --- |
| One executor walking several task files | Saves about 1%; the context built by working is the cost, not the spec text |
| Order encoded in file names or batch separators | Dependencies form a graph |
| Open decisions in the return | Judgment never delegates downward |
| Line-number anchors | They go stale the moment an earlier task edits the file |
| Code bodies in a task file | Written twice, verified by nobody |
| An acceptance command with predicted output | Written for code that does not exist; exact and never run, it reads as verified |
| Reasons, rejected options and history in a task file | The executor needs none of them and re-reads them on every call |
| A format chosen by the caller or copied from the repository's older specs | One shape everywhere is what the dispatcher and the tooling rely on |
| Rulings written by the dispatcher beside the directory | A second spec over the first; a spec fault goes back to `flights-speccer` |
| Recursive review passes inside `flights-speccer` | A quoted shape checks itself at execution time; the reconcile phase's six checks are the review |
| Scheduling between flights | The main chat runs flights one after another |
| Proof-of-concept and research modes | A task that needs a proof is rated `hard` and goes to a frontier-judgment executor |

## Measuring a spec

A spec is judged from the transcript of the executor that ran it.

| Measure | Healthy | A broken spec shows |
| --- | --- | --- |
| Files read outside `Files` and `reads` | None | The executor learning shapes the spec left out |
| Calls before the first edit | A handful: open the spec, open the target, edit | A long reading phase |
| Peak context | Small enough that the run, fail, fix loop stays cheap | A task that should have been split |
| Calls per executor | Between about 40 and 80 | Fewer: tasks cut too small, the fixed price paid too often. More: a task that should have been cut |
| `SPEC-DRIFT` returns | Rare | Progress dependencies that list adaptable details, a wrong early decision, or a spec that contradicts itself |
| `flights-speccer`'s own peak context | Low enough that a revising call stays cheap | Map reports that quoted files instead of mapping them |
| "Adapted" notes in returns | Few, and never about a pinned shape | Shapes written from memory |

## Surfaces that stay in sync

| Surface | File | Holds |
| --- | --- | --- |
| The agent | `templates/global/agents/flights-speccer.md` | The executable protocol, the only place the format is spelled out for a model; its `description` is the caller's input contract. It is pinned at apex (`fable`), effort `high`, and a small flight's caller passes `model: "opus"` on the spawn |
| The fleet prompt | `pfm/harness-prompts/share/tail.md` § Orchestration | The outer contract: unspecified work goes to `flights-speccer` with content and never a format; the flight directory, the index and the ready rule; only `flights-speccer` changes a task file |
| The command | `/flights:spec` | The human-in-the-loop front end: the maps, the user's answers, the hand-off, the one question, the presentation. It never restates the format |
| The adopter contract | `CLAUDE.md` and `templates/project/CLAUDE.md`, `/pcm` | Wording that names the spec writer and how a spec travels |

## Open items

- The size limits (absorb under about 5 files or 8 steps, cut over about 20 files or 25 steps) and the reading budget (16,000 characters) are first values. Tune them until executors land between about 40 and 80 calls.
- Whether `flights-speccer` spawns more of itself for a very large flight, and how it partitions by file ownership.
- Distilling `/architecture-design` into how `Decisions` is written.
- A test on this repository that measures whether `flights-speccer` reuses what exists instead of inventing it.
- A `pfm` verb that compiles and validates `index.md` from the task files' frontmatter.
