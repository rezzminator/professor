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

A unit run builds pfm once and hands it to the tests through `PFM_TEST_PFM_BINARY`.

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

The steady-state ratchet uses three independent successful captures with identical package sets and flags. Record host load and uptime before and after each capture. A failed run cannot establish or lower a budget. For the sharded unit re-baseline above, an `iso gate` run whose only red is package timing against the superseded unit block is an eligible capture when its tests and skip checks pass and it has no `SHARD-LOST`. Median durations are rounded up to seconds. Preserve all raw captures beside the measurement log so each budget can be traced back to evidence.

For the shared-host unit block at `-p 4 -parallel 4`, the current baseline uses the maximum of three passing fenced captures, rounded up and doubled per package and suite. The tagged e2e block retains its initial serial-capture rule: round up and double. Both blocks are checked at budget × `tolerance` (`1.25`). This shared-host rule does not claim a quiet-host median; a later measurement can establish one.

## Concurrency sweep

`make sweep` runs a host preflight followed by the fenced sweep and report. Host test execution is excluded from this train. The sweep explores package concurrency and then per-package test concurrency, retaining three repetitions per point and a separate shuffle check. A pin needs the fastest point supported by at least three passing fenced captures and green timing checks.

The target enables `--wait-quiet`, which prefers load below 4 before each capture. The wait is outside the timed command and capped at five minutes; sustained load emits a warning and proceeds with high-load evidence. The raw TSV records each result; its sibling `.load.log` records capture identity and dated uptime before and after, including failed captures. Unreadable load probes fail explicitly.

