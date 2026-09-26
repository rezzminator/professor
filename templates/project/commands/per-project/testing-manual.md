---
name: {project}-testing-manual
description: The testing law of {project} ({PROJECT_ROLE}) — tiers, where a test lives, lanes and registries, mock boundary, environments, run commands, concurrency, gates and floors, bug classes, traps, what not to test. Read by flights-speccer at intake, by every flight or general executor before its first test, by flights-lander whole; `/{project}-testing-manual` opens it for a human. Keep it true in the same change that alters how {project} is tested.
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

- A new or changed capability lands in one commit with its landscape row, its map row, and the unit, hermetic or live row mapping it together with that row's bite: {the landscape, map and ledger paths}.
- Where a live tier exists, every close runs the full lane sequence: {the sequence command}.
- Shared-core files with one editor per flight: {paths}.

## Mock boundary

- Always real: {boundaries}. May be mocked: {boundaries}, through {the one mock helper}.

## Environments and cleanup

- Environment files, the stack start command, ports, and the cleanup targets a run owes: {commands}.

## Run commands

- Affected, an executor's only run (flight or general): `{PROJECT_TEST_RUNNER} {path or filter}` — timeout {n} s.
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
- The refused shapes, in any unit, hermetic or live test, its harness and its checks: a fail-open verdict; a weak or tautological oracle; an unscoped read of a shared store; a wall-clock wait; a consuming probe; the toolchain tested as product; shipped migration content in a test; a source invariant tested instead of linted; a self-skip; a double or a test path in production code; a second logging path; a line-anchored exemption; a host path, process id or captured log in a fixture. This stack's spelling of each: {shape → spelling}.
- A removed feature, field, route, flag, environment variable or prompt section takes its tests with it, never inverted into an absence assertion.
