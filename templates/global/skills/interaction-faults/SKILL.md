---
# professor: SOURCE TEMPLATE — edit here for a framework change (routes through the `pcm` agent); project-scaffold customization belongs in its installed local source; engine mirrors are never hand-edited.
name: interaction-faults
description: 'Ranks where systems break together — `/interaction-faults <systems|paths> [since]`, or asks like "same areas keep breaking", "fix whack-a-mole", "which combinations does no test hold". Returns ranked cells as JSON and a three-view report. Test design → /quality:integration-suite.'
argument-hint: "<systems or paths> [since]"
---

# interaction-faults

Find the bugs that appear only when particular states of two or more systems meet while each system passes its own tests, ranked by what git history and the tests prove.

## Terms

- system: an argument; a path as given, or a name resolved to the tracked paths implementing or adapting it (`git ls-files | grep -i {name}`, `git grep -il {name}`).
- factor: one dimension where the systems meet, of one `kind`:
  - `parameter`: a configuration or runtime-state value (multiplexer, engine, pane state).
  - `feature`: something on or off (a protection layer, a plugin, a mode).
  - `event`: a runtime happening from outside (a server restart, a killed pane, a pruned version, a reload).
- cell: a combination of factor values, a pair by default, a triple only when a fix proves all three factors were needed. A cell is not a test row: turning cells into test rows is test design, `/quality:integration-suite`'s.
- group: set by the kinds in a cell, `event` beating `feature`, which beats `parameter`:
  - `combinatorial`, parameters only: combinatorial testing (NIST; Kuhn, Wallace & Gallo, "Software Fault Interactions and Implications for Software Testing", IEEE TSE 2004: most failures are triggered by 2–3 interacting factors).
  - `feature`, a feature and no event: the telephony "feature interaction problem", two features each correct alone conflicting when both are on.
  - `external`, any event: Avižienis, Laprie, Randell & Landwehr, "Basic Concepts and Taxonomy of Dependable and Secure Computing", IEEE TDSC 2004: interaction faults are external faults entering during operation from operators, users or other systems.
- worst: the cell's worst outcome, in order `data-loss` (work or state destroyed) > `wrong-target` (an action lands on the wrong pane, seat, chat or file) > `wrong-state` (a reported or stored state is wrong) > `cosmetic` (display only).
- held_by: `{test id} {path}` of the test whose assertion pins the cell's outcome; null when none does.

## Setup

- `$OUT`: `/tmp/{project}/interaction-faults/{slug}/`, `{project}` being `basename "$(git rev-parse --show-toplevel)"` minus any leading dot, `{slug}` the systems joined by `-` (`pfm-herdr-tmux`). It holds `faults.json`, kept current from step 1 on, and `report.md`.
- Window: `until` is today; `since` is the argument, or else the latest of the systems' first-add dates, each `git log --diff-filter=A --format=%as -- {its paths} | tail -1`.

## 1. Factors

Read the code where the systems meet: a call from one system's paths into another's, a state file or config key both read. Each factor and each value is found at a path:line: the factor's goes in `source`, each value's in the report's factor table. Write `interaction`, `systems`, `window` and `factors` to `faults.json`.

## 2. Breaks

List the window's fix commits touching the systems:

```bash
git log --no-merges --since='{since} 00:00' --until='{until} 23:59:59' -i -E --grep='fix|bug|regress|broke|revert' --format='%h %as %s' -- {paths}
```

Read each commit's message and diff (`git show {sha}`), and assign it to the smallest value combination that triggered it: append its sha to that cell's `broke`, or open the cell. A trigger outside the factor list joins it under step 1's source rule. A commit no cell explains goes to `unassigned` with its `why`, never dropped. A broken cell's `worst` is the worst outcome its commits describe.

## 3. Untested pairs

Pair two factors only where they share a code path, its path:line cited in the report. Enumerate their value pairs:

```bash
python3 -c 'import itertools as it,json,sys; F={f["name"]:f["values"] for f in json.load(open(sys.argv[1]))["factors"]}; [print(f"{a}={x} {b}={y}") for a,b in (p.split(":") for p in sys.argv[2:]) for x,y in it.product(F[a],F[b])]' "$OUT/faults.json" {factor}:{factor} …
```

Skip a pair that is already a cell. Judge each remaining pair's `worst` from the shared code path; keep it as a cell with `broke: []` only at `data-loss` or `wrong-target`.

## 4. Coverage

For each cell, search the test tree (unit, hermetic, lane) for tests reaching its values and read their assertions. Set `held_by` only from an assertion that pins the cell's outcome, never from a test's name: a lane check named `status` that asserts only `idle` holds the idle cell and leaves the `working` cell beside it open.

## 5. Rank and write

`rank` comes from evidence, never from predicted likelihood:

1. `broke` holds 2 or more shas, `held_by` null: the whack-a-mole.
2. `broke` holds 1 sha, `held_by` null.
3. `broke` empty, `held_by` null, `worst` `data-loss` or `wrong-target`.

A cell with `held_by` set is listed with `rank: null` after the ranked ones. Within a rank, order by `worst`, then by the most recent break. `cells` and each `views` list keep that order; a cell sits in its group's view only.

```json
{
  "interaction": "pfm × herdr × tmux",
  "systems": ["pfm", "herdr", "tmux"],
  "window": { "since": "YYYY-MM-DD", "until": "YYYY-MM-DD" },
  "factors": [
    { "name": "multiplexer", "kind": "parameter", "values": ["tmux", "herdr"], "source": "path:line" },
    { "name": "herdr_shield", "kind": "feature", "values": ["off", "on"], "source": "path:line" },
    { "name": "runtime_event", "kind": "event", "values": ["none", "reload", "seat_retired"], "source": "path:line" }
  ],
  "cells": [
    { "id": "C1", "values": { "runtime_event": "seat_retired", "multiplexer": "herdr" },
      "group": "external", "broke": ["{sha}", "{sha}"], "held_by": null,
      "worst": "wrong-target", "rank": 1 },
    { "id": "C2", "values": { "pane_state": "idle", "multiplexer": "herdr" },
      "group": "combinatorial", "broke": ["{sha}"], "held_by": "{test id} {path}",
      "worst": "wrong-state", "rank": null }
  ],
  "unassigned": [ { "sha": "{sha}", "why": "..." } ],
  "views": { "combinatorial": ["C2"], "feature": [], "external": ["C1"] }
}
```

`report.md`, in order: the systems with their paths; the window; every command run with its result count; the factor table (factor, kind, value, source) and the cited factor pairs; the three views, each a ranked table (`rank · id · values · worst · broke · held_by`) with rank-1 cells first; `unassigned`; the dropped factors and values; last, one line: `Next: /quality:integration-suite {absolute path of faults.json}`.

## Failure looks like

- No fix commits: report "no fix commits found" with the step 2 command and its count beside the same command without `--grep`; zero there too means the paths or window are wrong, never a clean history.
- No code path shared by two systems: report "no interaction surface found" with the paths searched, and stop.
- A factor or value with no source: dropped, listed in the report.

Every cell, rank and `held_by` stands on a cited commit, code line or read assertion, never on likelihood or a test's name.
