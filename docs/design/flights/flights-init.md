# /flights:init

`/flights:init` readies a repository for flights. It maps the repository's build units, writes the project map, and gives each unit the three things a flight leans on: a testing manual, a test command and a static-check command, each command with its tests. It runs once per repository and again whenever the repository's shape moves: it detects what exists, builds only what is missing, aligns what is stale, never rewrites a working file, and reports. It is concept-level: the units, toolchains, runners and paths are the repository's own, found by the map and never assumed by the command. Its first runs on real repositories are its proof: each run's frictions go back into this file and the command ([What run 1 measured](#what-run-1-measured)).

Decisions live in this file. The executable wording lives in [`templates/global/commands/flights/init.md`](../../../templates/global/commands/flights/init.md).

## Contents

- [Input](#input)
- [Terms](#terms)
- [The re-run law](#the-re-run-law)
- [I1 — Map](#i1--map)
- [I2 — The project map](#i2--the-project-map)
- [I3 — A testing manual per unit](#i3--a-testing-manual-per-unit)
- [I4 — The test command](#i4--the-test-command)
- [I5 — The check command](#i5--the-check-command)
- [I6 — Tests for both](#i6--tests-for-both)
- [I7 — The manual names both](#i7--the-manual-names-both)
- [I8 — Engine copies](#i8--engine-copies)
- [I9 — Verify](#i9--verify)
- [I10 — Git](#i10--git)
- [The return](#the-return)
- [What run 1 measured](#what-run-1-measured)
- [Not part of the design](#not-part-of-the-design)
- [Surfaces that stay in sync](#surfaces-that-stay-in-sync)

## Input

`/flights:init [repository root]`; absent, the current one. A user types it, or a model runs it to ready a repository before a flight; it carries no `disable-model-invocation`. `{project}` in the state path is the name the repository's flight directories already use under `$HOME/.local/state/pfm/flights/`, else the main checkout's directory name.

## Terms

- Build unit: a part of the repository with its own toolchain and its own testing manual. A single-unit repository has one.
- `{unit}`: the unit's name in every path the command writes: the suffix the repository's existing per-unit scripts already use, else its manuals', else, for a single unit, `{project}`, so a run inside a worktree still takes the repository's name, else the unit's directory name.
- The shared contract: what two units both build against (a schema, an interface definition, a wire format), with every output generated from it in its consumers.
- The agent-scripts directory: where the repository keeps the scripts its agents run; `.claude/scripts/` unless the repository already keeps them elsewhere.
- The temp-artifact directory: where the repository's rules put generated and temporary files; with no such rule, `${TMPDIR:-/tmp}/{project}/`.

## The re-run law

Every item the command owns ends in one of three states:

- `created`: it was absent and now exists.
- `aligned`: it existed and lacked a behaviour or held a fact this file requires; the edit adds or corrects exactly that and keeps every working line.
- `ok`: it existed and its tests and smoke pass with every behaviour this file requires; it is left untouched.

The tests of I6 are the detector: one test per rule of I4 and I5, so an existing command that passes them is `ok`, and one that fails a test gets the smallest edit that passes it. A file that existed and fails for a reason this command does not own (a broken toolchain, a red suite) stays as found and is a gap.

## I1 — Map

The command reads the project map when one exists and names the build units from it and the repository's layout, in dependency order inside-out, the shared contract first. `tracer` sub-agents, one per unit in one message, each read their unit's testing manual first and answer the same questions, each fact with its path:

1. Toolchain: each test runner, with its selector syntax, and each static check as the unit's installed tools run it; the command that installs its dependencies and an offline check that they are installed, or `no dependencies`.
2. Static checks: which take file arguments and which files each applies to, which run whole, which build, serve or reach beyond the machine, which rewrite the tree to compare, and which another check already runs.
3. Services: each check or test that needs a running service, how to probe it, how a script tells a worktree from the main checkout, and the command that starts the service in each.
4. Homes: where its tests live, the script-test home, and the gates its full suite uses.
5. Shared contract: where it lives or what the unit consumes from it, and the paths generated from it.
6. Hot files: existing files nearly every parallel change adds a line to (a registry, a routes table), as recent history shows — the foreman's meaning: the unit that owns a hot file adds every line to it, so two children never edit it at once.
7. Smoke: one real test file and one source file its checks pass, both needing no running service.

The questions are fixed so every run briefs its tracers the same way and the main context never reads every unit's manual whole. A fact the map could not prove stays unproven: it is a gap, never a guess.

## I2 — The project map

Write or update `$HOME/.local/state/pfm/flights/{project}/project-map.md`, the foreman's per-project map ([`flights-foreman`](flights-foreman.md)), with the map's static facts, each with its path: the build units (each with its own testing manual) and their inside-out dependency order, where the shared contract lives and what it generates, each unit's testing manual path and gates, test homes, hot files. An update rewrites every line the map proved wrong, adds the missing ones and leaves the rest. The code wins over it, and it is never a source of shapes: a shape is always pasted from output the run printed. The map's runners and static checks go to the unit's testing manual (I3, I7), never into this list. It sits in the flights state root, outside the repository, so it takes no commit.

## I3 — A testing manual per unit

Each unit needs a [testing manual](testing-manual.md) where flights looks for it: `.claude/commands/{unit}-testing-manual.md` by default, or the repository's own path, which the project map records. A unit without one gets a minimal manual under the eleven fixed headings, holding only the facts the map proved (run commands, test homes, the two commands below) and marked at its top as a starting point the owner completes. A section the map did not reach says `unmapped`, kept apart from `none`, which claims the section does not apply: an unread fact never renders as an absent one.

## I4 — The test command

`test-{unit}.sh ALL | <test file>...` in the agent-scripts directory, the one way an agent runs the unit's tests:

- A selection is required: no argument, a flag, an empty argument or a pattern exits 2 with its usage line. File paths only; a runner's own selector suffix after a file (a test id) passes through byte-for-byte.
- Every path, here and in I5, goes through the repository's one path resolver, `unit-path.sh` in the agent-scripts directory; a copy per script drifts.
  - Repository-, unit-, cwd-relative and absolute spellings all resolve. A path that resolves to two existing files exits 2 naming both, never picking one silently.
  - The full resolved path decides whether it is outside the unit (exit 2), never its first segment, which lets a repository-root file shadowed by a same-named unit directory pass as a unit file.
  - A file deleted in the change (tracked at `HEAD`) is dropped silently, distinguishable from a typo; any other path that is no file exits 2 naming it in the test command, and in the check command prints `FAIL path {arg} (no such file in {unit})` and the run is not green: an error never renders as absence.
- A selection that drops to empty exits 2 naming the dropped files and never calls the runner, which would otherwise run the whole suite or print green over nothing.
- `ALL` prints its rerun reminder first: the whole suite runs, and after a fix only the files that failed last round rerun.
- Full output goes to a log under the temp-artifact directory, `test-{unit}/{UTC timestamp}-{pid}.log`, so two runs in one second never share one; the repository's output filter, when it has one, reads that file (never a live pipe); the command prints the log path.
- A unit with several runners routes each named file to its runner; `ALL` runs every runner. The command exits with the first non-zero runner code, else 0, so a later green runner never hides an earlier red one.
- After a green file selection, never after `ALL`, two more lines: `A file that failed in a wider run and now passes alone, with no change that explains it, fails alongside others: rerun the selection it failed in.` and `Green. When your work is done, run {agent-scripts directory}/check-{unit}.sh <your task's files> once, then write your return.`

An existing test command is aligned, never rewritten.

## I5 — The check command

`check-{unit}.sh <file>...` in the agent-scripts directory, the static-check command a builder runs once, last, with the files it changed:

- No argument, a flag or an empty argument exits 2 with the usage line `Name the files you changed.`; paths go through I4's resolver.
- It calls the unit's installed tools only, never a runner that fetches a tool: a fetched tool is unpinned, and its verdict is not the unit's. A unit the map's offline check finds not installed prints only the line `FAIL install ({unit} has no installed dependencies — run: {install command})`, runs no check and exits 1; a unit with `no dependencies` never prints it.
- File-scoped checks (format check, lint) run on the named files their tool applies to; one that no named file applies to prints exactly `SKIP {check} (no named file it applies to)`, one wording in every unit, never `PASS`, since it checked nothing. Unit-wide checks (type check, an architecture ratchet, any other static check the testing manual requires for an executor's change) run whole.
- A check that builds, serves or reaches beyond the machine stays out of the check command; the lander's gate does not run it either, so the return names it under GAPS as a landing check the owner places. A check another listed check already runs is listed once.
- A check that needs a running service probes it only after the check fails, never on a green run: the probe decorates that `FAIL` line with the missing service and the command that starts it in the current checkout, since a worktree may start it differently from the main checkout. A probe that itself errors says so and why, never reporting the service as up, so a down service never reads as a code red. A probe never interpolates a config value into a shell string.
- A check that mutates the tree (regenerate-and-compare) serialises per checkout, because parallel foremen share one worktree: a lock keyed to the checkout path, a bounded wait that ends in a `FAIL {check}` line naming the holder, freed when its holder dies; the check leaves the tree as it found it.
- Every check runs even after one fails, each printing one `PASS {check}`, `FAIL {check}` or `SKIP {check}` line; the full output goes to `check-{unit}/{UTC timestamp}-{pid}.log` under the temp-artifact directory, and the command prints its path.
- Exit 1 when any line is `FAIL`, else 0.
- No change to the unit's package manifest.

## I6 — Tests for both

Each command gets tests in the repository's script-test home (the map finds it; with none, beside the scripts, and the missing home is a gap), with fixtures in a fake root, never the host checkout: a test green only where it was written is red after the merge. Every tool a command calls (runner, formatter, linter, type checker, environment wrapper, service probe) is a stub executable in that root, placed where the command finds it, that records its arguments and exits as the test tells it, so the tests run in seconds with no toolchain and never touch the unit's real tools. There is one test per rule of I4 and I5, the fetching-runner, lock and manifest rules included. Steps I4 to I6 run per unit as one loop, tests first: each assertion that something is present is watched failing against the missing or stale command, then the command is built or aligned until they pass. An assertion that something is absent (no green lines after `ALL`) already passes against a missing command, so it is exempt from the watch.

## I7 — The manual names both

The unit's testing manual names the test command as the only way to run its tests and the check command as its static-check command: run once, last, with the task's `files`; on a red, fix and run the same command again. The gate commands the [`flights-lander`](flights-lander.md) runs stay as they are. A manual line naming another way to run an affected test is aligned to the test command.

## I8 — Engine copies

A repository keeps engine copies of its commands when `.codex/` or `.opencode/` sits at its root; each is regenerated after the manuals change (`pfm codex build .`, `pfm opencode build .`), so every engine reads the same manual.

## I9 — Verify

Every script test runs. Before the smoke, a unit the map's offline check finds not installed is installed by its install command, so a re-run pays no install; a file the install changes (a lockfile) is restored and named under GAPS, never committed, and a unit still not installed is a gap. Then a live smoke: each test command on the map's smoke test file and each check command on the map's smoke source file, both needing no running service. Every path the project map names resolves. A red that is the unit's own state (a failing test on the base branch, a service down) is reported as printed and is a gap; a red in what the command built is fixed before the return.

## I10 — Git

Every write into the repository goes through the repository's git writer and its branch and worktree rules: the branch or worktree is in place before the first write, and the work is committed there, never on a protected branch directly. The commit holds exactly the paths the run created or changed by explicit pathspec, engine copies only where the repository tracks them: an ignored copy is regenerated and never added. Every guard the run's writes will meet is opened, by the steps its deny message carries, before any write is delegated: an opening keyed to the session covers the sub-agents it sends, and a sub-agent sent first is denied. Merging follows the repository's own rules.

## The return

The smoke cell holds each command's verdict lines and exit code on one line, a `|` inside written `\|`, or `not run ({why})`, so a unit never smoked never reads as one that passed; the full output stays in its log.

```text
| unit | test command | check command | manual | smoke |
| {unit} | {path} created|aligned|ok | {path} created|aligned|ok | {path} created|aligned|ok | test exit {code} · check {its PASS, FAIL and SKIP lines} exit {code} |
RESOLVER {path} created|aligned|ok
PROJECT MAP {path} created|aligned|ok
ENGINE COPIES regenerated|none
COMMIT {sha} on {branch}|none
GAPS {each fact or item the owner must supply}|none
```

## What run 1 measured

Run 1 on a six-unit repository, 2026-10-05, and the merge-gating review of the scripts it built are the measured case for these rules:

| Rule | What run 1 hit |
| --- | --- |
| I1's fixed questions | The map named no questions: the run wrote its own per unit, and reading six testing manuals whole in the main context was its largest cost |
| `{unit}` from the existing suffix | The existing per-unit scripts used short suffixes, not directory names: one unit, two names |
| Hot files still exist | Undefined, the history listed paths since deleted |
| `SKIP` line (I5) | A file-scoped check that no named file applied to printed `PASS` over nothing checked |
| Service probe (I5) | A check that needs a running service read as a code red with the service down; a worktree starts the service with a different command than the main checkout |
| Installed tools, `FAIL install` (I5, I9) | In a fresh worktree with no dependencies installed, a fetching runner downloaded an unpinned tool and printed a false `PASS` |
| Landing checks, listed once (I5) | "Any other static check the testing manual requires" forced a ruling per heavy gate (build, end-to-end, checks nested in another) |
| Path forms (I4) | The test command took unit-relative paths only; a flight's repository-relative task path failed with exit 1 |
| Presence watched failing (I6) | "Watch them red" could not hold for an absence assertion, which passes against a missing command |
| Smoke files need no running service (I1, I9) | A smoke test file that needs a running service reds on the service, not on the command under proof |
| Engine copies detected (I8) | The command named the builds but not how to tell that a repository keeps engine copies |
| Commit scope, guards before delegating (I10) | The commit's scope was unstated; a sub-agent sent to write before the session opened the guard was denied |
| Smoke cell (the return) | Whole printed output in the smoke cell made the return unreadable |
| One path resolver (I4, I5) | Per-script copies drifted: one compared only the first path segment, so a repository-root file shadowed by a same-named unit directory passed as a unit file |
| Fixtures in a fake root (I6) | Script tests read the host checkout: green only where they were written |
| Lock on a tree-mutating check (I5) | A regenerate-and-compare check races when parallel foremen share one worktree |
| Probe only after a red (I5) | The review found a probe on green runs, where there is no red to explain, and config values interpolated into a shell string |
| One `SKIP` wording (I5) | The skip line's wording differed between units |

## Not part of the design

| Left out | Reason |
| --- | --- |
| Running a flight, the full suite or a gate | The [`flights-lander`](flights-lander.md)'s; the command proves its own scripts and one live smoke each |
| Writing project test law the map did not prove | The owner's; an unproven section stays `unmapped` |
| Rewriting a working file into the command's shape | Align, never rewrite: a working line is the owner's decision |
| A package-manifest script entry | The scripts are the entry point; the manifest stays the toolchain's |
| A test-output hook | Hooks are per engine and only remove text; the output filter lives in the test command, which every engine runs |
| Merging the branch | The repository's own rules |

## Surfaces that stay in sync

| Surface | File | Holds |
| --- | --- | --- |
| The command | `templates/global/commands/flights/init.md` | The ten steps and the return |
| The builder | [`flights-foreman`](flights-foreman.md) | The project map's file and content, read at intake; hot files in its meaning |
| The testing manual | [testing-manual.md](testing-manual.md) | The default home and the eleven headings |
| The builders | [`flights-foreman`](flights-foreman.md), [mechanical](mechanical-executor.md), `templates/global/agents/flights-foreman.md`, `flights-mechanical-executor.md` | The check command run once, last, with the files changed |
| The lander | [`flights-lander`](flights-lander.md), `templates/global/agents/flights-lander.md` | The gate commands this command leaves as they are |
| The family | [`flights.md`](flights.md) | The command's row in the family table |
