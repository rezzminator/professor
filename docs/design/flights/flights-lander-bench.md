# flights:lander bench ledger

The bench behind [flights-lander.md](flights-lander.md): inputs, judges, prices, versions and every result.

## Contents

- [Bench input](#bench-input)
- [The metric](#the-metric)
- [Judges](#judges)
- [Price table](#price-table)
- [Versions](#versions)
- [Review-only results](#review-only-results)
- [Full landings](#full-landings)
- [Phase split of the full landings](#phase-split-of-the-full-landings)
- [History replay](#history-replay)
- [Negative results](#negative-results)
- [Open items](#open-items)

## Bench input

Three rungs of real Professor commit ranges, each with defects planted before the first run.

| Rung | Changed lines | Flights | Planted defects |
| --- | --- | --- | --- |
| the 648-line rung | 648 | 1 | 4 |
| the 2,200-line rung | 2,200 | 8 | 9 |
| the 5,480-line rung | 5,480 | 7 | 14 |

The full landings use a lab testing manual: `go vet` as the affected-test command, `go build && go vet` as the gate, under a minute here. One landing of the 2,200-line rung used a slow gate (a 330 s pause, then build and vet).

## The metric

Fixed before the first run. Landing dollars per 100 changed lines (lander and children) and landing wall time. Guard: recall of the planted defects not below the single-flight runs, a verdict returned, the gate reached. A version whose dollars per 100 lines rise from the small rung to the large one has not won. A landing's price is every request of the lander and of every child it spawned.

## Judges

- A blind Opus grader per review-only wave: the runs under neutral letters, one list of planted defects and one list of other findings; each planted defect found, partial or missed, each other finding real (blocking, fix or cosmetic) or false.
- Full landings: a script reads each planted defect in the landed tree as restored, rewritten or still planted. It reads one planted defect of the 5,480-line rung and one of the 2,200-line rung as REWRITTEN even on an untouched tree: subtract one REWRITTEN on each.
- Objective: the landed tree builds, the gate's exit code.

## Price table

As in [flights-foreman-bench.md](flights-foreman-bench.md) § Price table: dollars per million tokens, Opus input 4, output 20, cache read 0.2, 5-minute cache write 5; Sonnet half of each. A landing's dollars are every request of the lander and its children from their transcripts.

## Versions

| Version | The one change |
| --- | --- |
| installed | the lander before this change: `min(4, ceil(lines / 600))` diff-checker clusters, review, fixes, tests, gate |
| the review cut by area | diff-checker clusters by directory of about 600 changed lines, 12 at most, a `PLAN OWN` row, a diff-checker that returns `CONTRACTS` |
| the shed | the installed lander plus one `forward_compaction` call between the report and the tests |
| the review cut by area plus the shed | both, the shed before the fixes |
| Sonnet readers | the review cut by area with the diff-checkers on Sonnet at effort medium, and at effort high |
| ship form | the review cut by area, the shed before the fixes, 270 s waits |
| shipped | the installed review, the fixes, then the shed, then tests and gate, 270 s waits |

## Review-only results

`STOP_AT review`, blind-graded. "Own" is the lander's own dollars; the rest is its children.

| Rung | Lander | Dollars | Own, peak context | Wall min | Planted found / partial / missed | Other real (blocking / fix / cosmetic) | False |
| --- | --- | --- | --- | --- | --- | --- | --- |
| 648 lines | installed | 0.59 | 0.59, 62k | 1.6 | 3/0/1 | 0/0/1 | 0 |
| 648 lines | the review cut by area | 0.43 | 0.43, 57k | 1.3 | 3/0/1 | 0/0/1 | 0 |
| 2,200 lines | installed | 3.03 | 1.68, 137k | 4.1 | 7/1/1 | 0/2/0 | 1 |
| 2,200 lines | the review cut by area | 3.01 | 1.37, 103k | 4.8 | 7/1/1 | 0/4/1 | 0 |
| 5,480 lines | installed, run 1 | 4.25 | 1.80, 146k | 4.2 | 14/0/0 | 0/3/0 | 1 |
| 5,480 lines | installed, run 2 | 4.41 | 2.01, 153k | 4.6 | 12/0/2 | 0/1/1 | 0 |
| 5,480 lines | the review cut by area, run 1 | 6.18 | 1.99, 162k | 5.4 | 14/0/0 | 1/5/2 | 0 |
| 5,480 lines | the review cut by area, run 2 | 5.36 | 1.77, 142k | 4.8 | 13/0/1 | 1/7/2 | 0 |

Dollars per 100 changed lines, review only:

| Rung | installed | the review cut by area |
| --- | --- | --- |
| 648 lines | 0.091 | 0.066 |
| 2,200 lines | 0.138 | 0.137 |
| 5,480 lines | 0.079 | 0.105 |

- Planted recall is equal: installed 26 of 28 on the 5,480-line rung, the review cut by area 27 of 28.
- The review cut by area finds more real defects that were not planted in the real commits (8 and 10 on the 5,480-line rung against 3 and 2) and wrote no false finding (installed: 2). The one blocking defect, the engine guard judging the wrong operand as the script (`pfm/internal/hookentry/engine_guard.go`, lines 331-337 of commit add35b49), was found by both runs of the review cut by area and by neither installed run.
- It costs the same at the 2,200-line rung and $1.44 more (+33%) at the 5,480-line rung: 6 diff-checkers instead of 4 ($3.35 against $2.23) plus contract threads ($0.27 to $0.81).

### Sonnet readers, 5,480-line rung

Blind-graded, review only.

| Lander | Diff readers | Dollars | Readers' dollars | Wall min | Planted found / partial / missed | Other real | False |
| --- | --- | --- | --- | --- | --- | --- | --- |
| installed | 4 Opus | 4.25, 4.41 | 2.23 | 4.2, 4.6 | 14/0/0, 12/0/2 | 0/3/0, 0/1/1 | 1, 0 |
| the review cut by area | 6 Opus | 6.18, 5.36 | 3.35 | 5.4, 4.8 | 14/0/0, 13/0/1 | 1/5/2, 1/7/2 | 0, 0 |
| the review cut by area | 6 Sonnet medium | 3.56, 2.57 | 0.73, 0.71 | 5.8, 4.0 | 9/1/4, 10/0/4 | 0/2/1, 0/1/0 | 0, 0 |
| the review cut by area | 6 Sonnet high | 4.84 | 1.82 | 6.3 | 12/0/2 | 0/1/2 | 2 (+1 unclear) |

A Sonnet reader costs $0.12 (medium) or $0.30 (high) against $0.56 for Opus; the review is $2.70 cheaper and loses a quarter of the planted defects.

## Full landings

| Rung | Lander | Dollars | Own, requests | Wall min | Verdict | Findings fixed | Planted restored / rewritten / still planted |
| --- | --- | --- | --- | --- | --- | --- | --- |
| 2,200 lines | installed | 3.06 | 1.86, 37 (peak 146k to the end) | 3.9 | FIXED | 7 | 6/1/2 |
| 2,200 lines | installed plus the shed | 3.01 | 1.68, 39 (123k, then 31-41k after the shed) | 5.4 | FIXED | 9 | 5/2/2 |
| 5,480 lines | installed | 5.46 | 2.69, 61 (peak 158k) | 6.0 | FIXED | 16 | 9/2/3 |
| 5,480 lines | the review cut by area plus the shed | 7.73 | 3.23, 69 (peak 167k, 34-57k after the shed) | 8.4 | FIXED | 30 | 9/4/1 |
| 2,200 lines, slow gate | ship form | 4.78 | 3.25, 73 | 14.1 (gate 6) | FIXED | 11 | 5/3/1 |

- All landed trees build. The ship-form row adds 2 diff-checkers $1.12, 4 usage-checkers $0.40 and a relay chat $0.15; its mechanics as written: exactly the `PLAN` rows in one message, one `forward_compaction` call (144k to 34k), the manual opened after it, the gate in the background with two waits (270 s and 250 s) and none longer.
- The shed works live: a 1,866-character frontier with the report in `load_files` stored at 121.5k, the next request installed it (123k to 31k), and the lander ran its tests and the gate and returned in 6 more requests at 31-41k; its 8 code edits came before the shed. The lab gate is far cheaper than a real one, so the live saving there is small ($0.18 of own); its size is the replay's.
- The review cut by area plus the shed fixes 30 findings against 16 and leaves 1 planted defect against 3, for $2.27 (+42%) and 2.4 minutes more. Its shed fired at request 52 of 69: the lander spent 48 requests verifying about 40 candidates at 41k to 161k tokens before it, so only 17 requests were left to cheapen.

## Phase split of the full landings

Own requests and dollars up to the shed (or, with no shed, the report write), and after it.

| Landing | Up to the shed or report | After | Findings fixed |
| --- | --- | --- | --- |
| 2,200 lines, installed | 24 requests, $1.23, 4 code edits | 13 requests, $0.63, at 128-146k | 7 |
| 2,200 lines, installed plus the shed | 33 requests, $1.45, 8 code edits | 6 requests, $0.23, at 31-41k | 9 |
| 2,200 lines, ship form | 32 requests, $1.80, 0 code edits | 41 requests, $1.45, 18 code edits, 34k growing to 99k | 11 |
| 5,480 lines, installed | 58 requests, $2.53 | 3 requests, $0.17, at 157k | 16 |
| 5,480 lines, the review cut by area plus the shed | 52 requests, $2.70, 3 code edits | 17 requests, $0.54, at 34-57k | 30 |

A lander that sheds before it fixes re-reads every file it fixes (41 requests, $1.45); one that fixes with the review in context and sheds before the tests pays $0.23 after the shed. The shipped order is fix, shed, tests, gate.

## History replay

Over the usage store's past landings of 1000 or more changed lines.

| Replay | Landings | Own / children | Wake 35k / 45k / 55k saves | Share of own | Share of the landing | Median per landing | Landings that lose |
| --- | --- | --- | --- | --- | --- | --- | --- |
| shed at the report write | 103 | $679 / $316 | $184 / $162 / $140 | 27% / 24% / 21% | 18.5% / 16% / 14% | $1.18 / $0.96 / $0.79 | 11 to 17 |
| shed after the fixes (boundary = the first affected-test or gate request after the report) | 95 | $647 / $291 | $128 / $112 / $96 | 19.8% / 17.3% / 14.8% | 13.7% / 12.0% / 10.2% | $1.00 / $0.77 / $0.63 | 9 / 11 / 16 |

- The report-write replay does not charge the re-reads the live run showed; the after-the-fixes replay is the one the shipped order rests on.
- Median boundary context in the report-write replay 154k tokens, median 35 requests after it. In past landings 62% of a large landing's own dollars come after the review.
- Sub-agents get no compaction nudge before 200k tokens (20% of a 1,000,000-token window), and landers stored a frontier in 4 of about 68 runs, which is why they carried 150k through fixes, tests and gate.
- Waits: the requests that follow one foreground call of 290 s or more cost $77 across 239 landers; in 270 s slices $9. Foremen paid $162 in 30 days after one foreground call of 290 s or more.

## Negative results

- The review cut by area lost on the price metric. By the metric fixed before the first run, it has not won: review-only dollars per 100 lines are 0.066 / 0.137 / 0.105 against 0.091 / 0.138 / 0.079 installed; a full landing costs +42% on the 5,480-line rung and +56% on the 2,200-line rung (ship form, one run, with the shed-before-fixes order fault included). It wins on findings only (other real defects 18 against 5 over the two 5,480-line runs, false findings 0 against 2, the one blocking defect only it found), which the metric does not hold. Held ready, not shipped.
- The "lean coordinator" claim failed: the lander of the review cut by area ends its review at 142-162k tokens like the installed one. Where the context comes from (5,480-line rung, run 1): 12 child returns about 45k tokens (16k characters each), own reads 44k, the `PLAN OWN` diff 20k (the inventory's "191 lines" printed 69k characters), own output and thinking 30k.
- Sonnet diff readers at medium effort (9.5 and 10 of 14 planted defects, Opus 13.5) and at high effort (12 of 14, 2 false findings): rejected by the ship rule (13 of 14 or better), and none of the three runs found the blocking defect. The diff-checker stays on Opus.
- The shed before the fixes (ship form): 41 requests and $1.45 after the shed against 6 requests and $0.23 where the fixes came first.

## Open items

- The review cut by area with the fixes before the shed: unbenched as a full landing.
- Verifying the checkers' candidates after the shed, each checker's return kept in a file: unbenched.
- A rung in a second repository: not run.
- A real long gate: the Professor gate took 64 minutes at host load 24 and was never run in the bench.
- The Sonnet diff-checker on the shipped review: not run.
