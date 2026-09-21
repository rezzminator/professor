# pfm layout — phase P2c "layout moves"

**Status:** DESIGN, brownfield, executable. Measured on `wave/reliability-gates @ c28b072d` (2026-09-17), `pfm/`-relative paths (module `github.com/rezzminator/professor/pfm`). Method: `/quality:llm-codebase` (brownfield: measure, then migrate incrementally — under a tenth of the tree moves, so no rebuild). Companion: `docs/dev/pfm-architecture.md` (the target package map and C1–C21); this document is its layout slice and changes where files live, never what pfm does.

**Precondition (P2b done first):** `pfm/internal/dream/**`, `pfm/prompts/**` and `cmd/pfm/dream_command.go` + `dream_command_test.go` are gone. `pfm/prompts/embed.go` embeds only `dreamer` and its only importers are `internal/dream/resources/resources.go` and `internal/dream/live_contract_test.go`, so P2b must delete the whole `pfm/prompts/` package — a surviving `//go:embed dreamer` with no files is a compile error. Step 0 below checks this.

**Scope ruling — what a "layout move" is.** A move is `git mv` + package-clause/import rewrite + `go build ./...` green, with no exported API designed. Measured against that bar, the four `cmd/pfm` units the architecture doc extracts later cannot leave `package main` by moving: the doctor files use 11 `package main` symbols from other files (`commandRuntime newFlagSet parseFlags closeCommandResource expectedEngineCapabilities mcpDaemonReachability printProfessorDoctor version` …), the hook-entry files 22, the update files 10, the picker files 11. Those extractions stay the architecture doc's steps 3–7. P2c gives each unit its grep-true file prefix **in place** (`doctor_* internal_* ls_* chat_* update_*`), so the later extraction is a prefix strip (`cmd/pfm/doctor_vscode.go → internal/doctor/vscode.go`) and never a re-discovery.

## (a) Principles, applied here

**One directory per unit of change.** A unit is what co-changes: the architecture doc's commit census (cmd/pfm+installer 45, cmd/pfm+ui 44, cmd/pfm+mcpserv 38) says the units are chat verbs, the picker, doctor, hook entries, update, installer. Under `internal/` the boundaries already hold; the defects are four packages that exist only to hold one file another package should own (`shared`, `tmuxfmt`, `engine/matchutil`, `chatkeys`), and `cmd/pfm`, where 60 files carry no unit in their names, so `ls` cannot show a unit and a reader finds the doctor's eight files by grepping `Doctor` across 18K lines. After P2c every `cmd/pfm` file's first token is its unit, and the three fold targets are the packages their importers already import.

**Tests, fixtures, prompts and docs beside the package.** Go already puts `x_test.go` beside `x.go` (C13 enforces the mirror); the exceptions are a root `shim/` holding tests for `internal/installer/assets/shim/pfm.zsh` two directories away, and two root operator docs (`HARVESTER.md`, `HEADLESS.md`) whose subjects are `internal/harvest` and `internal/headless`. Root `testdata/` stays: `golden/` has three consumers (`action`, `compose`, `ui`), `codex-state-schema.sql` two (`kill`, `store`), and the `claude-store codex-store crumbs proc` corpora feed the jail through `PFM_*` overrides and `testdata/e2e.sh` — a corpus with N consumers has no single package to sit beside. Single-consumer fixtures already sit beside their consumer (`statusline/testdata`, `usagehook/testdata`, `harvestpy/testdata`). `e2e/` stays at the root: it builds the binary and drives the install → update flow of the whole product, and `dev.sh iso e2e` names `./e2e/...`.

**One façade per mechanism — named.** `internal/tmux` (every tmux invocation: `Command`, `Invocation`, `CouldNotRun`, and after P2c the `-F` parsing `FormatSplit`/`FormatJoin`), `internal/atomicfile.Write` (whole-file replace), `internal/sqlitedb` (every SQLite open), `internal/paths` (every filesystem location and `PFM_*` read), `internal/deps` (every external binary), `internal/engine` (which engines exist; after P2c also `MatchCommand`, the one executable matcher), `internal/config` (accounts, emoji, theme, posture), `internal/chat` (the chat verbs every surface calls typed; after P2c also the tmux key-name contract `KeyValid`/`KeyNames`), `internal/fleetdb` (the operator-decision store, renamed from `shared`). No façade is created empty: each of the four folds moves an existing implementation into the façade that already owns the concept.

