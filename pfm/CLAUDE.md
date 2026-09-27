# pfm — the Go fleet engine: one binary that indexes, lists, names, kills, attaches, injects and drives every live and resumable Claude Code and Codex chat on the box
It reads and writes the user's real chat state: a destructive operation on a live socket is not recoverable by a rerun, so verify the target resolves before acting, and prefer refusing to guessing.

# Vocabulary

- flow matrix: every flow, its test tier and its jail status · `TESTPLAN.md`
- CLI adapters: stdlib `flag` dispatch plus the end-to-end jail tests; a file name starts with its unit (`chat_` is the one multi-file unit), so `ls cmd/pfm/{unit}_*` lists a unit; `pfm help` lists the subcommands · `cmd/pfm/`
- package map: each `internal/` package opens with a doc comment naming what it owns; `go list -f '{{.ImportPath}}: {{.Doc}}' ./internal/...` prints the map · `internal/doctor/`, `internal/hookentry/`, `internal/picker/`, `internal/update/`, `internal/professor/`
- tagged e2e: the `-tags e2e` tier, run serially · `e2e/`
- jail: a test run with every filesystem location redirected by `PFM_*` overrides (§ Environment Variables); reference harness `testdata/e2e.sh`, fixtures `testdata/{claude-store|codex-store|crumbs|proc|golden}/`
- eval protocol (K1): the binary prints one shell line on stdout for the shim to `eval`, never execing the final tmux attach itself
- K3: one implementation per rule — naming precedence, the kill ratchet, row classification, run-string synthesis — each in exactly one package with table-driven tests
- façades: the one door per cross-cutting primitive — `atomicfile.Write` (whole-file replace), `sqlitedb` (every SQLite open), `tmux.Exec` and `tmux.Socket` over it (every tmux call on a chat socket), `internal/chat/` (the chat verbs a surface calls typed) · `internal/atomicfile/`, `internal/sqlitedb/`, `internal/tmux/`
- architecture ratchet: C1–C21 checked against baselines that only shrink; `--measure` locks a shrink · `scripts/arch-check.sh`, `.arch/`
- lint law: gofumpt + gci + golines at 120, plus dupl, goconst, gocritic, revive, staticcheck; coverage thresholds · `.golangci.yml`, `.testcoverage.yml`, tools pinned in `infra/fence/tools.env`
- shim: the thin zsh wrapper that `eval`s the eval-protocol line · `internal/installer/assets/shim/pfm.zsh`, its tests `internal/installer/shim/`
- installer: stages every host asset; with the fleet prompt embedded from `harness-prompts/`, the binary is the single source — no external template dir · `internal/installer/`
- `deps.Registry`: the single place a platform difference is declared; `deps.Resolve` refuses a gated name off-platform even when it is on PATH · `internal/deps/registry.go`
- config: account identity, emoji, theme and permission posture · `internal/config/`
- lineage: folds a Codex subagent thread into its parent seat · `internal/store/lineage.go`
- migrations: additive, numbered `migration_v{N}.sql` files, each with its `go:embed` · `internal/store/schema.sql`
- `pfm.dev`: the local build artifact, never the shipped path; the host mirror build is `make host-install` (root § Host), which stamps `-X main.version` from `VERSION`

# Runtime

## Fence

- `iso verify pfm` runs vet, fmt-check, lint-new (lines changed since origin/develop), the architecture ratchet and the gate scripts' own `scripts/*_test.sh`; `iso test pfm` runs unit plus tagged e2e, each against its timing budget; `iso e2e` the tagged tier alone.
- One package or probe: `.claude/scripts/dev.sh iso run 'go -C pfm test -count=1 ./internal/{pkg}/'`; the lint burn-down view: `iso run 'make -C pfm lint'`.
- The fence mounts the worktree read-only: formatting rewrites the tree, so `make -C pfm fmt` runs on the host in the worktree, after `make -C pfm tools` installs the pinned tools.

## Environment Variables

- Test-jail overrides, not a config system (`internal/paths/paths.go`): `PFM_HOME` · `PFM_DB` · `PFM_FLEET_DB` · `PFM_SID_DIR` · `PFM_CLAUDE_ROOTS` · `PFM_CODEX_ROOT` · `PFM_TMUX_DIR` · `PFM_PROC_ROOT` · `PFM_TMUX_CONF`.
- `PFM_TMUX_CONF` unset: a chat's tmux server loads the user's own `~/.tmux.conf`, since a chat is a terminal the user lives in; a jail sets it to `/dev/null` so a real machine config never steers a fixture.
- Test knobs outside `internal/paths/`: the scan clock `PFM_TEST_NOW_NS` (`internal/fleet/scan.go`), `PFM_TEST_FRESH_SOCKET` (`internal/spawn/socket.go`).

# Rules

## Live chat state (sacred ground)

- **Tests NEVER touch a live `cc-*` / `cx-*` socket or the real `/tmp/cc-sid`:** every test sets `TMUX_TMPDIR = t.TempDir()`.
- **Destructive commands default to a dry run, and the dry run IS the apply's preview:** `reap`, `archive` and `heal` classify identically with and without `--apply`; every unknown — an unanswerable busy query, a silent socket, a chat writing its transcript right now — resolves toward keeping what exists.
- **A `REAL-SESSION` flow is scheduled deliberately, NEVER incidentally:** one that cannot be jailed is named in `TESTPLAN.md` § Flows that CANNOT be jailed, never left quietly uncovered.

## Code Standards

