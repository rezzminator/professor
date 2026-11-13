---
paths:
  - "pfm/**/*_test.go"
---

# A pfm unit test touches only memory and its own temp directory

Binds every agent writing a `pfm/**/*_test.go` except `pfm/journey/**`, `pfm/e2e/**` and `*_jail_test.go`, the fenced tiers that run real resources by design (`pfm-testing-manual` § Tiers).

- Touch: process memory and the test's own temporary directory (`t.TempDir()`, or a `testjail` home built in one).
- Fake at the seam production code calls: a child process through `deps.Runner` (`deps.FakeRunner`, `Handle` for an answer computed at call time; engine-sync `testjail.FakeEngineSyncRunner`; git `fakegit.Runner` in `pfm/internal/testjail/fakegit`, fixtures through `Must`; the professor store's git `professor.StubStoreRunnerForTest`) handed in at the package's runner seam (`tmux.WithClientRunner`, `herdr.WithAttachRunner`, `stats.ackRunner`, `usagehook.usageRunner`, `reap.ClaudeAgents.Runner`, `agentopen.ExecCommands.Runner`, hookentry `GitGuardWith`/`EngineGuardWith`/`LaunchWith`/`SeatHostWith`/`CodexLaunchWith`, cmd/pfm `commandRunner` and `scriptManagers`, `installer.NewCommandRunner`); a lookup through the runner the code holds (`runner.LookPath`, scripted by `deps.FakeRunner.ScriptLookPath`), never `exec.LookPath` or `deps.Resolve` beside it; an executable the code only resolves or stats through `testjail.WriteExecutableNeverRun`; the clock and every sleep, timer or ticker through `clock.Clock` (`clock.Fake`); `$HOME`, `~/.config` and `~/.local/state/pfm` through `paths.Env` (`paths.MapEnv`) or the `PFM_*` overrides pointed into the temp directory.
- Fake tmux and every unix or network socket at the interface the package's code calls (e.g. `kill.TmuxClient`); SQLite runs in memory: in a test process the SQLite door serves every path from a memory database (`testjail.Run` turns it on), and a test of the file itself (WAL, an older on-disk version, a backup's bytes, a corrupt file, another process) calls `testjail.DatabaseFiles` on its directory.
- Plain `go test ./...` is the unit tier: it compiles no jail file and runs in place on any host inside testjail's unit net (`pfm/internal/testjail/unitnet.go`); a process start or host-tool lookup fails the test as `UNIT-NET REFUSED`.
- A test needing the real thing is a `{name}_jail_test.go` opening `//go:build jail` (arch-check C34), run by `go test -tags jail` in the fence or on a GitHub-hosted runner, or a journey step.
