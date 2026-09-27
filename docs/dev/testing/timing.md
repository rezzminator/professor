# Test timing budgets

`pfm/scripts/test-timing.sh` reads Go test JSON, writes a TSV, checks suite budgets, and lowers budgets from three successful captures. `.claude/scripts/dev.sh` owns the unit and tagged e2e test commands. `pfm/Makefile` supplies `TESTFLAGS`; the CI workflow delegates to the same fenced gate.

## Gate

From the worktree root:

```bash
.claude/scripts/dev.sh iso test pfm
```

The source mount stays read-only. Generated JSON and TSV artifacts go to `/tmp/{project}/timing/` through a separate writable mount. Each invocation gets its own `run.*` directory containing `unit.json`, `unit.tsv`, `e2e.json`, and `e2e.tsv`. Failed test output remains available for diagnosis.

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

## Budget file

`pfm/.testtiming.yml` contains `tolerance` and `suites.<name>.wall_s` plus a package-to-seconds map at `suites.<name>.packages`. Unit and e2e package sets are independent. The initial tolerance is 1.25: a duration above budget times tolerance fails.

New package budgets and deliberate increases require an explicit edit with its evidence in the commit. A `TESTFLAGS` change re-baselines the unit block by hand from three passing captures at the new flags: take the maximum event span for each package and the suite, round up to whole seconds with a one-second floor, then double. The capture set and per-package derivation table are retained with the change. `--measure` only lowers existing budgets. Architecture baselines under `pfm/.arch/` are separate and are not changed by timing measurements.

## Checks and measurement

Run the parser inside the fence against captures available on its artifact mount:

```bash
.claude/scripts/dev.sh iso run 'bash pfm/scripts/test-timing.sh --check --suite unit --out /pfm-timing/check.tsv /pfm-timing/run/unit.json'
```

Malformed or incomplete input, unreadable budgets, missing dependencies, failed tests, missing packages, and exceeded budgets must remain distinct from success. The shell fixture suites exercise these failure paths through `dev.sh iso run`.

The steady-state ratchet uses three independent successful captures with identical package sets and flags. Record host load and uptime before and after each capture. A failed run cannot establish or lower a budget. Median durations are rounded up to seconds. Preserve all raw captures beside the measurement log so each budget can be traced back to evidence.

For the shared-host unit block at `-p 4 -parallel 4`, the current baseline uses the maximum of three passing fenced captures, rounded up and doubled per package and suite. The tagged e2e block retains its initial serial-capture rule: round up and double. Both blocks are checked at budget × `tolerance` (`1.25`). This shared-host rule does not claim a quiet-host median; a later measurement can establish one.

## Concurrency sweep

`make sweep` runs a host preflight followed by the fenced sweep and report. Host test execution is excluded from this train. The sweep explores package concurrency and then per-package test concurrency, retaining three repetitions per point and a separate shuffle check. A pin needs the fastest point supported by at least three passing fenced captures and green timing checks.

The target enables `--wait-quiet`, which prefers load below 4 before each capture. The wait is outside the timed command and capped at five minutes; sustained load emits a warning and proceeds with high-load evidence. The raw TSV records each result; its sibling `.load.log` records capture identity and dated uptime before and after, including failed captures. Unreadable load probes fail explicitly.

The unit gate is pinned at `-p 4 -parallel 4`; a later sweep may move it. The output report is `docs/dev/testing/concurrency-sweep.md`; raw runs and failure output live under `/tmp/{project}/timing/`.

## Accepted measurements

The tagged e2e budget remains from the 2026-09-18 serial capture: 09:03:42 to 09:05:36 UTC, load 10.25 → 14.63, event span 111.784 s, rounded up and doubled to 224 s. Its raw JSON, TSV, candidates, and load evidence are under `tmp/timing/provisional-serial-utf8-20260918/`.

The unit block was re-baselined on 2026-09-27 at `-p 4 -parallel 4` from `speed-3a-1`, `speed-3a-1-repeat`, and `speed-3a-2`. Their start loads were 19.55, 26.08, and 18.25; SUITE event spans were 239.680, 227.630, and 260.828 s. Twice the rounded-up maximum gives a 522 s unit suite budget, down from the serial-calibrated 1174 s. All 76 package rows and the suite pass re-checks against the new file; the independent `speed-3a-3` validation passes at 234.939 s event span and 246.774 s host wall. Raw JSON, TSV, check logs, metadata, and the derivation table are under `$HOME/.local/state/pfm/flights/professor/test-speed/rebaseline/`; the capture table, with host walls and dates, is in [concurrency-sweep.md](concurrency-sweep.md).

The 2026-09-18 serial unit stream had a stale `TestResolveOverrides` expected value, then a corrected fixture was used for its historical baseline. That serial unit capture does not contribute to the current unit budgets; its untouched and corrected artifacts remain beside the e2e evidence for audit.

Two earlier attempts remain rejected evidence: default concurrency failed under sustained load, and a serial scratch driver incorrectly forced the C locale, breaking a Unicode window-name assertion. Neither contributed a budget.

The historical slow-test inventory in [slow-tests.md](slow-tests.md) identifies tests worth investigating. Its durations were observed under contention and cannot establish isolated costs or a current budget.
