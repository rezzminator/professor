# pfm (Professor-Fleet-Manager)

Go engine for the chat fleet — one binary that indexes, lists, names, kills, attaches, injects, and drives every live and resumable Claude Code and Codex chat on the box. It replaced 1,293 lines of zsh (`cc-ls`) whose cost was structural: ~300 forks per refresh over a 12,000-transcript corpus, and the same naming / kill-ratchet logic written three and four times over. The flow matrix is `TESTPLAN.md`.

## Quick Start

```bash
go build -o pfm.dev ./cmd/pfm   # from pfm/
./pfm.dev doctor                     # environment self-check
./pfm.dev ls --plain                 # read-only listing, no TUI
make tools                           # pinned golangci-lint + go-test-coverage (infra/fence/tools.env)
make fmt && make lint                # rewrite formatting; the full lint backlog
make cover                           # cross-package coverage against .testcoverage.yml
```

Use `/dev build pfm` · `/dev verify pfm` · `/dev test pfm` for anything the pipeline reads.

## Stack

- **Runtime:** Go 1.24.13 — **pinned, not latest** (`go.mod`; mise: `mise use -g go@1.24`)
- **TUI:** `charm.land/bubbletea/v2` + `bubbles/v2` + `lipgloss/v2` — verify the v2 API against the upstream repo before writing UI code; v1 examples do not compile
- **Fuzzy match:** `sahilm/fuzzy` · **MCP:** `modelcontextprotocol/go-sdk`
- **Storage:** `modernc.org/sqlite` — pure Go, `CGO_ENABLED=0`, static binary. On-demand incremental indexing; **no daemon.**
- **CLI:** stdlib `flag` dispatch in `cmd/pfm/`. **No cobra, no config system, no telemetry** — boring Go for one box.
- **Platforms:** Linux and Darwin, one codebase; see § Platforms.

## File Structure

`cmd/pfm/` holds the CLI adapters and the end-to-end jail tests; the doctor, the hook entries, the picker, the self-update and the project baselines are `internal/{doctor,hookentry,picker,update,professor}`; `pfm help` lists the subcommands. Each `internal/` package opens with a doc comment naming what it owns: `go list -f '{{.ImportPath}}: {{.Doc}}' ./internal/...` is the package map.

`internal/installer/assets/shim/pfm.zsh` is the thin post-cutover wrapper (`internal/installer/shim/` holds only its tests). `cmd/pfm` file names start with their unit — `chat_` is the one multi-file unit still in main; `doctor ls internal update` left for `internal/` — so `ls cmd/pfm/<unit>_*` lists a unit. `testdata/` holds `claude-store/ codex-store/ crumbs/ proc/ golden/` plus the reference harness `e2e.sh`.

## Platforms

`internal/deps.Registry` is the single place a platform difference is declared. An entry with no `Platforms` field applies everywhere; a gated entry is skipped on any other OS, and `deps.Resolve` REFUSES a gated name off-platform even when the binary is present on PATH — so a gate is a capability claim, not a hint.

Platform-gated entries:

| Dependency | Platform | Required | Purpose |
| --- | --- | --- | --- |
| `ps` | darwin | yes | process-table inspection |
| `lsof` | darwin | yes | open-file inspection |
| `launchctl` | darwin | yes | launch-agent wiring |
| `setsid` | linux | yes | detached helper processes |
| `systemctl` | linux | no | user-service wiring |
| `systemd-run` | linux | no | durable chat scopes from user services |
| `nohup` | linux, darwin | no | detach fallback where `setsid` is absent |
| `uv` | linux, darwin | yes | provisioned harvestpy package verifier |
| `harvestpy` | linux, darwin | yes | provisioned harvestpy interpreter |

Everything else (`tmux`, `git`, `sh`, `bash`, `zsh`, `sleep`, `script`, `go`) is ungated and must work identically on both.

Two rules follow from the table:

- A fallback's gate must cover every platform that reaches it. `setsid` is linux-only, so darwin is the only platform that ever takes the `nohup` branch — gating `nohup` to linux would make the fallback unreachable exactly where it is needed.
- Gate on the binary's real availability, and give an entry `VersionArgs` only if every gated platform's build accepts them. BSD and GNU builds of the same name diverge: `nohup --version` is a GNU extension that BSD `nohup` rejects, so a version probe there reports a working binary as broken.

## Code Standards

