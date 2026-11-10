# flights:foreman

A foreman turns one request into tested code where its parent points it, by its own hand, for the fewest dollars: three reading rounds, one write-and-prove round (a group at a time on a work list), one probe. It spawns nothing, never lands, commits or asks the user. The price evidence is in the ledger [flights-foreman-bench.md](flights-foreman-bench.md); the one-builder case is [flights.md](flights.md#why-the-foreman-builds-by-its-own-hand).

## Contents

- [Tiers](#tiers)
- [The price model](#the-price-model)
- [Reading](#reading)
- [Building](#building)
- [Work list](#work-list)
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

Heavy work (ledger § Heavy rungs): `flights:foreman` on opus · medium is the heavy-work foreman. On the list rung the shipped text costs $6.70 and $7.71 on opus · medium against $12.64 for the foreman before the price law, with 4 and 4 of 24 minor items open; `flights:foreman-sonnet` on sonnet · high costs $4.01 and $5.38, about two thirds of that (the two-runs-each means are $4.70 against $7.21), and is not reliable on a heavy list: 4 to 14 of 24 minor items open over six runs, four of six returns of 45 to 73 lines with bullets against the 25-line block, one opening with other words before `PARTIAL`. The brief's difficulty still chooses the tier; a heavy list that must come back complete goes to opus.

A spawn tagged `[effort: high]` scored 7 and 9 on those two tasks for 46% more dollars than medium, mean grade 7.5 against 7.1 over four runs each: medium is the default, and the tag is the caller's for work where one missed rule costs more than a dollar.

## The price model

The law states it: a run is billed three ways. Every request re-reads the whole context; every token a read or a command's output brings in is billed on entry and re-read by each later request; every written token, thinking included, costs the most.

Why: in 818 Opus foreman runs over 40 days, requests that only read or search were 68.1% of the dollars and 73% of the requests; dollars track request count (Spearman 0.96); the median run spent 31% of its dollars before its first edit.

The law ends: the build's rounds, every one of them taken, are the whole run for work one write-and-prove round holds; larger work repeats them group by group ([Work list](#work-list)). Price shapes how a round is packed, never how much of the brief is built.

Why: on the list rung, 2 of 3 runs of the previous text ("the rounds are the whole run") returned `PARTIAL` at 65 and 82 requests with the whole 24-item list unbuilt, exactly when their context reached the 200k compaction notice; the leftover is a second foreman's price (ledger § Heavy rungs, table B2).

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

## Work list

Work one write-and-prove round cannot hold, several items or one item across many files, is built group by group:

1. Right after Locate, one Write saves the list to a file outside the tree, named for the request (`worklist-{the request in three words}.md`), a line an item, `open {item} — {files}`; items sharing files, a pattern or a test command form one group, the groups the acceptance names first.
2. A group takes one Read, one Fill where needed, then one write-and-prove round; the same edit at many sites is one script run over all of them.
3. The message opening the next group's Read rewrites the finished group's lines: `done {item} — {test name or file:line}` or `open {item} — {why}`.
4. One probe after the last group.
5. The compaction notice is answered at the next group's end, the list's path in `load_files`.

Why: every Opus run with the list compacted once at the notice (about 205k down to 34k) and went on; open minor items fell from 10 and 24 (previous text) to 1, 3, 4 and 4 (table B2). The file is named for the request because two foremen of one chat first wrote one shared `worklist.md` (a bench run killed at 16 and 11 requests).

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

A worker tier (one Sonnet high worker per work-list group, spawned in one message) was benched on the list rung and lost on price and wall (7.53 and 8.48 dollars against 6.70 and 7.71; 38.0 and 47.8 min against 32.4 and 32.3), winning completeness only (1 and 0 minor items open against 4 and 4); see the bench ledger's B4. A child pays its own start and re-reads what its parent read. Work that splits into areas is split by the main chat: several foremen on separate files of one worktree. A file a guard refuses goes under `OUTSIDE` with its exact new text.

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
- **Nothing named is left open:** an item the brief names, or a file it points to lists, is built, or the return is `PARTIAL` naming it. The run's size, its context and its price are never the why (the definition carries one ✗ and one ✓ example). Neither is a check it cannot run (a screen it cannot see, a device, a live service): the item is built from the code, and what could not be checked goes under `UNTESTED`. Why: with the size rule alone, five of seven runs still left 6 to 13 listed items open as "needs a rendered check"; with the sentence the two Opus runs left 4 and 4 and one Sonnet run 4, the other Sonnet run about 14 (it disobeyed the sentence).
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

`DONE` only when every acceptance row is `met`. One `open` row makes the status `PARTIAL`, whoever owns the item and however sound its why: an item narrowed, deferred, met in part or left to another owner is `open`. `UNTESTED` and the `open:` rows are the foreman's NOT READ.

A work list of more than 10 items returns one `ACCEPTANCE` row a group, one for each `open` line, and the list's path under `FILES`.

Why the status rule: without it 2 of 4 list-rung runs opened with `DONE` while a row was open (`DONE (with … left open)`, `DONE with caveats`); with it, none of 7 runs did. A variant that added "`PARTIAL` with a sound why is a correct return" was reverted: its Opus run stopped at 96 requests with about 13 items left in one open line (ledger table B2).

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

Open after the heavy-rung bench:

- The work list's folder is not pinned: "a file outside the tree" landed in the session scratch folder (2 runs), the shared temp root (2 runs) and the brief's own folder (2 runs).
- Sonnet on a heavy list is not reliable: 4 to 14 of 24 minor items open over six runs; four of six Sonnet returns run 45 to 73 lines with bullets against the 25-line block; one opened with other words before `PARTIAL`.
- One tool call a request is still the norm on heavy runs; the reading rounds become ten and more single-call reads on a many-item brief.
- Wait-only requests cost 10-14% of a run that waits on long commands; no rule: a wait longer than the 5-minute prompt cache makes the next request re-write the whole context.

Unbenched:

- Compaction point: replay prices a compaction at 150k tokens 11.4% lower and one at 80k 10.5% higher; a replay of 22 runs of 60 minutes and more puts a reset past 180k at -2%, past 130k at +4%, past 110k at +18%; no live run tested it.
- A brief that carries the reading list (exact files and symbols): not tested live.
- The same law on the lander, tracer and reviewers.
- A main chat splitting a heavy flight into several foremen of under 60 requests each.
- A trace whose questions are the foreman's own ("where does each change land").
- A blind grade of the heavy builds.
- The heavy rungs' wall time on an idle host.
- A many-edits script: edit requests emit about 47k output tokens a run, the old text and the new; an editor addressed by line range would emit the new text only. At most about 10% of a run.
- A start context without the skill listing for a foreman whose brief names no skill: about 4% of a run (30,101 characters re-read by every request).
- A read hook that appends the code around a search's matches to the search's own result, so the follow-up range read is not needed: at most about 8% of a run; it grows every search's result.
