---
name: pfm-testing-manual
description: Governs how pfm (the Go fleet engine) is tested — tiers, test homes, lanes, mocks, run commands, gates and floors, bug classes, traps. Read by flights-foreman and flights-mechanical-executor before testing, flights-speccer for tiers, homes and gates, flights-lander whole; `/pfm-testing-manual` opens it for a human. Keep it true in the same change that alters how pfm is tested.
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
- The plugin lab's hermetic self-tests live in `infra/plugin-lab/tests/` (fence step `templates.plugin-lab`); its live, metered runs of a plugin checkout run on the host through `infra/plugin-lab/lab run`: `docs/dev/testing/plugin-lab.md`.

## Lanes and registries

- A new command or MCP tool lands with its beat (`infra/fence/lanes/beats.md` and `infra/fence/lanes/<lane>.sh`) and its map row (`infra/fence/lanes/map.tsv`, `name · lane · beat`) in the same commit; a fleet capability lands with its beat; the recipe is `docs/dev/testing/lanes.md` § Extend it.
- One editor per flight: `infra/fence/lanes/lib.sh`, `run.sh`, `map.tsv`, `beats.md`, `known-gaps.yml`, `pfm/scripts/known-skips.tsv`.

## Mock boundary

- Always real: the filesystem under the jail, SQLite, tmux on a scratch socket. Never real in a test: a live `cc-*` / `cx-*` socket, the real `/tmp/cc-sid`, a provider account. Engine processes are played by `internal/mockengine`.
- The release rehearsal (`/pfm:release` REHEARSE) is the one real-model run: host-side, never a suite.

## Environments and cleanup

