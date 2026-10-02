# RR — What to embed in a Go + shell test suite so every failing or slow test leaves a diagnosis on disk without a rerun

Question: Goal: a cited map of what to EMBED in a Go + shell test suite so that every failing or slow test leaves a diagnosis on disk without a rerun. Context: Go 1.27 engine (`pfm`) tested inside a Docker Desktop linuxkit VM (kernel 7.0.x, 10 vCPU, 2 GB mem cap, cgroup v2, PSI available). The gate runs `go test -json` sharded across packages, an e2e package (`-tags e2e -p 1`), plus bash lane tests. The gate is disk-I/O bound (5.1 GB read / 4.4 GB written per 68 s gate). Timing reds were caused by host contention outside the VM (Mac load starving VM vCPUs; in-VM steal read 0%). In scope: (1) go test -json / test2json; (2) per-package profile flags and multi-package restriction, overhead; (3) runtime/trace and trace.FlightRecorder; (4) timeout behaviour, GOTRACEBACK, T.Deadline, pre-panic goroutine dumps; (5) TestMain, t.Cleanup, t.Failed, -exec, -test.testlogfile, GOFLAGS; (6) gotestsum, go-junit-report, tparse; (7) PSI / cgroup v2 / proc accounting and host vCPU starvation detection from a guest; (8) perf/eBPF in Docker Desktop; (9) shell xtrace/PS4/traps/proc state; (10) CI test analytics models. Out of scope: vendor sales material, Java/JS-only tooling. Mark unverified rather than guessing.

## Answer

