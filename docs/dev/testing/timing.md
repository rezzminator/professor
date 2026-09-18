# Test timing budgets

`pfm/scripts/test-timing.sh` reads Go test JSON, writes a TSV, checks suite budgets, and lowers budgets from three successful captures. `.claude/scripts/dev.sh` owns the unit and tagged e2e test commands. `pfm/Makefile` supplies `TESTFLAGS`; the CI workflow delegates to the same fenced gate.

## Gate

From the worktree root:

```bash
.claude/scripts/dev.sh iso test pfm
```

The source mount stays read-only. Generated JSON and TSV artifacts go to the worktree's ignored `tmp/timing/` directory through a separate writable mount. Each invocation gets its own `run.*` directory containing `unit.json`, `unit.tsv`, `e2e.json`, and `e2e.tsv`. Failed test output remains available for diagnosis.

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

New package budgets and deliberate increases require an explicit edit with its evidence in the commit. `--measure` only lowers existing budgets. Architecture baselines under `pfm/.arch/` are separate and are not changed by timing measurements.

## Checks and measurement

Run the parser inside the fence against captures available on its artifact mount:

```bash
.claude/scripts/dev.sh iso run 'bash pfm/scripts/test-timing.sh --check --suite unit --out /pfm-timing/check.tsv /pfm-timing/run/unit.json'
```

Malformed or incomplete input, unreadable budgets, missing dependencies, failed tests, missing packages, and exceeded budgets must remain distinct from success. The shell fixture suites exercise these failure paths through `dev.sh iso run`.

The steady-state ratchet uses three independent successful captures with identical package sets and flags. Record host load and uptime before and after each capture. A failed run cannot establish or lower a budget. Median durations are rounded up to seconds. Preserve all raw captures beside the measurement log so each budget can be traced back to evidence.

The initial shared-host ratchet is explicitly provisional: one successful serial capture is rounded up and doubled. This exception establishes a loose first ceiling without claiming a quiet-host median. W5 replaces it with the normal three-capture measurement and may only shrink the checked-in values.

## Concurrency sweep

`make sweep` runs a host preflight followed by the fenced sweep and report. Host test execution is excluded from this train. The sweep explores package concurrency and then per-package test concurrency, retaining three repetitions per point and a separate shuffle check. A failed or incomplete sweep cannot pin `TESTFLAGS`.

The target enables `--wait-quiet`, which prefers load below 4 before each capture. The wait is outside the timed command and capped at five minutes; sustained load emits a warning and proceeds with high-load evidence. The raw TSV records each result; its sibling `.load.log` records capture identity and dated uptime before and after, including failed captures. Unreadable load probes fail explicitly.

Conservative `-p 1 -parallel 1` flags remain in effect until W5 supplies a successful sweep recommendation. The output report is `docs/dev/testing/concurrency-sweep.md`; raw runs and failure output live under `tmp/timing/`.

## Accepted measurements

The first accepted budgets are **provisional — re-measure at W5 close**. The serial unit capture ran from 08:51:51 to 09:01:45 UTC at load 20.54 → 8.89 and measured a 586.849 s event span. The tagged e2e capture ran from 09:03:42 to 09:05:36 UTC at load 10.25 → 14.63 and measured 111.784 s. Rounded values were doubled to unit 1174 s and e2e 224 s; every package budget was doubled by the same rule. Raw JSON, TSV, candidates and load evidence are retained under `tmp/timing/provisional-serial-utf8-20260918/`.

The raw unit stream's sole package failure was the stale W6-A `TestResolveOverrides` expected value, which omitted the newly derived activity-log path. Its focused fenced rerun passed after the expectation was corrected. `unit-validated.json` preserves the original timestamps and changes only that test and package's two terminal fail actions; the untouched `unit.json` and `paths-fixed.json` remain beside it for audit.

Two earlier attempts remain rejected evidence: default concurrency failed under sustained load, and a serial scratch driver incorrectly forced the C locale, breaking a Unicode window-name assertion. Neither contributed a budget.

The historical slow-test inventory in [slow-tests.md](slow-tests.md) identifies tests worth investigating. Its durations were observed under contention and cannot establish isolated costs or a current budget.
