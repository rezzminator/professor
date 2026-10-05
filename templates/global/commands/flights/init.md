---
name: flights:init
description: 'Readies a repository for flights — /flights:init [root]: maps its build units inside-out, writes the speccer manual, gives each unit a testing manual, test-{unit}.sh and check-{unit}.sh with their tests; re-runnable. here → /flights:spec. Returns a row per unit, the speccer-manual path, gaps.'
argument-hint: [repository root]
---

# Init — ready a repository for flights

Input: $ARGUMENTS — the repository root, else the current one. Terms:

- Unit: a build unit, a part with its own toolchain and testing manual; a single-unit repository has one.
- `{unit}`: the suffix the repository's existing per-unit scripts already use, else its manuals', else, for a single unit, `{project}`, else the unit's directory name.
- `{project}`: the name the repository's flight directories already use under `$HOME/.local/state/pfm/flights/`, else the main checkout's directory name.
- `{scripts}`: the repository's agent-scripts directory, `.claude/scripts/` unless it keeps them elsewhere.
- `{tmp}`: the repository's temp-artifact directory per its rules, else `${TMPDIR:-/tmp}/{project}/`.

Re-runnable: every item ends `created`, `aligned` or `ok`. One that passes its tests and smoke and holds every behaviour below is `ok` and untouched; one that lacks a behaviour or holds a stale fact gets the smallest edit that fixes exactly that, never a rewrite; one that fails for a reason you do not own (a broken toolchain, a red suite) stays as found and is a gap.

Before the first write into the repository: a branch or worktree from the repository's git writer under its branch and worktree rules, never a protected branch directly. Every guard the run's writes will meet opens, by the steps its deny message carries, before any write is delegated: an opening keyed to the session covers the sub-agents it sends.

## 1 — Map

Read the speccer manual when one exists; name the units from it and the repository's layout, in order inside-out, the shared contract first. Then one message of `tracer` sub-agents, one per unit, each reading its unit's testing manual first, answers the same questions, each fact with its path:

1. Toolchain: each test runner, with its selector syntax, and each static check as the unit's installed tools run it; the command that installs its dependencies and an offline check that they are installed, or `no dependencies`.
2. Static checks: which take file arguments and which files each applies to, which run whole, which build, serve or reach beyond the machine, which rewrite the tree to compare, and which another check already runs.
3. Services: each check or test that needs a running service, how to probe it, how a script tells a worktree from the main checkout, and the command that starts the service in each.
4. Homes: where its tests live, the script-test home, and the gates its full suite uses.
5. Shared contract: where it lives or what the unit consumes from it, and the paths generated from it.
6. Hot files: existing files nearly every parallel change adds a line to (a registry, a routes table), as recent history shows.
7. Smoke: one real test file and one source file its checks pass, both needing no running service.

A fact the map could not prove is a gap, never a guess.

## 2 — Speccer manual

Write or update `$HOME/.local/state/pfm/flights/{project}/speccer-manual.md`: static facts, each with its path: build units (each with its own testing manual) in inside-out dependency order, where the shared contract lives and what it generates, each unit's testing manual path and gates, test homes, hot files; runners and checks belong to the testing manual. Rewrite every line the map proved wrong, add the missing ones, leave the rest; the code wins over it; never a source of shapes. It lives outside the repository and takes no commit.

## 3 — Testing manual

Per unit, a testing manual where flights looks for it: `.claude/commands/{unit}-testing-manual.md`, or the repository's own path, recorded in the speccer manual. A unit without one gets a minimal manual under the eleven headings (Tiers, Where a test lives, Lanes and registries, Mock boundary, Environments and cleanup, Run commands, Concurrency, Gates and floors, Bug classes, Tricks and traps, What not to test) holding only facts the map proved, its top line marking it a starting point the owner completes. A section the map did not reach says `unmapped`, never `none`.

## 4 — Test command

`{scripts}/test-{unit}.sh ALL | <test file>...`:

