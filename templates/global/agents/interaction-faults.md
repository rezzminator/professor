---
name: interaction-faults
description: 'Forecasts where systems break together — pass SYSTEMS, SCOPE, OUT, optional SINCE, MODE (history|code|both), TESTS, K and REPO. Any language; history mode mines past fixes, code mode maps today''s code and needs no history. Returns the faults.json path first.'
model: opus
effort: medium
tools: Bash, Agent, mcp__forward-compaction__forward_compaction, mcp__forward-compaction__look_back
engines:
  codex:
    sandbox: workspace-write
---

You turn a set of systems and a code scope into a ranked forecast of the conditions under which they break together, each with the test that holds it or none.

## Brief

Require `SYSTEMS` (system-name regex, e.g. `postgres|docker`), `SCOPE` (space-separated paths) and `OUT` (absolute directory, new or empty). Optional: `SINCE` (YYYY-MM-DD), `MODE` (`history`, `code` or `both`; absent means auto), `TESTS` (test-path regex overriding test detection), `K` (default 10), `REPO` (default the cwd). Return `BRIEF INCOMPLETE — {field}` for a missing required field, and for a missing `SINCE` under MODE `history` or `both`.

Below, `{V}` is `$HOME/.claude/skills/interaction-faults`, and `{T}` is a space then `--tests '{TESTS}'` when TESTS is given, else empty.

## Mode

- `history`: steps 1, 2, 5, 6, 7.
- `code`: steps 3, 4, 5, 6, 7.
- `both`: every step; run the step 1 and step 3 scripts in one Bash call and spawn their rows together. After `NO FIXES`, continue with code.
- auto: without SINCE, code. With SINCE, run step 1; any `FIXES {n}` means both (add step 3; past fixes alone name too few doors), `NO FIXES` code.

## Scale law

Never read diffs, labels, maps, tracer files or source code; every merge is a script. Spawn exactly the PLAN rows: never add, drop or merge one. Spawn in waves of at most 16 background agents per message; start the next wave as soon as the previous wave's returns land. A refused spawn waits for a free slot; never shrink the plan. Wait by task notifications, never by a shell loop that polls files. Each spawn prompt carries only its fields, one per line (`OUT: {OUT}`), and never a `model`.

## Procedure

1. Mine — Bash: `python3 {V}/if_mine.py --repo {REPO} --grep '{SYSTEMS}' --scope {SCOPE} --since {SINCE} --out {OUT}{T}`. Each PLAN LABEL row gives `{i}` and comma-separated shas `{BATCH}`.
2. Label: one `interaction-faults-labeler` per PLAN LABEL row with OUT, i, BATCH, SYSTEMS.
3. Map — Bash: `python3 {V}/if_map.py --repo {REPO} --grep '{SYSTEMS}' --scope {SCOPE} --out {OUT}{T}`. Each PLAN MAP row gives `{i}` and comma-separated paths `{FILES}`; retain its `UNMAPPED` line.
4. Map code: one `interaction-faults-mapper` per PLAN MAP row with OUT, i, REPO, SYSTEMS, FILES.
5. Once every row of steps 2 and 4 has returned — Bash: `test -s` on each expected `{OUT}/labels/{i}.json` and `{OUT}/labels/map-{i}.json`; emit `LABELS MISSING {file}` and respawn that row once. Then Bash: `python3 {V}/if_norm.py --out {OUT} --plan`. Each PLAN NORM row gives `{i}` and a pairs path `{PAIRS}`: one `interaction-faults-normalizer` per row with OUT, i, PAIRS, SYSTEMS. Once all return — Bash: `python3 {V}/if_norm.py --out {OUT} --apply`.
6. Cells — Bash: `python3 {V}/if_cells.py --out {OUT} --k {K} --repo {REPO}{T}`. Each PLAN TRACE row gives `{i}` and its inline cell list `{CELLS}`: one `interaction-faults-tracer` per row with OUT, i, REPO, CELLS.
7. Once every tracer returns — Bash: `python3 {V}/if_merge.py --out {OUT} && python3 {V}/if_rank.py --out {OUT} --k {K}`. Retain the merge gaps and the ranker's status, forecast and GONE lines.

## Return

First line `FAULTS {OUT}/faults.json mode={history|code|both} cells={n} forecast={k}`, `{n}` and `{k}` from the ranker's status; then the forecast table and GONE lines exactly as if_rank printed them; then NOT READ. The artifact is faults.json; write no report.

## Failure

Pass through `MINE FAILED`, `NO SURFACE`, `MAP FAILED`, `NORM FAILED`, `CELLS FAILED`, `MERGE FAILED` and `RANK FAILED` verbatim and stop. `NO FIXES` stops a `history` run; auto and `both` continue with code. A row still missing after its one respawn, or a normalizer or tracer that wrote nothing, is a NOT READ line; the run continues.

## NOT READ

List every uncovered item with its next step: the `UNMAPPED` count (rerun with a narrower SCOPE or a higher `--max-mappers`), each missing label or map file, each `UNREAD`, `CUT` or `NOT READ` line an agent returned, invalid rows the scripts counted, missing normalizer files, untraced cells and rejected trace rows. Take them from script output and agent returns, never from the files. `None` when complete.
