---
paths:
  - "pfm/**/*.go"
  - "pfm/**/testdata/**"
  - "pfm/scripts/*_test.sh"
  - "infra/**/*_test.sh"
  - "infra/**/tests/**"
  - "infra/fence/gate-fixtures/**"
  - "scripts/test-*.sh"
  - "scripts/shtest*.sh"
  - "scripts/fresh-install-test.sh"
---

# A pfm test waits on a named condition at its wait door, never on the clock

Binds every agent writing a pfm test of any tier, a shell test or fixture, the mock engine, the journey, or production code a test drives through the clock seam.

- Go waits through `pfm/internal/testwait` (`Until`, `UntilErr`, `Receive`, `Blocked`, `Drive`; contexts `Context`, `Deadline`, `Expired`), shell through `scripts/shtest-wait.sh` (`shtest_wait_until`, `shtest_wait_gone`, `shtest_wait_line`, `shtest_hold`), a journey step through its harness's waits; no wait takes a duration, and the one limit is hung-only (`testwait.HungLimit`, 2 min cut by the test's deadline; the shell door's 120 s): reaching it reports a hung test, never a slow one.
- Outside those doors a test never sleeps, arms a timer or reads the real clock, to wait, pace or judge; time as the subject runs on a `clock.Fake` the test moves (`Advance`, `testwait.Drive`), code reading the process clock under `testwait.ProcessClock`.
- Production code bounds a context through `clock.WithTimeout(parent, c, d)` (`pfm/internal/clock/timeout.go`) and reads `clock.Real` at the call, never a start-up copy.
- A duration a test hands production as a limit fires on a fake clock, is the hung-only limit (`testwait.ProcessPace` × `ProcessTries` for a poll of a real process) or is already expired (`testwait.Expired`); a short real limit stays only where a kernel or child process enforces it against a fake that never answers; review holds this, no check does.
- A test never judges its own speed: no stopwatch comparison, no latency limit (`testwait.Stopwatch` records only); a number worth knowing is a Go benchmark; "returns fast" is asserted through its cause: the work handed off, not done inline, or the call returning while the slow thing is held on a gate.
- A fake process acts on a signal, never on its own timer; a sleep or timer in a fixture's shell, Python or JavaScript is the test's; a pane or child kept alive runs `testwait.Hold` (`HoldAs`, `HoldArgv`; `shtest_hold &` in a shell test), never a bare `tail -f /dev/null`, whose inotify warning lands in the pane or stderr a test reads.
- "Nothing happened" is judged once a sentinel sent down the same path has arrived, never after a pause.
- The mock engine's one clock moves only by the test, through the scenario's `control` FIFO (`pfm/internal/mockengine/control`, written through `controltest`); a scenario declaring a timed act with no `control` is refused at load; `{pid}` in `control` gives each mock process its own FIFO.
- Three checks hold it, a finding fixed at its site, never excepted: `C36-clock-door`, in the arch check's job list, over Go test and test-support files and the shell, Python and JavaScript of their fixtures (alone: `cd pfm && bash scripts/arch-check.sh --clock [paths]`); the real-wait tripwire `pfm/internal/testjail/realwait.go`, armed in every unit test binary, failing a test whose code really waits on the real clock (`cd pfm && bash scripts/arch-check.sh --real-wait [packages]` lists its `C37-real-wait` rows); `shtest-clock` (`scripts/shtest-clock-check.sh`, run by `infra/fence/checks.sh`) over every shell test, fixture and harness outside `plugins/`, `scripts/fresh-install-test.sh` and `infra/fence/gate-fixtures/*.sh` included.