**Grep-true naming.** The first grep for a concept lands in its home: `grep -rl fleetdb` finds the fleet DB package and nothing else, where `shared` today matches 48 files and the word; `ls cmd/pfm/doctor_*` is the doctor; `ls cmd/pfm/internal_*` is every `pfm internal` entry body; `ls cmd/pfm/ls_*` is the picker; `ls cmd/pfm/chat_*` is the chat verb surface. A file name in `cmd/pfm` is `<unit>_<concept>[_command].go`; `_command` is kept where the file is a top-level command adapter and dropped for hook entries and picker internals, so the unit prefix, not the suffix, carries the meaning. The two `_e2e_test.go` files are renamed to the `_jail_test.go` tier they belong to (`TESTPLAN.md` § Legend names the tier `JAIL`; `_e2e_` is the opt-in `e2e/` suite's word).

## (b) Target tree

```
pfm/
  CLAUDE.md AGENTS.md TESTPLAN.md      # binary-level law and flow matrix (AGENTS.md compiled from CLAUDE.md)
  Makefile go.mod .golangci.yml .testcoverage.yml
  .arch/                               # C1–C21 baselines, only shrink; re-keyed by hand when a file moves (§ e)
  scripts/arch-check.sh                # the ratchet
  testdata/                            # multi-consumer fixtures: claude-store codex-store crumbs proc golden e2e.sh codex-state-schema.sql
  e2e/                                 # opt-in installer end-to-end suite (build tag e2e); builds and drives the binary
  cmd/pfm/                             # CLI shell: one file prefix per unit
    main.go runtime_config.go engines.go   # run() dispatch, Runtime loading, engine composition root
    chat_*.go                          # `pfm chat <verb>` adapters: dispatch, new, ask, reload, keys, satellite, caller identity, inject resume
    ls_*.go                            # the picker: `pfm ls` loop, refresh pipeline, K1 action dispatch
    doctor_*.go                        # `pfm doctor` and its probe families (harness prompt, spawn audit, tmux titles, prepush, vscode, mcp client)
    internal_*.go                      # every `pfm internal <entry>` body (hook entries, launch, then, update-check, prompt block)
    update_*.go init_command.go        # binary self-update, project baselines
    <command>_command.go               # one adapter per remaining top-level command (archive, codex, config, harvest, heal, install, issues, mcp_serve, namesync, reap, statusline, uninstall, whoami, headless_exec)
    mcp_shared.go                      # the one bridge from the command package into the MCP server
    *_test.go                          # same prefixes; jail scenario tests keep their `<flow>_jail_test.go` names
  internal/
    chat/                              # verb layer + target resolution + (new) keys.go: the tmux key-name contract
    fleetdb/                           # RENAMED from shared/: comms, issues, the fleet store (operator decisions)
    store/                             # the index DB (rebuildable); `indexdb` rename is architecture ruling 4, not P2c
    sqlitedb/ atomicfile/ paths/ deps/ config/   # façades, unchanged
    tmux/                              # tmux.go + (new) format.go: the -F format parser absorbed from tmuxfmt/
    engine/                            # engine.go builtin.go + (new) match.go: MatchCommand absorbed from matchutil/
      claude/ codex/ opencode/         # per-engine capability adapters; imported only by cmd/pfm/engines.go
    installer/                         # host wiring; assets/ is staged verbatim (go:embed), never holds Go tests
      assets/{bin,launchd,prompts,shim,systemd,vscode}/
      shim/                            # MOVED from pfm/shim/: the zsh shim's tests, one hop from assets/shim/pfm.zsh
    harvest/  README.md                # MOVED from pfm/HARVESTER.md
    headless/ README.md                # MOVED from pfm/HEADLESS.md; run/ is the one-shot process boundary
    harvestmcp/ harvestpy/             # unchanged (harvestpy is the pinned Python sidecar)
    action agentopen agentrole archive ask binwatch codexappendix codexgen codexmeta compose fleet gather heal
    index inject kill mcpserv naming nudge professor reap rearm recovery reload resolve sky spawn stale stats
    statusline testjail theme transcript ui update updatecheck usagehook     # unchanged
```

Gone after P2c: `pfm/shim/`, `pfm/internal/shared/`, `pfm/internal/tmuxfmt/`, `pfm/internal/engine/matchutil/`, `pfm/internal/chatkeys/`, `pfm/HARVESTER.md`, `pfm/HEADLESS.md` (and, from P2b, `pfm/prompts/`, `pfm/internal/dream/`).

## (c) Move table

Every row is exact. "Symbol" rows name the identifier rewrite the executor performs with the move; a row without one is a pure `git mv`.

**Package folds and renames**

| # | From | To | Reason |
| --- | --- | --- | --- |
| 1 | `internal/shared/shared.go` | `internal/fleetdb/fleetdb.go` | C8: `shared` is a negation name; the package IS the fleet DB (architecture glossary). Package clause `shared` → `fleetdb`; 48 files rewrite `shared.X` → `fleetdb.X` |
| 2 | `internal/shared/comms.go` | `internal/fleetdb/comms.go` | with #1 |
| 3 | `internal/shared/issues.go` | `internal/fleetdb/issues.go` | with #1 |
| 4 | `internal/shared/shared_test.go` | `internal/fleetdb/fleetdb_test.go` | test mirrors its source stem |
| 5 | `internal/shared/comms_test.go` | `internal/fleetdb/comms_test.go` | with #2 |
| 6 | `internal/shared/issues_test.go` | `internal/fleetdb/issues_test.go` | with #3 |
| 7 | `internal/tmuxfmt/tmuxfmt.go` | `internal/tmux/format.go` | one tmux façade (architecture § 4); importers `action gather reap resolve cmd/pfm` all import `tmux` already. Symbols: `tmuxfmt.SplitN` → `tmux.FormatSplit`, `tmuxfmt.Join` → `tmux.FormatJoin`; drop its `// Package` line |
| 8 | `internal/tmuxfmt/tmuxfmt_test.go` | `internal/tmux/format_test.go` | with #7; package clause `tmuxfmt` → `tmux` |
| 9 | `internal/engine/matchutil/match.go` | `internal/engine/match.go` | C8 (`*util*`); architecture step 8. Symbol: `matchutil.Command` → `engine.MatchCommand` (5 call sites: `gather/agents.go` ×2, `engine/{claude,codex,opencode}/match.go`); C17 `command:` twin shrinks by one |
| 10 | `internal/chatkeys/keys.go` | `internal/chat/keys.go` | "the tmux key-name contract shared by the chat command and its MCP adapter" — both importers already import `chat`. Symbols: `chatkeys.Valid` → `chat.KeyValid`, `chatkeys.Names` → `chat.KeyNames` (5 call sites in `cmd/pfm/chat_keys_command.go`, `internal/mcpserv/server.go`); drop its `// Package` line |

**Tests and docs beside their subject**

| # | From | To | Reason |
| --- | --- | --- | --- |
| 11 | `shim/shim_test.go` | `internal/installer/shim/shim_test.go` | tests of `internal/installer/assets/shim/pfm.zsh` sit one hop from it; the fixture path at `shim_test.go:351` becomes `filepath.Join("..", "assets", "shim", "pfm.zsh")`. Not under `assets/`: `installer/assets.go:16` embeds that whole tree and would stage the tests onto the host |
| 12 | `shim/chat_server_shim_test.go` | `internal/installer/shim/chat_server_shim_test.go` | with #11 |
| 13 | `shim/retirement_test.go` | `internal/installer/shim/retirement_test.go` | with #11 |
| 14 | `HARVESTER.md` | `internal/harvest/README.md` | the package's operator doc beside the package; re-point `docs/demo/inventory-pfm-cli.md:3,177,194,265` and `docs/demo/inventory-harvester.md:101` |
| 15 | `HEADLESS.md` | `internal/headless/README.md` | same; re-point `docs/demo/inventory-pfm-cli.md:3,194,266` |
| 16 | `cmd/pfm/attach_e2e_test.go` | `cmd/pfm/attach_jail_test.go` | one suffix per tier (naming audit § 1.15) |
| 17 | `cmd/pfm/lineage_e2e_test.go` | `cmd/pfm/lineage_jail_test.go` | same |

**`cmd/pfm` unit prefixes — doctor** (`ls cmd/pfm/doctor_*`)

| # | From | To | Reason |
| --- | --- | --- | --- |
| 18 | `cmd/pfm/harness_prompt_doctor.go` | `cmd/pfm/doctor_harness_prompt.go` | doctor probe family |
| 19 | `cmd/pfm/harness_prompt_baselines.go` | `cmd/pfm/doctor_harness_prompt_baselines.go` | `printHarnessPromptDoctor` lives here |
| 20 | `cmd/pfm/spawn_audit_doctor.go` | `cmd/pfm/doctor_spawn_audit.go` | probe family |
| 21 | `cmd/pfm/tmux_titles_doctor.go` | `cmd/pfm/doctor_tmux_titles.go` | probe family |
| 22 | `cmd/pfm/prepush_doctor.go` | `cmd/pfm/doctor_prepush.go` | probe family |
| 23 | `cmd/pfm/vscode_doctor.go` | `cmd/pfm/doctor_vscode.go` | probe family |
| 24–28 | `cmd/pfm/harness_prompt_{doctor,capture,metadata,native,scope}_test.go` | `cmd/pfm/doctor_harness_prompt_{,capture_,metadata_,native_,scope_}test.go` | tests follow #18 (`doctor_harness_prompt_test.go`, `doctor_harness_prompt_capture_test.go`, …) |
| 29 | `cmd/pfm/spawn_audit_doctor_test.go` | `cmd/pfm/doctor_spawn_audit_test.go` | follows #20 |
| 30 | `cmd/pfm/tmux_titles_doctor_test.go` | `cmd/pfm/doctor_tmux_titles_test.go` | follows #21 |
| 31 | `cmd/pfm/vscode_doctor_test.go` | `cmd/pfm/doctor_vscode_test.go` | follows #23 |

**`cmd/pfm` unit prefixes — `pfm internal` entries** (`ls cmd/pfm/internal_*`; the entry name is the file stem)

| # | From | To | Reason |
| --- | --- | --- | --- |
| 32 | `cmd/pfm/agent_open_command.go` | `cmd/pfm/internal_agent_open.go` | `pfm internal agent-open` |
| 33 | `cmd/pfm/chat_server_command.go` | `cmd/pfm/internal_chat_server.go` | `pfm internal chat-server` |
| 34 | `cmd/pfm/claude_version_command.go` | `cmd/pfm/internal_claude_version.go` | `pfm internal claude-version` |
| 35 | `cmd/pfm/clear_kill_command.go` | `cmd/pfm/internal_clear_kill.go` | `pfm internal clear-kill` |
| 36 | `cmd/pfm/codex_appendix_command.go` | `cmd/pfm/internal_codex_appendix.go` | `pfm internal codex-appendix` |
| 37 | `cmd/pfm/codex_launch_compat.go` | `cmd/pfm/internal_codex_launch.go` | `pfm internal codex-launch` |
| 38 | `cmd/pfm/compact_nudge_command.go` | `cmd/pfm/internal_compact_nudge.go` | `pfm internal compact-nudge` |
| 39 | `cmd/pfm/epic_inject_command.go` | `cmd/pfm/internal_epic_inject.go` | `pfm internal epic-inject` |
| 40 | `cmd/pfm/exit_close_command.go` | `cmd/pfm/internal_exit_close.go` | `pfm internal exit-close` |
| 41 | `cmd/pfm/exit_intercept_command.go` | `cmd/pfm/internal_exit_intercept.go` | `pfm internal exit-intercept` |
| 42 | `cmd/pfm/explore_deny_command.go` | `cmd/pfm/internal_explore_deny.go` | `pfm internal explore-deny` |
| 43 | `cmd/pfm/launch_command.go` | `cmd/pfm/internal_launch.go` | `pfm internal launch` |
| 44 | `cmd/pfm/launcher_repair_command.go` | `cmd/pfm/internal_launcher_repair.go` | `pfm internal launcher-repair` |
| 45 | `cmd/pfm/reload_intercept_command.go` | `cmd/pfm/internal_reload_intercept.go` | `pfm internal reload-intercept` |
| 46 | `cmd/pfm/then_command.go` | `cmd/pfm/internal_then.go` | `pfm internal then` |
| 47 | `cmd/pfm/update_notice_command.go` | `cmd/pfm/internal_update_check.go` | `pfm internal update-check`; it is a hook entry the installer wires, so the entry unit wins over the update unit |
| 48 | `cmd/pfm/prompt_block.go` | `cmd/pfm/internal_prompt_block.go` | the UserPromptSubmit block answer only hook entries use |
| 49–60 | `cmd/pfm/{chat_server_command,claude_version_command,clear_kill_jail,codex_launch_compat,compact_nudge_command,epic_inject_command,exit_close_command,exit_close_reload,exit_intercept_command,explore_deny_command,launch_command,reload_intercept_command}_test.go` | `cmd/pfm/internal_{chat_server,claude_version,clear_kill_jail,codex_launch,compact_nudge,epic_inject,exit_close,exit_close_reload,exit_intercept,explore_deny,launch,reload_intercept}_test.go` | tests follow #32–#45 |
| 61 | `cmd/pfm/update_notice_command_test.go` | `cmd/pfm/internal_update_check_test.go` | follows #47 |

`kill-exit`, `primary-get`, `primary-set`, `stale` are dispatched inline in `main.go:runInternal` and `reload-run` is the reload verb's worker in the reload file — they have no file of their own and get none (no stubs).

**`cmd/pfm` unit prefixes — picker and chat**

| # | From | To | Reason |
| --- | --- | --- | --- |
| 62 | `cmd/pfm/commands.go` | `cmd/pfm/ls_command.go` | `runLS`/`runIndex` and the picker's launch helpers; "commands" names nothing |
| 63 | `cmd/pfm/pipeline.go` | `cmd/pfm/ls_pipeline.go` | the refresh cadence and fleet scan stream of `pfm ls` |
| 64 | `cmd/pfm/action_dispatch.go` | `cmd/pfm/ls_action_dispatch.go` | the K1 one-line action protocol for a picker selection |
| 65–66 | `cmd/pfm/pipeline_{async,idle_backoff}_test.go` | `cmd/pfm/ls_pipeline_{async,idle_backoff}_test.go` | follow #63 |
| 67 | `cmd/pfm/action_dispatch_test.go` | `cmd/pfm/ls_action_dispatch_test.go` | follows #64 |
| 68 | `cmd/pfm/headless_command.go` | `cmd/pfm/chat_dispatch.go` | it is the `pfm chat <verb>` dispatcher (`runChat`, `runChatWithRuntime`, `headlessTarget`, `reportTargetError`) plus the `pfm headless <verb>` alias rows; "headless" today means `headless exec` (`headless_exec_command.go` keeps its name) |
| 69 | `cmd/pfm/run_command.go` | `cmd/pfm/chat_new_command.go` | `runRun` is `pfm chat new` / its `headless run` alias; `run_command.go` collides with the `run*` entry-point prefix |
| 70 | `cmd/pfm/ask_command.go` | `cmd/pfm/chat_ask_command.go` | `pfm chat ask` |
| 71 | `cmd/pfm/reload_command.go` | `cmd/pfm/chat_reload_command.go` | `pfm chat reload` and its `reload-run` worker |
| 72 | `cmd/pfm/inject_resume.go` | `cmd/pfm/chat_inject_resume.go` | the dormant half of the chat inject ladder |
| 73 | `cmd/pfm/caller_engine.go` | `cmd/pfm/chat_caller_engine.go` | caller identity for chat verbs (canonical per dup audit A3) |
| 74–78 | `cmd/pfm/reload_{args,command,engine_roster,hide,jail}_test.go` | `cmd/pfm/chat_reload_{args,command,engine_roster,hide,jail}_test.go` | follow #71 |
| 79–81 | `cmd/pfm/run_{engine_roster,jail,role}_test.go` | `cmd/pfm/chat_new_{engine_roster,jail,role}_test.go` | follow #69 |
| 82 | `cmd/pfm/ask_superseded_test.go` | `cmd/pfm/chat_ask_superseded_test.go` | follows #70 |
| 83 | `cmd/pfm/caller_engine_test.go` | `cmd/pfm/chat_caller_engine_test.go` | follows #73 |

**Stays** (a reader would expect a move; none happens)

| Path | Why it stays |
| --- | --- |
| `cmd/pfm/doctor.go`, `doctor_mcp_client.go`, the ten `doctor_*_test.go` | already carry the prefix; `internal/doctor/` is architecture step 5 (11 main-package symbols, not a move) |
| `cmd/pfm/engines.go`, `mcp_shared.go`, `runtime_config.go`, `main.go`, `whoami_command.go`, `headless_exec_command.go` | composition root, MCP bridge, runtime, dispatch, and two top-level commands named for themselves |
| `cmd/pfm/<flow>_jail_test.go` scenario tests (`booting_row`, `two_way`, `same_name_resolution`, …), `main_test.go`, `testmain_test.go`, `webgl_glyph_guard_test.go`, `headless_matrix_test.go`, `fourth_engine_test.go`, `dev_binary_test.go`, `jail_home_test.go`, `internal_unknown_test.go` | binary-level contract tests over `run()`; they belong to no single unit and keep the tier suffix |
| `internal/stats/limits.go`, `usage_source.go` → `internal/limits/` | NOT a move: `limits.go` uses `Header`, `Window`, `AccountLimits`, `UnknownUsedPct`, `percent` declared in `stats.go:23,61,74,76,655`, and `Header`/`Window` are also the Stats-tab `Snapshot`'s; splitting them is type surgery for the pfmd Phase 2 wave (open decision 3) |
| `internal/engine/{claude,codex,opencode}/` | per-engine adapters, one importer (`cmd/pfm/engines.go`); grep-true already |
| `internal/store/` → `indexdb` | architecture open ruling 4; a rename with a user-visible file rename attached (`fleet.db` → `index.db`), not layout |
| `testdata/`, `e2e/`, `internal/installer/assets/**`, `internal/harvestpy/**` | § (a) |
| `internal/codexgen`, `codexappendix`, `codexmeta` | three Codex concerns at three layers (installer compiler, hook appendix, rollout reader); folding them would create a `codex/` umbrella with no unit of change |

## (d) Import rules after the move

Layering, top to bottom; a package imports only downward. This is today's graph (`go list`, read-only) with the four folds applied; no new edge is introduced and no cycle is possible because every fold target is already imported by every consumer of the folded package.

1. `cmd/pfm` — imports anything under `internal/`; nothing imports it (C10 keeps `mcpserv` off argv into it).
2. surfaces and orchestration: `mcpserv`, `harvestmcp`, `ui`, `installer`, `fleet`, `chat`, `headless`, `reload`, `reap`, `heal`, `archive`, `stats`, `statusline`, `agentopen`, `engine/{claude,codex,opencode}`.
3. mechanisms over the fleet: `action`, `spawn`, `inject`, `kill`, `index`, `compose`, `gather`, `resolve`, `store`, `fleetdb`, `recovery`, `transcript`, `codexmeta`, `naming`, `usagehook`, `harvest`, `harvestpy`, `professor`, `update`, `updatecheck`, `stale`, `binwatch`, `nudge`, `rearm`, `agentrole`, `codexgen`, `codexappendix`, `ask`, `headless/run`.
4. façades and leaves: `tmux` (imports `deps` only), `sqlitedb`, `atomicfile`, `paths` (imports `engine`), `deps` (imports `engine`), `config`, `engine` (imports nothing in-module), `theme`, `sky`, `testjail`.

Rules the executor and every later wave keep:

- `internal/engine` imports no in-module package. `match.go` (#9) arrives with only `engine`'s own symbols, so this holds; if a future matcher needs `deps`, it goes in `deps`, not here.
- `internal/tmux` imports only `deps`. `format.go` (#7) imports nothing. Anything needing `paths` for socket addressing stays in the caller (`paths.SocketUnder` is the `..`-refusing resolver; P4 #6 makes it the one).
- `internal/chat` imports nothing that imports `chat` (`mcpserv` and `cmd/pfm` are its callers). `keys.go` (#10) imports nothing.
- `internal/fleetdb` imports `atomicfile paths sqlitedb` only, exactly as `shared` did; `store` imports `fleetdb` (for hides), never the reverse.
- The only import-isolation test outside `internal/dream` is none: the 13 files a `forbidden|allowlist|may not import` grep hits were read — 11 use the word for stdout/argv/asset assertions, `internal/harvest/gateway_chokepoint_test.go` is an HTTP-egress allowlist over `harvest` **file names** (`gatewayExemptFiles`), and `harvest` does not move, so no isolation test changes. `internal/dream/isolation_test.go` leaves with P2b; the `pfm/CLAUDE.md` § Code Standards paragraph about it is P2b's `/pcm` edit.
- No new package is created. The three new files (`tmux/format.go`, `engine/match.go`, `chat/keys.go`) each carry one sentence in their existing package's doc: `format.go` "parses the output of a tmux -F format string with either control-separator spelling"; `match.go` "MatchCommand is the one executable matcher for engine registry matchers and process scanners"; `keys.go` "the tmux key-name contract the chat keys verb and its MCP tool validate against".

## (e) Impact on `scripts/arch-check.sh` C1–C21 and `.arch/`

**The mechanic.** Every baseline is keyed by path. `--measure` with a baseline present keeps only entries still in both (`comm -12`), so it cannot re-key a moved file: the old key drops, the new key FAILs as `new:`. The executor therefore re-keys by hand **before** running the check, in the same commit as the move, and the check must PASS with unchanged counts: `LC_ALL=C sed -i.bak 's#<old path>#<new path>#g' .arch/*.txt && rm .arch/*.bak && for f in .arch/*.txt; do [ "$f" = .arch/cmd-budget.txt ] || LC_ALL=C sort -u -o "$f" "$f"; done` (`-i.bak` runs the same on BSD and GNU sed — the host is darwin, the fence is Linux; the baselines are byte-sorted and `comm` needs them sorted after the edit; `cmd-budget.txt` is one number and is never sorted). No count changes and no ratchet is re-measured in P2c except where a row below says so.

| Check | Keys that change | Meaning after P2c |
| --- | --- | --- |
| C1 `ceiling-src` | `cmd/pfm/chat_satellite_command.go` unchanged; `cmd/pfm/headless_command.go 832` → `chat_dispatch.go`; `reload_command.go 1216` → `chat_reload_command.go`; `update_*` unchanged | meaningful; `CEIL_SLACK=5` absorbs an import-line change per moved file |
| C2 `ceiling-test` | `cmd/pfm/update_command_test.go` etc. unchanged; no renamed test is over 1,000 | meaningful |
| C3 `cmd-budget` | none (renames add no lines; `tmuxfmt`→`tmux` in `chat_reload_command.go` is one import line for one) | meaningful; budget stays 18,073 |
| C4 `cmd-primitives` | `harness_prompt_doctor.go 1` → `doctor_harness_prompt.go`; `prepush_doctor.go 2` → `doctor_prepush.go`; `update_notice_command.go 1` → `internal_update_check.go`; `reload_command.go 1` → `chat_reload_command.go`; `headless_command.go 1` → `chat_dispatch.go` | meaningful |
| C5 `tmux-runners` | none (`internal/gather/tmuxprobe.go` stays; `format.go` never resolves the binary) | meaningful |
| C6 `atomic-writers` | `cmd/pfm/launch_command.go` → `cmd/pfm/internal_launch.go` | meaningful; dream rows left with P2b |
| C7 `sql-openers` | none (empty baseline) | meaningful |
| C8 `negation-dirs` | `internal/engine/matchutil`, `internal/shared` both gone → baseline becomes empty | **strengthened**: any negation-named dir is now a FAIL with no grandfathered entry |
| C9 `no-package-doc` | none (`shim/` tests-only dir has no non-test source, so C9 never enumerates it; `matchutil`/`chatkeys`/`tmuxfmt` docs are dropped with their packages) | meaningful |
| C10 `mcp-argv-calls` | none | meaningful |
| C11 `fleet-db-spellings` | none (`paths.go`, `installer.go` untouched; `PFM_SHARED_DB`/`SharedDB` are open decision 1) | meaningful |
| C12 `claude-dangling` | `pfm/CLAUDE.md:31` cites `shim/` and `:65` cites `internal/tmuxfmt`; neither form is caught (the regex wants a backticked trailing-slash path and excludes the `shim` prefix), so the check stays PASS while the doc dangles — fix the two sentences through `/pcm` in the same wave (§ f step 8), and drop `shim` and `prompts` from the exclusion list at `arch-check.sh:165` so a future `shim/` citation is checked | meaningful once the exclusion shrinks |
| C13 `untested-sources` | every renamed source without a same-stem test: `cmd/pfm/{agent_open_command,chat_keys_command,commands,harness_prompt_baselines,inject_resume,launcher_repair_command,pipeline,prepush_doctor,prompt_block,run_command,then_command}.go` → their new names; `internal/chatkeys/keys.go` → `internal/chat/keys.go`; `internal/engine/matchutil/match.go` → `internal/engine/match.go` | meaningful; #4 (`fleetdb_test.go`) keeps `fleetdb.go` mirrored |
| C14 / C15 | none (parse `main.go`, unchanged) | meaningful |
| C16 `env-outside-paths` | `cmd/pfm/commands.go 1` → `ls_command.go`; `launch_command.go 1` → `internal_launch.go`; `update_notice_command.go 1` → `internal_update_check.go`; `run_command.go 1` → `chat_new_command.go` | meaningful |
| C17 `dup-functions` | value strings carry paths: `command:` loses `internal/engine/matchutil/match.go` (entry becomes `command: internal/codexappendix/register.go internal/tmux/tmux.go`); `open:` → `internal/fleetdb/fleetdb.go internal/store/store.go`; `containsstring:` → `cmd/pfm/update_command.go internal/harvestmcp/remote.go` after P2b; `lastlines:` unchanged (`chat_satellite_command.go` keeps its name); `livesockets:` → `cmd/pfm/namesync_command.go internal/ui/model.go` unchanged | meaningful; the `command:` entry shrinks (P4 #8 owns the rest) |
| C18 `engine-spellings` | `cmd/pfm/commands.go 7` → `ls_command.go`; `pipeline.go 2` → `ls_pipeline.go`; `reload_command.go 3` → `chat_reload_command.go`; `run_command.go 4` → `chat_new_command.go`; `update_notice_command.go 2` → `internal_update_check.go`; `reload_engine_roster_test.go 5`, `run_engine_roster_test.go 4` → their `chat_` names; `internal/engine/opencode/match.go` unchanged | meaningful (P3 #2 shrinks it) |
| C19 `env-namespace` | `cmd/pfm/commands.go 1` → `ls_command.go` | meaningful |
| C20 `codex-home` | `cmd/pfm/reload_command.go 1` → `chat_reload_command.go` | meaningful (P3 #1 shrinks it) |
| C21 `test-jail-copies` | `cmd/pfm/attach_e2e_test.go 1` → `attach_jail_test.go`; `run_jail_test.go 1` → `chat_new_jail_test.go`; `chat_server_command_test.go 1` → `internal_chat_server_test.go`; `picker_cancel_jail_test.go` unchanged | meaningful |

No C-check becomes meaningless. Two become stronger (C8 empties; C12's exclusion list shrinks). C3's budget is not lowered by P2c — it moves no logic.

## (f) Ordered steps for the executor

Each step is one commit (gitter, pathspec = the step's files plus `.arch/`), leaves `go build ./... && go vet ./...` green in the fence, and ends with `bash scripts/arch-check.sh` reporting every line PASS (no MEASURE, no FAIL, no ERROR). Run from `pfm/`. `git mv` everywhere, never `mv`.

0. **Precondition.** `test ! -d internal/dream && test ! -d prompts && test ! -e cmd/pfm/dream_command.go` — else stop: P2b incomplete. `go build ./...` green at the start.
1. **`shared` → `fleetdb`** (#1–#6). `git mv internal/shared internal/fleetdb && git mv internal/fleetdb/shared.go internal/fleetdb/fleetdb.go && git mv internal/fleetdb/shared_test.go internal/fleetdb/fleetdb_test.go`; in `internal/fleetdb/*.go` `package shared` → `package fleetdb` and the `// Package shared` line → `// Package fleetdb is the fleet's authoritative state store — operator decisions: kills, comms, issues.`; over every file importing it (`grep -rl '"github.com/rezzminator/professor/pfm/internal/shared"' --include='*.go' .`, 48 files): rewrite the import path and `sed -E 's/\bshared\.([A-Za-z])/fleetdb.\1/g'` (verified: every `shared.` in the tree is the package selector). Re-key `.arch/` (`negation-dirs.txt` drops `internal/shared`; `dup-functions.txt` `open:` row). Build, vet, arch.
2. **`tmuxfmt` → `tmux/format.go`** (#7–#8). `git mv internal/tmuxfmt/tmuxfmt.go internal/tmux/format.go && git mv internal/tmuxfmt/tmuxfmt_test.go internal/tmux/format_test.go`; package clause → `tmux`, drop the `// Package tmuxfmt` line, rename `SplitN` → `FormatSplit`, `Join` → `FormatJoin` (declarations, the test, and the 5 call sites `cmd/pfm/reload_command.go:73`, `internal/resolve/tmux.go:46`, `internal/gather/tmuxprobe.go:159`, `internal/reap/tmux.go:83`, `internal/action/tmux.go:53`); delete the `internal/tmuxfmt` import in those 5 files (each already imports `internal/tmux`); reword the two comments that say "see internal/tmuxfmt" to "see internal/tmux/format.go". `rmdir internal/tmuxfmt`. Build, vet, arch.
3. **`matchutil` → `engine/match.go`** (#9). `git mv internal/engine/matchutil/match.go internal/engine/match.go`; package clause → `engine`, drop its `// Package` line, drop its self-import of `engine` and the `pfmengine.` qualifier inside, rename `Command` → `MatchCommand`; rewrite the 5 call sites (`internal/gather/agents.go` ×2, `internal/engine/{claude,codex,opencode}/match.go`) and remove the `matchutil` import in each (all already import `engine`). `rmdir internal/engine/matchutil`. Re-key `.arch/` (`negation-dirs.txt` now empty; `dup-functions.txt` `command:` row; `untested-sources.txt`). Build, vet, arch.
4. **`chatkeys` → `chat/keys.go`** (#10). `git mv internal/chatkeys/keys.go internal/chat/keys.go`; package clause → `chat`, drop its `// Package` line, rename `Valid` → `KeyValid`, `Names` → `KeyNames`; rewrite `cmd/pfm/chat_keys_command.go` (3 uses) and `internal/mcpserv/server.go` (2 uses), remove the `chatkeys` import in both. `rmdir internal/chatkeys`. Re-key `untested-sources.txt`. Build, vet, arch.
5. **Tests and docs beside their subject** (#11–#17). `git mv shim internal/installer/shim`; fix the fixture path at `internal/installer/shim/shim_test.go:351` to `filepath.Join("..", "assets", "shim", "pfm.zsh")`; `git mv HARVESTER.md internal/harvest/README.md && git mv HEADLESS.md internal/headless/README.md`; re-point the `docs/demo/inventory-*.md` lines named in #14–#15; `git mv cmd/pfm/attach_e2e_test.go cmd/pfm/attach_jail_test.go && git mv cmd/pfm/lineage_e2e_test.go cmd/pfm/lineage_jail_test.go`. Re-key `test-jail-copies.txt`. `go test ./internal/installer/shim/ ./cmd/pfm/ -run 'Attach|Lineage|Shim' -count=1` must run the moved tests (a PASS with 0 tests is a failure to look — check `-v` lists them). Build, vet, arch.
6. **`cmd/pfm` doctor and internal prefixes** (#18–#61). Pure `git mv` per row; no code changes. Re-key C4, C6, C13, C16, C18, C21 as § (e) lists. Build, vet, arch, then `go test ./cmd/pfm/ -count=1`.
7. **`cmd/pfm` picker and chat prefixes** (#62–#83). Pure `git mv` per row. Re-key C1, C4, C13, C16, C18, C20, C21. Build, vet, arch, `go test ./cmd/pfm/ -count=1`.
8. **Orientation truth** — through `/pcm`, since `pfm/CLAUDE.md` is guarded: `:31` "(`shim/` holds only its tests)" → "(`internal/installer/shim/` holds only its tests)"; `:65` "parse through `internal/tmuxfmt`" → "parse through `internal/tmux` (`format.go`)"; § File Structure gains one sentence: "`cmd/pfm` file names start with their unit — `chat_ ls_ doctor_ internal_ update_` — so `ls cmd/pfm/<unit>_*` lists a unit." `AGENTS.md` recompiles from it (`pfm codex build`). In the same commit (not guarded): `scripts/arch-check.sh:165` exclusion `^\`(cmd|internal|testdata|shim|e2e|prompts)` → `^\`(cmd|internal|testdata|e2e)`; `TESTPLAN.md` references to `shim/` tests, `HARVESTER.md`, `HEADLESS.md` re-pointed (`grep -n 'shim/\|HARVESTER\|HEADLESS' TESTPLAN.md`). Arch must still PASS.
9. **Close.** `dev.sh iso verify pfm` and `dev.sh iso test pfm` green; `bash scripts/arch-check.sh` all PASS with **no** `run --measure` note (a "fixed — run --measure" note means a key was dropped instead of re-keyed: find it, re-key it). Report the 83-row table as executed, with any row that could not be executed named.

## (g) Open decisions

1. **`PFM_SHARED_DB` / `paths.Values.SharedDB` after `fleetdb`.** The package is renamed but its env knob and field keep the old word, so `grep -r SharedDB` still finds the fleet DB under two names. Recommendation: rename both to `PFM_FLEET_DB` / `FleetDB` in P3 row 1's style (identifier rename; the env STRING is a jail-only override documented in `pfm/CLAUDE.md` § Environment Variables, so the string may change with a `/pcm` doc edit and C12 re-check). Cost of doing it in P2c: touches `paths.go`, `testjail`, `installer.go:2266` and every jail test that sets it — a P3-shaped edit inside a move commit. Cost of deferring: one more release where the DB has two names.
2. **`internal/tmux/format.go` symbol names.** `FormatSplit`/`FormatJoin` keep the word the concept is called by tmux (`-F` format). Alternative `SplitFields`/`JoinFields` reads better at call sites but loses the grep from "format" to the file. Recommendation: `FormatSplit`/`FormatJoin`.
3. **`internal/limits` split.** `stats/limits.go` + `usage_source.go` depend on `Header`, `Window`, `AccountLimits`, `UnknownUsedPct`, `percent` in `stats.go`, and `Header`/`Window` are also fields of the Stats-tab `Snapshot` that `ui` renders (`stats.Window` ×32 uses outside `stats`). Recommendation: not in P2c; the pfmd Phase 2 wave that needs a daemon-side poller moves the five with the sampler and has `stats` import `limits`. Cost of doing it now: a type relocation across `stats`, `ui`, `usagehook`, `cmd/pfm` with no behavior driver in this train.
4. **`cmd/pfm/chat_satellite_command.go`** (970 lines, `runChatFind runChatReadExcerpt runChatSave runChatLS runChatBranch runChatHistory runChatModal`). It already carries the `chat_` prefix but "satellite" names the file's history, not its verbs. Recommendation: leave it in P2c (a split into per-verb files is architecture step 4c, where each verb moves to `internal/chat/<verb>.go`). Cost of renaming now: churn on a file both P3 (`--sock`) and P4 (#7 no-such-chat) edit.
5. **Scenario-test names in `cmd/pfm`.** 27 `<flow>_jail_test.go` files keep flow names, not unit prefixes (`booting_row`, `two_way`, `same_name_resolution`, …). Recommendation: keep — they test `run()` end to end and belong to the binary, and forcing a unit prefix would mislabel half of them. Cost of the alternative: 27 more renames whose unit is a guess.
6. **`HEADLESS.md` home.** It documents `pfm headless exec`, whose adapter is `cmd/pfm/headless_exec_command.go` and whose engine is `internal/headless/run`. Recommendation: `internal/headless/README.md` (the parent covers `run/`). Alternative: `cmd/pfm/README-headless.md` beside the adapter — rejected because the doc's substance (flags, engines, exit codes) is the run boundary's contract.
