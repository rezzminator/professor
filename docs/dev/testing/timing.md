# Test timing budgets

## Contents

- [Gate](#gate)
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

Use `.claude/scripts/dev.sh iso gate pfm` for pfm alone. The source mount stays read-only. Generated artifacts go to `/tmp/{project}/timing/` through a separate writable mount. Each invocation gets its own `run.*` directory containing `gate.tsv`, `steps/<name>.log`, `unit.json`, `unit.tsv`, `unit-shards/`, `e2e.json`, and `e2e.tsv` for applicable steps. The gate runs tests first, then static checks; at most `STEPS_JOBS` steps run at once (default: the CPU count), and at most `STEPS_HEAVY_JOBS` heavy Go steps run at once (default: 2). It prints the step table and judges `WALL` against `infra/fence/gate-budget.yml`. A failed step keeps its log for diagnosis.

The gate reports test execution and timing checks as separate rows. Unit tests use the Makefile's `TESTFLAGS`; tagged e2e tests use `-p 1`. Environment overrides reach the container. `make timing` prints the newest unit or e2e TSV. CI retains the JSON and TSV files even after failure.

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

The script compiles the `cmd/pfm` test binary once, lists its tests from that binary, and starts all four shards as `go tool test2json` runs of the same binary. It starts the unsharded process first and runs it alongside the sharded package. Sharded packages run one at a time, so at most one unsharded process plus the largest shard count are direct children of the script.

A unit run builds pfm and mock-engine once each and hands them to the tests through `PFM_TEST_PFM_BINARY` and `PFM_TEST_MOCK_ENGINE_BINARY`.

The merged stream has one terminal event per package. A sharded package's `wall_s` is its slowest shard's elapsed time; its existing `pfm/.testtiming.yml` budget is judged the same way as before. `SHARD-LOST <test>` means a listed test did not run and fails the package. Each process's raw JSON stream is kept in `unit-shards/` beside `unit.json` for diagnosis.

## Budget file

`pfm/.testtiming.yml` contains `tolerance` and `suites.<name>.wall_s` plus a package-to-seconds map at `suites.<name>.packages`. Unit and e2e package sets are independent. The initial tolerance is 1.25: a duration above budget times tolerance fails.

New package budgets and deliberate increases require an explicit edit with its evidence in the commit. A `TESTFLAGS` change re-baselines the unit block by hand from three passing captures at the new flags: take the maximum event span for each package and the suite, round up to whole seconds with a one-second floor, then double. The capture set and per-package derivation table are retained with the change. `--measure` only lowers existing budgets. Architecture baselines under `pfm/.arch/` are separate and are not changed by timing measurements.

## Checks and measurement

Run the parser inside the fence against captures available on its artifact mount:

```bash
.claude/scripts/dev.sh iso run 'bash pfm/scripts/test-timing.sh --check --suite unit --out /pfm-timing/check.tsv /pfm-timing/run/unit.json'
```

Malformed or incomplete input, unreadable budgets, missing dependencies, failed tests, missing packages, and exceeded budgets must remain distinct from success. The shell fixture suites exercise these failure paths through `dev.sh iso run`.

The unit runner writes `unit.load` beside `unit.json`, sampling the VM's busy CPU seconds from `/proc/stat` and the fence container's own CPU seconds from `/sys/fs/cgroup/cpu.stat` from before the first test process through after the last. For each package's event window and the suite window, the checker uses the samples bracketing that window: `f = ((busy₁ − busy₀) − (own₁ − own₀)) / ((time₁ − time₀) × cpus)`, clamped to 0–0.9. It compares elapsed time with `budget × tolerance / (1 − f)`. An overage explained by that measured other load prints `TIMING CORRECTED`; an unexplained one remains `TIMING FAIL`. A missing or unavailable record prints `TIMING: other load not measured` and leaves the limits uncorrected. A malformed record is `TIMING-UNREADABLE`, exit 2.

The steady-state ratchet uses three independent successful captures with identical package sets and flags. Record host load and uptime before and after each capture. A failed run cannot establish or lower a budget. For the sharded unit re-baseline above, an `iso gate` run whose only red is package timing against the superseded unit block is an eligible capture when its tests and skip checks pass and it has no `SHARD-LOST`. Preserve all raw captures beside the measurement log so each budget can be traced back to evidence.

At `-p 6 -parallel 4`, the unit block, tagged e2e block, and gate walls use one rule: take the maximum of three eligible captures, round up to whole seconds (with a one-second floor), then double each package, suite, and gate target budget. Unit, tagged e2e, and `all` use `iso gate all` captures; `templates` uses `iso gate templates` captures. `pfm` takes the `all` gate budget because it runs a subset of its steps. Every budget is checked at budget × `tolerance` (`1.25`). The doubling absorbs shared-host load: clean `all` walls of one tree spanned 61.3–115.6 s on 2026-10-01; the slowest was the first gate after a merge, with cold build and lint caches.

## Concurrency sweep

`make sweep` runs a host preflight followed by the fenced sweep and report. Host test execution is excluded from this train. The sweep explores package concurrency and then per-package test concurrency, retaining three repetitions per point and a separate shuffle check. A pin needs the fastest point supported by at least three passing fenced captures and green timing checks.

The target enables `--wait-quiet`, which prefers load below 4 before each capture. The wait is outside the timed command and capped at five minutes; sustained load emits a warning and proceeds with high-load evidence. The raw TSV records each result; its sibling `.load.log` records capture identity and dated uptime before and after, including failed captures. Unreadable load probes fail explicitly.

The unit gate is pinned at `-p 6 -parallel 4` by the [2026-10 hermetic sweep](concurrency-sweep.md#hermetic--p-sweep-2026-10). The output report is `docs/dev/testing/concurrency-sweep.md`; raw runs and failure output live under `/tmp/{project}/timing/`.

## Accepted measurements

The 2026-10-01 baseline uses the three eligible `iso gate all` captures `all-2`, `all-3`, and `all-4` under `$HOME/.local/state/pfm/flights/professor/fast-gate/measure/r5/`. All 79 unit package rows, the unit suite, the tagged e2e package and suite, and every gate step passed. Each capture had `EGRESS PASS`, zero `memory.swap.peak`, and an exclusive lock with zero fence containers. The `all-1` and `all-2-pre-ruling` timing-only reds are retained beside them; neither supplies a baseline span. The two new pricing rows were set to 2 s each from `all-1` before the three eligible captures.

The unit SUITE event spans were 49.790, 40.513, and 29.297 s. The maximum, rounded up and doubled, sets `unit.wall_s` to 100 s. The tagged e2e SUITE spans were 74.988, 61.079, and 45.553 s, setting `e2e.wall_s` to 150 s by the same rule. Every package row uses its own maximum. Old budgets, all three spans, and the new values are in `measure/r5/derivation.tsv`; the stored unit and e2e streams rechecked green against the new file.

The `all` gate wall budget is the maximum of its three WALL values, rounded up and doubled: 98.6 s becomes 198 s. `pfm` takes the same 198 s budget because it runs a subset of `all`.

| Target | Three WALL values (s) | Three memory peaks (MB) | Start → end 1m loads | Max WALL (s) | Budget (s) |
| --- | --- | --- | --- | --- | --- |
| `all` | 98.6, 78.2, 61.3 | 1972, 1891, 2062 | 8.26→12.69, 8.00→10.92, 8.98→9.40 | 98.6 | 198 |
| `pfm` | subset of `all` | bounded by `all` | same captures | bounded by `all` | 198 |
| `templates` | 15.1, 14.1, 13.7 | not measured | 4.37→6.75, 3.25→4.57, 4.57→5.96 | 15.1 | 32 |

The three eligible `iso gate templates` captures `tmpl-1`, `tmpl-2`, and `tmpl-3` are under `$HOME/.local/state/pfm/flights/professor/fast-gate/measure/r7/`. Each had 34 passing steps, `EGRESS PASS`, an exclusive lock, and zero fence containers before the run. Their 1m host loads ranged 3.25–6.75. The maximum WALL, 15.1 s, rounds up to 16 s and doubles to a 32 s `templates` budget; the unchanged 1.25 tolerance sets its limit at 40 s.

The median `all` wall fell from r3's 120.2 s to 78.2 s, but still misses the ≤45 s target by 33.2 s. The memory-peak median was 1972 MB, and swap was zero in all three captures. `r5/all-1` (115.6 s, timing-only red on the superseded file) and `r4/all-1` (106.0 s, eight reds since repaired) remain outside the baseline and recheck green against the 198 s gate budget.

The median capture's `gate.tsv`, from `measure/r5/all-3/run.rlWIks/`:

```text
step	verdict	seconds
pfm.unit	PASS	56.2
pfm.e2e	PASS	63.6
pfm.self.arch-c24	PASS	1.4
pfm.self.arch-check	PASS	4.7
pfm.self.host-window	PASS	3.3
pfm.self.install-downgrade-guard	PASS	2.4
pfm.self.rollback-guard	PASS	0.9
pfm.self.skip-check	PASS	0.8
pfm.self.test-shard	PASS	3.8
pfm.self.test-sweep	PASS	2.7
pfm.self.test-timing	PASS	5.3
templates.lanes.check-map	PASS	3.8
templates.lanes.checks	PASS	0.8
templates.lanes.container	PASS	0.4
templates.lanes.cred-scan	PASS	0.4
templates.lanes.egress	PASS	3.4
templates.lanes.host-backup	PASS	1.1
templates.lanes.host-rehearsal	PASS	4.8
templates.lanes.housekeeping	PASS	4.8
templates.lanes.image-key	PASS	4.0
templates.lanes.iso-housekeeping	PASS	3.4
templates.lanes.lib	PASS	4.6
templates.lanes.mcp-stdio	PASS	3.2
templates.lanes.release-rehearsal	PASS	0.2
templates.lanes.root	PASS	4.1
templates.lanes.run	PASS	6.6
templates.lanes.steps	PASS	1.9
templates.demo.codex_fence_home	PASS	0.1
pfm.lint-new	PASS	8.7
pfm.vet	PASS	5.9
pfm.vet-darwin	PASS	2.3
pfm.fmt-check	PASS	6.4
pfm.arch	PASS	8.9
templates.check-map	PASS	2.2
templates.clone	PASS	1.1
templates.leak	PASS	6.2
templates.placeholders	PASS	0.1
templates.scratch-paths	PASS	3.9
templates.descriptions	PASS	0.7
templates.mirrors	PASS	5.4
templates.token-audit	PASS	4.4
templates.flight-index	PASS	0.6
templates.release-check	PASS	2.3
templates.codex-sync	PASS	0.1
templates.refresh-scope	PASS	0.6
templates.pfm-guard	PASS	0.2
templates.dev-report	PASS	0.2
templates.opencode-writer-tests	PASS	0.2
templates.skill-tests	PASS	2.4
templates.opencode-writer-refs	PASS	0.0
WALL	PASS	78.2
```

The tagged e2e budget from the 2026-09-18 serial capture was 224 s: 09:03:42 to 09:05:36 UTC, load 10.25 → 14.63, event span 111.784 s. The r5 e2e rows supersede that budget; its raw JSON, TSV, candidates, and load evidence remain under `tmp/timing/provisional-serial-utf8-20260918/`.

The superseded unit block was re-baselined on 2026-09-27 at `-p 4 -parallel 4` from `speed-3a-1`, `speed-3a-1-repeat`, and `speed-3a-2`. Their start loads were 19.55, 26.08, and 18.25; SUITE event spans were 239.680, 227.630, and 260.828 s. Twice the rounded-up maximum gives a 522 s unit suite budget, down from the serial-calibrated 1174 s. All 76 package rows and the suite passed re-checks against that 2026-09-27 file; the independent `speed-3a-3` validation passed at 234.939 s event span and 246.774 s host wall. Raw JSON, TSV, check logs, metadata, and the derivation table are under `$HOME/.local/state/pfm/flights/professor/test-speed/rebaseline/`; the capture table, with host walls and dates, is in [concurrency-sweep.md](concurrency-sweep.md).

The 2026-09-18 serial unit stream had a stale `TestResolveOverrides` expected value, then a corrected fixture was used for its historical baseline. That serial unit capture does not contribute to the current unit budgets; its untouched and corrected artifacts remain beside the e2e evidence for audit.

Two earlier attempts remain rejected evidence: default concurrency failed under sustained load, and a serial scratch driver incorrectly forced the C locale, breaking a Unicode window-name assertion. Neither contributed a budget.

The historical slow-test inventory in [slow-tests.md](slow-tests.md) identifies tests worth investigating. Its durations were observed under contention and cannot establish isolated costs or a current budget.
