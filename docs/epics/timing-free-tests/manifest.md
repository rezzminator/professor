---
epic: timing-free-tests
status: IN_PROGRESS
created: 2026-11-18
updated: 2026-11-23
pipelines: []
flights: [timing-free-tests]
---

# Timing-free tests: no pfm test passes or fails by the clock

## Vision & Scope

The owner's ask, 2026-11-18: tests must not depend on timing and timeouts, so a loaded host stops faking red gates ("so we won't be having this headache"). The same day he ruled: land the first fix and park all the rest as this epic. The epic resumed on 2026-11-22 as the flight `timing-free-tests`, in progress.

Done means: no pfm test passes or fails because of how much wall-clock time elapsed, and the gate check with no allowlist reads zero.

- A test of time-dependent logic owns a fake clock.
- A test that drives a real process (tmux, the tagged e2e tier, the journey) waits as a bounded poll on a named condition, never a fixed sleep. The law: `templates/global/rules/testing.md` ("Poll a named condition, bounded; never a fixed sleep or wait") and `.claude/rules/pfm-os-doors.md` ("A test waits on a named condition, never on the Linux fence's speed").
- The full gate passes at high host load without a TIMEOUT attributed to contention.

## Key Decisions

Owner rulings:

- 2026-11-18: tests must not depend on timing and timeouts, so a loaded host stops faking red gates.
- 2026-11-18: land the first fix, park the rest as this epic, work it later with a fresh token limit.
- 2026-11-22: "I want NO sleeping/timer left there, ZERO"
- 2026-11-22: "you don't need to make a gate refusal with allowlist and then update it one by one, that's wasted effort, just fix ALL of them, and then do it, with NO allowlist"
- 2026-11-22: "write a rule in the local .claude therefor (if there's one already then just extend thereto)"
- 2026-11-22: "Make a worktree and do it in phases using foremans there"

The rulings that still bind a later change are in `update.md` § State of work. The rule is `.claude/rules/pfm-test-waits.md`; `.claude/rules/pfm-os-doors.md` points to it; the run commands are in `.claude/commands/pfm-testing-manual.md`.

## The doors and the checks

Every wait a test needs goes through a door; three checks hold it, none with an exception list.

| Door | Where | Shape |
| --- | --- | --- |
| Go test wait | `pfm/internal/testwait` | `Until`, `UntilErr`, `Receive`, `Drive`, `Blocked`, `Context`, `Deadline`, `Expired`, `Stopwatch`, `ProcessClock`, `ProcessPace`, `ProcessTries`, `HungLimit`; no call takes a duration |
| Shell test wait | `scripts/shtest-wait.sh` | loaded through `scripts/shtest.sh` and `infra/plugin-lab/tests/testlib.sh` |
| Production time bound | `pfm/internal/clock/timeout.go` | `clock.WithTimeout(parent, c, d)` is the one door for a bound made as a context; `clock.Wall()` reads the time; `clock.Real` is read at the call; no production limit changed its value |
| Mock engine | `pfm/internal/mockengine/control*.go`, test side `pfm/internal/mockengine/control/` | one clock only a test moves, through a control FIFO; a scenario declaring a timed act with no control is refused at load |
| Journey | `infra/fence/journey/budget.sh` | judges the 500 s time budget and the 20 % CPU share limit after the run |

| Check | Run alone | Judges |
| --- | --- | --- |
| `C36-clock-door` (arch check job; source `pfm/scripts/testclock/`) | `cd pfm && bash scripts/arch-check.sh --clock [paths]` | Go test and test-support files, `testdata/`, and the shell, Python and JavaScript written inside fixtures |
| real-wait tripwire (`pfm/internal/testjail/realwait.go`), armed in every unit test binary | `cd pfm && bash scripts/arch-check.sh --real-wait [packages]` | a unit test that makes production code wait on the real clock |
| `shtest-clock` (`scripts/shtest-clock-check.sh`, spellings in `scripts/shtest-clock-check.tsv`), a step of the fence gate | the fence gate step | shell tests |

