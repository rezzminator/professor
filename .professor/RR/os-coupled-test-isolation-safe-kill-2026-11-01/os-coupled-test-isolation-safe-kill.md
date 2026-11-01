# RR — How projects like pfm test OS-coupled behaviour safely (incl. macOS) and kill processes safely

Question: find how projects similar to ours test OS-coupled behaviour safely and on macOS, and how they kill processes safely — to inform a redesign of our own (tmux regress, zellij/wezterm, supervisors, systemd tests, gopsutil/go-ps, claude-squad-style fleet managers; 5 questions: mock vs real, host isolation, macOS, safe kill, patterns/anti-patterns).

## Answer

The projects that stopped damaging live setups run real tmux and real processes, but only on a private, run-unique tmux socket inside a short `/tmp` directory with HOME/XDG redirected, and the product code itself refuses the default socket ([tmux regress](https://github.com/tmux/tmux/blob/a73616d781f2af13085691c741cfb0780d365328/regress/command-order.sh#L4-L10), [tmux test_tmpdir](https://github.com/tmux/tmux/blob/a73616d781f2af13085691c741cfb0780d365328/regress/regress-common.inc#L92-L99), [agent-deck IsolateTmuxSocket](https://github.com/asheshgoplani/agent-deck/blob/10aa3a4a78484852b3f1ead9cbe5239c0a7ac7f2/internal/testutil/tmuxenv.go#L55-L131), [agent-deck default-socket guard](https://github.com/asheshgoplani/agent-deck/blob/10aa3a4a78484852b3f1ead9cbe5239c0a7ac7f2/internal/tmux/default_socket_guard.go#L10-L85)). They kill a process only after matching it to a pid-plus-start-time record taken when they spawned it, re-checked before every signal and sent through a pidfd on Linux ([agent-deck reap.go](https://github.com/asheshgoplani/agent-deck/blob/10aa3a4a78484852b3f1ead9cbe5239c0a7ac7f2/internal/procowner/reap.go#L228-L300), [reap_linux.go](https://github.com/asheshgoplani/agent-deck/blob/10aa3a4a78484852b3f1ead9cbe5239c0a7ac7f2/internal/procowner/reap_linux.go#L13-L54), [pitchfork PR #878](https://github.com/jdx/pitchfork/pull/878)); on macOS no such handle exists, so the check-to-kill window stays open ([reap.go](https://github.com/asheshgoplani/agent-deck/blob/10aa3a4a78484852b3f1ead9cbe5239c0a7ac7f2/internal/procowner/reap.go#L140-L165)).

## Map

### Q1 — Mock vs real: where is the seam?

- **tmux regress: everything real.** It runs real servers and PTYs, with a nested tmux acting as the client ([am-terminal.sh#L14-L22](https://github.com/tmux/tmux/blob/a73616d781f2af13085691c741cfb0780d365328/regress/am-terminal.sh#L14-L22)). It waits by polling a condition, never by sleeping ([regress-common.inc#L1-L15](https://github.com/tmux/tmux/blob/a73616d781f2af13085691c741cfb0780d365328/regress/regress-common.inc#L1-L15)).
- **claude-squad: injected interfaces.** Commands go through `cmd.Executor` ([cmd/cmd.go#L8-L25](https://github.com/smtg-ai/claude-squad/blob/ce1ffb4392b01f38e2c4599c7c84d2a93973b138/cmd/cmd.go#L8-L25)) and PTYs through a `PtyFactory` ([session/tmux/pty.go#L10-L26](https://github.com/smtg-ai/claude-squad/blob/ce1ffb4392b01f38e2c4599c7c84d2a93973b138/session/tmux/pty.go#L10-L26)). Both are wired in by `NewTmuxSessionWithDeps` ([tmux.go#L75-L82](https://github.com/smtg-ai/claude-squad/blob/ce1ffb4392b01f38e2c4599c7c84d2a93973b138/session/tmux/tmux.go#L75-L82)) and faked by `MockCmdExec` ([testutils.go#L7-L18](https://github.com/smtg-ai/claude-squad/blob/ce1ffb4392b01f38e2c4599c7c84d2a93973b138/cmd/cmd_test/testutils.go#L7-L18)). Some tests still run a real `kill-session` against the default server ([ui/preview_test.go#L35-L47](https://github.com/smtg-ai/claude-squad/blob/ce1ffb4392b01f38e2c4599c7c84d2a93973b138/ui/preview_test.go#L35-L47)).
- **agent-deck: both, by layer.**
  - tmux calls go through an argv factory, the `tmuxExec` seam ([socket.go#L162-L235](https://github.com/asheshgoplani/agent-deck/blob/10aa3a4a78484852b3f1ead9cbe5239c0a7ac7f2/internal/tmux/socket.go#L162-L235)).
  - Process ownership sits behind `Prober` and `Signaler`. Most of its tests use a scriptable `fakeProber` and spawn nothing ([fake_prober_test.go#L8-L30](https://github.com/asheshgoplani/agent-deck/blob/10aa3a4a78484852b3f1ead9cbe5239c0a7ac7f2/internal/procowner/fake_prober_test.go#L8-L30)).
  - Pid reuse is simulated by changing the start id inside the signaler's send hook ([verify_test.go#L225-L240](https://github.com/asheshgoplani/agent-deck/blob/10aa3a4a78484852b3f1ead9cbe5239c0a7ac7f2/internal/procowner/verify_test.go#L225-L240)).
  - One Linux-only test spawns a real `sleep 30` and reaps it through a pidfd ([reap_linux_test.go#L18-L50](https://github.com/asheshgoplani/agent-deck/blob/10aa3a4a78484852b3f1ead9cbe5239c0a7ac7f2/internal/procowner/reap_linux_test.go#L18-L50)).
  - The `/proc` root is a package variable that tests point at a temp dir ([prober_linux_test.go#L28-L75](https://github.com/asheshgoplani/agent-deck/blob/10aa3a4a78484852b3f1ead9cbe5239c0a7ac7f2/internal/procowner/prober_linux_test.go#L28-L75)).
  - The tmux package swaps `processIdentityOf` while spawning real helper children ([softkill_pid_reuse_test.go#L155-L215](https://github.com/asheshgoplani/agent-deck/blob/10aa3a4a78484852b3f1ead9cbe5239c0a7ac7f2/internal/tmux/softkill_pid_reuse_test.go#L155-L215)).
- **gopsutil: recorded fixtures for the process table.**
  - `HOST_PROC` can be injected ([common.go#L410-L415](https://github.com/shirou/gopsutil/blob/21bfdde71496e9587533d361bb1d34f70d3f4a08/internal/common/common.go#L410-L415)). Tests run `t.Setenv("HOST_PROC","testdata/linux")` ([process_linux_test.go#L52-L56](https://github.com/shirou/gopsutil/blob/21bfdde71496e9587533d361bb1d34f70d3f4a08/process/process_linux_test.go#L52-L56)).
  - Darwin reads the real sysctl `kern.proc.all` ([process_darwin.go#L39-L52](https://github.com/shirou/gopsutil/blob/21bfdde71496e9587533d361bb1d34f70d3f4a08/process/process_darwin.go#L39-L52)).
  - Kill is tested on a real spawned `sleep` ([process_test.go#L643-L660](https://github.com/shirou/gopsutil/blob/21bfdde71496e9587533d361bb1d34f70d3f4a08/process/process_test.go#L643-L660)).
- **Homebrew services: memoized, injectable launchctl.**
  - `System.launchctl` is memoized and has an `attr_writer` ([services/system.rb#L17-L32](https://github.com/Homebrew/brew/blob/1f55ebf835304c959bd2617fe8086316c5d94b37/Library/Homebrew/services/system.rb#L17-L32)).
  - Specs stub `launchctl?` and `systemctl?` ([cli_spec.rb#L20-L26](https://github.com/Homebrew/brew/blob/1f55ebf835304c959bd2617fe8086316c5d94b37/Library/Homebrew/test/services/cli_spec.rb#L20-L26)).
  - Other specs put a fake `#!/bin/sh` launchctl on PATH inside a mktmpdir ([system_spec.rb#L8-L30](https://github.com/Homebrew/brew/blob/1f55ebf835304c959bd2617fe8086316c5d94b37/Library/Homebrew/test/services/system_spec.rb#L8-L30)).
- **overmind: no tests at all.** There are no `*_test.go` files and CI only builds on Ubuntu (clone-distiller/DarthSim-overmind-2026-11-01.md). goreman's process-group kill is likewise untested ([proc_posix.go](https://raw.githubusercontent.com/mattn/goreman/master/proc_posix.go)).
- **systemd: whole OS images.** Integration tests boot under mkosi with qemu or nspawn (`test/integration-tests/README.md`, per sub-rr/06) — unquoted.

### Q2 — Host isolation

- **tmux.**
  - Each test runs on its own socket with an empty config: `TMUX="$TEST_TMUX -LtestA$$ -f/dev/null"` ([command-order.sh#L4-L10](https://github.com/tmux/tmux/blob/a73616d781f2af13085691c741cfb0780d365328/regress/command-order.sh#L4-L10)).
  - The suite runs under `env -i` with a minimal environment ([Makefile#L30-L37](https://github.com/tmux/tmux/blob/a73616d781f2af13085691c741cfb0780d365328/regress/Makefile#L30-L37)), and some tests set `TMUX_TMPDIR` ([mode-kill.sh#L28-L34](https://github.com/tmux/tmux/blob/a73616d781f2af13085691c741cfb0780d365328/regress/mode-kill.sh#L28-L34)).
  - Teardown reads `#{pid}`, runs `kill-server` on the test's own socket and polls `kill -0` ([regress-common.inc#L76-L90](https://github.com/tmux/tmux/blob/a73616d781f2af13085691c741cfb0780d365328/regress/regress-common.inc#L76-L90)).
- **agent-deck, the strongest model.**
  - `IsolateTmuxSocket` unsets `TMUX` and `TMUX_PANE`, sets `TMUX_TMPDIR=/tmp/ad-tmux-*` and exports the marker `AGENT_DECK_TEST_ISOLATED=1`. It panics if the default base is still in use ([tmuxenv.go#L55-L131](https://github.com/asheshgoplani/agent-deck/blob/10aa3a4a78484852b3f1ead9cbe5239c0a7ac7f2/internal/testutil/tmuxenv.go#L55-L131)).
  - `IsolateHome` redirects HOME and XDG and sets `AGENTDECK_PROFILE=_test` ([homeenv.go#L90-L143](https://github.com/asheshgoplani/agent-deck/blob/10aa3a4a78484852b3f1ead9cbe5239c0a7ac7f2/internal/testutil/homeenv.go#L90-L143)). The DB path derives from HOME and XDG ([agentpaths/paths.go#L199-L206](https://github.com/asheshgoplani/agent-deck/blob/10aa3a4a78484852b3f1ead9cbe5239c0a7ac7f2/internal/agentpaths/paths.go#L199-L206)).
  - A lint test fails any `TestMain` that does not call `IsolateTmuxSocket` ([testmain_audit_test.go#L15-L35](https://github.com/asheshgoplani/agent-deck/blob/10aa3a4a78484852b3f1ead9cbe5239c0a7ac7f2/internal/testutil/testmain_audit_test.go#L15-L35)).
  - Product code calls `assertTmuxSpawnIsolated`, which panics on the default socket while in test mode ([default_socket_guard.go#L10-L85](https://github.com/asheshgoplani/agent-deck/blob/10aa3a4a78484852b3f1ead9cbe5239c0a7ac7f2/internal/tmux/default_socket_guard.go#L10-L85); [test_isolation_guard.go#L41-L92](https://github.com/asheshgoplani/agent-deck/blob/10aa3a4a78484852b3f1ead9cbe5239c0a7ac7f2/internal/tmux/test_isolation_guard.go#L41-L92)).
  - Cleanup only kills servers under the test's own directory: `KillTmuxServersUnder(dir)` ([tmuxenv.go#L178-L224](https://github.com/asheshgoplani/agent-deck/blob/10aa3a4a78484852b3f1ead9cbe5239c0a7ac7f2/internal/testutil/tmuxenv.go#L178-L224)).
  - The history explains why. Commit cf4b3d80 (2026-04-17) followed a `go test ./...` run that killed every live session. 76242653 added the HOME/XDG sandbox (clone-distiller/asheshgoplani-agent-deck-2026-11-01.md).
- **wux, a proposed design, not yet code.** Its issue reports that "running the full suite from inside a tmux session (inherited `TMUX`) on a maintainer host killed that host's default tmux server, and with it every live session on the host". The fix it proposes: "Unless the socket is strictly inside `allowedRoot`, or inside `WUX_TEST_TMUX_ROOT`, it throws without running tmux." ([wux #26](https://github.com/jvrck/wux/issues/26)).
- **overmind.** It uses a unique socket name, `-L overmind-<dir>-<nanoid>` ([start/command.go#L58-L75](https://github.com/DarthSim/overmind/blob/f57a6d00fc8b2eafd1d12014283eea25c7132d6e/start/command.go#L58-L75)), and runs `kill-session` rather than `kill-server` ([start/tmux.go#L220-L226](https://github.com/DarthSim/overmind/blob/f57a6d00fc8b2eafd1d12014283eea25c7132d6e/start/tmux.go#L220-L226)).
- **claude-squad, weak isolation.** Sessions only get a name prefix, `claudesquad_` ([tmux.go#L60-L72](https://github.com/smtg-ai/claude-squad/blob/ce1ffb4392b01f38e2c4599c7c84d2a93973b138/session/tmux/tmux.go#L60-L72)), on the default server. HOME is changed with a bare `os.Setenv` ([config_test.go#L128-L136](https://github.com/smtg-ai/claude-squad/blob/ce1ffb4392b01f38e2c4599c7c84d2a93973b138/config/config_test.go#L128-L136)).
- **Homebrew.** Specs snapshot and restore ENV ([spec_helper.rb#L331-L335](https://github.com/Homebrew/brew/blob/1f55ebf835304c959bd2617fe8086316c5d94b37/Library/Homebrew/test/spec_helper.rb#L331-L335), [#L398-L402](https://github.com/Homebrew/brew/blob/1f55ebf835304c959bd2617fe8086316c5d94b37/Library/Homebrew/test/spec_helper.rb#L398-L402)) and install services into a mktmpdir ([cli_spec.rb#L757-L770](https://github.com/Homebrew/brew/blob/1f55ebf835304c959bd2617fe8086316c5d94b37/Library/Homebrew/test/services/cli_spec.rb#L757-L770)). gitconfig's bats suite stubs `systemctl` and unsets `XDG_RUNTIME_DIR` (PR #290 per sub-rr/06) — unquoted.
- **Sandboxes on macOS.**
  - `sandbox-exec(1)` is marked "DEPRECATED" (sub-rr/11) — unquoted.
  - Bazel notes that "On macOS, you cannot run sandbox-exec inside a process that's already being sandboxed." (sub-rr/11) — unquoted.

### Q3 — macOS

- **tmux tests on macOS nightly.**
  - The regress workflow runs on ubuntu x64/arm plus `macos-26` and sets `sudo sysctl -w kern.tty.ptmx_max=999` ([regress.yml#L76-L91](https://github.com/tmux/tmux/blob/a73616d781f2af13085691c741cfb0780d365328/.github/workflows/regress.yml#L76-L91)).
  - For the socket limit it uses `mktemp -d /tmp/tmux-regress.XXXXXX`, because "macOS's default temporary directory can leave too little room for a Unix socket name" ([regress-common.inc#L92-L99](https://github.com/tmux/tmux/blob/a73616d781f2af13085691c741cfb0780d365328/regress/regress-common.inc#L92-L99)).
  - The client fails with ENAMETOOLONG rather than truncating ([client.c#L110-L119](https://github.com/tmux/tmux/blob/a73616d781f2af13085691c741cfb0780d365328/client.c#L110-L119)).
- **agent-deck** forces a short `/tmp` base on darwin for the 104-byte limit ([tmuxenv.go#L234-L268](https://github.com/asheshgoplani/agent-deck/blob/10aa3a4a78484852b3f1ead9cbe5239c0a7ac7f2/internal/testutil/tmuxenv.go#L234-L268); commit 27925487). Its CI installs tmux on Ubuntu, and runs only a native-ssh job on `macos-latest` ([go-test.yml#L269-L276](https://github.com/asheshgoplani/agent-deck/blob/10aa3a4a78484852b3f1ead9cbe5239c0a7ac7f2/.github/workflows/go-test.yml#L269-L276), [#L458-L475](https://github.com/asheshgoplani/agent-deck/blob/10aa3a4a78484852b3f1ead9cbe5239c0a7ac7f2/.github/workflows/go-test.yml#L458-L475)).
- **The 104-byte limit.**
  - Go's own tests hit it: "is too long, must be < 104". The cause is that "`testing.TB.TempDir` includes the test name and some suffix" and "`os.TempDir` on darwin is a long user-specific thing" ([golang/go#62614](https://github.com/golang/go/issues/62614)).
  - x/net's nettest uses `/tmp` on darwin (golang/go#54416, sub-rr/06) — unquoted.
  - Hosted macOS runners use a temp directory under `/var/folders`, which resolves to `/private/var` ([pstack-t3 #649](https://github.com/creedants/pstack-t3/pull/649)) — unquoted.
- **No /proc.**
  - gopsutil uses sysctl `kern.proc.all` and `kern.procargs2` ([process_darwin.go#L424-L428](https://github.com/shirou/gopsutil/blob/21bfdde71496e9587533d361bb1d34f70d3f4a08/process/process_darwin.go#L424-L428)).
  - agent-deck's darwin prober shells out to `ps -p <pid> -o pid=,ppid=,pgid=,uid=,state=,lstart=` ([prober_ps.go#L15-L17](https://github.com/asheshgoplani/agent-deck/blob/10aa3a4a78484852b3f1ead9cbe5239c0a7ac7f2/internal/procowner/prober_ps.go#L15-L17)). Its only darwin test checks that the prober sees its own process ([prober_darwin_test.go#L1-L22](https://github.com/asheshgoplani/agent-deck/blob/10aa3a4a78484852b3f1ead9cbe5239c0a7ac7f2/internal/procowner/prober_darwin_test.go#L1-L22)).
  - `SysctlKinfoProc` in x/sys is a single sysctl call ([syscall_darwin.go](https://raw.githubusercontent.com/golang/sys/master/unix/syscall_darwin.go)) — unquoted.
  - On another user's process, KERN_PROCARGS2 returns EINVAL (TBD PR #1296, sub-rr/10) — unquoted.
- **Runners.**
  - "The Linux and macOS virtual machines both run using passwordless `sudo`." ([GitHub-hosted runners](https://docs.github.com/en/actions/reference/runners/github-hosted-runners)). So the CI user is not root, and chown-as-other fails unless the test uses sudo.
  - gopsutil CI covers ubuntu, macos-14/15/26 and windows. Homebrew covers ubuntu and macos-26 (clone-distiller reports).
  - claude-squad only cross-compiles darwin, Ubuntu only ([build.yml#L38-L47](https://github.com/smtg-ai/claude-squad/blob/ce1ffb4392b01f38e2c4599c7c84d2a93973b138/.github/workflows/build.yml#L38-L47)).
  - SIP status on current hosted images is NOT FOUND. The image scripts only check `csrutil status` ([configure-machine.sh](https://raw.githubusercontent.com/actions/runner-images/main/images/macos/scripts/build/configure-machine.sh)).
- **launchd on CI.**
  - No project found loads a real LaunchAgent on a hosted runner. Homebrew stubs launchctl instead (Q1).
  - Whether a hosted runner has a `gui/<uid>` domain is DISPUTED and unverified. A plan in [locklane #703](https://github.com/haninaguib-devtools/locklane/issues/703) asserts it. [mnapoli](https://mnapoli.fr/running-github-actions-mac-mini) reports that on a self-hosted headless Mac "the agent won't load".
- **Local VMs.**
  - Tart: "a virtualization toolset to build, run and manage macOS and Linux virtual machines on Apple Silicon" ([tart.run](https://tart.run)). Its licence is DISPUTED. [tart.run/licensing](https://tart.run/licensing/) says "Free Tier license has a 100 CPU core limit for Tart", but the repo [LICENSE](https://raw.githubusercontent.com/cirruslabs/tart/main/LICENSE) is FSL-1.1-ALv2 with no core limit.
  - Lume is MIT ([cua README](https://github.com/trycua/cua/blob/main/README.md)).
  - Anka is "host-based and not CPU core based" ([Anka licensing](https://docs.veertu.com/anka/licensing/)).
  - UTM's `utmctl` is "a wrapper around the AppleScript interface" ([UTM scripting](https://docs.getutm.app/scripting/scripting/)).
  - Cirrus CI runs macOS on Tart ([Cirrus macOS guide](https://github.com/cirruslabs/cirrus-ci-docs/blob/master/docs/guide/macOS.md)).
  - No project found runs tmux or launchd tests inside a macOS VM.

### Q4 — Safe kill

- **Reference design, agent-deck procowner (commit 4af87a44, 2026-08-16, #1873).**
  - At spawn it writes a receipt per member: pid, start_id, pgid, uid, comm and role, plus a boot id ([receipt.go#L97-L111](https://github.com/asheshgoplani/agent-deck/blob/10aa3a4a78484852b3f1ead9cbe5239c0a7ac7f2/internal/procowner/receipt.go#L97-L111), [#L143-L168](https://github.com/asheshgoplani/agent-deck/blob/10aa3a4a78484852b3f1ead9cbe5239c0a7ac7f2/internal/procowner/receipt.go#L143-L168)).
  - `VerifyMember` sorts each process into gone, unknown or stranger, and only owned processes get a signal ([verify.go#L176-L201](https://github.com/asheshgoplani/agent-deck/blob/10aa3a4a78484852b3f1ead9cbe5239c0a7ac7f2/internal/procowner/verify.go#L176-L201)).
  - Identity is re-checked before every signal, including the SIGKILL escalation ([reap.go#L228-L300](https://github.com/asheshgoplani/agent-deck/blob/10aa3a4a78484852b3f1ead9cbe5239c0a7ac7f2/internal/procowner/reap.go#L228-L300)).
  - On Linux each member is pinned with `PidfdOpen`, then verified, then sent `PidfdSendSignal` ([reap_linux.go#L13-L54](https://github.com/asheshgoplani/agent-deck/blob/10aa3a4a78484852b3f1ead9cbe5239c0a7ac7f2/internal/procowner/reap_linux.go#L13-L54)). Linux start time comes from `/proc/<pid>/stat` field 22 ([prober_linux.go#L108-L160](https://github.com/asheshgoplani/agent-deck/blob/10aa3a4a78484852b3f1ead9cbe5239c0a7ac7f2/internal/procowner/prober_linux.go#L108-L160); [proc_pid_stat(5)](https://man7.org/linux/man-pages/man5/proc_pid_stat.5.html)).
  - On macOS it uses `ps lstart` at one-second resolution and accepts that a same-second pid reuse cannot be told apart ([prober_darwin.go#L19-L44](https://github.com/asheshgoplani/agent-deck/blob/10aa3a4a78484852b3f1ead9cbe5239c0a7ac7f2/internal/procowner/prober_darwin.go#L19-L44)). The verify-to-kill window stays open there ([reap.go#L140-L165](https://github.com/asheshgoplani/agent-deck/blob/10aa3a4a78484852b3f1ead9cbe5239c0a7ac7f2/internal/procowner/reap.go#L140-L165)).
- **agent-deck's tmux control-client reaper (592edc6e, #1941).** It signals `-pgid` and re-checks identity before both SIGTERM and SIGKILL ([pipemanager.go#L2023-L2110](https://github.com/asheshgoplani/agent-deck/blob/10aa3a4a78484852b3f1ead9cbe5239c0a7ac7f2/internal/tmux/pipemanager.go#L2023-L2110)). The same repo still has a weak path, `EnsurePIDsDead`, which matches on a comm substring ([ensure_pids_dead.go#L127-L172](https://github.com/asheshgoplani/agent-deck/blob/10aa3a4a78484852b3f1ead9cbe5239c0a7ac7f2/internal/tmux/ensure_pids_dead.go#L127-L172)).
- **pitchfork.** "A mismatch is logged and the record treated as stale, never signalled." Also: "On Linux the identity check binds a pidfd and every signal, including the SIGKILL escalation, is sent through it, so a pid recycled after the check gets `ESRCH` instead of the signal." ([pitchfork PR #878](https://github.com/jdx/pitchfork/pull/878)).
- **psutil.**
  - "Process identity is guaranteed by (PID + creation time)". Its `kill()` docstring says: "Kill the current process with SIGKILL preemptively checking whether PID has been reused" ([psutil __init__.py](https://raw.githubusercontent.com/giampaolo/psutil/master/psutil/__init__.py)).
  - gopsutil's `IsRunning` compares create time ([process.go#L296-L309](https://github.com/shirou/gopsutil/blob/21bfdde71496e9587533d361bb1d34f70d3f4a08/process/process.go#L296-L309)), but its `Kill` and `SendSignal` do not re-check identity ([process_posix.go#L143-L172](https://github.com/shirou/gopsutil/blob/21bfdde71496e9587533d361bb1d34f70d3f4a08/process/process_posix.go#L143-L172)).
- **Go and the kernel.**
  - Go 1.23: "On Linux with pidfd support (generally Linux v5.4+), `Process`-related functions and methods use pidfd (rather than PID) internally, eliminating potential mistargeting when a PID is reused by the OS." ([Go 1.23](https://go.dev/doc/go1.23)). Keeping the `*os.Process` from `cmd.Start` holds that pidfd.
  - `os.FindProcess` on a scanned pid gains nothing: "On Unix systems, FindProcess always succeeds and returns a Process for the given pid" ([pkg.go.dev/os](https://pkg.go.dev/os#FindProcess)).
  - The pidfd race: "a PID could be reused in the time between when a caller learns about it and when the pidfd_open() call actually completes" ([LWN](https://lwn.net/Articles/784831/)). That is why a check after opening the pidfd is still needed.
  - pidfd_open dates to Linux 5.3 ([pidfd_open(2)](https://man7.org/linux/man-pages/man2/pidfd_open.2.html)) — unquoted.
- **Supervisors.**
  - s6 says "the most important drawback of .pid files is that they create race conditions and management scripts may kill the wrong process". Its alternative: "Since your daemon is its direct child, the supervisor always knows the correct PID of your daemon." ([s6 overview](https://skarnet.org/software/s6/overview.html)).
  - systemd: "Note that PID files should be avoided in modern projects." ([systemd.service](https://www.freedesktop.org/software/systemd/man/latest/systemd.service.html)).
  - start-stop-daemon on `--pidfile`: "Using this matching option alone might cause unintended processes to be acted on." ([start-stop-daemon(8)](https://man7.org/linux/man-pages/man8/start-stop-daemon.8.html)).
- **Process groups.**
  - overmind sends `syscall.Kill(-pid,s)` ([process.go#L150-L158](https://github.com/DarthSim/overmind/blob/f57a6d00fc8b2eafd1d12014283eea25c7132d6e/start/process.go#L150-L158)). goreman uses `Setpgid:true` with `unix.Kill(-1*pid)` ([proc_posix.go](https://raw.githubusercontent.com/mattn/goreman/master/proc_posix.go)).
  - POSIX: "A process group ID shall not be reused by the system until the process group lifetime ends." ([POSIX base defs ch.4](https://pubs.opengroup.org/onlinepubs/9699919799/basedefs/V1_chap04.html)) — unquoted, per sub-rr/13. Even so, a stale pgid can be killed after the group has ended ([P2pKit #494](https://github.com/p2pKit/P2pKit/issues/494)).
- **Better macOS identity than `ps lstart`.**
  - XNU copies `p_start` to the microsecond into `kp_proc.p_starttime` ([kern_sysctl.c](https://raw.githubusercontent.com/apple-oss-distributions/xnu/main/bsd/kern/kern_sysctl.c)), readable through `unix.SysctlKinfoProc` — unquoted.
  - `p_uniqueid = ++nextuniqueid` ([kern_fork.c](https://raw.githubusercontent.com/apple-oss-distributions/xnu/main/bsd/kern/kern_fork.c)) is only exposed through a private flavor — unquoted.
  - kqueue `EVFILT_PROC` binds to the `struct proc` once attached ([kern_event.c](https://raw.githubusercontent.com/apple-oss-distributions/xnu/main/bsd/kern/kern_event.c)) — unquoted. Whether a zombie gets ESRCH is DISPUTED between [Chromium kill_mac.cc](https://chromium.googlesource.com/chromium/src/base/+/master/process/kill_mac.cc) and the XNU attach code.
- **Anti-examples.**
  - claude-squad's `CleanupSessions` uses an unanchored regex ([tmux.go#L499-L525](https://github.com/smtg-ai/claude-squad/blob/ce1ffb4392b01f38e2c4599c7c84d2a93973b138/session/tmux/tmux.go#L499-L525)), and its daemon sends SIGKILL to a pid file's pid with no identity check ([daemon.go#L145-L165](https://github.com/smtg-ai/claude-squad/blob/ce1ffb4392b01f38e2c4599c7c84d2a93973b138/daemon/daemon.go#L145-L165)).
  - Homebrew finds pids by regex ([formula_wrapper.rb#L509-L514](https://github.com/Homebrew/brew/blob/1f55ebf835304c959bd2617fe8086316c5d94b37/Library/Homebrew/services/formula_wrapper.rb#L509-L514)), but stops services by launchd label `bootout` ([cli.rb#L236-L243](https://github.com/Homebrew/brew/blob/1f55ebf835304c959bd2617fe8086316c5d94b37/Library/Homebrew/services/cli.rb#L236-L243)).

### Q5 — Patterns to copy and anti-patterns, ranked by the pfm failures they would have prevented

1. **Copy: the product refuses the live default unless it is in a test sandbox.** agent-deck's `assertTmuxSpawnIsolated` and its isolation guard, with wux's path-containment kill guard as the same idea (Q2). This stops fixtures landing in the live account and the live DB being opened, whatever the test author forgets.
2. **Copy: one isolation helper sets HOME, XDG, TMUX_TMPDIR and the profile, and a lint test makes every `TestMain` call it.** agent-deck's `IsolateHome`, `IsolateTmuxSocket` and `testmain_audit_test.go` (Q2). This prevents the same two dev-Mac failures and makes isolation impossible to skip.
3. **Copy: a short, run-unique socket directory under `/tmp`, never `t.TempDir()` or `$TMPDIR` on darwin.** tmux and agent-deck both do this (Q3), and golang/go#62614 shows the failure. It fixes the 104-byte failures on macOS runners.
4. **Copy: spawn-time receipts with a recheck before every signal, and pidfd on Linux.** agent-deck procowner and pitchfork (Q4). Prefer a supervised direct child, per s6 and Go 1.23 pidfd. On darwin, use kinfo `p_starttime` in microseconds rather than `ps lstart`. This stops a scan from killing a live process.
5. **Copy: one seam per OS mechanism, with darwin and linux implementations behind it.** gopsutil's `HOST_PROC` fixtures; agent-deck's `Prober` with its `procRoot` variable; Homebrew's injectable launchctl and fake-binary-on-PATH (Q1). This answers the no-/proc and launchd failures on macOS runners: the decision logic runs on fakes on every OS, and the real-OS tests carry darwin and linux build tags.
6. **Copy: CI on real macOS.** tmux on macos-26 nightly and gopsutil on macos-14/15/26 (Q3). This catches the next darwin-only regression before a developer's Mac does. Runners are non-root with passwordless sudo, so chown tests need sudo or an injected owner.
7. **Avoid:**
   - matching kills by name, regex or comm substring (claude-squad, Homebrew, agent-deck `EnsurePIDsDead`);
   - pid files without an identity check (claude-squad daemon, start-stop-daemon `--pidfile` alone);
   - real tmux calls on the default server from tests (claude-squad `preview_test.go`);
   - bare `os.Setenv(HOME)` with no restore;
   - `kill-server` on a socket the test did not create.

## Coverage

1. tmux regress suite (software testing, clone-distiller): settled. Report: clone-distiller/tmux-tmux-2026-11-01.md
2. AI agent tmux fleet managers (software testing, clone-distiller): settled. Reports: clone-distiller/smtg-ai-claude-squad-2026-11-01.md, clone-distiller/asheshgoplani-agent-deck-2026-11-01.md
3. Process-table libraries (systems programming, clone-distiller): settled. Report: clone-distiller/shirou-gopsutil-2026-11-01.md
4. Process supervisors (Unix operations): settled. Reports: clone-distiller/DarthSim-overmind-2026-11-01.md, sub-rr/09-supervisor-pidfile-guards.md
5. Safe kill (OS kernel / systems programming): partial. Open questions:
   - the macOS check-to-kill window has no handle;
   - `ps lstart` one-second resolution comes only from agent-deck's own source comment;
   - whether kqueue gives a zombie ESRCH is DISPUTED;
   - `p_uniqueid` cannot be reached without a private header.

   Reports: sub-rr/05-safe-kill-primitives.md, sub-rr/10-safe-kill-go-macos.md, sub-rr/13-macos-process-identity.md, clone-distiller/asheshgoplani-agent-deck-2026-11-01-2.md
6. macOS testing (CI/release engineering): partial. Open questions:
   - SIP status on current hosted runners is NOT FOUND;
   - whether a hosted runner has a `gui/<uid>` launchd domain is DISPUTED and unverified;
   - the Tart licence is DISPUTED.

   Reports: sub-rr/06-macos-host-isolation.md, sub-rr/11-macos-runner-vm-sandbox.md, sub-rr/14-macos-runner-launchd-vm.md
7. Service-install testing (added; release engineering, clone-distiller): settled. Report: clone-distiller/Homebrew-brew-2026-11-01.md

Not studied: zellij, wezterm mux and go-ps. The clone budget went to agent-deck, which turned out more instructive.

Digging ended: a round settled nothing and added no sub-area.

## Verification

19 facts were checked on 12 pages: Go 1.23, s6, start-stop-daemon, GitHub-hosted runners, golang/go#62614, pitchfork #878, systemd.service, wux #26, LWN pidfd, tart.run/licensing, psutil and pkg.go.dev os. 15 were confirmed with quotes.

NOT ON PAGE, removed or corrected:
- start-stop-daemon "`--pidfile` verifies that the process matches". The page only says "Check whether a process has created the file pidfile."
- start-stop-daemon warning tied to name/exec matching. The warning is attached to `--pidfile` alone, and the map now says so.
- tart.run/licensing "runs macOS VMs on Apple Silicon". That fact now cites the tart.run homepage instead.

The wux guard is confirmed, but only as a proposed design, not code.

UNCHECKED: none. Clone-distiller facts are pinned to commits and were not re-fetched.

## Open rabbit holes

- SIP status and `gui/<uid>` launchd domain on hosted macos-15/26 runners — dug, unsettled. A job log of `csrutil status` and `launchctl print gui/$(id -u)` would settle it.
- psutil's C conversion of `p_starttime` to an epoch value, and how sensitive it is to clock steps — dug, unsettled.
- Whether kqueue `EVFILT_PROC` gives a zombie ESRCH — dug, unsettled.
- `proc_uniqidentifierinfo` / `p_uniqueid` from Go without cgo — dug, unsettled.
- Tart licence discrepancy (FSL text vs the 100-core page) — dug, unsettled.
- agent-deck `orphan_reap_live_identity_test.go`: real tmux on private `-L` sockets — undug.
- agent-deck issue #1873 and PR #1941 incident write-ups — undug.
- tmux issue #4980 (macOS 3.6a notify-path crash, which motivates SIGTERM-then-SIGKILL by group) — undug.
- zellij, wezterm mux and go-ps test isolation — undug.
- systemd integration-test isolation via mkosi/nspawn, as a model for Linux-side launchd/systemd unit tests — dug, unquoted.