- **The eval protocol (K1) — the binary never execs the final tmux attach.** TUI renders on `/dev/tty`, informational output goes to **stderr**, and exactly ONE shell line goes to **stdout** for the zsh wrapper to `eval`. Bunker semantics need `exec tmux attach` to replace the interactive shell; a ✦-new Claude row spawns natively through `action.ClaudeSpawn` (no shell function), while a ✦-new Codex row still calls the user's own `cx`. Emitted lines are golden-testable — keep them that way.
- **One implementation each (K3).** Naming precedence, the kill ratchet, row classification, and run-string synthesis each live in exactly ONE package with table-driven tests. A second copy is the bug this rewrite exists to kill. The cross-cutting primitives have one façade each: `atomicfile.Write` for a whole-file replace, `sqlitedb` for every SQLite open, `tmux.Exec` (and `tmux.Socket` over it) for every tmux invocation on a chat socket, and `internal/chat` for the chat verbs a surface (CLI, MCP) calls typed — MCP reaches the rest through argv `Dispatch`, which C10 counts and only lets shrink.
- **The architecture ratchet.** `scripts/arch-check.sh` checks C1–C21 against the `.arch/` baselines, and `.claude/scripts/dev.sh verify pfm` runs it. A baseline only shrinks: `--measure` locks a shrink, a new entry is a FAIL to fix, and a genuinely new exception is a hand edit to `.arch/` named in the commit message.
- **The lint law.** `.golangci.yml` (dupl, goconst, gocritic, revive, staticcheck; gofumpt + gci + golines at 120) and `.testcoverage.yml` are gates, not advice: `make lint-new` blocks a wave on the lines it changed, `make lint` is the burn-down view, `make cover` thresholds only ratchet up. Tools are pinned in `infra/fence/tools.env` — `make tools` on the host, baked into the fence image.
- **A kill is permanent; the store table keeps its `hidden` name.** A kill of a LIVE chat always runs the detached exit choreography; `--exit` is its explicit form.
- **One binary, two kernels — and the seam is a build tag, never a runtime `if`.** Linux and macOS are both supported. Where they differ, a `_linux.go` / `_darwin.go` pair defines the same identifier and the caller stays platform-blind (`getTermios`, `nativeProcFS`, `nativeProcesses`, `schedulerIsLaunchd`). Three differences are load-bearing: there is no `/proc`, so the process table comes from `sysctl` and `ProcFS` dispatches through `gather.NewProcFS` — which still honours an EXISTING root, because that is how the jail feeds it fixtures; tmux's ioctl and format-separator spellings differ, so parse through `internal/tmux` (`format.go`) and never against one spelling; and there is no dead-launchd jail, so the installer's rc 97 gate narrows to "not mid-execution" rather than "manager not live". A platform whose constants nobody has confirmed must fail to BUILD, not fall back to a guess.
- **Never hardcode `$HOME`, `/tmp`, a socket dir, or `/proc`.** Every filesystem location resolves through `internal/paths`; `/proc` sits behind the `ProcFS` interface. This is not decoration — it is the only reason the suite can run in a jail.
- **The ratchet counts prompts, not bytes (K2).** Kill baselines are `baseline_prompts`; auto-unkill is `prompt_count > baseline`. Legacy byte baselines convert exactly once at import.
- **Account identity, emoji, theme, and permission posture come ONLY from `internal/config`** — a hardcoded account count, `.cc/N` literal, medal emoji, or bypass flag outside the config package is a defect.
- **A seat is identified by lineage, never by file recency.** Codex ≥0.146 writes subagent threads into `~/.codex/sessions` beside real seats, so the newest rollout there is usually a child — its `session_meta` carries `thread_source: subagent` and a `parent_thread_id`. `store/lineage.go` folds a child into its parent so it never earns a row; anything reading the rollout tree directly checks those two fields first, or it reads a subagent's empty context as a seat's amnesia.
- **Identity is derived where the chat is, never where the message is delivered.** A detached process — the `--then` waiter, any dispatcher a chat backgrounds — is reparented, severing the process chain ancestry recovery walks, and a codex tool shell carries neither `$TMUX` nor a session id of its own: it derives NOTHING and its message goes out UNSIGNED. The chat resolves its own identity while it still can and states it through `CHAT_SENDER_SESSION` / `CHAT_SENDER_LABEL` / `CHAT_SENDER_SID`, which `inject` reads from its OWN environment only — a chat states who IT is, never who somebody else is. A codex seat driven by `codex app-server` never has tmux in its ancestry at all (one server, reparented to init, serving every seat), so there identity comes from `CODEX_THREAD_ID` through the fleet's thread→socket binding — the last rung, because that variable is inherited and must never rename a process that has a pane of its own.
- **Destructive commands default to a dry run, and the dry run IS the apply's preview.** `reap`, `archive`, and `heal` classify identically with and without `--apply`; only the actions differ. A preview that disagrees with the run it previews is worse than none, and every unknown — an unanswerable busy query, a socket that did not respond, a chat writing its transcript right now — resolves toward keeping what exists.
- **A probe that could not run never returns "nothing found."** A pane capture on the wrong socket returns silence identical to a quiet chat; `kill -0` cannot tell a healthy waiter from a reparented deaf one. Build the distinguishing signal into the probe and return an error, not an empty set. This is the root law at engine scale, and it is the single most common defect class here.
- Errors wrap with context (`fmt.Errorf("...: %w", err)`); a swallowed error in a gather path renders as a missing chat row, which reads as "no such chat."
- SQLite migrations are additive and numbered — a new `migration_vN.sql` plus its `go:embed`, never an edit to `schema.sql` that existing databases will never see.
- Behavior belongs in Go, not in the shim (`internal/installer/assets/shim/pfm.zsh`). If a fix is easier in the shim, that is a signal the Go surface is wrong.

