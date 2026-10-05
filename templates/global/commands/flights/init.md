---
name: flights:init
description: 'Readies a repository for flights — /flights:init [root]: maps its build units inside-out, writes the speccer manual, gives each unit a testing manual, test-{unit}.sh and check-{unit}.sh with their tests; re-runnable. here → /flights:spec. Returns a row per unit, the speccer-manual path, gaps.'
argument-hint: [repository root]
---

# Init — ready a repository for flights

Input: $ARGUMENTS — the repository root, else the current one. Terms:

- Unit: a build unit, a part with its own toolchain and testing manual; a single-unit repository has one, named after the repository.
- `{project}`: the name the repository's flight directories already use under `$HOME/.local/state/pfm/flights/`, else the main checkout's directory name.
- `{scripts}`: the repository's agent-scripts directory, `.claude/scripts/` unless it keeps them elsewhere.
- `{tmp}`: the repository's temp-artifact directory per its rules, else `${TMPDIR:-/tmp}/{project}/`.

Re-runnable: every item ends `created`, `aligned` or `ok`. One that passes its tests and holds every behaviour below is `ok` and untouched; one that lacks a behaviour or holds a stale fact gets the smallest edit that fixes exactly that, never a rewrite; one that fails for a reason you do not own (a broken toolchain, a red suite) stays as found and is a gap.

Before the first write into the repository: a branch or worktree from the repository's git writer under its branch and worktree rules, never a protected branch directly. A path a guard denies opens by the steps its deny message carries.

## 1 — Map

The build units, their dependency order inside-out (the shared contract first), where the shared contract lives and what it generates, each unit's test runners, static checks, test homes, and the gates its full suite uses. Read the existing testing manuals and the speccer manual first; one message of `tracer` sub-agents, one per unit, answers the rest with each fact's path. A fact the map could not prove is a gap, never a guess.

## 2 — Speccer manual

Write or update `$HOME/.local/state/pfm/flights/{project}/speccer-manual.md`: static facts, each with its path: build units (each with its own testing manual) in inside-out dependency order, where the shared contract lives and what it generates, each unit's testing manual path and gates, test homes, hot files. Rewrite every line the map proved wrong, add the missing ones, leave the rest; the code wins over it; never a source of shapes. It lives outside the repository and takes no commit.

## 3 — Testing manual

Per unit, a testing manual where flights looks for it: `.claude/commands/{unit}-testing-manual.md`, or the repository's own path, recorded in the speccer manual. A unit without one gets a minimal manual under the eleven headings (Tiers, Where a test lives, Lanes and registries, Mock boundary, Environments and cleanup, Run commands, Concurrency, Gates and floors, Bug classes, Tricks and traps, What not to test) holding only facts the map proved, its top line marking it a starting point the owner completes. A section the map did not reach says `unmapped`, never `none`.

## 4 — Test command

`{scripts}/test-{unit}.sh ALL | <test file>...`:

- No argument, a flag or a pattern: exit 2 with the usage line. File paths only.
- `ALL` first prints its rerun reminder: after a fix, run only the files that failed last round.
- Output to `{tmp}/test-{unit}/{UTC timestamp}.log`; the repository's output filter, when it has one, reads that file, never a live pipe; print the log path; exit with the runner's code.
- Several runners: each named file goes to its runner; `ALL` runs every runner.
- After a green file selection only, two lines: `A file that failed in a wider run and now passes alone, with no change that explains it, fails alongside others: rerun the selection it failed in.` and `Green. When your work is done, run {scripts}/check-{unit}.sh <your task's files> once, then write your return.`

## 5 — Check command

`{scripts}/check-{unit}.sh <file>...`:

- No argument: exit 2 with the usage line `Name the files you changed.`; a path outside the unit: exit 2. Repository-relative and unit-relative paths both work.
- File-scoped checks (format check, lint) on the named files their tool applies to; unit-wide checks (type check, an architecture ratchet, any other static check the testing manual requires for an executor's change) whole.
- Every check runs even after one fails, one `PASS {check}` or `FAIL {check}` line each; output to `{tmp}/check-{unit}/{UTC timestamp}.log`.
- Exit 0 when all pass, 1 when any fails. No change to the unit's package manifest.

## 6 — Tests for both

In the repository's script-test home (none: beside the scripts, and a gap). Every tool a command calls is a stub executable on `PATH` that records its arguments and exits as told; assert every behaviour of steps 4 and 5. Steps 4 to 6 run per unit as one loop, tests first: watch them red against the missing or stale command, then build or align it until they pass.

## 7 — The manual names both

The test command as the only way to run the unit's tests; the check command as its static-check command, run once, last, with the task's `files`, and on a red fixed and run again. Align a line naming another way to run an affected test; the lander's gate commands stay.

## 8 — Engine copies

When the repository generates engine copies of its commands: `pfm codex build .`, `pfm opencode build .`.

## 9 — Verify

Every script test; then a live smoke of each test command on one real test file and each check command on one real source file of its unit, the lines reported as printed; every path in the speccer manual resolves. A red in what you built is fixed; a red that is the unit's own state is reported as printed and is a gap.

## 10 — Git

Commit through the repository's git writer on the branch set up before the first write; merging follows the repository's rules.

## Return

```text
| unit | test command | check command | manual | smoke |
| {unit} | {path} created|aligned|ok | {path} created|aligned|ok | {path} created|aligned|ok | {lines as printed} |
SPECCER MANUAL {path} created|updated|ok
ENGINE COPIES regenerated|none
COMMIT {sha} on {branch}|none
GAPS {each fact or item the owner must supply}|none
```