## Where real time is still read

"Zero" means no test outcome decided by elapsed time. Real time remains in three places:

- Inside the door: look pacing (1 ms doubling to 50 ms) and the one 2-minute hung-only limit, cut by the test's own deadline. A test waiting on another process must look again somehow; the limit ends a hung test, it never decides a pass.
- Production's own limits, where a test runs the real binary and does not replace the clock: the journey (pfm's 5 s launch watch) and three jail-tier tests that wait 3 to 10 s for a production limit to fire against a frozen real server (`TestHerdrBridgeTmuxGivesUpOnAFrozenServer`, `TestOpenRefusesAnUnresponsiveLiveServer`, `TestWatchLadderAFrozenTmuxServerIsNeverAnExit`). Load cannot fail them; a slow machine only makes them slower.
- Limits whose timer lives outside pfm, kept short on purpose and verified by hand, not by a check: `pfm/cmd/pfm/headless_exec_jail_test.go` (`--timeout 0.05`, enforced by the child) and the os/exec grace periods (`WaitDelay`) in `pfm/internal/deps/runner_jail_test.go`, `pfm/internal/installer/skill_sources_jail_test.go` and `pfm/internal/headless/run/web_search_budget_test.go`. The clock check does not see a `WaitDelay` set inside an options literal.

## Progress Log

- 2026-11-18 — session: first fix landed, the watch-ladder tests run on a fake clock (`8e52471d1`)
- 2026-11-23 — flight: the sweep is built and gated on `flight/timing-free-tests`, merge-ready pending a macOS run (commit on the branch, not merged)

## Discoveries

- The census before the work, at develop `388901f5f15e`: about 880 real-clock sites in 341 of 1,745 Go test files, 108 sites in 22 shell test files, 379 unit tests in 21 of 103 packages that made production code wait on the real clock. The tool: `census.py` in the private folder.
- A second, older time override exists beside `pfm/internal/clock`: the scan clock `PFM_TEST_NOW_NS` in `pfm/internal/fleet/scan.go`.
- The rest of `reload`'s time is CPU, not a wait: `fleetdb` re-runs its whole schema on every open. One schema init per database per process is a found speed-up, not built.

## Open Questions

1. The macOS run, `bash infra/fence/journey/run.sh --ci-mac` (it pushes, so only on the owner's order): four macOS-only test files changed and never ran (`pfm/internal/deps/runner_group_darwin_jail_test.go`, `pfm/internal/gather/procfs_darwin_jail_test.go`, `pfm/internal/stats/stats_darwin_jail_test.go`, `pfm/internal/index/stress_peak_darwin_jail_test.go`), and the macOS journey step of `.github/workflows/ci.yml` now runs `infra/fence/journey/budget.sh`. The landing waits on it.
2. Two budget rows of `pfm/.testtiming.yml` are exceeded at rest and were not raised: `internal/call` (24 s against 16 s) and `internal/reload` (10.5 s against 8 s). Whether to raise them or build the `fleetdb` speed-up is not asked.
3. The stress tests run only with `PFM_STRESS=1`, which the gate never sets.
4. The plugins under `plugins/` are out of this sweep: each is its own repository.

## Files

- `update.md` — state of work, the rulings that still bind, what the flight delivered.

Private, outside the repo, in `~/.local/state/pfm/flights/professor/timing-free-tests/`:

- `plan.md` — the flight's plan and rulings, the source of `update.md` § Rulings.
- `census.py`, `census.tsv` — the clock census before the work; regenerate with `python3 census.py {worktree}/pfm {out.tsv}`.
- `patterns.md` — the pattern catalogue for converting a test.
- `herdr-test-contention.patch` — an unproven 87-line patch that raises wall-clock bounds instead of removing the dependence; reference only, not the fix.
