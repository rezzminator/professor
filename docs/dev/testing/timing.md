# Test timing budgets

## Contents

- [Gate](#gate)
- [Critical path](#critical-path)
- [TSV](#tsv)
- [Sharded packages](#sharded-packages)
- [Budget file](#budget-file)
- [Checks and measurement](#checks-and-measurement)
- [Concurrency sweep](#concurrency-sweep)
- [Accepted measurements](#accepted-measurements)

`pfm/scripts/test-timing.sh` reads Go test JSON, writes a TSV, checks suite budgets, and lowers budgets from three successful captures. `.claude/scripts/dev.sh` owns the unit and tagged e2e test commands. `pfm/Makefile` supplies `TESTFLAGS`; the CI workflow delegates to the same fenced gate.

## Gate

From the worktree root:

```bash
.claude/scripts/dev.sh iso gate
```

Use `.claude/scripts/dev.sh iso gate pfm` for pfm alone. The source mount stays read-only. Generated artifacts go to `/tmp/{project}/timing/` through a separate writable mount. Each invocation gets its own `run.*` directory containing `gate.tsv`, `gate.meta`, `steps/<name>.log`, `steps/<name>.prof.tsv`, `resources.tsv`, `profile/`, `profile.tsv`, and, for the pfm steps, `unit.json`, `unit.tsv`, `unit.load`, `unit-shards/`, `bin/`, `e2e.json`, `e2e.tsv` and `e2e.load`. What each profiling artifact holds, and how to read a red gate from them, is in [profiling.md](profiling.md).

**Phases.** `gate_run` registers every test step in phase 0: `pfm.unit` and `pfm.e2e` (heavy), the `pfm.self.*`, `templates.lanes.*` and `templates.demo.*` suites, and the `fixture.*` steps under `PFM_GATE_FIXTURES=1`. `steps_barrier` then opens phase 1, the static checks, which starts only when every phase-0 step has ended. Phase 1 registers its heavy Go steps longest first: `pfm.lint-new`, `pfm.fmt-check`, `pfm.vet`, `pfm.vet-darwin`, then `pfm.arch` (light) and `templates.check-map` (heavy), then the light `templates.*` checks. A step starts as soon as a slot is free, in registration order: at most `STEPS_JOBS` steps run at once (default: the CPU count), and at most `STEPS_HEAVY_JOBS` heavy steps (default: 3).

**Step bound.** Every step runs under `stepprof_run` with a bound: its own (`steps_set_bound`, used only by fixture steps) or the default `STEPS_BOUND_S`. When the caller does not set `STEPS_BOUND_S`, `gate_run` derives it from the target's budget fail limit, `budget × tolerance × fail_factor` (494 s for `all`); a budget that cannot give one falls back to 1500 s and the gate says why. A step that reaches its bound is snapshotted, TERMed, then KILLed.

**Table.** `gate.tsv` has the header `step verdict seconds` and one row per step in registration order; a verdict is `PASS`, `FAIL`, `NOT-RUN` (the command could not start, or died of a signal) or `TIMEOUT` (the bound ended it). Then two rows:

- `STEPS <PASS|FAIL> <seconds>`: the wall from the first step's start to the last step's end, one decimal; `FAIL` when any step is not `PASS`.
- `BUDGET <PASS|WARN|FAIL|UNPINNED|ERROR> <seconds>`: the `STEPS` wall rounded up to a whole second, judged against the target's row of `infra/fence/gate-budget.yml`: `PASS` within budget × `tolerance`; `WARN` over it through `fail_factor` × that limit, and beyond it when the run's [attribution](profiling.md#attribution) is `CONTENTION`; `FAIL` beyond it otherwise; `UNPINNED` for a row marked `unpinned` (recorded, not judged); `ERROR` when there is no row, no readable budget or no `STEPS` wall.

The printed table adds pointers to each non-PASS row: the step log, the hang tree of a `TIMEOUT`, the ERR records. It is followed by the `PROFILE` block. A failed step keeps its log for diagnosis.

The gate reports test execution and timing checks as separate rows. Unit tests use the Makefile's `TESTFLAGS` (`-p 6 -parallel 4`); tagged e2e tests use `-p 1`. `pfm.e2e` samples the VM's busy and the container's own CPU into `e2e.load` for the whole e2e run, the record the e2e timing check corrects for other load with, as the unit check does with `unit.load`. `dev.sh iso` passes `TESTFLAGS` and the knobs `STEPS_JOBS STEPS_HEAVY_JOBS STEPS_BOUND_S STEPPROF_TRACE STEPPROF_GRACE_TICKS PFM_TEST_PROFILE PFM_GATE_FIXTURES` into the container when they are set. `make timing` prints the newest unit or e2e TSV. CI retains the JSON and TSV files even after failure.

## Critical path

`STEPS` is phase 0's last step plus phase 1's longest chain. In the five captures of the kept schedule (S5, `$HOME/.local/state/pfm/flights/professor/test-profiling/measure/10-b/captures.tsv`), `pfm.unit` ends phase 0 in all five at 31.0–31.9 s, with `pfm.e2e` 0.8–2.6 s earlier. Phase 1 then takes 8.2–8.7 s and its last step is `pfm.fmt-check` in all five: it starts in the first heavy slot when phase 1 opens and runs alone to the end. `pfm.lint-new` (6.6–7.0 s) and `pfm.vet` → `pfm.vet-darwin` → `templates.check-map` share the other two slots and end before it. The light lanes of phase 0 all end by about 23 s and bind nothing. With two heavy slots (`STEPS_HEAVY_JOBS=2`, the S0 arm in `critical-path.txt` in the same directory; `slowness-map.md` § 1 there for the 54.0 s baseline) `pfm.fmt-check` waits 5.5–5.7 s for a slot and phase 1 becomes vet, vet-darwin and fmt-check in series.

Reading it from one run:

1. **`gate.tsv`**: the order is registration order, so phase-0 rows come first; the largest phase-0 `seconds` is the candidate phase-0 binder, and `STEPS` minus it is roughly phase 1.
2. **`steps/<name>.prof.tsv`**: `start_epoch` and `finish_epoch` place every step on one clock. Subtract the earliest `start_epoch` and sort by finish: the last phase-0 finish is where phase 1 opens (every phase-1 `start_epoch` is at or after it); the last finish overall is the end of `STEPS`.
3. **Heavy-slot waits**: a heavy phase-1 step whose `start_epoch` is later than phase 1's opening waited that long for a slot (`STEPS_HEAVY_JOBS` and the registration order decide which). A light step waits only when `STEPS_JOBS` steps are already running.
4. **Inside a Go step**: `unit.json` and `e2e.json` are `go test -json` streams; per package, the first event's `Time` and the terminal `pass`/`fail` event's `Time` and `Elapsed` give its start and end. The package ending last is what holds its step open; a package whose `Elapsed` far exceeds its CPU (`profile/<label>.<pid>/summary.json` `user_s` + `sys_s`) is waiting, not working.
5. **`resources.tsv`**: Δ`cg_cpu_s` ÷ (Δ`epoch_s` × `cpus`) over a window is the container's CPU use; a long stretch well under the CPU count is idle time a reordering could fill. The maximum `cg_mem_peak_mb` is the run's memory peak against the 2 GiB cap; `cg_swap_mb` above zero means the run was memory-bound.

## TSV

The header is:

```text
package  wall_s  tests  parallel_tests  slowest_test  slowest_s  status
```

Package rows use tab separators. `wall_s` is the package terminal event's elapsed time. `tests` counts top-level tests, excluding subtests; `parallel_tests` counts top-level pause events. The slowest test may be a subtest. `status` preserves the package result.

The trailing `SUITE` row reuses the columns as follows:

| Column | Suite meaning |
| --- | --- |
| `wall_s` | Last event timestamp minus first event timestamp |
| `tests` | Package count |
| `parallel_tests` | Sum of package elapsed times |
| `slowest_test` | Slowest package |
| `slowest_s` | That package's elapsed time |
| `status` | Aggregate test result |

Event span excludes build work before the first event. The sweep records external command wall time separately. Neither elapsed value isolates CPU cost from host contention.

## Sharded packages

`pfm/scripts/test-shard.sh` pins `cmd/pfm` to four shards. In a clean plain `go test ./...` run (75.7 s), that package took 55.7 s and no other package exceeded 22 s. History-run medians were 43.8 s at three shards, 43.3 s at five, and 39.2 s then 43.7 s at four under varying host load. History only orders tests across the four shards; it never selects packages or changes shard counts. Every other package runs in one unsharded `go test` process.

The script compiles the `cmd/pfm` test binary once, lists its tests from that binary, and starts all four shards as `go tool test2json` runs of the same binary. It starts the unsharded process first and runs it alongside the sharded package. Sharded packages run one at a time, so at most one unsharded process plus the largest shard count are direct children of the script. With `--history` (the gate passes the newest earlier `unit.json` with test events), the unsharded process lists its packages longest first by their history `Elapsed`, packages with no history first in their given order, so the slowest packages start at once instead of last.

A unit run first builds pfm and mock-engine, both at once, and starts no test before both have finished; it hands them to the tests through `PFM_TEST_PFM_BINARY` and `PFM_TEST_MOCK_ENGINE_BINARY`. A failed build leaves no binary and fails the run. With `--bin-dir D` the binaries are built into `D` and kept; the gate passes `$run/bin`, and `templates.check-map` and `templates.mirrors` reuse `$run/bin/pfm` instead of building their own; each builds one when the unit run left none (a `templates` gate, a failed build). Without `--bin-dir` they go to a temporary directory that is removed.

The merged stream has one terminal event per package. A sharded package's `wall_s` is its slowest shard's elapsed time; its existing `pfm/.testtiming.yml` budget is judged the same way as before. `SHARD-LOST <test>` means a listed test did not run and fails the package. Each process's raw JSON stream is kept in `unit-shards/` beside `unit.json` for diagnosis.

## Budget file

`pfm/.testtiming.yml` contains `tolerance` and `suites.<name>.wall_s` plus a package-to-seconds map at `suites.<name>.packages`. Unit and e2e package sets are independent. The initial tolerance is 1.25: a duration above budget times tolerance fails.

New package budgets and deliberate increases require an explicit edit with its evidence in the commit. A `TESTFLAGS` change re-baselines the unit block by hand from three passing captures at the new flags: take the maximum event span for each package and the suite, round up to whole seconds with a one-second floor, then double. The capture set and per-package derivation table are retained with the change. `--measure` only lowers existing budgets. Architecture baselines under `pfm/.arch/` are separate and are not changed by timing measurements.

## Checks and measurement

Run the parser inside the fence against captures available on its artifact mount:

```bash
.claude/scripts/dev.sh iso run 'bash pfm/scripts/test-timing.sh --check --suite unit --out /pfm-timing/check.tsv /pfm-timing/run/unit.json'
```

Malformed or incomplete input, unreadable budgets, missing dependencies, failed tests, and missing packages remain red. For package and SUITE timing rows, an overage through `fail_factor × limit` is `TIMING WARN` with exit 0 and a `GATE-WARN` marker that the gate prints after its table. An overage beyond that boundary is `TIMING FAIL` with exit 1. The shell fixture suites exercise these paths through `dev.sh iso run`.

The unit runner writes `unit.load` beside `unit.json`, sampling the VM's busy CPU seconds from `/proc/stat` and the fence container's own CPU seconds from `/sys/fs/cgroup/cpu.stat` from before the first test process through after the last. For each package's event window and the suite window, the checker uses the samples bracketing that window: `f = ((busy₁ − busy₀) − (own₁ − own₀)) / ((time₁ − time₀) × cpus)`, clamped to 0–0.9. It compares elapsed time with `budget × tolerance / (1 − f)`. An overage explained by that measured other load prints `TIMING CORRECTED`; beyond that corrected limit, it prints `TIMING WARN` through `fail_factor × limit` and `TIMING FAIL` above it. A missing or unavailable record prints `TIMING: other load not measured` and leaves the limit at `budget × tolerance`. A malformed record is `TIMING-UNREADABLE`, exit 2.

The steady-state ratchet uses three independent successful captures with identical package sets and flags. Record host load and uptime before and after each capture. A failed run cannot establish or lower a budget. For the sharded unit re-baseline above, an `iso gate` run whose only red is package timing against the superseded unit block is an eligible capture when its tests and skip checks pass and it has no `SHARD-LOST`. Preserve all raw captures beside the measurement log so each budget can be traced back to evidence.

At `-p 6 -parallel 4`, the unit block, tagged e2e block, and gate walls use one rule: take the maximum of three eligible captures, round up to whole seconds (with a one-second floor), then double each package, suite, and gate target budget. Unit, tagged e2e, and `all` use `iso gate all` captures; `templates` uses `iso gate templates` captures. `pfm` takes the `all` gate budget because it runs a subset of its steps. Every budget is checked at budget × `tolerance` (`1.25`); a gate wall over that limit warns through `fail_factor` × limit and fails beyond it. The doubling absorbs shared-host load: clean `all` walls of one tree spanned 61.3–115.6 s on 2026-10-01; the slowest was the first gate after a merge, with cold build and lint caches.

## Concurrency sweep

`make sweep` runs a host preflight followed by the fenced sweep and report. Host test execution is excluded from this train. The sweep explores package concurrency and then per-package test concurrency, retaining three repetitions per point and a separate shuffle check. A pin needs the fastest point supported by at least three passing fenced captures and green timing checks.

The target enables `--wait-quiet`, which prefers load below 4 before each capture. The wait is outside the timed command and capped at five minutes; sustained load emits a warning and proceeds with high-load evidence. The raw TSV records each result; its sibling `.load.log` records capture identity and dated uptime before and after, including failed captures. Unreadable load probes fail explicitly.

The unit gate is pinned at `-p 6 -parallel 4` by the [2026-10 hermetic sweep](concurrency-sweep.md#hermetic--p-sweep-2026-10). The output report is `docs/dev/testing/concurrency-sweep.md`; raw runs and failure output live under `/tmp/{project}/timing/`.

## Accepted measurements

The 2026-10-01 baseline uses the three eligible `iso gate all` captures `all-2`, `all-3`, and `all-4` under `$HOME/.local/state/pfm/flights/professor/fast-gate/measure/r5/`. All 79 unit package rows, the unit suite, the tagged e2e package and suite, and every gate step passed. Each capture had `EGRESS PASS`, zero `memory.swap.peak`, and an exclusive lock with zero fence containers. The `all-1` and `all-2-pre-ruling` timing-only reds are retained beside them; neither supplies a baseline span. The two new pricing rows were set to 2 s each from `all-1` before the three eligible captures.

The unit SUITE event spans were 49.790, 40.513, and 29.297 s. The maximum, rounded up and doubled, sets `unit.wall_s` to 100 s. The tagged e2e SUITE spans were 74.988, 61.079, and 45.553 s, setting `e2e.wall_s` to 150 s by the same rule. Every package row uses its own maximum. Old budgets, all three spans, and the new values are in `measure/r5/derivation.tsv`; the stored unit and e2e streams rechecked green against the new file.

The `all` gate wall budget is the maximum of its three gate walls, rounded up and doubled: 98.6 s becomes 198 s. `pfm` takes the same 198 s budget because it runs a subset of `all`.

| Target | Three gate walls (s) | Three memory peaks (MB) | Start → end 1m loads | Max gate wall (s) | Budget (s) |
| --- | --- | --- | --- | --- | --- |
| `all` | 98.6, 78.2, 61.3 | 1972, 1891, 2062 | 8.26→12.69, 8.00→10.92, 8.98→9.40 | 98.6 | 198 |
| `pfm` | subset of `all` | bounded by `all` | same captures | bounded by `all` | 198 |
| `templates` | 15.1, 14.1, 13.7 | not measured | 4.37→6.75, 3.25→4.57, 4.57→5.96 | 15.1 | 32 |

The three eligible `iso gate templates` captures `tmpl-1`, `tmpl-2`, and `tmpl-3` are under `$HOME/.local/state/pfm/flights/professor/fast-gate/measure/r7/`. Each had 34 passing steps, `EGRESS PASS`, an exclusive lock, and zero fence containers before the run. Their 1m host loads ranged 3.25–6.75. The maximum gate wall, 15.1 s, rounds up to 16 s and doubles to a 32 s `templates` budget; the unchanged 1.25 tolerance sets its limit at 40 s.

The median `all` gate wall of these three captures was 78.2 s. The memory-peak median was 1972 MB, and swap was zero in all three captures. `r5/all-1` (115.6 s, timing-only red on the superseded file) and `r4/all-1` (106.0 s, eight reds since repaired) remain outside the baseline and recheck green against the 198 s gate budget.

### Speed changes

Each change was measured before and after in the fence, on the quantity it targets. A speed change is kept when its after median beats the before median by more than max(5 %, 2 × the before MAD); a behaviour fix is kept without that rule. Evidence is each `verdict.txt` under `$HOME/.local/state/pfm/flights/professor/test-profiling/measure/`.

| Change | Verdict | Before → after median | Evidence |
| --- | --- | --- | --- |
| `-ldflags=-s` on unit test binaries | dropped | `iso gate all` STEPS 55.7 → 54.0 s (n=3 each, −3.1 %) | `7-a/` |
| e2e `t.Parallel`, concurrent `TestMain` builds, previous-release prefetch | kept | e2e package `Elapsed` 18.184 → 12.507 s (n=3, −31.2 %) | `8-b/` |
| concurrent pfm + mock-engine prebuild | kept | test-shard start to first unit test event 2.397 → 1.990 s (n=3, −17.0 %) | `8-c/` |
| longest-first unsharded package order (`--history`) | kept | unsharded stream end 23.193 → 20.657 s (n=6, −10.9 %) | `8-c/` |
| prebuilt pfm reused by check-map and mirrors (`--bin-dir`) | kept (no keep rule) | one warm pfm build, median 0.652 s (n=5), saved in each of two steps | `8-c/` |
| `arch-check.sh` checks run concurrently, one-pass C25 | kept | script wall 2.517 → 0.853 s (n=7 / n=4, −66.1 %) | `8-e/` |
| attach picker plain mode waits on the live row marker, not a 5 s sleep | kept (behaviour fix) | three plain subtests' summed `Elapsed` 15.36 → 0.78 s (n=3, −94.9 %) | `8-g/` |
| `go_test_report` single pass over `unit.json` | kept | 0.104 → 0.054 s (n=5, −48 %) | `8-h/` |
| testjail profiler test waits shortened | kept | testjail package `Elapsed` 9.538 → 5.548 s (n=3, −41.8 %) | `9-d/` |
| schedule S5: three heavy slots, phase-1 heavy steps longest first | kept | `iso gate all` STEPS 42.3 → 39.8 s (n=5, −5.9 %; peak memory 1728 MB, no OOM) | `10-b/` |

The kept schedule's `STEPS` median is 39.8 s (n=5) against the 54.0 s median of the 17 `iso gate all` captures in `$HOME/.local/state/pfm/flights/professor/test-profiling/slowness-map.md`: −14.2 s, −26.3 %.

The median S5 capture's `gate.tsv`, from `$HOME/.local/state/pfm/flights/professor/test-profiling/measure/10-b/` capture `S5-r2-3` (`run.1X6AoI`; `STEPS PASS`, `BUDGET PASS`, attribution `CODE`, `steps_heavy_jobs 3`):

```text
step	verdict	seconds
pfm.unit	PASS	31.3
pfm.e2e	PASS	30.0
pfm.self.arch-c24	PASS	0.5
pfm.self.arch-check	PASS	3.2
pfm.self.host-window	PASS	2.2
pfm.self.install-downgrade-guard	PASS	0.8
pfm.self.rollback-guard	PASS	0.3
pfm.self.skip-check	PASS	0.2
pfm.self.test-contention	PASS	2.4
pfm.self.test-shard	PASS	4.9
pfm.self.test-sweep	PASS	1.6
pfm.self.test-timing	PASS	7.1
templates.lanes.check-map	PASS	2.3
templates.lanes.checks	PASS	12.2
templates.lanes.container	PASS	0.3
templates.lanes.cred-scan	PASS	0.2
templates.lanes.egress	PASS	2.1
templates.lanes.gate-history	PASS	1.9
templates.lanes.host-backup	PASS	0.8
templates.lanes.host-rehearsal	PASS	2.5
templates.lanes.housekeeping	PASS	1.7
templates.lanes.image-key	PASS	1.2
templates.lanes.iso-housekeeping	PASS	1.7
templates.lanes.lib	PASS	4.0
templates.lanes.mcp-stdio	PASS	4.4
templates.lanes.profile-report	PASS	3.6
templates.lanes.release-rehearsal	PASS	0.1
templates.lanes.root	PASS	4.6
templates.lanes.run	PASS	9.9
templates.lanes.sampler	PASS	17.3
templates.lanes.stepprof	PASS	10.3
templates.lanes.steps	PASS	5.3
templates.demo.codex_fence_home	PASS	0.1
pfm.lint-new	PASS	6.8
pfm.fmt-check	PASS	8.5
pfm.vet	PASS	4.6
pfm.vet-darwin	PASS	2.0
pfm.arch	PASS	2.6
templates.check-map	PASS	0.5
templates.clone	PASS	0.9
templates.leak	PASS	4.8
templates.placeholders	PASS	0.1
templates.scratch-paths	PASS	3.0
templates.descriptions	PASS	0.7
templates.mirrors	PASS	4.2
templates.token-audit	PASS	3.0
templates.flight-index	PASS	0.5
templates.release-check	PASS	1.6
templates.codex-sync	PASS	0.1
templates.refresh-scope	PASS	0.6
templates.pfm-guard	PASS	0.2
templates.dev-report	PASS	0.3
templates.opencode-writer-tests	PASS	0.1
templates.skill-tests	PASS	1.3
templates.opencode-writer-refs	PASS	0.0
STEPS	PASS	39.8
BUDGET	PASS	40
```

The tagged e2e budget from the 2026-09-18 serial capture was 224 s: 09:03:42 to 09:05:36 UTC, load 10.25 → 14.63, event span 111.784 s. The r5 e2e rows supersede that budget; its raw JSON, TSV, candidates, and load evidence remain under `tmp/timing/provisional-serial-utf8-20260918/`.

The superseded unit block was re-baselined on 2026-09-27 at `-p 4 -parallel 4` from `speed-3a-1`, `speed-3a-1-repeat`, and `speed-3a-2`. Their start loads were 19.55, 26.08, and 18.25; SUITE event spans were 239.680, 227.630, and 260.828 s. Twice the rounded-up maximum gives a 522 s unit suite budget, down from the serial-calibrated 1174 s. All 76 package rows and the suite passed re-checks against that 2026-09-27 file; the independent `speed-3a-3` validation passed at 234.939 s event span and 246.774 s host wall. Raw JSON, TSV, check logs, metadata, and the derivation table are under `$HOME/.local/state/pfm/flights/professor/test-speed/rebaseline/`; the capture table, with host walls and dates, is in [concurrency-sweep.md](concurrency-sweep.md).

The 2026-09-18 serial unit stream had a stale `TestResolveOverrides` expected value, then a corrected fixture was used for its historical baseline. That serial unit capture does not contribute to the current unit budgets; its untouched and corrected artifacts remain beside the e2e evidence for audit.

Two earlier attempts remain rejected evidence: default concurrency failed under sustained load, and a serial scratch driver incorrectly forced the C locale, breaking a Unicode window-name assertion. Neither contributed a budget.

The historical slow-test inventory in [slow-tests.md](slow-tests.md) identifies tests worth investigating. Its durations were observed under contention and cannot establish isolated costs or a current budget.
