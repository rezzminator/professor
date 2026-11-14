---
name: {project}-testing-manual
description: Governs how {project} ({PROJECT_ROLE}) is tested — tiers, test homes, suite duties, mocks, run commands, gates and floors, bug classes, traps. Read by the flights foremen for the affected tests, the flights lander whole for the full gate; `/{project}-testing-manual` opens it for a human. Keep it true in the same change that alters how {project} is tested.
---

# {project} testing manual

Fixed headings, fixed order; a section that does not apply says `none`. State the current law only — no history. Stack detail lives in the project's runbook; cite it, never restate it. How to write a test is the `testing` rule's (`~/.claude/rules/testing.md`); this manual holds {project}'s facts and how its tests run.

## Tiers

- Unit: reaches no real database, queue or network; `{UNIT_RUNNER}` over `{UNIT_TEST_DIR}`.
- Integration: a real boundary is crossed; `{INTEGRATION_RUNNER}` over `{INTEGRATION_TEST_DIR}`.
- The boundary that decides a test's tier here: {one sentence}.

## Where a test lives

- A test lives in the module that owns its contract; the path convention: {source path → test path}.

## Suite duties

- A new or changed capability lands in one commit with the step or test that walks it, watched red: {the tree file and the test homes}.
- Where a live tier exists, every pull request runs the whole tree on every operating system the suite design lists: {the tree command}; while fixing, the touched branch alone: {the filtered command}.
- An escaped bug's fix lands with its assertion on the step or test that walked past it and its `escapes.tsv` line: {the escapes path}.
- Shared-core files with one editor per flight: {paths}.

## Mock boundary

- Always real: {boundaries}. May be mocked: {boundaries}, through {the one mock helper}.

## Environments and cleanup

- Environment files, the stack start command, ports, and the cleanup targets a run owes: {commands}.

## Run commands

- Tests run only {where: the sandbox or command they run through}, against the tree holding the change; CI is never a debug loop, and every suite CI runs passes here before a push.
- Development runs only the tests the change affects; the full gate runs once per change, at no other time: on a flight once per worktree, right before its merge, by the lander; on small work by the main chat, before the commit. A suite green on a tree never reruns on that tree; a red reruns only its failing tests until green, then the red suite again, until every suite is green.
- Affected, an executor's only run (flight or general): `{PROJECT_TEST_RUNNER} {path or filter}` — timeout {n} s.
- Full, the lander's run on a flight or the main chat's on small work, never an executor's: {the one command of the full suite} — timeout {n} s.
- Static check, an executor's only one, run once, last, given its task's files: {the one static-check command that takes a list of files}; a red is fixed and the same command run again.
- Type check `{PROJECT_TYPECHECK}` · lint `{PROJECT_LINT}` · format `{PROJECT_FORMAT}`.

## Concurrency

- Workers: `{PARALLEL_FLAG}`.
- No two runs share a tree, a database or a fixture directory: {what isolates one run from another}; what may never run beside what: {rule}.

## Gates and floors

- Coverage floor {n}%; lint and type check clean; any scored gate with its pass and fail thresholds: {gates}.
- Wall-time budget per suite: {suite → n s}; a suite over budget has its slowest tests measured before any test joins it.

## Bug classes

- The finding codes only this project raises, one line each: `{code}` — {what it names}.

## Tricks and traps

- The local knowledge a newcomer gets wrong, one line each: {the wrong move → what it breaks → the right move}.

## What not to test

- Tests this project refuses: {kinds}.
- This stack's spelling of the `/design:test-suite` refused shapes: {shape → spelling}.
