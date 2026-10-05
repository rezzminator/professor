---
name: pfm-testing-manual
description: The testing law of pfm (the Go fleet engine) — tiers, where a test lives, lanes and registries, mock boundary, environments, run commands, concurrency, gates and floors, bug classes, traps, what not to test. Read by flights-speccer at intake, by every flight or general executor before its first test, by flights-lander whole; `/pfm-testing-manual` opens it for a human. Keep it true in the same change that alters how pfm is tested.
---

# pfm testing manual

Fixed headings, fixed order. Detail lives in `pfm/CLAUDE.md` § Testing Rules, `pfm/TESTPLAN.md` and `docs/dev/testing/`; this file states what a change owes.

## Tiers

- Unit: a package test beside its package, no real tmux server, no real engine process, no network: it injects its resolver and HTTP client (the harvest family's `harvest.StubPublicResolverForTest` in `TestMain`).
- `JAIL`, `JAIL+tmux`, `JAIL+sh`, `LIVE-READ`, `UNPLAYED`: `pfm/TESTPLAN.md` § Legend. The boundary that decides: whether the test needs a real tmux server (a scratch socket inside the jail's `TMUX_TMPDIR`); a flow the fake engine cannot play is `UNPLAYED`, named in `TESTPLAN.md` § "Flows the fake engine does not yet play".
- e2e: the tagged suite under `pfm/e2e/` (build tag `e2e`, `PFM_DEV_FENCE=1`) and the fence lanes (hermetic: mock engine, fixture seats, `--network none`); both run only inside the fence.

## Where a test lives

- `<file>_test.go` beside the source file, same package; end-to-end jail tests under `pfm/cmd/pfm/`. Extend the owning test file; add one only when none exists.
- A single-implementation rule (naming precedence, the kill ratchet, row classification, run-string synthesis) gets a table-driven test; an emitted shell line gets a golden file.

## Lanes and registries

- A new command or MCP tool lands with its beat (`infra/fence/lanes/beats.md` and `infra/fence/lanes/<lane>.sh`) and its map row (`infra/fence/lanes/map.tsv`, `name · lane · beat`) in the same commit; a fleet capability lands with its beat; the recipe is `docs/dev/testing/lanes.md` § Extend it.
- One editor per flight: `infra/fence/lanes/lib.sh`, `run.sh`, `map.tsv`, `beats.md`, `known-gaps.yml`, `pfm/scripts/known-skips.tsv`.

## Mock boundary

- Always real: the filesystem under the jail, SQLite, tmux on a scratch socket. Never real in a test: a live `cc-*` / `cx-*` socket, the real `/tmp/cc-sid`, a provider account. Engine processes are played by `internal/mockengine`.
- The release rehearsal (`/pfm:release` REHEARSE) is the one real-model run: host-side, never a suite.

## Environments and cleanup

- Every test runs under `internal/testjail` (`testjail.Run` in `TestMain`; `ShortRoot`, `Fleet`, `InstalledHome`, `CleanHome` build the homes). The `PFM_*` overrides in `pfm/internal/paths/paths.go` are the only knobs; `TMUX_TMPDIR = t.TempDir()`. `C25-testmain-jail` fails a test package whose `TestMain` does not reach `testjail.Run`; with `PFM_TEST_ARTIFACT_DIR` set (the gate sets `$run/profile`) every package process leaves `summary.json`, and a red or timed-out one a `DIAGNOSIS.txt` bundle: `docs/dev/testing/profiling.md`.
- Code flights build and test inside the fence: `.claude/scripts/dev.sh iso`. The host's `~/.local/bin` and the real `$HOME` are never test targets.
- Live traffic (a real page, the harvester's browser rung, a walled or lazy-loaded site) runs in the real-simulation fence: `.claude/scripts/dev.sh iso sim '<command>'` — Google Chrome (headless only, no display) and `pfm` installed from the worktree with the browser rung on; the harvester state persists per worktree. Its first line is `sim: chrome=… browser-rung=on` or `sim: BOOTSTRAP-FAILED — <step>`. A live check proves behavior; the regression test is still a fixture-driven unit test.

## Run commands

- Affected, an executor's only way to run a test (flight or general): `.claude/scripts/test-pfm.sh <test file>[::<go -run regexp>]...` for a file under `pfm/`, `.claude/scripts/test-templates.sh <test file>[::<test id>]...` for any other — both run in the fence and print their log path; timeout 600 s.
- Static, an executor's check command: run once, last, with the task's `files`; on a red, fix and run it again. `.claude/scripts/check-pfm.sh <files>` (in the fence: fmt on the named `.go` files; `typecheck`, `go vet` of the whole module; `lint-new` on the named `.go` files, where a finding on another file is burn-down, never its red, except a typecheck finding; `arch` — size ceilings, every `x.go` has its `x_test.go`; the `claudelaunch` launch-literal test) and `.claude/scripts/check-templates.sh <files>` (on the host: rumdl on the named `.md` files, the leak scan on every named file, the clone ratchet, placeholders, scratch paths, descriptions, the self-hosted manifest, codex markers, opencode-writer refs).
- Full, the flight gate's run and never an executor's: `.claude/scripts/dev.sh iso gate` (`iso gate pfm` for pfm alone) — the verify and test rows of pfm and templates as concurrent steps in one container, a `step · verdict · seconds` table in the run's `gate.tsv` (verdicts PASS, FAIL, NOT-RUN, TIMEOUT; rows `STEPS` and `BUDGET`), a `PROFILE` block pointing at each red step's diagnosis in the run dir, the run dir itself named by the output's last line `RUN DIR: <absolute host path>` (read it there, never the newest `run.*`), the wall judged against `infra/fence/gate-budget.yml` — in the fence only, a suite never runs on the host; timeout 600 s, background past that. `iso verify pfm` and `iso test pfm` still run their rows one after another.
- Static: `.claude/scripts/dev.sh iso verify pfm` (vet, fmt-check, lint-new, the architecture ratchet, the gate scripts' self-tests). Lanes: `infra/fence/lanes/run.sh`; the map gate `infra/fence/lanes/check-map.sh --pfm <a pfm built from this tree>`. `LANE_PROFILE=1 infra/fence/lanes/run.sh --lanes <L>` writes `waits.tsv` (`lane · beat · helper · condition · elapsed_s · outcome`) and prints the top waits; a lane timing change is measured with it, before and after.

## Concurrency

- Package and test concurrency is pinned by `TESTFLAGS ?= -p 6 -parallel 4` in `pfm/Makefile` (`make -s -C pfm testflags` prints it; the pick: `docs/dev/testing/concurrency-sweep.md`); callers may override it. A test that mutates process state (`t.Setenv`, `t.Chdir`, or a package variable) stays serial; a jail contained in a subprocess may use `t.Parallel` with `testjail.FleetEnv`. Isolation is the jail, one temp root per test. Timing budgets per package and per suite: `docs/dev/testing/timing.md`; an unbudgeted package fails.
- Packages pinned in `pfm/scripts/test-shard.sh`'s `SHARDS` table run split by top-level test across concurrent processes of one compiled test binary (`docs/dev/testing/timing.md` § Sharded packages): a test never depends on another top-level test of its package having run in the same process, nor on a fixed port, path or name another process of that package could hold.
- `go test -race` cannot run in the fence (`CGO_ENABLED=0`, no C compiler in the pfm-dev image): a `t.Parallel` change is proven by `-count=3` in the fence, under load once.
- A top-level e2e test calls `t.Parallel` unless a comment names the shared resource that keeps it serial.

## Gates and floors

- Coverage `pfm/.testcoverage.yml`: total 77, package 52 (`make -C pfm cover`).
- `make -C pfm gate` is ready-to-merge: fmt-check, lint-new, vet, arch, test, iso.
- Every skip is a row in `pfm/scripts/known-skips.tsv` (`pfm/scripts/skip-check.sh`); an unlisted skip fails.
- `iso gate` runs under `infra/fence/egress.sh`, and every non-dry lanes run ends with the verdict of a capture inside its lane container: `EGRESS PASS`, or red as `EGRESS FAIL` (each DNS name and destination listed) or NOT RECORDED; that verdict is the only network check.
- Every gate run is appended to the host ledger `$HOME/.local/state/pfm/gate-history/<project>/ledger.tsv`; `bash infra/fence/gate-history.sh report` shows per-step medians, regressions and verdict flips.

## Bug classes

- none beyond the gate's own check ids.

## Tricks and traps

- A long `TMUX_TMPDIR` fails with "File name too long" and reads as a tmux bug: keep scratch socket paths short (`testjail.ShortRoot`).
- `codex` is a shell function on a developer host: a test or script calls `command codex`.
- A probe that launches an engine closes stdin (`</dev/null`) or it hangs.
- The e2e harness refuses to run without `PFM_DEV_FENCE=1`: that red on a host is the fence law, not a defect.
- `internal/harvest` (a `/private` symlink) and `internal/hookentry` (socket path length) are red on a macOS host and green in the fence.
- A shell wait polls its own condition and counts its bound in 0.1 s ticks or from `$EPOCHREALTIME`, never a fixed sleep or whole `date +%s` seconds (a 1 s bound then waits up to 2 s); a fixed grace is a parameter a self-test can shorten (`MCP_STDIO_GRACE_SECS`).
- A closed listener keeps accepting while a parallel test's fork holds its fd until exec: a test expecting a refused dial on a closed port stays serial.
- A file a test writes then runs goes through `testjail.WriteExecutable` (`C26-exec-write` fails a bare exec-mode `os.WriteFile`: a parallel test's fork mid-write makes it ETXTBSY); `internal/deps` and `internal/config`, which testjail imports, use their local `writeExecutableUnderForkLock`.
- `PFM_TEST_PROFILE=cpu` kills a test's exec'd children (SIGPROF survives execve): never in a gate.
- A test binary started by a profiled test inherits `PFM_TEST_PROFILE_PARENT` and is a helper (summary only); a test that re-execs its own binary to prove profiling removes it from the child's env.
- A test that reads process-wide allocation or heap counters (`testing.AllocsPerRun`, `runtime.ReadMemStats`) is serial and calls `testjail.PauseFlightRecorder(t)` first: the always-on flight recorder allocates in its own goroutine.

## What not to test

- A model's words: a beat asserts from pfm's own report or the pane.
- A value one run printed about its data (a count, an id).
- A removed command, flag or tool takes its tests, its beat and its map row with it, never inverted into an absence assertion.
- That an event did not happen (a network call, a DNS query, a sleep, a fork): the egress verdict and the fenced measurements prove those.