- No argument, a flag, an empty argument or a pattern: exit 2 with the usage line. File paths only; a runner's selector suffix after a file (a test id) passes through byte-for-byte.
- Every path, here and in step 5, goes through the repository's one path resolver, `{scripts}/unit-path.sh`: repository-, unit-, cwd-relative and absolute spellings resolve; the full resolved path, never its first segment, decides outside the unit, and outside or resolving to two existing files exits 2 naming it. A file deleted in the change (tracked at `HEAD`) drops silently; any other path that is no file exits 2 naming it here, and in step 5 prints `FAIL path {arg} (no such file in {unit})`.
- A selection that drops to empty exits 2 naming the dropped files, the runner never called.
- `ALL` first prints its rerun reminder: after a fix, run only the files that failed last round.
- Output to `{tmp}/test-{unit}/{UTC timestamp}-{pid}.log`; the repository's output filter, when it has one, reads that file, never a live pipe; print the log path. Several runners: each named file goes to its runner, `ALL` runs every runner; exit with the first non-zero runner code, else 0.
- After a green file selection only, two lines: `A file that failed in a wider run and now passes alone, with no change that explains it, fails alongside others: rerun the selection it failed in.` and `Green. When your work is done, run {scripts}/check-{unit}.sh <your task's files> once, then write your return.`

## 5 — Check command

`{scripts}/check-{unit}.sh <file>...`:

- No argument, a flag or an empty argument: exit 2 with the usage line `Name the files you changed.`
- The unit's installed tools only, never a runner that fetches a tool. Not installed per the map's offline check: only the line `FAIL install ({unit} has no installed dependencies — run: {install command})`, no check run, exit 1; a unit with `no dependencies` never prints it.
- File-scoped checks (format check, lint) on the named files their tool applies to; one that no named file applies to prints exactly `SKIP {check} (no named file it applies to)` in every unit, never PASS. Unit-wide checks (type check, an architecture ratchet, any other static check the testing manual requires for an executor's change) whole. A check that builds, serves or reaches beyond the machine stays out, named under GAPS as a landing check the owner places; a check another listed check already runs is listed once.
- A check that needs a running service: only after it fails, a probe names on its FAIL line the missing service and the command that starts it in the current checkout, or that the probe itself failed and why, never the service as up; config values never interpolate into a shell string.
- A check that rewrites the tree to compare (regenerate-and-compare) holds a lock keyed to the checkout path, waits a bounded time then prints `FAIL {check}` naming the holder, is freed when its holder dies, and leaves the tree as it found it.
- Every check runs even after one fails, one `PASS {check}`, `FAIL {check}` or `SKIP {check}` line each; output to `{tmp}/check-{unit}/{UTC timestamp}-{pid}.log`, its path printed.
- Exit 1 when any line is FAIL, else 0. No change to the unit's package manifest.

## 6 — Tests for both

In the repository's script-test home (none: beside the scripts, and a gap), with fixtures in a fake root, never the host checkout. Every tool and service probe a command calls is a stub executable in that root where the command finds it, recording its arguments and exiting as told; one test per rule of steps 4 and 5. Steps 4 to 6 run per unit as one loop, tests first: watch each assertion that something is present fail against the missing or stale command (one that something is absent is exempt), then build or align it until they pass.

## 7 — The manual names both

The test command as the only way to run the unit's tests; the check command as its static-check command, run once, last, with the task's `files`, and on a red fixed and run again. Align a line naming another way to run an affected test; the lander's gate commands stay.

## 8 — Engine copies

When `.codex/` or `.opencode/` sits at the root, regenerate that engine's copies: `pfm codex build .`, `pfm opencode build .`.

## 9 — Verify

Every script test. Then a unit not installed per the map's offline check is installed by its install command; a file the install changes is restored and named under GAPS, and a unit still not installed is a gap. A live smoke runs each test command on the map's smoke test file and each check command on its smoke source file; every path in the speccer manual resolves. A red in what you built is fixed; a red that is the unit's own state (a failing test on the base branch, a service down) is reported as printed and is a gap.

## 10 — Git

Commit exactly the paths this run created or changed, engine copies only where the repository tracks them, by explicit pathspec, through the repository's git writer on the branch set up before the first write; merging follows the repository's rules.

## Return

The smoke cell holds each command's verdict lines and exit code on one line, a `|` inside written `\|`, or `not run ({why})`; the full output stays in its log.

```text
| unit | test command | check command | manual | smoke |
| {unit} | {path} created|aligned|ok | {path} created|aligned|ok | {path} created|aligned|ok | test exit {code} · check {its PASS, FAIL and SKIP lines} exit {code} |
RESOLVER {path} created|aligned|ok
SPECCER MANUAL {path} created|aligned|ok
ENGINE COPIES regenerated|none
COMMIT {sha} on {branch}|none
GAPS {each fact or item the owner must supply}|none
```
