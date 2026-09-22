---
name: {project}-testing-manual
description: The testing law of {project} ({PROJECT_ROLE}) — tiers, where a test lives, lanes and registries, mock boundary, environments, run commands, concurrency, gates and floors, bug classes, traps, what not to test. Read by flights-speccer at intake, by every flight executor before its first test, by flights-gater whole; `/{project}-testing-manual` opens it for a human. Keep it true in the same change that alters how {project} is tested.
---

# {project} testing manual

Fixed headings, fixed order; a section that does not apply says `none`. State the current law only — no history. Stack detail lives in the project's runbook; cite it, never restate it.

## Tiers

- Unit: reaches no real database, queue or network; `{UNIT_RUNNER}` over `{UNIT_TEST_DIR}`.
- Integration: a real boundary is crossed; `{INTEGRATION_RUNNER}` over `{INTEGRATION_TEST_DIR}`.
- The boundary that decides a test's tier here: {one sentence}.

## Where a test lives

- A test lives in the module that owns its contract; the path convention: {source path → test path}.
- Extend the owning test file; add a file only when none exists.

## Lanes and registries

- The lane, beat or suite a new capability lands with, and the registry rows (landscape, map, known-gaps) that land in the same change: {files}.
- Shared-core files with one editor per flight: {paths}.

## Mock boundary

- Always real: {boundaries}. May be mocked: {boundaries}, through {the one mock helper}.

## Environments and cleanup

- Environment files, the stack start command, ports, and the cleanup targets a run owes: {commands}.

## Run commands

- Affected, a flight executor's only run: `{PROJECT_TEST_RUNNER} {path or filter}` — timeout {n} s.
- Full, the flight gate's run and never an executor's: {the one command of the full suite} — timeout {n} s.
- Type check `{PROJECT_TYPECHECK}` · lint `{PROJECT_LINT}` · format `{PROJECT_FORMAT}`.

## Concurrency

- Workers: `{PARALLEL_FLAG}`. What isolates one run from another, and what may never run beside what: {rule}.

## Gates and floors

- Coverage floor {n}%; lint and type check clean; any scored gate with its pass and fail thresholds: {gates}.

## Bug classes

- The finding codes only this project raises, one line each: `{code}` — {what it names}.

## Tricks and traps

- The local knowledge a newcomer gets wrong, one line each: {the wrong move → what it breaks → the right move}.

## What not to test

- Tests this project refuses: {kinds}.
- A removed feature, field, route, flag, environment variable or prompt section takes its tests with it, never inverted into an absence assertion.
