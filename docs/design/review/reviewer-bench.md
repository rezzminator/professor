# flights:reviewer bench ledger

The bench behind [reviewer.md](reviewer.md): inputs, the metric, judges, prices, versions and every run.

## Contents

- [Bench input](#bench-input)
- [The metric](#the-metric)
- [Judges](#judges)
- [Price table](#price-table)
- [Versions](#versions)
- [Runs](#runs)
- [Negative results](#negative-results)
- [Open items](#open-items)

## Bench input

The three rungs of [flights-lander-bench.md](../flights/flights-lander-bench.md#bench-input): real Professor change sets with defects planted before the first run, each reviewed as an uncommitted tree with its request text as INTENT.

| Rung | Changed lines | Planted defects |
| --- | --- | --- |
| the 648-line rung | 648 | 4 |
| the 2,200-line rung | 2,200 | 9 |
| the 5,480-line rung | 5,480 | 14 |

## The metric

Fixed before the first run: value = (planted defects found, a PARTIAL 0.5, ÷ planted) × (real findings ÷ all findings) ÷ dollars of the whole run, the reviewer and every agent it spawned, priced from the transcripts. Real findings are the planted ones found plus every other finding the grader ruled REAL. Guard: planted recall on a rung never below the old reviewer's. At equal value within the spread, the smaller prompt wins. Two runs of one version on the 5,480-line rung differed by 0.064 (0.174 and 0.238): a gap under that is a tie.

## Judges

Two blind graders (Opus, effort medium, designing nothing) read the reports under neutral letters with the checker names stripped, against each rung's key and the tree: each planted defect FOUND, PARTIAL or MISSED, every other finding REAL (BLOCKS, FIX or COSMETIC), FALSE, PRE-EXISTING or UNCLEAR. Each grade carried calibration letters graded before; all three calibration letters came back with the same planted counts. The three runs of the 648-line rung marked "by key" were not graded blind: each report holds three findings, all on planted lines, matched to the key by path and line.

## Price table

Dollars per million tokens: Opus input 4, output 20, cache read 0.2, five-minute cache write 5; Sonnet half of each.

## Versions

| Version | The one change | Result |
| --- | --- | --- |
| old reviewer | thread-first lead on Opus high with one tracer run | the baseline |
| 1 | the lander's shape: the inventory script plans, the three checkers read, the lead reads only its own share and verifies; Opus medium, no tracer | kept |
| 2 | a kit cuts the diff into packs of units with their far-side lines; one pack-checker per planned set | reverted: 6 of 9 planted on the 2,200-line rung, under the guard |
| 3 | one pack-checker per pack file, the kit assembling the report | reverted: no gain past the spread, costlier on the largest rung, fewer real findings |
| 4 | version 1 plus a literal report skeleton and doc files read as added lines alone | kept, shipped |

## Runs

| Version | Rung | Dollars | Minutes | Planted found / partial / missed | Other real | False | Findings | Value |
| --- | --- | ---: | ---: | --- | ---: | ---: | ---: | ---: |
| old reviewer | 648 | 1.12 | 4.3 | 3 / 0 / 1 | 2 | 0 | 5 | 0.67 |
| old reviewer | 2,200 | 3.91 | 14.8 | 6 / 2 / 1 | 2 | 0 | 9 | 0.199 |
| old reviewer | 5,480 | 5.10 | 33, stopped | no report: the lead waited on a tracer whose reader hung | | | | 0 |
| 1 | 648 | 0.38 | 1.1 | 3 / 0 / 1 | 0 | 0 | 3 | 1.97 |
| 1 | 2,200 | 2.66 | 3.7 | 7 / 1 / 1 | 2 | 0 | 11 | 0.271 |
| 1 | 5,480 | 3.77 | 3.6 | 11 / 0 / 3 | 4 | 1 | 18 | 0.174 |
| 1 | 5,480 | 3.99 | 4.0 | 14 / 0 / 0 | 5 | 0 | 20 | 0.238 |
| 2 | 2,200 | 2.03 | 3.3 | 6 / 0 / 3 by key | 1 | | 7 | under the guard |
| 3 | 648 | 0.36 | 0.8 | 3 / 0 / 1 by key | 0 | 0 | 3 | 2.08 |
| 3 | 2,200 | 2.63 | 2.4 | 7 / 0 / 2 | 2 | 0 | 9 | 0.296 |
| 3 | 5,480 | 4.65 | 3.9 | 12 / 0 / 2 | 2 | 0 | 14 | 0.184 |
| 3 | 5,480 | 4.41 | 3.4 | 12 / 0 / 2 | 2 | 0 | 14 | 0.194 |
| 4 | 648 | 0.48 | 1.5 | 3 / 0 / 1 by key | 0 | 0 | 3 | 1.56 |
| 4 | 2,200 | 2.46 | 4.8 | 7 / 1 / 1 | 1 | 0 | 9 | 0.320 |
| 4 | 5,480 | 4.15 | 5.2 | 13 / 0 / 1 | 4 | 0 | 19 | 0.200 |

Reference, the lander's own review stopped before fixing, on the 5,480-line rung: $4.25 with 14 of 14 planted and 3 other real findings, value 0.222.

Where version 4's dollars go on the 5,480-line rung: the lead $1.43 (peak context 117,000 tokens), four diff-checkers $2.50, the usage-checkers $0.21. On the 2,200-line rung: the lead $0.92, two diff-checkers $1.39, the usage-checkers $0.15. The 648-line rung spawns no checker: 9 requests of the lead.

## Negative results

- Packs of units (versions 2 and 3): the packs fed 514,000 characters on the largest rung, twice the raw diff, and each of 11 checkers paid a 13,000-token base context: $3.3 for the checkers against $2.3 for four diff-checkers reading their clusters themselves. A checker handed a prepared pack wrote 3,000 to 7,000 output tokens and made four lookups; it reported the planted defects and 2 other real findings a run, where a diff-checker exploring its cluster reported 4 to 5.
- One checker for a 128,000-character set (version 2) missed 3 of the 6 planted defects in its set.
- The tracer-led reviewer on the largest rung: $5.10 and no report.
- Version 4 against version 1 is a tie on value on every rung (0.320 against 0.271, 0.200 against a mean of 0.206, 1.56 against 1.97, each from one or two runs); it ships for the report layout it holds exactly.

## Open items

- One run per rung for version 4; the spread comes from version 1's two runs of the largest rung.
- Unbenched: a diff past 10,000 lines, a second repository, a commit range, PATHSPECS, a re-review, cluster sizes other than 750, the lead on Sonnet or at another effort, a diff-checker effort pin.
- Real-defect recall against the estimated 21 to 26 real defects of the largest rung is 2 to 5 a review in every shape; a second, differently cut review is the only arm seen to raise it (8 real findings for $6.18 on the lander's bench).
