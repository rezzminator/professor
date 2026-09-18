# Wave 2 — timing ratchet + the concurrency sweet spot

**Why:** the user's law — tests must never become the next bottleneck. A budget nobody measures is a wish; this wave makes suite time a gated, ratcheting number and pins the parallelism knobs from measurement, not folklore.
**Worktree:** `.worktrees/testing-foundation`. Files: `pfm/scripts/test-timing.sh`, `pfm/scripts/test-sweep.sh`, `pfm/.testtiming.yml`, `pfm/Makefile` (`test`, `timing`, `sweep` targets), `docs/dev/testing/timing.md` (report), `.github/workflows/verify.yml` (artifact upload). Guarded: `.claude/scripts/dev.sh` `test pfm` row → GOD via `/pcm`.

## Baseline (fence, develop @ 8bfb6cdc, 15 vCPU) — filled from `tmp/logs/baseline-*.json`

**Contention notice (coordinator finding, confirmed):** this specific run shared the host with a second, concurrent `pfm-dev` image build/test run (another wave's `internal/hookentry`/`internal/reload` work) at load average ≈23 on 15 vCPU — the outer default-concurrency invocation took longer than the serial invocation; the table below measures Go event span and excludes image/build work before the first event. These clocks must not be compared as the same measurement. The old `remeasure-timing.txt` captures failed and cannot establish budgets. The initial ratchet therefore uses one successful serial capture at the host's unavoidable shared load, doubled for a provisional ceiling and explicitly scheduled for re-measure at W5 close. Per-test `Elapsed` values in `docs/dev/testing/slow-tests.md` are observations under contention, not isolated costs: `-p 1` serializes packages, while `t.Parallel` tests within a package and external host workloads can still overlap.

| run | event span | packages | tests | slowest package | slowest test |
| --- | --- | --- | --- | --- | --- |
| `go test -count=1 ./...` (default `-p`=15) | 436.66s (FAIL) | 64 | 2,332 top-level (3,432 incl. subtests) | `hostops/pfm/cmd/pfm` (436.66s) | `TestInternalLaunchPrintExecsDirectlyWithoutTmux` (117.7s, `cmd/pfm`) |
| `go test -count=1 -p 1 ./...` | 711.38s (FAIL) | 64 | 2,332 top-level (3,432 incl. subtests) | `hostops/pfm/cmd/pfm` (295.26s) | `TestInternalLaunchPrintExecsDirectlyWithoutTmux` (94.46s, `cmd/pfm`) |
| `go test -count=1 -tags e2e -p 1 ./e2e/...` | 97.59s (PASS) | 1 | 13 | `hostops/pfm/e2e` (97.59s) | `TestInstallInitUpdateUninstallE2E` (53.22s, `hostops/pfm/e2e`) |

Both unit rows report **FAIL**: `hostops/pfm/cmd/pfm`'s real-tmux attach test (`TestJailedEvalAttachFromPlainAndNestedTmux`, `attach_jail_test.go:72`) failed its subtests under contention in both runs (`eval/bunker/picker` and `raw/inside-tmux/picker` in the default-`-p` run; `raw/inside-tmux/picker` in the `-p 1` run), plus `TestKillSelfResolveAndInternalCLI` (`main_test.go:347`) in the `-p 1` run only — a "did not switch client to target session" timing assertion sensitive to host CPU pressure, flagged `flaky-under-load` in the slow-test ledger. Out of this wave's scope to fix (`cmd/pfm` is not an owned file); named for W3/whoever owns `cmd/pfm` reliability. The `-tags e2e` row is clean (PASS) on both runs.

The accepted first ratchet is provisional. On 2026-09-18, one fenced serial
unit capture measured 586.849 s while host load moved from 20.54 to 8.89; one
tagged e2e capture measured 111.784 s while load moved from 10.25 to 14.63.
Rounded values and every package value were doubled, producing suite ceilings
of 1174 s and 224 s. `TESTFLAGS` is `-p 1 -parallel 1`. W5 must replace this
shared-host evidence with the normal three-capture median and a complete
concurrency sweep; the ratchets may only shrink.

## Deliverables

1. **`pfm/scripts/test-timing.sh`** — reads `go test -json` (from stdin or a file) and emits `tmp/timing/<suite>-<stamp>.tsv`: `package · wall_s · tests · parallel_tests · slowest_test · slowest_s · status`, plus a `SUITE` row (wall, package count, the serial sum — the ratio serial/wall is the achieved parallelism). With `--check`, it compares every package and the suite against `pfm/.testtiming.yml` and exits 1 naming each offender with its budget and measured value; a package absent from the budget file is reported `UNBUDGETED` (red), never silently allowed. With `--measure`, it pins any package whose median-of-3 is BELOW its current budget (ratchet down only); raising a budget is a hand edit, named in the commit — same law as `arch-check.sh`. Tolerance: budget × 1.25 (timing noise), documented in the yml header. Its own broken state: unparsable JSON → `TIMING-UNREADABLE` exit 2, never a green.
2. **`pfm/.testtiming.yml`** — `tolerance` plus `suites.<name>.wall_s` and `suites.<name>.packages: {import path: max_s}`; initial pins from the baseline median-of-3 rounded up to the next second, with separate unit and tagged e2e package sets. Heavy-test dispositions live in the slow-test ledger.
3. **`pfm/scripts/test-sweep.sh`** — the RND runner: two one-dimensional sweeps, never the full matrix (a 5×4×2 matrix × 3 reps is hours of fence CPU for no extra knowledge): first `-p ∈ {1,2,4,8,nproc}` with `-parallel` = GOMAXPROCS, then `-parallel ∈ {1,4,8,16}` at the measured package knee; each `-count=1`, warm build cache (a test-cache warm-up is recorded separately; `go clean -testcache` does not clear the build cache); 3 repetitions, median; it refuses to start while another fence test run is live (checks `docker ps` for a `pfm-dev` run container and says so); emits `docs/dev/testing/concurrency-sweep.md` with the table, the knee (first point where the next step gains < 10 %), CPU-seconds vs wall (from Bash `time`, available in the fence image), and the pinned recommendation written into `pfm/Makefile` as `TESTFLAGS ?= -p <package-knee> -parallel <test-knee>`. Runs inside the fence only under the hand-off law. The report names the host leg as not run. Host preflight requires reachable Docker and no other fence test container. Low load is preferred; `--wait-quiet` warns and proceeds after at most five minutes when the shared host stays busy. Until W5 produces a complete sweep, the provisional gate uses `-p 1 -parallel 1` rather than claiming a measured knee.
4. **Slow-test ledger** — from the baseline JSON: every test ≥ 1 s ranked, classified by reading it (sleep-bound · real subprocess/tmux · filesystem-heavy · CPU · network-mocked-but-slow), written to `docs/dev/testing/slow-tests.md` with the W3 disposition per row (mock the seam → U, move → A, or keep with a justification). This ledger is W3's priority list.
5. **Wiring** — `make test` uses `TESTFLAGS` and pipes through `test-timing.sh --check`; `make timing` prints the last TSV; `make sweep` runs the RND; `dev.sh test pfm` (GOD) gains the timing check as its own row (`pfm: test timing (budget)`); CI uploads the TSV artifact. Tier A's e2e row is timed the same way; Wave 4's lane runner emits the same TSV shape so one script reads all three tiers.

## Red-first proof

`test-timing.sh --check` against a fixture JSON with one package over budget must exit 1 naming it (watched red before the parser exists: the script absent → the Makefile target fails; then the fixture case). `test-sweep.sh` is measured by its report, not a unit test; its dry-run (`--plan`) prints the matrix and is asserted in a shell test under `pfm/scripts/` beside `arch-check.sh`'s tests (find how those are tested first; mirror it).

## Done when

- Baseline table filled with real numbers; provisional serial `TESTFLAGS` and doubled first-capture budgets pinned with their load evidence; W5 re-measure named; `make test` red on a synthetic over-budget package and green on the tree; `iso test pfm` + `iso verify pfm` green on the committed tree; the slow-test ledger handed to W3 with a disposition on every row.

## Prior art (RR-2, `.professor/RR/agent-fleet-testing-release-chain-2026-09-17.md`)

- **mise** ships `test:shuffle` (order-sensitivity) and `TEST_TRANCHE`/`TEST_TRANCHE_COUNT` sharding — adopted: `-shuffle=on` is a third leg of the sweep (a shuffle-only failure is a shared-state bug, reported by name, never a flake), and `test-timing.sh`'s per-package TSV is the input a later tranche split would need; the split itself is not built here.
- **Goose** forces `--jobs 1` on its scenario suite — Tier A's `-p 1` is the same rule; the sweep's job is to find how far the SEAMED unit tier can go, not to re-derive that.
- **Baseline finding (2026-09-17):** the first fence baseline ran beside two image builds at load ≈23 and came back `P15 1334 s · P1 733 s · E2E 104 s` with four real-tmux failures — parallel slower than serial is a contention signature, not a suite property. The measurement rule: the sweep refuses to start while another `pfm-dev` container is alive, and every capture records its load average. Initial budget establishment requires three successful, complete captures per suite. A failed capture cannot feed `--measure`.
- **No neighbour documents a flaky-test quarantine policy** (chezmoi, mise, goose, opencode, claude-squad all silent) — RR-1 owns that question; until it lands, the slow-test ledger's `flaky-under-load` rows are the only quarantine and each carries a file:line and an owner.

## Prior art (RR-1, `.professor/RR/hermetic-trustworthy-test-gates-2026-09-17.md`)

- **Flake root causes** (Luo et al., FSE 2014, Google TAP data): async wait 45 %, concurrency 20 %, order dependency 12 % — and 24 % of "flaky" fixes changed the code under test, 94 % of those real bugs. Law for the slow-test ledger: a `flaky-under-load` row is a bug ticket with an owner, never a quarantine; reruns fire only for tests already on the ledger (Google's rule), tracked per configuration (`-p`, `-parallel`, shuffle), never per test alone.
- **No published parallelism sweet spot and no CI-time ratchet mechanism** were found in two rounds — the sweep and `.testtiming.yml` are original work; their numbers are this repo's, taken under a recorded load average, and the ten-minute build (Fowler) is the guideline the unit-tier budget answers to.
- **Google's small-test definition** — one thread, one process, one machine — is Tier U's definition; large tests default to 15 min / 1 h timeouts and "suffer a lack of standardization" — which is what Wave 4's per-lane budgets exist to prevent.
- **`testing/synctest`** (a fake clock plus durable-blocking detection inside a goroutine bubble; `synctest.Wait()` replaces sleep-polling) — experimental in Go 1.24 (`GOEXPERIMENT=synctest`), GA in 1.25: a candidate to replace `clock.Fake` for the async-wait class once the toolchain moves; noted, not adopted at 1.24.