- Eval protocol (K1): the TUI renders on `/dev/tty`, information goes to stderr, exactly one shell line to stdout; bunker semantics need `exec tmux attach` to replace the interactive shell.
- A ✦-new Claude row spawns natively through `action.ClaudeSpawn`; a ✦-new Codex row calls the user's own `cx`; emitted lines stay golden-testable.
- K3: a second copy of a K3 rule is a defect; MCP reaches everything outside `internal/chat/` through argv `Dispatch`, which C10 counts and lets only shrink.
- Architecture ratchet: `.claude/scripts/dev.sh iso verify pfm` runs it; a new entry is a FAIL to fix; a genuinely new exception is a hand edit to `.arch/` named in the commit message.
- Lint law: `.golangci.yml` and `.testcoverage.yml` are gates — lint-new blocks a wave on the lines it changed, `make lint` is the burn-down view, coverage thresholds only ratchet up.
- A kill is permanent, and the store table keeps its `hidden` name; a kill of a live chat always runs the detached exit choreography, `--exit` its explicit form.
- The ratchet counts prompts, not bytes (K2): kill baselines are `baseline_prompts`; auto-unkill is `prompt_count > baseline`; legacy byte baselines convert once, at import.
- One binary, two kernels: where Linux and macOS differ, a `_linux.go` / `_darwin.go` pair defines the same identifier and the caller stays platform-blind (`getTermios`, `nativeProcFS`, `nativeProcesses`, `schedulerIsLaunchd`) — the seam is a build tag, not a runtime `if`.
- Darwin has no `/proc`: the process table comes from `sysctl`, and `ProcFS` dispatches through `gather.NewProcFS`, which still honours an existing root, because that is how the jail feeds it fixtures.
- tmux's ioctl and format-separator spellings differ by kernel: parse through `internal/tmux/format.go`, not against one spelling.
- Darwin has no dead-launchd jail, so the installer's rc 97 gate narrows to "not mid-execution" instead of "manager not live".
- A platform whose constants nobody has confirmed fails to build instead of falling back to a guess.
- `deps.Registry`: an entry with no `Platforms` field applies everywhere; everything ungated (`tmux`, `git`, `sh`, `bash`, `zsh`, `sleep`, `script`, `go`) works identically on both kernels.
- A fallback's gate covers every platform that reaches it: `setsid` is linux-only, so darwin alone takes the `nohup` branch, and gating `nohup` to linux would strand the fallback exactly where it is needed.
- Gate an entry on the binary's real availability, and give it `VersionArgs` only when every gated platform's build accepts them: BSD `nohup` rejects GNU's `--version`, reporting a working binary as broken.
- Every filesystem location resolves through `internal/paths/`, and `/proc` sits behind the `ProcFS` interface — `$HOME`, `/tmp`, a socket dir and `/proc` are never literals; this is what lets the suite run in a jail.
- Account identity, emoji, theme and permission posture come from `internal/config/` alone: a hardcoded account count, `.cc/{N}` literal, medal emoji or bypass flag elsewhere is a defect.
- A seat is identified by lineage, not file recency: Codex writes subagent threads into `~/.codex/sessions` beside real seats, marked `thread_source: subagent` with a `parent_thread_id`; code reading the rollout tree directly checks those two fields first.
- Identity is derived where the chat is, not where the message is delivered: a detached process (the `--then` waiter, any dispatcher a chat backgrounds) is reparented, and a Codex tool shell has neither `$TMUX` nor a session id — it derives nothing, and its message goes out unsigned.
- A chat states its own identity through `CHAT_SENDER_SESSION` / `CHAT_SENDER_LABEL` / `CHAT_SENDER_SID`, which `inject` reads from its own environment only — a chat states who it is, never who somebody else is.
- A seat driven by `codex app-server` has no tmux ancestry: its identity comes from `CODEX_THREAD_ID` through the fleet's thread→socket binding, the last rung, since that variable is inherited and never renames a process that has a pane of its own.
- A probe that could not run returns an error, not "nothing found": a pane capture on the wrong socket reads as a quiet chat, and `kill -0` cannot tell a healthy waiter from a reparented deaf one — build the distinguishing signal into the probe; the most common defect class here.
- Errors wrap with context (`fmt.Errorf("…: %w", err)`): a swallowed error in a gather path renders as a missing chat row, which reads as "no such chat".
- A schema change is a new `migration_v{N}.sql` plus its `go:embed`; `internal/store/schema.sql` is never edited for it, since existing databases never see it.
- Behavior belongs in Go, not the shim: a fix that is easier in the shim signals that the Go surface is wrong.
- The CLI stays boring Go for one box: stdlib `flag`, no cobra, no config system, no telemetry.
- Storage is modernc.org/sqlite, pure Go with `CGO_ENABLED=0` for a static binary; indexing is on-demand and incremental, with no index daemon.
- The TUI is charm.land/bubbletea, bubbles and lipgloss, all v2: verify the v2 API against upstream before writing UI code, since v1 examples do not compile.
- Go is pinned in `go.mod` (mise: `mise use -g go@1.27`).

## Testing Rules

- Package tests sit beside every package; the end-to-end jail tests under `cmd/pfm/`; tiers per `TESTPLAN.md` § Legend — the SAFETY column.
- Keep scratch socket paths short: a long `TMUX_TMPDIR` hits "File name too long", and the failure looks like a tmux bug.
- Every K3 rule gets table-driven tests; every emitted shell line a golden file.
