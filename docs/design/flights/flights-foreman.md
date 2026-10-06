# flights-foreman

The one agent that builds. It takes a request, with a spec (the rulings and requirement rows `/flights:spec` settled with the user) or without one (a problem), reads its scope, designs what no ruling decided, builds it, splits only where ownership splits, and lands the flight through one `flights-lander`. It replaced the planner/executor split of both families: `flights-speccer`, `flights-orchestrator`, the smart and precise executors, and the general family (`general-orchestrator`, `general-foreman`, `general-executor`). Ruled by the maintainer on 2026-10-06.

## Contents

- [Why one agent reads and builds](#why-one-agent-reads-and-builds)
- [Input](#input)
- [Rulings bind](#rulings-bind)
- [Before the first edit](#before-the-first-edit)
- [Split only at ownership](#split-only-at-ownership)
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

The speccer read every file a task touched, wrote what it learned into task files, and an executor read the same files again to build: every handoff paid for the reading twice, and every belief the speccer formed from what it read travelled to the executor as a fact. Measured:

- E1, one Go corpus (callmeter walker-followups, 7 tasks, about 650 code lines), blind replay, one judge: the v2 flight read 38.4M tokens for 42 of 44 behaviours met and 3 known traps; one opus builder plus the lander read 35.8M for the same 42 of 44 and 2 traps, 19% cheaper and 10% faster. The flight's extra trap came from its speccer: it pinned "the content is a JSON string" from the one fixture it read, and every executor built to the pin.
- The audit of 30 standalone `general-foreman` runs: every foreman that briefed an executor paid for an executor re-reading 69 to 99.9% of the bytes the foreman had already read.
- The reason flights split in the first place was one agent passing 100 calls on a large batch and its context growing until every call was expensive. The sub-agent-compact plugin (2026-09-25, four days after flights were built) removed that: arm B's builder ran 223 calls with three compactions at about 125k tokens per request.
- Published agent frameworks agree: OpenHands scores the same 76.8% on SWE-bench Verified with and without sub-agents, cheaper without; it ships its own context condenser and leaves delegation off by default. Sub-agents win on long-horizon work (Commit0: 62.5% at $4.40 against 56.2% at $7.69), where a context cannot hold the work.

So the reader builds, and the work splits only where one context cannot or should not hold it: at an ownership boundary.

## Input

A request; its scope (a build unit or a problem); acceptance; each testing manual's path; the worktree; the flight directory `$HOME/.local/state/pfm/flights/{project}/{flight}/`; the project map beside the project's flight directories. With a spec, the request is `requirements.md` in the flight directory: the binding rulings and the requirement rows. A child also gets the frozen interface it builds against. The same agent serves a main chat's "fix X" with no flight directory: it then creates one under the project, so its run log and the lander's files have a home.

## Rulings bind

A ruling is the user's; the foreman never re-decides one. A contradiction between two rulings, or between a ruling and the code, returns `BLOCKED` with both quoted. Measured: E9's builder stopped on exactly such a contradiction after 30 files instead of guessing, and the guess is what the rule forbids.

## Before the first edit

In order, each written to the run log:

1. The base gate: the scope's check script on the untouched base, and the reds that already exist. Measured: 11 of 12 audited `general-orchestrator` runs met a surprise at the acceptance gate, most of them reds, ratchets or format failures already on the base.
2. A `GOAL` line and a door list: every producer, consumer and caller of what changes, from a search (for a shared contract, the contract project's own finder), never from memory; every door on the list is opened. Measured: five of the 19 audited foreman runs that shipped a defect missed a code path or caller they never opened.
3. The data shapes: every shape the work relies on is read from its producer (its docs, code or schema, never only its consumer), plus one probe of real data where real data exists. A value one run printed is evidence for the return, never a requirement; so is the form one sample of outside data took; every form the producer documents or emits is handled, and any other form is reported, never read as absent. Measured: three of the 19 shipped a defect because real data took a form nobody probed (a listing site records 0 bedrooms for every new-build; a filter built on it hid real flats).
4. A `DECIDE` line when no ruling decided the design: the design and the alternative it rejected. Measured: 2 of 30 audited foremen wrote a decision before building; nothing forced it.

## Split only at ownership

A build unit (a project in the project map) or a problem unrelated to the others is the only seam. Inside one unit, whatever its size, one foreman holds it: one context per unit, compaction for length. Related small fixes in one unit stay with one foreman; a batch of unrelated problems (what `general-orchestrator` did) is one child per problem.

E8 is the caution against splitting by planning convenience: one writer for a whole spec read 47% less than the nested planner but mapped worse (100/5/9 against 110/1/3), contradicted a ruling and added two medium defects. Splitting by ownership keeps that benefit, because each child reads only what it owns.

## Inside-out and the frozen interface

The innermost unit is the one the others build against: the shared contract and what generates from it. The root foreman writes the interface there first and freezes it: its files written, that unit's gate green. Then it builds the unit it read to design the interface, the producer, itself. It never hands code it read to another agent to read again.

Freezing first is what makes the work parallel: once the interface exists in the repository, every other unit builds against it at once, with no need for the producer to be finished.

## Children

Once the interface is frozen, one child foreman per other unit, all spawned in one message. Each child gets:

- its requirement rows verbatim, only the rows its unit owns;
- the frozen interface's paths;
- acceptance, its testing manual's path, the worktree, the flight directory.

Never the parent's beliefs about code the parent did not read, never steps: a belief passed down is the speccer's pinned sample again. Depth is two: a child never spawns a foreman, and never a lander.

## Mechanical bulk

A foreman may hand `flights-mechanical-executor` a bulk edit whose every line it already fixed (a rename across many files), as a task file in `{flight directory}/tasks/` in the shape that executor reads. Nothing else is handed down: an edit that still needs a decision is built by the foreman that made it.

## Waiting

The most common failure in the 42 audited general runs was waiting: six ended their turn while a gate or a child still ran, and the returns reached the main chat instead of the agent waiting for them.

- While children run, the foreman ends its message to wait; each return wakes it.
- A command it must wait for is never backgrounded and then left. A gate runs in the foreground with an explicit timeout; one longer than a call allows runs in the background together with a watch command that waits for its output file and prints it (`timeout {s} bash -c 'until [ -s "$1" ]; do sleep 20; done; cat "$1"' _ {file}`), because that command's completion always wakes the agent that started it, while a sub-agent's return can go elsewhere. The lander's return is waited for the same way: its return file plus the watch.
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

After every child returned, the root foreman spawns one `flights-lander` for the whole flight, over every project's diff at once, exactly as the lander defines it: every project's gate, one review of the whole diff so both sides of every contract change are read together, adversarial tests, its own fixes. Measured: in E1 the lander found four defects in the builder's diff and four in the flight's; the builders wrote equally imperfect code and the lander made them equal. Every audited foreman run without an independent review that shipped a defect shipped it to the user.

## Cap and compaction

250 calls, a first value sized to the work (arm B's builder needed 223 for seven tasks of one unit). `autoCompact` 300k forced, nudges from 120k every 60k, the executors' iteration-0 points. A cap reached returns `FAILED {id}: cap` with the handoff, never `DONE`: one audited foreman ran 64 calls against a 45 cap and still returned `DONE`.

## The run log

`run.md` in the flight directory, written by the foreman that owns the flight (the root): a header with the baseline sha and date; then one line per event — `BASE {unit} {verdict lines}`, `GOAL`, `DECIDE`, `FROZEN {interface paths} {gate line}`, `{unit} CLAIMED · {agent id}`, `{unit} DONE|FAILED|BLOCKED · {check line}`, `gate CLAIMED`, `gate PASS|FIXED|FAIL`. A child writes no file of the parent's; it returns. The lander reads the baseline from the header and the intent from `requirements.md` (or the `GOAL` line when there is no spec); `/flights:audit` reads all of it.

## Return

First line `DONE {flight}`, `PARTIAL {flight}`, `FAILED {flight}: {why}` or `BLOCKED {flight}: {question}`; then one row per unit (owner, verdict, its check line as printed), the doors, `UNPROVEN`, the lander's verdict and residuals, `OUTSIDE`, the cost (calls, tokens read), `RETRO`. A child returns the same shape for its unit.

## Evidence

- E1, E7, E8, E9, the v1 baseline, the 30 foreman and 12 orchestrator runs, and the frameworks ranked by their own benchmarks: the maintainer's flight-audit notes, outside this repository; every number this document relies on is quoted above.
- Open: the multi-project form has not been measured; the first cross-project flights are its measurement (calls per unit, re-read share, lander findings).
