# Test and step profiling

## Contents

- [Always captured](#always-captured)
- [On failure](#on-failure)
- [Where](#where)
- [Reading a red gate](#reading-a-red-gate)
- [Attribution](#attribution)
- [Ledger and report](#ledger-and-report)
- [Knobs and hazards](#knobs-and-hazards)
- [Fixtures](#fixtures)

Every `iso gate` run records what each Go test process, each gate step and the container did, without a flag. A red gate therefore carries its own evidence: a rerun is never the first move. Gate scheduling, budgets and the step table are in [timing.md](timing.md).

## Always captured

### Per Go package process

`testjail.Run` starts a profiler in every test binary that runs with `PFM_TEST_ARTIFACT_DIR` set (`checks_pfm_unit` and `checks_pfm_e2e` set it to `$run/profile`, each Go fixture step to `$run/profile/fixture-<kind>` and `checks_pfm_race` to `$run/profile/race`, so a fixture's or the race step's failure pointers name only its own bundles). Each process writes `profile/<label>.<pid>/summary.json`: written at start with `event` `started-no-exit-recorded` and `exit_code` -1, rewritten at exit. A process that execs itself away or is killed keeps the start record.

| Key | Meaning |
| --- | --- |
| `package`, `pid`, `helper_of` | label, process id, and the parent's pid when this binary is a helper re-exec'd by a test (empty otherwise) |
| `event`, `exit_code` | `exit`, `helper-exit`, `timeout-imminent`, `deadline-imminent` or `started-no-exit-recorded` |
| `wall_s`, `timeout_s`, `watchdog_s` | process wall, its `-test.timeout`, and when the watchdog fires (null when none is armed) |
| `goroutines`, `gomaxprocs`, `num_cpu` | at exit |
| `user_s`, `sys_s`, `maxrss_kb`, `inblock`, `oublock`, `nvcsw`, `nivcsw` | rusage of the process |
| `run_delay_s` | seconds the process waited for a CPU (`/proc/self/schedstat`); null with `run_delay_error` |
| `psi` | VM pressure stall seconds over the process life, `{cpu,io,memory}_{some,full}`; null with `psi_error` |
| `bundles` | the `<reason>` of every failure bundle written, never null |

The profiler also keeps a runtime flight recorder: the last 10 s (at most 16 MiB) of execution trace, written only into a failure bundle. A test that reads process-wide allocation counters (`testing.AllocsPerRun`, `runtime.MemStats`) calls `testjail.PauseFlightRecorder(t)` first: the recorder's reader goroutine allocates while it drains, and those counters count every goroutine. The pause holds for the rest of the test, its cleanup starts a fresh recorder, and the test stays serial.

### Per step

`steps_run` runs every step under `stepprof_run` (`infra/fence/stepprof.sh`), which always writes `steps/<name>.prof.tsv`, `key<TAB>value` in this order:

| Key | Meaning |
| --- | --- |
| `step`, `ended`, `rc`, `bound_s` | name; `exit` or `timeout`; exit status; the bound in seconds |
| `start_epoch`, `finish_epoch`, `wall_s` | `$EPOCHREALTIME` at start and finish, and their difference |
| `children_cpu`, `cpu_s` | bash `times` children line, and its user + sys in seconds |
| `trace`, `err_records` | `STEPPROF_TRACE` mode, and the count of `ERR` lines in `steps/<name>.xtrace` |
| `psi_<res>_<kind>_s` | VM pressure stall seconds over the step, one per line of `/proc/pressure/{cpu,io,memory}`; one `psi<TAB>UNAVAILABLE <reason>` line instead when unreadable |

With the default `STEPPROF_TRACE=err`, every bash the step starts writes one ERR record per failing command (`ERR rc=… <epoch> file:line func: command` plus its call stack) into `steps/<name>.xtrace`; the file is removed when empty. Bash fires no ERR inside an `if` or `||` condition, so the records show unexpected `set -e` deaths and expected non-zero tests alike: a PASS step with ERR records is normal. A Go test process unsets `BASH_ENV`, so a shell a Go test starts writes no record.

### Gate-wide resources

`infra/fence/sampler.sh` samples the container every `SAMPLER_INTERVAL_S` (0.5 s) into `resources.tsv` for the whole gate:

| Columns | Source |
| --- | --- |
| `epoch_s`, `uptime_s` | epoch at start plus the rise of `/proc/uptime`, so a VM clock step never reorders rows |
| `cg_cpu_s`, `vm_busy_s`, `cpus` | cgroup `cpu.stat` `usage_usec`; `/proc/stat` busy ticks; CPU count |
| `cg_io_read_mb`, `cg_io_write_mb` | cgroup `io.stat` bytes summed over devices |
| `cg_mem_mb`, `cg_mem_peak_mb`, `cg_swap_mb` | `memory.current`, `memory.peak`, `memory.swap.current` |
| `cg_psi_*_s`, `vm_psi_*_s` | cgroup and VM pressure `total=` in seconds (cpu some, io some/full, memory some/full) |
| `spin_us`, `load1` | wall µs of a fixed `SAMPLER_SPIN_ITERS` bash loop; `/proc/loadavg` field 1 |

Counters are cumulative; take differences over a window. An unreadable source is `NA` in every row of its column, never `0`, and its reason is written once to `resources.err`. The unit and e2e runners also sample VM busy and own CPU into `unit.load` and `e2e.load` for the timing checks' load correction.

### `gate.meta`

`key<TAB>value`, written when the gate starts (`finished` reads `NA running`) and rewritten at its end: `target`, `commit`, `dirty_files`, `started`, `finished` (UTC `%Y-%m-%dT%H:%M:%SZ`), `fixtures` (`1` under `PFM_GATE_FIXTURES=1`, else `0`), `steps_bound_s`, `steps_jobs`, `steps_heavy_jobs`. An unreadable value is `NA <reason>`.

## On failure

### Go bundles

A test process writes a bundle to `profile/<label>.<pid>/<reason>/`:

| Reason | When |
| --- | --- |
| `exit` | the process exits red |
| `timeout` | the watchdog fires before the binary's `-test.timeout` panic, which would run no cleanups |
| `deadline` | the watchdog fires before the step's bound, from `PFM_TEST_DEADLINE_EPOCH`, when that comes first |

The watchdog fires a tenth of the remaining time early, between 1 and 15 s; a helper process arms none. A bundle holds `DIAGNOSIS.txt` (one screen: wall, CPU, run delay, max RSS, VM pressure, the tests running at capture with their file:line, the module's other goroutines grouped by where they wait), `goroutines.txt` (full dump), `trace.out` (the flight-recorder window), `heap.pprof`, and `errors.txt` when an artifact could not be written; a paused recorder is named there.

### Step hang tree

When a step reaches its bound, `stepprof_run` snapshots the step's process tree into `steps/<name>.hang/tree.txt` (state, wchan, schedstat, open fds and the holders of every pipe or fifo it touches), sends TERM, KILLs after `STEPPROF_GRACE_TICKS` 0.1 s ticks, and ends the step 124 with `ended timeout`. The step's verdict is `TIMEOUT`.

### ERR records

The step's `steps/<name>.xtrace` (see [Per step](#per-step)). For a red shell step, the last records before the exit name the command, line and caller that failed.

## Where

`$run` is `/pfm-timing/run.XXXXXX` in the fence and `/tmp/<worktree>/timing/run.XXXXXX` on the host. Besides `steps/<name>.log`, `steps/<name>.failures`, `unit.json`, `unit.tsv`, `unit.load`, `unit-shards/`, `e2e.json` and `e2e.tsv`:

| Path under `$run` | Writer | Content |
| --- | --- | --- |
| `gate.tsv` | `steps_run` + `gate_run` | step table, `STEPS` and `BUDGET` rows ([timing.md](timing.md#gate)) |
| `gate.meta` | `gate_run` | [`gate.meta`](#gatemeta) |
| `steps/<name>.prof.tsv` | `stepprof_run` | [Per step](#per-step) |
| `steps/<name>.xtrace` | `stepprof_run` | ERR records (`err`) or full xtrace (`x`); removed when empty |
| `steps/<name>.hang/tree.txt` | `stepprof_run` | process tree, only when the bound ended the step |
| `resources.tsv`, `resources.err` | `infra/fence/sampler.sh` | [Gate-wide resources](#gate-wide-resources) |
| `e2e.load` | `pfm/scripts/test-shard.sh sample` | same header and rows as `unit.load` |
| `bin/` | `pfm/scripts/test-shard.sh run --bin-dir` | the `pfm` and `mock-engine` the unit run built |
| `profile/<label>.<pid>/summary.json` | `internal/testjail` | [Per Go package process](#per-go-package-process) |
| `profile/<label>.<pid>/<reason>/` | `internal/testjail` | [Go bundles](#go-bundles) |
| `profile/fixture-<kind>/<label>.<pid>/`, `profile/race/<label>.<pid>/` | `internal/testjail` | the same summary and bundles, one root per Go fixture step and one for the race step, listed in `INDEX.txt` |
| `profile/INDEX.txt`, `profile.tsv` | `infra/fence/profile-report.sh summary` | [Reading a red gate](#reading-a-red-gate) |
| `fixture-go-<kind>.json` | fixture steps | `go test -json` stream of the profile fixture |

`<label>` is the package directory relative to `pfm/` with `/` replaced by `_`: `internal/pricing` → `internal_pricing`, `cmd/pfm` → `cmd_pfm`, `e2e` → `e2e`.

## Reading a red gate

1. **The step table.** Each non-PASS row carries its pointers: the step log, `steps/<name>.hang/tree.txt` for `TIMEOUT`, and `steps/<name>.xtrace` when it holds ERR records. The fence prints `/pfm-timing/…`; read the same path under `/tmp/<worktree>/timing/` on the host.
2. **`PROFILE` lines under a red Go step's report.** `profile-report.sh failures` prints, per failing package, `PROFILE <import path>: <dir>/<reason>/DIAGNOSIS.txt` per bundle, `no bundle — <dir>/summary.json (event <e>, exit <c>)`, or `NOT RECORDED — no <label>.* under <profile-dir>`.
3. **The `PROFILE` block after the table.** `PROFILE gate` gives the run's verdict, wall, CPU, I/O, memory peak, swap and attribution; one `PROFILE step` line per non-PASS step, then the five slowest PASS steps, each with its attribution and its `· xtrace`, `· hang` or `· profiles` pointer (`MISSING` when the artifact is not there); `PROFILE index` names `profile/INDEX.txt`.
4. **`profile/INDEX.txt`.** Three sections: `## Go test processes` (each process directory with event, exit, wall and run delay), `## Steps` (verdict, how it ended, rc, wall, CPU, and the first ERR record), `## Resources` (the sample count, or why there is none). `profile.tsv` holds the same per-step numbers, plus a `GATE` row: `step verdict wall_s cpu_s io_mb psi_cpu_s psi_io_s psi_mem_s attribution evidence`.
5. **`DIAGNOSIS.txt`.** Start at "Tests running at capture": the test, its goroutine state and the line it sits on. "Other goroutines of this module" shows what it waits for.
6. **`go tool trace trace.out`** opens the last 10 s of scheduling, syscalls and allocation of the red process. `go tool trace -d=parsed trace.out` prints the events as text, enough to count which goroutines allocated inside a test's window.
7. **`go tool pprof heap.pprof`** for live heap at capture: `top`, `list <func>`.
8. **`tree.txt`** of a `TIMEOUT` step: the process that did not exit, what it waits in (`wchan`), and which process holds the other end of its pipe.

A step's own output is in `steps/<name>.log`; the gate's stdout holds only the table and the `PROFILE` block.

## Attribution

Every timing verdict says whether the host or the code is to blame, as one of three words rendered `attribution <WORD> (<evidence>)`: `CONTENTION`, `CODE` or `not measured`. The judge is `pfm/scripts/test-contention.sh` (`window` for one window, `windows` for a batch); it reads `resources.tsv` and thresholds from `pfm/.testcontention.yml`.

| Signal | Value over the window | Crosses when |
| --- | --- | --- |
| `tick-deficit` | (Δ`vm_busy_s` − Δ`cg_cpu_s`) ÷ (Δ`epoch_s` × `cpus`); negative means the host withheld vCPU time | ≤ `tick_deficit_max` (-0.05, -5 %) |
| `cpu-psi` | Δ`cg_psi_cpu_some_s` ÷ Δ`epoch_s` | ≥ `cpu_psi_share_min` (0.36) |
| `spin` | median `spin_us` ÷ the least 5th-percentile `spin_us` of this file and the 20 newest sibling `run.*/resources.tsv` | ≥ `spin_ratio_min` (×1.6) |
| `run-delay` | `run_delay_s` ÷ `cpu_s`, only when both are given | ≥ `run_delay_ratio_min` (1.4) |

A signal needs `min_samples` (4) rows with a value inside the window, else it is `NA (<reason>)`. The verdict is `CONTENTION` when any measured signal crosses, `CODE` when at least one is measured and none crosses, and `not measured` when none is measured. A missing or empty `resources.tsv` is `not measured (<reason>)`; a judge that fails renders `not measured (judge failed: …)`, never a silent `CODE`. The evidence names every signal: `tick-deficit +3.3% (> -5%) · cpu-psi +24.6% (< 36%) · spin ×1.4 (< ×1.6; ref …) · run-delay NA (not given)`.

**FAIL → WARN.** A fail-tier verdict whose own window the judge calls `CONTENTION` is downgraded to WARN and says `· downgraded from FAIL`: the gate budget (`BUDGET` row, whole run) and each package and SUITE row of `test-timing.sh --check` (the package's event window, or the whole stream). `CODE` and `not measured` change nothing. Every other verdict (a red test, a `TIMEOUT`, a malformed record) stays red whatever the attribution.

The spin reference is the least floor over this file and its sibling runs, a floor being a file's nearest-rank 5th-percentile `spin_us` (its least when it has 20 samples or fewer): a single sample shortened by a VM clock step no longer sets the reference, while a file whose low 5 % is short still does. When every gate turns `CONTENTION` at once, check the sibling floors.

## Ledger and report

`.claude/scripts/dev.sh iso gate` runs `infra/fence/gate-history.sh ingest <timing-base> --host-load-now` on the host after the container exits; a failed ingest prints `LEDGER-NOT-WRITTEN` and leaves the gate verdict alone. The ledger is `${PFM_GATE_HISTORY_DIR:-$HOME/.local/state/pfm/gate-history}/<project>/ledger.tsv`, append-only, header `stamp run worktree commit target step verdict wall_s cpu_s io_mb psi_cpu_s psi_io_s psi_mem_s attribution host_load`. `<project>` is the basename of the parent of `git rev-parse --git-common-dir` without a leading dot; `--project` overrides.

- **What counts as a gate:** a run whose `gate.tsv` has a `templates.leak` row and whose `gate.meta` does not say `fixtures 1`. Others are skipped. Ingest adds one row per `profile.tsv` row, `GATE` included (a run without `profile.tsv` gives one row per `gate.tsv` step plus `GATE`, unmeasured cells `NA`). A run is keyed worktree + run, so a second ingest of the same base appends nothing.
- **`gate-history.sh report [--last N] [--project P]`** (N defaults to 20): per step of the newest gate, the earlier runs' count, median and p90 (NOT-RUN rows left out), the newest wall and verdict, and a flag: `history <3` (fewer than three earlier measurements), `REGRESSION` (newest wall > 1.5 × the median, attribution `CODE`), `slow (<WORD>)` (the same slowdown under `CONTENTION` or `not measured`), `not run` / `no wall`, or `ok`.
- **Flips:** one `FLIP <step>: <before> → <after> (<run>)` line per step whose verdict differs between the last two gates.

Exit 0 done; 1 a named failure (`LEDGER-NOT-WRITTEN`, `NO LEDGER`, an unreadable base or run, a bad ledger header); 2 bad usage or an unknown project.

## Knobs and hazards

| Name | Meaning |
| --- | --- |
| `PFM_TEST_ARTIFACT_DIR` | profile root of Go test processes; unset means profiling off |
| `PFM_TEST_PROFILE` | `0` turns profiling off; `cpu` adds `cpu.pprof` per process |
| `PFM_TEST_PROFILE_PARENT` | exported by the first profiled process; a test binary inheriting it is a helper |
| `PFM_TEST_DEADLINE_EPOCH` | exported by `stepprof_run`: step start + bound, decimal epoch seconds; the watchdog fires before it |
| `STEPPROF_TRACE` | `err` (default), `x` (full xtrace with timestamps), `off` |
| `STEPPROF_GRACE_TICKS` | 0.1 s ticks between TERM and KILL at the bound, default 20 |
| `STEPPROF_PSI_DIR` | pressure directory, default `/proc/pressure` (test seam) |
| `STEPS_BOUND_S` | default per-step bound; `gate_run` derives it from the target's budget fail limit unless the caller set it |
| `SAMPLER_INTERVAL_S`, `SAMPLER_SPIN_ITERS` | default `0.5`, `10000` |
| `SAMPLER_CGROUP_DIR`, `SAMPLER_PROC_DIR` | default `/sys/fs/cgroup`, `/proc` (test seams) |
| `PFM_GATE_FIXTURES` | `1` registers the fixture steps |
| `PFM_GATE_HISTORY_DIR` | ledger root override |

`dev.sh iso` forwards `STEPS_JOBS STEPS_HEAVY_JOBS STEPS_BOUND_S STEPPROF_TRACE STEPPROF_GRACE_TICKS PFM_TEST_PROFILE PFM_GATE_FIXTURES` into the fence when set, beside `TESTFLAGS`.

Hazards:

- **`PFM_TEST_PROFILE=cpu` kills exec'd children.** The SIGPROF interval timer survives `execve`; a child program without a SIGPROF handler dies of the signal. Use it on one package whose tests exec nothing you need.
- **`STEPPROF_TRACE=x` costs about 25 % of step wall.** A capture taken with it is not a timing measurement.
- **Lane suites run nested under the gate's own stepprof** and inherit `BASH_ENV`, `PFM_TEST_DEADLINE_EPOCH`, `PFM_TEST_TIMING_DIR` and every knob the gate set. A suite that asserts on a knob calls `shtest_unset_gate_env` (`scripts/shtest.sh`) right after sourcing the harness; no suite unsets `BASH_ENV`.

## Fixtures

`PFM_GATE_FIXTURES=1 .claude/scripts/dev.sh iso gate templates` registers five deliberately red steps in phase 0, the instrument's own proof:

| Step | What it proves |
| --- | --- |
| `fixture.go-fail` | a failing Go package leaves an `exit` bundle and its `PROFILE` line |
| `fixture.go-hang` | a hang past `-timeout 20s` leaves a `timeout` bundle before the panic |
| `fixture.go-slow` | a step bound of 45 s leaves a `deadline` bundle and a `TIMEOUT` verdict |
| `fixture.shell-fail` | a red shell step leaves ERR records (`infra/fence/gate-fixtures/shell-fail.sh`) |
| `fixture.shell-hang` | a step bound of 5 s leaves `hang/tree.txt` and a `TIMEOUT` verdict |

The Go kinds run `pfm/internal/testjail/testdata/profilefixture` into `fixture-go-<kind>.json`. A fixture gate is red by design, is marked `fixtures 1` in `gate.meta`, and never enters the ledger.