- Every test runs under `internal/testjail` (`testjail.Run` in `TestMain`; `ShortRoot`, `Fleet`, `InstalledHome`, `CleanHome` build the homes). The `PFM_*` overrides in `pfm/internal/paths/paths.go` are the only knobs; `TMUX_TMPDIR = t.TempDir()`. `C25-testmain-jail` fails a test package whose `TestMain` does not reach `testjail.Run`; with `PFM_TEST_ARTIFACT_DIR` set (the gate sets `$run/profile`) every package process leaves `summary.json`, and a red or timed-out one a `DIAGNOSIS.txt` bundle: `docs/dev/testing/profiling.md`.
- Code flights build inside the fence through `.claude/scripts/dev.sh iso` and test through `.claude/scripts/test.sh`. The host's `~/.local/bin` and the real `$HOME` are never test targets.
- Live traffic (a real page, the harvester's browser rung, a walled or lazy-loaded site) runs in the real-simulation fence: `.claude/scripts/dev.sh iso sim '<command>'` — Google Chrome (headless only, no display) and `pfm` installed from the worktree with the browser rung on; the harvester state persists per worktree. Its first line is `sim: chrome=… browser-rung=on` or `sim: BOOTSTRAP-FAILED — <step>`. A live check proves behavior; the regression test is still a fixture-driven unit test.

## Run commands

- The full gate runs ONCE per worktree, right before its merge. During development you run ONLY the tests your change affects. No full suite at any other time: not per repair round, not to verify a fix, not before the gate. A red gate is fixed with affected tests only, then the gate runs once more before the merge.
- A change is verified by one `.claude/scripts/test.sh` call over every file it touched, once, at the end of the work, before the commit; a red is fixed, and only the red test or check runs again until green.
- Affected, the only way to run a test: `.claude/scripts/test.sh <path>[::<selector>]...` — `pfm/` and every other path in one call, in the fence. Each path brings its affected tests (a `_test.go` file its package, `-run` the `::selector`, else every Test and Fuzz func of the file; any other `.go` file its whole package; a shell, node or python test file itself, a source file its same-named test: the map is the script's header), then the static checks of exactly the named paths: for `pfm/` paths `fmt` on the named `.go` files, `typecheck` (`go vet` of the named `.go` files' packages and every module package importing one, the whole module when no `.go` file is named), `lint-new` on those packages (a finding on another file is burn-down, never its red, except a typecheck finding), `arch` (size ceilings, every `x.go` has its `x_test.go`), `launch-literals`; for every path rumdl on the named `.md` files and the leak scan on the named files; for the other paths the clone ratchet, placeholders, scratch paths, descriptions, the self-hosted manifest, codex markers. It prints one `TEST` line per test invocation and one `CHECK` line per check, then `TESTS`, `STATIC`, `VERDICT PASS|FAIL` and last `log: /tmp/{project}/test/{UTC}-{pid}.log`; exit 0 PASS, 1 FAIL, 2 usage. No setup step comes first: every tool is the fence image's. A runner that exits 0 without proving a test (no test ran, an unlisted skip) is red; timeout 600 s.
- The test scripts' own suites: `scripts/test-test.sh` tests `.claude/scripts/test.sh`, `scripts/test-unit-path.sh` tests `.claude/scripts/unit-path.sh`; each is the templates-gate step `templates.{test|unit-path}-tests`, which CI's `iso test templates` runs; a change to either script runs its suite through `test.sh`.
- Full: `.claude/scripts/test.sh --full` (`dev.sh iso gate`) — pfm, templates, e2e and the lane self-tests as concurrent steps in one run, a `step · verdict · seconds` table in the run's `gate.tsv` (verdicts PASS, FAIL, NOT-RUN, TIMEOUT; rows `STEPS` and `BUDGET`), a `PROFILE` block pointing at each red step's diagnosis in the run dir, the run dir itself named by the output's last line `RUN DIR: <absolute host path>` (read it there, never the newest `run.*`), the wall judged against `infra/fence/gate-budget.yml`; one run closes the pfm and templates gates both; timeout 600 s, background past that.
- Lanes: `infra/fence/lanes/run.sh`; the map gate `infra/fence/lanes/check-map.sh --pfm <a pfm built from this tree>`. `LANE_PROFILE=1 infra/fence/lanes/run.sh --lanes <L>` writes `waits.tsv` (`lane · beat · helper · condition · elapsed_s · outcome`) and prints the top waits; a lane timing change is measured with it, before and after.

## Concurrency

- Package and test concurrency is set by `TESTFLAGS ?= -p $(TEST_P) -parallel 4` in `pfm/Makefile`, `-p` following the fence's CPU cap (`PFM_FENCE_TEST_P`, else the container's cgroup quota; the cap knobs `PFM_FENCE_CPUS`, `PFM_FENCE_MEM` and their defaults: `infra/fence/shared.sh`; `make -s -C pfm testflags` prints it; the pick: `docs/dev/testing/concurrency-sweep.md`); callers may override it. A test that mutates process state (`t.Setenv`, `t.Chdir`, or a package variable) stays serial; a jail contained in a subprocess may use `t.Parallel` with `testjail.FleetEnv`. Isolation is the jail, one temp root per test. Timing budgets per package and per suite: `docs/dev/testing/timing.md`; an unbudgeted package fails.
- Packages pinned in `pfm/scripts/test-shard.sh`'s `SHARDS` table run split by top-level test across concurrent processes of one compiled test binary (`docs/dev/testing/timing.md` § Sharded packages): a test never depends on another top-level test of its package having run in the same process, nor on a fixed port, path or name another process of that package could hold.
- `go test -race` runs in the fence with cgo on for that command alone: `.claude/scripts/dev.sh iso run 'cd pfm && CGO_ENABLED=1 go test -race …'` (the pfm-dev image carries gcc; every other build stays `CGO_ENABLED=0`). The gate's `pfm.race` step runs the tests `CHECKS_PFM_RACE_TESTS` names (`infra/fence/checks.sh`) and reds any of them without a pass event; a test guarding a race joins that list. A `t.Parallel` change is proven by `-count=3` in the fence, under load once, and by `-race` over its package. A test whose ceiling the race detector breaks (`testing.AllocsPerRun`) skips on its package's `raceEnabled` constant (`//go:build race`) with a `race-mode` known-skips row.
- A top-level e2e test calls `t.Parallel` unless a comment names the shared resource that keeps it serial.

## Gates and floors

