# flights:foreman bench ledger

The bench behind [flights-foreman.md](flights-foreman.md): inputs, judges, prices, versions and every result.

## Contents

- [Bench input](#bench-input)
- [Judges](#judges)
- [Price table](#price-table)
- [Versions](#versions)
- [Results: dollars [requests] and blind grade](#results-dollars-requests-and-blind-grade)
- [Confirm run](#confirm-run)
- [What the grades turned on](#what-the-grades-turned-on)
- [Store analysis behind the law](#store-analysis-behind-the-law)
- [Heavy rungs](#heavy-rungs)
- [Forced-trace probe](#forced-trace-probe)

## Bench input

Five tasks in five repositories, three languages; each run gets the task's brief verbatim in a clean worktree at the task's base commit.

| Task | Repository | Kind |
| --- | --- | --- |
| hook | the flights plugin (Python) | replay of a landed run: the flight-worktree codebase map; one of its five items is blocked for every build |
| front-end | a private TypeScript web app (jest) | replay of a landed run: two review findings |
| check | a private Python scraper | replay of a landed run: a design-heavy location check |
| stats | jd/tenacity, pull request 653 (Python) | hold-out: no agent files, upstream's tests hidden |
| help | urfave/cli, pull request 2476 (Go) | hold-out: upstream's tests hidden |

The three replays' original runs cost $5.00 (67 requests), $2.95 (56, one more item) and $3.09 (49).

## Judges

- Blind graders: a fresh Opus agent at high effort per task, the builds under neutral letters, one rubric, probe scripts written before reading any build and reused in later rounds (39 then 52 probes on the check task, 14 then 20 on the hook task, 11 on the front-end task); score 1–10 and a verdict a lander would give: pass, fix or fail. 10 graders dispatched, 10 received.
- Objective: each build's own tests; on the hold-outs upstream's hidden tests and full suite; a six-probe concurrency script on the stats task.

## Price table

Dollars per million tokens: Opus input 4, output 20, cache read 0.2, 5-minute cache write 5; Sonnet half of each. A run's dollars are every request of the foreman and of every agent it spawned, from their transcripts.

## Versions

| Version | The one change |
| --- | --- |
| control | the foreman before this change: opus · high, the earlier manual |
| law 1 | rounds: one request carries all it can; ranged reads; write in one round |
| architect | law 1 with an Opus parent designing for Sonnet children |
| law 2 | law 1 + the three-term price model, output cut at its source, the test table, foreground proving, an outside red left alone, no child, a 25-line return |
| split | law 2 as two Sonnet foremen on disjoint files of one worktree |
| law 3 | law 2 + the probe step |
| scout | law 3 with one tracer run in place of the Locate round |
| shipped | law 3 as the agent's own body, with the tools list and `PROBES`/`UNTESTED` in the return |
| price law | `shipped`, unchanged, as the heavy rungs first met it: "§ Build's rounds are the whole run" |
| work list | price law + § Work list and the rule that the run's size, context and price are never the why for `PARTIAL` (heavy rungs) |
| reverted status sentence | work list + the status rule + "`PARTIAL` with a sound why is a correct return" (heavy rungs) |
| shipped text | work list + the status rule and the sentence that a check that cannot be run is `UNTESTED`, never an open item (heavy rungs) |

## Results: dollars [requests] and blind grade

| Version · setup | hook | front-end | check | stats | help | total | mean grade |
| --- | --- | --- | --- | --- | --- | --- | --- |
| control · opus high | 2.63 [42] · 9 pass | 1.75 [37] · 9 pass | 2.93 [41] · 9 pass | 0.60 [13] · 8 pass | 0.69 [18] · 10 pass | 8.60 | 9.0 |
| law 1 · opus high | 2.16 [43] · 7 fix | 1.12 [17] · 8 pass | 1.99 [19] · 7 fix | 0.77 [13] · 8 pass | 0.59 [13] · 9 pass | 6.63 | 7.8 |
| law 1 · opus medium | 1.59 [25] · 8 fix | 0.93 [19] · 7 pass | 1.30 [12] · 6 fix | 0.63 [13] · 8 pass | 0.42 [11] · 8 fix | 4.87 | 7.4 |
| law 1 · sonnet high | 1.05 [27] · 8 fix | 0.43 [20] · 8 pass | 0.82 [21] · 5 fix | 0.24 [11] · 7 fix | 0.21 [10] · 8 fix | 2.75 | 7.2 |
| architect · opus high parent | 2.52 [43] · 8 fix | 1.28 [29] · 8 pass | 2.11 [28] · 6 fix | 0.86 [15] · 8 pass | 0.72 [16] · 9 pass | 7.49 | 7.8 |
| law 2 · opus medium | 1.66 [30] · 7 fix | 0.69 [15] · 8 pass | 1.47 [24] · 6 fix | 0.38 [9] · 8 pass | 0.44 [14] · 9 pass | 4.64 | 7.6 |
| law 2 · sonnet high | 0.87 [30] · 8 fix | not run | 0.64 [18] · 7 fix | 0.25 [11] · 8 pass | 0.21 [13] · 8 fix | 1.97 on four | 7.8 |
| law 2 · sonnet medium | 0.84 [32] · 7 fix | 0.27 [14] · 7 pass | 0.38 [12] · 5 fail | 0.17 [8] · 5 fail | 0.15 [8] · 6 fix | 1.81 | 6.0 |
| split · two sonnet medium | — | 0.39 [19] · 8 pass | — | — | — | — | — |
| law 3 · opus medium | 1.80 [30] · 9 pass | — | 1.57 [23] · 8 fix | — | — | — | — |
| law 3 · sonnet medium | 0.77 [28] · 7 fix | — | 0.32 [10] · 4 fail | 0.15 [7], 0.15 [7] | 0.17 [11], 0.18 [9] | — | — |
| scout · opus medium | 2.50 [60], partial | — | 2.91 [76] | — | — | — | not graded: dearer on both |

- Every hook build returns partial: its fifth item is blocked for all of them alike.
- The bench rows above were run with each law version appended to the earlier definition's brief; the design doc cites § Confirm run, where the shipped definitions run alone.
- The law on sonnet · high over the four tasks it ran: $1.97 against the control's $6.85 on those four, mean grade 7.75.

## Confirm run

The shipped definitions, no addendum; dollars [requests] · blind grade where graded.

| Setup | hook | front-end | check | stats | help | total |
| --- | --- | --- | --- | --- | --- | --- |
| `flights:foreman` opus · medium | 1.45 [23] · 7 fix | 0.84 [17] | 1.63 [24] · 6 fix | 0.56 [12] | 0.45 [12] | 4.93 |
| `flights:foreman-sonnet` sonnet · high | 1.02 [30] · 8 fix | 0.33 [18] | 0.85 [28] · 5 fail | 0.24 [9] | 0.24 [16] | 2.68 |
| `flights:foreman` tagged `[effort: high]` | 2.17 [31] · 9 pass | — | 2.78 [30] · 7 fix | — | — | — |

- Hidden tests: stats and help pass the upstream fix's tests on both tiers (157 passed; 5 of 5 subtests); both stats builds pass 6 of 6 concurrency probes; every build's own tests pass on rerun. The front-end, stats and help builds were not blind-graded in this run.
- Start load (first request, tokens): hook 25.8k (control 32.7k), front-end 28.9k (36.2k), check 27.9k (34.8k), stats 22.9k (29.9k), help 23.0k (30.0k).
- Effort actually sent: the opus · medium check build ran 14 requests at medium and 10 at high (the failing-test raise); every other medium build stayed at medium.
- Tools: no refused tool in 12 builds; one sonnet build called `bash` in lower case once.
- What the hard-task grades turned on: check, opus · medium — script variables, a form the brief names, ruled out and returned `DONE`; check, tagged high — a waiver for a point the mapper marks uncertain, which the brief forbids; check, sonnet — a JSON detail body raises out of the check. Hook, opus · medium — four git reads share one timeout fixed before the first, so a slow git runs past the hook's deadline, and the return claimed otherwise; hook, sonnet — the prune deletes any aged file, whatever its name.
- Probes: every opus build's return carries printed probe results; one sonnet front-end build wrote no probe script and said so.
- Return shape after the one-sentence return rule, three reruns: help sonnet 0.27 [17], help opus 0.52 [14], front-end sonnet 0.47 [19, returned partial]; all three open with the status word; hidden tests pass.
- Effort on the two hard tasks under the law, all graded runs: high 7, 7 (check) and 7, 9 (hook), mean price $4.55 the pair; medium 6, 6, 8, 6 and 8, 7, 9, 7, mean price $3.12 the pair.

## What the grades turned on

- Check task, control against law 1: the control was right on all 39 probes; each cheaper build missed a rule (a waiver path, a crash on a JSON page, a script reader too narrow).
- Stats task, sonnet medium: one shared statistics dictionary, a KeyError under 8 threads; 2 of 3 sonnet-medium builds chose it. Sonnet high and opus medium passed 6 of 6 and 5 of 6 concurrency probes.
- Help task, sonnet medium: 1 build of 4 guarded the fix so the root command still failed 3 of 5 hidden subtests.
- Probe step: opus medium ran 4 probes on each hard task; sonnet medium ran none in 5 runs.

## Store analysis behind the law

40 days, 818 Opus foreman runs. Call mix: search 48.8%, read 22.7%, edit 12.3%, test 7.0%. Edits per read or search: 0.172 (5.8 reads an edit). Runs with the fewest reads per edit cost $0.017 a changed line, those with the most $0.045.

Replay of one week (134 runs, $1,262, 19,863 requests; the replay model prices the real runs within 0.6%):

- read streaks batched four to a request −23.1%, unlimited −29.0%;
- read results capped at 3,000 tokens −8.8%;
- compaction at 150k −11.4%, at 80k +10.5%;
- a start 10k tokens smaller −5.6%;
- a survey handed to a fresh Sonnet child −11.0% to −19.1%, to a fresh Opus child +1.2%.

Mid-run cache misses were 2.6% of the dollars.

Under the law, edits per read or search by request: 0.30 to 0.75 across the five tasks.

## Heavy rungs

Two heavy briefs, replayed verbatim from history in clones at their recorded base commits, each run in its own chat and worktree. The report-hang rung: a Go command-line tool over a 1.8 GB SQLite store whose report command hangs, cause unknown; acceptance: every one of 21 report topics exits 0 and a second run takes under 10 s. The list rung: a TypeScript front end with about ten named restyle items, a lint sweep over 153 sites, and a file listing 24 minor items. Prices as in § Price table; versions as in § Versions.

### B1. The report-hang rung

| Run | Dollars | Requests | Wall min | Model + tool min | Peak context | Return | Second run of a topic (bar: under 10 s) |
| --- | ---: | ---: | ---: | --- | ---: | --- | --- |
| foreman before the price law (history) | 12.41 | 202 | 71 | 28 + 43 | 303k | PARTIAL | 40-46 s |
| price law, Opus medium, run 1 | 9.12 | 147 | 93.5 | 26.3 + 67.2 | 260k | PARTIAL | 19-31 s |
| price law, Opus medium, run 2 | 9.55 | 184 | 102.1 | 25.1 + 77.0 | 202k | PARTIAL | 31-40 s |
| price law, Sonnet high | 6.00 | 172 | 91.8 | - | 341k | PARTIAL | 85-115 s |
| work list, Opus medium | 7.79 | 160 | 112.4 | 19.8 + 92.6 | 205k | PARTIAL | 12-26 s without the cost ledger, 78-132 s with it |
| work list + a trace named in the brief, run 1 | 7.61 (+1.35 tracer) | 160 | 93.8 | 17.9 + 75.9 | 201k | PARTIAL | 23.5 s without the ledger |
| work list + a trace named in the brief, run 2 | 9.70 (+1.35 tracer) | 171 | 99.0 | 25.3 + 73.8 | 205k | PARTIAL | 37-56 s |

Every run leaves the same one acceptance item open (the 10-second bar needs a stored-progress design the brief puts out of scope), as the landed build did. Wall time on this rung is confounded: three runs raced CPU-bound report commands on one host at load 20-27; model time is the comparable figure.

Landers on the two price-law Opus builds (history: 6.19 dollars with children, 69 min): 3.68 dollars, 80 requests, 37.7 min, no verdict (it ended its message while two background commands ran, which its definition forbids); 4.24 dollars, 94 requests, 40.7 min, FAIL on the same open item. Whole flight, foreman plus lander: 18.60 dollars and 140 min before; 13.79 dollars and 142.8 min after (-26% dollars at the same wall).

### B2. The list rung (24 minor items listed in a file the brief points to)

| Run | Dollars | Requests | Wall min | Peak context | Compactions | Return opens with | Minor items left open (of 24) |
| --- | ---: | ---: | ---: | ---: | ---: | --- | ---: |
| foreman before the price law, first return (history) | 12.64 | 259 | 45 | 206k | - | PARTIAL | 2 |
| price law, Opus medium, run 1 | 9.26 | 210 | 41.8 | 203k | 1 | PARTIAL | 10 |
| price law, Opus medium, run 2 | 3.92 | 65 | 23.7 | 195k | 0 | PARTIAL | 24 |
| price law, Sonnet high | 2.35 | 82 | 23.1 | 214k | 0 | PARTIAL | 23 |
| work list, Opus medium, run 1 | 8.88 | 173 | 41.7 | 205k | 1 | DONE with a row open | 1 |
| work list, Opus medium, run 2 | 9.30 | 220 | 42.3 | 202k | 1 | PARTIAL | 3 |
| work list, Sonnet high, run 1 | 2.98 | 108 | 25.0 | 233k | 0 | DONE with rows open | 8 |
| work list, Sonnet high, run 2 | 3.40 | 150 | 32.2 | 205k | 1 | PARTIAL | 6 |
| + status rule with the reverted sentence, Opus medium | 5.20 | 96 | 25.0 | 202k | 1 | PARTIAL | about 13 |
| + status rule with the reverted sentence, Sonnet high, run 1 | 3.15 | 110 | 22.6 | 235k | 0 | PARTIAL | about 12 |
| + status rule with the reverted sentence, Sonnet high, run 2 | 4.94 | 166 | 29.5 | 280k | 0 | PARTIAL | 8 |
| shipped text, Opus medium, run 1 | 6.70 | 130 | 32.4 | 217k | 1 | PARTIAL | 4 |
| shipped text, Sonnet high, run 1 | 5.38 | 211 | 39.9 | 256k | 1 | PARTIAL | 4 |
| shipped text, Sonnet high, run 2 | 4.01 | 136 | 25.5 | 272k | 0 | other words before PARTIAL | about 14 |
| shipped text, Opus medium, run 2 | 7.71 | 151 | 32.3 | 213k | 1 | PARTIAL | 4 |
| foreman with workers, run 1 (not shipped) | 7.53 | 242 | 38.0 | 108k (foreman) | 0 | PARTIAL | 1 |
| foreman with workers, run 2 (not shipped) | 8.48 | 312 | 47.8 | 93k (foreman) | 0 | DONE, two failing suites listed as outside | 0 |
| shipped text + read reminder hook (not shipped) | 6.95 | 148 | 30.6 | 202k | 1 | PARTIAL | 4 |

Dollars and requests of the two worker rows are the whole family (foreman plus its workers).

Shipped text on Opus medium, mean of 2: 7.21 dollars and 32.4 min, against 12.64 dollars and 45 min for the first return of the foreman before the price law (-43% dollars, -28% wall) with 4 and 4 minor items open against 2; each open item carries a why outside the run (another owner's file, a routing decision, no code site found). Both returns are the labelled block (15 and 23 lines).

Hidden check of every list-rung build: front-end lint 0 and the affected test suites green. No blind grader ran on the heavy rungs; open-item counts and the hidden checks stand in for the grade.

### B3. Shape facts of the heavy runs (from transcripts)

- 96-97% of requests carry one tool call (a Bash call chaining 11 to 16 commands); 75% of requests are reads; 600-870 output tokens a request; context reaches 200k by request 60 to 91; a compaction leaves 25k to 34k.
- Requests that only wait on a running command: 20 to 32 a run on the report-hang rung (0.94 to 1.07 dollars, 10-14% of the run), 0 to 1 on the list rung.
- Effort is no price lever inside a run: 0.045 dollars a request at xhigh against 0.044 at medium in one run that changed effort.
- A read request is a chain of 2 to 5 commands, not one command: only 11 of 129 and 4 of 150 requests ran a single read command (the two shipped-text Opus runs). What repeats is the read request: 79 and 87 of those requests only read, in streaks of up to 10 and 13 in a row.
- Edits are already packed: 20 and 14 requests carry every edit of a run (a `python3` heredoc with a replace helper, many edits a call); 21 and 31 requests run tests.
- Where one run's dollars sit (the read reminder hook run, 6.95 dollars): cache reads about 3.0 (148 requests over a context growing from 27k to 202k), output about 2.2 (107.7k tokens: 34.2k thinking, 46.8k on edit requests, 40.8k on read requests), cache writes about 1.6.
- The foreman's start is 26,984 tokens; the skill listing is 30,101 characters of it, the agent's own prompt 11,087 characters, project instructions 9,665.

### B4. Negative results

- A trace run named in the brief (one tracer run, 1.35 dollars, five questions about the report path; its path and questions prepended to the foreman's brief): 7.61 and 9.70 dollars against 7.79 without it, inside the run-to-run spread, the trace run named in 2 of 160 to 171 requests. Not built into the plugin.
- An earlier compaction point, by replay of 22 recorded runs of 60 minutes and more: a reset past 180k changes dollars by -2%, past 150k -1%, past 130k +4%, past 110k +18% (each reset pays a 51k floor again); no live run was spent on it.
- The worker. Design benched, not shipped: the Opus medium foreman gains the `Agent` tool; with three or more groups on its work list it reads nothing beyond Locate and spawns one `worker` agent (Sonnet, effort high) per group in one message, the spawn prompt two lines (the list's path, the group's heading); each worker builds, tests and probes its group and returns its list lines rewritten; a group whose edits land wherever a pattern occurs is held back and spawned alone afterwards; the foreman runs the combined tests. Two runs on the list rung: 7.53 and 8.48 dollars against 6.70 and 7.71 for the shipped foreman; 38.0 and 47.8 min against 32.4 and 32.3; 242 and 312 requests against 130 and 151; 1 and 0 minor items open against 4 and 4. Run 1: foreman 2.09 dollars in 26 requests, seven workers 5.44 in 216 requests (0.22 to 1.26 each). Run 2: foreman 1.99 in 24 requests, nine workers 6.51 in 288 requests (0.17 to 1.47 each, 2.0 to 19.0 min each). Why it lost: a worker request costs 0.025 dollars, the same as a request of the Sonnet foreman working alone (5.38 / 211); a worker read with 6 to 48 read requests and 12 to 32 requests before its first edit; seven to nine contexts bring in about twice the tokens one context does (each loads its start, reads shared files, runs its own checks); a foreman wait longer than the 5-minute cache life re-writes its 70k to 100k context at the cache-write rate, about 0.75 dollars a run. It wins completeness only. Guard miss: run 2 returned DONE while its own return listed two failing suites as outside, and the hidden test check failed (1 suite, 2 tests of 926). A small control task (one fix in a Go CLI) spawned no worker: 0.45 dollars, 14 requests, 1.9 min.
- A one-call reader named in the steps. Design benched, not shipped: a script `look` (one call takes names and ranges; a name prints the block of code around each match, found by indentation; `path:A-B` a range; `path:N` the block holding a line; a path alone the file or its outline), defined in Build step 1 with a contrastive example and named in four more steps as the way code is read. One run, stopped at 42 requests, 1.93 dollars, 9.0 min: the foreman defined the shortcut in three calls and never ran it; its reads stayed `grep` and `sed -n`. Naming a reader in the definition does not change the read habit of Opus at medium.
- A read reminder hook. Design benched, not shipped: the shipped text plus a `hooks:` block in the agent's frontmatter, PostToolUse on Bash, running a script that counts requests in a row that only read (greps, `sed -n` ranges, cats, chained or alone) and on every third appends one line to the tool result (`READS {n} requests in a row only read, and each re-read your whole context. Name everything this group still needs and bring it in with the next call: …`). A frontmatter `hooks:` block of a project agent fires for the spawned sub-agent; the event carries `session_id`, `agent_id`, `agent_type` and the command; the hook's `additionalContext` reaches the model appended to that tool result as a system-reminder. One run (table B2): 6.95 dollars, 148 requests, 30.6 min, 4 minor items open, hidden check green (lint 0; 15 suites, 469 tests), against 6.70 and 7.71, 130 and 151 requests, 32.4 and 32.3 min, 4 and 4 open. 16 reminders fired from request 32 on (the hook's first version counted single-command reads and never fired; it was corrected while the run was in flight, so requests 1 to 31 ran without it). After a reminder the foreman called `look`, the first real calls of any run, but its read requests did not fall: 93, against 84 and 101. A reminder changes which reader is called, not how many requests read: each read answers a question the last one raised.
- A context reset after each group (reset size 34k), by replay over the heavy Opus foreman runs in the usage store; no live run: a reset past 180k of context changes dollars by -9%, past 150k -9%, past 130k -8%, past 110k -4%, past 90k +8%, past 75k +39%.
- The status sentence "`PARTIAL` with a sound why is a correct return": reverted (table B2).
- The planned multi-foreman flight. An Opus main chat planned the flight as a tracer first, a foreman per ownership piece, phases (core outward), on the 6-item UI request; two runs, both stopped unfinished:

| Run | Dollars | Minutes | Pieces | State at the stop |
| --- | --- | --- | --- | --- |
| control: one Opus foreman, no tracer | 6.70, 7.71 (+ main chat 0.3 to 1) | 32 | 1 | finished, 4 open items |
| planned flight 1 | 17.85 (sub-agents 15.15 + Opus main chat 2.70 in 25 requests) | 33.0 | 7 Opus foremen 12.35 | phase 1 returned, 3 phase-2 foremen building; 93 files, +1,410 -775, lint 0, jest 30 suites and 848 tests green, 29 formal-address words left |
| planned flight 2 | 10.99 (8.55 + 2.44) | 25.9 | 5 Opus 5.34, 3 Sonnet 0.73 | phase 1 returned, phase 2 just spawned; 15 files changed |

  Why it lost: the tracer run cost $2.5 to $2.8 before any build (37 to 42% of a whole control flight; tracer 0.84 and 22 sub-tracers 1.96 in run 1, 0.83 and 17 sub-tracers 1.65 in run 2), the Opus main chat $2.4 to $2.7 against the control's $0.3 to $1, and each extra Opus foreman pays its own reading ($1.07 to $1.22; a Sonnet piece $0.24). Run 1 tiered all 7 pieces as Opus. No blind grade of the two trees. It is the fourth delegation form that lost to one foreman building alone (an Opus foreman designing for Sonnet children, two foremen sharing a worktree and a tracer reading first being the others). The rule that replaced it is in [flights.md](flights.md) and unbenched.

## Forced-trace probe

A Sonnet foreman (`flights:foreman-sonnet`) on the six-item UI piece of a task; two runs with the plain brief, two with the trace forced into the brief (`TRACE {RUN} · nodes {IDs}`). Hidden check on all four: jest green.

| Arm | Dollars | Requests | Requests before the first edit | Wall min | Status |
| --- | --- | --- | --- | --- | --- |
| plain, run 1 | 0.99 | 39 | 26 | 8.1 | DONE |
| plain, run 2 | 1.31 | 55 | 27 | 12.4 | PARTIAL |
| trace forced, run 1 | 0.77 | 29 | 16 | 7.3 | DONE |
| trace forced, run 2 | 0.92 | 32 | 20 | 7.2 | DONE |

- Reading: forcing the trace is -27% dollars ($0.845 against $1.15), -35% requests and -29% wall time, the two ranges not overlapping.
- The tracer run that fed the trace cost $2.14 (Sonnet lead $0.70 and 13 Haiku readers $1.44): through foremen alone it pays for itself from about 7 Sonnet pieces up; when the main chat traces anyway to plan, forcing the trace into the briefs is free.
- Shipped as the `TRACE` input line of both foremen.
- Open: the four builds were not graded blind; the Opus foreman's use of a trace is unbenched.