## Testing Rules

Package tests beside every package, plus the end-to-end jail tests under `cmd/pfm/`. Tiers are defined in `TESTPLAN.md` § Legend:

| Tier | Means |
| --- | --- |
| `JAIL` | fully exercisable via the `PFM_*` env overrides; reference harness `testdata/e2e.sh` |
| `JAIL+tmux` | needs a real tmux server on a **scratch socket inside the jail's `TMUX_TMPDIR`** |
| `JAIL+sh` | zsh/bash fixture with a fake `$HOME`, scratch DB, and an EMPTY socket dir |
| `LIVE-READ` | read-only observation of live state (`ls --tsv`, `--plain`, `doctor`, `sqlite3 -readonly`) |
| `REAL-SESSION` | ⚠ cannot be jailed — needs a genuine `claude`/`codex` process. Schedule deliberately, never incidentally |

- **No test may touch a live `cc-*` / `cx-*` socket or the real `/tmp/cc-sid`.** Every test sets `TMUX_TMPDIR = t.TempDir()`.
- Keep scratch socket paths SHORT — a long `TMUX_TMPDIR` hits "File name too long" and the failure looks like a tmux bug.
- Table-driven tests for every single-implementation rule (K3); golden files for emitted shell lines.
- A `REAL-SESSION` flow that cannot be jailed is NAMED in `TESTPLAN.md` § "Flows that CANNOT be jailed" — never quietly left uncovered.

## Environment Variables

Test-jail overrides only — **not a config system** (`internal/paths/paths.go`):

`PFM_HOME` · `PFM_DB` · `PFM_FLEET_DB` · `PFM_SID_DIR` · `PFM_CLAUDE_ROOTS` · `PFM_CODEX_ROOT` · `PFM_TMUX_DIR` · `PFM_PROC_ROOT` · `PFM_TMUX_CONF`

`PFM_TMUX_CONF` is load-bearing beyond the jail: unset, a chat's tmux server loads the user's own `~/.tmux.conf` — because a chat IS a terminal the user lives in, and one that ignores their config wears the wrong status bar. Jails set it to `/dev/null` so a real machine config can never steer a fixture.

Test knobs outside `internal/paths`: the scan clock `PFM_TEST_NOW_NS` (`internal/fleet/scan.go`); `PFM_TEST_FRESH_SOCKET` (`internal/spawn/socket.go`).

## Boundaries

- The engine reads and writes the user's real chat state. A destructive operation on a live socket is not recoverable by a rerun — verify the target resolves before acting, and prefer refusing to guessing.
- `pfm.dev` is a local build artifact, never the shipped path. The host mirror build is `make host-install` — it stamps `-ldflags "-X main.version=$(cat VERSION)"`, so a bare `go build -o ~/.local/bin/pfm ./cmd/pfm` is never the documented command (it reports `--version` as `dev`, not the release).
- The installer lives in `internal/installer/`, and every staged host asset (shim, units, command cards) comes from `internal/installer/assets/` — the binary is the single source of truth; no external template dir exists.
