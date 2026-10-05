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
- Every task has a fixed price whatever its size: a task file written at the apex or smart tier, and a fresh executor that opens its spec and re-reads the files it will change before its first edit. Executors that finished in 17 to 24 calls stayed under 90K of context, far below the range where growth hurts. A flight is therefore cut only as far as each cut pays for itself, and every part of the design that asks for more (more tasks, more decisions, more text, more quotes) carries a budget.

## The two flows

`flights-speccer` is called in two ways and behaves identically in both. The only difference is who supplies the decisions.

### Human in the loop: `/flights:spec`

1. The user calls `/flights:spec`.
2. The command sends `tracer` agents to map the area.
3. The command asks the user its questions, technical and product.
4. The command hands `flights-speccer` the decisions, the maps, the requirements and the flight directory.
5. `flights-speccer` writes the flight directory and returns.
6. A `BLOCKED` item carrying a question goes to the user once, and the answer returns as a [revising](#drift-adapt-or-spec-drift) call. The command then presents the index; running it is the user's separate decision.

### Model caller: work handed to a sub-agent

1. A main chat hands work to a sub-agent of any type.
2. Handed large work whose solution is not in hand (a design to choose, a failure of unknown cause: the ladder's third rung), the sub-agent's first tool call spawns `flights-speccer` at its pin (`opus`), handing it the work, everything it holds and a directory under `$HOME/.local/state/pfm/flights/{project}/`.
3. `flights-speccer` writes the flight directory and returns the path and the index.
4. A directory holding a single task the sub-agent executes itself; one holding several it hands to `flights-orchestrator`, which dispatches one fresh executor per task file by the [ready rule](#the-ready-rule) and executes none itself.

Ordering between flights belongs to the main chat that runs them, one after another. `flights-speccer` sees one flight and nothing beside it. Never two calls run on one directory at once, except a planner's [children](#nesting-a-large-flight-is-written-by-child-speccers); the agent body's § Input says so.

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
| The flight directory | One directory under `$HOME/.local/state/pfm/flights/{project}/`; `flights-speccer` writes everything there, and [its manual](#the-speccers-manual) beside it, and nowhere else |

### What a spec never restates

Anything the [executor](flights-executors.md) or the [lander](flights-lander.md) agent holds is never written into a task file or a `0-` file: how to run or wait for a command in general, read discipline, the one-red-run proof, the return format, the review, the cap, the layout laws, the testing manual's content. A `0-` file carries project facts several tasks need; an instruction every executor follows on every task belongs to the executor agent, and a project testing rule belongs to the testing manual — finding one missing is a `NOTES` line, never a paragraph in the spec. The task file's fixed lines went the same way: the closing line of `Done when` and the opening line of `Progress dependency` restated executor rules and now live in the executor's body; a task file keeps only what is a fact about this task. Reconcile's restatement check enforces it. The measured cost of the old way: 4.3 KB and 12.3 KB of generic instruction per executor in two audited flights, rewritten every revising round.

### Layout laws at design time

Four laws of `/quality:llm-codebase` bind when tasks are cut, and live in the Design phase: a task cuts along a unit of change, never across one; the unit's fixed file set decides `Files`; no new hand-kept parallel list and no directory named by negation, a registry being a hot file; a change crossing a wire boundary starts at the contract package's consumer index, and `needs` follows it. The laws that bind while a file is written live in the executor.

### The speccer's manual

`$HOME/.local/state/pfm/flights/{project}/speccer-manual.md`, beside the project's flight directories: in the flights state root, outside the repository, so the git-read-only speccer writes it and no guard or docs-ownership rule reaches it. It holds the static facts that barely change, each with its path: the build units (each with its own testing manual) and their inside-out dependency order, where the shared contract lives and what it generates, each unit's testing manual path and gates, test homes, hot files. `/flights:init` writes it; `flights-speccer` reads it at intake when present, writes it from its map phase when absent, and rewrites every line it finds wrong. The code wins over it, and it is never a source of shapes: a shape is always pasted from output the run printed.

### The model by round

Every spec, first or revising, runs at the agent's pin (`opus`). Every revising round is spawned fresh by the orchestrator, or by its caller for a ruling, and reads the `RETRO` lines of `run.md` with the executor's transcript, through the `transcript` skill's `show` digest: a raw rollout of 1.1–2.8 MB is 13–33 KB there, and on four real reds a reader of the digest named the same stop cause as a reader of the raw file for 15–40% less spend on the Codex seats. The raw transcript was the brief's pointer before, and the revising speccers did not open it.

With `/flights:spec` the fourth row arrives full, because the user answered the questions. From a model caller it arrives thin, and `flights-speccer` decides.

A caller brings content and never a format. File names, index columns, extra sections and the shape of the return belong to `flights-speccer` alone and are the same in every project: a format asked for in the spawn prompt is ignored, and so is the style of spec directories already in the repository. The dispatcher and every tool built on a spec directory rely on that one shape. The agent's `description` tells callers the same.

## Output: three readers, three artifacts

Each reader reads only its own artifact, and nothing is stated twice.

| Reader | Artifact | Carries |
| --- | --- | --- |
| The executor | One task file plus the shared files it names | What to do, how, the shapes it types against, the steps in order, its outcome |
| The dispatcher | `index.md`, repeated in the return | Per task: what it needs, what it shares, its rating, what it reads |
| The caller | The return message | The directory path, the index, any `BLOCKED` task, notes |

## The spec directory

```text
$HOME/.local/state/pfm/flights/{project}/
  speccer-manual.md  the speccer's manual, shared by every flight
  {flight}/
    index.md      one row per task
    0-{topic}.md  a shared file, only when two or more tasks need the same content
    1-a.md        a task file: {level}-{letter}.md
    2-a.md
    3-a.md  3-b.md  3-c.md
```

- One task file is one task, one fresh executor and one outcome.
- Content one task needs lives in that task. Content two or more tasks need lives once in a `0-` file, and each task names only the `0-` files it uses. This is the only pointer a task file holds that is not a code path, and it points into the spec directory, which nobody edits while executors run. A `0-` file is never the place for every shape of every task: each executor would pay for all of it to use a fraction.
- The reading budget: a task file plus the `0-` files it reads stays under 25,000 characters. `flights-speccer` measures it before returning; over budget, it cuts words first and the task second.
- Discover once: whatever every executor would otherwise find out alone (how to run the checks, where failures are logged, a house convention) is found in the map round and written in one `0-` file.
- The dispatcher's brief names the task file and its `0-` files together, and the executor opens them all in its first message.

## `index.md`

| Column | Meaning |
| --- | --- |
| `id` | The task file name, `{level}-{letter}` |
| `needs` | The ids this task waits for; empty means none |
| `rating` | `mechanical`, `precise` or `smart`; `main-chat` for a task the main chat applies itself |
| `shares` | Named shared resources the task contends for: a test database, a lock, a port |
| `reads` | The `0-` files its executor opens with the task file |
| `files` | Every path the task creates, edits or deletes, from its frontmatter; the dispatcher verifies a `DONE` against them and the landing's commit names them |
| `title` | A few words, for the dispatcher's spawn label |

- The order of execution lives in `needs`, never in the file names. Dependencies form a graph, and a list of batches cannot hold a graph: a batch boundary makes a task wait for siblings it has nothing to do with.
- `level` is 1 when `needs` is empty, otherwise one above the deepest task it needs. Sorted file names are therefore always a valid serial order.
- The letter separates tasks of one level. A task continuing a single predecessor keeps its letter so a chain reads at a glance. The dispatcher never relies on the letter.
- `files` is in the index for verification and the commit, never for scheduling. Inside a flight, `flights-speccer` has already resolved every file overlap into one task or a `needs` chain, so the dispatcher schedules on `needs` and `shares` only; it reads `files` to check that a `DONE` changed them, and hands their union to `gitter`. Without the column the dispatcher would take the file list from the return it is verifying: the judge would be the judged.
- Each task file's frontmatter is the source of truth; the index is those fields collected into one table by `templates/global/commands/flights/flight-index.mjs`, which writes a title and the table and nothing else. Every speccer, dispatcher, lander and audit re-reads the index, so a note written into it is paid on every later call: in a 40-task flight the revisions' notes had grown to 36 KB of a 53 KB index. A fact a later round needs goes into the Decisions of the task it binds, or a `0-` file when two tasks need it; history goes in the return's `NOTES`.

### The ready rule

> A task starts the moment every id in its `needs` is done, as many at once as its `shares` admit. Holding a ready task for a sibling is a violation.

`flights-speccer` names a shared resource and never counts its capacity; the dispatcher applies the capacity at run time.

## The task file

Frontmatter carries `id`, `title`, `rating`, `needs`, `shares`, `reads` and `files` (the paths of the `Files` section, actions stripped). Eight sections follow, always in this order; `none` is a valid body.

| Section | Holds | Prevents |
| --- | --- | --- |
| `Goal` | The deliverable and why it exists, two sentences at most, then a `Never:` line: what is out of scope and which approaches are forbidden | An executor wandering before it knows the finish line, or past the fence |
| `Done when` | A matrix of scenarios (scenario · input or state · expected behaviour · error handling), then Given/When/Then lines for what the matrix cannot hold; every row carries a concrete example in real values the speccer decides: a unit row `call(args) → result` (errors `→ throws X` or `→ error {…}`), an integration row `given … / when … / then …` at the project's real entry and exit; behaviour only, never a command; the flight's own checks (the full suite, the static gate, "the flight green") and the testing manual's floors are the lander's gate, never a task's | A task with no finish line; a test whose values drift from the decided contract; an acceptance command predicted for code that does not exist yet; a "make the whole flight green" task that runs the gate's checks a second time |
| `Progress dependency` | What the needed tasks must have landed | A whole directory drifting on a false premise |
| `Files` | Every file created, edited or deleted, with its action; a rename or deletion lists every reference, docs and tests included; the gate-owned data the change trips (line baselines, exemption lists, codegen outputs, mirrored or twin tests, a test home already at its line ceiling); every hit of the caller sweep lands here, in a later task's `Files` under a `Temporary reds` line, or in a Decisions line stating it out of scope | Out-of-scope edits, half-finished renames and deletions, a gate tripped by a file nobody listed |
| `Decisions` | Every design decision as one line of fact: mechanism, placement, names, failure behaviour, user-visible text; `Temporary reds: {test ids} · green by {task id}`, written only by the speccer, for tests this task leaves red that a later task turns green; when one project's work spans several tasks, its first task carries the project's integration examples and writes their test red under this line, green by the project's last task; `External need: {flight directory} {id}` for another flight's output, dispatched on once that flight's `run.md` shows the id `DONE` | The executor re-deciding the design |
| `Shapes` | `EXISTING`: what the executor types against, quoted with its file path. `NEW`: what the task creates, by name, inputs, outputs and behaviour | Re-reading the code to learn a shape |
| `Steps` | Numbered in the order they run, inside-out; each names the file, the place as a quoted line of code, and the change as behaviour; none re-breaks or mutates working code to watch a red, and a new gate proves its bite on a fixture in `Files` | The executor assembling an order from scattered constraints; working code broken to stage a proof |
| `Execution judgments` | Every implementation call left to the executor | A hidden hole; it also computes the rating |

Reuse targets and the target directory's conventions are `EXISTING` shapes: a helper to import is quoted by its signature, and a house convention is stated where the executor will meet it. The layout laws bind `precise` and `smart` executors through their bodies; a `mechanical` executor makes no design choice, so in a mechanical task the speccer quotes the façade and every reuse target as `EXISTING` shapes.

A task file instructs and never argues. After its research `flights-speccer` holds the reasons, the rejected options and the history; an executor needs none of them and would re-read them on every call. They stay with `flights-speccer`, and the `NOTES` lines of the return are the only place a reason travels.

`Done when` is data, not a script. Each matrix row and each Given/When/Then line is an assertion the executor turns into a test (tests carried in the spec measured large gains in pass rate in the published ablations, with no failing-test loop needed); the test keeps its example's inputs and expected values exactly, and only the test framework's idiom is the executor's own. An example states the contract the speccer decides. An exact command with predicted printed output, written for code that does not exist, is a guess that reads as verified, and stays banned; an executor rightly refuses to rewrite its own acceptance line, so each wrong guess stops the flight. How a row is proven, and what a row read two ways does, live in the executor's body, never as lines in a task file: a row read two ways takes the reading today's code supports, named in the return, and `SPEC-DRIFT` only when neither reading settles it and they build different code. The project's standing checks (the test suite, the linter) arrive through a `0-` file or the dispatcher's brief.

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
- Exact text appears only where `flights-speccer` copied it from the code or saw a command print it during the run. A command, a flag, an exit code or an output it has not observed is written as intent. A shape or a literal written from memory is a defect: precision that was never run reads as verified. A value a run printed about its data — a count, a selection size, an id — is evidence for the return, never a `Done when` row, whose example values are decided, never observed: the state a run sees is dynamic, and a row written from one run is a coincidence written as a contract.

## Rating

The rating is computed from `Execution judgments`, not asserted. A call the task's `Decisions` already settle is not a judgment. A judgment counts only when a wrong call breaks a `Done when` row or reaches past the task's files; a local choice — a helper's name, awk over printf, where a fixture sits — is listed and never counted. In a `mechanical` task each local choice is written as a `Decisions` line, so the mechanical executor never meets a name the task does not give. Counted literally, 28 of 28 historical `mechanical` tasks carried one or two such local choices and would all have been promoted to `precise`, paying `xhigh` for no quality.

- A path in `Files` the guard keeps for the main chat (`.claude/**`, a `CLAUDE.md`): `main-chat`, whatever the count. No executor can write it, since the guard denies every sub-agent, so the main chat applies the task under `/pcm`. The first, fast-gate's 2-a, carried the value before it existed and the index script refused it.
- More than three judgments: `smart`.
- An interface the task touches left unpinned: `smart`, whatever the count.
- Any judgment that is a diagnosis of an unknown cause: `smart`, whatever the count.
- A deliverable that is a document, a prompt, a spec or a report: `smart`, whatever the count. Writing for a reader to act on is reasoning, however exactly the task file words it.
- One to three judgments, with every interface the task touches pinned in `Shapes`: `precise`.
- No judgment, but the difficulty is the implementation itself — concurrency, failure paths, many error rows: `precise`.
- Otherwise, no judgment, every touched interface pinned and the implementation itself not the difficulty: `mechanical`, which means repetitive, straightforward work that needs no reasoning to do right.

The rating picks the executor: `mechanical` → `flights-mechanical-executor`, `precise` → `flights-precise-executor`, `smart` → `flights-smart-executor`, `main-chat` → none, the main chat applies it; the spawn carries no model override. Each tier's model and effort, and the measurement that set them, are the [tier table](flights-executors.md#the-tiers).

## The run

Seven phases, each producing one thing. The order keeps `flights-speccer`'s own context small: the first reading of an area goes to probes, the design is settled before any shape is collected, and writing comes last. A phase with nothing to ask is skipped; a one-task flight whose caller supplied the maps runs intake, design, one shapes round and write.

1. Intake. No reading beyond [the speccer's manual](#the-speccers-manual), when present. The input becomes a numbered list of requested changes, and the absent inputs are noted.
2. Map, probe round one. `tracer` probes, as many as the speccer judges, all in one message, each handed the repo root and numbered questions; test homes, the check command and the verbatim lines to paste come back unasked, and a `NOT READ` is asked again or read, never taken as a fact (an experiment: see Open items). A removal or a rename adds one probe that returns every place mentioning the thing. What the caller handed over is not asked again. With no manual, the speccer writes it from these maps.
3. Design. Per change: mechanism, placement, names, the contracts that cross a boundary, failure behaviour, the outcome. Two rules. Right-size: the smallest design that delivers the numbered changes; every mechanism names the change it serves, a pattern borrowed from a reference is scaled to the project borrowing it, and what `flights-speccer` finds wise but nobody asked for is one line in the return's notes, never a task. Reuse: look for a similar problem this codebase already solves, follow its pattern, reuse whatever exists, and invent nothing that already exists. A decision a human would want the chance to overrule also gets one notes line; it is still decided.
4. Form tasks, as laid out below. It follows design because `needs` edges come from who creates and who consumes each contract.
5. Collect shapes. Only after the design is it known which existing shapes each executor types against; quoting before designing means quoting everything. The tracers' fenced verbatim lines are shapes already and are pasted as they stand. What is still missing, and only the lines a task will quote, never a whole file, the speccer reads itself by line range, or sends to `collector` when many shapes sit in files it has not read. A mandated `codeprobe.py collect` plan was measured and dropped: 2 of 72 speccer runs used it, because 586 of the 631 reads in one flight's runs came before the first task file, so the text was already in context; in a matched test on 21 real orders a plan cost about 20% more money and context than reading the ranges directly, at the same fidelity. Measured on the tracer replay: a phase 5 that re-ordered everything, 14 whole files among it, wrote 4,363 lines, of which 11% reached the task files and half of those the tracers had already returned; reading it was about a quarter of the speccer's tokens. A shape in a task file is copied, never typed from memory; the caller's maps give direction, never a quote.
6. Sweep, then write. Before a task's `Files` close, the speccer sweeps the whole repository (tests, fixtures, golden files, `*.tsv` and `*.json` registries, scripts, docs) with `grep` or a tracer for every symbol, signature, file, flag, literal and behaviour the task changes or deletes, and every output string and error text the changed behaviour prints; every hit lands in this task's `Files`, in a later task's `Files` with a `Temporary reds` line, or in a Decisions line stating it out of scope and why. Measured: one flight's first 13 claims had 6 `SPEC-DRIFT` or `FAILED` reds, every one outside `Files`; after the sweep, none. Shared files first, then each task file inside a budget computed before it is written (25,000 characters minus the measured size of its `reads`), then the index, all files of a level in one message. Several writes in one message are one model call, so the context is re-sent once; writing over budget and trimming afterwards costs a call per trim at the run's largest context.
7. Reconcile. The index script runs first: it rebuilds `index.md` from the frontmatter and checks what a script can decide — frontmatter shape, `needs` ids that name no task, `reads` files that are missing, cycles, file collisions (a file in two tasks with no `needs` chain between them), and the reading budget, measured per task file plus its `reads`. Exit 1 prints one `ERROR` line per defect; exit 2 means it built nothing, so a failed run never reads as an empty flight. Then four checks only `flights-speccer` is placed to make, each with a definite answer, fixed before returning. Restatement: no task file or `0-` file carries an instruction the executor or the lander agent already holds, or a rule of the testing manual ([What a spec never restates](#what-a-spec-never-restates)). Coverage: every numbered change from intake, and every mention of a removed or renamed thing, lands in exactly one task or in `BLOCKED`; a dropped change is invisible downstream, because an executor sees one file and the dispatcher opens none. Sense: given its decisions, each task's outcome can come true; no two decisions contradict; every sweep hit is placed, so nothing a task changes breaks something outside its `Files` that runs or reads it beyond its listed temporary reds; every task fits the cut size of [Forming tasks](#forming-tasks); a step that demands a red has the test edits it needs in `Files`. Names: of the files `flights-speccer` writes, the directory holds `0-*.md` and `{level}-{letter}.md` and nothing else, whatever the caller asked for; the script writes `index.md`; `run.md` and `audit.md` belong to other writers and are never touched. The numbers travel in the return's `RECONCILED` line — task count, collisions and largest read are the script's — so the caller sees that the checks ran.

A look smaller than a probe (one file, a listing, a search) `flights-speccer` does itself; mapping a whole area is what the first round's tracers are for. The template names each probe by its literal spawn parameters (`subagent_type: "tracer"` for questions, `subagent_type: "collector"` for shapes): named only by role, a model reaches for the harness's default search agent.

Probes run in the background. After spawning a round `flights-speccer` ends its message with one line and no tool call, and the harness delivers each report as it lands. A watch loop costs a call per look and can watch for a signal that never comes.

The map budget is also what keeps `flights-speccer` cheap to wake: every report lands in its context and is re-sent on every later call, including a [revising](#drift-adapt-or-spec-drift) call long after the run.

### Forming tasks

A task is one goal of one project: one cohesive change, even when it spans layers and files inside it. Two tasks exist only where two top-level deliverables could each be reviewed, tested and merged without the other. Verbs, "and"s and noun phrases are never counted, and the layers of one goal are never split: "add the export button and show its progress" is one task; "add the export button, move auth to tokens, and build the admin page" is three. Joining is the default and a cut has to pay for itself, because every cut costs twice: the fixed price of one more task, and one more in-between state of the code that never ships and still has to be designed correctly.

A goal that spans several projects — build units, each with its own testing manual — becomes one task per project, ordered inside-out by dependency. The shared contract between the projects comes first, and its task owns the contract and every output generated from it in the consumer projects (generated types, vendored copies). Every other project's task `needs` only the contract task, and those tasks run in parallel against the contract; a project `needs` another project's task only when it uses that task's code beyond the contract. The order is derived every run from the map and [the speccer's manual](#the-speccers-manual), never fixed in this design.

1. List every requested change with what it touches (files and shared resources) and what it needs to exist first.
2. Start from one task per project, holding that project's changes.
3. Cut only for a reason: the parts can run at the same time and the flight finishes sooner, or the task is too large for one executor (more than 15 files or about 25 steps). Cut only on a condition: each piece's outcome can be verified on its own. Pieces of a too-large task still overlap, so they form a chain, each needing the one before.
4. Absorb: a piece under about 5 files or 8 steps joins a neighbour in its project, unless it runs beside something.
5. No filler: no task exists only to keep the state between two other tasks tidy, and none for work nobody asked for.
6. Extract hot files: a file nearly every parallel task would add a line to (a registry, a routes table, a barrel) gets all its edits in one task, placed before the others when they need it and after them when they do not.
7. Relate: `needs` when a task uses what another creates; `shares` when tasks contend for a resource and either order works. Contention written as `needs` forces an order nobody requires and stops a free slot from taking whichever task is ready. A `shares` still serializes its tasks, so it is cut first: contenders touch different build units (one Go package, one TS project); when unavoidable, the share stays off the tasks the rest of the flight needs first, and share-mates are ordered by `needs`, smallest first. Measured: one Go package share held a single task for 84 minutes while another package share queued four. Another flight's output is a Decisions line `External need: {flight directory} {id}`, never a `needs` entry: the index script checks `needs` against this directory's ids only.
8. Name each task by level and letter.

A flight whose parts cannot run side by side stays one task or a short chain. A wide flight comes out as small shared foundations first, a wide middle of tasks that do not overlap, and the hot-file task last.

### Nesting: a large flight is written by child speccers

A speccer's cost grows with the number of task files it writes in one context, because every quoted shape, every written file and every probe report is re-sent on each later call. Measured over sixteen first-spec runs: writing 1 to 4 task files cost 11 to 43 calls, 0.6M to 4.0M tokens and a peak context of 71K to 159K; writing 7 to 12 cost 90 to 127 calls, 10.4M to 22.3M tokens and a peak of 267K to 352K. The expensive runs barely explored: one of them made a single exploration call and still reached 18.5M, because its twelve task files and ten tracer reports shared one context.

So when the tasks fall into separate contexts — different projects, builds or subsystems that share no files and need different maps — the speccer becomes the planner and writes no task file itself. Tasks sharing one context stay with one writer however many there are: the raised reading budget lets that writer keep its multi-pass writing, and the split is for separation of context, not for count.

1. The planner runs intake, map, design and form tasks as usual, fixing every task's id, level, `needs`, `shares`, `files` and goal line.
2. It cuts one batch per context. A batch owns its files outright: no file is written by two batches.
3. It writes the shared `0-` files, pinning every contract that crosses from one batch to another, so no batch waits on another batch's design.
4. It spawns one child per batch, all in one message, each as `Agent(subagent_type: "flights-speccer")`. The brief carries the batch plan (every batch's rows, so each child sees the contracts around its own), the design lines for the child's tasks, the maps that concern them and the directory. A contract the planner cannot pin before a child designs it puts that child first and the batches that consume it after it; that is the only reason children run in sequence.
5. A child skips intake and map, collects its shapes, writes exactly its assigned ids, reconciles its own files with the index script's `--check` mode, which writes nothing, and returns the ids it wrote. It never nests again. A task it cannot write as assigned comes back with the reason, and the planner re-cuts that batch.
6. Once every child has returned, the planner runs the index script and reconciles over its output, never by opening a task file: collisions, sizes and graph defects from the script, coverage from the `files` column, names by listing the directory. Restatement and sense within a task are each child's own reconcile. A batch whose child never returns shows as missing ids; the planner spawns that batch once more, and a second miss is `BLOCKED`.

Batches exist only inside the spec run. The index stays one graph: the orchestrator dispatches from `needs` and never sees a batch. A revising call never nests: it rewrites a few task files in a directory it reads as its map.

## Drift: adapt, or `SPEC-DRIFT`

An executor keeps the work moving. The rule lives in the executor's body, never in a task file. The red rule per tier, with its outside-defect `DONE` exception and the `blocked by` return of a cause outside `Files`, is in [What it holds](flights-executors.md#what-it-holds).

What else differs from the spec is per tier. `precise` and `smart` reach the Goal their own way inside `Files` and say what they changed; a Goal that cannot be reached stops with `SPEC-DRIFT {id}`, what was found and what already landed. `mechanical` applies the Steps and makes only its listed adaptations — a quoted line found at another place, an import the edit needs, the formatter's output, a fix for its own red; anything else returns `SPEC-DRIFT`.

- `Progress dependency` lists only facts whose absence breaks the rest of the directory. A detail the executor can adapt to stays out of it.
- The cause line exists because the executor is the one agent standing at the red with the logs open. A return that hands back a symptom and an artefact path moves the whole diagnosis to a reader who was not there. Measured once: five rounds on one task, each round's spec cut from the previous round's red line, 180 executor calls and four hours, because the executor's stop rule was read as "do not look" and nobody downstream looked either.
- A `precise` or `smart` executor has an open hand inside its task: where a detail of the spec and the code disagree, the Goal wins and the return says so. A `mechanical` executor has no open hand: a disagreement beyond its listed adaptations is `SPEC-DRIFT`. `SPEC-DRIFT` is for a changed world and for a spec fault that puts the Goal out of reach (a contradiction, an outcome the decisions make impossible).
- A small adaptation upstream reaches the downstream executor through its brief: the orchestrator pastes the `run.md` lines of the tasks it needs, and each line names what its executor adapted. `SPEC-DRIFT` remains for what a line cannot carry: a needed task that landed nothing, or a contract that changed shape rather than name.
- On `SPEC-DRIFT` the dispatcher starts nothing that needs that task, sends the report, what already landed, the completed ids and the executor's transcript (its path, or the seat's name and transcript id) back to a fresh `flights-speccer`, and dispatches again from the rewritten index. The report is the executor's conclusion; the transcript is how it got there — what it read, what it ran, what each run printed — and the revising call reads it before rewriting a line.
- The orchestrator marks the revising call diagnose-first on the second `FAILED` or `SPEC-DRIFT` line of one id (`WAIT` and `TOO-LARGE` lines are not reds); a call so marked means the cause is unknown, whatever the reports say, and `flights-speccer` runs diagnose-first only on a call so marked, never by counting `run.md` lines itself (below). A third red of the same id is returned `BLOCKED {id}` with the executor's cause line as the question; there is no third rewrite. One fault per run, prescribed from the last red line, is the loop the executor's stop rule exists to prevent, and it reappears one level up the moment the spec writer prescribes without diagnosing.
- `FAILED` takes the same road with a different brief: the spec stood and the executor could not reach the Goal (tests that would not pass, a cap hit). The revising call cuts the failed task smaller or re-approaches it; the same task file is never sent out again unchanged. Decomposing on failure measured +33 points over decomposing up front.
- Only `flights-speccer` changes a task file. A dispatcher that patches one, or writes rulings beside the directory, makes a second spec over the first: the task files stop being the truth, every executor reads one more file, and the longest-lived context in the family does design work.
- Revising: given an existing spec directory, a reason (`SPEC-DRIFT`, `FAILED`, a failing check, a ruling on a `BLOCKED` question, a refinement), what already landed and the completed ids, `flights-speccer` leaves the completed task files as they are, rewrites, adds or removes the remaining ones, rebuilds the index, and returns its table cut to the rows of the task files it added or rewrote, and one line `REVISED {those ids} · REMOVED {ids}`; the revised ids a caller names are that line's. The index script's `DELTA` line is not the carrier: it compares index rows only, so a ruling that rewrites a task file's `Decisions` or `Steps` and leaves its frontmatter alone is never `changed`, and the script rewrites `index.md` on every run, so the run that exits clean drops the ids an earlier unclean run reported. The fix goes into the task file itself, and a rewritten task file states what the previous round of the same task already landed.
- Every revising round is a fresh `Agent(subagent_type: "flights-speccer", model: "opus")`: the one that wrote the directory was another agent's child, and a sub-agent cannot address it. The one `SendMessage` revision is [`/flights:spec` S4](flights-spec.md#s4--the-one-question). The fresh one reads the directory as its map — the index, and the task files the reason names — and probes only for what the reason requires; the shapes already quoted are its own earlier work, and re-mapping the area would pay the whole run again.
- Diagnose-first, on a call marked so: before any rewrite, `flights-speccer` reads the whole unit the task changes — the entire test, beat or module, never the window around the failing line — and the runtime path it exercises, through one tracer when the path leaves the unit, and writes the cause as a `Decisions` line in the task file. A rewrite without a named cause is the previous round again with new words. The executor transcripts of every round on that id are part of the reading.
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
RECONCILED {n} changes in {m} tasks, {b} batches, {k} blocked, {c} file collisions, largest read {x} chars
BLOCKED {id or item}: {what is missing} · {the one question} | none
NOTES {up to five lines} | none
```

- `batches` is 0 when the speccer wrote every task file itself, and the number of children when it [nested](#nesting-a-large-flight-is-written-by-child-speccers).
- `NOTES` carries the decisions a human would want the chance to overrule, what `flights-speccer` found wise but nobody asked for.
- `largest read` is the biggest task file plus its `reads`, measured; it shows the reading budget was counted.
- The index travels in the return so the caller spends no call reading it and cannot forget to. It is the script's table, the same as on disk, not a thinner one: two formats would be a second thing to keep in sync. A revising call returns only the rows of the task files it added or rewrote, and its `REVISED` line: its caller already holds the table, and a full copy on every revision filled the dispatcher's context with rows it had.
- No dispatch rule travels in the return: the `flights-orchestrator` agent holds the ready rule, the rating → executor map and the `SPEC-DRIFT` road, and a copy here was a second home for one rule.

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
| Recursive review passes inside `flights-speccer` | A quoted shape checks itself at execution time; the reconcile phase's index script and four checks are the review |
| Scheduling between flights | The main chat runs flights one after another |
| Proof-of-concept and research modes | A task that needs a proof is rated `smart` |

## Measuring a spec

A spec is judged from the transcript of the executor that ran it.

| Measure | Healthy | A broken spec shows |
| --- | --- | --- |
| Files read outside `Files` and `reads` | None | The executor learning shapes the spec left out |
| Calls before the first edit | A handful: open the spec, open the target, edit | A long reading phase |
| Peak context | Small enough that the run, fail, fix loop stays cheap | A task that should have been split |
| Calls per executor | Between about 40 and 80 | Fewer: tasks cut too small, the fixed price paid too often. More: a task that should have been cut |
| `SPEC-DRIFT` returns | Rare | Progress dependencies that list adaptable details, a wrong early decision, or a spec that contradicts itself |
| `flights-speccer`'s own peak context | Low enough that a revising call stays cheap | Map reports that quoted files instead of mapping them, or the tasks of separate contexts written in one context instead of by children |
| "Adapted" notes in returns | Few, and never about a pinned shape | Shapes written from memory |

## Surfaces that stay in sync

| Surface | File | Holds |
| --- | --- | --- |
| The agent | `templates/global/agents/flights-speccer.md` | The executable protocol, the only place the format is spelled out for a model; its `description` is the caller's input contract. It is pinned at `opus`, effort `high`, with `autoCompact: forceAt: 600k`, which forces compaction of the speccer's context at 600k tokens |
| The fleet prompt | `pfm/harness-prompts/share/tail.md` § Orchestration | The outer contract: unspecified work goes to `flights-speccer` with content and never a format; the flight directory, the index and the ready rule; only `flights-speccer` changes a task file |
| The index script | `templates/global/commands/flights/flight-index.mjs` | The only writer of `index.md`, and the reconcile checks a script can decide; its tests run in the templates gate |
| The transcript skill | `templates/global/skills/transcript/` | Digests an executor's Claude or Codex session into one line per event for the revising call; its tests run in the templates gate |
| The init command | `/flights:init` | Writes the speccer's manual; its content list is the one under [The speccer's manual](#the-speccers-manual) |
| The command | `/flights:spec` | The human-in-the-loop front end: the maps, the user's answers, the hand-off, the one question, the presentation. It never restates the format |
| The adopter contract | `CLAUDE.md` and `templates/project/CLAUDE.md`, `/pcm` | Wording that names the spec writer and how a spec travels |

## Open items

- The size limits (absorb under about 5 files or 8 steps, cut over 15 files or about 25 steps) and the reading budget (25,000 characters, raised from 16,000 because trimming task files to fit was most of a large run's cost) are first values. Tune them until executors land between about 40 and 80 calls.
- Distilling `/architecture-design` into how `Decisions` is written.
- Experiment: map probes are `tracer` (general-purpose made specific to the speccer), measured on one bench against open-handed `general-purpose` (the `opus` tier, `tracer-pro-max`, 28.0/30 with 2 wrong at $0.80, against 29.5 with 3 wrong at $1.37; the speccer spawns tier 1, `tracer` — [tracer-bench.md](../tracing/tracer-bench.md)); a whole-flight replay against the open-hand opus baseline decides whether it stays.
- A test on this repository that measures whether `flights-speccer` reuses what exists instead of inventing it.
