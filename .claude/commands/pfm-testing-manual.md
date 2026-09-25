---
name: pfm-testing-manual
description: The testing law of pfm (the Go fleet engine) — tiers, where a test lives, lanes and registries, mock boundary, environments, run commands, concurrency, gates and floors, bug classes, traps, what not to test. Read by flights-speccer at intake, by every flight or general executor before its first test, by flights-lander whole; `/pfm-testing-manual` opens it for a human. Keep it true in the same change that alters how pfm is tested.
---

# pfm testing manual

Fixed headings, fixed order. Detail lives in `pfm/CLAUDE.md` § Testing Rules, `pfm/TESTPLAN.md` and `docs/dev/testing/`; this file states what a change owes.

## Tiers

- Unit: a package test beside its package, no real tmux server, no real engine process.
- `JAIL`, `JAIL+tmux`, `JAIL+sh`, `LIVE-READ`, `REAL-SESSION`: `pfm/TESTPLAN.md` § Legend. The boundary that decides: whether the test needs a real tmux server (a scratch socket inside the jail's `TMUX_TMPDIR`) or a genuine `claude`/`codex` process (`REAL-SESSION`, scheduled deliberately, named in `TESTPLAN.md` § "Flows that CANNOT be jailed").
- e2e: the tagged suite under `pfm/cmd/pfm/` and the fence lanes; both run only inside the fence.

## Where a test lives

- `<file>_test.go` beside the source file, same package; end-to-end jail tests under `pfm/cmd/pfm/`. Extend the owning test file; add one only when none exists.
- A single-implementation rule (naming precedence, the kill ratchet, row classification, run-string synthesis) gets a table-driven test; an emitted shell line gets a golden file.

## Lanes and registries

- A new command or MCP tool lands with its beat (`infra/fence/lanes/beats.md` and `infra/fence/lanes/<lane>.sh`) and its map row (`infra/fence/lanes/map.tsv`, `name · lane · beat`) in the same commit; a fleet capability lands with its beat; the recipe is `docs/dev/testing/lanes.md` § Extend it.
- callmeter: the store is real SQLite under `t.TempDir()`, never a mock; hook payload fixtures are the captured Claude Code payloads (`pfm/internal/hookentry/testdata/callmeter/`) with every path rewritten to `/tmp/demo-proj/…`; the Python parser tests run the fence's real `python3`.
- One editor per flight: `infra/fence/lanes/lib.sh`, `run.sh`, `map.tsv`, `beats.md`, `known-gaps.yml`, `pfm/scripts/known-skips.tsv`.

## Mock boundary

- Always real: the filesystem under the jail, SQLite, tmux on a scratch socket. Never real in a test: a live `cc-*` / `cx-*` socket, the real `/tmp/cc-sid`, a provider account. Engine processes are played by `internal/mockengine`.

## Environments and cleanup

- Every test runs under `internal/testjail` (`testjail.Run` in `TestMain`; `ShortRoot`, `Fleet`, `InstalledHome`, `CleanHome` build the homes). The `PFM_*` overrides in `pfm/internal/paths/paths.go` are the only knobs; `TMUX_TMPDIR = t.TempDir()`.
- Code flights build and test inside the fence: `.claude/scripts/dev.sh iso`. The host's `~/.local/bin` and the real `$HOME` are never test targets.
- Live traffic (a real page, the harvester's browser rung, a walled or lazy-loaded site) runs in the real-simulation fence: `.claude/scripts/dev.sh iso sim '<command>'` — Google Chrome (headless only, no display) and `pfm` installed from the worktree with the browser rung on; the harvester state persists per worktree. Its first line is `sim: chrome=… browser-rung=on` or `sim: BOOTSTRAP-FAILED — <step>`. A live check proves behavior; the regression test is still a fixture-driven unit test.

## Run commands

- Affected, an executor's only run (flight or general): `.claude/scripts/dev.sh iso run "go -C pfm test ./internal/<package>/ -run <Test> -count=1"` in the fence, `go -C pfm test ./internal/<package>/ -run <Test>` on the host — timeout 600 s.
- Full, the flight gate's run and never an executor's: `.claude/scripts/dev.sh test pfm` on the host, `.claude/scripts/dev.sh iso test pfm` in the fence — about 13 minutes, timeout 600 s, background past that.
- Static: `.claude/scripts/dev.sh verify pfm` (vet, fmt-check, lint-new, the architecture ratchet, the gate scripts' self-tests). Lanes: `infra/fence/lanes/run.sh`; the map gate `infra/fence/lanes/check-map.sh --pfm <a pfm built from this tree>`.

## Concurrency

- Package tests run in parallel; isolation is the jail, one temp root per test. Timing budgets per package and per suite: `docs/dev/testing/timing.md`; an unbudgeted package fails.

## Gates and floors

- Coverage `pfm/.testcoverage.yml`: total 77, package 52 (`make -C pfm cover`).
- `make -C pfm gate` is ready-to-merge: fmt-check, lint-new, vet, arch, test, iso.
- Every skip is a row in `pfm/scripts/known-skips.tsv` (`pfm/scripts/skip-check.sh`); an unlisted skip fails.

## Bug classes

- none beyond the gate's own check ids.

## Tricks and traps

- A long `TMUX_TMPDIR` fails with "File name too long" and reads as a tmux bug: keep scratch socket paths short (`testjail.ShortRoot`).
- `codex` is a shell function on a developer host: a test or script calls `command codex`.
- A probe that launches an engine closes stdin (`</dev/null`) or it hangs.
- The e2e harness refuses to run without `PFM_DEV_FENCE=1`: that red on a host is the fence law, not a defect.
- `internal/harvest` (a `/private` symlink) and `internal/hookentry` (socket path length) are red on a macOS host and green in the fence.

## What not to test

- A model's words: a beat asserts from pfm's own report or the pane.
- A value one run printed about its data (a count, an id).
- A removed command, flag or tool takes its tests, its beat and its map row with it, never inverted into an absence assertion.