The unit gate is pinned at `-p 6 -parallel 4` by the [2026-10 hermetic sweep](concurrency-sweep.md#hermetic--p-sweep-2026-10). The output report is `docs/dev/testing/concurrency-sweep.md`; raw runs and failure output live under `/tmp/{project}/timing/`.

## Accepted measurements

The tagged e2e budget remains from the 2026-09-18 serial capture: 09:03:42 to 09:05:36 UTC, load 10.25 → 14.63, event span 111.784 s, rounded up and doubled to 224 s. Its raw JSON, TSV, candidates, and load evidence are under `tmp/timing/provisional-serial-utf8-20260918/`.

The 2026-09-30 sharded unit baseline uses the three eligible `iso gate` captures `all-1`, `all-2`, and `all-3` under `$HOME/.local/state/pfm/flights/professor/fast-gate/measure/r3/`. All 77 packages and the suite passed test execution; their only gate red was timing against the superseded unit block. Unit SUITE event spans were 88.992, 85.286, and 91.026 s. The maximum, rounded up and doubled, sets the unit suite budget to 184 s; the same rule sets every package row. The per-package values and old/new budgets are in `$HOME/.local/state/pfm/flights/professor/fast-gate/measure/unit-derivation.tsv`. Each capture rechecked green against the new block. The recorded one-minute host load across these captures ranged from 3.85 to 18.55.

The gate wall budgets are the rounded-up medians of three eligible 2026-09-30 captures per target. The shared host's load was recorded and did not disqualify a run.

| Target | Three WALL values (s) | Start load range | End load range | Median (s) | Budget (s) |
| --- | --- | --- | --- | --- | --- |
| `all` | 119.8, 120.2, 122.2 | 3.85–16.03 | 14.30–18.55 | 120.2 | 121 |
| `pfm` | 106.0, 105.6, 104.4 | 14.60–18.51 | 13.91–16.30 | 105.6 | 106 |
| `templates` | 14.4, 13.6, 14.4 | 6.76–7.67 | 7.23–8.08 | 14.4 | 15 |

The median `all` wall missed the ≤45 s target by 75.2 s. Its `gate.tsv` below is from `measure/r3/all-2/`; `pfm.unit` and `WALL` read FAIL because that capture was judged against the superseded unit block. The post-pin `all` run printed `budget: ✓ gate(all)`, 136 s within its 151 s tolerated ceiling, but its steps were red: seven unit package timing rows exceeded the new block and the lane map self-test found `UNMAPPED-TOOL: harvester_read`. That run is retained as `measure/r3/all-judged/`, outside the baseline.

```text
step	verdict	seconds
pfm.unit	FAIL	106.4
pfm.e2e	PASS	120.2
pfm.lint-new	PASS	74.7
templates.check-map	PASS	10.5
templates.lanes.check-map	PASS	2.9
templates.lanes.checks	PASS	0.3
templates.lanes.container	PASS	0.2
templates.lanes.cred-scan	PASS	0.4
templates.lanes.host-backup	PASS	1.0
templates.lanes.host-rehearsal	PASS	13.5
templates.lanes.housekeeping	PASS	3.7
templates.lanes.iso-housekeeping	PASS	0.7
templates.lanes.lib	PASS	6.1
templates.lanes.mcp-stdio	PASS	3.0
templates.lanes.release-rehearsal	PASS	0.1
templates.lanes.root	PASS	2.6
templates.lanes.run	PASS	2.3
templates.lanes.steps	PASS	0.8
pfm.vet	PASS	48.5
pfm.vet-darwin	PASS	48.0
pfm.fmt-check	PASS	75.1
pfm.arch	PASS	86.9
pfm.self.arch-c24	PASS	2.6
pfm.self.arch-check	PASS	2.9
pfm.self.host-window	PASS	1.9
pfm.self.install-downgrade-guard	PASS	3.0
pfm.self.rollback-guard	PASS	0.3
pfm.self.skip-check	PASS	0.3
pfm.self.test-shard	PASS	17.6
pfm.self.test-sweep	PASS	60.1
pfm.self.test-timing	PASS	62.9
templates.demo.codex_fence_home	PASS	5.3
templates.clone	PASS	27.4
templates.leak	PASS	51.3
templates.placeholders	PASS	0.5
templates.scratch-paths	PASS	18.3
templates.descriptions	PASS	7.0
templates.mirrors	PASS	20.8
templates.token-pricing	PASS	0.3
templates.token-audit	PASS	13.9
templates.release-check	PASS	16.1
templates.codex-sync	PASS	0.3
templates.refresh-scope	PASS	3.2
templates.pfm-guard	PASS	1.6
templates.dev-report	PASS	1.3
templates.opencode-writer-tests	PASS	1.5
templates.codeprobe	PASS	7.0
templates.opencode-writer-refs	PASS	0.3
WALL	FAIL	120.2
```

The superseded unit block was re-baselined on 2026-09-27 at `-p 4 -parallel 4` from `speed-3a-1`, `speed-3a-1-repeat`, and `speed-3a-2`. Their start loads were 19.55, 26.08, and 18.25; SUITE event spans were 239.680, 227.630, and 260.828 s. Twice the rounded-up maximum gives a 522 s unit suite budget, down from the serial-calibrated 1174 s. All 76 package rows and the suite passed re-checks against that 2026-09-27 file; the independent `speed-3a-3` validation passed at 234.939 s event span and 246.774 s host wall. Raw JSON, TSV, check logs, metadata, and the derivation table are under `$HOME/.local/state/pfm/flights/professor/test-speed/rebaseline/`; the capture table, with host walls and dates, is in [concurrency-sweep.md](concurrency-sweep.md).

The 2026-09-18 serial unit stream had a stale `TestResolveOverrides` expected value, then a corrected fixture was used for its historical baseline. That serial unit capture does not contribute to the current unit budgets; its untouched and corrected artifacts remain beside the e2e evidence for audit.

Two earlier attempts remain rejected evidence: default concurrency failed under sustained load, and a serial scratch driver incorrectly forced the C locale, breaking a Unicode window-name assertion. Neither contributed a budget.

The historical slow-test inventory in [slow-tests.md](slow-tests.md) identifies tests worth investigating. Its durations were observed under contention and cannot establish isolated costs or a current budget.
