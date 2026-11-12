# interaction-faults

`interaction-faults` forecasts the conditions under which a set of systems breaks together, each cell with the test that holds it or none. The caller passes the systems (a name regex), a code scope and an output directory; the result is `faults.json` plus a ranked top-K table and a NOT READ list. It works in any language and on a repository with no commit history.

## Contents

- [Shape](#shape)
- [Flow and modes](#flow-and-modes)
- [Scripts](#scripts)
- [Rules](#rules)
- [Model](#model)
- [Open items](#open-items)
- [Bench ledger](#bench-ledger)

## Shape

- Entry: skill `templates/global/skills/interaction-faults/SKILL.md`, invoked `/interaction-faults <systems> [paths…] [since]`.
- Coordinator: agent `templates/global/agents/interaction-faults.md`, `opus`, effort `medium`. It never reads diffs, labels, maps, tracer files or source; every merge is a script. Its tools are `Bash` and `Agent` beside the compaction tools: no step calls `Read` or `Write`, it holds no effort button because the steered-role list is closed, and the Codex twin runs `workspace-write` because its scripts write `OUT`.
- Workers, caller-only (visible to the coordinator alone), each `sonnet` at `medium`: `interaction-faults-labeler` (fix commits to factors and door), `interaction-faults-mapper` (today's code to meeting points), `interaction-faults-normalizer` (synonym factor names), `interaction-faults-tracer` (the test that holds a cell).
- Brief fields: `SYSTEMS`, `SCOPE`, `OUT` required; `SINCE`, `MODE`, `TESTS`, `K` (default 10), `REPO` optional. A missing required field, or a missing `SINCE` under `history` or `both`, returns `BRIEF INCOMPLETE — {field}`.
- Return: first line `FAULTS {OUT}/faults.json mode={…} cells={n} forecast={k}`, then the forecast table and GONE lines as `if_rank.py` printed them, then NOT READ.

## Flow and modes

mine and map, then labelers and mappers, then normalizer, then cells, then tracers, then merge, then rank.

- `history`: mine, label, normalize, cells, trace, merge, rank.
- `code`: map, mappers, normalize, cells, trace, merge, rank.
- `both`: every step; the mine and map scripts run in one Bash call and their rows spawn together.
- Auto (no `MODE`): without `SINCE`, `code`; with `SINCE`, run the mine step and any fix found means `both`, no fixes means `code`.
- Failure lines pass through verbatim and stop the run: `MINE FAILED`, `NO SURFACE`, `MAP FAILED`, `NORM FAILED`, `CELLS FAILED`, `MERGE FAILED`, `RANK FAILED`. `NO FIXES` stops a `history` run only.

## Scripts

All sit beside the skill; each has an `if_*_test.py` suite.

| Script | Does |
| --- | --- |
| `if_mine.py` | fix commits since SINCE; diffs capped at 800 lines; labeler batches packed to at most 2400 diff lines |
| `if_map.py` | today's code split into mapper batches, 40-mapper cap, UNMAPPED reported |
| `if_norm.py` | the pairs file for the normalizers and the apply of their synonym maps |
| `if_cells.py` | cells per door file, text re-anchoring of each door to HEAD, the trace plan |
| `if_merge.py` | joins trace rows into cells, rejects malformed rows |
| `if_rank.py` | score, forecast, at most 2 cells per door file |
| `if_repo.py` | git reads and language-agnostic test detection |
| `if_json.py` | shared JSON reads and writes |

## Rules

### A script plans, the coordinator spawns exactly the plan

Why: in the first run the Opus lead read 63 commit diffs itself and held 80% of a $5.68 tree. The plan comes from script PLAN rows; the coordinator adds, drops or merges none, spawns in waves of at most 16, waits by task notifications and never passes a `model`. Rests on: baseline hindsight run; ledger v5 ($4.15, 25 agents against $5.82 and 44 for v4.1).

### Door-centric cells from free-form labels

Why: a fixed vocabulary before labeling narrowed what labelers could name and capped the pool recall near 0.09 to 0.16; free-form labels kept the specificity of per-fix cells. Neighbor cells (one factor value swapped) added about nothing and were deleted. Rests on: v2 to v4 offline rows (H5 refuted).

### Normalize names after labeling, never before

Why: labelers write snake_case names freely; the normalizer maps synonyms to one spelling so equal conditions group. It never merges different conditions. Rests on: v4 plan after the vocabulary refutation.

### Cells name two or three conditions

Why: single-condition cells omitted the second condition the real bug needed. Rests on: v4.1 median-3 R1 0.10 against 0.05 for v4 offline.

### An invalid pair drops itself, not its row

Why: one bad factor pair erased all factors of 16 of 42 and 9 of 43 label rows. A conflicting normalized factor (two names merged to one with two values) keeps the first value and counts the pair in `invalid_pairs`; it does not stop the run. Rests on: v2 seen rows; v7 small-crawler rerun 2 stop at `CELLS FAILED`.

### Trace only the planned cells, rank only traced ones

Why: untraced cells entered the forecast with the default held of none and took 3 of 10 slots. Cells beyond the plan are reported as untraced in NOT READ. Rests on: v4.1 defects; v5 live rows.

### Pre-scores of the two sources are scaled to each source's best cell

Why: code heat (meeting points per file, about 20) and fix heat (decayed fix count, under 1) share no unit, so the trace plan held only code cells and every history cell scored 0. Rests on: v7 zellij row; test watched failing on the revert.

### At most PER_DOOR+1 cells traced per door file, PER_DOOR in the forecast

Why: one 3,000-line workflow file held 25 of 30 planned trace cells and the 2-per-door forecast cap then left 6 of 10 slots. `PER_DOOR` is one constant in `if_rank.py` shared by plan and forecast. Rests on: v7 monorepo row; test watched failing on the revert.

### The tracer reads the fix commits' tests first, and `none` needs `searched`

Why: crediting a unit test for a held cell was the largest accuracy loss of the held check. `if_cells.py --repo` adds `fix_tests` (tests the broken commits touched, nearest the door, same directory first, else the 6 nearest); `held=none` needs a non-empty `searched` or the merge rejects the row. Rests on: v6 A/B, agreement with verified verdicts on the top 10 went from 6 of 10 to 8 of 10.

### Doors are re-anchored to HEAD by text before tracing

Why: `path:line` cites come from the fix commits' era and drift; a Go-only `func Name` anchor failed other languages, so the anchor is a text match. The tracer still reads the function around `door_head` and reports `meet`. Rests on: v6 retrace (every tracer re-located the meet line); v7 bias audit before the build.

### Test detection is language-agnostic and counts only runnable tests

Why: `_test.go` and fixed globs hid Python, TypeScript and Rust tests, and data and docs inside test directories were offered as tests (`docker-compose.test.yml`, spec markdown). `if_repo.is_runnable_test` excludes data and doc suffixes unless `TESTS` matches. Rests on: v7 bias audit; v7 small-crawler R1 row.

### Diffs up to 800 lines, labeler batches packed to 2400 diff lines

Why: a 250-line cap cut 14 of 26 diffs and starved the labels. Rests on: v7 scraper row; v7 zellij row (11 labeler batches, no CUT section).

### Any fix found means `both`

Why: history alone named too few doors (9 cells on the scraper world); `both` costs one labeler and never discards a valid mine. No fixes means `code`. Rests on: v7 scraper row; v7 zellij and monorepo rows.

### Workers are dedicated agents, never general-purpose

Why: the v6 coordinator spawned general-purpose workers with no contract of their own; the owner banned that. Each worker carries its brief fields, its return line and its one write. Rests on: v6 baselines on all four worlds.

### Tracers spawn nothing and write through Bash

Why: v4.1 tracers spawned sub-tracers (refused) and could not write trace files. The tracer holds `Read, Grep, Glob, Bash`; the Codex twin runs `workspace-write`. Rests on: v4.1 NOT READ.

### The mapper is bounded: at most 12 meetings, at most 10 outside reads

Why: a batch of files yields rows in one write; the mapper changes nothing and runs no repository code. Mapper writes to a mistyped `OUT` happened twice; the coordinator checks every expected `labels/map-{i}.json` with `test -s` and respawns that row once. Rests on: v7 zellij row.

## Model

The coordinator is `opus` at `medium`; every worker is `sonnet` at `medium`. Bench rows:

- Opus lead reading everything (v0): 80% of cost. Moving to a Sonnet lead with script plans (v2 to v5) cut a $5.4 to $6.9 run to $3.25 to $4.15 on the Professor repository, accuracy inside the grader noise.
- v7 returned the lead to Opus on the owner's ruling; the lead reads nothing, so its share is the plan and wave management.
- Haiku labelers (v6) became Sonnet in v7 with dedicated agents; no head-to-head of the two on labeling quality was run.
- No model by effort grid was benched on the final body: this is an open item.

## Open items

- The final scripts were never run end to end on the zellij and monorepo worlds; the last runs on both predate the scale and per-door fixes. The "not worse than v6 everywhere" gate is met on the two smaller worlds and unverified on those two.
- The bench scratch (keys, blind copies, grades, capsules) no longer exists; a new bench rebuilds a capsule and a blind key first.
- Strict recall stays near 0 on every version (v6 0.00 on all four worlds, v7 at most 0.02); door-hit is the axis that moves.
- Diffs over 800 lines are cut; the labeler is told what was left out and returns `CUT`.
- The 40-mapper cap leaves large repositories part-mapped (557 unmapped files on zellij); `UNMAPPED` is reported and a narrower scope or a higher `--max-mappers` is the next step.
- Cost is 2 to 3.5 times the previous version because `both` maps the code on every run.
- Grader noise (run-to-run about 0.09 on recall) exceeds most version differences; no accuracy ranking between v1 and v5 stands.
- No model by effort grid, no caller probe and no field-run sweep of the shipped agent exist; the usage audit found no run of it after the last bench.
- The tracer's cap of 8 file reads per cell rests on no bench row.
- The coordinator's tool list without `Read`, `Write` and the effort buttons has not run; every bench row ran with them listed and `effortControl: on`.
- A fix's diff is not split across labelers; a 3,000-line file can still crowd a trace plan below the per-door cap.

## Bench ledger

### Method

- Input: the brief `/interaction-faults <systems> [paths] [since]` over a repository at a cutoff; the later history is the answer key.
- Capsule: a fresh repository holding only the ancestors of the last commit on or before the cutoff, so no future object exists to leak.
- Key: facets are the post-cutoff interaction-fix commits, built by a blind top-tier judge before any forecast; DELIVERED 1, PARTIAL 0.5, MISSING 0. Key sizes: Professor repository R1 29 interaction rows of 60 fix commits; small crawler 9; scraper 6 interaction rows of 36 commits; zellij 24; monorepo 35 of 51 commits.
- Judge and grader: Opus builds keys; median of 3 Sonnet graders with one fixed strict prompt scores recall.
- Cheap instruments: door-hit (key interaction rows whose door file is a forecast cell's door file, top 10 and all cells) and a null model ($0: the 10 files most touched by fixes since SINCE; 31%, 31%, 30% file recall on the Professor repository).
- Cost: the whole spawn tree at the price table.
- Contract: the return read line by line against the agent's return and failure shapes.

### Worlds

- The Professor repository (a Go fleet engine), ladder R1 and R2 (cold start), versions v0 to v6.
- A small Python crawler with almost no fix history (2 fixes, code-only target).
- A Python scraper with 26 fix commits (33 prior fixes).
- zellij, a public Rust terminal multiplexer, 65 fix commits.
- A TypeScript and Python product monorepo (156 prior fixes, 51 key commits).

### Professor repository rows (v0 to v6)

| Version | Run | Result |
| --- | --- | --- |
| v0 | hindsight, 24 agents | $5.68, 16.6 min; 79% of next week's fixes in cells first broken after the cutoff |
| v0 | R1 capsule | $5.85, 19.5 min, one agent, 2 ranked cells |
| v1 | R1 runs B, C | median-3 recall 0.05, 0.14; $5.39 |
| v2 | R1, R2 | recall 0.03 and 0.06 (later 0.03); $6.08, $6.91; door-hit R2 0.12 against null 0.09 |
| v2.2 | R1 F, R2 G | median-3 0.03 and 0.03 |
| v3 offline | R1 H, R2 I | 0.03 and 0.03; neighbor cells add about nothing |
| v4 offline | R1 J | 0.05 |
| v4.1 | R1 L, R2 K | 0.10 and 0.02; $5.82, 44 agents; $4.69, 41 agents |
| v5 | R1 M, R2 N | 0.05 and 0.03; $4.15, 25 agents, about 4 min; $3.25, 22 agents |
| v5 | HEAD, 127 cells | $3.95, 29 agents, 30 traced |
| v6 | retrace on v5 labels | held-verdict agreement 6 of 10 to 8 of 10 |

Product metric: top-11 cells of the v1 forecast sent to fixers on live HEAD gave 5 confirmed bugs, 6 already held (precision 45%). v6's top cells at HEAD: 4 of 4 held.

### v6 baselines on the four v7 worlds

| World | Cells | Forecast | Cost | Wall | Recall | Door-hit top10 / all | Null |
| --- | --- | --- | --- | --- | --- | --- | --- |
| small crawler, no history | 0 | 0 | $0.30, 3 agents | 3m42s | 0 | 0.00 / 0.00 | 0.33 |
| scraper, 26 fixes | 16 | 10 | $2.12, 13 agents | 8m17s | 0.00 | 0.00 / 0.00 | 0.00 |
| zellij | 27 | 10 | $3.67, 21 agents | 8m43s | 0.00 | 0.38 / 0.42 | 0.21 |
| monorepo | 94 | 10 | $5.50, 25 agents | 11m0s | 0.00 | 0.14 / 0.31 | 0.03 |

### v7 rows

| World | Run | Cells | Agents | Cost | Wall | Recall | Door-hit top10 / all |
| --- | --- | --- | --- | --- | --- | --- | --- |
| small crawler | R1 | 120 | 36 | $2.91 | 4m02s | 0.00 | 0.11 / 0.56 |
| scraper | R2 | 9 | 10 | $1.44 | 2m58s | 0.00 | 0.00 / 0.00 |
| zellij | R3 | 389 | 67 | $8.99 | 10m25s | 0.02 | 0.00 / 0.71 |
| monorepo | R4 | 441 | 70 | $12.60 | 6m43s | n/a | 0.11 / 0.49 |
| scraper | rerun 2 | 207 | 61 | $7.67 | 6m09s | n/a | 0.50 / 0.83 |
| small crawler | rerun 2 | stopped by contract at `CELLS FAILED` | 27 | $2.01 | n/a | n/a | offline rebuild all-cells 0.56 |

Each row exposed one fix, confirmed on the next world: R1 the runnable-test filter, R2 the 800-line cap and `both` mode, R3 the prescore scale, R4 the per-door plan cap, rerun 2 the conflict rule. The scraper rerun confirmed the scale and plan fixes on a world they were not derived from; 4 diffs over 800 lines were still cut. A run is ended at the owner's call; zellij and the monorepo were not rerun.