Embed two layers. The always-on layer costs almost nothing. Keep the raw `go test -json` stream: since Go 1.24 it carries build failures as their own Action types ([go1.24](https://go.dev/doc/go1.24)), and since Go 1.27 it tags output lines with an `OutputType` ([go1.27](https://go.dev/doc/go1.27)). Add per-test `Elapsed` on every pass and fail event ([test2json](https://pkg.go.dev/cmd/test2json)), the cgroup v2 `cpu.stat`/`io.stat`/`memory.peak`/`*.pressure` counters of the container ([cgroup-v2](https://docs.kernel.org/admin-guide/cgroup-v2.html)), `/proc/<pid>/schedstat` run-queue delay ([lore patch](https://lore.kernel.org/lkml/5ccbef17d4bc841084ea6e6421d4e4a23b7b806f.1435654789.git.naveen.n.rao@linux.vnet.ibm.com/)), and bash xtrace on its own fd with `$EPOCHREALTIME` stamps ([bash vars](https://www.gnu.org/software/bash/manual/html_node/Bash-Variables.html), [NEWS](https://cgit.git.savannah.gnu.org/cgit/bash.git/plain/NEWS)).

The on-trigger layer captures state at the moment of failure. A `runtime/trace.FlightRecorder` ring buffer ([runtime/trace](https://pkg.go.dev/runtime/trace)) and a debug=2 goroutine profile ([runtime/pprof](https://pkg.go.dev/runtime/pprof)) should be written from a TestMain timer set before `T.Deadline` ([testing](https://pkg.go.dev/testing)). That timer is needed because the testing package's own timeout path writes profiles and panics but never runs `t.Cleanup` ([testing.go](https://github.com/golang/go/blob/4ea26e6624008a0c91f59e6ef4285fc2019f649d/src/testing/testing.go#L2947-L2982)).

In-VM steal cannot be trusted as the host-starvation signal. A steal value of 0 can mean "nothing is measuring" ([pandastack](https://www.pandastack.ai/blog/microvm-cpu-steal-time-explained/)), and QEMU documents steal accounting only for KVM ([QEMU arm cpu-features](https://www.qemu.org/docs/master/system/arm/cpu-features.html)). The starvation evidence needs a guest-side calibration probe ([arXiv 1810.01139](https://arxiv.org/abs/1810.01139)) plus a host-side sample of the VM process ([docker/for-mac#5397](https://github.com/docker/for-mac/issues/5397)).

## Map

### 1. `go test -json` / test2json stream and reporters

- **Action set.** test2json documents start, run, pause, cont, pass, bench, fail, output and skip ([test2json](https://pkg.go.dev/cmd/test2json)): "The Action field is one of a fixed set of action descriptions: start … run … pause … cont … pass … bench … fail … output … skip".
  - Go 1.25 adds `attr`: "With the `-json` flag, attributes appear as a new "attr" action." ([go1.25](https://go.dev/doc/go1.25)). The test2json page itself does not list `attr`.
- **Elapsed.** "The Elapsed field is set for 'pass' and 'fail' events. It gives the time elapsed for the specific test or the overall package test that passed or failed." ([test2json](https://pkg.go.dev/cmd/test2json))
  - This is the per-test timing to extract: take the pass/fail events that carry a `Test` field.
- **Interleaving.** "When the go command runs parallel tests in -json mode, events from different tests are interlaced; the Package field allows readers to separate them." ([test2json](https://pkg.go.dev/cmd/test2json), unquoted in verification)
- **Build events (Go 1.24).** "`go test -json` now reports build output and failures in JSON, interleaved with test result JSON. These are distinguished by new `Action` types, but if they cause problems … you can revert to the text build output with GODEBUG setting `gotestjsonbuildtext=1`." ([go1.24](https://go.dev/doc/go1.24))
  - In code, they are a separate `BuildEvent{ImportPath, Action, Output}` with Action `"build-output"` / `"build-fail"` ([printer.go](https://github.com/golang/go/blob/4ea26e6624008a0c91f59e6ef4285fc2019f649d/src/cmd/go/internal/load/printer.go#L89-L126)).
  - The failing test's `fail` event carries `FailedBuild` ([test.go](https://github.com/golang/go/blob/4ea26e6624008a0c91f59e6ef4285fc2019f649d/src/cmd/go/internal/test/test.go#L1514-L1525)).
  - The tracer found these first in tag go1.24.0. `gotestjsonbuildtext=1` also drops `FailedBuild` ([testflag.go](https://github.com/golang/go/blob/4ea26e6624008a0c91f59e6ef4285fc2019f649d/src/cmd/go/internal/test/testflag.go#L348-L363)).
- **OutputType (Go 1.27).** "`go test -json` now annotates `"Action":"output"` lines with an optional new field `"OutputType"` … the possible values include "error", "error-continue", and "frame"." ([go1.27](https://go.dev/doc/go1.27))
  - test2json: "frame - test framing, such as '=== RUN ...' or '--- FAIL: ...' error - an error produced by Error(f) or Fatal(f) error-continue - continuation of a multi-line error" ([test2json](https://pkg.go.dev/cmd/test2json)).
  - The tracer places the field as absent at go1.26.0 and present at go1.27.0 ([test2json main.go](https://github.com/golang/go/blob/4ea26e6624008a0c91f59e6ef4285fc2019f649d/src/cmd/test2json/main.go#L34-L102)).
  - Use `error` lines to pull the failure message without parsing text.
- **`-json` implies verbose.** cmd/go injects `-test.v=test2json` and deletes any `v` taken from GOFLAGS ([testflag.go](https://github.com/golang/go/blob/4ea26e6624008a0c91f59e6ef4285fc2019f649d/src/cmd/go/internal/test/testflag.go#L348-L356)).
- **gotestsum.** Quotes from [cmd/main.go](https://raw.githubusercontent.com/gotestyourself/gotestsum/main/cmd/main.go):
  - `--jsonfile`: "write all TestEvents to file".
  - `--jsonfile-timing-events`: "write only the pass, skip, and fail TestEvents to the file".
  - `--rerun-fails`: "rerun failed tests until they all pass, or attempts exceeds maximum. Defaults to max 2 reruns when enabled".
  - `--rerun-fails-report`: "write a report to the file, of the tests that were rerun".
  - `--rerun-fails-max-failures`: "do not rerun any tests if the initial run has more than this number of failures".
  - `tool slowest` default threshold: `100*time.Millisecond` ([slowest.go](https://raw.githubusercontent.com/gotestyourself/gotestsum/main/cmd/tool/slowest/slowest.go)).
  - `--post-run-command` receives `TESTS_TOTAL`, `TESTS_FAILED`, `TESTS_SKIPPED`, `TESTS_ERRORS`, `GOTESTSUM_JSONFILE` and `GOTESTSUM_ELAPSED` ([README](https://raw.githubusercontent.com/gotestyourself/gotestsum/main/README.md), unquoted).
  - What it adds over raw `-json`: a persisted file, a timing-only file, a rerun report, a slow-test tool and a post-run hook.
- **go-junit-report v2.** "JSON produced by `go test -json` is supported by the `gojson` parser." It emits JUnit XML and needs `2>&1` for build errors ([go-junit-report](https://github.com/jstemmer/go-junit-report), unquoted).
- **tparse.** `-all` adds "elapsed time of each passed test", sorted "longest to shortest" ([tparse](https://github.com/mfridman/tparse), unquoted).

### 2. Profile flags, overhead, execution trace

- **Still refused for multiple packages.** `base.Fatalf("cannot use %s flag with multiple packages", testProfile())` on master ([test.go#L777](https://github.com/golang/go/blob/4ea26e6624008a0c91f59e6ef4285fc2019f649d/src/cmd/go/internal/test/test.go#L777-L779)).
  - `testProfile()` covers `-blockprofile`, `-cpuprofile`, `-memprofile`, `-mutexprofile` and `-trace`. `-coverprofile` is not covered ([test.go#L614](https://github.com/golang/go/blob/4ea26e6624008a0c91f59e6ef4285fc2019f649d/src/cmd/go/internal/test/test.go#L614-L629)).
  - The tracer found no commit relaxing it. The proposal [golang/go#71996](https://github.com/golang/go/issues/71996) is still open as a proposal (unquoted).
  - The Go 1.27 notes mention no relaxation ([go1.27](https://go.dev/doc/go1.27), absence per fetch).
  - Consequence: under the sharded gate, profile per package. Either invoke `go test` once per package with `-outputdir`, or embed collection inside the test binary via TestMain.
- **Test binary kept.** "The test flags that generate profiles (other than for coverage) also leave the test binary in pkg.test for use when analyzing the profiles." ([test.go raw](https://raw.githubusercontent.com/golang/go/master/src/cmd/go/internal/test/test.go))
- **Rates when profiles are requested.** Defaults are `test.blockprofilerate` 1 and `test.mutexprofilefraction` 1. They are applied only when `-test.blockprofile` / `-test.mutexprofile` is set, so every event is recorded ([testing.go](https://github.com/golang/go/blob/4ea26e6624008a0c91f59e6ef4285fc2019f649d/src/testing/testing.go#L2767-L2802)).
- **Overhead.**
  - Go diagnostics doc: "enabling some profiles (e.g. the CPU profile) adds cost" and "it is recommended to collect only a single profile at a time." It gives no figure ([diagnostics](https://go.dev/doc/diagnostics)).
  - Block profile: "production workloads suffering up to 4% overhead under this setting" (rate 10,000).
  - Goroutine profile: "One O(N) stop-the-world pauses where N is the number of goroutines. Expect ~1-10µsec pause per goroutine."
  - CPU signal rate "can be up to `N * 100Hz`". Memory sampling is "one allocation every `512KiB`".
  - The block, goroutine, CPU and memory figures are all from [DataDog go-profiler-notes guide](https://raw.githubusercontent.com/DataDog/go-profiler-notes/main/guide/README.md). The guide gives no CPU-profiler percentage.
- **Execution tracer overhead.** "Prior to Go 1.21, the run-time overhead of tracing was somewhere between 10–20% CPU"; now "down to 1–2% for many applications"; "the work landed in Go 1.22" ([execution-traces-2024](https://go.dev/blog/execution-traces-2024)).
- **Goroutine dump formats.** `debug=2` prints "the goroutine stacks in the same form that a Go program uses when dying due to an unrecovered panic" ([runtime/pprof](https://pkg.go.dev/runtime/pprof)).
- **Goroutine leak profile.** It is GA in Go 1.27: "named `goroutineleak`, is supported in the `runtime/pprof` package" ([go1.27](https://go.dev/doc/go1.27)).
  - Go 1.26 introduced it as an experiment and said "not incur any additional run-time overhead unless it is actively in-use" ([go1.26](https://go.dev/doc/go1.26)).
  - Candidate for a TestMain post-run check.

### 3. runtime/trace FlightRecorder

- **API.** Added in go1.25.0 ([runtime/trace](https://pkg.go.dev/runtime/trace)).
  - "At most one flight recorder may be active at any given time, though flight recording is allowed to be concurrently active with a trace consumer using trace.Start."
  - "Only one goroutine may execute WriteTo at a time."
  - WriteTo errors "if another WriteTo call is already in-progress, or if the flight recorder is inactive."
- **Defaults when config fields are zero.** `targetSize = 10 << 20 // 10 MiB` and MinAge `10 * time.Second`. The public doc calls them "implementation defined" ([flightrecorder.go](https://github.com/golang/go/blob/4ea26e6624008a0c91f59e6ef4285fc2019f649d/src/runtime/trace/flightrecorder.go#L47-L62)).
- **Sizing.** "MinAge … we suggest setting it to around 2x the time window of the event."
  - "a few MB of trace data to be produced per second of execution, or 10 MB/s for a busy service" ([flight-recorder blog](https://go.dev/blog/flight-recorder)).
  - Under a 2 GB memory cap, set MaxBytes explicitly (inference).
- **Predecessor.** "a flight recorder experiment, available in the golang.org/x/exp/trace package" ([execution-traces-2024](https://go.dev/blog/execution-traces-2024)).
- **Crash flush (Go 1.23).** "The runtime now explicitly flushes trace data when a program crashes due to an uncaught panic." ([go1.23](https://go.dev/doc/go1.23), unquoted)
- **TestMain usage (inference, unverified).** No source shows the FlightRecorder in TestMain. The pattern that follows from the API:
  - Start it in TestMain.
  - In each test's `t.Cleanup`, if `t.Failed()`, `WriteTo` a file named after `t.Name()`, serialised by a mutex because only one WriteTo may run at a time.
  - Also write it from the deadline timer in §4.

### 4. Timeout behaviour

- **Testing-package alarm.** `startAlarm` arms an `AfterFunc` that runs `m.after()`, which writes the requested profiles. Then it calls `debug.SetTraceback("all")`, then `panic("test timed out after …")` with a sorted `running tests:` list rounded to the second ([testing.go#L2947](https://github.com/golang/go/blob/4ea26e6624008a0c91f59e6ef4285fc2019f649d/src/testing/testing.go#L2947-L2982)).
  - The tracer dates `SetTraceback("all")` to go1.6 and the running list to go1.20 (commit ad5d2f64fbb9).
  - The timeout panic therefore dumps all goroutines regardless of GOTRACEBACK.
- **Cleanups do not run on timeout** (inferred from code). The alarm path calls only `m.after()` and never reaches `tRunner`'s cleanups ([testing.go#L2092](https://github.com/golang/go/blob/4ea26e6624008a0c91f59e6ef4285fc2019f649d/src/testing/testing.go#L2092-L2198)).
  - On panic and on normal paths, `doPanic` calls `t.Fail()` before `t.runCleanup`, so `t.Failed()` inside a Cleanup is accurate there.
  - A `t.Cleanup`-based dump never fires on a timeout. It needs a separate deadline timer.
- **Kill.** cmd/go's kill deadline is `timeout + max(1m, testWaitDelay)`, where `testWaitDelay = max(timeout/10, 5s)` ([test.go#L819](https://github.com/golang/go/blob/4ea26e6624008a0c91f59e6ef4285fc2019f649d/src/cmd/go/internal/test/test.go#L819-L855)).
  - At that deadline it sends `SIGQUIT` on Unix, which produces a second goroutine dump ([test.go#L1686](https://github.com/golang/go/blob/4ea26e6624008a0c91f59e6ef4285fc2019f649d/src/cmd/go/internal/test/test.go#L1686-L1703)).
  - It then prints `*** Test killed with quit: ran too long` ([test.go#L1750](https://github.com/golang/go/blob/4ea26e6624008a0c91f59e6ef4285fc2019f649d/src/cmd/go/internal/test/test.go#L1750-L1757)).
- **`T.Deadline`.** "Deadline reports the time at which the test binary will have exceeded the timeout specified by the -timeout flag." ([testing](https://pkg.go.dev/testing))
  - It returns ok=false when no timeout is set ([testing.go#L2325](https://github.com/golang/go/blob/4ea26e6624008a0c91f59e6ef4285fc2019f649d/src/testing/testing.go#L2325-L2338)).
  - The `-timeout` default is "10 minutes (10m)" ([cmd/go](https://pkg.go.dev/cmd/go#hdr-Testing_flags)).
  - Pattern (inference): in TestMain, after `flag.Parse()`, read the `test.timeout` flag. Arm a timer at timeout − margin that writes `pprof.Lookup("goroutine").WriteTo(f, 2)`, the FlightRecorder window and a cgroup/proc snapshot, before the alarm's panic.
- **GOTRACEBACK.** `all` "adds stack traces for all user-created goroutines". `system` is "like 'all' but adds stack frames for run-time functions and shows goroutines created internally". `crash` is "like 'system' but crashes in an operating system-specific manner instead of exiting" ([runtime](https://pkg.go.dev/runtime)).
- **SetCrashOutput (go1.23.0).** "SetCrashOutput configures a single additional file where unhandled panics and other fatal errors are printed, in addition to standard error." ([runtime/debug](https://pkg.go.dev/runtime/debug#SetCrashOutput))
  - Calling it from TestMain puts the timeout panic dump into a file (inference).

### 5. TestMain, per-test hooks, flags

- **TestMain and Cleanup.** `m.Run` "returns an exit code to pass to os.Exit". Cleanup functions are "called in last added, first called order" ([testing](https://pkg.go.dev/testing)).
- **Test API by version** ([testing](https://pkg.go.dev/testing)):
  - `t.Context()` (go1.24.0) "is canceled just before Cleanup-registered functions are called".
  - `t.Output()` (go1.25.0) returns "a Writer that writes to the same test output stream as TB.Log".
  - `t.Attr` (go1.25.0).
  - `t.ArtifactDir()` (go1.26.0) "returns a directory in which the test should store output files. When the -artifacts flag is provided, this directory is located under the output directory."
  - This is the native home for per-test diagnosis files (inference).
- **`-exec`.** "Run the test binary using xprog. The behavior is the same as in 'go run'" ([cmd/go](https://pkg.go.dev/cmd/go)).
  - A wrapper can run `/usr/bin/time -v`, sample cgroup files and keep stderr per package (inference).
- **`-test.testlogfile` is not a diagnostic log.** It records Getenv/Stat/Open/Chdir calls for test-cache keying ([testlog/log.go](https://github.com/golang/go/blob/4ea26e6624008a0c91f59e6ef4285fc2019f649d/src/internal/testlog/log.go#L5-L21), [test.go#L2015](https://github.com/golang/go/blob/4ea26e6624008a0c91f59e6ef4285fc2019f649d/src/cmd/go/internal/test/test.go#L2015-L2075)).
- **GOFLAGS.** "A space-separated list of -flag=value settings to apply to go commands by default … Flags listed on the command line are applied after this list and therefore override it." ([helpdoc.go](https://raw.githubusercontent.com/golang/go/master/src/cmd/go/internal/help/helpdoc.go))
- **`-fullpath`.** The `-test.fullpath` option (Go 1.21) prints full paths ([go1.21](https://go.dev/doc/go1.21)).

### 6. cgroup v2, PSI and proc accounting (inside the container)

- **cpu.stat.** "It always reports the following three stats … usage_usec, user_usec, system_usec and the following five when the controller is enabled … nr_periods, nr_throttled, throttled_usec, nr_bursts, burst_usec" ([cgroup-v2](https://docs.kernel.org/admin-guide/cgroup-v2.html)).
- **io.stat.** Example: "8:16 rbytes=1459200 wbytes=314773504 rios=192 wios=353 dbytes=0 dios=0" ([cgroup-v2](https://docs.kernel.org/admin-guide/cgroup-v2.html)).
  - This matters most for an I/O-bound gate: take a per-test delta of rbytes/wbytes (inference).
- **memory.events.** Includes "oom_kill" and "oom_group_kill" ([cgroup-v2](https://docs.kernel.org/admin-guide/cgroup-v2.html)).
- **memory.peak.** "The max memory usage recorded for the cgroup and its descendants since either the creation of the cgroup or the most recent reset for that FD." ([cgroup-v2](https://docs.kernel.org/admin-guide/cgroup-v2.html))
  - The write handlers came in Linux 6.12 ([kernelnewbies 6.12](https://kernelnewbies.org/Linux_6.12)).
  - Reset is per fd, so keep one fd open per measurement window ([patch email](https://www.mail-archive.com/linux-kselftest@vger.kernel.org/msg16486.html)).
- **Pressure files.** Each cgroup has cpu/memory/io `.pressure` "the format is the same as the /proc/pressure/ files" ([psi.rst](https://www.kernel.org/doc/Documentation/accounting/psi.rst)).
  - Each pressure line has "some" and "full" with avg10/avg60/avg300 plus "total absolute stall time (in us)". "CPU full is undefined at the system level … set to zero" ([psi](https://docs.kernel.org/accounting/psi.html)).
  - irq.pressure is Linux 6.1: "a new PSI_IRQ to IRQ/SOFTIRQ pressure" ([kernelnewbies 6.1](https://kernelnewbies.org/Linux_6.1)). It needs `CONFIG_IRQ_TIME_ACCOUNTING`, which linuxkit master leaves unset ([linuxkit config](https://github.com/linuxkit/linuxkit/blob/master/kernel/6.12.x/config-x86_64)).
- **Use the container's cgroup files for container-scoped values.** Docker's default "cgroup namespace mode … is `private` on v2" ([Docker runmetrics](https://docs.docker.com/engine/containers/runmetrics/)).
  - LXCFS 7.0 "now virtualizes the /proc/pressure files" ([lxcfs news](https://linuxcontainers.org/lxcfs/news/)). That implies `/proc/pressure` is VM-wide without LXCFS (inference).
- **PSI in Docker Desktop's kernel.** linuxkit master x86_64 6.12 has "# CONFIG_PSI is not set", alongside `CONFIG_TASK_DELAY_ACCT=y`, `CONFIG_TASK_IO_ACCOUNTING=y` and "# CONFIG_PARAVIRT_TIME_ACCOUNTING is not set" ([linuxkit config](https://github.com/linuxkit/linuxkit/blob/master/kernel/6.12.x/config-x86_64)).
  - You report PSI is available on your 7.0.x kernel, so Docker Desktop's kernel differs. Probe at runtime and record "absent" explicitly.
- **`/proc/<pid>/schedstat`.** Three fields: "time spent on the cpu", "time spent waiting on a runqueue", "# of timeslices" ([sched-stats](https://www.kernel.org/doc/Documentation/scheduler/sched-stats.rst)).
  - It exists under `CONFIG_SCHED_INFO` and prints "0 0 0" only when `sched_info_on()` is false ("booted with nodelayacct") ([lore patch](https://lore.kernel.org/lkml/5ccbef17d4bc841084ea6e6421d4e4a23b7b806f.1435654789.git.naveen.n.rao@linux.vnet.ibm.com/)).
  - It sees guest runqueue wait, not host descheduling (inference).
- **`/proc/<pid>/io`.** `read_bytes`: "The number of bytes really fetched from the storage layer." `write_bytes`: "The number of bytes really sent to the storage layer." ([proc_pid_io](https://man7.org/linux/man-pages/man5/proc_pid_io.5.html))
- **`/proc/stat` steal.** "Stolen time, which is the time spent in other operating systems when running in a virtualized environment" ([proc_stat](https://man7.org/linux/man-pages/man5/proc_stat.5.html)).
- **getrusage and `time -v`.**
  - getrusage: `ru_maxrss` "(in KiB)". `ru_nivcsw` counts switches due to "a higher priority process becoming runnable or because the current process exceeded its time slice" ([getrusage](https://man7.org/linux/man-pages/man2/getrusage.2.html)).
  - `time -v` exposes the same counters ([time(1)](https://man7.org/linux/man-pages/man1/time.1.html)).
  - Go: `ProcessState.SysUsage()` "returns resource usage information about the exited process" ([os](https://pkg.go.dev/os#ProcessState.SysUsage)).
- **Delay accounting.** "Delay accounting is disabled by default at boot up … Alternatively, use sysctl kernel.task_delayacct to switch the state at runtime." ([delay-accounting](https://docs.kernel.org/accounting/delay-accounting.html))

### 7. Host vCPU starvation detection from inside the guest

- **Steal 0 is not evidence of no contention.** "A steal column that reads 0.00 forever means one of two very different things: nothing is contending, or nothing is measuring." Also: "KVM has to advertise the steal-time capability to the guest. If it doesn't, the guest never registers a page and never sees a nonzero value." ([pandastack](https://www.pandastack.ai/blog/microvm-cpu-steal-time-explained/))
  - QEMU documents steal accounting only as `kvm-steal-time`: "enabled by default when KVM is enabled" ([QEMU arm cpu-features](https://www.qemu.org/docs/master/system/arm/cpu-features.html)). The page says nothing of hvf.
  - linuxkit master builds without `CONFIG_PARAVIRT_TIME_ACCOUNTING` ([linuxkit config](https://github.com/linuxkit/linuxkit/blob/master/kernel/6.12.x/config-x86_64)).
- **Hypervisor in use.** "Starting with Docker Desktop 4.86, it uses Docker's own VMM implementation instead of `libkrun`" ([Docker VMM](https://docs.docker.com/desktop/features/vmm/)). Record which VMM ran in each ledger row (inference).
- **Calibration probe.** "The theoretical execution time of a deterministic microbenchmark is compared to its execution time in a virtualized environment … this solution … can compute the steal time."
  - The probe is a dependent `add` chain run 10M times, about 420 ms, timed with the monotonic clock. The paper states its limits: single-core VMs and fixed frequency ([arXiv 1810.01139](https://arxiv.org/abs/1810.01139)).
  - Embed a short version before and after the gate, and on a slow test (inference).
- **Kernel log signals.** "One possibility for a softlockup report in a Linux VM, is that the host system is overcommitted to the point where the watchdog task is unable to make progress." ([LKML](https://lkml.iu.edu/hypermail/linux/kernel/1306.3/03321.html))
  - Red Hat lists "kernel: hrtimer: interrupt took ###### ns" and vCPU "time-jump" ([Red Hat 5008811](https://access.redhat.com/articles/5008811), unquoted).
  - Capture `dmesg` on failure (inference).
- **Clock-jump detection.** clocktick_jumps measures "scheduling latencies at a low level, with as little involvement from the operating system as possible" ([nokia/clocktick_jumps](https://github.com/nokia/clocktick_jumps), unquoted).
- **Go scheduler trace.** "setting schedtrace=X causes the scheduler to emit a single line to standard error every X milliseconds" ([runtime](https://pkg.go.dev/runtime)).
- **Host-side sample.** The Docker Desktop VM shows on macOS as `com.apple.Virtualization.VirtualMachine`: "Process com.apple.Virtualization.VirtualMachine runs at 100% CPU all the time when Docker daemon is up" ([docker/for-mac#5397](https://github.com/docker/for-mac/issues/5397)). This is an old version, so the process name may differ under Docker VMM.
  - Have the host-side runner log that process's CPU and the macOS load beside each gate run (inference; macOS sampling commands unsourced).

### 8. perf / eBPF in Docker Desktop

- **perf_event_paranoid.** "Controls use of the performance events system by unprivileged users (without CAP_PERFMON). The default value is 2." ([sysctl kernel](https://docs.kernel.org/admin-guide/sysctl/kernel.html))
  - "Starting from Linux v5.9 CAP_SYS_PTRACE capability is not required and CAP_PERFMON is enough" ([perf-security](https://www.kernel.org/doc/html/latest/admin-guide/perf-security.html), unquoted).
- **Headers.** "eBPF tools such as BCC and bpftrace … rely on Linux kernel headers. These do not ship with the Docker Desktop for macOS VM." ([Malmgren](https://petermalmgren.com/docker-mac-bpf-perf/))
  - BTF exists on recent Docker Desktop kernels ("`6.4.16-linuxkit`" onward) ([Tetragon FAQ](https://tetragon.io/docs/installation/faq/)).
  - "bpftrace automatically reads BTF of the running kernel … it is not necessary to use the `#include` directive" ([bpftrace](https://bpftrace.org/blog/kernel-includes)).
  - docker-bpf "will automatically mount linuxkit kernel headers, BTF and debugfs into the container" ([hemslo/docker-bpf](https://github.com/hemslo/docker-bpf)).
- **Verdict.** Feasible with `--privileged` or CAP_PERFMON/CAP_BPF, as an on-demand tool rather than always-on. Hardware PMU counters in the guest are unverified.

### 9. Shell lane diagnostics

- **BASH_XTRACEFD.** "Bash writes the trace output generated when 'set -x' is enabled to that file descriptor, instead of the standard error." ([bash vars](https://www.gnu.org/software/bash/manual/html_node/Bash-Variables.html)) NEWS lists it under Bash-4.1 ([NEWS](https://cgit.git.savannah.gnu.org/cgit/bash.git/plain/NEWS)).
- **EPOCHREALTIME.** "expands to the time in seconds since the Unix epoch with microsecond granularity" (Bash-5.0, [NEWS](https://cgit.git.savannah.gnu.org/cgit/bash.git/plain/NEWS)).
  - PS4 "is expanded like `PS1`" and "The first character of the expanded value is replicated multiple times, as necessary, to indicate multiple levels of indirection" ([bash vars](https://www.gnu.org/software/bash/manual/html_node/Bash-Variables.html)).
  - So `PS4='+ ${EPOCHREALTIME} ${BASH_SOURCE}:${LINENO}: '` costs no fork. No source shows this exact recipe; one blog uses a `$(date "+%s.%N")` fork instead ([linuxbash.sh](https://linuxbash.sh/post/xtracefd)).
- **Traps and set options** ([set builtin](https://www.gnu.org/software/bash/manual/html_node/The-Set-Builtin.html), [builtins](https://www.gnu.org/software/bash/manual/html_node/Bourne-Shell-Builtins.html)):
  - `-E`: "any trap on `ERR` is inherited by shell functions, command substitutions, and commands executed in a subshell environment".
  - `pipefail`: "the return value of a pipeline is the value of the last (rightmost) command to exit with a non-zero status".
  - EXIT: "action is executed when the shell exits".
  - `BASH_COMMAND` gives the failing command inside the trap ([bash vars](https://www.gnu.org/software/bash/manual/html_node/Bash-Variables.html)).
- **Process-state capture** in an ERR/EXIT trap:
  - `/proc/pid/wchan` is "The symbolic name corresponding to the location in the kernel where the process is sleeping", under a PTRACE_MODE_READ check ([wchan](https://man7.org/linux/man-pages/man5/proc_pid_wchan.5.html)).
  - `/proc/pid/fd` links are readable under PTRACE_MODE_READ_FSCREDS. The link format is `type:[inode]`, which lets you match fifo and pipe holders ([fd](https://man7.org/linux/man-pages/man5/proc_pid_fd.5.html)).
  - `/proc/pid/status` State is one of "R (running)", "S (sleeping)", "D (disk sleep)" and the rest ([status](https://man7.org/linux/man-pages/man5/proc_pid_status.5.html)).
  - `ps opid,wchan:42,cmd` gives explicit width control ([ps](https://man7.org/linux/man-pages/man1/ps.1.html)).
- **`/proc/pid/stack` needs root in the initial namespace.** "Restrict the ability to inspect kernel stacks of arbitrary tasks to root"; the code checks `file_ns_capable(..., &init_user_ns, CAP_SYS_ADMIN)` ([LKML backport](https://lkml.iu.edu/hypermail/linux/kernel/1812.1/00748.html)).
  - An unprivileged container cannot read it, so use wchan and status instead.

### 10. CI test analytics models (for a local append-only ledger)

- **Datadog.**
  - Flaky test: "A flaky test is a test that exhibits both a passing and failing status across multiple test runs for the same commit." ([Datadog](https://docs.datadoghq.com/tests/flaky_test_management/))
  - Regression: "A test run is marked as a regression when its duration is both five times the mean and greater than the max duration for the same test in the default branch", with the baseline mean "calculated over the last week of test runs" ([Datadog explorer](https://docs.datadoghq.com/tests/explorer/)).
  - Early Flake Detection retries new tests "up to ten times" ([EFD](https://docs.datadoghq.com/tests/early_flake_detection/)).
  - Auto Test Retries "retry any failing test case up to 5 times" ([ATR](https://docs.datadoghq.com/tests/flaky_tests/auto_test_retries/)).
- **Trunk** ([Trunk detection](https://docs.trunk.io/flaky-tests/detection)):
  - Same-commit rule: "A test fails then passes on the same commit (retry after failure)".
  - Rate monitor: "Failure rate exceeds a configured percentage over a time window".
  - Slow-test monitor: "a test's average duration exceeds a configured threshold".
  - Timeout Inflation: "a test's typical failure duration is much larger than its passing duration".
- **BuildPulse.**
  - "failure and success for the same code … comparing the git tree SHA" ([BuildPulse](https://docs.buildpulse.io/flaky-tests/overview)).
  - Statistical mode: "Detection Minimum Count … (default: 10)" and "Detection Disruption Percentage … (default: 30%)" ([BuildPulse quarantining](https://docs.buildpulse.io/flaky-tests/guides/Test%20Quarantining)).
- **Buildkite** ([Buildkite monitors](https://buildkite.com/docs/pipelines/configure/tests/workflows/monitors)):
  - Passed on retry: "a test that both passes and fails on the same git commit SHA". It recovers after "seven days or 100 executions".
  - Transition: "a change from passing to failing, or failing to passing, in a sequence of results for a test over time".
  - PFS "was developed by Meta, and uses a Bayesian statistical model".
  - Duration monitor "triggers when the aggregated duration over a sliding window crosses a configured threshold".
- **Ledger row these models imply** (inference): test id, package, commit or tree SHA, outcome, Elapsed, retry index, timestamp, environment fingerprint (VMM, kernel, cgroup limits, PSI present). The models then reduce to:
  - Flake: same SHA, mixed outcomes.
  - Duration regression: more than 5× the mean and above the max over a 7-day window.
  - Transition count over the last N runs.

## Coverage

- Go `-json` stream & reporters: settled
- Profiles, trace, FlightRecorder: settled (code facts: `tracer-rr/golang-go-2026-10-01.md`)
- Timeout & per-test hooks: settled (code facts: `tracer-rr/golang-go-2026-10-01.md`)
- cgroup v2 / PSI / proc accounting: partial. The kernel version that introduced memory.peak is unquoted, the VM-wide scope of `/proc/pressure` is only inferred, and Docker Desktop's own kernel PSI config is unsourced.
- Host vCPU starvation + perf/eBPF: partial. No source states directly that Apple VZ or Docker VMM provide no steal accounting, macOS host-side sampling commands are unsourced, and guest PMU availability is unverified.
- Shell diagnostics: settled
- CI analytics models: settled

Digging ended: round 3 settled nothing and added no sub-area.

## Verification

44 statements checked on 12 pages: test2json, go1.27, execution-traces-2024, flight-recorder blog, DataDog profiler guide, gotestsum main.go, cgroup-v2, pandastack, Datadog explorer, Buildkite monitors, QEMU arm cpu-features, LKML softlockup.

NOT ON PAGE:
- `attr` action on the test2json page. Removed from that page's citation; the fact is now cited to go1.25.
- A CPU-profiler overhead percentage in the DataDog guide. The absence is stated in the map.
- The gotestsum `--rerun-fails-max-failures` default of 10. Dropped.
- hvf steal time on the QEMU page. The absence is stated.

UNCHECKED: none.

## Open rabbit holes

- Apple Virtualization.framework / Docker VMM paravirtual steal (PV_TIME) support: dug, unsettled. An empirical check is to run `grep ^cpu /proc/stat` while the host is loaded.
- Docker Desktop's shipped kernel `CONFIG_PSI`, and the aarch64 linuxkit config: dug, unsettled. Read `/proc/config.gz` in the VM.
- Kernel version that introduced memory.peak (5.19 claimed by a search snippet only): dug, unsettled.
- A primary kernel statement that `/proc/pressure` is not cgroup-namespaced: dug, unsettled.
- A worked example of FlightRecorder or a goroutine dump from TestMain before `T.Deadline`: dug, unsettled (pattern inferred).
- macOS host-side load sampling (`host_statistics`, `powermetrics`, `sysctl vm.loadavg`) and the current Docker VMM process name: dug, unsettled.
- Guest PMU / hardware perf counters under Apple hypervisors: dug, unsettled.
- test2json output-attribution caveats with parallel tests (go.dev/issue/37555, test2json reporting pass on a failed binary): undug.
- Meta's probabilistic flakiness score paper; Google "Flaky Tests at Google"; FlakeFlagger: undug.
- Cost of polling cgroup/PSI files from a sampler, and PSI's own overhead: dug, unsettled.
