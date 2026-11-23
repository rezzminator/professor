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

The owner's ask, 2026-11-18: tests must not depend on timing and timeouts, so a loaded host stops faking red gates ("so we won't be having this headache"). The same day he ruled: land the first fix and park all the rest as this epic. The epic resumed on 2026-11-22 as the flight `timing-free-tests`, which landed on 2026-11-23.

Done means: no pfm test passes or fails because of how much wall-clock time elapsed, and the gate check with no allowlist reads zero.

- A test of time-dependent logic owns a fake clock.
- A test that drives a real process (tmux, the tagged e2e tier, the journey) waits as a bounded poll on a named condition, never a fixed sleep. The law: `plugins/flights/plugins/flights/rules/testing.md` ("Poll a named condition, bounded; never a fixed sleep or wait") and `.claude/rules/pfm-os-doors.md` ("A test waits on a named condition, never on the Linux fence's speed").
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
- Production's own limits, where a test runs the real binary and does not replace the clock: the journey (pfm's 5 s launch watch) and two jail-tier tests that wait for a production limit to fire against a frozen real server (`TestOpenRefusesAnUnresponsiveLiveServer`, `TestWatchLadderAFrozenTmuxServerIsNeverAnExit`). Load cannot fail them; a slow machine only makes them slower.
- Limits whose timer lives outside pfm, kept short on purpose and verified by hand, not by a check: `pfm/cmd/pfm/headless_exec_jail_test.go` (`--timeout 0.05`, enforced by the child) and the os/exec grace periods (`WaitDelay`) in `pfm/internal/deps/runner_jail_test.go`, `pfm/internal/installer/skill_sources_jail_test.go` and `pfm/internal/headless/run/web_search_budget_test.go`. The clock check does not see a `WaitDelay` set inside an options literal.

## Progress Log

- 2026-11-18 — session: first fix landed, the watch-ladder tests run on a fake clock (`8e52471d1`)
- 2026-11-23 — flight: the sweep landed on `develop` as one fast-forward commit, macOS journey and unit and e2e jobs green on it (`4d43dbee4`)
- 2026-11-23 — session: the fleet database brings its schema once per process and `internal/call` is back under its budget row (`14236aa30`, local `develop`, not pushed)

## Discoveries

- The census before the work, at develop `388901f5f15e`: about 880 real-clock sites in 341 of 1,745 Go test files, 108 sites in 22 shell test files, 379 unit tests in 21 of 103 packages that made production code wait on the real clock. The tool: `census.py` in the private folder.
- A second, older time override exists beside `pfm/internal/clock`: the scan clock `PFM_TEST_NOW_NS` in `pfm/internal/fleet/scan.go`.
- The rest of `reload`'s time is CPU, not a wait. `fleetdb` now brings a schema once per process (`schemaFingerprint` and `schemasBrought` in `pfm/internal/fleetdb/open.go`), which did not bring `internal/reload` under its row.
- `testwait.Drive`'s looks are the remaining time of the Herdr unit tests of `internal/call` (`TestHerdrCallNative`, about 1.1 s) and 44 % of `internal/reload`'s unit-tier CPU profile; curing it changes the shared wait door `pfm/internal/testwait`. Found, not built.
- A new shared database still costs 8 to 9 ms (the schema script and its steps): paid once per machine in production, once per database in tests. Found, not built.

## Open Questions

1. One budget row of `pfm/.testtiming.yml` is exceeded at rest and was not raised: `internal/reload` (8 s row; 10.5 s on a quiet host before the schema change, 15.5 s and 18.0 s in two loaded gates after it). Whether to raise it or cure `testwait.Drive`'s looks is not asked.
2. The stress tests run only with `PFM_STRESS=1`, which the gate never sets.
3. The plugins under `plugins/` are out of this sweep: each is its own repository.

## Files

- `update.md` — state of work, the rulings that still bind, what the flight delivered.

Private, outside the repo, in `~/.local/state/pfm/flights/professor/timing-free-tests/`:

- `plan.md` — the flight's plan and rulings, the source of `update.md` § Rulings.
- `census.py`, `census.tsv` — the clock census before the work; regenerate with `python3 census.py {checkout}/pfm {out.tsv}`.
- `patterns.md` — the pattern catalogue for converting a test.
- `herdr-test-contention.patch` — an unproven 87-line patch that raises wall-clock bounds instead of removing the dependence; reference only, not the fix.
