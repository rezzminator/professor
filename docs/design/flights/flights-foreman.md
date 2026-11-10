# flights:foreman

A foreman turns one request into tested code where its parent points it, by its own hand, for the fewest dollars: three reading rounds, one write-and-prove round, one probe. It spawns nothing, never lands, commits or asks the user. The price evidence is in the ledger [flights-foreman-bench.md](flights-foreman-bench.md); the one-builder case is [flights.md](flights.md#why-the-foreman-builds-by-its-own-hand).

## Contents

- [Tiers](#tiers)
- [The price model](#the-price-model)
- [Reading](#reading)
- [Building](#building)
- [Probe](#probe)
- [Children: none](#children-none)
- [Effort](#effort)
- [Tools](#tools)
- [Input](#input)
- [Waiting, gates, decisions, law](#waiting-gates-decisions-law)
- [The return](#the-return)
- [Never](#never)
- [Declaration](#declaration)
- [Open items](#open-items)

## Tiers

| Agent | Model · effort | For |
| --- | --- | --- |
| `flights:foreman` | opus · medium | design choices or ambiguity, unfamiliar code, an unknown cause, work across several modules |
| `flights:foreman-sonnet` | sonnet · high | a clear path: the pattern is in the code |

Difficulty and ambiguity choose the tier, never size. Both files carry the same body, byte for byte (pinned by `test_sonnet_foreman_carries_the_foreman_manual`).

### Why these two setups

Ledger § Results: on five tasks in five repositories the foreman before this change (opus · high, the earlier manual) cost $8.60 at a blind-grader mean of 9.0. The law as it ships costs $4.93 on opus · medium (43% less) and $2.68 on sonnet · high (69% less), every hidden test passing (ledger § Confirm run). Sonnet · medium costs $1.81 and fails correctness on 3 of 5 tasks, so it is not shipped.

What the cut costs: on the two hard tasks the earlier foreman was graded 9 and 9 (merge as is); the law on opus · medium 6 to 8 and 7 to 9 over two graded runs each, on sonnet · high 5 to 7 and 8 (merge after small fixes, one fail), so the lander's review carries more on hard work.

A spawn tagged `[effort: high]` scored 7 and 9 on those two tasks for 46% more dollars than medium, mean grade 7.5 against 7.1 over four runs each: medium is the default, and the tag is the caller's for work where one missed rule costs more than a dollar.

## The price model

The law states it: a run is billed three ways. Every request re-reads the whole context; every token a read or a command's output brings in is billed on entry and re-read by each later request; every written token, thinking included, costs the most.

Why: in 818 Opus foreman runs over 40 days, requests that only read or search were 68.1% of the dollars and 73% of the requests; dollars track request count (Spearman 0.96); the median run spent 31% of its dollars before its first edit.

## Reading

Three rounds before the first edit:

- **Locate:** one Bash call: a grep for every name the brief gives, line counts, test names, the testing manual's run commands.
- **Read:** the exact ranges Locate found.
- **Fill:** only what Read left open. A round past the third opens with one line naming its question.

A read brings in the lines its decision needs (ranges, context greps), a file over 300 lines is read by range, a command's output is cut where it is produced.

Why: replaying last week's 134 Opus runs with read streaks batched four to a request prices them 23.1% lower (unlimited: 29.0%); capping each read result at 3,000 tokens 8.8% lower. Live, the law cut requests on the three replays from 37–42 to 12–25.

How the batching happens: inside one Bash call (20 to 28 commands chained), not as parallel tool calls: 357 of 368 tool-calling messages under the law carry one tool call.

## Building

- **Tests are one table:** a row per case the acceptance names and per boundary of a guard, each asserting the exact value.
- **Write and prove in one round:** the test edits, the new tests run on the unchanged code (the red proof), the change, then one foreground call chaining the affected tests and the static checks, each step ending `|| echo "FAILED: {step}"`.
- **A red in the change:** read to its cause, every cause fixed in one round, one rerun.
- **A red in a file the foreman did not touch:** its first error line under `OUTSIDE`, uninvestigated.
- **The full suite and the gate** stay the lander's.
- **Every named item is built,** or the return is `PARTIAL` naming it.

Why the table: blind graders found the first law's cheaper builds correct but thinner in tests; with the table rule the opus · medium builds passed all three clearer tasks (8, 8, 9).

## Probe

Once the tests are green, one Bash call runs one throwaway script, kept outside the tree, that drives the change with inputs the tests leave out:

- the input built to break each absolute in the acceptance;
- the other kind of input or code path;
- the empty and malformed input;
- the failure path;
- the commonest real form from the repository's own fixtures.

It prints input then result, one line each. A wrong result is fixed and becomes a test row.

Why: the earlier foreman's quality lead came from about five ad-hoc probes of its own build, about $0.20 a run, not from its extra reading (its context before the first edit was the same size as the law's: 128k against 120k tokens). With the probe step, opus · medium scored 8 and 6 on the check task and 9 and 7 on the hook task over two runs each, against 6 and 7 without it, for about 7% more dollars: a gain inside the run-to-run spread, kept because each confirm build's probes found and fixed wrong results its tests had passed (a 15-second parse cut to 63 ms, three missed coordinate forms). As a rule alone the step leaked: none of five sonnet · medium runs probed. So the printed lines are a field of the return (`PROBES`).

## Children: none

The definitions list no `Agent` tool. Three forms of delegation were benched against the foreman building alone, and each cost more:

- an Opus foreman designing for Sonnet children ($7.49 against $6.63 for the same law alone, lower grades);
- two Sonnet foremen splitting one task in one worktree (+44% dollars, 5.3 min against 4.6);
- a tracer reading first (+85% and +39%, 76 and 60 requests against 23 and 30).

A child pays its own start and re-reads what its parent read. Work that splits into areas is split by the main chat: several foremen on separate files of one worktree. A file a guard refuses goes under `OUTSIDE` with its exact new text.

## Effort

No effort buttons and no effort prose. `effortControl` (floor medium, ceiling xhigh) stays: the effort plugin raises a foreman one step at a failing test run and returns it home at the next green. The red proof is a failing test run, so the write that follows runs one step above the reading.

Measured in the plugin's decision log over 57 bench runs: 56 first-failure and 34 second-failure raises; 14% to 26% of a run's requests ran above its home level; the foreman at home high ran 19.4% of its requests at xhigh.

Why no buttons: 0 presses in 85 bench runs, 37 presses in 922 runs over 40 days.

## Tools

`Read, Write, Edit, Bash, Skill, SendMessage` and the two forward-compaction tools. Each has its reason:

- `Edit` and `Write` refuse a file the agent has not `Read`;
- `Skill` loads a testing manual that is a skill (650 of the foremen's 736 Skill calls in 40 days);
- `SendMessage` carries a critical decision to main, and listed it needs no `ToolSearch` first (the lander, with a list, made 41 SendMessage calls and no ToolSearch call; the unlisted foremen made 178 ToolSearch calls beside 289 SendMessage calls);
- the compaction tools hold a long run.

Absent on purpose: `Agent` ([Children: none](#children-none)), `ToolSearch` and its deferred-tool list, `Grep` and `Glob` (0 calls in 922 runs: foremen search through Bash), the effort buttons ([Effort](#effort)), every other MCP tool.

Start load: a foreman's first request was 29.9k to 36.2k tokens across the five repositories with every tool, and is 22.9k to 28.9k with the list, 20% to 23% less.

## Input

The brief:

- the work and its acceptance (absent: one acceptance row per thing the work asks for);
- where to work (absent: the current directory);
- the testing manual, a path or a skill (absent: the test command the project's own build files name).

The project contract is already in context.

## Waiting, gates, decisions, law

- **Waiting:** a waited command runs in the foreground with a timeout, nothing is started in the background and polled; one longer than 10 minutes gets one blocking wait.
- **Gates:** a failing gate is fixed in the code; a limit rises only through the project's documented exception route.
- **Decisions:** the foreman decides everything the work leaves open, never asks the user, sends a critical decision to `SendMessage(to: "main")` and keeps working.
- **Law:** it stays inside the request; a deletion leaves nothing behind.

## The return

One message, the fixed block alone, 25 lines at most, its first word `DONE` or `PARTIAL`, every line opening with its label, and whatever return the brief asks for placed under the label it fits.

Why: bench briefs carry their own loose return section, and with the shape stated alone 3 of 5 sonnet returns came back as free prose with one to four labels; with this sentence 3 of 3 reruns opened with the status word and carried every label, the sonnet ones at 31 and 39 lines.

The labels, in order:

- `DONE` or `PARTIAL`;
- `ACCEPTANCE`: each item `met: {test name or file:line}` or `open: {why}`;
- `DECISIONS`: each with its reason and how to flip it;
- `CRITICAL`;
- `FILES`;
- `TESTS`: command and printed result line, or `COULD NOT RUN: {first error line}`;
- `PROBES`: input and printed result, `none: {the change runs no code}`, or `COULD NOT RUN`;
- `UNTESTED`: behaviour neither a test nor a probe ran, with why;
- `OUTSIDE`.

`DONE` only when every acceptance row is `met`. `UNTESTED` and the `open:` rows are the foreman's NOT READ.

## Never

- no child, tracer or other agent;
- no effort button;
- no full suite or gate;
- no commit, merge or landing;
- no question to the user;
- no `DONE` over an item left unbuilt;
- no change beyond the request.

## Declaration

- **`plugins/flights/plugins/flights/agents/foreman.md`:** opus, effort medium, `effortControl` floor medium and ceiling xhigh, the `tools:` line of [Tools](#tools).
- **`plugins/flights/plugins/flights/agents/foreman-sonnet.md`:** a full file, sonnet at effort high, the same band, tools and body.

## Open items

Unbenched:

- Compaction point: replay prices a compaction at 150k tokens 11.4% lower and one at 80k 10.5% higher; no live run tested it.
- A brief that carries the reading list (exact files and symbols): not tested live.
- Runs past 100 requests: no bench task is that large; the five tasks run 8 to 43 requests.
- The same law on the lander, tracer and reviewers.
- Splitting large work across several foremen: benched only on one small task.
