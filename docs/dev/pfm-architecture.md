# pfm: architecture for agent maintainers

**Status:** DESIGN, brownfield. Measured on `develop @ ea312fe` (2026-09-12). Every citation is `pfm/`-relative (module `hostops/pfm`). Nothing here is built yet. Code moves through `/wave:refine` and the fenced builder, and prompt files (`pfm/CLAUDE.md`, `.claude/**`) move through `/pfm`.
**Companion designs:** `docs/dev/pfmd-spec.md` (the daemon, owner-settled) and `docs/dev/pfm-surface.md` (the operator surface). This document changes where code lives, not what pfm does. § Migration shows where pfmd phases land on the new tree.

## Contents

- [The finding in one paragraph](#the-finding-in-one-paragraph)
- [1. Units of change](#1-units-of-change)
- [2. Tree](#2-tree)
- [3. Glossary](#3-glossary)
- [4. Façades](#4-façades)
- [5. Registries](#5-registries)
- [6. Checks](#6-checks)
- [7. Metrics](#7-metrics)
- [8. Migration](#8-migration)
- [9. Brief template](#9-brief-template)
- [10. Open rulings](#10-open-rulings)

## The finding in one paragraph

pfm has 98.5K source lines and 101.4K test lines across 64 packages. The package boundaries under `internal/` are mostly sound; three structural defects make maintenance expensive:

1. **`cmd/pfm` is where the logic lives.** It is 18,203 lines of `package main` in 58 files and imports 47 of the 63 internal packages. It is touched by **67% of all pfm commits** (146 of 219 non-release commits since 2026-06-01), and only 19 of those commits touched it alone. The MCP server reaches every stateful chat verb by building an argv slice, calling `runChatWithRuntime` in `package main`, and parsing the printed stdout (`internal/mcpserv/actions.go:32,84,295`; `cmd/pfm/mcp_shared.go:29`). The chat family's behavior is therefore unreachable except through a CLI string.
2. **Mechanisms are re-implemented per package.** There are 10 concrete tmux runners (`CommandTmux` in 7 packages, plus `agentopen.RealTmux`, `dream/seat.CommandHost` and `cmd/pfm/reload_command.go:30`), about 2,100 lines of which roughly 1,500 are the same socket-scoped `command()` shape. There are also 6 named atomic-write helpers plus about 25 inline temp-and-rename sites, 7 `sql.Open` call sites with two near-identical pragma sets, 4 JSONL line-reading idioms with 3 different line caps, and a second procfs reader in `resolve` forced by an import cycle (`gather` imports `resolve`).
3. **The reader's first hop gives wrong directions, and the lists are hand-kept.** `pfm/CLAUDE.md` cites two deleted packages (`check/`, `legacy/`), two missing docs (`PLAN.md`, `CUTOVER.md`) and an env var nothing reads (`PFM_DB_SCRIPT`). It says "no build tags, no per-OS packages" in § Stack, while 21 `_linux`/`_darwin` files exist and its own § Code Standards mandates them. Its package table covers 22 of 61 internal packages, its "Subcommands today" line misses 7 of 23 commands (`harvest config uninstall update init issues codex`), and its Platforms table misses the darwin-required `security` entry (`internal/deps/registry.go:65`). The `pfm internal` usage string names 8 of its 19 dispatched entries, and `docs/dev/pfm-surface.md:44` names 7. The MCP server's routing prose (`internal/mcpserv/server.go:103`) names 6 verbs as excluded, while 8 have no tool (`ask` and `branch` go unnamed). Two different databases are both named `fleet.db`.

The design fixes all three incrementally. No rebuild is needed: under a fifth of the tree moves.

## 1. Units of change

Co-change evidence is the number of commits since 2026-06-01, releases excluded, that touched both directories.

| Unit | Concepts it owns | Directory (target) | Co-change evidence |
| --- | --- | --- | --- |
| CLI shell | argv → typed request → exit code; global `--config`; usage | `cmd/pfm/` (dispatch only) | touched by 67% of commits today; target ≤ 25% |
| Chat verbs | the 27 `pfm chat` verbs (new, open, read, last, status, stream, inject, self-compact, goal, ask, watch, capture, keys, recover, name, kill, unkill, end, reload, whoami, find, save, branch, history, ls, modal, resolve), target resolution, caller identity | `internal/chat/` **new** | cmd/pfm+mcpserv 38 · cmd/pfm+inject 32 · inject+mcpserv 26 |
| MCP chat server | tool schemas, caller identity, JSON adaptation over `internal/chat` | `internal/mcpserv/` | the 38 above collapse into chat+mcpserv |
| Hooks and internal entries | every `pfm internal <entry>` body, its harness event, and its installer wiring | `internal/hooks/` **new** | cmd/pfm+installer 45 (the top pair) |
| Doctor | probes and verdict lines | `internal/doctor/` **new** | `cmd/pfm/doctor.go` is the hottest file (35 commits) |
| Fleet scan | one snapshot of every chat: index → gather → compose, account roots, live enrichment | `internal/fleet/` **new** (from `pipeline.go:227-690,1474-1600`) | three consumers today: the picker, `pfm chat ls`, and MCP `chat_ls` through `mcpSharedOperations` · cmd/pfm+compose 26 · cmd/pfm+gather 20 |
| Fleet picker | the `pfm ls` TUI loop: refresh cadence, cosmos sampler, key → action; its row actions (open, kill, reboot, deactivate) call `internal/chat` verbs | `internal/picker/` **new** (from `commands.go:30-620`, `pipeline.go:60-190,692-1037`) over `ui/ sky/ theme/` | cmd/pfm+ui 44 · compose+ui 26 |
| Update and baselines | binary self-update; project template pins | `internal/update/` **new**, `internal/professor/` | `cmd/pfm/update_*.go` + `init_command.go` = 1,896 lines in main |
| Usage limits | account windows, fetch, TTL, 429 backoff, shared cache | `internal/limits/` **new** (from `stats/limits.go`, `stats/usage_source.go`) | stats+statusline 15 · stats+ui 18; pfmd Phase 2's home |
| Resource stats | host, process tree, Docker, token counts for the Stats tab | `internal/stats/` | — |
| Statusline and usage hook | render; the fail-open prompt hook | `internal/statusline/`, `internal/usagehook/` | statusline+ui 15 |
| Installer | host wiring (links, units, settings, Codex agents, overlays, MCP clients) | `internal/installer/` split by surface | `installer.go` 2,484 lines, 32 commits |
| Installer migrations | one-time retire/migrate steps with a sunset | `internal/installer/migrations/` **new** | 1,624 lines in 49 retire/migrate/legacy funcs (name heuristic) |
| Harvester | transport, MCP surface, Python sidecar | `internal/harvest/`, `harvestmcp/`, `harvestpy/` | cohesive today; unchanged |
| Dream | memory organ, isolated by `internal/dream/isolation_test.go` | `internal/dream/**` | cohesive today; unchanged |
| Headless | one-shot engine processes; two-way await | `internal/headless/`, `headless/run/`, `ask/` | unchanged |
| Hygiene | dry-run-first reap, archive, heal | `internal/{reap,archive,heal}/` | unchanged |
| Engine registry | which engines exist; per-engine adapters | `internal/engine/**` | `matchutil` folds into `engine` |
| Databases | index DB (rebuildable); fleet DB (operator decisions) | `internal/store/`, `internal/fleetdb/` (from `shared/`) | two files named `fleet.db` |
| Façades | tmux, procfs, SQLite open, atomic write, JSONL lines, paths, deps, config | `internal/{tmux,procfs,sqlitedb,atomicfile,jsonl,paths,deps,config}/` | § Façades |

## 2. Tree

The target tree lists new and changed units file by file. Unchanged packages are listed by directory only.

```
pfm/
  cmd/pfm/                          # CLI shell. No exec, no SQL, no fs writes (check C4).
    main.go                         # run(): global flags → Runtime → commands table → Run
    command_table.go                # THE top-level command table {Name, Group, Summary, Hidden, Diagnostic, Run}
    flags.go                        # newFlagSet / parseFlags / parseFlagsAnywhere (from main.go:541-580)
    runtime_config.go               # commandRuntime = config.Runtime; the loaders live in internal/config/runtime.go
    chat.go                         # `pfm chat <verb>` over chat.Verbs (from headless_command.go:85 + runChatSatellite)
    internal_entry.go               # `pfm internal <entry>` over hooks.Table (from main.go:381 if-chain)
    <command>_command.go            # one thin adapter per top-level command: flags → package call → render
    main_test.go testmain_test.go   # jailTest fixture; the 31 in-process run() contract tests stay CLI-level
    <command>_*_test.go             # argv / exit-code contracts only; logic tests live with the logic
  internal/
    chat/                           # NEW. The chat verb layer. CLI, MCP, picker and pfmd all call it.
      README.md                     # ≤ 20 lines: what a verb is, what feeds it, what it feeds
      verbs.go                      # THE verb table {Name, Summary, MCPTool, ReadOnly}
      target.go                     # target resolution shared by verbs (from headlessTarget and friends)
      <verb>.go × 27                # XRequest (json tags = MCP schema) · XResult · X(ctx, rt, req) · CLI flag binding
      <verb>_test.go                # one per verb; jail scenarios named <verb>_<scenario>_jail_test.go
    hooks/                          # NEW. Harness hook bodies with no other domain home.
      README.md
      table.go                      # THE entry table {Entry, Event, Matcher, Engines, Summary}; installer reads it
      explore_deny.go epic_inject.go reload_intercept.go exit_intercept.go exit_close.go compact_nudge.go …
      <entry>_test.go
    doctor/                         # NEW. From cmd/pfm/doctor.go + *_doctor.go.
      README.md doctor.go           # probe runner; three distinct verdicts: healthy / broken / could-not-look
      deps.go hooks.go harness_prompt.go spawn_audit.go tmux_titles.go prepush.go harvest.go mcp.go codex_panes.go
      <probe>_test.go
    fleet/                          # NEW. Scan(ctx, db, Request) → Result: index → gather → reconcile → compose (from pipeline.go, codexpanes.go)
    picker/                         # NEW. The ls loop, cadence, cosmos sampler; row actions call chat verbs (from commands.go, pipeline.go)
    update/                         # NEW. From update_command.go (binary self-update).
    professor/                      # + update_project.go and init_command.go logic (template baselines)
    limits/                         # NEW. From stats/limits.go + stats/usage_source.go.
    tmux/                           # NEW. The one runner; absorbs tmuxfmt.
      runner.go format.go runner_test.go format_test.go
    procfs/                         # NEW. ProcFS interface + native readers (from gather/procfs*, resolve/procfs*)
    sqlitedb/                       # NEW. OpenWriter (WAL set) + OpenReadOnly (one timeout)
    atomicfile/                     # NEW. Write(path, body, mode): temp → chmod → write → fsync → close → rename
    jsonl/                          # NEW. Lines(r, fn): the one line iterator and line-length policy
    fleetdb/                        # RENAMED from shared/ (3 files)
    store/                          # the index DB (rename to indexdb: Open ruling 4)
    engine/  claude/ codex/ opencode/   # matchutil/ folded into engine/match.go
    installer/                      # installer.go split by surface; migrations/ below
      migrations/                   # NEW. v<version>_<slug>.go per step + steps.go ordered slice
    unchanged: action agentopen agentrole archive ask codexappendix codexgen codexmeta compose config deps
               dream/** gather harvest harvestmcp harvestpy headless headless/run heal index inject kill
               mcpserv naming nudge paths rearm recovery reap reload resolve sky spawn stats statusline
               testjail theme transcript ui updatecheck usagehook
```

## 3. Glossary

One term per concept, spelled the same in the directory, the identifier, the wire key and the test name.

| Concept | Canonical term | Directory / identifier | Wire key | Today's divergence |
| --- | --- | --- | --- | --- |
| Rebuildable transcript index | index DB | `internal/store` · `paths.Values.IndexDB` | file `~/.local/state/pfm/index.db` · env `PFM_INDEX_DB` | file is `fleet.db`, field `DB`, env `PFM_DB` |
| Operator decisions (kills, children, comms, issues, branch seats) | fleet DB | `internal/fleetdb` · `paths.Values.FleetDB` | file `~/.cc/fleet.db` · env `PFM_FLEET_DB` | package `shared`, field `SharedDB`, env `PFM_SHARED_DB`; `installer.go:2266` re-spells the path |
| One operation on one chat | verb | `internal/chat/<verb>.go` · `chat.Verbs` | CLI `pfm chat <verb>` · MCP `chat_<verb>` (`-` → `_`) | verbs live in `cmd/pfm/*_command.go`; dispatch sits in `headless_command.go:85`; `pfm headless <verb>` is a second entry to every verb, with the aliases `run`→`new` and `transcript`→`read` (`headless_command.go:54-61`) |
| Resolved paths + config for one process | Runtime | `internal/config` · `config.Runtime` (`LoadRuntime`, `RuntimeOrDefault`) | — | `mcpserv.Runtime` (a second shape; `commandRuntime` is now an alias) |
| A `pfm internal` entrypoint (hooks are the subset with a harness event) | entry | `internal/hooks/table.go` · `hooks.Table` | argv `pfm internal <entry>` (unchanged — installed wiring depends on it) | if-chain at `main.go:381`; usage lists 8 of 19 |
| The one tmux process runner | tmux runner | `internal/tmux` · `tmux.Runner` | — | `CommandTmux` ×7, `RealTmux`, `CommandHost`, `reloadCommandTmux` |
| Account usage windows | limits | `internal/limits` · `limits.Sampler` | cache `cc-usage-<uid>/acct-N.json` (unchanged) | lives in `stats` beside resource sampling |
| A one-time host migration | migration step | `internal/installer/migrations/v<ver>_<slug>.go` · `migrations.Steps` | — | 49 `retire*`/`migrate*`/`*Legacy*` funcs across 12 files |
| Executable matching per engine | engine match | `engine.MatchCommand` | — | package `engine/matchutil` |
| Removing a chat from the list (and ending it if live) | **Open ruling 5** | — | CLI `kill`, MCP `chat_kill` | CLI/package say `kill`, the UI and MCP description say `hide`, the SQL table says `hidden` |

## 4. Façades

A unit calls the façade and never the primitive. Checks C4–C7 enforce the first four rows.

| Concern | Module | Primitive it hides | Today |
| --- | --- | --- | --- |
| tmux commands | `internal/tmux` (`Runner.Command`, `ListPanes`, `Capture`, `SendLiteral`, `SendKey`, `KillServer`, `RenameWindow`) | `exec.Command(deps.Executable("tmux"), "-S", …)` + `TMUX=` env strip + `-F` parsing | 10 runners (§ finding 2); consumers keep their narrow `TmuxClient` interfaces, satisfied by `*tmux.Runner` |
| Process table | `internal/procfs` | `/proc/<pid>/{stat,environ}`, `kern.proc.pid`, `kern.procargs2` | `gather` and `resolve` each decode `stat`/`environ` on both OSes (`resolve/procfs_linux.go:22,37` vs `gather/procfs.go:113,158`); resolve's copy drops `StartTime`. `dream/seat` keeps its copy by design (Open ruling 3) |
| SQLite open | `internal/sqlitedb` (`OpenWriter`, `OpenReadOnly`) | `sql.Open("sqlite", …)` + pragmas | `store/store.go:118,162` and `shared/shared.go:128,151` are near-identical; read-only DSNs use 2000 ms vs 5000 ms at `heal/heal.go:336,403`, `index/opencode.go:228`, `store/codexstate.go:255` |
| Atomic file write | `internal/atomicfile.Write` | `CreateTemp` → `Chmod` → write → `Sync` → `Close` → `Rename` | 6 helpers that differ on `Sync`/`MkdirAll` (`usagehook/hook.go:668`, `config/config.go:1528`, `recovery/recovery.go:229`, `statusline/render.go:929`, `installer/files.go:24`, `updatecheck/updatecheck.go:194`), plus about 25 inline renames |
| Transcript/rollout lines | `internal/jsonl.Lines` | `bufio.Reader`/`Scanner` loops with ad hoc caps | 4 idioms; caps of 64 KB default, 1 MB, 8 MB and 32 MB, plus uncapped `ReadBytes` (`index/stream.go:33`, `transcript/transcript.go:294,336`, `codexmeta/header.go:90`, `recovery/recovery.go:142`, `archive/archive.go:523`, `archive/manifest.go:78`) |
| Filesystem locations and `PFM_*` env | `internal/paths` (exists) | `os.Getenv`, `$HOME`, `/tmp` literals | 13 `PFM_*` variables read outside `paths` across 8 files (e.g. `PFM_TEST_PROBE_SOCKETS` read in 4 places, `inject/engine.go:1802`, `gather/tmuxprobe.go:576`, `statusline/render.go:534,676`) |
| External commands | `internal/deps` (exists) | `exec.LookPath`, platform gates | sound |
| Engine names | `internal/engine` (exists) | `"claude"` / `"codex"` literals, socket prefixes | sound (`FromSocket` is the one parser) |
| Accounts, emoji, theme, posture | `internal/config` (exists) | `.cc/N` literals | sound per `pfm/CLAUDE.md` |
| Chat operations | `internal/chat` (new) | argv into `package main` | every surface (CLI, MCP, picker actions, pfmd RPC) calls `chat.X(ctx, rt, req)` |

Tiny duplicates that die rather than getting a façade: the `min`/`max` shadows of Go builtins (`codexgen/reconcile.go:242`, `usagehook/hook.go:788`), `contains` / `rosterContains` in place of `slices.Contains` (`codexgen/compiler.go:683`, `reload/reload.go:482`), and the `firstNonEmpty`/`first` triplet (`compose/compose.go:879`, `index/codex.go:174`, `codexmeta/header.go:108`), which moves into `codexmeta` beside its heaviest user. The rollout locator `archive/archive.go:378` calls `recovery.locate` instead of re-walking `sessions/`.

## 5. Registries

Each derived artifact names its source and the command that regenerates or verifies it.

| Derived artifact | Source | Regenerate / verify |
| --- | --- | --- |
| top-level dispatch, `pfm help`, diagnostic-command set | `cmd/pfm/command_table.go` | structural (one table); `pfm help` renders it |
| `pfm chat` dispatch, chat usage line | `internal/chat/verbs.go` | structural |
| MCP `chat_*` tool set and the server's routing instructions | `internal/chat/verbs.go` (`MCPTool` column) | `internal/mcpserv/verbs_parity_test.go`: registered tools == table's MCP rows, both directions |
| `pfm internal` dispatch and usage | `internal/hooks/table.go` | `cmd/pfm/internal_entry_test.go`: every table entry has exactly one binding |
| Claude `settings.json` + Codex hook wiring | `internal/hooks/table.go` (`Event`, `Matcher`, `Engines`) | `internal/installer/expected_hooks.go` derives from the table; installer tests pin the rendered JSON |
| package map in `pfm/CLAUDE.md` | `// Package` doc comments | `go list -f '{{.ImportPath}} — {{.Doc}}' ./...`; C9 fails a package without one |
| `PFM_*` list in `pfm/CLAUDE.md` | `internal/paths` constants | the doc points at `paths.go`; C16 ratchets reads outside `paths` |
| Platforms table in `pfm/CLAUDE.md` | `deps.Registry` (`internal/deps/registry.go`) | `pfm doctor` prints the live rows; the doc keeps only the two gating rules |
| installer migration order | `internal/installer/migrations/` files | `steps_test.go`: slice == directory listing, `Since` versions ascend, none older than the sunset window (Open ruling 7) |
| SQL schema migrations | `internal/store/migration_v*.sql` | existing `go:embed` + `migrate` (`store/store.go`) |
| over-ceiling files, primitive sites, untested sources | `pfm/.arch/*.txt` baselines | `pfm/scripts/arch-check.sh`; a baseline only ever shrinks |

## 6. Checks

`pfm/scripts/arch-check.sh`, run by `.claude/scripts/dev.sh verify pfm`. It prints one `CHECK <id> PASS|FAIL|ERROR` line per check. A missing baseline or an enumerator that parsed nothing reports **ERROR**, never PASS (verified: with no `pfm/.arch/`, every ratchet check reports ERROR). `--measure` writes candidate baselines. The script was run against `ea312fe` and produced the counts in § Metrics.

| Id | Asserts | Broken state reports |
| --- | --- | --- |
| C1 / C2 | no source file over 800 lines and no test file over 1,000 beyond `.arch/ceiling-{src,test}.txt` | `FAIL new: <file>` |
| C3 | `cmd/pfm` non-test lines ≤ `CMD_BUDGET` (lowered as each extraction lands) | `FAIL cmd/pfm = N > budget B`; `ERROR` when no `cmd/pfm` source is listed |
| C4 | no `exec.Command`, `sql.Open`, `os.WriteFile` or `os.Rename` in `cmd/pfm` beyond baseline | `FAIL new: <file> xN`; `ERROR` when no `cmd/pfm` source is listed |
| C5 | no file outside `internal/tmux` resolves the tmux binary (`deps.Executable("tmux")`) or assembles the `-S` socket argv itself | `FAIL new: <file>` |
| C6 | no named atomic-write helper, and no inline `os.CreateTemp` + `os.Rename`, outside `internal/atomicfile` | `FAIL new: <file>` |
| C7 | no `sql.Open` outside `internal/sqlitedb` | `FAIL new: <file>` |
| C8 | no negation-named directory (`*util*`, `helpers`, `common`, `misc`, `shared`) | `FAIL new: <dir>` |
| C9 | every package has a `// Package` doc comment | `FAIL new: <dir>` |
| C10 | `internal/mcpserv` never calls `backend.dispatch(` (argv into main) | `FAIL new: <file:line>` |
| C11 | `"fleet.db"` is spelled at most once in Go source | `FAIL "fleet.db" spelled N times` |
| C12 | every package, `*.md` and `PFM_*` name that `pfm/CLAUDE.md` cites exists — a `PFM_*` name only when production code uses it beyond declaring it | `FAIL new: <names>` |
| C13 | every source file has a same-stem `_test.go`, beyond `.arch/untested-sources.txt` | `FAIL new: <file>` |
| C14 | every dispatched top-level command appears in usage (structural once `command_table.go` lands) | `FAIL dispatched but not in usage: <cmd>` |
| C15 | every `pfm internal` entry appears in its usage (structural once `hooks.Table` lands) | `FAIL N dispatched, missing from usage: <entries>` |
| C16 | no `PFM_*` env read outside `internal/paths` beyond baseline — a literal `Getenv("PFM_…")` or one through a constant holding a `PFM_*` name | `FAIL <file> (new N)` |
| C17 | one free function per name across files, case-folded — `clipRunes` ×5, `isLive`/`IsLive` with opposite answers; methods and `_linux`/`_darwin` twins excluded | `FAIL <name>: <files>` |
| C18 | one spelling per engine — `Opencode`, `Oc*`/`oc*`, and `GPT`-for-Codex counted per file, only shrinks | `FAIL <file> (new N)` |
| C19 | one env namespace — `CHAT_*`, `CC_*`, `DREAM_*` reads counted per file; a `grep PFM_` must find every knob | `FAIL <file> (new N)` |
| C20 | one name for `~/.codex` — `codexRoot`/`CodexRoot`/`AccountHome` counted per file; `CodexHome` is canonical | `FAIL <file> (new N)` |
| C21 | one test jail — `os.MkdirTemp("/tmp", …)` in tests outside `internal/testjail` counted per file; `testjail.ShortRoot` is the jail | `FAIL <file> (new N)` |

C6 was widened in the same pass: any `os.CreateTemp` outside `internal/atomicfile` is a hand-rolled writer, rename or not — the both-patterns rule had hidden three scratch-file copies in `headless/run`. Beyond the ratchet, `pfm/.golangci.yml` (dupl, goconst, gocritic, revive, staticcheck; gofumpt + gci + golines formatting) is the lint law and `scripts/clone-check.sh` (jscpd) ratchets the shell/JS/Python assets the Go tools cannot see.

The body below is the exact script that produced § Metrics. § Migration step 1 commits it as `pfm/scripts/arch-check.sh` together with its `--measure` baselines. It needs bash, git and POSIX tools, and no Go toolchain.

```bash
#!/usr/bin/env bash
# pfm architecture checks — proposed home: pfm/scripts/arch-check.sh, run by `dev.sh verify pfm`.
# Every check prints exactly one line: CHECK <id> PASS|FAIL|ERROR <detail>.
#   PASS  = the enumerator ran and found no violation beyond the committed baseline
#   FAIL  = a violation the baseline does not list (a NEW over-ceiling file, a NEW copy, a NEW twin)
#   ERROR = the enumerator could not run (missing baseline, missing file, empty input) — never PASS
# --measure prints today's counts and writes candidate baselines to $OUT instead of comparing.
set -uo pipefail
PFM="${PFM:-$(cd "$(dirname "$0")/.." && pwd)}"
BASE="$PFM/.arch"                    # committed ratchet baselines (only ever shrink)
CEIL_SRC="${CEIL_SRC:-800}"; CEIL_TEST="${CEIL_TEST:-1000}"; CMD_BUDGET="${CMD_BUDGET:-18203}"
MODE="${1:-check}"; OUT="${OUT:-$BASE}"
rc=0
say()  { printf 'CHECK %-22s %-5s %s\n' "$1" "$2" "$3"; case $2 in FAIL) [ $rc -lt 1 ] && rc=1;; ERROR) rc=2;; esac; }
src()  { git -C "$PFM" ls-files '*.go' | grep -v '_test\.go$'; }
tst()  { git -C "$PFM" ls-files '*_test.go'; }
# ratchet <id> <name> <current-list-file>: FAIL on any line not in the baseline; ERROR if baseline absent.
ratchet() {
  local id=$1 name=$2 cur=$3
  if [ "$MODE" = --measure ]; then mkdir -p "$OUT"; sort -u "$cur" > "$OUT/$name.txt"; say "$id" MEASURE "$(wc -l < "$cur" | tr -d ' ') entries -> $OUT/$name.txt"; return; fi
  [ -f "$BASE/$name.txt" ] || { say "$id" ERROR "baseline $BASE/$name.txt missing — cannot tell new from old"; return; }
  local new; new=$(comm -13 <(sort -u "$BASE/$name.txt") <(sort -u "$cur"))
  if [ -n "$new" ]; then say "$id" FAIL "new: $(echo "$new" | tr '\n' ' ')"; else say "$id" PASS "$(wc -l < "$cur" | tr -d ' ') baselined, 0 new"; fi
}
cd "$PFM" || { say setup ERROR "cannot cd $PFM"; exit 2; }
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
[ "$(src | wc -l)" -gt 0 ] || { say setup ERROR "git ls-files returned no Go sources under $PFM"; exit 2; }

# C1 size ceilings (source / test), ratcheted
src | xargs wc -l | awk -v c="$CEIL_SRC"  '$2!="total" && $1>c {print $2}' > "$T/c1"; ratchet C1-ceiling-src  ceiling-src  "$T/c1"
tst | xargs wc -l | awk -v c="$CEIL_TEST" '$2!="total" && $1>c {print $2}' > "$T/c2"; ratchet C2-ceiling-test ceiling-test "$T/c2"

# C3 cmd/pfm stays dispatch: total LOC budget (lowered as each extraction lands)
n=$(src | grep '^cmd/pfm/' | xargs cat | wc -l | tr -d ' ')
if [ "$MODE" = --measure ]; then say C3-cmd-budget MEASURE "cmd/pfm = $n lines"; elif [ "$n" -gt "$CMD_BUDGET" ]; then say C3-cmd-budget FAIL "cmd/pfm = $n > budget $CMD_BUDGET"; else say C3-cmd-budget PASS "cmd/pfm = $n <= $CMD_BUDGET"; fi

# C4 primitives inside cmd/pfm (exec, sql, raw fs writes) — each belongs to a package
src | grep '^cmd/pfm/' | xargs grep -nE 'exec\.Command|sql\.Open|os\.(WriteFile|Rename)\(' | cut -d: -f1 | sort | uniq -c | awk '{print $2" x"$1}' > "$T/c4"; ratchet C4-cmd-primitives cmd-primitives "$T/c4"

# C5 one tmux runner: concrete runners outside internal/tmux/
src | grep -v '^internal/tmux/' | xargs grep -lE '^type (CommandTmux|RealTmux|CommandHost|reloadCommandTmux) struct' > "$T/c5"; ratchet C5-tmux-runner tmux-runners "$T/c5"

# C6 one atomic writer: named helpers outside internal/fsatomic/
src | grep -v '^internal/fsatomic/' | xargs grep -lE '^func (writeAtomic|WriteAtomic|atomicWrite|AtomicWrite)\(' > "$T/c6"; ratchet C6-atomic-write atomic-writers "$T/c6"

# C7 one SQLite opener: sql.Open outside the two DB packages' opener
src | xargs grep -nE 'sql\.Open\(' | grep -vE '^internal/sqlitedb/' | cut -d: -f1 | sort -u > "$T/c7"; ratchet C7-sql-open sql-openers "$T/c7"

# C8 negation-named directories
d=$(find internal cmd -type d \( -iname '*util*' -o -name helpers -o -name common -o -name misc -o -name shared \) | sort)
[ -z "$d" ] && say C8-negation-dirs PASS "none" || { echo "$d" > "$T/c8"; ratchet C8-negation-dirs negation-dirs "$T/c8"; }

# C9 every package states what it owns (the package map is `go list`, not a hand table)
: > "$T/c9"; for dir in $(src | xargs -n1 dirname | sort -u); do ls "$dir"/*.go | grep -v _test.go | xargs grep -lq '^// Package ' 2>/dev/null || echo "$dir" >> "$T/c9"; done
ratchet C9-package-doc no-package-doc "$T/c9"

# C10 MCP reaches chat verbs through typed calls, never argv + stdout parsing
src | grep '^internal/mcpserv/' | xargs grep -n 'backend\.dispatch(' | cut -d: -f1,2 > "$T/c10"; ratchet C10-mcp-argv mcp-argv-calls "$T/c10"

# C11 one name per database file
m=$(src | xargs grep -n '"fleet\.db"' | wc -l | tr -d ' ')
[ "$m" -le 1 ] && say C11-db-names PASS "\"fleet.db\" spelled $m time(s)" || say C11-db-names FAIL "\"fleet.db\" spelled $m times — two databases share one filename"

# C12 orientation doc cites only what exists (pfm/CLAUDE.md package table + doc names)
[ -f CLAUDE.md ] || say C12-claude-pointers ERROR "pfm/CLAUDE.md missing"
miss=""; for p in $(grep -oE '^\| `[a-z/]+/`' CLAUDE.md | tr -d '|` '; grep -oE '`[a-z/]+/`' CLAUDE.md | grep -vE '^`(cmd|internal|testdata|shim|e2e|prompts)' | tr -d '`' ); do [ -d "internal/$p" ] || [ -d "$p" ] || miss="$miss $p"; done
for f in $(grep -oE '`?[A-Z][A-Z_]+\.md`?' CLAUDE.md | tr -d '`' | sort -u); do [ -e "$f" ] || [ -e "../$f" ] || miss="$miss $f"; done
for v in $(grep -oE 'PFM_[A-Z_]+' CLAUDE.md | sort -u); do src | xargs grep -lq "\"$v\"" || miss="$miss $v"; done   # a cited env var something reads
[ -z "$miss" ] && say C12-claude-pointers PASS "every cited package/doc exists" || say C12-claude-pointers FAIL "dangling:$(echo $miss | tr ' ' '\n' | sort -u | tr '\n' ' ')"

# C13 tests mirror sources (Go form: x.go -> x_test.go, scenario tests prefixed by the source stem)
: > "$T/c13"; for s in $(src); do [ -e "${s%.go}_test.go" ] || echo "$s" >> "$T/c13"; done; ratchet C13-test-mirror untested-sources "$T/c13"

# C14 top-level command list: every dispatched command appears in usage (until the table makes this structural)
cases=$(awk '/^func run\(/,/^}/' cmd/pfm/main.go | grep -oE 'case "[a-z-]+"' | grep -oE '"[a-z-]+"' | tr -d '"' | grep -vE '^(help|version|internal)$')   # internal = hidden plumbing by design
[ -n "$cases" ] || say C14-usage-parity ERROR "no case labels parsed from cmd/pfm/main.go run()"
um=""; for c in $cases; do awk '/^func printUsage/,/^}/' cmd/pfm/main.go | grep -qE "\"  $c " || um="$um $c"; done
[ -z "$um" ] && say C14-usage-parity PASS "$(echo $cases | wc -w | tr -d ' ') commands, all in usage" || say C14-usage-parity FAIL "dispatched but not in usage:$um"

# C15 internal verbs: every dispatched verb appears in the `pfm internal` usage line
iv=$(awk '/^func runInternal\(/,/^}/' cmd/pfm/main.go | grep -oE 'args\[0\] (==|!=) "[a-z-]+"' | grep -oE '"[a-z-]+"' | tr -d '"' | sort -u)
[ -n "$iv" ] || say C15-internal-usage ERROR "no verbs parsed from cmd/pfm/main.go runInternal()"
line=$(grep -oE 'usage: pfm internal [a-z-]+(\|[a-z-]+)+' cmd/pfm/main.go | head -1)
[ -n "$line" ] || say C15-internal-usage ERROR "no multi-verb 'usage: pfm internal a|b' line found in cmd/pfm/main.go"
im=""; for v in $iv; do echo "$line" | grep -qE "(^|[ |])$v([|]|$)" || im="$im $v"; done
[ -z "$im" ] && say C15-internal-usage PASS "$(echo $iv | wc -w | tr -d ' ') verbs, all in usage" || say C15-internal-usage FAIL "$(echo $iv | wc -w | tr -d ' ') dispatched, missing from usage:$im"
# C16 PFM_* env reads stay inside internal/paths (plus the pipeline's test knobs) — beyond baseline
src | grep -vE '^internal/paths/' | xargs grep -nE 'Getenv\("PFM_|LookupEnv\("PFM_' | cut -d: -f1 | sort | uniq -c | awk '{print $2" x"$1}' > "$T/c16"; ratchet C16-env-outside-paths env-outside-paths "$T/c16"
exit $rc
```

## 7. Metrics

Hops are reported as the pair source-only · orientation.

| Metric | Baseline (`ea312fe`) | Target |
| --- | --- | --- |
| dirs per change (219 non-release commits since 2026-06-01) | median 2 · p75 5 · p90 9 · mean 4.15 | median 1 · p75 2 |
| share of commits touching `cmd/pfm` | 67% (146/219) | ≤ 25% |
| `cmd/pfm` non-test lines | 18,203 | ≤ 5,000 (Open ruling 2) |
| first-edit hops (source-only · orientation), 12 Claude transcripts with a pfm `.go` edit | median 13 · 0 · p75 28 · 11 · max 78 · 59 | ≤ 3 · ≤ 2 |
| files re-read ≥ 2× per session | median 0 · p75 2 · max 5 | ≤ 2 |
| zero-result searches per session | 0 | 0 |
| source files > 800 lines / test files > 1,000 | 24 / 12 | 0 / 0 (ratchet) |
| concrete tmux runners · atomic-write helpers · `sql.Open` sites | 10 · 6 · 5 files (7 calls) | 1 · 1 · 1 |
| MCP argv calls into `package main` | 3 call sites carrying every stateful verb | 0 |
| dangling pointers in `pfm/CLAUDE.md` · incomplete hand lists in it | 5 (`check/ legacy/ PLAN.md CUTOVER.md PFM_DB_SCRIPT`) + 1 self-contradiction · 3 (subcommands −7, packages −39, platforms −1) | 0 · 0 (lists replaced by `go list`, `pfm help`, `deps/registry.go`) |
| packages without a doc comment | 10 | 0 |
| `pfm internal` entries missing from usage | 11 of 19 | 0 (structural) |
| unreferenced exported funcs (heuristic, see gaps) | 5 (`harvest/mirror.go` ×3, `ui/model.go` ×2) | 0 |

**Named gaps.** The hop sample covers Claude seats only; Codex seats, where most pfm builds run, were not measured. The dead-code count is a grep heuristic. `deadcode` could not be built offline (x/tools test deps are missing from the module cache), and the installed `golangci-lint` v1.64.8 cannot read go1.27 export data (Open ruling 9).

## 8. Migration

**Incremental, not a rebuild.** `cmd/pfm` is 18% of source, and every other move is a package split or a rename. Each step is one wave, independently shippable with the suite green, and names the metric it moves. Every wave also splits any over-ceiling file it touches (the C1 ratchet).

**As built on `refactor/pfm-rearchitect`.** The order changed once, for a reason the plan missed: every verb resolves its target through the fleet scan, and the scan lived in `package main`, so no verb could leave `cmd/pfm` before the scan did. The executed order is:

- step 1's ratchet half (`pfm/scripts/arch-check.sh`, the `pfm/.arch/` baselines, `make arch` in `gate`); the guarded `pfm/CLAUDE.md` half waits for `/pfm`;
- step 6's scan half, as `internal/fleet`. Codex pane reconciliation moved with it, because `fleet.Scan` runs it. `config.Runtime` became the one runtime shape (`commandRuntime` is an alias), and the account projections became `config` methods;
- step 4(a)'s first verbs: target resolution, `last`, `status` and `read` in `internal/chat`. MCP `chat_last` and `chat_status` call `chat.Verbs` typed (C10 10 → 8).
- step 4(a)'s rest and 4(d)'s `mcpSharedOperations`: `chat.List`, `chat.Find` and `chat.NameResolver`. MCP `chat_ls`, `chat_find` and `chat_read` call `ChatVerbs`, and inject's roster rung is one resolver for the CLI and MCP. `capture`, `whoami` and `resolve` already called `inject` and `resolve` typed, so they needed no move. An index pass refuses an engine with no index source instead of skipping it.
- step 2's `internal/atomicfile`: every hand-rolled byte writer calls `atomicfile.Write`. C6 counts inline `os.CreateTemp` + `os.Rename` too, and its baseline is the 12 files the name grep missed: the streaming writers, plus the harvester's writer (`config.writeAtomic`, kept verbatim in `config/harvester_write.go`) and `harvest/cache.go`, which are held out of this wave.
- step 2's `internal/sqlitedb` and `internal/tmux`: every SQLite open goes through `OpenStore`, `OpenReadOnly` or `OpenReadWrite` (C7 7 → 0), and the tmux builders in kill, inject, resolve, reap, spawn, action, agentopen, reload, launch and the chat commands call `tmux.Command`. C5 keeps `gather/tmuxprobe.go` (its own socket addressing) and `dream/seat/host.go` (dream isolation). The ratchet runs in the C locale and inside the fence (`dev.sh verify pfm`).

The picker half of `pipeline.go` stays in `cmd/pfm` until step 6's loop half.

1. **Orientation truth and the ratchet.** Through `/pfm`, since both files are guarded: in `pfm/CLAUDE.md`, delete `check/`, `legacy/`, `PLAN.md` and `CUTOVER.md`; delete the § Stack "no build tags" sentence; replace the package table with the `go list` command and the subcommand list with `pfm help`; fix "13 jail tests". Wire `arch-check.sh` into `.claude/scripts/dev.sh verify pfm` and commit `pfm/.arch/*.txt` from `--measure`. As a code change, add the 10 missing package docs. *Moves:* orientation hops, C9, C12.
2. **Façades, bottom-up.** Land `internal/tmux` (absorbing `tmuxfmt`), `internal/procfs` (breaking the `gather → resolve` cycle that forced resolve's copy), `internal/sqlitedb`, `internal/atomicfile` and `internal/jsonl`, then move every consumer onto them. Delete the builtin shadows and the helper triplets. *Moves:* C5, C6, C7, and roughly 2K fewer lines.
3. **Registries.** `cmd/pfm/command_table.go` becomes the one table. It is a new file, because today's `commands.go` holds picker code that leaves in step 6. `internal/hooks/table.go` feeds both `pfm internal` dispatch and `installer/expected_hooks.go`, and the domainless hook bodies move into `internal/hooks/`. *Moves:* C14 and C15 become structural, the cmd/pfm+installer pair shrinks, and three twin lists in `main.go` die.
4. **Chat verbs, in four batches.** Each verb's logic moves from `cmd/pfm` to `internal/chat/<verb>.go` with typed Request/Result. Its CLI adapter shrinks to parse-and-render, its MCP tool calls the typed function, and its logic tests move with it.
   - (a) read-only verbs MCP calls today (`last`, `status`, `read`, `find`, `capture`, `whoami`, `resolve`, `ls`)
   - (b) mutating verbs MCP calls (`new`, `open`, `name`, `kill`, `unkill`, `reload`, `save`, `keys`, `goal`, `self-compact`, `inject`)
   - (c) CLI-only verbs (`stream`, `watch`, `modal`, `history`, `recover`, `end`, `branch`, `ask`)
   - (d) delete `mcpserv.Dispatch`, `cliAction`/`cliTargetAction`, `runChatSatellite` and `mcpSharedOperations`

   *Moves:* C10 to 0 and C3 down by the verbs' lines. The cmd/pfm+mcpserv pair becomes chat+mcpserv. pfmd Phases 3–4 then build on `internal/chat` instead of `package main`.
5. **Doctor.** `cmd/pfm/doctor.go` and the `*_doctor.go` files become `internal/doctor/`, one file per probe family. pfmd Phase 1's `doctor: daemon` line lands as `internal/doctor/daemon.go`. *Moves:* the hottest file leaves main.
6. **Fleet scan and picker.** The scan half of `pipeline.go` becomes `internal/fleet` (`fleet.Scan`), and `pfm chat ls`, MCP `chat_ls` and the picker all read it. The loop half, plus `commands.go:30-620`, becomes `internal/picker`, whose row actions call `chat.Open`, `chat.Kill`, `chat.Reload` and `chat.End`. Primary-account I/O (`pipeline.go:1600-1654`) moves to `internal/config`, which owns accounts. Codex pane reconciliation (`pipeline.go:1117-1470`, `codexpanes.go`) stays with the refresh loop in `internal/picker` until name convergence gets its own ruling. *Moves:* the cmd/pfm+ui, cmd/pfm+compose and cmd/pfm+gather pairs, and C3 by about 2,400 lines.
7. **Update and baselines.** `update_command.go` becomes `internal/update/`, and the logic of `update_project.go` and `init_command.go` goes to `internal/professor/`. *Moves:* C3 and C4 (5 exec sites in `update_command.go`).
8. **Renames, one mechanical commit each.** `shared` → `fleetdb` (plus the field and env); the index DB file `fleet.db` → `index.db` (an installer step that moves the file and never deletes it: an older binary still finds its hides in the fleet DB and re-indexes the rest); `matchutil` → `engine.MatchCommand`; `stats/limits.go` + `usage_source.go` → `internal/limits`, which **must land before pfmd Phase 2** so the daemon's poller and its clients import one package; and `store` → `indexdb` if ruled. *Moves:* C8, C11, glossary divergence.
9. **Installer.** Split `installer.go` by surface (Codex agents, global links, units, harvest install, overlays), and move every `retire*` and `migrate*` step into `internal/installer/migrations/` with a `Since` version and a sunset. *Moves:* C1 (the largest file), plus a standing trim of one-time code.

**What dies:** `mcpserv.Dispatch` and its argv adapters; `runChatSatellite`; the `run()` switch, `printUsage` and `diagnosticCommand` as three hand lists; the `runInternal` if-chain and its usage string; `reloadCommandTmux` and 8 more tmux runners; resolve's procfs; 5 atomic-write helpers; 2 pragma sets; `engine/matchutil`; the package name `shared`; the duplicate `fleet.db`; the package table and subcommand list in `pfm/CLAUDE.md`; and migration steps past their sunset.

**Queued specs** (`docs/dev/trains/queue/2026-08-2*`) touch `reap`, `mcp enable`, Codex rebind, spawn cgroups and same-name resolution. None conflicts. A step that moves a file a queued spec cites re-anchors that spec in the same wave, which is a coupled edit.

## 9. Brief template

Every task on this tree carries the following. A pointer to a ledger or evidence directory in place of a fact is a gap.

```
## Task <n> — <one-line change>
Target (hop 1):  pfm/<path>:<line>   "<quoted line>"
Anchors:         pfm/<path>:<line>   "<quoted fact the hand depends on>"   (one per fact)
Registry rows:   command_table.go | chat/verbs.go | hooks/table.go | .arch/<baseline>.txt — which rows change
Consumers:       <pasted tracer map of the renamed/moved name — templates/**, .claude/**,
                 installer assets, docs/dev/pfm-surface.md, docs/dev/pfmd-spec.md citations>
Commands:        .claude/scripts/dev.sh iso test pfm
                 pfm/scripts/arch-check.sh            (expected: PASS, baseline <name> shrinks by <k>)
Acceptance:      <observable behavior> · C3 budget lowered to <N>
```

Work-tree anatomy for each wave: `docs/dev/trains/<train>/waves/<n>-<slug>/{spec.md,STATE.md,receipts/,evidence/}`, at most three levels deep. Evidence a later wave needs is quoted into its spec.

## 10. Open rulings

Each ruling lists the recommendation first.

1. **Line ceiling.** 800 source / 1,000 test (24 + 12 files over today). The alternative, 600 / 800, puts 39 + 23 over.
2. **`cmd/pfm` end-state budget.** 5,000 lines, lowered wave by wave through C3.
3. **Dream isolation.** Admit `internal/tmux` and `internal/procfs` (pure mechanisms that hold no host state) to `dream/seat`'s allowlist in `internal/dream/isolation_test.go`, or keep the seat's deliberate copies. The owner's rule; the recommendation is to admit them.
4. **`store` → `indexdb`.** One mechanical rename that makes both database packages name what they hold. The recommendation is yes, late (step 8).
5. **kill / hide / hidden.** Pick one term for "remove from the list, end if live". CLI and MCP names are wire keys; a rename needs an alias period.
6. **`docs/dev/pfm-surface.md`.** Keep the hand-written prose and add a parity check that every command, verb and entry it names exists and vice versa (recommended), or generate its name columns from `pfm help --markdown`.
7. **Installer migration sunset.** How long a retire/migrate step lives. The recommendation is 10 minor versions past its `Since`. This depends on how far behind an adopter may upgrade from.
8. **JSONL line policy.** Recommended: one uncapped `ReadBytes` iterator (7 of today's 11 sites already work this way), with a partial final line returned as an error, never skipped.
9. **Dead-code gate.** Pin `golang.org/x/tools/cmd/deadcode` (the Go team's tool) so C-dead replaces the grep heuristic. It needs your approval to install.
10. **Rebuild vs incremental.** Incremental, as above.
11. **`pfm headless <chat-verb>` alias surface.** Retire it, so that `pfm headless` means only `exec`, the one-shot process. A grep over `*.md *.go *.sh *.zsh *.json *.toml` outside tests finds no consumer. It is a user-facing spelling, so the recommendation is to retire it behind one release of a deprecation notice.
12. **`docs/dev/pfm-surface.md` drift, fix now or at ruling 6.** Its top-level table documents `pfm run` and `pfm agent open`, and neither is a top-level command: `run` is a `pfm headless` alias for `chat new`, and agent-open is `pfm internal agent-open`. It also names 7 of 19 internal entries.
