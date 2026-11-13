---
paths:
  - "pfm/**/*_test.go"
---

# A pfm unit test touches only memory and its own temp directory

Binds every agent writing a `pfm/**/*_test.go` except `pfm/journey/**`, `pfm/e2e/**` and `*_jail_test.go`, the fenced tiers that run real resources by design (`pfm-testing-manual` § Tiers).

- Touch: process memory and the test's own temporary directory (`t.TempDir()`, or a `testjail` home built in one).
- Fake at the seam production code calls: a child process through `deps.Runner` (`deps.FakeRunner`); the clock and every sleep, timer or ticker through `clock.Clock` (`clock.Fake`); `$HOME`, `~/.config` and `~/.local/state/pfm` through `paths.Env` (`paths.MapEnv`) or the `PFM_*` overrides pointed into the temp directory.
- Fake tmux and every unix or network socket at the interface the package's code calls (e.g. `kill.TmuxClient`); SQLite runs in memory through `store.OpenMemory`.
- A test needing the real thing is a `{name}_jail_test.go`, run in the fence, or a journey step.
