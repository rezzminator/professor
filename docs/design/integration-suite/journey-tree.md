# The live tier as a journey tree

How [`/design:test-suite`](../../../templates/global/commands/design/test-suite.md) designs the live tier, and why: one tree of small owner steps, typed or clicked into the installed product on a disposable machine, branches running together, a failed step blocking only what grows from it. The unit and hermetic tiers and the validity law are unchanged; [laws.md](laws.md) and [validity.md](validity.md) hold their reasoning, read with the vocabulary of § What it replaced.

## Contents

- [The shape](#the-shape)
- [Reading the product](#reading-the-product)
- [The harness](#the-harness)
- [Profile and coverage](#profile-and-coverage)
- [The chat-function cells](#the-chat-function-cells)
- [Speed](#speed)
- [Retiring an old suite](#retiring-an-old-suite)
- [What it replaced](#what-it-replaced)

## The shape

```text
tree file (one) -> step files (one small file each) -> harness -> real terminal or browser -> product state + activity log
```

A step is what the owner does and sees, then what lies underneath. The owner's hands come first because the defects that keep coming back sit between the product's state and what the owner is shown: a row that exists but is drawn wrong, a window named one way and listed another, a screen that never returns after a reload. A test that reads only state passes over all of them; a test that reads only the screen cannot say which layer lied. So a step types the owner's keys, waits for the literal the owner would recognise, then reads the product's own listing, its database and the process table and asserts exact values. The read is the second check, never the replacement.

The tree is one file because it is the plan the owner approves and the report the run prints: the same names in the same nesting. A step is one small file because a builder, a reviewer and a failure all land on one step at a time.

Fork or chain is decided by one question: does this step need what that one left? A reload needs the chat it reloads, so it chains; a question asked in one chat and a rename in another only share a starting point, so they fork. A chain is the expensive shape: a red step blocks everything under it. Forking every step that can fork keeps one failure from hiding the rest, and the timeline reports how many real faults exist instead of the first one.

Branches run together because the owner's day is concurrent: several sessions, several windows, several accounts at once. The overlap is itself a test, and it is what lets the tree scale: one machine runs branches in parallel, several machines run subtrees, CI shards by path prefix, all without a redesign, since a step depends only on its context and the state handed to it, never on the order of its neighbours. Concurrency holds only if each branch owns what it creates: its own session or server, its own account, a run-unique name, reads filtered on those names, and a teardown that stops what it started even when it failed. Checks over the whole machine, and steps that change it all (install, restart, upgrade, uninstall), go at the joins, where nothing is mid-change.

Side effects are caught once, globally. Asserting every observable field on every step made each step heavy and each assertion a maintenance cost; the log gate (no undeclared error record while the step ran) and the consistency check (every live entity listed once, no orphan process, names equal on every surface) catch the same class of defect at a fraction of the cost.

Breadth grows from escapes. The defects users met while the suite passed name the paths the suite walks blind; each becomes an assertion on the step that walked past it, watched red on the unfixed code. The cross product of every dimension produces branches nobody walks and a suite too slow to run on every change, which is how the bugs come back.

A step the product or its fakes cannot play is left out and named. A skip, a placeholder node or a known-gap list reads as coverage in a summary; a named omission reads as what it is.

## Reading the product

The trunk goes where the defects cluster, so the escapes are read before anything is drawn: the version-control log's fixes and the issue tracker, grouped by area and entry. The areas that break again, and the entries they break through, are the trunk and the first branches. The entries themselves come from the built product's registries, because docs list what was meant and registries list what exists.

## The harness

The language's own test runner already walks a tree: nested subtests are the nesting, parallel subtests are the together node, a name filter runs one path with its ancestors, and its machine-readable output is the record. A runner of its own would re-implement all four and add a scheduler to maintain. The harness adds only what the runner lacks: the state value handed down by value (a branch can never see a sibling's), the block-descendants rule with its `BLOCKED` line, the terminal or browser driver, bounded polls, run-unique names, teardown, the timeline and the budget.

The terminal is a kernel pseudo-terminal running the owner's login shell, read through a screen emulator, because multiplexers and full-screen programs query the terminal and draw with cursor movement: a pipe sees neither. Its environment is a fresh login's, so no test variable reaches the product through it, and the product meets the terminal the owner has. Evidence is kept only on a failure: every screen as text and raw bytes, the log slice, the product's listing and the process table, enough to read the failure without rerunning it.

The machine is disposable so nothing needs jailing: the suite runs as the owner against the owner's real paths and service manager, which is where the escapes happened. Because it writes those paths, it refuses to start anywhere it cannot see it may spend.

## Profile and coverage

Every run, green or red, is profiled; the local run and CI's Linux journey job also measure their coverage of pfm's own source. pfm is built instrumented, the suite winds every pfm process down so each writes its counters (`windDown` in `pfm/journey/profile_test.go`), and `harness.Cover` merges them with `summary.md`, `steps.tsv` and `waits.tsv` into the run's artifacts. `infra/fence/journey/run.sh` copies the artifacts to `{checkout}/tmp/journey/{run id}/`, links `latest` to the newest and keeps the last ten runs; the folder is git-ignored. Coverage is a lower bound: a process killed by SIGKILL writes no counters. CI's journey job uploads the artifacts every time; the Linux run's include the raw counters (`covdata/`). The macOS journey job measures no coverage and sets no `GOCOVERDIR`: Go's coverage runtime names its temporary meta file by the clock's nanoseconds, macOS's clock ticks in microseconds, and two pfm starting in the same tick fail the emit into a step's output (`.github/workflows/ci.yml`, the macOS step of job `journey`). A run without `GOCOVERDIR` writes a summary whose coverage line says so.

## Running the suite on CI

`bash infra/fence/journey/run.sh --ci-linux|--ci-mac` (either or both) runs the same suite on CI instead of locally, through `infra/fence/journey/ci-run.sh`. It snapshots the disk (a throwaway index, parent HEAD, each plugin's on-disk commit) onto a temporary branch `ci-tmp/{branch}-{stamp}-{id}` on origin and dispatches `.github/workflows/ci.yml` with its `only` input naming that OS's journey and installer end-to-end entries: `--ci-mac` runs `journey (macos)`, `macos (unit)` and `macos (e2e)`; `--ci-linux` runs `journey (linux)` and `gate (e2e)`. It waits up to an hour, prints one line per job, saves each failed step's log and downloads the journey artifacts into `{checkout}/tmp/journey/ci/{run id}/`, and deletes every temporary branch on any exit (success, failure, timeout, interrupt). The caller's branch, HEAD, index, working tree and stash are untouched. The snapshot goes to origin through the leak-gated pre-push hook, so it is publication: run only on the owner's ask in that turn.

## The chat-function cells

The functions a chat has are played at both doors, the CLI and the MCP server, from inside a live Claude chat of the run (`pfm/journey/doors_cells_test.go`): the chat alone, a child working and then idle, a child ended and reopened, a chat ended before its first prompt, and the chats a chat starts, a Codex one among them. Each function present at both doors runs against the same chat so the two answers are read side by side. The `herdr-mesh` node (`pfm/journey/tree_test.go`) runs five forks side by side, each in a Herdr session of its own: `alone`, `host`, `started`, `unnamed` and `chat-new` (steps in `pfm/journey/step_herdr_mesh_test.go`).

Host-placement edges are played with the owner's own hands: with a tmux server stopped (`kill -STOP`) and no Herdr server running, `claude` typed in a plain terminal launches in tmux, and its `chat.placement` record names the server that did not answer in `caller_err` (step `tmuxStopped`, `pfm/journey/step_tmux_stopped_test.go`); exit 10 stays the answer when pfm cannot tell whether Herdr runs or a placement fails.

## Speed

A budget is measured, never priced. Paper estimates per assertion summed to a figure nobody checked, while the run itself grew past it. The trunk's first green run sets the tree's budget before a second branch is written, the runner fails any run over it with the measured and allowed seconds, and a trunk over its target re-cuts the tree before more is built. Short enough to run on every pull request is the target that matters: a slow suite runs on demand, and a suite run on demand is skipped under pressure.

## Retiring an old suite

An old suite is a list of claims, read and never copied: its fixtures and expected values encode what the old method needed, not what the owner sees. Each claim guarding an escape lands as an assertion on the step or test that walks its path; the retiring change names every other claim it drops and why. A per-assertion retirement ledger proves the bookkeeping, not the product, and is not kept.

## What it replaced

The owner ruled on 2026-11-11 to replace the lane method: lanes in a fixed sequence over one shared environment, a `need` prelude and checkpoint snapshot per lane, a landscape and map of every capability, a ledger row, a recorded mutation and a paper cost per assertion, a known-gap list, a retirement ledger, and a checks script whose rows policed those ledgers. Followed to the letter for this repository's own engine, it produced a serial chain of nine lanes priced at 110 minutes, of which seven never ran green; one red step reported as one failure and over a hundred blocked; checkpoint images filled the disk; and one process per assertion. Most of the effort went into proving the tests instead of writing them, and the defects kept escaping because nothing that slow ran on every change.

Still held, for their reasoning: [laws.md](laws.md) § Law 1 to Law 5, Law 10 to Law 13, Law 15 to Law 18, Law 33 and Law 34; [validity.md](validity.md) § Law 21 to Law 23, Law 25 to Law 31, Law 35 and § The refused shapes; [integration-suite.md](integration-suite.md) § 3. Doors, § 4. Research and § The threat model. Every other section of those files describes the replaced method, and where any of them differs from the command, the command governs.

Reading the held sections, the old words map to the new:

| Old | New |
| --- | --- |
| lane | branch |
| beat | a step's assertion |
| root | the machine |
| `need` prelude, checkpoint | the state handed down the tree |
| crossing, after-effect beat | the consistency check at the joins, and each branch's scoped reads |
| landscape row, map row, run ledger | the tree and the runner's own output |
| bite | watched red |
| `gaps.tsv`, `known`, `skip-because` | Not covered |
| `budgets.tsv`, `cost_ms` | the measured budget the runner enforces |
| `retired.tsv` | the retiring change's named claims |
