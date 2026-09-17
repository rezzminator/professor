# Wave 3 — the unit-test law: every host door seamed, ten host edge cases, parallel by default

**Input:** `tmp/walks/unit-seams.md` (62/62 packages, 332 doors, 79 seamed, 253 NONE; per-package summary and the tests still hitting real doors). **Worktree:** `.worktrees/testing-foundation`. **Law (the user's):** a unit under test has every dependency on the host, the account, an API or another process MOCKED at its seam; tests run on the host (`make test`), concurrently; the ten most common host edge cases are first-class fixtures.

## Three seams, one ratchet

1. **`pfm/internal/clock`** — `Clock` (`Now`, `Sleep(ctx, d)`, `After`, `NewTimer`, `NewTicker`), `clock.Real`, and `clock.Fake` (deterministic: `Advance(d)` fires due timers/sleeps; `Pending()` for assertions). Replaces every bare `time.Now/Sleep/After/NewTimer/Tick` in non-test code. Existing per-package styles (`installer.Options.Now/Sleep`, `kill.Dependencies.Now`, `reap.Dependencies.Now`, `picker.ActivityClock`, `statusline.Runtime.Now`) become thin adapters over it or are replaced outright — one clock, never five.
2. **`deps.Runner`** (in the existing `pfm/internal/deps`) — `Run(ctx, argv, opts) (stdout, stderr, exit, err)` + `LookPath`; `deps.RealRunner`; `deps.FakeRunner` scripted by argv pattern (stdout/stderr/exit/ENOENT per call, with a call ledger for assertions). Every `exec.Command*`/`exec.LookPath` outside the `internal/tmux` façade goes through it (git, claude, codex, opencode, systemctl, launchctl, security, docker, uv, python, node, jq); tmux keeps its façade and gains `tmux.Fake` (scripted `capture-pane`/`list-*`/`send-keys` results) used by reload/inject/gather/statusline. `installer.CommandRunner` and `reload.Process` fold into it.
3. **Environment + filesystem** — no bare `os.Getenv/LookupEnv/UserHomeDir/user.Current/Hostname` outside `paths` (which already jails `PFM_HOME`); callers take an `Env` seam (`paths.Env` interface, `paths.OSEnv`, `paths.MapEnv`). Every absolute `/tmp`, `/var/folders`, socket dir, sqlite path resolves through `paths`. Every test package's `TestMain` uses `testjail` (C21 already counts copies).
4. **Ratchet C22 (`pfm/scripts/arch-check.sh`)** — counts bare doors in non-test code by the exact spelling set the seam map used (`os.Getenv|LookupEnv|UserHomeDir|user.Current|exec.Command|exec.CommandContext|exec.LookPath|time.Now|time.Sleep|time.After|time.NewTimer|time.Tick|net.Dial|net.Listen` outside `internal/{clock,deps,paths,tmux}` and outside `_test.go`), baseline hand-written at the start of this wave (`pfm/.arch/host-doors.txt`: 276 doors in 108 files, B0), then only shrinks; **B1 adds `time.NewTicker` to the spelling set** (B0 implemented the list above verbatim and flagged the omission — the seam map searched both) and re-baselines that one row by hand, the count named in its commit; the final commit of this wave measures it again and the number in the commit message is the wave's proof.

**B0 landed with two named deviations (2026-09-17):** the run result type is `deps.RunResult` (a `deps.Result` already names the probe result), and `tmux.Fake` is zero-value-usable (`&tmux.Fake{}`) — there is no `tmux.NewFake`, since ratchet C17 forbids a second free function named `NewFake` beside `clock.NewFake`. B1..B5b use those spellings.

## The ten host edge cases — `pfm/internal/hostfixture`

One package, one function per case, each returning a jailed environment plus the assertion helpers the case needs; every case has at least one test in the package that owns the behaviour (the seam map says which):

| # | fixture | builds | first consumers |
| --- | --- | --- | --- |
| 1 | `NoHome(t)` / `ReadOnlyHome(t)` | `HOME` unset; a home dir with `0o555` (skipped by name when running as root — the fence is root; the host is not) | `paths`, `installer`, `fleetdb`, `store` |
| 2 | `SymlinkedConfigDir(t)` | `~/.claude` → a physical dir elsewhere; blueprint reached via a link | `installer.claudeConfigDirs`, `paths.DevRepoGitDir`, `professor.storeSHA` |
| 3 | `CaseFoldProbe(t)` | creates `a`/`A`, reports whether the FS folded them; tests assert both branches | `installer` link/ledger paths, `codexgen` output names |
| 4 | `NoTmux(t)` / `OldTmux(t)` | `deps.FakeRunner` returning ENOENT for tmux / a version below the minimum | `tmux`, `doctor`, `spawn`, `reload` |
| 5 | `NoServiceManager(t)` | ENOENT for `systemctl` and `launchctl` | `installer` units, `doctor`, `kill` service scope |
| 6 | `BareTerm(t)` | `TERM` unset / `LANG=C` / non-UTF-8 | `statusline`, `ui` cosmos glyphs, `hookentry` renudge |
| 7 | `OddPaths(t)` | HOME, cwd and worktree under a dir with spaces and non-ASCII | `paths`, `inject` lock namespace, `fleet` scan, `archive` |
| 8 | `ExpiredCreds(t)` / `NoCreds(t)` | `.credentials.json` with `expiresAt` in the past; the file absent; Keychain (`security`) returning nothing | `usagehook`, `doctor` seat rows, `headless`, `resolve` — every probe must say NOT-LOGGED-IN by name, never "no chats" |
| 9 | `StaleArtifacts(t)` | dead tmux socket files, a pid file of an exited pid, a `fleet.db-wal` from a crashed writer, a leftover reload lock | `reap`, `stale`, `fleetdb`, `reload.InFlight`, `kill` |
| 10 | `TwoWriters(t, fn)` | runs `fn` twice concurrently against the same ledger/settings/manifest | `atomicfile`, `installer` ownership ledgers, `fleetdb`, `updatecheck` lock |

## Batches (disjoint packages; each agent edits only its packages and runs only its packages in the fence)

- **B0 — foundation (first, alone):** `clock`, `deps.Runner`+`FakeRunner`, `tmux.Fake`, `paths.Env`, `hostfixture` (all ten, each with its own test), C22 with its measured baseline, `TESTPLAN.md` § Seams + § Host fixtures. Green on `go test ./internal/{clock,deps,tmux,paths,hostfixture}/...`.
- **B1 — reload · inject · hookentry** (the busy/idle/lock/sleep doors; `reload_test.go` sleeps and flocks for real today): clock + tmux.Fake + Runner; `t.Parallel`; cases 4, 7, 9.
- **B2 — cmd/pfm** (~55 doors, 5 seamed; the 130 s suite): route every door through the seams; split the slow tests by cause (from Wave 2's slow-test ledger); `t.Parallel`; cases 1, 2, 8.
- **B3 — gather · tmux · statusline · stats · spawn · stale · kill** (real tmux, real timers, `docker_identity.go` "production only"): tmux.Fake, clock, Runner; cases 4, 5, 6, 9.
- **B4 — harvest · harvestmcp · harvestpy** (`find_works_test.go` hits the real network; `auth.go` time unmocked; sidecar exec/time real): `httptest` for every HTTP door, clock, Runner for `uv`/python; case 8 for API tokens.
- **B5a — doctor · installer · update · updatecheck · usagehook · mcpserv · binwatch** (real `security`, real git, real `net.Listen`/timers): Runner for git/security/systemctl/launchctl, clock, an injected listener for binwatch; cases 1, 2, 3, 5, 8, 10.
- **B5b — headless · headless/run · heal · archive · nudge · rearm · professor · picker · store · chat · fleet · resolve · config · index · atomicfile · action**: jail every write, clock, Env seam; cases 1, 7, 9, 10.

Each batch reports: doors NONE → seamed per package (before/after from the seam map's table), tests converted, `t.Parallel` count, per-package wall before/after (`go test -json` in the fence), every remaining real door with its reason (a named list, never silence), and any behaviour bug the seams exposed — those get a watched-red regression test before the fix.

## Proof and gates

- C22 baseline measured in B0's commit; the wave's last commit re-measures and names the drop (target: ≤ 40 bare doors, all inside the four seam packages).
- `make test` on the devbox host (`mise exec -- go test` with Wave 2's `TESTFLAGS`) green and under the Wave 2 budget; `dev.sh iso test pfm` + `iso verify pfm` (fmt, lint-new, arch incl. C22) green on the committed tree.
- Coverage floor (`.testcoverage.yml`) ratchets up if the number rose; never down.
- The ten fixtures each cited from at least one test outside `hostfixture` (a `grep -rl hostfixture.` census in the report).

## Prior art (RR-2, `.professor/RR/agent-fleet-testing-release-chain-2026-09-17.md`)

- **chezmoi's four-tier ladder** — unit → `go-vfs` filesystem fakes → `testscript`/txtar e2e under a redirected `HOME` → Docker/Vagrant/GHA OS sweep — is the same problem as pfm's (a tool that writes into the user's home) and maps onto U → A (`pfm/e2e`, testjail) → B. `paths.Env` is the redirected-`HOME` seam made explicit; `hostfixture` is the OS-edge layer chezmoi pays for with virtual machines — ours runs on the host in milliseconds.
- **testscript mechanics** (`HOME`/`XDG_*` redirected in `Setup`, a temp bin dir first on `PATH`, commands registered in `TestMain`) — Tier A already does this; the unit law extends the same discipline to every package through the seams instead of a second harness.
- **mise**: per-test isolated config/data/state dirs plus an order-shuffle job — the exact failure class of a daemon writing global state across accounts; `-shuffle=on` joins Wave 2's flags (below) so a hidden shared-state dependency surfaces as a shuffle-only failure.
- **Codex CLI's `insta` snapshot MANDATE** ("any change that affects user-visible UI must include snapshot coverage") — the same rule lands in `TESTPLAN.md`: a statusline, picker, Stats, Limits or cosmos change ships with its golden file in the same commit.
- **Goose** serialises its shared-resource scenario suite (`--jobs 1`) — Tier A stays `-p 1`; the unit tier's `t.Parallel` is only legal because its host doors are seamed.
- **Cline's Checkpoints** corrupting user `.git` dirs — destructive operations on user state are a named test class: `hostfixture` cases 9 (`StaleArtifacts`) and 10 (`TwoWriters`) plus every installer `uninstall`/`drop` path assert the refusal branch, not only the success branch.

## Prior art (RR-1, `.professor/RR/hermetic-trustworthy-test-gates-2026-09-17.md`)

- **"Hermeticity is bought at the seam, not at the boundary"** — fake the clock, jail HOME/env, declare every input (Bazel's definition: sandboxed execution, declared inputs, controlled env vars, and a loud failure the moment an undeclared dependency is touched) — is the unit law in one sentence; C22 is the "loud failure" for an undeclared host door.
- **`testscript`** gives every script a fresh `$WORK`, `HOME=/no-home`, `TMPDIR=$WORK/.tmp`, and runs the CLI as a real subprocess — Tier A's shape; `hostfixture.NoHome` is the same posture at unit scale.
- **tmux tests itself from the outside** (`regress/`: real sessions, captured pane output diffed against `.result` baselines) — `tmux.Fake` scripts exactly those captures for the unit tier; the real-tmux truth stays in Tier A/B.
- **`creack/pty`** is the primitive under every Go expect-style harness — Tier A's pty path; the unit tier never opens one.