- Coverage `pfm/.testcoverage.yml`: total 77, package 52, in the fence: `.claude/scripts/dev.sh iso cover pfm`; the full gate does not run it.
- Every skip is a row in `pfm/scripts/known-skips.tsv` (`pfm/scripts/skip-check.sh`); an unlisted skip fails.
- `iso gate` runs under `infra/fence/egress.sh` inside its run's own network namespace, so its verdict covers that run alone, and every non-dry lanes run ends with the verdict of a capture inside its lane container: `EGRESS PASS`, or red as `EGRESS FAIL` (each DNS name and destination listed) or NOT RECORDED; that verdict is the only network check.
- Every gate run is appended to the host ledger `$HOME/.local/state/pfm/gate-history/<project>/ledger.tsv`, with the count of runs live in the shared fence container when it started (`concurrent_runs`); `bash infra/fence/gate-history.sh report` shows per-step medians, regressions and verdict flips.

## Bug classes

- none beyond the gate's own check ids.

## Tricks and traps

- A long `TMUX_TMPDIR` fails with "File name too long" and reads as a tmux bug: keep scratch socket paths short (`testjail.ShortRoot`).
- A tool the fence image lacks makes `test.sh` print `CHECK FAIL image (the fence image lacks: …)` and run no check, exit 1: the fix is `infra/fence/pfm-dev.Dockerfile`, never a host install. A fresh worktree lacks the engine mirrors, and `iso gate`'s `templates.mirrors` needs none: the step copies the tree under test (tracked and unignored files, no submodule checkout) into `$TMP_BASE/engine-sync/mirror-tree`, builds the mirrors there with the engine-sync built from its mounted source, under a scratch home whose `.claude/engine-sync.json` is the tree's `templates/global/engine-sync.json`, and every mirror gate reads that copy — so a rename, a stale host build or the host's installed engine-sync policy never turns it red; the worktree's own `AGENTS.md`, `.codex/**` and `.opencode/**` are for the host's tools, not the gate.
- The fence mounts the worktree read-only: `PFM_UPDATE_GOLDENS=1` regenerates in a writable scratch copy through `dev.sh iso run`; diff the result against the tracked golden, then apply it on the host.
- `codex` is a shell function on a developer host: a test or script calls `command codex`.
- A probe that launches an engine closes stdin (`</dev/null`) or it hangs.
- The e2e harness refuses to run without `PFM_DEV_FENCE=1`: that red on a host is the fence law, not a defect.
- `internal/harvest` (a `/private` symlink) and `internal/hookentry` (socket path length) are red on a macOS host and green in the fence.
- A shell wait polls its own condition and counts its bound in 0.1 s ticks or from `$EPOCHREALTIME`, never a fixed sleep or whole `date +%s` seconds (a 1 s bound then waits up to 2 s); a fixed grace is a parameter a self-test can shorten (`MCP_STDIO_GRACE_SECS`).
- A closed listener keeps accepting while a parallel test's fork holds its fd until exec: a test expecting a refused dial on a closed port stays serial.
- A closed `gather.AcquireAccountGuard` keeps its flock while a parallel test's fork holds the open file description until exec: `AcquireAccountGuard(dir, false)` retries a busy lock for `accountBusyGrace` before it refuses, and a test that expects the busy refusal calls `acquireAccountGuardWithin(dir, false, 0)` (in `gather`) or holds the guard for longer than the grace.
- A file a test writes then runs goes through `testjail.WriteExecutable` (`C26-exec-write` fails a bare exec-mode `os.WriteFile`: a parallel test's fork mid-write makes it ETXTBSY); `internal/deps` and `internal/config`, which testjail imports, use their local `writeExecutableUnderForkLock`.
- `PFM_TEST_PROFILE=cpu` kills a test's exec'd children (SIGPROF survives execve): never in a gate.
- A test binary started by a profiled test inherits `PFM_TEST_PROFILE_PARENT` and is a helper (summary only); a test that re-execs its own binary to prove profiling removes it from the child's env.
- A test that reads process-wide allocation or heap counters (`testing.AllocsPerRun`, `runtime.ReadMemStats`) is serial and calls `testjail.PauseFlightRecorder(t)` first: the always-on flight recorder allocates in its own goroutine.

## What not to test

- A model's words: a beat asserts from pfm's own report or the pane.
- A value one run printed about its data (a count, an id).
- A removed command, flag or tool takes its tests, its beat and its map row with it, never inverted into an absence assertion.
- That an event did not happen (a network call, a DNS query, a sleep, a fork): the egress verdict and the fenced measurements prove those.
