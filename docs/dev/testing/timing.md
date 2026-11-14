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

`.claude/scripts/test.sh --full` runs the same gate. The source mount stays read-only. Generated artifacts go to `/tmp/{project}/fence/shared/{worktree}/timing/` on the host, the run's `/pfm-timing`, through a separate writable mount. Each invocation gets its own `run.*` directory containing `gate.tsv`, `gate.meta`, `steps/<name>.log`, `steps/<name>.prof.tsv`, `resources.tsv`, `profile/`, `profile.tsv`, and, for the pfm steps, `unit.json`, `unit.tsv`, `unit.load`, `unit-shards/`, `bin/`, `e2e.json`, `e2e.tsv` and `e2e.load`. What each profiling artifact holds, and how to read a red gate from them, is in [profiling.md](profiling.md).

**Order.** `gate_run` registers every step in one list, and no step waits for another: a step starts as soon as a slot is free, in registration order. The list is longest first, so the steps that outlast the rest open the gate and none is left to run alone at its end: the Go test steps `pfm.unit-proof`, `pfm.e2e`, `pfm.unit` and `pfm.race` (heavy); the heavy static steps `pfm.lint-new`, `pfm.fmt-check`, `pfm.vet` and `pfm.vet-darwin`, which queue for the heavy slots the test steps free; `pfm.testplan` and `pfm.arch` (light); the light `templates.*` checks, `templates.mirrors` first; the `pfm.self.*` and `templates.fence.*` suites; and the `fixture.*` steps under `PFM_GATE_FIXTURES=1`. At most `STEPS_JOBS` steps run at once (default: the container's CPU quota rounded up, else the CPU count), and at most `STEPS_HEAVY_JOBS` heavy steps (2; native 4). The heavy cap is the gate's memory guard: a third heavy step OOM-killed `pfm.lint-new` on a cold lint cache in a 2 GB container (speed table), and native's four run under a 24g cap.

**Step bound.** Every step runs under `stepprof_run` with a bound: its own (`steps_set_bound`, used only by fixture steps) or the default `STEPS_BOUND_S`. When the caller does not set `STEPS_BOUND_S`, `gate_run` derives it from the target's budget fail limit, `budget × tolerance × fail_factor` (494 s for `all`); a budget that cannot give one falls back to 1500 s and the gate says why. A step that reaches its bound is snapshotted, TERMed, then KILLed. The bound stays the target's fail limit: a step that reaches it has by itself taken the gate past that limit, so a tighter bound would only cut evidence short, and a looser one would let a hang outlive a gate that has already failed. The `TIMEOUT` it ends with closes a hang and stays red whatever the attribution ([profiling.md](profiling.md) § Attribution, **FAIL → WARN**). On a host loaded enough to stretch one step that far, the caller sets `STEPS_BOUND_S`.

**Table.** `gate.tsv` has the header `step verdict seconds` and one row per step in registration order; a verdict is `PASS`, `FAIL`, `NOT-RUN` (the command could not start, or died of a signal) or `TIMEOUT` (the bound ended it). Then two rows:

- `STEPS <PASS|FAIL> <seconds>`: the wall from the first step's start to the last step's end, one decimal; `FAIL` when any step is not `PASS`.
- `BUDGET <PASS|WARN|FAIL|UNPINNED|ERROR> <seconds>`: the `STEPS` wall rounded up to a whole second, judged against the target's row of `infra/fence/gate-budget.yml`: `PASS` within budget × `tolerance`; `WARN` over it through `fail_factor` × that limit, and beyond it when the run's [attribution](profiling.md#attribution) is `CONTENTION`; `FAIL` beyond it otherwise; `UNPINNED` for a row marked `unpinned` (recorded, not judged); `ERROR` when there is no row, no readable budget or no `STEPS` wall.

The printed table adds pointers to each non-PASS row: the step log, the hang tree of a `TIMEOUT`, the ERR records. It is followed by the `PROFILE` block. A failed step keeps its log for diagnosis.

The gate reports test execution and timing checks as separate rows. Unit tests use the Makefile's `TESTFLAGS` (`-p` the fence's CPU cap, `-parallel 4`); tagged e2e tests use `-p 1`. `pfm.e2e` samples the VM's busy and the container's own CPU into `e2e.load` for the whole e2e run, the record the e2e timing check corrects for other load with, as the unit check does with `unit.load`. `dev.sh iso` passes `TESTFLAGS` and the knobs `STEPS_JOBS STEPS_HEAVY_JOBS STEPS_BOUND_S STEPPROF_TRACE STEPPROF_GRACE_TICKS PFM_TEST_PROFILE PFM_GATE_FIXTURES` into the container when they are set. `make timing` prints the newest unit or e2e TSV. CI retains the JSON and TSV files even after failure.

## Critical path

No step waits for another, so `STEPS` is the wall of the step that ends last: `pfm.unit-proof` or `pfm.e2e`. The gate is CPU-bound, so that wall follows the cores the container gets more than the order: its steps burn 2,250–2,420 CPU-seconds, and in capture `final5` under `$HOME/.local/state/pfm/flights/professor/gate-wall/measure/` (native profile, tick deficit 19 %) the container used 17–18 of its 20 CPUs for the first 100 s. There `pfm.unit-proof` ended the gate at 161.6 s, `pfm.e2e` ended at 151.1 s and `pfm.unit` at 126.2 s; `templates.mirrors` ended at 60.2 s and the last light step, `pfm.testplan`, at 110.5 s. The heavy static steps take the one heavy slot `pfm.race` frees at 16.6 s, one after another (`pfm.lint-new` to 26.8 s, `pfm.fmt-check` to 84.4 s, `pfm.vet` to 90.7 s, `pfm.vet-darwin` to 98.4 s), and bind nothing. From 120 s on only the two longest steps run, on about 10 CPUs. A host that leaves the container fewer cores stretches every step together: capture `final4` (tick deficit 45 %) ended at 197.1 s, `pfm.e2e` at 194.8 s. `pfm.e2e` is one package at `-p 1`, 87.4 s beside `pfm.unit` alone (capture `base`); `pfm.unit` and `pfm.unit-proof` run the same unit tier twice, plain and traced.

Reading it from one run:

1. **`gate.tsv`**: one row per step in registration order; the largest `seconds` belongs to the step that ended the gate when that step started with the gate.
2. **`steps/<name>.prof.tsv`**: `start_epoch` and `finish_epoch` place every step on one clock. Subtract the earliest `start_epoch` and sort by finish: the last finish is the end of `STEPS`, and a step whose start is above zero waited that long for a slot.
3. **Slot waits**: a heavy step waits while `STEPS_HEAVY_JOBS` heavy steps run, a light step only while `STEPS_JOBS` steps run; the registration order decides which waiting step takes a freed slot.
4. **Inside a Go step**: `unit.json` and `e2e.json` are `go test -json` streams; per package, the first event's `Time` and the terminal `pass`/`fail` event's `Time` and `Elapsed` give its start and end. The package ending last is what holds its step open; a package whose `Elapsed` far exceeds its CPU (`profile/<label>.<pid>/summary.json` `user_s` + `sys_s`) is waiting, not working.
5. **`resources.tsv`**: Δ`cg_cpu_s` ÷ (Δ`epoch_s` × `cpus`) over a window is the container's CPU use; a long stretch well under the CPU count is idle time a reordering could fill, and a stretch at the count is CPU-bound, shortened only by less work or more cores. `cg_mem_mb` is the container's memory use; `cg_mem_peak_mb` is its high-water mark since the container started, not this run's, against the memory cap (5g; native 24g, `PFM_FENCE_MEM`); `cg_swap_mb` above zero means the run was memory-bound.

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

`pfm/scripts/test-shard.sh` pins `cmd/pfm` to four shards and `internal/update` to two. In a clean plain `go test ./...` run (75.7 s), `cmd/pfm` took 55.7 s and no other package exceeded 22 s. History-run medians were 43.8 s at three shards, 43.3 s at five, and 39.2 s then 43.7 s at four under varying host load. `internal/update` took 11.9 s alone against its 10 s budget, nearly all of it about 45 `Run` tests that swap the package's seam variables and so stay serial within one process. History only orders tests across a package's shards; it never selects packages or changes shard counts. Every other package runs in one unsharded `go test` process.

The script compiles a sharded package's test binary once, lists its tests from that binary, and starts all of its shards as `go tool test2json` runs of the same binary. It starts the unsharded process first and runs it alongside the sharded package. Sharded packages run one at a time, so at most one unsharded process plus the largest shard count are direct children of the script. With `--history` (the gate passes the newest earlier `unit.json` with test events), the unsharded process lists its packages longest first by their history `Elapsed`, packages with no history first in their given order, so the slowest packages start at once instead of last.

A unit run first builds pfm and mock-engine, both at once, and starts no test before both have finished; it hands them to the tests through `PFM_TEST_PFM_BINARY` and `PFM_TEST_MOCK_ENGINE_BINARY`. A failed build leaves no binary and fails the run. They are built into a temporary directory the run removes.

The merged stream has one terminal event per package. A sharded package's `wall_s` is its slowest shard's elapsed time; its existing `pfm/.testtiming.yml` budget is judged the same way as before. `SHARD-LOST <test>` means a listed test did not run and fails the package. Each process's raw JSON stream is kept in `unit-shards/` beside `unit.json` for diagnosis.

The unit proof splits on its own table. `pfm/scripts/unit-proof.sh` runs every test binary under `strace` and filters its traces when it exits, so a package of hundreds of serial tests ends long after the rest. On Linux a package `PROOF_SHARDS` names (`internal/installer` 6 parts; `internal/hookentry`, `internal/doctor` and `internal/chat` 4 each; `UNIT_PROOF_SHARDS` replaces the table, and empty splits nothing) runs its binary as that many traced processes at once: a traced `-test.list` run names the tests, the sorted names are dealt round-robin, each part takes its names as `-test.run`, and `go test` reads the parts' output in part order. Each part's traces are filtered on their own into `reach/{slug}#{part}.tsv`, and the verdict adds a package's parts up into one row per kind and target. A test list that cannot be read or names no test, and a listed test that ran in no part, are `UNIT-PROOF ERROR` (exit 2). The proof's `go test -p` is `PFM_FENCE_TEST_P` where the fence sets it, the unit tier's package concurrency: go's own default is every CPU of the container, and the proof runs beside the unit tier.

The agent-roster suite splits by process too: `templates.mirrors` runs `scripts/check-agent-roster.test.mjs`, whose tests each wait on a process they start, under `NODE_TEST_SHARDS=8` (`infra/fence/checks.sh`). `node_test_suite` (`.claude/scripts/dev.sh`) then starts eight `node --test` processes at once, each with `NODE_TEST_SHARD={i}/8`, and the `test` export of `scripts/node-test-helpers.mjs` registers every eighth top-level test in each, so the eight together run each test once. Every process must report a passing test; one left with no test fails by name.

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

At `-p 6 -parallel 4`, the unit block, tagged e2e block, and gate walls use one rule: take the maximum of three eligible captures, round up to whole seconds (with a one-second floor), then double each package, suite, and gate target budget. Unit, tagged e2e, and `all` use captures of the full gate; `templates` uses captures of the templates-only gate (`dev.sh gate templates`, run in the fence). `pfm` takes the `all` gate budget because it runs a subset of its steps. Every budget is checked at budget × `tolerance` (`1.25`); a gate wall over that limit warns through `fail_factor` × limit and fails beyond it. The doubling absorbs shared-host load: clean `all` walls of one tree spanned 61.3–115.6 s on 2026-10-01; the slowest was the first gate after a merge, with cold build and lint caches.

## Concurrency sweep

`make sweep` runs a host preflight followed by the fenced sweep and report. Host test execution is excluded from this train. The sweep explores package concurrency and then per-package test concurrency, retaining three repetitions per point and a separate shuffle check. A pin needs the fastest point supported by at least three passing fenced captures and green timing checks.

The target enables `--wait-quiet`, which prefers load below 4 before each capture. The wait is outside the timed command and capped at five minutes; sustained load emits a warning and proceeds with high-load evidence. The raw TSV records each result; its sibling `.load.log` records capture identity and dated uptime before and after, including failed captures. Unreadable load probes fail explicitly.

The unit gate is pinned at `-p 6 -parallel 4` by the [2026-10 hermetic sweep](concurrency-sweep.md#hermetic--p-sweep-2026-10). The output report is `docs/dev/testing/concurrency-sweep.md`; raw runs and failure output live under `/tmp/{project}/fence/shared/{worktree}/timing/`.

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
| `arch-check.sh` checks run concurrently, one-pass C25 | kept | script wall 2.517 → 0.853 s (n=7 / n=4, −66.1 %) | `8-e/` |
| attach picker plain mode waits on the live row marker, not a 5 s sleep | kept (behaviour fix) | three plain subtests' summed `Elapsed` 15.36 → 0.78 s (n=3, −94.9 %) | `8-g/` |
| `go_test_report` single pass over `unit.json` | kept | 0.104 → 0.054 s (n=5, −48 %) | `8-h/` |
| testjail profiler test waits shortened | kept | testjail package `Elapsed` 9.538 → 5.548 s (n=3, −41.8 %) | `9-d/` |
| schedule S5: three heavy slots in a 2 GB container, heavy static steps longest first | dropped: OOM on cold caches; S4 (two slots, same order) kept | warm `iso gate all` STEPS 42.3 → 39.8 s (n=5, −5.9 %; peak memory 1728 MB, no OOM), but `run.HF0phs` and a cold-cache capture OOM-killed `pfm.lint-new` (`Error 137`); two heavy slots: 0 OOM in 3 cold-lint-cache captures, warm STEPS 41.9 s (n=5) | `10-b/`, `13-a/` |
| native step slots: `STEPS_JOBS` 6 → the CPU quota, `STEPS_HEAVY_JOBS` 2 → 4 | kept | `iso gate all` STEPS 310.4 → 263.8 s (n=1 each, −15.0 %) | gate-wall `base/`, `arm1/` |
| one start order, no step waiting for another | kept | `iso gate all` STEPS 263.8 → 203.9 s (n=1 each, −22.7 %) | gate-wall `arm1/`, `arm2/` |
| native caps 12 CPUs, 12g → 20 CPUs, 24g | kept | `iso gate all` STEPS 250.4 → 188.8 s (n=1 each, tick deficit 45.5 % and 44.9 %; −24.6 %) | gate-wall `arm3/`, `arm4/` |
| agent-roster suite as eight processes | kept | `templates.mirrors` alone 96.4 → 31.2 s (n=1 each, −67.6 %) | gate-wall `mirrors-before/`, `mirrors-after/` |
| unit proof split, its `go test -p` 12 → 6 | kept | `pfm.unit-proof` with no other heavy step 117.1 → 81.5 s (n=1 each, −30.4 %) | gate-wall `base/`, `proof-p6/` |

The gate-wall rows are single captures a side, on a host other work was loading, with their run tables under `$HOME/.local/state/pfm/flights/professor/gate-wall/measure/`. Together they take `iso gate all` from 310.4 s (`base/`, attribution `CODE`) to 161.6 s at a tick deficit of 19 % (`final5/`) and 197.1 s at 45 % (`final4/`). The proof's `-p` is a bound, not a speed change: while it followed the 20-CPU cap, two captures at tick deficits of 61 % and 51 % (`final2/`, `final3/`) went red on deadline tests.

Schedule S4's `STEPS` median was 41.9 s (n=5) against the 54.0 s median of the 17 `iso gate all` captures in `$HOME/.local/state/pfm/flights/professor/test-profiling/slowness-map.md`: −12.1 s, −22.4 %.

The median S5 capture's `gate.tsv`, from `$HOME/.local/state/pfm/flights/professor/test-profiling/measure/10-b/` capture `S5-r2-3` (`run.1X6AoI`; `STEPS PASS`, `BUDGET PASS`, attribution `CODE`, `steps_heavy_jobs 3`), less the rows of since-deleted steps (the OpenCode writer check's two and one fence self-test suite's one):

A capture from before the lane suite was retired (its `templates.lanes.*` steps are now `templates.fence.*`, and `templates.check-map` is gone).

```text
step	verdict	seconds
pfm.unit	PASS	31.3
pfm.e2e	PASS	30.0
pfm.self.arch-c24	PASS	0.5
pfm.self.arch-check	PASS	3.2
pfm.self.mcp-restart	PASS	0.4
pfm.self.install-downgrade-guard	PASS	0.8
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
templates.lanes.housekeeping	PASS	1.7
templates.lanes.image-key	PASS	1.2
templates.lanes.iso-housekeeping	PASS	1.7
templates.lanes.lib	PASS	4.0
templates.lanes.mcp-stdio	PASS	4.4
templates.lanes.profile-report	PASS	3.6
templates.lanes.root	PASS	4.6
templates.lanes.run	PASS	9.9
templates.lanes.sampler	PASS	17.3
templates.lanes.stepprof	PASS	10.3
templates.lanes.steps	PASS	5.3
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
templates.flight-index	PASS	0.5
templates.release-check	PASS	1.6
templates.refresh-scope	PASS	0.6
templates.dev-report	PASS	0.3
templates.skill-tests	PASS	1.3
STEPS	PASS	39.8
BUDGET	PASS	40
```

The tagged e2e budget from the 2026-09-18 serial capture was 224 s: 09:03:42 to 09:05:36 UTC, load 10.25 → 14.63, event span 111.784 s. The r5 e2e rows supersede that budget; its raw JSON, TSV, candidates, and load evidence remain under `tmp/timing/provisional-serial-utf8-20260918/`.

The superseded unit block was re-baselined on 2026-09-27 at `-p 4 -parallel 4` from `speed-3a-1`, `speed-3a-1-repeat`, and `speed-3a-2`. Their start loads were 19.55, 26.08, and 18.25; SUITE event spans were 239.680, 227.630, and 260.828 s. Twice the rounded-up maximum gives a 522 s unit suite budget, down from the serial-calibrated 1174 s. All 76 package rows and the suite passed re-checks against that 2026-09-27 file; the independent `speed-3a-3` validation passed at 234.939 s event span and 246.774 s host wall. Raw JSON, TSV, check logs, metadata, and the derivation table are under `$HOME/.local/state/pfm/flights/professor/test-speed/rebaseline/`; the capture table, with host walls and dates, is in [concurrency-sweep.md](concurrency-sweep.md).

The 2026-09-18 serial unit stream had a stale `TestResolveOverrides` expected value, then a corrected fixture was used for its historical baseline. That serial unit capture does not contribute to the current unit budgets; its untouched and corrected artifacts remain beside the e2e evidence for audit.

Two earlier attempts remain rejected evidence: default concurrency failed under sustained load, and a serial scratch driver incorrectly forced the C locale, breaking a Unicode window-name assertion. Neither contributed a budget.

The historical slow-test inventory in [slow-tests.md](slow-tests.md) identifies tests worth investigating. Its durations were observed under contention and cannot establish isolated costs or a current budget.
