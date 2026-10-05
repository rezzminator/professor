# /flights:init

`/flights:init` readies a repository for flights. It maps the repository's build units, writes the speccer manual, and gives each unit the three things a flight leans on: a testing manual, a test command and a static-check command, each command with its tests. It runs once per repository and again whenever the repository's shape moves: it detects what exists, builds only what is missing, aligns what is stale, never rewrites a working file, and reports. It is concept-level: the units, toolchains, runners and paths are the repository's own, found by the map and never assumed by the command. Its first runs on real repositories are its proof: each run's frictions go back into this file and the command.

Decisions live in this file. The executable wording lives in [`templates/global/commands/flights/init.md`](../../../templates/global/commands/flights/init.md).

## Contents

- [Input](#input)
- [Terms](#terms)
- [The re-run law](#the-re-run-law)
- [I1 — Map](#i1--map)
- [I2 — The speccer manual](#i2--the-speccer-manual)
- [I3 — A testing manual per unit](#i3--a-testing-manual-per-unit)
- [I4 — The test command](#i4--the-test-command)
- [I5 — The check command](#i5--the-check-command)
- [I6 — Tests for both](#i6--tests-for-both)
- [I7 — The manual names both](#i7--the-manual-names-both)
- [I8 — Engine copies](#i8--engine-copies)
- [I9 — Verify](#i9--verify)
- [I10 — Git](#i10--git)
- [The return](#the-return)
- [Not part of the design](#not-part-of-the-design)
- [Surfaces that stay in sync](#surfaces-that-stay-in-sync)

## Input

`/flights:init [repository root]`; absent, the current one. A user types it, or a model runs it to ready a repository before a flight; it carries no `disable-model-invocation`. `{project}` in the state path is the name the repository's flight directories already use under `$HOME/.local/state/pfm/flights/`, else the main checkout's directory name.

## Terms

- Build unit: a part of the repository with its own toolchain and its own testing manual. A single-unit repository has one, and its commands take the repository's name.
- The shared contract: what two units both build against (a schema, an interface definition, a wire format), with every output generated from it in its consumers.
- The agent-scripts directory: where the repository keeps the scripts its agents run; `.claude/scripts/` unless the repository already keeps them elsewhere.
- The temp-artifact directory: where the repository's rules put generated and temporary files; with no such rule, `${TMPDIR:-/tmp}/{project}/`.

## The re-run law

Every item the command owns ends in one of three states:

- `created`: it was absent and now exists.
- `aligned`: it existed and lacked a behaviour or held a fact this file requires; the edit adds or corrects exactly that and keeps every working line.
- `ok`: it existed and its tests and smoke pass with every behaviour this file requires; it is left untouched.

The tests of I6 are the detector: they state every behaviour I4 and I5 require, so an existing command that passes them is `ok`, and one that fails a test gets the smallest edit that passes it. A file that existed and fails for a reason this command does not own (a broken toolchain, a red suite) stays as found and is a gap.

## I1 — Map

The command maps the repository: its build units, their dependency order inside-out (the shared contract first), where the shared contract lives and what it generates, each unit's test runner or runners, static checks, test homes, and the gates its full suite uses. The existing testing manuals and the speccer manual are read first; `tracer` sub-agents, one per unit in one message, answer the rest with each fact's path. A fact the map could not prove stays unproven: it is a gap, never a guess.

## I2 — The speccer manual

Write or update `$HOME/.local/state/pfm/flights/{project}/speccer-manual.md`, the speccer's own per-project manual ([`flights-speccer`](flights-speccer.md)), with the map's static facts, each with its path: the build units (each with its own testing manual) and their inside-out dependency order, where the shared contract lives and what it generates, each unit's testing manual path and gates, test homes, hot files. An update rewrites every line the map proved wrong, adds the missing ones and leaves the rest. The code wins over it, and it is never a source of shapes: a shape is always pasted from output the run printed. The map's runners and static checks go to the unit's testing manual (I3, I7), never into this list. It sits in the flights state root, outside the repository, so it takes no commit.

## I3 — A testing manual per unit

Each unit needs a [testing manual](testing-manual.md) where flights looks for it: `.claude/commands/{unit}-testing-manual.md` by default, or the repository's own path, which the speccer manual records. A unit without one gets a minimal manual under the eleven fixed headings, holding only the facts the map proved (run commands, test homes, the two commands below) and marked at its top as a starting point the owner completes. A section the map did not reach says `unmapped`, kept apart from `none`, which claims the section does not apply: an unread fact never renders as an absent one.

## I4 — The test command

`test-{unit}.sh ALL | <test file>...` in the agent-scripts directory, the one way an agent runs the unit's tests:

- A selection is required: no argument, a flag or a pattern exits 2 with its usage line. File paths only.
- `ALL` prints its rerun reminder first: the whole suite runs, and after a fix only the files that failed last round rerun.
- Full output goes to a log under the temp-artifact directory, `test-{unit}/{UTC timestamp}.log`; the repository's output filter, when it has one, reads that file (never a live pipe); the command prints the log path and exits with the runner's code.
- A unit with several runners routes each named file to its runner; `ALL` runs every runner.
- After a green file selection, never after `ALL`, two more lines: `A file that failed in a wider run and now passes alone, with no change that explains it, fails alongside others: rerun the selection it failed in.` and `Green. When your work is done, run {agent-scripts directory}/check-{unit}.sh <your task's files> once, then write your return.`

An existing test command is aligned, never rewritten.

## I5 — The check command

`check-{unit}.sh <file>...` in the agent-scripts directory, the static-check command an executor runs once, last, with its task's `files`:

- No argument exits 2 with the usage line `Name the files you changed.`; a path outside the unit exits 2.
- Repository-relative and unit-relative paths are both accepted.
- File-scoped checks (format check, lint) run on the named files their tool applies to; unit-wide checks (type check, an architecture ratchet, any other static check the testing manual requires for an executor's change) run whole.
- Every check runs even after one fails, each printing one `PASS {check}` or `FAIL {check}` line; the full output goes to `check-{unit}/{UTC timestamp}.log` under the temp-artifact directory.
- Exit 0 when every check passes, 1 when any fails.
- No change to the unit's package manifest.

## I6 — Tests for both

Each command gets tests in the repository's script-test home (the map finds it; with none, beside the scripts, and the missing home is a gap). Every tool a command calls (runner, formatter, linter, type checker, environment wrapper) is a stub executable on `PATH` that records its arguments and exits as the test tells it, so the tests run in seconds with no toolchain and assert every behaviour of I4 and I5: the usage exits, the path guard, the arguments forwarded, one line per check, every check run after a red, the exit codes, the reminder before `ALL`, the two green lines after a green file selection and never otherwise, the log path. Steps I4 to I6 run per unit as one loop, tests first: the tests are written and watched red against the missing or stale command, then the command is built or aligned until they pass.

## I7 — The manual names both

The unit's testing manual names the test command as the only way to run its tests and the check command as its static-check command: run once, last, with the task's `files`; on a red, fix and run the same command again. The gate commands the [`flights-lander`](flights-lander.md) runs stay as they are. A manual line naming another way to run an affected test is aligned to the test command.

## I8 — Engine copies

When the repository generates engine copies of its commands (Codex and OpenCode, through `pfm codex build .` and `pfm opencode build .`), they are regenerated after the manuals change, so every engine reads the same manual.

## I9 — Verify

Every script test runs; then a live smoke: each test command on one real test file and each check command on one real source file of its unit, the printed lines reported as printed. Every path the speccer manual names resolves. A red that is the unit's own state (a failing test on the base branch) is reported as printed and is a gap; a red in what the command built is fixed before the return.

## I10 — Git

Every write into the repository goes through the repository's git writer and its branch and worktree rules: the branch or worktree is in place before the first write, and the work is committed there, never on a protected branch directly. Merging follows the repository's own rules. A path a guard denies is opened by the steps its deny message carries.

## The return

```text
| unit | test command | check command | manual | smoke |
| {unit} | {path} created|aligned|ok | {path} created|aligned|ok | {path} created|aligned|ok | {lines as printed} |
SPECCER MANUAL {path} created|updated|ok
ENGINE COPIES regenerated|none
COMMIT {sha} on {branch}|none
GAPS {each fact or item the owner must supply}|none
```

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
| The speccer | [`flights-speccer`](flights-speccer.md) | The speccer manual's file and content, read at intake |
| The testing manual | [testing-manual.md](testing-manual.md) | The default home and the eleven headings |
| The executors | [flight executors](flights-executors.md) | The check command run once, last, with the task's `files` |
| The lander | [`flights-lander`](flights-lander.md) | The gate commands this command leaves as they are |
| The family | [`flights.md`](flights.md) | The command's row in the family table |
