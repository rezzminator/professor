# flights-foreman

The one agent that builds. It takes a request, with a spec (the rulings and requirement rows `/flights:spec` settled with the user) or without one (a problem), reads its scope, designs what no ruling decided, builds it, splits where ownership splits or where the cut sends a segment to a child, and lands the flight through one `flights-lander`. It replaced the planner/executor split of both families: the task-file speccer, `flights-orchestrator`, the smart and precise executors, and the general family (`general-orchestrator`, `general-foreman`, `general-executor`). Ruled by the maintainer on 2026-10-06.

## Contents

- [Why one agent reads and builds](#why-one-agent-reads-and-builds)
- [Input](#input)
- [Novel work](#novel-work)
- [Code it has not read](#code-it-has-not-read)
- [Rulings bind](#rulings-bind)
- [Before the first edit](#before-the-first-edit)
- [The cut](#the-cut)
- [Inside-out and the frozen interface](#inside-out-and-the-frozen-interface)
- [Children](#children)
- [Mechanical bulk](#mechanical-bulk)
- [Waiting](#waiting)
- [Tests](#tests)
- [A return is a claim; the owner repairs](#a-return-is-a-claim-the-owner-repairs)
- [Landing](#landing)
- [Cap and compaction](#cap-and-compaction)
- [The run log](#the-run-log)
- [Return](#return)
- [Evidence](#evidence)

## Why one agent reads and builds

The task-file speccer read every file a task touched, wrote what it learned into task files, and an executor read the same files again to build: every handoff paid for the reading twice, and every belief the speccer formed from what it read travelled to the executor as a fact. Measured:

- E1, one Go corpus (callmeter walker-followups, 7 tasks, about 650 code lines), blind replay, one judge: the v2 flight read 38.4M tokens for 42 of 44 behaviours met and 3 known traps; one opus builder plus the lander read 35.8M for the same 42 of 44 and 2 traps, 19% cheaper and 10% faster. The flight's extra trap came from its speccer: it pinned "the content is a JSON string" from the one fixture it read, and every executor built to the pin.
- The audit of 30 standalone `general-foreman` runs: every foreman that briefed an executor paid for an executor re-reading 69 to 99.9% of the bytes the foreman had already read.
- The reason flights split in the first place was one agent passing 100 calls on a large batch and its context growing until every call was expensive. The sub-agent-compact plugin (2026-09-25, four days after flights were built) removed that: arm B's builder ran 223 calls with three compactions at about 125k tokens per request.
- Published agent frameworks agree: OpenHands scores the same 76.8% on SWE-bench Verified with and without sub-agents, cheaper without; it ships its own context condenser and leaves delegation off by default. Sub-agents win on long-horizon work (Commit0: 62.5% at $4.40 against 56.2% at $7.69), where a context cannot hold the work.

So the reader builds, and the work splits only where no agent reads what another already read: at an ownership boundary, or at a segment cut before its code is read ([The cut](#the-cut)).

## Input

A request; its scope (a build unit or a problem); acceptance; each testing manual's path; the worktree; the flight directory `$HOME/.local/state/pfm/flights/{project}/{flight}/`, which holds every flight artifact the manual names (`run.md`, `agents.tsv`, `design.md` and each child's `design-{unit}.md`, `briefs/`, `returns/`, `tasks/`, `gate.md`, `REVIEW.md`, `UNLANDED.md`), never the worktree; the door list `doors.txt` and the cut kit's output `cut-kit.txt` (a child's `doors-{unit}.txt`, `cut-kit-{unit}.txt`) beside them; the project map beside the project's flight directories. With a spec, the request is `requirements.md` in the flight directory: the binding rulings and the requirement rows; or a [`flights-speccer`](flights-speccer.md) `spec.md`, whose requirement rows and `## Decisions` bind as rulings. A child also gets the frozen interface or spine it builds against. The same agent serves a main chat's "fix X" with no flight directory: it then creates one under the project, so its run log and the lander's files have a home.

## Novel work

When `run.md` holds no `SPEC` line, a closest-pattern search, by the foreman's own grep or one `tracer` question, finds none to copy (a new subsystem, an integration with a system nothing here talks to, a layout or test suite the quality laws would design from scratch), and the new code exceeds about 300 lines in one unit or turns on a choice research must settle (a dependency, a service, a data store, a protocol), a piece repeated across units measured once, the root foreman, before its base gate, writes `briefs/speccer-r{round}.md` (its brief, all it holds, the flight directory, the project map and the child-brief return line, the return file `returns/speccer-r{round}.md`), spawns one `flights-speccer` with that path as it spawns a child (the `speccer CLAIMED` line, the `agents.tsv` row, the return watch) and ends its message. A `SPEC` return is recorded in `run.md` and its `spec.md` becomes the request; for a phased spec, the phase the brief names, else the first, the return naming the next. A `PARTIAL` or `BLOCKED` return makes the foreman's first line `BLOCKED {flight}` with the speccer's missing part or question, nothing built. `design.md` cites the spec's `D` ids in place of a comparison of its own and adds only the per-door changes, states and tests. A `D` the code refuses goes back to the same speccer by `SendMessage` with the finding; its revised `spec.md` binds, and a second refusal is `BLOCKED`. The spec's `USER` decisions exist only when the user gave the authority to decide forks, and travel up on the return's `SPEC` line with its path and `COST`.

Root only: a child builds against the root's frozen interface and never fires one. Work with a pattern, and small new work, go straight to the build: a fifth door on an existing alerter copies the four, a 30–60-line log writer per plugin is designed in `design.md`, and a spec of either costs more than it buys. The speccer is a separate context because novel, large work's research, dependency map, quality laws and open choices are more than a builder should carry into the build, and a wrong design of something new costs a rebuild.

## Code it has not read

Code the foreman has not read goes first to `tracer`, as numbered questions, at intake or at any later step. It works the answer with `/tracer:use` — a node's hops, the component map, the tree below or above a component, exact lines for a brief — and then reads only the ranges it changes, so the reading is paid at the sub-tracers' price and every line it relies on was re-read from disk. Tracing is not mandatory: in a blind A/B on five unrelated pfm defects, a foreman that traced first at every flight fixed the same defects with the same test strength and regression count as one that read the code itself, at about $1 more build cost per flight. A child's brief carries the run's `RUN` and the node IDs covering its unit, so the child starts from verified lines rather than a second trace. The door search stays the foreman's own chained search: a tracer run is evidence, while a door list must be a closed-world search. The agent carries `Skill` to load `/tracer:use` and any other skill the work names; without it a foreman could read a skill's file but never invoke one.

The questions are asked in the build's terms — where each change lands, the surface it reports to, the test and fixture a new case copies — never as a survey, and a fact the run holds is never reported unchecked. The tracer `RUN` rides every compaction focus, and a compaction waits until the step in hand is done. The first flight under this rule, a fleet-prompt launch warning, spawned tracer as its first call after the brief. Its tracer run cost $0.56 of a $3.41 round, and the run held 5% of the 4,871 code lines the foreman went on to read: the brief had surveyed every engine and the doctor, and 5 of its 13 threads traced paths the build ruled out. The foreman then spent about 17 calls reading test fixtures and output surfaces no thread had asked for. A compaction dropped the `RUN`, so the return listed a door as unchecked although the run held it verified. It compacted between reading a door and editing it, and re-read the same ranges in five calls.

## Rulings bind

A ruling is the user's; the foreman never re-decides one. A contradiction between two rulings, or between a ruling and the code, returns `BLOCKED` with both quoted. Measured: E9's builder stopped on exactly such a contradiction after 30 files instead of guessing, and the guess is what the rule forbids.

## Before the first edit

In order, each written to the run log, with [the cut](#the-cut) between the door list and the data shapes:

1. The base gate: the scope's check script on the untouched base, and the reds that already exist. Measured: 11 of 12 audited `general-orchestrator` runs met a surprise at the acceptance gate, most of them reds, ratchets or format failures already on the base.
2. A `GOAL` line and a door list: every producer, consumer and caller of what changes, from a search (for a shared contract, the contract project's own finder), never from memory or a map handed to it (a `/flights:spec` probe map is evidence); every door on the list is opened; behaviour that shows only live is reproduced live before anything changes. Measured: five of the 19 audited foreman runs that shipped a defect missed a code path or caller they never opened.
3. The data shapes: every shape the work relies on is read from its producer (its docs, code or schema, never only its consumer), plus one probe of real data where real data exists. A value one run printed is evidence for the return, never a requirement; so is the form one sample of outside data took; every form the producer documents or emits is handled, and any other form is reported, never read as absent. Measured: three of the 19 shipped a defect because real data took a form nobody probed (a listing site records 0 bedrooms for every new-build; a filter built on it hid real flats).
4. A `DECIDE` line when no ruling decided the design: the design and the alternative it rejected, written out in the design file: the root's `design.md`, a child's `design-{unit}.md`, so children sharing the flight directory never overwrite each other's or the root's. Measured: 2 of 30 audited foremen wrote a decision before building; nothing forced it.

## The cut

A build unit (a project in the project map) or a problem unrelated to the others splits at once: a batch of unrelated problems (what `general-orchestrator` did) is one child per problem. Inside one unit the foreman cuts as the step right after its door list, before it reads any segment's code; the data shapes and the design then cover only the spine and what it keeps. The step number carries the order: a sonnet foreman on a replayed flight, its cut numbered after the design with a note to run it earlier, ran the steps in numbered order, read and designed every segment, then rejected the kit's split. The cut: `tracetree.py segment {worktree} --rev {baseline} --seeds {doors file}` ([tracetree](../tracing/tracetree.md)) lifts each door named from two or more cells into a `SPINE` of contracts and returns at most four segments, each with an `ORDER` and an `ESCALATE` line, and a `VERDICT SPLIT k`, `SOLO` or `EMPTY`. Its output goes through `tee` into `cut-kit.txt` (a child's `cut-kit-{unit}.txt`), a rerun overwriting it: `/flights:audit` reads the `DECIDE` lines a `CUT` line summarises, and the `cutgate` hook still finds the verdict in the transcript, which a plain redirect would hide. The kit finds where to cut and which segment goes to a child; how a segment builds is its child's design. The kit's run is a mechanism, not an instruction: the `cutgate` hook ([tracetree](../tracing/tracetree.md#cutgate)) refuses the foreman's first code edit, through an edit tool or through Bash (`sed -i`, a redirect, a heredoc into a file, and a tree left dirty for what parsing misses), until its own transcript holds a `segment` run with a verdict other than `EMPTY`, since none of three sonnet foremen under the cut law ran the kit, each logging its cut from judgement; flight-directory writes stay open, and a segment or `DEPTH 3` child, whose parent already cut, is never refused.

The kit decides the spawn: one `DECIDE S{n} CHILD|KEEP · {reason}` line per segment, after its `ORDER` and `ESCALATE` lines, and a `VERDICT` ending `· CHILD n`. A segment is `CHILD` when it builds a `new=` seed (a symbol, field, type or file the change creates: a mechanism) or its seeds carry three or more of the request's rows; otherwise `KEEP`, its reason call-site swaps only, one function, or its row count with no new symbol. A spawn costs about 20 calls of intake, which a fix inside one function or a run of call-site swaps never repays.

The foreman spawns every `CHILD` segment and keeps every `KEEP` one. Keeping a `CHILD` takes an `OVERRIDE S{n} · {fact}` line in `run.md` and the return's `CUT` line, the fact checkable: a `path:line`, a row, a spine contract. Needing to read the segment's code is never such a fact; its child reads it. The decision lives in the kit because every prose rewording of the gates moved the veto without removing it: a sonnet foreman holding the kit's split, code unread, kept every segment under a size gate (each judged under "about 40 calls"; one had alone taken about 86 in the original flight), a coupling gate (a new snapshot field two segments needed read as coupling, not spine), a briefability gate (a presence-probe mechanism "needs its bodies read to be orderable") and an ownership gate read backwards ("shares a file with another row's failure path; one owner", applied to the one segment that built a mechanism). A rule disobeyed two rounds running became a mechanism. The control, call-site swaps across three segments, was kept under every prose version and is `CHILD 0` under the kit.

The kit's segments partition the blast's files, so no file sits in two of them; each project-map hot file keeps one owner. A `new=` seed with no file of its own sits beside its row's other seeds: the one segment they land in owns it; two or more make it a spine contract, `NEW {name} · rows {r…} · freeze as a compiling stub`, and the segments it joins stay `CHILD` or `KEEP` on their own merits. Every spine contract is frozen first as code, an existing signature or a new one as a compiling stub with its test, never a prose contract. A segment's order names the rows it meets with their checks, the files it owns and the spine contracts it consumes or produces, the only signatures the parent fixes; the how inside its files is the child's.

The parent keeps the spine, the `KEEP` segments and the tail that compiles only after every segment (deletions, registries, the unit gate). `VERDICT SOLO`, or one segment left, builds alone.

A segment child returns `BLOCKED {unit}: ESCALATE {question}`, nothing outside its segment changed, when a spine contract must change, a door sits outside its segment, a shape contradicts its order, or it needs a type, helper or fixture another segment would also need; everything else it decides. The parent amends and re-freezes the spine, then answers every affected child by `SendMessage`, which resumes it with its context intact. The escalation rides the `BLOCKED` first line because the GPT foremen's Stop hook (`gpt-agent-stop.py`) admits only `DONE`, `PARTIAL`, `FAILED` and `BLOCKED` returns.

The cut keeps E1's and E8's lesson, that the parent never reads before delegating. E8 is the caution against splitting by planning convenience: one writer for a whole spec read 47% less than the nested planner but mapped worse (100/5/9 against 110/1/3), contradicted a ruling and added two medium defects. A segment child is the only reader of its segment's code, so the split costs no second reading. The audit of ten heavy foreman runs is the new evidence:

- 8 of 8 Claude runs could have split; none weighed it in visible text, and 6 of 8 passed the 250-call cap. Of each run's re-sent context, 10 to 36% belonged to other segments (scout estimates).
- GPT foremen on the same prose split only at units and froze prose contracts, so children built divergent copies and collided: the spine is frozen as compiling code for that reason.
- A backtest of the cut at the flights' base commits: door-list seeds agreed with the scouts' counterfactual split at ARI 0.53 against 0.04 for request-text seeds over the same four flights, and the package rule (one segment per top-level directory) scored 0.09 over eight.

## Inside-out and the frozen interface

The innermost unit is the one the others build against: the shared contract and what generates from it. The root foreman writes the interface there first and freezes it: its files written, that unit's gate green. Then it builds the unit it read to design the interface, the producer, itself. It never hands code it read to another agent to read again.

Inside one unit the spine is frozen the same way, as code: an existing signature, or a new one as a compiling stub with its test.

Freezing first is what makes the work parallel: once the interface exists in the repository, every other unit builds against it at once, with no need for the producer to be finished.

## Children

Once the interface or spine is frozen (for unrelated problems, which share none, at once), one child foreman per other unit, problem or `CHILD` segment, all spawned in one message. Each child gets:

- its requirement rows verbatim, only the rows its unit owns, and with a `spec.md` its path and the `D` ids those rows rest on;
- a segment child: its `SEGMENT`, `ORDER`, `ESCALATE` and `DECIDE` lines verbatim and the parent's order beneath them;
- the frozen interface's or spine's paths, and the files another unit or segment owns: each hot file in the project map has one owning unit, which adds every line to it, so two children never edit it at once;
- acceptance, its testing manual's path, the worktree, the flight directory.

Never the parent's beliefs about code the parent did not read, never steps: a belief passed down is the speccer's pinned sample again. Foremen go three deep: a child cuts its own door list once by the same law and may spawn segment children, briefed with `DEPTH 3`, which build alone; only the root fires a speccer or a lander. A segment child's unit is its parent's unit plus `-S{n}`.

## Mechanical bulk

A foreman may hand `flights-mechanical-executor` a bulk edit whose every line it already fixed (a rename across many files), as a task file in `{flight directory}/tasks/` in the shape that executor reads: `Goal`, `Files` (the test file among them), `Progress dependency`, `Steps` with every edit, `Decisions` (each local choice, `Temporary reds` among them), `Shapes` (reuse targets and the façade as `EXISTING`), `Done when` rows with examples and `Given` lines; its brief beside it in `briefs/{id}-r{round}.md`. Nothing else is handed down: an edit that still needs a decision is built by the foreman that made it.

## Waiting

The most common failure in the 42 audited general runs was waiting: six ended their turn while a gate or a child still ran, and the returns reached the main chat instead of the agent waiting for them.

- While children run, the foreman ends its message to wait; each return wakes it; no watcher is placed on a sub-agent. A hook denies a no-op wait command (`sleep`, `true`, a bare `echo`) and a file-existence wait loop on a `returns/` path. Every child still writes its return to `returns/{unit}-r{round}.md` before returning, the audit trail.
- A command it must wait for is never backgrounded and then left. A gate runs in the foreground with an explicit timeout; one longer than a call allows runs in the background together with a watch command that waits for its output file and prints it (`timeout {s} bash -c 'until [ -s "$1" ]; do sleep 20; done; cat "$1"' _ {file}`), because that command's completion wakes the agent that started it. The lander's return wakes the foreman like a child's: it ends its message after the spawn. A return without `gate.md` or each project's last full-run verdict line gets one question back to the same lander, answered in `returns/gate-r{round}-q.md`; a second such return is `FAIL`.
- Its final message is its return, sent only after every child returned and the lander finished.

## Tests

Carried over from the v2 executors unchanged in substance:

- Each decision is tested once, in the test of the unit whose code makes it; that unit's rows are cases of one table-driven test, or assertions of one test, in the unit's existing test file (a new file only where the source has no test home).
- Tests before code; one red run of every new or extended test against the unfixed tree, failing on an assertion, its log kept; then the code; then the same command green.
- No catch-nothing assertions; a test proves behaviour that exists, never that something is gone.
- Affected tests only while building; the testing manual's static-check command once, last, over the files changed; the full suite is the lander's.

Measured: the v2 test rules took the test-to-code line ratio from 3.55 (v1) to 2.0 (v2 flight) and 1.33 (arm B) with no behaviour lost.

## A return is a claim; the owner repairs

A child's return is verified against `git diff` of the child's unit and the covering tests before it is recorded. A red goes back to the child that built it, with the cause, by `SendMessage`; the agent that read the code fixes it. A second red of one child is `BLOCKED` and travels up. The parent edits a child's unit only when the cause is the interface the parent froze.

## Landing

After every child returned, the root foreman spawns one `flights-lander` for the whole flight, over every project's diff at once, exactly as the lander defines it: every project's gate, one review of the whole diff so both sides of every contract change are read together, adversarial tests, its own fixes. A commit or merge follows only the caller's order and a `PASS` or `FIXED` gate. Measured: in E1 the lander found four defects in the builder's diff and four in the flight's; the builders wrote equally imperfect code and the lander made them equal. Every audited foreman run without an independent review that shipped a defect shipped it to the user.

## Cap and compaction

250 calls, one model request each, a first value sized to the work (arm B's builder needed 223 for seven tasks of one unit). The `pfm internal call-cap` PreToolUse hook on the foreman, the lander and the mechanical executor keeps the count: it counts the agent's model requests from its own transcript, compactions included, against the cap its agent type sets (`transcript.CallCap`: a foreman 250, a lander 200, any other 150), injects the count from four fifths of the cap on, and at the cap orders the stop and the role's cap return, because a count the model holds in its head is lost at every compaction. `autoCompact` 300k forced, nudges from 120k every 60k, the executors' iteration-0 points. A cap reached returns `FAILED {flight}: cap` with the handoff, never `DONE`: one audited foreman ran 64 calls against a 45 cap and still returned `DONE`.

## The run log

`run.md` in the flight directory, written by the foreman that owns the flight (the root): a header with the baseline sha and date; then one line per event — `BASE {unit} {verdict lines}`, `GOAL`, `SPEC {spec.md path}`, `DECIDE`, `CUT {verdict} · {k} · {spine} · CHILD {n}`, `OVERRIDE S{n} · {fact}`, `FROZEN {interface paths} {gate line}`, `{unit} CLAIMED · {agent id}`, `ESCALATE {unit} · {question} · {answer}`, `{unit} DONE|FAILED|BLOCKED · {check line}`, `gate CLAIMED`, `gate PASS|FIXED|FAIL`, `RETRO {lesson}`. Every foreman appends each spawn it makes, and only spawns, to `agents.tsv`, so `pfm tokens --flight` matches a grandchild too. A completion is a `run.md` verdict line: the root, verifying a return, logs one per `UNITS` row, and a child reports its own children in those rows, so segment and nested children land there too; a child writes no other file of the parent's, its `CUT` and `ESCALATE` lines going in its return. The lander reads the baseline from the header and the intent from `spec.md` when the flight has one (it carries every `requirements.md` row verbatim plus its `## Decisions`), else `requirements.md`, else the `GOAL` line; `/flights:audit` reads all of it.

## Return

First line `DONE {flight}`, `PARTIAL {flight}`, `FAILED {flight}: {why}` or `BLOCKED {flight}: {question}`, the root's `DONE` only on the lander's `PASS` or `FIXED` and no `UNBUILT` item (otherwise `PARTIAL` names every unbuilt requested element; a `FAIL` returns `FAILED {flight}: gate`, or `PARTIAL` naming the units it sits in); then the `SPEC` line when a speccer ran (its path, its `USER` ids, the next phase, its cost), the `CUT` line with its `CHILD` count, each `OVERRIDE` and escalation, one row per unit (owner, verdict, its check line as printed), the doors, `UNPROVEN`, the lander's verdict and residuals, `OPEN` carrying every item from the spec's `## Open` with `UNBUILT` first, `OUTSIDE`, the cost (calls, tokens read), `RETRO`. A child returns the same shape for its unit.

## Evidence

- E1, E7, E8, E9, the v1 baseline, the 30 foreman and 12 orchestrator runs, the ten-run foreman replication audit with its cut backtest, and the frameworks ranked by their own benchmarks: the maintainer's flight-audit notes, outside this repository; every number this document relies on is quoted above.
- Open: the multi-project form has not been measured; the first cross-project flights are its measurement (calls per unit, re-read share, lander findings).
