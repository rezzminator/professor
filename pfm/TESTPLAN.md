# pfm (Professor-Fleet-Manager) TESTPLAN — the complete flow matrix

Every pathway through the fleet tooling: the Go engine (`pfm/`), the embedded zsh launcher shim, the `pfm chat` surface and its two-line compatibility delegate, the MCP server, the history helper, and the self-installer. One row per flow.

Go paths are relative to `~/.professor/pfm/` (the engine lives at the repo root — it is a program, not a template); embedded helper paths live under `internal/installer/assets/`. Absolute paths are absolute.

## Legend — the SAFETY column

| Token | Meaning |
| ------------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `JAIL` | Fully exercisable in a path jail: `internal/paths/paths.go` `Env*` overrides (`PFM_HOME`, `PFM_DB`, `PFM_SHARED_DB`, `PFM_SID_DIR`, `PFM_CLAUDE_ROOTS`, `PFM_CODEX_ROOT`, `PFM_TMUX_DIR`, `PFM_PROC_ROOT`) plus the test knobs `PFM_TEST_NOW_NS` (`internal/fleet/scan.go`) and `PFM_TEST_FRESH_SOCKET` (`cmd/pfm/pipeline.go`). Reference harness: `pfm/testdata/e2e.sh`. |
| `JAIL+tmux` | Needs a real tmux **server on a scratch socket inside the jail's `TMUX_TMPDIR`** — never a live fleet socket. Reference harness: `internal/*/tmux_jail_test.go` (e.g. `internal/kill/tmux_jail_test.go:58-66`). |
| `JAIL+sh` | zsh/bash fixture with a fake `$HOME`, scratch db and an EMPTY socket dir. References: `shim/shim_test.go` and `internal/installer/installer_test.go`. Window-name convergence is covered by the Go `probe-*` tmux jails. |
| `LIVE-READ` | Read-only observation of live state (`pfm ls --tsv`, `--plain`, `--killed`, `doctor`, `sqlite3 -readonly`). Never mutates. |
| **`REAL-SESSION`** | ⚠ **CANNOT be jailed.** Needs a genuine `claude` / `codex` process, a real transcript/rollout writer, or a real Claude-account login. The supervisor must schedule these deliberately on a scratch project directory. |

## Legend — the REGRESSION column

The four identity/state regressions that established this plan. A tagged row must retain a fixture.

- **B1** — resumed store-only codex thread renders the wrong name.
- **B2** — workflow twin threads: a kill resurrects as a doppelgänger row.
- **B3** — an agent-row kill is a no-op.
- **B4** — a rename that lands only in `~/.codex/session_index.jsonl` never reaches the picker.

---

## Table of contents

1. [A — `pfm` CLI subcommands and flags](#a--pfm-cli-subcommands-and-flags)
2. [B — Picker TUI: key bindings and model state](#b--picker-tui-key-bindings-and-model-state)
3. [C — Row-kind × operation cross-matrix](#c--row-kind--operation-cross-matrix)
4. [D — Index, naming and identity resolution](#d--index-naming-and-identity-resolution)
5. [E — Kill / unkill and shared state](#e--kill--unkill-and-shared-state)
6. [F — Action synthesis and launch](#f--action-synthesis-and-launch)
7. [G — MCP server (18 tools)](#g--mcp-server-18-tools)
8. [H — `pfm chat`: subcommands, guards, `--then`, exit codes](#h--pfm-chat-subcommands-guards---then-exit-codes)
9. [I — zsh shell surface: launchers](#i--zsh-shell-surface-launchers)
10. [J — Internal wiring and store](#j--internal-wiring-and-store)
11. [K — Installer and systemd units](#k--installer-and-systemd-units)
12. [Flows that CANNOT be jailed](#flows-that-cannot-be-jailed)
13. [Residual known divergences](#residual-known-divergences)
14. [Highest-risk joins](#highest-risk-joins)

---

## A — `pfm` CLI subcommands and flags

### A.1 — Codex compiler divergence and acceptance matrix

The compiler is one static-binary surface. `build` may write only generated artifacts; `check` is its read-only twin and returns rc 1 for any finding. Fixtures use invented names and a jailed HOME. The Repo A-shaped and Repo B-shaped fixtures are structurally equivalent; their only permitted tree difference is the generated-marker source path. The real-repo equivalence exercise is recorded below as a manual jailed acceptance result; it is not a permanently wired fixture in this suite.

| flow | safety | expected behavior | test / status |
| ------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | --------------------------------------------------------------------------------------------------- |
| `pfm codex build [repo-root]` and `check [repo-root]`; no legacy `generate` or `doctor` action | JAIL | build compiles; check reports current; unknown actions rc 2 | `cmd/pfm/codex_command_test.go` |
| strict `.claude/codex-build.json` version and unknown-key rejection | JAIL | unsupported version or key is a hard error naming the key | `internal/codexgen/codexgen_test.go` + CLI route |
| block-scalar frontmatter, guarded command roster, model map, persona strip, TOML escaping, root/child AGENTS, collision/suffix, repo/global skills, MCP fence | JAIL | outputs are transformed deterministically and only real command names become `$...` | `internal/codexgen/codexgen_test.go`; generic union fixture — PASS |
| MCP-backed global `chat-*` commands keep prompt cards but retire marker-owned skills; `chat-interrogate` remains a skill | JAIL | build preserves every prompt card, emits only the non-MCP interrogate skill, and removes an obsolete marker-owned chat skill | `internal/codexgen/codexgen_test.go`; `TestMCPBackedChatCommandsRetireGlobalSkillsButKeepInterrogate` |
| flags override repository config: `--home`, repeatable `--model`, adapter/preamble, excludes, never-register, suffix mode/prefix | JAIL | CLI values have highest precedence and malformed model syntax rc 2 | `cmd/pfm/codex_command_test.go` |
| dangling `$HOME/.claude/commands/*.md` symlink | JAIL | build skips and warns; check reports the dangling source and returns rc 1 | `internal/codexgen/codexgen_test.go` |
| reconcile findings: missing, stale, orphan, conflict | JAIL | check names missing/stale/orphan without writes; build names and preserves a hand-written conflict | `internal/codexgen/codexgen_test.go`; deterministic stale/orphan/conflict fixture — PASS |
| Repo A/Repo B fixture equivalence and full output trees | JAIL | byte-equivalent except explicitly ruled generated-marker command change | manual jailed equivalence: Repo A 66/66, Repo B 54/54, zero diffs after marker normalization — PASS |
| `pfm codex agents [--home PATH]` — no positional | JAIL | compiles `{home}/.professor/templates/global/agents/*.md` into sibling TOMLs, installs sources into `{home}/.claude/agents` and TOMLs into `{home}/.codex/agents`; escaping mirrors build-codex.mjs:151-153 byte-for-byte (verified against the retired host `build-global-agents.py`, since deleted) | `internal/codexgen/globalagents_test.go`, `cmd/pfm/codex_agents_command_test.go` |

| flow | safety | expected behavior (source) | regression |
| --------------------------------------------------------------------------------------------------------------------- | --------- | --------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------- |
| no args → the same interactive picker as `pfm ls` | JAIL+tmux | `main.go`, `attach_e2e_test.go` | |
| unknown subcommand → usage, rc 2 | JAIL | `main.go:62-66` | |
| `help` / `-h` / `--help` → usage on stdout, rc 0 | JAIL | `main.go:59-61` | |
| `version` → `pfm <version>`; extra arg → rc 2 | JAIL | `main.go:95-106` | |
| global `--config PATH` loads once before dispatch; malformed present files name their path and JSON byte | JAIL | `runtime_config.go`, `config_cli_test.go`, `internal/config/config_test.go` | |
| configured account roots are the exact transcript boundary; absent config preserves the three-account discovery | JAIL | `runtime_config.go`, `config_cli_test.go`, `internal/config/config_test.go` | |
| configured Claude/Codex binary and permission policy reach actual launch argv; absent config preserves current argv | JAIL+tmux | `run_jail_test.go`, `internal/action/*_test.go`, `internal/reload/reload_test.go` | |
| local `pfm.dev` is absent or carries the current `hostops/pfm/cmd/pfm` build path | JAIL | `cmd/pfm/dev_binary_test.go` | |
| `ls` (interactive) → BubblePicker on `/dev/tty`, cached first frame then streamed refresh | JAIL+tmux | `commands.go:101-121`, `ui/picker.go:12-68` | |
| routine picker refreshes every 4s and indexes only the priority project; it never walks the full transcript corpus | JAIL | `pipeline.go`, `pipeline_async_test.go` | live defect: three pickers each sustained ~60% CPU for 4h+ |
| `ls --plain` → PlainPicker, one pass, rc 0 | JAIL | `commands.go:88-100,126-128` | |
| `ls --tsv` → TSVPicker, stable rows | JAIL | `commands.go:96-99`; golden `testdata/golden/ui.tsv` | |
| `ls -a/--all` → AllView | JAIL | `commands.go:33-34,60-65` | B2 |
| `ls -K/--killed` → the kill ledger (`id`, engine, timestamp), noninteractive | JAIL | `commands.go`, `main_test.go` | B2, B3 |
| `ls --all --killed` → rc 2 (mutually exclusive) | JAIL | `commands.go:43-48` | |
| `ls --plain --tsv` (or any two renderers) → rc 2 | JAIL | `commands.go:44` (`boolCount`) | |
| `ls <id>` → same as `chat open <id>` | JAIL | `commands.go` | |
| `ls <id>` combined with any flag → rc 2 | JAIL | `commands.go:53-56` | |
| `chat open <target>` on an unindexed id → `is not indexed`, rc 1 | JAIL | `chat_command.go`, `commands.go` | B3 |
| `chat open <target>` whose CWD vanished → falls back to `$PWD` for non-live kinds | JAIL | `commands.go` | |
| `chat new --name X [prompt]` (or positional X) → chat on a fresh immutable fleet socket, rc 0 | JAIL+tmux | `run_command.go`, `action/headless.go`, `spawn/spawn.go`; `run_jail_test.go` | |
| `chat new` without `--engine` inherits the CALLING chat's engine — `CLAUDE_CODE_SESSION_ID` wins over an inherited `CODEX_THREAD_ID`; neither ambient → config default | JAIL | `caller_engine.go`, `run_command.go` (`resolveRunEngineAccount`) | |
| `chat new` without a name → rc 2; unknown `--engine` or `--effort` → rc 2 | JAIL | `headless_matrix_test.go` | |
| `chat new --engine cx` → thread named through Codex's own `/rename` UI, THEN prompted | JAIL+tmux | `spawn/spawn.go` (`nameCodexThread`) | |
| `chat new --engine cx` on a Codex without `/rename` → live chat, `UNNAMED`, warning, rc 1, composer cleared | JAIL+tmux | `TestRunReportsACodexBuildThatCannotBeRenamed` | |
| Codex boots through startup overlays (hooks review, trust) — Escape, then a composer that HOLDS, before a keystroke | JAIL+tmux | `TestCodexBootsThroughStartupModals`, `TestCodexComposerFlashBeforeAModalIsNotReadiness` | |
| A startup screen that never clears → nothing typed, chat live, `UNNAMED`, rc 1 | JAIL | `TestCodexStuckAtAStartupScreenTypesNothing` | |
| The rename field is CLEARED before typing (Codex pre-fills it with the current name) | JAIL | `TestCodexThreadIsRenamedThenPrompted` (clear token) | |
| The rename Enter is re-sent until the status line carries the name; zero typing gap DROPS it | JAIL | `spawn/types.go` (`orDefaults`), `spawn/spawn.go` (`confirmPresses`) | |
| Codex 0.154 renames silently → the rename is PROVEN from `session_index.jsonl` (the name, stamped at or after the rename began), a proven rename is never retried, the pre-filled modal is found by its `Name thread`/`Rename thread` title, and an unreadable ledger reports "could not verify", never "unnamed"; ONE proof inside `RenameCodex`, fed once by `main` (`spawn.UseCodexHomes`), so every door (chat new, chat branch, post-`/clear` re-apply, dream seat) is covered without carrying anything; the dialog is recognized only as its own gutter line with no composer on screen | JAIL | `TestCodexSilentRenameIsProvenFromTheIndex`, `TestCodexRenameOfANamedThreadFindsItsRetitledModal`, `TestCodexRenameThatCannotBeVerifiedSaysSo`, `TestRenameModalOpenReadsOnlyTheDialog`, `internal/spawn/rename_proof_test.go`, `internal/codexmeta/session_index_test.go` | live PING_PROBE launch reported unnamed |
| `chat new --prompt-file PATH` → prompt read from the file; with an inline prompt → rc 2 | JAIL | `TestRunPromptSourcesAreExclusive` | |
| `chat new --model M --effort E` → engine-specific model and effort arguments | JAIL | `TestModelAndEffortReachBothEngines` | |
| `chat new` on a chat that dies at birth → `died at birth`, rc 1 | JAIL | `spawn/spawn.go` (`waitForBoot`) | |
| `chat new` from a systemd user service launches the tmux server through `systemd-run --user --collect --scope`; missing `systemd-run` refuses loudly | JAIL | `internal/spawn/systemd_scope_linux_test.go` | REAL-SESSION restart survival remains unjailable |
| `chat new` preflights the plan's engine binary before any server exists — an unreachable bare word or missing absolute path refuses loudly naming the binary, the PATH, and the remedies; a server that dies before configuration names its pane command | JAIL | `internal/spawn/preflight_test.go`, `internal/action/headless_binary_test.go` | |
| `chat status <target> [--json]` → state/idle_seconds/engine/model/cwd/session_id/context_pct | JAIL | `headless/headless.go`; `headless_test.go` | |
| `chat status --summary` isolates the last exchange, caps at 40 words, caches complete offsets, and leaves no-flag output byte-identical | JAIL+UNIT | `chat_status_summary_test.go`, `headless/summary_test.go`, `store/summary_test.go` | |
| `chat status --ask` answers current state from the live pane + last exchange, NEVER caches, words not-live vs capture-failed vs no-exchange distinctly, caps at 40 words, and removes its temp files on every return | JAIL+tmux | `headless/statusask_test.go`, `cmd/pfm/chat_status_ask_test.go`, `mcpserv/server_test.go` | No REAL-SESSION coverage: no test drives `--ask` through a genuine spawned claude/codex seat. |
| Working summaries say `PARTIAL` and are not cached; missing binary says unavailable while crash/timeout says failed | UNIT | `headless/summary_test.go` | |
| `transcript.LastExchange` keeps the newest human turn and following tool/assistant records for both engines | UNIT | `TestLastExchangeIsEngineAgnosticAndKeepsTools`, `TestLastExchangeNamesPartialAndMissingShapesWithoutGuessing` | |
| `internal/ask` runners use deps resolution, roster homes, model/effort, a process-group timeout, stderr-tail errors, and nullable usage | JAIL | `internal/ask/ask_test.go` | |
| working vs idle comes from the TRANSCRIPT (assistant spoke last = idle), never from a timer | JAIL | `TestStateComesFromTheTranscriptNotAClock` | |
| `chat read <target> [--tail N] [--condensed] [--json]` | JAIL | `transcript/transcript.go`; `transcript_test.go` | |
| `chat last <target>` → the last assistant message, bare | JAIL | `TestLastFindsTheNewestAssistantTurn` | |
| `chat stream <target> [--filter RE] [--margin N]` → follow prompts and replies | JAIL | `TestStreamFilterWithMargin` | |
| `chat stream` ends when the chat dies instead of hanging on a quiet file | JAIL | `TestStreamFollowEndsWhenTheChatDies` | |
| `chat inject <target> <message>` → guarded delivery with `--file`, `--force-now`, and repeated `--then` | JAIL+tmux | `headless_command.go`, `internal/inject` | |
| `chat ask <target> <message>` → deliver, wait, print the answer on stdout alone | JAIL+tmux | `ask_command.go`, `headless/converse.go`; `TestAskHoldsATwoWayConversation` | |
| `ask` answers the question just asked — the frontier is taken BEFORE delivery, so an older answer cannot be returned | UNIT | `TestAwaitReturnsTheAnswerToTheQuestionJustAsked` | |
| `ask` waits through tool work: an assistant line followed by a tool call is a preamble, not an answer | UNIT | `TestAwaitWaitsThroughToolWork` | |
| `ask --timeout N` → rc 5 with the message still DELIVERED (a different fact from unheard) | JAIL+tmux | `TestAskReportsATimeoutWithoutLosingDelivery` | |
| `ask --json` → name/delivered/answer/state/tools/waited_seconds; `--progress` → condensed turns on stderr | JAIL+UNIT | `headless/converse.go`; `TestAwaitStreamsProgress` | |
| `ask` on a WORKING chat re-offers the message until it lands (inject rc 7) instead of aborting; `--now` interrupts | LIVE | `ask_command.go` (`busyRetry`, `inject.CodeBusy`) | |
| `ask` reports `superseded` when a second human turn lands mid-wait — the answer may be theirs | UNIT | `TestAwaitFlagsAnAnswerSomebodyElseMayOwn` | |
| `--timeout` bounds the WHOLE exchange: time spent waiting for a busy chat is spent from the same budget | JAIL | `ask_command.go` (`remaining`) | |
| `ask` on a chat that dies mid-answer keeps what it said, then reports it gone | UNIT | `TestAwaitKeepsWhatADyingChatManagedToSay` | |
| `chat new` with a prompt PROVES delivery from the engine transcript; unproven → rc 6 + attach hint, chat left running | JAIL+tmux | `awaitLaunch`; `TestRunRefusesToCallAnUnheardPromptDelivered` | |
| `chat new --await` → the first answer on stdout, launch summary moved to stderr | JAIL+tmux | `TestRunAwaitsTheAnswerItAskedFor` | |
| The prompt's Enter is re-sent until the composer releases it; a composer that never does → `Prompted=false` + warning | UNIT | `TestCodexPromptIsResentUntilItLeavesTheComposer`, `TestCodexPromptThatNeverSubmitsIsReportedUndelivered` | |
| A wait re-reads the transcript every poll but re-scans the fleet only every `ResolveEvery` | UNIT | `headless/converse.go` (`ResolveEvery`) | |
| `transcript.From` never consumes a half-written record, and restarts when the file shrinks | UNIT | `TestFromHoldsAPartialLine`, `TestFromRestartsWhenTheFileShrinks` | |
| `chat watch <target>` → `IDLE`/`EXIT`/`DEAD` lines + `--on-idle`/`--on-exit` hooks | JAIL | `TestWatchAnnouncesIdleThenExit`, `TestWatchReportsAChatThatVanishesAsDead` | |
| `chat ls [--all]` → live-seat listing from the Go gather/compose pipeline | JAIL | `chat_satellite_command.go`, `chat_satellite_command_test.go` | |
| An unknown name → rc 4 with `not-found` on stdout, on EVERY verb — never empty at rc 0 | JAIL | `TestUnknownChatIsRc4WithAMachineShape` | |
| A dead (non-live) chat → rc 3, explicitly, never silence | JAIL | `headless_command.go` (`codeDeadChat`) | |
| Flags parse before or after the target (`status seat --json`) | JAIL | `main.go` (`parseFlagsAnywhere`); `TestChatArgumentMatrix` | |
| `chat inject` takes its message verbatim — a leading dash is not a flag | JAIL | `runHeadlessInject` | |
| hidden `headless` compatibility alias prints a deprecation to stderr | JAIL | `TestHeadlessCompatibilityAliasIsHiddenAndDeprecated` | |
| Name resolution: name, id-prefix, socket; live row beats its resume twin; ambiguity refused | JAIL | `TestChatMatchingPrefersTheLiveSeat` | |
| `index` → counters line on stdout | JAIL | `commands.go:351-398`, `formatCounters` `commands.go:400-412` | B4 |
| `index --full` → reparse all + `last_full_index_at` meta | JAIL | `commands.go:386-392` | B4 |
| `index --progress` → start/elapsed on stderr | JAIL | `commands.go:378-379,394-396` | |
| `kill <id>` → shared row + carrier line | JAIL | `main.go:108-146`, `kill/manager.go:69-111` | B3 |
| `kill --self` → identity from `$TMUX`/`$TMUX_PANE`/`$CLAUDE_CODE_SESSION_ID` | JAIL+tmux | `main.go:114`, `kill/self.go:16-38` | B3 |
| `kill --self --exit` → detached finisher; `--exit` without a live pane → error | JAIL+tmux | `main.go:115`, `kill/manager.go:86-88,98-109` | |
| `kill` arg-shape violations (`--self` with an id, no id without `--self`) → rc 2 | JAIL | `main.go:119-123` | |
| `unkill <id>` → canonicalizes a Codex id to its lineage root first | JAIL | `main.go:148-168`, `kill/manager.go:113-131` | B2 |
| `killed` → `id\tengine\tkilled_at` per row | JAIL | `main.go:207-215` | B3 |
| `killed --prune-orphans` → dry-run report; `--yes` deletes; `--yes` alone → rc 2 | JAIL | `main.go:176-192`, `commands.go:463-511` | |
| `chat resolve <target>` → socket/session/id tuple; missing target rc 4 | JAIL+tmux | `chat_command.go`, `resolve/resolve.go` | |
| `resolve` with a bad kind → rc 2 | JAIL | `resolve/resolve.go:92-93`, `main.go:241-244` | |
| `whoami` → this process's own tmux session name | JAIL+tmux | `main.go:53`, `resolve/whoami.go:165-213` | |
| `doctor` → db + jail health; exit 0 clean, 1 warnings-only, 2 usage, 3 at least one failure (a state `pfm install --yes` owns and did not produce, or a required non-harvestpy dependency); `doctor: failures=M` prints only when M>0, `doctor: warnings=N` only when N>0, `doctor: clean` only when both are zero | LIVE-READ | `main.go:39-40`; `internal/store/health.go`; `cmd/pfm/doctor_jail_test.go` (`TestDoctorExitsThreeOnARequiredDependencyMissingAndOneOnWarningsAlone`) | issue #24 finding 1 |
| `mcp` → stdio server; any arg → rc 2 | JAIL | `main.go:69-93` | |
| `internal kill-exit --engine…` → detached finisher; missing flags → rc 2 | JAIL+tmux | `main.go:250-299`, `kill/finisher.go:94-144` | |
| `internal then …` → steer waiter | JAIL+tmux | `main.go:251-253`, `inject/then.go` | |

### A.2 — Doctor probe classification

| probe | safety | expected behavior | regression |
| --- | --- | --- | --- |
| config load error | JAIL | visible config FAILURE, rc 3 | `config_cli_test.go` |
| disabled MCP daemon | JAIL | no daemon probe or warning | `doctor_jail_test.go` |
| enabled MCP daemon reachability | JAIL | running is clean; unreachable is a warning and rc 1 | `mcp_serve_test.go` |
| enabled MCP daemon version | JAIL | matching version is clean; skew is a warning and rc 1 | `mcp_serve_test.go` |
| canonical `pfm` executable read | JAIL | readable target-HOME binary is clean; unreadable or absent canonical binary is a warning and rc 1 | `doctor_path_test.go` |
| PATH candidate resolution | JAIL | target-HOME canonical candidate first is clean; no target-HOME candidate or an in-home shadow is a warning and rc 1; host candidates are ignored | `doctor_jail_test.go`, `doctor_path_test.go` |
| PATH candidate hash | JAIL | matching target-HOME candidates are clean; target-HOME read failure or hash mismatch is a warning and rc 1 | `doctor_jail_test.go`, `doctor_path_test.go` |
| managed Claude launcher | JAIL | canonical launcher is `ok`; absent is `missing`; a native-updater replacement is `DISPLACED` — both a FAILURE, rc 3 | `launch_command_test.go`, `internal/installer/launcher_test.go` |
| host overlay symlinks (`pfm-statusline`, `tmux-title-renudge`) and statusLine wiring | JAIL | a canonical link resolving to the managed copy is `ok`; absent is `missing`, rc 3; not resolving to the managed copy is `DISPLACED`, rc 3; a configured account's `statusLine.command` still naming raw `pfm statusline` is a FAILURE, rc 3 | `cmd/pfm/doctor_host_overlay_test.go`, `internal/installer/installer.go` (`InspectHostOverlays`) |
| tmux title ownership per live socket | JAIL+tmux | INFO only, never a warning and never a write: the resolved `tmux.titles` policy is printed with its source, then each live socket is read with `show-options -g set-titles` and reported `pfm-owned` or `host-owned`; an unreadable socket is `unknown` with the reason | `cmd/pfm/tmux_titles_doctor.go`, `cmd/pfm/tmux_titles_doctor_test.go` |
| external-command registry coverage | JAIL | every production literal exec is registered and routed through `deps.Resolve`; configured engine names and provisioned harvest paths have one owner | `internal/deps/guard_test.go` |
| dependency resolve/version/minimum | JAIL | fake PATH binaries distinguish ok, below-minimum, garbage, missing, failed execution, and timeout; tmux requires 1.8; a `Required` dependency the fleet engine cannot run without (e.g. `tmux`) missing/broken/timeout/cancelled is a FAILURE, rc 3 — the opt-in harvestpy sidecar's own `Required` deps (`uv`, the provisioned interpreter) stay warnings, rc 1, since the fleet engine runs without them | `internal/deps/probe_test.go`, `doctor_external_test.go`, `cmd/pfm/doctor_jail_test.go` (`TestDoctorExitsThreeOnARequiredDependencyMissingAndOneOnWarningsAlone`) |
| dependency platform and harvest filters | JAIL | Darwin/Linux-only rows say `skipped (not this platform)` off-platform; install-owned harvest rows say provisioned-by-install or `--skip-harvest` without being probed | `internal/deps/probe_test.go` |
| configured engine self-doctors | JAIL | supported Claude/Codex doctor commands run under their own 30s self-doctor bound (falls back to the probe `Timeout` when `SelfDoctorTimeout` is unset); unsupported or interactive-only surfaces say unavailable; a summary call that outruns its bound stays `ok` and is named `timeout (<duration>)`, never broken; a real non-zero exit still reads broken and quotes the first output line | `internal/deps/probe_test.go`, `doctor_external_test.go` |
| installer-owned hooks | JAIL | every global/account Claude hook and the Codex clear-kill hook is present, parseable, canonical-binary-pointing, and ledger-owned; missing, broken JSON, and stale path are each a FAILURE (rc 3, `ReportHooks` returns `(warnings, failures)`); drift stays a distinct warning, rc 1 | `internal/installer/expected_hooks_test.go` (`TestReportHooksCountsMissingAsFailureAndDriftAsWarning`), `doctor_external_test.go` |
| global-agents wiring | JAIL | every configured account's registry links match the recorded clone's machine-global agents; `MISSING` and `UNREADABLE` are each a FAILURE, rc 3 (`ReportGlobalAgents` returns `(warnings, failures)`); `CONFLICT`, `NO-SOURCES`, and `UNRESOLVED` stay warnings, rc 1; `NO-CLONE` and `NO-CLAUDE` count neither | `internal/installer/global_fanout_test.go` |
| database open | JAIL | cannot open is a hard failure, rc 3 | `main_test.go:414-424` |
| database user-version read | JAIL | cannot read is a hard failure, rc 3 | `doctor.go` |
| database quick-check read | JAIL | cannot read is a hard failure, rc 3 | `doctor.go` |
| database schema or integrity mismatch | JAIL | mismatch is a warning and rc 1 | `doctor.go` |
| shared-store health | JAIL | healthy is clean; degraded shared state is a warning and rc 1 | `doctor.go` |
| row counts and orphaned hides | JAIL | count query failure is a hard failure, rc 3; nonzero orphaned hides is a warning and rc 1 | `doctor.go`, `store/hidden_test.go` |
| WAL stat | JAIL | absent WAL is clean; other stat failure is a warning and rc 1 | `doctor.go` |
| busy-warning counters | JAIL | zero counters are clean; nonzero counters are a warning, rc 1; an unreadable counter is a hard failure, rc 3 | `doctor.go` |
| process-table read | JAIL | readable, including empty, is clean; unreadable is a warning and rc 1 | `doctor.go`, `internal/gather` |
| configured roots | JAIL | existing directories are clean; missing, non-directory, or unreadable roots are warnings and rc 1 | `doctor.go` |
| SID crumb directory | JAIL | missing after clean install is empty and clean; readable invalid entries warn; non-directory or unreadable probe remains an error and rc 1 | `doctor_jail_test.go`, `main_test.go:427-502` |
| harvestpy plan | JAIL | available unblocked plan is clean; unavailable or blocked exact lock is a warning and rc 1 | `doctor_harvest_test.go` |
| harvestpy deliberate skip | JAIL | deliberate skip is reported as `skipped` and is not a warning | `doctor_harvest_test.go` |
| harvestpy interpreter and marker | JAIL | healthy interpreter and readable marker are clean; failed execution or marker inspection is a warning and rc 1 | `doctor_harvest_test.go` |
| harvestpy lock and inventory | JAIL | complete lock and inventory are clean; incomplete or unreadable state is a warning and rc 1 | `doctor_harvest_test.go` |
| harvestpy live smoke and conversion | JAIL | both healthy are clean; either failure is a warning and rc 1 | `doctor_harvest_test.go` |
| harvester cache | JAIL | missing cache root is clean; walk failure is a warning and rc 1; the root is `cache.dir` or `<home>/.professor/.cache`, never the working directory or a retired env variable | `doctor.go`, `internal/harvest/legacy_core_parity_test.go` (`TestCacheRootIsConfiguredDirOrTheOneDefault`), `internal/harvestmcp/service_test.go` (`TestServiceCacheIsTheOneRootNotTheWorkingDirectory`) |
| harvester config file | UNIT | `harvester.config.json` loads every key with file provenance; invalid/unsafe settings (external without auth or `publicURL`, bad URL, negative TTL, world-readable secret, port collision, unknown key) refuse at load | `internal/config/harvester_test.go` |
| pre-split config migration | UNIT+JAIL | `config.json` is read until migrated; `pfm install` previews then renames to `pfm.config.json`, moves `mcp.servers.harvester` into `harvester.config.json`, and moves the init-written port 8377 → 18377 BEFORE wiring clients; a second plan is empty | `internal/config/harvester_test.go` (`TestLoadFallsBackToPreSplitFileUntilMigrated`), `internal/config/migration_test.go`, `TestInstallMigratesPreSplitConfigBeforeWiring` |
| interrupted / colliding migration | UNIT+JAIL | a leftover `config.json` beside `pfm.config.json` (a migration interrupted between its two writes) is planned and parked, never read as done; a port move onto the external gateway's port is kept at 8377 with its reason; `config init` refuses before writing either file | `TestInterruptedMigrationLeftoverIsParkedNotIgnored`, `TestMigrationKeepsPortWhenExternalGatewayHoldsTheTarget`, `TestConfigInitRefusesBeforeWritingEitherFile` |
| external gateway doctor line | UNIT | a configured external gateway always renders its live state or why it cannot run (harvester disabled, daemon too old, bind failed); only `listening` is healthy | `TestDoctorExternalGatewayNeverRendersAsAbsence` |
| retired harvester env / pre-split layout | JAIL | each retired variable still exported, and a pre-split layout, is a doctor warning naming the replacement key | `TestDoctorWarnsOnRetiredHarvesterEnvAndPreSplitLayout` |
| trusted SearXNG origin | UNIT | a loopback SearXNG is reachable; a redirect off the configured origin is refused before contacting the target; fetch of that origin keeps the SSRF guard; disabled search contacts no backend; a total failure carries each backend's error | `internal/harvest/search_trusted_test.go`, `TestSearchFailureRendersEachBackend` |
| external harvester gateway | JAIL | the daemon's second port serves the harvester only, behind the bearer/OAuth wall (no/wrong token = 401; `/mcp/chat` = 404); an unbindable port is a reported failure on `/status`, never a claimed listener; a credential-free gateway refuses to exist | `cmd/pfm/harvester_gateway_test.go`, `TestRemoteRefusesToExistWithoutCredentials` |
| local-read confinement fails closed | UNIT | a non-empty root list none of which resolves refuses every read | `internal/harvest/local_confinement_test.go` |
| spawn-audit classifier: injected, predates-layer, and violation verdicts | UNIT | `--system-prompt-file` or the lean env arm classifies INJECTED only when argv ALSO carries `--settings {"outputStyle":"default"}` — carrying the prompt without it is a VIOLATION only when the process started AT/AFTER the staged prompt layer's mtime; a seat born BEFORE the stamp is PREDATES-LAYER instead (a reload fixes it, not a bug hunt), while an unusable age signal (no birth time, no stamp) or a correctly-flagged seat is never used to excuse or downgrade the verdict; an unreadable environment never clears a flagless seat | `cmd/pfm/spawn_audit_doctor_test.go` (`TestClassifySpawnSeparatesInjectedOldAndBypassed`) |

### A.3 — `pfm update` doctor gate (issue #24 finding 1)

`pfm update` runs a baseline doctor on the CURRENT binary before touching anything, then gates on the CANDIDATE's doctor exit code alone — never on a raw non-zero exit — so pre-existing warnings never roll an update back.

| behavior | safety | expected | regression |
| --- | --- | --- | --- |
| baseline doctor, run before any owned binary is replaced | JAIL | records `(warnings, failures)`; a baseline that cannot run, or exits 2 (usage), never blocks — printed and treated as "no baseline" | `cmd/pfm/update_command_test.go` (`stubUpdateBaselineDoctor`, every `TestUpdate*` test that drives a real `runUpdate()`) |
| candidate doctor exit 0 or 1 (clean or warnings-only) | JAIL | proceeds; prints `doctor after update: warnings=N (before update: M)`; rollback seams are NOT called | `TestUpdateProceedsWhenTheCandidateDoctorHasOnlyStandingWarnings` |
| candidate doctor exit 3 (failures) | JAIL | rolls back; message names the failure count | `TestUpdateRollsBackWhenTheCandidateDoctorReportsAFailure` |
| candidate doctor exit 2 or a spawn error | JAIL | rolls back — a doctor that cannot run is not a verdict | `update_command.go` (`runUpdateDoctor`/`updateRepository` gate switch) |
| warning rows the candidate introduced beyond the baseline | JAIL | every candidate row with no normalised (decimal/hex-masked) twin in the baseline output is listed under `new warning rows — read them before the next update:` | `TestUpdateNamesNewWarningRowsIntroducedByTheCandidate` |
| rollback doctor exit 1 (warnings only) | JAIL | reported, never claimed as residue | `TestUpdateRollbackDoctorWarningsAreNotResidue` |
| rollback doctor exit 1 from a binary predating M2 (no `doctor: failures=` and no `doctor: clean` in its output) | JAIL | named as an older pfm, never claimed as residue | `TestUpdateRollbackDoctorFromAnOlderBinaryIsNamedNotClaimedAsResidue` |

### A.4 — `pfm update` config path across the v0.74.0 migration (issue #24 findings 3/4)

The candidate's own `install --yes` renames `config.json` → `pfm.config.json` inside the candidate process only; the updater's `runtime.Config.Path` (resolved before the install ran) is re-resolved for the post-install doctor, and the config files the migration renamed are snapshotted and restored before a rollback reruns the previous installer.

| behavior | safety | expected | regression |
| --- | --- | --- | --- |
| post-install candidate doctor reads the migrated config path, not the stale pre-install one | JAIL | `--config` names `pfm.config.json`; stdout carries `config migrated by the update: <old> → <new>` | `cmd/pfm/update_command_files_test.go` (`TestUpdateCandidateDoctorReceivesTheMigratedConfigPath`) |
| rollback restores the config files the migration renamed before rerunning the previous installer | JAIL | `config.json` holds its pre-update bytes, `pfm.config.json` is gone, stderr reports `restored <config.json> to its pre-update state` | `TestUpdateRollbackRestoresTheConfigFilesTheMigrationRenamed` |
| rollback's `install --yes` (old binary) sees an existing config path | JAIL | `runtime.Config.Path` exists on disk by the time the rollback install runs | `TestUpdateRollbackInstallSeesAnExistingConfigPath` |
| `pfm install --yes --config <path>` refuses when the named path does not exist; preview names the skip | JAIL | apply exits 1 with the refusal text; preview exits 0 with a `skip` line and continues | `cmd/pfm/install_command_test.go` (`TestInstallApplyRefusesAnExplicitConfigThatDoesNotExist`) |
| `LoadRuntime` records whether `--config` was explicit | JAIL | `LoadRuntime("")` → false; `LoadRuntime(<path>)` → true | `internal/config/runtime_test.go` (`TestLoadRuntimeRecordsWhetherConfigWasExplicit`) |
| MCP launch-agent/unit removal names the config it read the disabled state from | JAIL | change line carries `(no MCP server is enabled in <MCPConfigPath>)` | `internal/installer/launchd_test.go` (`TestMCPLaunchAgentRemovalNamesTheConfigItReadEnabledFrom`) |
| a rollback that crosses the v0.74.0 migration boundary on a real, previously-installed pre-split host (`config.json` only, MCP genuinely enabled), with the plist and daemon confirmed restored end to end | REAL-SESSION | fenced rehearsal in an `iso shell`: install v0.73.x from source, `pfm update --to <tag>` with a candidate whose doctor is forced to fail, assert the MCP plist and `config.json` are back | not automated — see § Flows that CANNOT be jailed |

### A.5 — Orphaned hooks a rollback strands (issue #24 finding 2)

`v0.77.0` fixed hook-file snapshot/restore on rollback and unknown `pfm internal <name>` exiting 1 instead of 2, but only in the binary that ships them — a hook of pfm's own shape naming a subcommand THIS binary does not implement (a rollback to an older release, or a rollback whose residue guard leaves a newer-then-reverted file untouched) was invisible to install and doctor alike. `unknownPFMHookCommand` (`internal/installer/settings.go`) is the rule that names it.

| behavior | safety | expected | regression |
| --- | --- | --- | --- |
| `pfm install --yes` strips an unknown pfm-shaped hook on apply, leaving every real template hook wired | JAIL | the entry is gone, the nine template hooks remain, `unknownPFMHookCommand` names it `hook-from-a-newer-pfm` | `internal/installer/settings_wiring_test.go` (`TestInstallRetiresAPFMHookThisBinaryDoesNotImplement`) |
| a foreign hook whose command merely mentions "pfm" in its arguments is never matched | JAIL | the hook survives untouched (boundary pin — holds before and after the fix) | `internal/installer/settings_wiring_test.go` (`TestInstallLeavesAForeignHookThatMerelyMentionsPFM`) |
| `pfm doctor` reports an unknown pfm-shaped hook as `stale`, not silence | JAIL | `ProbeExpectedHooks` returns a `stale` result named `unknown:hook-from-a-newer-pfm` with an error naming the subcommand this pfm does not implement | `internal/installer/expected_hooks_test.go` (`TestProbeExpectedHooksReportsAnUnknownPFMHookAsStale`) |
| a rollback residue left by a concurrent settings edit names the stranded unknown hook commands it carries | JAIL | stderr contains `it still carries` and the stranded subcommand name | `cmd/pfm/update_command_test.go` (`TestUpdateRollbackResidueNamesTheStrandedHookCommands`) |
| a rolled-back update on a real ≤0.76.0 host, confirmed stranding both hooks in `settings.json` and blocking every new-chat prompt until hand-removed | REAL-SESSION | fenced rehearsal: install ≤0.76.0 from source, force a `pfm update` rollback, confirm the two entries survive on the OLD binary (nothing in this release can retroactively fix that binary) and that the release note's removal steps clear them | not automated — see § Flows that CANNOT be jailed |

### A.6 — Claude registry config-dir fanout (issue #24 finding 5)

`installer.ClaudeUserRegistry` (single-path resolver) assumed the implicit account's registry is always `$HOME/.claude.json`; a `claude` launched from a shell exporting `CLAUDE_CONFIG_DIR` (the launcher shim passes it straight through) reads `<CLAUDE_CONFIG_DIR>/.claude.json` instead, a genuinely different file the installer never registered `chat`/`harvester` into and doctor never inspected. `installer.ClaudeUserRegistries` replaces it: one resolver, naming every registry (per account, plus the ambient `CLAUDE_CONFIG_DIR`) and why, read by both the writer and doctor through `config.AmbientClaudeConfigDir()`.

| behavior | safety | expected | regression |
| --- | --- | --- | --- |
| the registry roster includes the ambient `CLAUDE_CONFIG_DIR` the launcher shim passes through, distinct from the implicit account's `$HOME/.claude.json`, each with its own reason | JAIL | two `ClaudeRegistry` entries, `~/.claude.json` (`account 1 (pfm spawns it without CLAUDE_CONFIG_DIR)`) and `<ambient>/.claude.json` (`ambient CLAUDE_CONFIG_DIR=<dir> (the claude launcher passes it through — launch_command.go)`) | `internal/installer/mcp_accounts_test.go` (`TestClaudeUserRegistriesIncludeTheAmbientConfigDirTheLauncherPassesThrough`) |
| `pfm install` registers `chat`+`harvester` into EVERY registry `ClaudeUserRegistries` resolves under an ambient `CLAUDE_CONFIG_DIR`, and the ownership ledger owns each one | JAIL | both `~/.claude.json` and `<ambient>/.claude.json` carry `chat` and `harvester`; the ledger's `Registrations` owns both physical paths | `internal/installer/mcp_wiring_test.go` (`TestInstallRegistersMCPServersInEveryRegistryAPFMLaunchedClaudeReads`) |
| `InspectHarvesterClientCutover` refuses a nil registry list instead of silently defaulting to `$HOME/.claude.json` | JAIL | exactly one `MCPClientUnreadable` report with error `no Claude registries supplied`; an explicitly empty (non-nil) list is still valid ("no Claude registries to inspect") | `internal/installer/mcp_inspect_test.go` (`TestInspectHarvesterClientCutoverRefusesANilRegistryList`) |
| `pfm doctor`'s `mcp client=claude` row names every registry `ClaudeUserRegistries` resolves, with its reason and both the `harvester` and `chat` states, and warns with a remediation on a registry a sibling registry's `pfm` registration shows was never reached | JAIL | two rows: the healthy implicit-account registry (`harvester=pfm chat=pfm`, no warning) and the absent ambient registry (`harvester=absent chat=absent remediation=run pfm install --yes (registers every registry a pfm-launched Claude reads)`) | `cmd/pfm/doctor_mcp_client_test.go` (`TestDoctorMCPClientRowNamesEachRegistryAndItsReason`) |
| on a real host whose shell exports `CLAUDE_CONFIG_DIR`, `pfm install --yes` then `pfm doctor \| grep 'mcp client='` shows every registry with a reason, and `claude mcp list` inside a shim-launched chat lists `chat` and `harvester` | LIVE-READ | confirms the jailed rows above against a genuine `CLAUDE_CONFIG_DIR`-exporting shell and a real `claude` session | not automated — see § Flows that CANNOT be jailed, item 35 |

### A.7 — `harness-prompt` capture on an OAuth/subscription host (issue #24 finding 6)

The doctor's `harness-prompt` check re-captures the Claude CLI's built-in system prompt through a local sink so it can compare its sha256 to a reviewed baseline without spending tokens. On an OAuth-logged-in default config dir the sink was never reached (`TOTAL_HITS 0`, a real priced result) and the run silently reported `CHECK FAILED to run (… timed out)` for the full 20s bound. `captureHarnessPrompt` now launches under a throwaway `CLAUDE_CONFIG_DIR` (created per capture, removed after) so the dummy `ANTHROPIC_API_KEY` is the only credential the CLI can find, keeps the run's own stdout/stderr under `--verbose`, and names the un-runnable state distinctly — `CANNOT CAPTURE` — when a CLI still answers from the real endpoint with zero sink hits.

| behavior | safety | expected | regression |
| --- | --- | --- | --- |
| a CLI that answers with a real, priced envelope (`stop_reason` + `usage`) and never reaches the sink | JAIL | capture error wraps `errHarnessBypassedSink`; the doctor row reads `CANNOT CAPTURE`, never `CHECK FAILED` or `DRIFT` | `cmd/pfm/harness_prompt_capture_test.go` (`TestHarnessCaptureReportsBypassWhenTheCLIAnswersWithoutHittingTheSink`) |
| the capture run's `CLAUDE_CONFIG_DIR` | JAIL | a fresh temp directory distinct from the caller's real `$HOME/.claude`, created per capture and removed once the capture returns | `cmd/pfm/harness_prompt_capture_test.go` (`TestHarnessCaptureRunsTheCLIInAThrowawayConfigDir`) |
| `pfm doctor --verbose` and a harness-prompt capture that never reaches the sink | JAIL | `tmp/pfm-doctor/harness-prompt.stdout`, `.stderr`, and `sink-hits.txt` are written; the CHECK FAILED line points at the stderr file | `cmd/pfm/harness_prompt_capture_test.go` (`TestDoctorVerboseKeepsTheHarnessRunOutput`) |
| the capture run's stdin | JAIL | pinned to `/dev/null` — the CLI's prompt travels as a positional `-p x` argument, never on a stream the CLI might wait on | `cmd/pfm/harness_prompt_capture_test.go` (all three tests exercise a fake CLI that never blocks on stdin) |
| a real, OAuth/subscription-logged-in host: the throwaway config dir actually routes the CLI to pfm's sink instead of the real endpoint | REAL-SESSION | `pfm doctor --verbose \| grep harness-prompt` reads `matches baseline` (or a named `DRIFT`), never `CHECK FAILED to run (… timed out …)`; a CLI regression that still bypasses the sink reads `CANNOT CAPTURE … OAuth-only routing` instead | not automated — see § Flows that CANNOT be jailed, item 36 |

### A.8 — `claude-versions`: retention for the versions the launcher's own updater can no longer clean up (issue #24 finding 8)

pfm's `claude` launcher (`assets/bin/claude`) never symlinks the canonical binary straight into `~/.local/share/claude/versions/`, so Claude Code's own updater reports version cleanup disabled and every build it ever installs accumulates. `internal/installer/claude_versions.go` splits the work in two: `InspectClaudeVersions` (enumeration, ordering, and the structural protections — newest, unparsed name, configured `claude.binary`, displaced native target) never touches a process table at all, because `pfm internal claude-version` calls it on EVERY `claude` launch and a whole-host process probe there is pure waste; `ProbeLiveClaudeVersions` is the separate, deliberately optional live-process half only `pfm doctor` and pruneClaudeVersions call. That probe scopes candidates the same way `internal/stale.Find` does (stale.go:53-89): a pid is worth `Image`'s cost only when its argv[0] names the Claude binary, lives inside the versions directory, or its own basename parses as a version — anything else, and any Cmdline read that fails, is skipped before `Image` is ever called, and a candidate's `Image` failure is refused (named "probe failed") UNLESS the pid has already exited (`kill(pid,0) -> ESRCH`), which is skipped instead. `PlanClaudeVersionPrune` decides what a prune may remove: everything outside the keep window and not otherwise protected, or nothing at all when the live probe could not vouch for every candidate.

| behavior | safety | expected | regression |
| --- | --- | --- | --- |
| the launcher shim selects the HIGHEST parsed version, not the freshest file — a touched older build must not win | JAIL+sh | the shim execs `pfm internal claude-version`'s answer; a fixture with the higher version's file at an OLDER mtime still wins | `internal/installer/launcher_test.go` (`TestRenderedClaudeLauncherChoosesTheHighestVersionNotTheFreshestFile`, REPLACES the old freshness-pinning `TestRenderedClaudeLauncherChoosesNewestVersionByFreshness`) |
| `pfm internal claude-version` — the launcher shim's hot path, run on every launch — never constructs or reads a process table at all | JAIL (structural) | `claude_version_command.go`'s own source names no `gather.*` symbol | `cmd/pfm/claude_version_command_test.go` (`TestInternalClaudeVersionCommandNeverConstructsAProcessTable`) |
| `InspectClaudeVersions` alone marks the newest, the configured `claude.binary`, and an unparsed name, but never populates `Live`/`LiveProbeErr`; `ProbeLiveClaudeVersions` is the one that marks a live build (by pid image identity) with its own protected reason | JAIL | `Newest` is the highest parsed version; `Protected["<configured path>"] == "configured claude.binary"`; `Protected["<unparsed path>"] == "unparsed name"`; after `ProbeLiveClaudeVersions`, `Protected["<live path>"] == "live (pids <n>)"` | `internal/installer/claude_versions_test.go` (`TestInspectClaudeVersionsMarksLiveNewestConfiguredAndUnparsed`) |
| a process table with no image identity at all (the optional `gather.ProcImage` extension missing) refuses to prune ANYTHING and names every version "probe failed" rather than guess one is unused | JAIL | `PlanClaudeVersionPrune` returns an empty remove list; every kept reason is exactly `"probe failed"` | `internal/installer/claude_versions_test.go` (`TestPlanClaudeVersionPruneRefusesEverythingWhenTheImageProbeFails`) |
| a non-candidate pid's `Image` error (a stranger's process pfm has no business probing) never poisons the scan — live detection still works for the actual Claude pid | JAIL | `ProbeLiveClaudeVersions` with one candidate and one non-candidate whose `Image` errors: `LiveProbeErr` stays nil, the candidate is still marked live | `internal/installer/claude_versions_test.go` (`TestProbeLiveClaudeVersionsSkipsNonCandidatePidsEvenWhenTheirImageErrors`) |
| a CANDIDATE pid's `Image` error, with the pid confirmed still alive, still refuses the whole prune, naming it | JAIL | `LiveProbeErr` set; `PlanClaudeVersionPrune` removes nothing and names the candidate `"probe failed"` | `internal/installer/claude_versions_test.go` (`TestProbeLiveClaudeVersionsRefusesWhenACandidatePidsImageErrorsAndItIsStillAlive`) |
| a candidate pid's `Image` error is SKIPPED, not refused, when the pid already exited mid-scan (`kill(pid,0) -> ESRCH`) — the same carve-out `internal/stale.Find` uses | JAIL | `LiveProbeErr` stays nil; the exited pid is never marked live | `internal/installer/claude_versions_test.go` (`TestProbeLiveClaudeVersionsSkipsACandidateWhoseImageFailsBecauseThePidIsGone`) |
| identifying the configured `claude.binary` (or a displaced native target) refuses the whole inspection on any error OTHER than not-exist — a permission or structural failure to identify a path is never silently read as "nothing configured there" | JAIL | a configured path whose parent is not a directory (`ENOTDIR`, not `ENOENT`) makes `InspectClaudeVersions` return an error | `internal/installer/claude_versions_test.go` (`TestInspectClaudeVersionsRefusesWhenIdentifyingTheConfiguredBinaryFailsForAReasonOtherThanAbsence`) |
| the prune plan keeps the newest two parsed versions and removes the rest, once the probe succeeds | JAIL | with 4 versions, the newest 2 are kept `"newest"` and the older 2 are removed | `internal/installer/claude_versions_test.go` (`TestPlanClaudeVersionPruneKeepsNewestTwoAndRemovesTheRest`) |
| `pfm install` preview lists every prunable version as a `change remove <path> (<version>, <bytes>)` line and names every protected version as `keep <path> (<reason>)`, without touching a file; apply removes only the unprotected set and keeps a live version by name | JAIL | preview leaves all 4 fixture files in place; apply removes only the one outside both the keep window and the live protection | `internal/installer/installer_test.go` (`TestInstallPreviewListsPrunableVersionsAndApplyRemovesOnlyThem`) |
| `pfm doctor` reports `claude-versions dir=… count=N bytes=… newest=… live=…(n pids) prunable=N (…)`, warns only when something is prunable or the probe failed, and never warns when the native installer simply never ran (dir absent) | JAIL | a 3-version fixture with one live (1 pid) and one prunable reports `count=3` and `prunable=1`; `PFM_PROC_ROOT` fixture supplies the live pid's `exe` AND `cmdline` (candidate scoping reads argv[0] before `Image`) | `cmd/pfm/doctor_jail_test.go` (`TestDoctorReportsClaudeVersionCountBytesAndPrunable`) |
| `pfm internal claude-version` prints the newest parsed version's absolute path and exits 0, or prints nothing and exits 127 when there is none to choose | JAIL | a fixture with the higher version at the older mtime still prints the higher version's path; an empty `versions/` directory exits 127 with empty stdout | `cmd/pfm/claude_version_command_test.go` (`TestInternalClaudeVersionPrintsNewestOrExits127`) |
| a real host with several live Claude chats running an OLDER `versions/` build than the newest installed: `pfm install` (preview) names every removal and every live build it protects, by pid, WITHOUT a standing PROBE FAILED warning from unrelated processes on the box | REAL-SESSION | `pfm doctor \| grep claude-versions` and `pfm install` (no `--yes`) both read against the live fleet | not automated — see § Flows that CANNOT be jailed, item 37 |

### A.9 — VS Code extension registration and doctor rows (issue #24 findings 9a/9b/9c)

A hand-linked `extensions/professor` directory is never loaded on its own: modern VS Code reads its user extension list from that product's own `extensions/extensions.json`, and walks the directory only when the file is absent or a folder appears while the window is already running. `pfm install --vscode` now writes (or updates) that index entry alongside the link, `pfm doctor` gets one row per product naming both the link and the index state, and the canonical `PFM` terminal profile carries an icon/colour. See the updated § K rows for the full JAIL matrix.

| flow | safety | expected | regression |
| --- | --- | --- | --- |
| on this macOS host, with VS Code Insiders present: `pfm install --yes --vscode`, open Insiders cold, `code-insiders --list-extensions \| grep professor` lists it, and `pfm doctor \| grep 'vscode product'` reports `index=registered` for every linked product | REAL-SESSION | manual: install, cold-launch Insiders, grep its extension list and `pfm doctor` | not automated — see § Flows that CANNOT be jailed, item 38 |

### A.10 — VS Code terminal surfaces (issue #24 findings 10/11/12)

`professor.newChatTerminal` now delegates to the SAME contributed-profile route (`workbench.action.terminal.newWithProfile` addressed at `professor.terminal`) the terminal `+` dropdown's **Professor** entry uses, instead of calling `createTerminal` with its own options, which rendered the default profile's icon (finding 10); the extension contributes a default keybinding for the command (finding 11b), collapsing the third redundant surface (finding 11a).

| flow | safety | expected | regression |
| --- | --- | --- | --- |
| `professor.newChatTerminal`'s registered command body never calls `createTerminal(` with its own options and delegates through `workbench.action.terminal.newWithProfile` addressed at `id: 'professor.terminal'` | JAIL | `internal/installer/vscode_extension_test.go:TestVSCodeExtensionCommandNeverCallsCreateTerminalWithItsOwnOptions` | |
| the staged `package.json` contributes exactly one keybinding for `professor.newChatTerminal` — `ctrl+shift+alt+t` / `cmd+shift+alt+t` | JAIL | `internal/installer/vscode_extension_test.go:TestVSCodeExtensionContributesOneKeybindingForTheCommand` | |
| the operator matrix — keybinding press, command palette, terminal `+` dropdown pick — each produces a tab whose icon is NOT the default profile's `mortar-board`, with a window reload between rounds | REAL-SESSION | manual: run all three, reload between rounds, confirm each tab's icon cycles | not automated — see § Flows that CANNOT be jailed, item 39 |

| `reap` dry run classifies every socket, changes nothing | JAIL+tmux | `cmd/pfm/reap_jail_test.go:134-189`, `internal/reap/reap.go:139-160` | |
| `reap` KEEP rules: attached, self, `cc-new-*`, busy, transcript written < 60s | JAIL | `internal/reap/reap_test.go:14-200` | |
| `reap` never kills a socket hosting non-chat processes (dev servers, `uv`) | JAIL+tmux | `internal/reap/proc.go:78-110`, `cmd/pfm/reap_jail_test.go:134-189` | |
| `reap` exempts a chat's OWN subtree (its MCP servers, its tool shells) | JAIL | `internal/reap/proc_test.go:86-97` | |
| `reap` fails closed when the busy query fails or a `cc-*` crumb is missing | JAIL | `internal/reap/reap_test.go:118-146` | |
| `reap` empty socket younger than 1h left alone (a server may be starting) | JAIL+tmux | `cmd/pfm/reap_jail_test.go:220-260` | |
| `reap` never reaps from AGE alone; a wedged socket times out into SKIP | JAIL | `internal/reap/reap_test.go:203-224`, `internal/reap/runner.go:33-38` | |
| `reap --apply` re-verifies attachment at kill time and clears crumbs | JAIL+tmux | `internal/reap/runner.go:320-360` | |
| `reap --apply` re-probes a planned corpse and skips removal if the socket becomes live or unreadable | JAIL | `internal/reap/reap_test.go:43-89`, `internal/reap/runner.go` | |
| `reap` socket selection delegates to the canonical gather classifier: cx included, vsct excluded, probe-* only with the jail opt-in | JAIL | `internal/reap/reap_test.go:91-110`, `internal/reap/runner.go:307-312` | |
| `reap` sweeps idle `vsct` bunkers by SESSION, never by killing the server | JAIL | `internal/reap/reap_test.go:258-296` | |
| `archive` dry run default; `--apply`, `--subagents`, `--older-than`, `--restore` | JAIL | `internal/archive/archive_test.go:85-204` | |
| `archive` re-checks liveness at run time (argv, codex fds, sid crumbs) | JAIL | `internal/archive/live.go:20-95`, `archive_test.go:296-340` | |
| `archive` retires every decided kill, live ones included | JAIL | `internal/archive/archive.go:196-240` | |
| `archive` prunes history.jsonl and the codex index for MOVED chats only | JAIL | `internal/archive/archive_test.go:180-200` | |
| `archive --restore` puts a chat back and refuses an unknown id | JAIL | `internal/archive/archive_test.go:228-262` | |
| `heal` report: CAUGHT_UP / CONSISTENT / WEDGED / MIDLINE / NO_ROLLOUT totals | JAIL | `internal/heal/heal_test.go:170-240`, `cmd/pfm/heal_jail_test.go:108-150` | |
| `heal` report is read-only; `--apply` heals only broken threads, backup first | JAIL | `internal/heal/heal_test.go:243-320` | |
| `heal` skips a thread whose writer lock is held, and heals it once released | JAIL | `internal/heal/heal_test.go:322-380` | |
| `heal --thread` is a silent exit-0 no-op on a healthy thread | JAIL | `cmd/pfm/heal_jail_test.go:152-176` | |
| ResumeCodex runs the native projection repair before the seat is created | JAIL | `internal/action/executor.go:113-127`, `testdata/golden/cmdlines.txt:47-54` | |
| `name-sync` converges both engines' window names; `--dry-run` changes nothing | JAIL+tmux | `internal/gather/labels_jail_test.go:14-90` | |
| `name-sync` reads every renamed window's name BACK and counts only a verified match as converged; each unverified window is printed with the value read (`window S W: wanted "A", reads "B" after rename`) and the command exits 1; `--dry-run` reports `windows planned: N` because it applied nothing | JAIL+tmux | `cmd/pfm/namesync_command.go` (`verifyRenames`), `cmd/pfm/namesync_verify_jail_test.go` | issue #14 F13 |
| `name-sync` converges every live server onto `config.ChatServerOptions` through one reader/applier (`gather.CommandTmux.ConvergeGlobalOptions`): a server still auto-renaming its window (a picker chat born before the one creator) stops on the next pass under either title policy, each transition is named, and an unreadable server counts unverified, never "nothing to converge" | JAIL+tmux | `cmd/pfm/namesync_command.go` (`convergeChatServerOptions`), `cmd/pfm/namesync_titles_regression_test.go`, `internal/gather/tmuxprobe_test.go` | |
| every picker route that opens a fresh server (N, P, O, A, R, X) plans a `ChatServer` the executor creates through the one creator, born with its engine's short window name; the eval line is only the attach, never a `new-session` | JAIL | `internal/action/synth.go` (`onChatServer`), `internal/action/synth_test.go`, `testdata/golden/cmdlines.txt` | |
| `pfm chat open` opens a row on its OWN engine's primary account (`config.PrimaryAccountFor`), never the Claude primary on a Codex/OpenCode row | JAIL + LIVE door re-run | `internal/config/accounts.go`, `internal/config/accounts_test.go` (`TestPrimaryAccountForPicksTheRowsOwnEngine`), `cmd/pfm/commands.go` | named gap: no jail fixture for a two-Claude/one-Codex roster `chat open`; proven at the live door |
| `pfm internal stale` (`make stale`) names every pfm process whose executable is not the installed binary by device+inode — `/proc/<pid>/exe` on Linux, lsof's program entry on macOS (`gather.ProcImage`) — distinguishes none / stale / could-not-read (non-zero), and refuses a table that cannot read images | JAIL + LIVE-READ (macOS, cross-checked against lsof) | `internal/stale/stale.go`, `internal/stale/stale_test.go`, `internal/gather/procfs.go` (`Image`, `FileIDOf`), `internal/gather/procfs_darwin.go` | |
| `pfm internal stale --sweep` (`make sweep-stale`, run by `make install`) TERMs every stale process, KILLs one that ignored TERM, re-scans and fails naming a survivor; never signals the installed image, and never hands kill(2) a pid ≤ 0 | JAIL + LIVE (host `make install`) | `internal/stale/stale.go` (`Sweep`, `deliver`), `internal/stale/stale_test.go` | |
| `make mcp-restart` restarts the macOS launch agent with `launchctl kickstart -k` and requires a NEW pid within 5s, else MCP-RESTART-FAILED | LIVE (host `make install`) | `pfm/Makefile` (`mcp-restart`) | named gap: Makefile targets have no jail; proven on the host |
| the window-name second writer: `automatic-rename` is ruled out (`rename-window` disables it window-scoped), an OSC `\e]2;…\a` write only moves `pane_title`, and `\ek…\e\\` with `allow-rename on` DOES take the name back — so `RenameWindow` latches `allow-rename off` window-scoped and the name survives the next write | JAIL+tmux | `internal/gather/tmuxprobe.go` (`RenameWindow`), `internal/gather/rename_second_writer_jail_test.go` | issue #14 F13 |
| a claude `/rename` converges the window name from the chat's OWN statusline render (same `gather.WindowNameFor` + `RenameWindow` name-sync uses), forks nothing once converged, leaves a two-pane `/chat:branch` window alone, and never touches tmux outside a fleet socket; the timer stays the backstop | JAIL+tmux | `internal/statusline/render.go` (`convergeWindowName`), `internal/statusline/window_converge_jail_test.go` | issue #14 F11 |
| picker/name-sync live scans seed empty Codex pane bindings but never overwrite a hook-supplied post-clear id | JAIL | `cmd/pfm/clear_kill_jail_test.go`, `cmd/pfm/pipeline.go` | |
| `internal clear-kill` owns Claude `SessionEnd(reason=clear)` and Codex `SessionStart(source=clear)`, ignores unrelated lifecycle events, and fails open | JAIL | `cmd/pfm/clear_kill_jail_test.go` | |
| clear-kill refreshes the indexed Claude transcript or Codex lineage before recording its prompt baseline | JAIL | `cmd/pfm/clear_kill_jail_test.go`, `internal/store/killed_test.go` | |
| a Codex pane's display NAME may SEED an unbound pane but NEVER moves a bound one — the lagging index made a name walk a binding backwards onto the cleared thread and clear-kill the live one | JAIL+tmux | `cmd/pfm/codexpanes.go`, `cmd/pfm/codexpanes_test.go`, `cmd/pfm/reconcile_codex_panes_jail_test.go` | watched RED: binding moved to the cleared thread with empty stderr |
| one thread is never bound to two panes — incumbent keeps it, the newcomer is refused out loud | JAIL+tmux | `cmd/pfm/codexpanes_test.go`, `cmd/pfm/reconcile_codex_panes_jail_test.go` | watched RED: 2 panes bound to 1 thread, silently |
| a same-lineage resume/fork in one pane is NOT a clear and never retires the chat's own lineage root | JAIL+tmux | `cmd/pfm/codexpanes.go`, `cmd/pfm/reconcile_codex_panes_jail_test.go` | watched RED: the resume retired the root |
| an unreadable lineage advances the binding but refuses the kill — a failed look is never a clear | JAIL | `cmd/pfm/codexpanes_test.go` | |
| a binding on a thread a /clear already RETIRED is impossible and is dropped, never re-seeded, so the pane can be re-seated from its own screen | JAIL+tmux | `cmd/pfm/codexpanes.go`, `cmd/pfm/codexpanes_test.go`, `cmd/pfm/reconcile_codex_panes_jail_test.go` | the state a real fleet was stuck in; a name can never move it back |
| an UNREADABLE kill table never drops a binding, and an explicit `pfm chat kill` (no prompt baseline) never does either | JAIL | `cmd/pfm/codexpanes_test.go`, `cmd/pfm/pipeline.go` | |
| `pfm doctor` names contested and clear-retired pane bindings, and an unreadable binding table is not an empty one | JAIL | `cmd/pfm/doctor.go`, `cmd/pfm/doctor_codex_pane_test.go` | |
| `pfm doctor` counts a binding whose PANE is gone as stale, never contested — a real host held 76 bindings for 19 live panes, and counting the dead ones reported 8 false emergencies | JAIL+tmux | `cmd/pfm/doctor.go`, `cmd/pfm/doctor_codex_pane_test.go` | found by running the new check on the live fleet |
| `pfm doctor` names a live Codex pane pfm cannot follow through a clear; the reconcile pass stays quiet there so the picker is not spammed | JAIL+tmux | `cmd/pfm/doctor.go`, `cmd/pfm/doctor_codex_pane_test.go`, `cmd/pfm/gather_warnings_jail_test.go` | |
| `pfm chat inject` REFUSES an unsigned send instead of delivering an UNSIGNED-stamped message; `--allow-unsigned` is the only way through, and the auto-file pointer is checked separately | JAIL | `internal/inject/body.go`, `internal/inject/signature_test.go` | replaces the old deliver-and-warn contract |
| `pfm chat reload` takes `--account N`; a bare prose word is refused with the flag it should have been | JAIL | `cmd/pfm/reload_command.go`, `cmd/pfm/reload_args_test.go` | |
| the picker groups a LONE `{GROUP}:{NAME}` chat, and a prose colon (`fix: the bug`) still declares no group | JAIL | `internal/ui/model.go`, `internal/ui/name_group_test.go`, `internal/ui/model_test.go` | |

## B — Picker TUI: key bindings and model state

| flow | safety | expected behavior (source) | regression |
| -------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------- | --------------------------------------------------------------------------------- | ---------- |
| chat-row carousel is `open → reboot → 1h → kill`; `⌃T` is unbound | JAIL | `ui/model_test.go`, `ui/carousel_boxes_test.go` | |
| Project groups keep their natural order; retired `⌃R` is a no-op | JAIL | `ui/model_test.go` | |
| `⌃X` persists the kill immediately; a live row is ended and demoted | JAIL+tmux | `ui/model.go`, `commands.go` | B3 |
| `⌃X` on a booting, split, label-killed, or ID-less row → visible refusal | JAIL | `ui/model.go`, `ui/model_test.go`, `ui/label_kill_test.go` | B3 |
| `⌃X` twice performs a kill then unkill; the outcome receipt carries the final state | JAIL | `ui/model_test.go` | |
| `Esc` after `⌃X` keeps the applied kill receipt but abandons a pending account change | JAIL | `ui/model_test.go`, `commands.go` | **B3** |
| `⌃X` on an agent row whose transcript is not indexed → **must not silently fail** | JAIL | `compose/compose.go:564-567` + `kill/manager.go:142-164` | **B3** |
| `⌃E` flip 1h cache for the next launch | JAIL | `ui/model.go:157-159` | |
| `⌃S` cycles primary account through the configured account-id roster | JAIL | `ui/model.go`, `pipeline_async_test.go` | |
| `⌃S` change persisted through the shared store on exit | JAIL | `commands.go:133-138`, `pipeline.go:578-631` | |
| `⌃O` reboot a live row → kill-server, drop crumbs, demote to resume kind | JAIL+tmux | `ui/model.go:166-172`, `commands.go:317-349` | |
| `⌃O` on a non-live row → no-op | JAIL | `ui/model.go:167-171` | |
| `⌃O` on a `LiveSplit` (no id) → error, never a half-reboot | JAIL | `commands.go:323-325` | |
| `Enter` → `OutcomeSelected` with the row; empty filter → no-op | JAIL | `ui/model.go:173-179` | |
| `Esc` / `⌃C` → cancelled, rc 0, no writes | JAIL | `ui/model.go:180-182`, `commands.go:162-163` | |
| Chats `↑/⌃P` and `↓/⌃N` wrap at both edges; empty lists no-op; `PgUp/PgDn` and `Home/End` stay bounded | JAIL | `ui/model.go`, `ui/model_test.go` | |
| Typing filters: substring hit, then fuzzy subsequence | JAIL | `ui/model.go:354-399`, `runeSubsequence:401-416` | |
| `Backspace`/`⌃H`, `⌃W` (word), `⌃U` (clear) | JAIL | `ui/model.go:210-224` | |
| Paste message appends to the query | JAIL | `ui/model.go:131-133` | |
| Query longer than `CharLimit` 200 → clipped, never panics | JAIL | `ui/model.go:80,228-236` | |
| Live refresh mid-picker preserves cursor, query and pending kills | JAIL | `ui/model.go:247-271` | B2 |
| the open picker re-gathers and incrementally re-indexes at 5s while driven; one untouched pass already crosses the 60s park threshold (×13 growth — a real pass costs several CPU-seconds against a live fleet's ~50 tmux sockets and ~1950 processes), after which the loop PARKS — no further pass at all, only a cheap atomic-load poll — and a keystroke un-parks it within one poll tick, snapping back to 5s | JAIL | `cmd/pfm/pipeline_async_test.go`, `cmd/pfm/pipeline.go`, `cmd/pfm/pipeline_idle_backoff_test.go` (`TestPickerRefreshStreamParksThenWakesOnKeystroke`) | |
| the ambient sky/cosmos header tick starts at ~8fps and stretches geometrically (×1.35) while untouched, PARKS entirely (no further tick scheduled) within a few seconds of continuous idle, and a keystroke/paste wakes it back to full cadence instantly rather than waiting on an already-scheduled tick | JAIL | `ui/activity.go` (`tickCadence`), `ui/model.go` (`skyTickMsg`, `wakeSky`), `ui/sky_idle_backoff_test.go` | |
| a PARKED stream's Codex idle-identity poll runs `/proc/<pid>/fd` (FDLinks) every 10s (widened from 2s) but pays for the tmux `capture-pane` half of reconciliation ONLY when the FDLinks-observed rollout set changed for some PID since the previous poll — a held rollout or an identity-error PID that is unchanged is skipped outright; a rollout-LESS PID (no open rollout fd — the normal shape of a paginated Codex thread since 0.146.1) is never treated as skippable, since procfs has no opinion for it at all (2026-09-08: ~4 capture-pane execs per 5s measured on an idle Limits tab) | JAIL+tmux | `cmd/pfm/pipeline.go` (`codexRolloutFingerprints`, `codexRolloutFingerprintsEqual`, `codexRolloutFingerprintsSkippable`), `cmd/pfm/pipeline_codex_park_test.go`, `cmd/pfm/codex_clear_retry_jail_test.go` (`TestParkedPickerStillObservesCodexClear`, `TestParkedPickerRetriesRefreshAfterCodexClear`) | |
| `deps.Resolve`/`deps.Executable` memoize a successful PATH lookup per (binary name, `$PATH`) instead of rebuilding the whole dependency `Registry()` and re-walking PATH on every call — every tmux capture-pane and every spawned `codex app-server` crosses this seam; the cached entry is invalidated (and re-resolved) if the file it names disappears | JAIL | `internal/deps/registry.go` (`resolveCached`, `rememberResolved`), `internal/deps/registry_test.go` | |
| Codex's live provider sampler gets its own 90s TTL (`CodexLiveLimitsTTL`) distinct from Claude's 60s `LiveLimitsTTL` — Codex's fetch execs `codex app-server` and drives a JSON-RPC handshake (~5 CPU-seconds at ~50% of a core per call) where Claude's is a cheap disk-cache-backed HTTP read; Claude keeps refreshing on its own TTL the whole time, Codex is not re-invoked again until 90s has actually elapsed (2026-09-08: `codex app-server` measured spawning every ~10s on an idle Limits tab under the shared 5s TTL) | JAIL | `internal/stats/limits.go` (`CodexTTL`, `ttlFor`), `internal/stats/limits_test.go` (`TestLimitsSamplerLiveHonorsSeparateCodexTTL`) | |
| Claude's live provider TTL is 60s (`LiveLimitsTTL`), not 5s: the fetch is cheap for this box but NOT for the provider's rate limiter — at 5s both accounts were 429'd (2026-09-11: a 429 nine seconds after a successful fetch, escalating to a server-sent 1h `Retry-After`). The UI's 2s→30s sample tick is unchanged; `SampleLive` only reaches the network once the shared cache is older than the TTL | JAIL | `internal/stats/limits.go` (`LiveLimitsTTL`), `internal/stats/limits_test.go` (`TestLimitsSamplerLiveTTLDoesNotExtendProviderConfirmationAge`, `TestLimitsSamplerLiveKeepsRefreshingAcrossHours`, `TestLimitsSamplerLiveHonorsSeparateCodexTTL`), `internal/stats/limits_cache_refresh_test.go` (`TestLimitsSharedCacheRefreshBoundaries`) | |
| the prompt hook ages the shared cache by the record's own `fetched_at`, never the file's mtime — a writer that records ONLY a backoff rewrites the file (bumping mtime) while carrying the previous payload's `fetched_at` forward, which revived a two-hour-old payload as fresh and warned from it; both the TTL refresh gate and the one-hour stale drop in `Evaluate`, and `CachedFableWindow`'s own hour, read `fetched_at` first and fall back to mtime only for records predating it. An active backoff still suppresses the hook's own request | JAIL | `internal/usagehook/hook.go` (`cacheAge`, `fileAge`, `Evaluate`, `CachedFableWindow`, `refresh`), `internal/usagehook/hook_test.go` (`TestEvaluateAgesTheCacheByFetchedAtNotFileMtime`) | |
| a cache record stamped in the FUTURE is not fresh — it is unverifiable. `fetched_at` is attacker-, corruption-, and clock-skew-reachable now that it (not mtime) ages the record, and a forward stamp otherwise suppresses the prompt hook's refresh forever while it keeps warning from the payload that carries it; the stats sampler already refuses one (`cacheFresh`'s `!confirmedAt.After(now)`, plus `refresh`'s "future confirmation timestamp" guard) and the hook must match. A record with NO `fetched_at` still ages by the file's mtime, and a corrupt/empty/half-written record refreshes rather than warning | JAIL | `internal/usagehook/hook.go` (`cacheAge`), `internal/usagehook/hook_cache_age_adversarial_test.go` (`TestEvaluateRejectsAFutureFetchedAt`, `TestEvaluateFallsBackToMtimeWhenFetchedAtIsAbsent`, `TestCachedFableWindowAgesLegacyRecordsByMtime`, `TestCorruptCacheRecordsNeverWarn`) | |
| Claude's 60s live TTL holds where it has to: across PROCESSES via the shared `acct-<id>.json` (a second picker opened 59s later spends no request) and across the picker's own 2s result poll that a keystroke restores, so no keypress cadence can outrun it; `pfm ls --json`/`pfm doctor`/legacy `Sample()` keep `defaultLimitsTTL` (3m). An empty or unparsable `resets_at` is UNKNOWN, never expired — the reading stays and the payload stays reusable; a backoff-only write by `fetchClaudeCached` reads as two hours old to the prompt hook, which neither warns nor requests through the active 429 | JAIL | `internal/stats/limits.go` (`LiveLimitsTTL`, `cacheFresh`, `reusableClaudeUsage`, `resetPassed`), `internal/stats/limits_reset_expiry_adversarial_test.go`, `internal/ui/limits_expired_row_width_test.go` | |
| a window whose `resets_at` has PASSED describes quota that has already rolled over and is honoured everywhere the way `fableWindow` always honoured it: the Limits tab keeps the row but shows an em dash and `reset passed · awaiting refetch` — no bar and no number, carried as the `UnknownUsedPct` (-1) sentinel rather than a literal 0 that would read as a real just-reset measurement; `reusableClaudeUsage` reports false when every utilization-bearing window has expired, so `fetchClaudeCached` skips the cache-fresh short-circuit and refetches (an active backoff is still never bypassed); and an expired window contributes only its fallback to the prompt hook's `maximum`, so a passed reset can never raise `USAGE LIMIT IMMINENT` | JAIL | `internal/stats/limits.go` (`usageWindows`, `resetPassed`, `expiredResetNote`, `reusableClaudeUsage`), `internal/usagehook/hook.go` (`currentUtilization`, `resetPassed`), `internal/stats/limits_test.go` (`TestUsageWindowsBlanksPassedResetsAndStopsCacheReuse`, `TestLimitsRefetchesCacheWhoseWindowsAllExpiredUnlessBackedOff`), `internal/ui/stats_test.go` (`TestLimitsTabRendersPassedResetNoteInsteadOfStaleBar`), `internal/ui/limits_expired_row_width_test.go` (`TestExpiredLimitsRowStaysBoundedAtEveryWidth`), `internal/usagehook/hook_test.go` (`TestEvaluateIgnoresWindowsPastTheirReset`) | |
| the prompt hook's warn LINE carries the same expiry opinion its arithmetic does: a window whose `resets_at` has passed renders `5h — (reset passed)` instead of a percentage plus a reset moment already in the past (`5h 0% (resets 11:03)` read out at 11:40 as if 11:03 were still ahead); the live window in the same line keeps its number and its reset | JAIL | `internal/usagehook/hook.go` (`windowPhrase`, `resetPassed`), `internal/usagehook/hook_test.go` (`TestWarnLineSaysResetPassedInsteadOfAStalePercentage`) | |
| the prompt-hook command tests pin the SEAT they claim to test rather than inheriting the developer's shell: a Codex-seat case clears `CLAUDE_CODE_SESSION_ID` as well as setting `CODEX_THREAD_ID`, so the suite's verdict is identical inside and outside a live Claude Code session | JAIL | `cmd/pfm/statusline_command_test.go` (`TestCodexSeatUsageHookNeverTouchesClaudeCredentials`) | |
| the Limits tab's own 2s sample tick (`statsWaitCmd`) decays geometrically (×1.35) toward a 30s ceiling while untouched via the same `tickCadence` the sky tick uses, and snaps back to 2s on any keystroke; the Stats tab's live CPU/memory bars stay pinned to the flat 2s cadence throughout — `statsCadence` is never consulted for it | JAIL | `ui/model.go` (`statsCadence`, `statsWaitCmd`), `ui/stats_idle_backoff_test.go` | |
| `render()` builds the fleet list panel (`renderListPanel`) only on the branch that actually uses it (Chats); a Stats, Limits, or Cosmos frame never builds and discards it | JAIL | `ui/render.go`, `ui/render_list_panel_skip_test.go` (`TestRenderSkipsListPanelBuildOnLimitsStatsAndCosmos`) | |
| an ABSENT Claude `.credentials.json` (the normal shape wherever credentials live in the OS keychain) records NO provider backoff, so the identity-matched `cc-rate-limits` statusline snapshot keeps supplying the card on EVERY sample, not only the first — a backoff replayed as `errors.New(record.Backoff.Message)` cannot satisfy the `errors.Is(err, os.ErrNotExist)` that gates `FetchClaude`'s statusline fallback, which blanked the Limits card for the whole backoff window while fresh quota sat on disk | JAIL | `internal/stats/limits.go` (`fetchClaudeCached`), `internal/stats/usage_source.go` (`FetchClaude`), `internal/stats/limits_test.go` (`TestLimitsSamplerKeepsStatuslineQuotaAfterACredentialBackoffIsRecorded`) | |
| the usage cache is SHARED across every pfm process, so a backoff written by a PEER (another picker, an MCP server, a build predating the no-write rule) must not blank the card either: with `usagehook.CredentialPath` absent on disk an EMPTY backoff replay falls through to the free local path that returns a properly wrapped `os.ErrNotExist`, while a backoff carrying usable windows — a 429's cached quota — still serves them unchanged; the absence is re-checked from the FILESYSTEM, never parsed out of the record's platform-varying message | JAIL | `internal/stats/limits.go` (`fetchClaudeCached`, `credentialsAbsent`), `internal/usagehook/hook.go` (`CredentialPath`), `internal/stats/limits_test.go` (`TestLimitsSamplerIgnoresAPeerCredentialBackoffWhenCredentialsAreAbsent`, `TestLimitsSamplerStaleRateLimitStatusPreservesRetryTime`) | |
| the `cc-rate-limits` statusline snapshot carries the model-SCOPED Fable window (`fable_used`, `fable_resets_at`) alongside the two flat ones — `windowsAt` has already folded the harness's `limits` array into `data.RateLimits.Windows` before `harvestRateLimits` runs, and a reader whose ONLY limits source is this file can render no window the writer dropped; 0% used is recorded as a real reading, a past reset is skipped, and an older snapshot missing the keys reads back as absent | JAIL | `internal/statusline/render.go` (`harvestRateLimits`), `internal/statusline/statusline_test.go` (`TestStatuslineQuotaSnapshotCarriesTheScopedFableWindow`) | |
| the Limits tab reconstitutes Fable from that snapshot by appending a synthesized `weekly_scoped` / display-name `Fable` entry to `usage.Limits` rather than rebuilding the selector — `usagehook.fableWindow` stays the single opinion on which scoped limit is Fable, so the two cannot drift; a utilization outside 0..100 is a hard decode error, not a silently dropped window | JAIL | `internal/stats/limits.go` (`statuslineClaudeLimits`, `fetchClaudeStatusline`), `internal/stats/limits_test.go` (`TestLimitsSamplerRendersTheFableWindowFromAStatuslineSnapshot`) | |
| a Claude account with NEITHER a credentials file NOR an identity-matched statusline snapshot gets the same hidden one-turn Haiku probe (`tryAck` → `defaultAck`, headless `-p ACK --max-turns 1`, at most once per account per sampler) the credential-rejection path uses, then re-reads BOTH sources; a snapshot belonging to a PREVIOUS account identity is still never adopted after the probe, and a probe that fails has its reason remembered (`ackFailure`) so every later refresh keeps naming it instead of decaying to the bare missing-credentials error | JAIL | `internal/stats/usage_source.go` (`FetchClaude`), `internal/stats/limits.go` (`tryAck`, `probeFailure`, `errAckAlreadyAttempted`), `internal/stats/limits_test.go` (`TestLimitsSamplerProbesAnAccountThatHasNoCredentialsAndNoSnapshot`, `TestLimitsSamplerReportsAFailedCredentialProbeAndKeepsReportingIt`, `TestLimitsSamplerRejectsStatuslineQuotaFromPreviousAccountIdentity`) | |
| an account's OAuth credential resolves from the credentials FILE first and the macOS login keychain second (`Claude Code-credentials-<first 4 bytes of sha256(configDir), hex>`, read via `/usr/bin/security` because pfm builds `CGO_ENABLED=0`) — on a macOS host Claude Code writes NO `.credentials.json` at all, so a file-only reader reports every fully-logged-in account as having no credentials and the provider usage API (the only source of the scoped Fable window) is permanently dead | JAIL | `internal/usagehook/credential.go` (`loadCredential`, `KeychainService`), `internal/usagehook/keychain_darwin.go`, `internal/usagehook/keychain_other.go`, `internal/usagehook/credential_test.go` (`TestLoadCredentialReadsTheKeychainWhenNoCredentialsFileExists`, `TestLoadCredentialPrefersTheCredentialsFileOverTheKeychain`, `TestKeychainServiceDerivesTheConfigDirScopedName`) | |
| the service name is derived per config dir and NEVER discovered by scanning, because a host accumulates one keychain entry per config dir it has ever used and a scan could attribute one account's quota to another; two config dirs never derive the same entry | JAIL | `internal/usagehook/credential.go` (`KeychainService`), `internal/usagehook/credential_test.go` (`TestKeychainServiceDerivesTheConfigDirScopedName`) | |
| a SIGNED-OUT credential (present in either source, empty `accessToken`) is distinguished from an ABSENT one: it reports `ErrSignedOut` naming `claude /login` as the one repair, never `no such file or directory`; it still counts as `IsCredentialUnavailable` so the statusline snapshot fallback engages, and it is NOT spent on the Haiku ACK probe because no headless turn can mint a token from an empty refresh token | JAIL | `internal/usagehook/credential.go` (`ErrSignedOut`, `IsCredentialUnavailable`), `internal/stats/usage_source.go` (`FetchClaude`), `internal/usagehook/credential_test.go` (`TestLoadCredentialTellsSignedOutApartFromAbsent`), `internal/stats/limits_test.go` (`TestLimitsSamplerServesASnapshotForASignedOutAccountWithoutProbingIt`, `TestLimitsSamplerTellsASignedOutAccountToLogIn`) | |
| a keychain we FAILED to read (locked keychain, denied ACL, `security` unavailable) never renders as absence — only `security`'s `errSecItemNotFound` (exit 44) maps to `os.ErrNotExist`; every other failure surfaces with the underlying reason and does NOT satisfy `IsCredentialUnavailable`, so the account cannot be silently parked on a stale fallback | JAIL | `internal/usagehook/keychain_darwin.go` (`keychainNotFoundStatus`), `internal/usagehook/credential_test.go` (`TestLoadCredentialSurfacesAKeychainFailureRatherThanReportingAbsence`) | |
| the credential is re-resolved on EVERY fetch with no cached copy, so the 401 -> ACK refresh -> retry chain sees the rotated token Claude Code just wrote back to the keychain; an access token lives hours against a refresh token good for weeks, so a cached one would strand the account at the first expiry | JAIL | `internal/usagehook/hook.go` (`Fetch`, `refresh`), `internal/usagehook/credential_test.go` (`TestLoadCredentialRereadsTheKeychainSoARotatedTokenIsPickedUp`), `internal/stats/limits_test.go` (`TestLimitsSamplerCredentialRefreshCanRetryBeforeBackoff`) | |
| the prompt hook's own "is this a Claude account seat at all" gate consults BOTH sources too, so a keychain host's usage warning is not permanently silent; it stays silent only for a genuinely credential-less dir, and a keychain read FAILURE propagates instead of masquerading as "no account here" | JAIL | `internal/usagehook/hook.go` (`Evaluate`), `internal/usagehook/credential.go` (`CredentialAvailable`) | |
| an idle `pfm ls` picker (no keystrokes ≥30s) costs ≤2% of one core on THIS box's real fleet (~1950 processes, ~50 tmux sockets), measured on the installed binary, not just an empty jail — Bubble Tea's own renderer ticks at a capped 10fps (`tea.WithFPS(10)`, down from the library default 60) rather than an uncapped flush loop, and a single gather pass reads every live process's cmdline ONCE (`processCmdlines`) instead of once per engine detector | JAIL + manual real-box /proc/pid/stat measurement (see report) | `ui/picker.go` (`tea.WithFPS`), `internal/gather/procscan.go`, `internal/gather/gather.go`, `ui/sky_idle_backoff_test.go` | |
| Refresh that DROPS the selected row → cursor falls back safely | JAIL | `ui/model.go:390-399` | B2 |
| Window resize re-widths the query field | JAIL | `ui/model.go:123-127` | |
| Footer legend matches the real bindings | JAIL | `ui/render.go:122-125` | |
| every Stats subtab renders labeled column headers | JAIL | `ui/stats_test.go`, `ui/render.go` | |
| agent rows are orange rather than Codex magenta; Stats labels and values use semantic colors | JAIL | `ui/golden_test.go`, `ui/stats_test.go`, `ui/render.go` | |
| Stats Chats renders lifetime-traffic `TOKENS` plus rolling one-minute live `TOK/MIN`; first sample is `…`, idle is `–`, and transcript/session discontinuities reset | JAIL | `stats/stats_test.go`, `stats/tokens.go`, `ui/stats_test.go`, `ui/golden_test.go` | |
| Stats Docker begins with cached container `NAME` and `IMAGE`, then cgroup metrics | JAIL | `stats/docker_identity_test.go`, `stats/docker_identity.go`, `ui/stats_test.go` | |
| the two-second Stats sampler delta-parses transcript growth and performs at most one Docker API identity lookup per new cgroup id | JAIL | `stats/stats_test.go`, `stats/docker_identity_test.go` | |
| Limits monitoring, cache freshness, and shared provider backoff | JAIL | See `stats.LimitsSampler.SampleLive` below | |
| Claude `limits[]` selects active future scoped Fable once for Stats and statusline; unknown top-level provider keys never become windows | JAIL | `usagehook/hook_test.go`, `statusline/statusline_test.go`, `stats/limits_test.go` | defect RED |
| Limits cards render fractional gradient bars, urgent/full/reset states, provider max totals, one skip footer, and fixed-width 80/120/narrow layouts | JAIL | `ui/stats_test.go`, `ui/golden_test.go`, `testdata/golden/ui_limits_*.ansi` | defect RED |
| the cosmos goldens pin the CODE, not the CPU: the quadratic Bezier rounds each of its three products explicitly (`bezierAt`), so an FMA architecture (arm64, ppc64, s390x, riscv64) cannot contract `a*b + c` into one fused multiply-add and slide a braille dot a cell — the focus golden's dead-straight rail down pixel column 78 keeps its two truncated samples (5 and 9), and with them (plus their halo rows) the ⢸ (U+28B8) at byte 1348, on every machine | JAIL | `ui/cosmoscanvas.go` (`bezierAt`), `ui/cosmoscanvas_test.go` (`TestBezierAtPinsTheStraightRailPixelColumns`, `TestBezierAtRefusesTheFusedMultiplyAdd`, `TestBezierAtRoundsEveryProductExplicitly`), `testdata/golden/ui_cosmos_focus_80.ansi` | #14 F9 |

### stats.LimitsSampler.SampleLive

**Tier:** JAIL. **Coverage:** `internal/stats/limits_test.go`, `internal/stats/limits_cache_refresh_test.go`, `internal/stats/limits_reset_expiry_adversarial_test.go`, `internal/ui/limits_refresh_test.go`, `internal/ui/limits_expired_row_width_test.go`, `cmd/pfm/limits_accounts_test.go`, `internal/usagehook/hook_test.go`, `internal/usagehook/hook_cache_age_adversarial_test.go`.

The focused Limits tab uses a sixty-second cache TTL and a two-second result poll (decaying to 30s while untouched). Accounts refresh independently with one pending request per account; leaving the tab or closing the picker cancels pending requests without recording provider backoff. Successful cache reads retain the provider's confirmation time. Empty or future-dated payloads cannot suppress a fresh fetch. The shared `acct-<id>.json` and `codex-<id>.json` caches retain HTTP 429 backoff of at least ten minutes and other provider failure backoff of sixty seconds; the prompt hook keeps its 180-second default TTL.

An independent five-second UI clock advances quota reset countdowns, confirmation ages, and the Cosmos ledger while fleet scans and sky animation are parked, including with `--no-sky`. Delayed sample, fleet, and animation timestamps cannot rewind time. Limits percentages explicitly say `used`; a window past its reset says neither, rendering an em dash with no bar. Rendering remains bounded at 40-, 60-, 80-, and 120-column widths.

## C — Row-kind × operation cross-matrix

Each row is one **session kind** crossed with the operations that touch it. This is the table tonight's four bugs all live in.

| flow (kind × op) | safety | expected behavior (source) | regression |
| ------------------------------------------------------------------------------------------------------------------ | ---------------- | -------------------------------------------------------------------------------- | ---------- |
| **live-claude** — one pane crumb → one row, name from indexed title else pane title | JAIL | `compose/compose.go:279-354,356-399`, `naming/naming.go:23-44` | |
| **live-claude, socket crumb only** (no pane crumb) → row only if a claude process holds the socket | JAIL | `compose/compose.go:337-346` | |
| **live-claude split** (≥2 pane crumbs) → one `LiveSplit` row, names joined `a+b` | JAIL | `compose/compose.go:327-329,401-480` | |
| **live-claude, two servers one transcript** → collapsed, `ServerCount` = n, newest socket wins | JAIL | `compose/compose.go:759-803` | |
| **live-codex rollout-backed** → fd-walk finds the rollout, pane must exist in the SAME snapshot | JAIL | `gather/codexproc.go:61-75`, `compose/compose.go:492-501` | |
| **live-codex store-only, fresh** → identity via `CODEX_THREAD_ID`, else cwd+birth ≤120s | JAIL | `gather/codexproc.go:76-87`, `resolve/codex.go:35-87` | B1 |
| **live-codex store-only, resumed** → birth window cannot match; row must not vanish or mislabel | **REAL-SESSION** | `resolve/codex.go:49-85` (returns an error → `codexproc.go:82-84` drops the row) | **B1** |
| **live-codex, exported thread id unknown to the store** → `RolloutPath` is `""` → row ID becomes `"."` | JAIL | `resolve/codex.go:42-48` → `compose/compose.go:502-513,834-847` | **B1, B2** |
| **live-codex whose thread is archived in Codex** → filtered out of candidates, same `""` path | JAIL | `store/codexstate.go:161-163` | B1, B2 |
| **resume-claude** → transcript row, capped 30 in DefaultView | JAIL | `compose/compose.go:70-108`, `store/queries.go:35-59` | |
| **resume-codex** → one row per lineage, keyed by `RootID`, newest member supplies path/mtime | JAIL | `compose/compose.go:110-148`, `store/lineage.go:22-81` | B2 |
| **store-only resumable** (thread with no rollout file) → row created with placeholder path | JAIL | `index/codexstate.go:98-130` | B1, B2 |
| **store-only resumable, resumed** → `LineageRoot` is set to its OWN id, so it does NOT join its ancestor's lineage | JAIL | `index/codexstate.go:120-129` (no `SessionID`/`ParentThread` set) | **B1, B2** |
| **twin threads from one conversation** → must collapse to ONE row; a kill on either must cover both | JAIL | `store/lineage.go:22-81`, `compose/compose.go:660-673` | **B2** |
| **agent row** → live claude under a NON-primary `CLAUDE_CONFIG_DIR` carrying `--session-id`/`--resume` | JAIL | `gather/agents.go:12-69` | B3 |
| **agent row, transcript not indexed** → row synthesized from the session id alone | JAIL | `compose/compose.go:564-567` | **B3** |
| **agent row already live as a chat** → suppressed, never doubled | JAIL | `compose/compose.go:559-562,568` | |
| **workflow / SDK background (claude)** → `IsBG`, excluded from DefaultView, visible under `-a` | JAIL | `index/claude.go:41-44,83-95`, `compose/compose.go:725-736` | B2 |
| **workflow / SDK background (codex sub-thread)** → `thread_source != user` → not `Listed()` | JAIL | `store/codexstate.go:44-46,252` | B2 |
| **squatter socket** (a session whose name ≠ its socket) → never a chat row | JAIL+tmux | `check_command.go:181-203`; naming `gather/labels.go:36-56` | |
| **vsct bunker chat** → `vsct` sockets excluded from the probe; open uses `exec` | JAIL+tmux | `gather/tmuxprobe.go:356-361`, `pipeline.go:666-668`, `action/synth.go:290-310` | |
| **killed row** → excluded from DefaultView, counted in `KilledCount`, shown under `-K` | JAIL | `compose/compose.go:660-673,708-716,738-747` | B2, B3 |
| **empty row** (size 0 / 0 prompts) → suppressed from DefaultView, counted in `SuppressedCount` | JAIL | `compose/compose.go:725-736` | |
| **both accounts** — a row's account comes from the longest matching config root | JAIL | `compose/compose.go:865-887`, `pipeline.go:527-542` | |
| **primary switch** — db meta first, `~/.claude-primary` mirror second, off-roster → 1 | JAIL | `shared/shared.go:441-482`, `pipeline.go:553-559` | |
| **cache badge on/off** — `C1H` from `/proc` env of the live process, per socket | JAIL | `gather/cache1h.go`, `compose/compose.go:378,413,586` | |

## D — Index, naming and identity resolution

| flow | safety | expected behavior (source) | regression |
| ------------------------------------------------------------------------------------------------- | ------ | --------------------------------------------------------------------------- | ---------- |
| Incremental skip: size+mtime unchanged → skipped | JAIL | `index/index.go:337-348` | |
| Delta parse: file grew and a prior parse exists | JAIL | `index/index.go:350-366` | |
| Row with `parsed_offset` 0 (store-created) → FULL parse, never a delta | JAIL | `index/index.go:354-366` | B1 |
| Parser-version bump forces a full reparse for each engine | JAIL | `index/index.go:96-116` | |
| `PriorityCWD` sorts this project's transcripts first | JAIL | `index/index.go:288-335` | |
| `PriorityOnly` pass skips codex, deletes and cx-names entirely | JAIL | `index/index.go:88-93,193,210,239` | |
| Deleted transcript/rollout is pruned only in a full pass | JAIL | `index/index.go:208-225` | |
| Codex state store: newest `state_<N>.sqlite` wins per thread id | JAIL | `store/codexstate.go:48-107,114-139` | |
| Codex state store opened `mode=ro`, never `immutable` (WAL visibility) | JAIL | `store/codexstate.go:182-193` | B4 |
| Older state schema missing columns → `COALESCE` fallback, never a failed pass | JAIL | `store/codexstate.go:266-309` | |
| Unreadable state generation is SKIPPED, never blanks the Codex half | JAIL | `store/codexstate.go:119-123` | |
| Store-vouched thread survives the prune that removes file-less rows | JAIL | `index/codexstate.go:45-52` | B2 |
| `applyCodexThread` only takes store content when the row has no parsed bytes | JAIL | `index/codexstate.go:79-96` | B1 |
| `reloadCxNames` truncates and rebuilds `cx_names` from `session_index.jsonl` on size/mtime change | JAIL | `index/cxindex.go:22-93` | **B4** |
| `reconcileCodexNames` applies store names AFTER the file rebuild — store outranks file | JAIL | `index/index.go:256-265`, `index/codexstate.go:132-179` | **B4** |
| Rename made only in `session_index.jsonl` while the store holds an older `name` | JAIL | conflict between `index/cxindex.go:51-63` and `index/codexstate.go:149-159` | **B4** |
| `CxName` lineage walk: own id → session id → parent thread → first prompt | JAIL | `naming/naming.go:88-106` | **B1** |
| `CxName` on a ≥3-deep lineage whose NAME sits on the root | JAIL | `naming/naming.go:97-105` vs root from `compose/compose.go:644-658` | **B1** |
| `DisplayName` precedence: custom title → AI title → first prompt | JAIL | `naming/naming.go:10-18` | |
| `LiveFallback` for cc sockets uses pane title, never the generated session name | JAIL | `naming/naming.go:23-44` | |
| Junk-prompt filter (`<x…`, `Caveat:`, `[Request`) and compact-summary skip | JAIL | `naming/naming.go:46-56`, `index/claude.go:52-56` | |
| Lineage cycle in the parent chain → lowest id in the cycle becomes root | JAIL | `store/lineage.go:83-128` | B2 |
| `ReconcileCodexLineageRoots` denormalizes roots after each full pass | JAIL | `store/lineage.go:201-236` | B2 |

## E — Kill / unkill and shared state

| flow | safety | expected behavior (source) | regression |
| --------------------------------------------------------------------------------------- | --------- | -------------------------------------------------------------------------------------------------- | ---------- |
| A kill writes one SQLite row and never recreates the retired carrier | JAIL | `shared/shared.go`, `shared/shared_test.go` | B2 |
| `KilledAt` returns SQLite rows or reports the lookup failure | JAIL | `shared/shared.go`, `shared/shared_test.go` | B2 |
| Persistent `SQLITE_BUSY` on kill → retry, warn, count, return the write error | JAIL | `store/killed.go:43-79` | |
| Persistent `SQLITE_BUSY` on unkill follows the same nonzero failure contract | JAIL | `store/killed.go:43-79` | |
| Engine derived from the index, not stored; transcript wins a collision | JAIL | `store/killed.go:171-241` | B3 |
| Killed id no index knows → empty engine = "killed whatever the engine" | JAIL | `store/killed.go:171-177`, `compose/compose.go:664-668` | B3 |
| `applyKill` skips split rows and ID-less rows | JAIL | `compose/compose.go:660-663` | B2 |
| Kill is PERMANENT — a growing prompt count never un-kills | JAIL | `compose/compose.go:669-671`; `compose/compose_test.go` | |
| `kill <id>` for an id in NEITHER table → `chat %q is not indexed`, rc 1 | JAIL | `kill/manager.go:138-165` | **B3** |
| Picker kill failure is a visible `⌃X failed` status and records no applied change | JAIL | `ui/model.go`, `ui/model_test.go` | **B3** |
| `unkill` maps a Codex member id to its lineage root before deleting | JAIL | `kill/manager.go:113-131` | B2 |
| Kill by lineage ROOT vs a RAW rollout id written by an older writer | JAIL | `compose/compose.go:116,645`, `compose/compose_test.go:904-950` | **B2** |
| `--exit` finisher: `/exit` (cc) or `/quit` (cx), poll, kill-pane, sweep crumbs | JAIL+tmux | `kill/finisher.go:104-131` | |
| Hiding a LIVE chat (resolved socket+pane) also runs the `--exit` finisher — CLI `chat kill <id\|self>`, picker ⌃X, and MCP `chat_kill` all resolve the address and hit the same `kill.Manager.Kill` branch; a resumable-only target stays a store write | JAIL+tmux | `kill/manager.go:86-145`, `cmd/pfm/pipeline.go` (`resolveRowTarget`), `ui/model.go` (`toggleKilled`), `mcpserv/actions.go` (`chatKill`) | |
| `pfm chat reload --new --hide` never runs the finisher on the conversation it hides — the pane it left behind now belongs to the reborn chat, so `hideReloadedConversation` calls `kill.Manager.Kill` with no socket/pane | JAIL+tmux | `cmd/pfm/reload_command.go` (`hideReloadedConversation`), `cmd/pfm/reload_hide_test.go` | |
| Post-exit re-kill keeps the ORIGINAL `killed_at` | JAIL | `kill/finisher.go:149-167` | |
| Teammate reap: `new` → kill-server, `pane` → kill-pane, never kill-server | JAIL+tmux | `kill/finisher.go:181-245` | |
| Teammate reap falls back to the flat file when the table has no row | JAIL | `kill/finisher.go:256-290` | |
| Two concurrent pickers killing different chats → both survive (WAL + transaction) | JAIL | `shared/shared.go`, `shared/shared_test.go` | |
| A `_KILL…` label kills a chat with no store row; legacy `_HIDE…` labels remain readable | JAIL | `naming.LabelKilled`, `compose/compose.go` (`applyKill`); `compose/label_kill_test.go` | |
| A label-killed chat still shows under `-a` and `-K`, and counts as killed | JAIL | `compose/label_kill_test.go` | |
| A split row is never label-killed — its name is a join, not a label | JAIL | `compose/label_kill_test.go` | |
| Label-killed rows never spend a cached-frame candidate slot (30/15) | JAIL | `store/queries.go` (`labelKilledSQL`, `codexLineageLabelKilled`); `store/label_candidates_test.go` | |
| Picker kill key refuses a label-killed row — renaming is the unkill | JAIL | `ui/model.go` (`toggleKilled`); `ui/label_kill_test.go` | |

## F — Action synthesis and launch

| flow | safety | expected behavior (source) | regression |
| -------------------------------------------------------------------------------------------------------------- | ---------------- | ----------------------------------------------------------------------- | ---------- |
| `NewClaude` → native `ClaudeSpawn` policy and `tmux new-session` | JAIL | `action.Synthesize`, `action.ClaudeSpawn` | `internal/action/native_new_claude_test.go` |
| Every Claude launch (interactive, resume, probe, query, the launcher shim, headless `pfm headless exec`/`pfm ask`) carries `--settings {"outputStyle":"default"}` so a project/user `outputStyle` never double-applies over the staged professor prompt; Codex and OpenCode carry nothing of it | JAIL | `internal/engine/builtin.go` (`Claude.LaunchArgs`), `internal/action/claude_spawn.go`, `internal/headless/run/run.go` (`arguments`) | `internal/action/output_style_settings_test.go`, `internal/headless/run/output_style_settings_test.go` |
| `NewCodex` → `(cd -- <cwd> && cx)` | JAIL | `action/synth.go:94-98` | |
| `tmux.titles.enabled` (default true) and `nameSync.interval` (Go duration, minimum 1m floor, default 15m) load, validate, round-trip through `Marshal`, and show up in `pfm config show` with their source | JAIL | `internal/config/config.go`, `internal/config/tmux_titles_test.go`, `internal/config/name_sync_test.go`, `cmd/pfm/config_command_test.go` | issue #14 F10/F12 |
| every chat server is born through ONE creator (`spawn.CommandTmux.NewSession`) carrying ONE option list (`config.ChatServerOptions`): the title policy — a disabled policy applies NEITHER option, a nil policy keeps today's behaviour — plus `automatic-rename off`, never gated; the picker (plan-carried `ChatServer`) and the shim (`pfm internal chat-server`) reach it as doors, and an unsized server lets its first client size it | JAIL+tmux | `internal/config/tmux_titles_test.go`, `internal/spawn/tmux_titles_jail_test.go`, `internal/action/tmux_titles_jail_test.go`, `cmd/pfm/chat_server_command_test.go` | issue #14 F10 |
| `Live` → `TMUX= tmux -L <sock> attach -t <target>` | JAIL | `action/synth.go:99-107,300-310` | |
| Live codex target is `session:window`, falling back to session then socket | JAIL | `action/synth.go:312-322` | B1 |
| Live codex window name verified against the live server before use | JAIL+tmux | `action/executor.go:92-98,139-156` | B1 |
| `ResumeClaude` → resume, with the agent router as the `                                                        |                  | ` fallback | JAIL | `action/synth.go:144-173` | |
| `Agent` → agent router first, fresh resume as the `                                                            |                  | ` fallback | JAIL | `action/synth.go:108-143` | B3 |
| `ResumeCodex` → detached server created BEFORE the attach line is emitted | JAIL+tmux | `action/synth.go:174-191`, `action/executor.go:128-135` | |
| Bunker (`$TMUX` socket basename `vsct`) → `exec` prefix on every launch line | JAIL | `action/synth.go:290-310`, `pipeline.go:666-668` | |
| Primary account outside the configured roster → refuses to synthesize | JAIL | `action/synth.go`, `action/synth_test.go` | |
| NUL in any row value → refuses | JAIL | `action/synth.go:66-79` | |
| Launch hygiene strips inherited identity, cache, and endpoint state | JAIL | `action/synth.go:21-37` | |
| `internal launch` pure pass-through policy execs print/output/help/version/subcommands and existing pfm panes directly with argv unchanged | JAIL | `launch_command.go`, `launch_command_test.go` | |
| no-TTY interactive Claude starts one listed `cc-*` session, prints one socket line, and propagates the pane command's status through tmux 1.8+ `wait-for` | JAIL+tmux | `launch_command.go`, `TestInternalLaunchNoTTYCreatesListedSessionAndPropagatesExit` | |
| TTY interactive Claude attaches to its new session; tmux startup, wait timeout, and missing/invalid status are loud and never fall back to bare Claude | JAIL+tmux | `launch_command.go`, `launch_command_test.go` | |
| Autonomy flags appended AFTER the resume argument, never duplicated on fresh launches | JAIL | `action/synth.go:39-50,215-242` | |
| Dead live socket at open time → demoted to a resume in a fresh server | JAIL+tmux | `action/executor.go:64-87` | |
| Dead live socket on a SPLIT row (no id) → hard error | JAIL | `action/executor.go:68-73` | |
| Self-switch: already on the target server → select the engine window, emit NO line | JAIL+tmux | `action/executor.go:223-278` | |
| Engine-window choice: exact `claude`/`codex` → `node`/version → lowest index | JAIL | `action/executor.go:264-278` | |
| Open gate: birth account/cache ≠ picker → offer reboot; failure attaches as-is | **REAL-SESSION** | `action/executor.go:158-207`, `action/gate.go` | |
| Reboot-to-match invokes `pfm chat reload --sock … <acct> --1h <0\|1>` | JAIL+Go | `action/executor.go:190-199` | |
| `_cc_solo` stray-claude sweep before an attach/resume | JAIL | `action/executor.go:208-215`, `action/solo.go` | |
| `_cc_solo` removes a crumb only after a successful empty pane probe; probe errors preserve it | JAIL | `action/executor_test.go:255-278`, `action/solo.go` | |
| `_cc_solo` skips the stray-Claude kill sweep when the keep socket cannot be probed | JAIL | `action/executor_test.go:284-308`, `action/solo.go` | |
| Empty keep-set is destructive only for `ResumeClaude`; `Agent` skips the sweep and live rows keep their socket | JAIL | `action/executor_test.go:310-373`, `action/executor.go:115-123,200-208` | |

## G — MCP server (18 tools)

| flow | safety | expected behavior (source) | regression |
| --------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------- | ------------------------------------------------------------------ | ----------------------------------------------------------------- |
| Tool roster is exactly the canonical `ToolNames()` set with correct read-only / mutating annotations | JAIL | `mcpserv/server.go`, `mcpserv/workflow_roster_test.go` | |
| `mcp ls` reports each registered server's independent enabled state and source | JAIL | `main.go`, `config_cli_test.go`, `internal/config/config_test.go` | |
| `mcp chat enable                                                                                          | disable`is atomic/idempotent; disabled`serve` names its remedy | JAIL | `main.go`, `config_cli_test.go`, `internal/config/config_test.go` | |
| `chat_ls` default view, `all`, `killed`, `project` filter | JAIL | `mcpserv/backend.go:82-253` | B2 |
| `chat_ls` with both `all` and `killed` → error | JAIL | `mcpserv/backend.go:83-85` | |
| `chat_ls` state: `busy` / `idle` / `dead` / `resumable` per row | JAIL+tmux | `mcpserv/backend.go:181-225`, `inject.IsBusy` | |
| `chat_ls` treats an **agent** row as live and captures its pane | JAIL+tmux | `mcpserv/backend.go:303-308` (diverges from `ui/model.go:467-471`) | B3 |
| `chat_ls` split row folds every pane's state into one verdict | JAIL+tmux | `mcpserv/backend.go:184-208` | |
| `chat_ls` engine label for a `NewCodex`/agent kind | JAIL | `mcpserv/backend.go:317-322` | |
| `chat_ls` primary account agrees with the picker | JAIL+sh | `mcpserv/backend.go:272-279` vs `pipeline.go:553-559` | |
| `chat_resolve` kind validation (`label`/`session`/`cxwin`) | JAIL | `mcpserv/server.go:114-124` | |
| `chat_resolve` status mapping 0/1/2 → `ok`/`not_found`/`ambiguous` | JAIL+tmux | `mcpserv/server.go:126-142` | |
| `chat_inject` full guard chain, mirrors chat.sh | JAIL+tmux | `mcpserv/server.go` (`chatInject`), `inject/engine.go` (`Engine.Inject`, `Engine.inject`) | |
| `chat_inject` refuses ANY `/compact` primary outright, with or without `then` → refused code 6; compaction is `chat_self_compact` / `pfm chat self-compact` only | JAIL | `inject/engine.go` (`Engine.Inject`); `TestChatInjectCarriesTheThenArgument` (`mcpserv/whoami_find_test.go`), `TestInjectRefusesCompactPrimaryPointingToSelfCompact` (`inject/engine_test.go`) | |
| `chat_inject` a `then` steer that is itself `/compact` → refused code 6 | JAIL | `inject/engine.go` (`checkSteerChain`) | |
| a 2,147-rune `/compact` focus → paced literal chunks, full transcript body, command fires; proven through `engine.inject` with `Chain: true` (the shape `DeliverThen`'s waiter drives — `chat_inject` no longer accepts a `/compact` primary at all) | JAIL+tmux | `inject/engine.go`, `then_test.go`, `tmux_jail_test.go` | |
| `chat_inject` long bodies cross the measured per-engine inline boundary into bracketed paste (proven by tail match or placeholder), by RUNE count — not a pointer | JAIL+tmux | `inject/body.go`, `inject/engine_test.go` | |
| `chat_inject` has no absolute body cap; prose above the inline boundary travels whole, byte-exact, through bracketed paste | JAIL | `inject/body.go`, `engine_test.go` | |
| `chat_capture` `tail_lines` 1..1000, `max_bytes` 1..4Mi, rune-safe tail cut | JAIL+tmux | `mcpserv/server.go:209-271` | |
| `chat_whoami` takes NO arguments; identity from this process only | JAIL+tmux | `mcpserv/server.go:177-207`, `mcpserv/types.go:95-97` | |
| `chat_whoami` failure returns `not_found` + message, never an error | JAIL | `mcpserv/server.go:186-195` | |
| `chat_find` needle extraction identical to chat.sh's awk pass | JAIL | `mcpserv/search.go:33-68` vs `chat.sh:455-456` | |
| `chat_find` excludes the asking session unless `include_self` | JAIL | `mcpserv/search.go:70-98` vs `chat.sh:462` | |
| `chat_read` bounds: `last_n` ≤200, `max_bytes` ≤1Mi, Claude and Codex turn shapes | JAIL | `mcpserv/read.go:18-21,49-70,199-273` | |
| `chat_status summary=true` delegates to the canonical CLI and returns `summary` + `summary_cached` | JAIL | `TestChatStatusSummaryUsesCanonicalCommandAndReturnsField` | |
| `chat_branch` is RETIRED from the MCP roster and its absence is pinned by name, so it cannot return under the advertised set; the CLI `pfm chat branch` is unaffected | UNIT | `TestChatMCPRosterNeverAdvertisesChatBranch` | |
| `chat_load` / `pfm chat load` stay retired — the MCP roster never advertises the tool and the CLI verb refuses as an unknown command | UNIT+JAIL | `TestChatMCPRosterNeverAdvertisesChatLoad`, `TestChatLoadVerbIsRetired` | |

## H — `pfm chat`: subcommands, guards, `--then`, exit codes

| flow | safety | expected behavior (source) | regression |
| -------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------- | --------------------------------------------------------------------------- | ----------------------- |
| root `whoami [--label]` → this chat's immutable socket identity or display label | JAIL+tmux | `whoami_command.go`, `whoami_test.go` | |
| `chat new NAME` → detached chat on a fresh immutable `cc-*`/`cx-*` socket | JAIL+tmux | `run_command.go`, `run_jail_test.go` | |
| a named chat resolves by its launch name before the first prompt creates a transcript or crumb | JAIL+tmux | `booting_row_jail_test.go`, `internal/compose/compose_test.go` | |
| `chat new NAME --attach` → launch, then attach this terminal; `--await --attach` → rc 2 | JAIL+tmux | `run_command.go`, `attach_e2e_test.go` | |
| `chat open <name                                                                                                                                                     | socket    | id                                                                          | self>` → attach action | JAIL+tmux | `chat_command.go`, `attach_e2e_test.go` | |
| `chat inject` ladder: self → any-socket live session → label → Codex thread → id/path/excerpt | JAIL+tmux | `headless_command.go`, `inject_resume.go`, `internal/inject/resolve` | B1 |
| busy Codex and Claude inject through the safe composer queue without Esc; receipts distinguish queued from delivered | JAIL+tmux | `internal/inject`, `inject_cli_jail_test.go`, `engine_test.go` | |
| `chat inject` delivers an over-threshold ORDINARY body whole through bracketed paste; the `~/.local/state/pfm/inject-bodies/` auto-file store is now a RESCUE for an unproven paste, not the default, and names both facts in the receipt when it fires | JAIL+tmux | `internal/inject/body.go`, `engine_test.go`, `inject_cli_jail_test.go` | |
| a `chat inject --file` body of any size keeps bracketed paste for byte-safe multi-line input; the canonical auto-file store is a RESCUE for that body only if the paste cannot be proven | JAIL+tmux | `internal/inject`, `inject_cli_jail_test.go` | |
| `chat inject --file PATH TARGET` and compatibility `chat inject TARGET --file PATH` both deliver the file body; raw `--file PATH` is never message text | JAIL+tmux | `headless_command.go`, `inject_cli_jail_test.go` | |
| inline boundaries: one character under stays inline `SendLiteral`, one over goes through bracketed paste; the auto-file store no longer intercepts by size alone on the live path — see the edge-table derivation above | JAIL+tmux | `TestInjectPasteBoundaryAndKillerBody`, CLI probe fixture | |
| a 5 KiB prose body travels byte-exact through bracketed paste and prints pane proof (tail match or placeholder); `AUTO-FILE` fires only if that proof fails | JAIL+tmux | `engine_test.go`, `tmux_jail_test.go` | |
| repeated `chat inject --then` waits busy→stable-idle and survives caller exit | JAIL+tmux | `internal/inject/then.go`, waiter jail tests | |
| exactly ONE `Enter` confirms submission per attempt of the submit loop, idle or queued, Claude or Codex — the removed unconditional second `Enter` is what let an operator's own next keystroke land as a submitted message once the first `Enter` had already cleared the composer | JAIL | `TestSingleEnterConfirmsSubmitAcrossCodexAndBusyQueues` (`engine_test.go`) | |
| a human typing in the target composer refuses `chat inject` delivery outright — `Code 7`, `Status "typing"`, names the pane, the last-keystroke age, and `force_now`, types nothing; quiet under `TypistQuiet` (default 3s, `CHAT_INJECT_TYPIST_QUIET`) delivers; `force_now` is the only bypass, even over recent activity; a `ClientActivity` read failure aborts as `Code 6` "could not read who is at" and is never rendered as "typing" | JAIL | `inject/engine.go` (`ClientActivity` guard in `inject()`), `inject/tmux.go` (`CommandTmux.ClientActivity`); `TestInjectRefusesATypingHumanUnlessForced` (`engine_test.go`) | |
| `ClientActivity` against a REAL tmux server: an unattended session (no client ever attached) answers `ok=false, err=nil`; a dead socket answers a real `err`, never read as "unattended" | JAIL+tmux | `TestJailedClientActivityReportsUnattendedAndDeadSocket` (`tmux_jail_test.go`) | |
| the `--then` steer log is scoped by SOCKET as well as pane (`chat-then-<sanitized base(SocketPath)>.<sanitized Pane>.log`, each component sanitized SEPARATELY before joining on a `.` the sanitizer never emits); a bare pane-derived name collided across every chat sharing the fleet-standard `%0` pane, and joining before sanitizing let a hyphen inside either component alias with the join delimiter | JAIL | `inject/engine.go` (`steerLogPath`), `then_test.go`, `TestSteerLogPathScopedBySocketAsWellAsPane` (`engine_test.go`) | |
| a `--then` steer never lands over a typing human: the waiter polls `ClientActivity` until quiet, a tmux read failure counts as a failed poll (never as quiet), and exhausting the bound aborts the whole chain undelivered rather than forcing | JAIL+tmux | `inject/then.go` (`waitForQuietTypist`, `DeliverThen`); `TestDeliverThenHoldsForTypistThenDelivers`, `TestDeliverThenRefusesWhenTypistNeverClears` (`then_edge_test.go`) | |
| `pfm chat self-compact --then STEER <focus>` is the CLI twin of `chat_self_compact`, sharing `Engine.ScheduleSelfCompact` — one composition, one wait-for-the-caller's-own-turn contract; `--then` is required (`checkSteerChain` refuses any `/compact` primary carrying zero `Then` entries) and a second `--then` is a usage error (rc 2) | JAIL | `headless_command.go` (`runHeadlessSelfCompact`), `inject/engine.go` (`ScheduleSelfCompact`, `checkSteerChain`); `TestChatSelfCompactUsageErrors`, `TestChatSelfCompactNoFocusNamesItsOwnUsage`, `TestChatUsageListsSelfCompact` (`cmd/pfm/self_compact_cli_test.go`) | |
| `Engine.ScheduleSelfCompact` composes `/compact <focus>` for a Claude self target and the bare `/compact` for a Codex one, refuses an empty/multi-line focus with "focus must be one non-empty line" before scheduling, and forwards `then` unmodified | JAIL | `TestScheduleSelfCompactComposesPerEngineAndForwardsThen`, `TestScheduleSelfCompactRefusesAnInvalidFocusBeforeScheduling` (`then_edge_test.go`) | |
| `chat inject` refuses ANY `/compact` primary outright (with or without `--then`); a `/compact` steer is refused too — compaction is `pfm chat self-compact` only | JAIL | `TestInjectRefusesCompactPrimaryPointingToSelfCompact` (`engine_test.go`), `TestChatInjectRefusesCompactBeforeAnyResolveOrEngine` (`cmd/pfm/self_compact_cli_test.go`) | |
| a 2,147-rune `/compact` focus bypasses auto-file, is paced under one lock, fires byte-exact, and queues safely while busy — proven through `engine.inject` with `Chain: true` (`chat inject` itself no longer carries a `/compact` primary) | JAIL+tmux | `then_test.go`, `tmux_jail_test.go` | |
| `--force-now` interrupts only a busy live target and marks the forced delivery | JAIL+tmux | `internal/inject` | |
| signature: `/`-prefixed commands travel bare; plain text carries the sender identity | JAIL+tmux | `internal/inject` | |
| `chat ask` delivers, waits and prints only the answer; timeout remains rc 5 | JAIL+tmux | `ask_command.go`, `internal/headless/converse.go` | |
| `chat read`, `last`, `stream`, `status`, and `watch` preserve transcript/state semantics | JAIL | `headless_command.go`, `internal/headless`, `internal/transcript` | |
| `chat capture` resolves the target, requires a live pane, and prints full scrollback | JAIL+tmux | `chat_command.go` | |
| `chat name` sends `/rename`, then converges that exact pane's window in the same process | JAIL+tmux | `chat_command.go`, `chat_name_jail_test.go` | B4 |
| `chat kill` / `unkill` resolve names through the store; `self` shares the picker path | JAIL+tmux | `chat_command.go`, `kill_cli_engine_jail_test.go` | B2, B3 |
| `chat kill <id\|self>` on a live target also ends it (exit finisher); see § E for the shared `kill.Manager.Kill` flow rows covering all three doors and the reload `--hide` store-only exception | JAIL+tmux | `chat_command.go`, `kill_cli_engine_jail_test.go` | see § E |
| `chat end` kills only the resolved chat server | JAIL+tmux | `chat_command.go` | |
| `chat find`, `save`, `load`, `branch`, `ls`, and `history` are all native Go | JAIL | `chat_satellite_command.go`, `chat_satellite_command_test.go` | |
| `chat branch [name]` creates a real detached Claude/Codex fork on an immutable socket, preserves caller layout/focus, names Codex through its rename UI, defaults to `<parent>-branch`, and is explicitly reapable while untouched | JAIL+tmux | `branch_jail_test.go`, `internal/action/headless_fork_test.go`, `internal/reap/reap_test.go`, `reap_jail_test.go` | |
| a bare fleet launch execs its tmux client so harness exit also ends the owning terminal | JAIL+PTY | `shim/shim_test.go`, `internal/installer/assets/shim/pfm.zsh` | |
| `chat resolve <target>` prints immutable socket, tmux session and chat id | JAIL | `chat_command.go` | B1 |
| exit contract: 0 delivered/queued, 2 usage, 3 dead, 4 unknown, 5 answer timeout, 6 undelivered | JAIL | `headless_command.go`, `headless_matrix_test.go`, `inject_cli_jail_test.go` | |
| hidden root compatibility alias emits a deprecation; `run`, `dump`, and the old stream verb are gone | JAIL | `main.go`, `headless_matrix_test.go` | |
| cache-window status stays compact, labels every shown unit, and omits a zero-hour field (`💾5m✗21m10s`, but `💾5m✗1h55m0s`); no prose is added | UNIT | `internal/statusline/render.go`, `statusline_test.go` | live display regression |

### Measured composer edges — Claude Code 2.1.224 / Codex CLI 0.147.0

Measured 2026-08-16 against authentic TUIs in a scratch working directory, each on a fresh `probe-*` socket under `/tmp/tmux-1000/`. Every payload carried distinct head/tail markers. For submitted samples, the Claude transcript or Codex rollout was the byte-count oracle; pane capture supplied the composer symptom and `#{pane_dead}` proved whether the TUI survived. No fleet socket was addressed. A failure edge is the first size that collapses into a paste block and does not reach the transcript/rollout on one Enter; the panes stayed alive.

| engine | transport | last intact composer body | first failure | observed symptom |
| ------ | --------------------------- | ------------------------- | ------------- | -------------------------------------------------------------------- |
| Claude | literal `send-keys -l` | 1,024 chars | 1,025 chars | `[Pasted text #N]`; one Enter left the block in the composer |
| Claude | bracketed `paste-buffer -p` | 800 chars | 801 chars | `[Pasted text #N]`; one Enter left the block in the composer |
| Codex | literal `send-keys -l` | 1,000 chars | 1,001 chars | `[Pasted Content N chars]`; one Enter left the block in the composer |
| Codex | bracketed `paste-buffer -p` | 1,000 chars | 1,001 chars | `[Pasted Content N chars]`; one Enter left the block in the composer |

The per-engine inline boundary (`ClaudeInlineMax`/`CodexInlineMax`, `internal/inject/types.go`, renamed from `ClaudeAutoFileMax`/`CodexAutoFileMax`) uses the smaller transport edge and rounds down at 90%: Claude `floor(801 × 0.9) = 720` runes; Codex `floor(1001 × 0.9) = 900` runes. The comparison is against the complete signed wire message, so a signature consumes part of the safety margin.

On LIVE delivery this is the inline-`SendLiteral`-vs-bracketed-`SendPaste` boundary, not an inline-vs-file boundary: a message over it still reaches the pane whole, through tmux's bracketed paste (`load-buffer` + `paste-buffer -p`), proven by either a tail match in the capture or the composer's own collapsed-paste placeholder (`[Pasted text #N]` / `[Pasted Content N chars]`). The auto-file store (`~/.local/state/pfm/inject-bodies`, mode 0600, pruned past seven days) is now a RESCUE rather than the default: it fires only when a paste delivery cannot be proven, and the caller is told through both the receipt text and `Result.AutoFilePath` — never a silent swap. On the dormant/resume path (`PrepareForResume` — writing directly into a transcript, with no live composer for a paste transport to target) the boundary is still inline-vs-file, unchanged: an 8 KiB body there still becomes a byte-exact pointer rather than one giant synthetic user turn. Slash commands never become pointers either way: they travel byte-exact in locked 512-rune literal chunks, with Enter only after the final chunk — the 2,147-rune `/compact` fixture proves the complete command reaches the transcript and fires.

## I — zsh shell surface: launchers

The Go action policy owns fresh Claude launches. The binary executes selected actions on a terminal and preserves its one-line shell protocol when stdout is captured. The installed shim retains Codex and direct-harness terminal ownership, and unloads retired shell commands.

| Flow | Safety | Source |
| --- | --- | --- |
| Missing/non-executable PFM stops shim sourcing with a diagnostic | JAIL+sh | `shim/shim_test.go` |
| Source/re-source leaves no public `cc*` functions or aliases; external compiler remains | JAIL+sh | `shim/shim_test.go` |
| Fresh Claude account, cache, prompt, autonomy and quoting use native policy | JAIL | `internal/action/synth.go`, `internal/action/claude_spawn.go` |
| Captured output stays a shell line; terminal output executes the action | JAIL | `cmd/pfm/action_dispatch_test.go` |
| Auto-open runs once after shell startup; legacy profile values open the picker | JAIL+sh | `shim/shim_test.go` |
| `cx` creates its server detached through `pfm internal chat-server` (no tmux call of its own), then attaches; a failed creation stops `cx` with pfm's reason | JAIL+sh | `shim/chat_server_shim_test.go` |
| `_pfm_selfswitch` prevents nesting the same server | JAIL+tmux | `shim/shim_test.go` |
| `_pfm_own_terminal` preserves scripts/nested chats and closes owned terminals | JAIL+sh | `shim/shim_test.go` |

## J — Internal wiring and store

| flow | safety | expected behavior (source) | regression |
| ------------------------------------------------------------------------------------------------------------------ | ---------------- | ----------------------------------------------------------------------------------------------------- | ---------- |
| shared-store open is idempotent, initializes schema, enables WAL and sets a busy timeout | JAIL | `internal/shared/shared.go`, `internal/shared/shared_test.go` | |
| kill add/delete/lookup use SQLite exclusively and never recreate the retired carrier | JAIL | `internal/shared/shared.go`, `internal/shared/shared_test.go` | B2 |
| clear-kill baselines survive reads, expire after transcript growth, and cannot weaken a permanent kill | JAIL | `internal/shared/shared_test.go`, `internal/store/killed_test.go`, `internal/compose/compose_test.go` | |
| kill sync and orphan pruning replace only the intended set, in one transaction | JAIL | `internal/shared/shared.go`, `internal/store/killed_test.go` | |
| chat cache load/save/prune preserves tab-bearing prompt text and exact ids | JAIL | `internal/store`, `internal/store/queries_test.go` | |
| hidden `internal primary-get/set` validates the 1/2 roster and keeps its mirror coherent | JAIL | `cmd/pfm/main.go`, `internal/shared/shared_test.go`, `shim/shim_test.go` | |
| child add/list/clear distinguishes `new` servers from `pane` children | JAIL | `internal/shared/shared.go`, `internal/shared/shared_test.go` | |
| installer removes owned `/bb` cards and hook wiring, preserves unrelated files, and wires one clear hook | JAIL | `internal/installer/installer_test.go` | |
| hidden `internal agent-open` holds a per-id mutex; exactly one concurrent takeover wins | JAIL | `internal/agentopen/agentopen.go`, `agentopen_test.go` | |
| agent registry accepts `sessionId` or `id` prefixes across configured account roots | **REAL-SESSION** | `internal/agentopen/agentopen.go`, `agentopen_test.go` | B3 |
| agent routing attaches busy/tmux-resident work and takes over idle work | JAIL+tmux | `internal/agentopen`, `agentopen_test.go` | |
| pane-less agent routing returns one typed `running outside pfm` line with pid and parent command, without per-socket probe noise | JAIL | `internal/agentopen`, `agentopen_test.go`, `agent_open_command.go` | |
| a failed registry scan refuses a fresh resume instead of opening the same transcript twice | JAIL | `TestOpenRefusesFreshResumeWhenEveryRegistryQueryFails` | |
| `chat reload` parses account/cache-only forms and rejects a multi-pane server | JAIL+tmux | `reload_command.go`, `swap_jail_test.go` | |
| `chat reload` preserves current account for cache-only requests and fresh-boots without a transcript | JAIL+tmux | `internal/reload`, `reload_test.go` | |
| `chat reload` serializes by pane, refuses an open selector, and waits for a real prompt before `--then` | JAIL+tmux | `internal/reload`, `swap_jail_test.go`, `reload_test.go` | |
| `chat reload --then` refreshes the respawned pane PID and ignores processes that vanish during `/proc` enumeration | JAIL | `internal/reload/reload_test.go` | |
| the scheduler hands its detached `reload-run` worker an explicit `--sock`/`--pane` it already resolved (ambiently or from the caller's own `--sock`), so the worker never re-derives identity | JAIL+tmux | `reload_command.go:runChatReloadWithRuntime`, `reload_jail_test.go:TestChatReloadHandsTheWorkerAnExplicitSockAndPane` | |
| `reloadTarget` with `--sock`+`--pane` selects the named pane out of a multi-pane server directly (never falls through to `resolve.NewWhoami`), and reports the pane by name when it is no longer live | JAIL | `reload_command.go:reloadTarget`, `reload_command_test.go:TestReloadTargetWithPaneSelectsItAmongSeveralWithoutWhoami`, `TestReloadTargetWithPaneRejectsAPaneThatIsNotLive` | |
| `reloadTarget` with `--sock` alone (no `--pane`) keeps the pre-existing single-pane rule: one pane auto-selects, more than one is refused | JAIL | `reload_command.go:reloadTarget`, `reload_command_test.go:TestReloadTargetWithSockOnlyKeepsTheSinglePaneRule` | |
| `chat recover` resolves id or rollout, rebuilds normal/compacted output, and is idempotent | JAIL | `internal/recovery`, `recover_jail_test.go` | |

## K — Installer and systemd units

| flow | safety | expected behavior (source) | regression |
| ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------- | ----------------------------------------------------------------------------------------- | ---------- |
| bare `pfm install` previews the full plan, writes nothing, and ends with the exact `pfm install --yes` confirmation | JAIL | `install_command_test.go`, `internal/installer` | |
| `pfm install --yes` applies the same classification as the preview; an executing name-sync service refuses before writes with actionable rc 97 | JAIL | `install_command_test.go`, installer tests | |
| `pfm install --yes --skip-harvest` applies with `ProvisionHarvest=false` and prints exactly `harvestpy: skipped (blocked, not attempted)` without calling the provisioner | JAIL | `install_command_test.go`, `internal/installer/harvest_integration_test.go` | |
| bare `pfm install --skip-harvest` previews `harvestpy: would skip (blocked, not attempted)` and preserves the flag in its confirmation | JAIL | `install_command_test.go`, `internal/installer/harvest_integration_test.go` | |
| `pfm install --vscode` previews and selectively merges a PFM picker profile selected as the default terminal (a settings profile — never the extension's contributed `Professor`, which would make a window reload drop every restored terminal), AND the four terminal-persistence keys (`enablePersistentSessions` true, `persistentSessionReviveProcess` "never", `showExitAlert` false, `remote.autoForwardPorts` false) into VS Code JSONC; repeat installs are byte-idempotent, comments/foreign profiles survive, updates retain ownership, an owned `Professor` default (from the release that briefly selected it) moves back to `PFM` while any other operator override of an owned key is relinquished rather than fought back, and uninstall restores only owned fields | JAIL+e2e | `install_command_test.go`, `internal/installer/vscode_test.go`, `internal/installer/vscode_profile_migration_test.go`, `e2e/install_e2e_test.go` | |
| `pfm install --vscode` links the staged Professor extension into `extensions/professor` of every VS Code product root present (the portable root is `$VSCODE_PORTABLE` itself), never creating a product that is absent; the ledger records each link; an ordinary install over a pre-extension ledger links it too; repeat installs are idempotent; a pre-existing non-link target is backed up and restored on uninstall; uninstall removes only links still pointing at pfm's staged copy; the embedded `package.json` profile title is the extension's contributed profile and is never the default pfm writes | JAIL | `internal/installer/vscode_extension_test.go` | |
| the link alone is never loaded — `pfm install --vscode` also registers `professor.professor` in each product's own `extensions/extensions.json` (absent → `[]`; unparsable → visible skip, never rewritten; a foreign pre-existing entry survives byte-equal after re-encode; a same-id entry pointing elsewhere is left alone and named); uninstall removes only the entry whose `relativeLocation` names pfm's own link folder, never a foreign one sharing the index file; the canonical `PFM` profile carries `icon`/`color` and an icon-less owned shape upgrades to it, same relinquish rule as every other owned field; `pfm doctor` prints one `vscode` row per recorded link (link/index state, version) and one per owned settings file, and reports "not managed" without a warning when the ledger never existed | JAIL | `internal/installer/vscode_index_test.go` (`TestVSCodeExtensionIsRegisteredInEachProductsIndexNotOnlyLinked`, `TestVSCodeExtensionIndexUnreadableIsSkippedVisiblyAndTheLinkStillMade`, `TestVSCodeExtensionUninstallRemovesOnlyItsOwnIndexEntry`), `internal/installer/vscode_profile_test.go` (`TestVSCodeCanonicalProfileCarriesIconAndColourAndUpgradesTheIconlessShape`), `cmd/pfm/vscode_doctor_test.go` (`TestDoctorReportsEachVSCodeProductLinkAndIndexState`) | issue #24 findings 9a/9b/9c |
| `sanitizeJSONC`/`parseJSONCObject` tolerate a trailing comma before a closing `}`/`]` — including consecutive trailing commas — so a hand-malformed settings.json merges instead of hitting `errMalformedVSCodeSettings` and being skipped; a `setJSONCProperty` insertion never itself leaves a trailing comma before the object's close, so an object pfm actually writes into is valid strict JSON, not merely JSONC | JAIL | `internal/installer/jsonc.go` (`sanitizeJSONC`, `setJSONCProperty`), `internal/installer/vscode_test.go` (`TestVSCodeMergeToleratesTheRealMalformedTrailingCommaFile`, `TestVSCodeMergeWritesStrictJSONIntoTheMalformedProfilesObject`), `internal/installer/jsonc_test.go` (`TestSanitizeJSONCToleratesConsecutiveTrailingCommas`) | |
| `pfm install --yes` keeps provisioning failures loud instead of downgrading them to a skip | JAIL | `install_command_test.go` | |
| a `config.json.pre-split` byte-identical to the file the migration is about to park (legacy rename or a stray leftover) lets `pfm install --yes` proceed instead of aborting; a differing backup still refuses, now naming the byte-size mismatch | UNIT+JAIL | `internal/config/migration_test.go` (`TestApplyMigrationParksOverAnIdenticalPreSplitBackup`, `TestApplyMigrationStillRefusesADifferentPreSplitBackup`, `TestApplyMigrationParksAnIdenticalStrayLegacyCopy`), `install_command_test.go` (`TestInstallApplyContinuesPastAnIdenticalPreSplitBackup`) | |
| install dependency preflight uses the doctor registry; missing required deps refuse before installer writes | JAIL | `doctor_external_test.go`, `internal/deps/probe_test.go` | |
| harvestpy builds directly at its final digest path so uv absolute links never move; `INCOMPLETE` is reported explicitly, controlled failure removes the new tree, and repair restores quarantine | JAIL | `internal/harvestpy/harvestpy_test.go` | |
| `pfm uninstall [--config-dir DIR]` dispatches `ModeUninstall` and restores/removes installer-owned state | JAIL | `uninstall_command.go`, installer tests | |
| apply stages embedded assets, removes retired links, and leaves `~/.claude/bin` empty | JAIL | `internal/installer`, installer tests | |
| command cards, helpers, shim and units link to the managed asset tree | JAIL+sh | `internal/installer`, installer tests | |
| install stages the configured POSIX Claude launcher, records/restores the displaced native target, repairs updater displacement, and wires one SessionStart repair hook | JAIL+sh | `internal/installer/launcher_test.go`, `settings_wiring_test.go`, `e2e/install_e2e_test.go` | |
| real destinations are backed up; uninstall restores the newest backup | JAIL | `internal/installer`, installer tests | |
| zshrc and every physical Claude settings file converge; account symlinks dedupe, while clear-kill appears once | JAIL+sh | `TestEveryClaudeSettingsFileGetsCompleteHookWiring` | |
| legacy dream scripts migrate in their original events, dream hooks remain migrate-only, and hook siblings survive | JAIL | `TestDreamHookMigrationIsMigrateOnlyAndUninstallPreservesManualHooks` | |
| uninstall removes only ledger-owned hook occurrences and preserves matching commands that predated installer wiring | JAIL | `TestDreamHookMigrationIsMigrateOnlyAndUninstallPreservesManualHooks` | |
| immediate second apply reports `changed=0` for canonical and numbered-account settings | JAIL | installer idempotence tests | |
| systemd units and `.wants/` links converge without a manager; live transitions use only the injected manager | JAIL | installer idempotence and unit-transition tests | |
| `pfm-name-sync.path` fires on `~/.codex/session_index.jsonl` modification | **REAL-SESSION** | `systemd/pfm-name-sync.path` | **B4** |
| `pfm-name-sync.timer` 15-min drift fallback | JAIL+sh | `systemd/pfm-name-sync.timer` | |
| `nameSync.interval` renders into BOTH schedulers from ONE value — launchd `StartInterval` in whole seconds and systemd `OnUnitInactiveSec` as the duration — and a caller with no config falls back to the shipped 15m instead of a unit systemd refuses | JAIL | `internal/installer/namesync_schedule.go`, `internal/installer/namesync_schedule_test.go` (`TestNameSyncIntervalRendersIntoBothSchedulers`, `TestNameSyncIntervalFallsBackToTheShippedDefault`) | issue #14 F12 |
| `pfm install --yes` stages the systemd timer with the marker already rendered — an unrendered `OnUnitInactiveSec` is a unit systemd cannot parse | JAIL | `internal/installer/namesync_schedule_test.go` (`TestApplyStagesTheTimerWithTheConfiguredInterval`) | issue #14 F12 |
| `pfm internal chat-server` (the shim's door) fails CLOSED on the title only: an unreadable config still opens the chat, the host keeps its title, the reason is on stderr; a socket naming no engine or leaving the tmux dir is refused before tmux runs | JAIL+tmux | `cmd/pfm/chat_server_command.go`, `cmd/pfm/chat_server_command_test.go` | issue #14 F10 |
| `pfm-name-sync.service` `ExecStart` runs the BINARY, never a `.sh` | JAIL+sh | `systemd/pfm-name-sync.service` | |
| installer retires the carrier, old units, script links, statusline shell, segments and Python refreshers | JAIL | `internal/installer`, installer tests | |
| installer rewires Claude and Codex clear-kill, statusline, usage and dream hooks while preserving unrelated entries | JAIL | `internal/installer`, installer tests | |
| every user-owned JSON the installer rewrites (`~/.claude.json` / account registries, Claude `settings.json` hooks and memory-helper paths, Codex `hooks.json`) keeps its numbers exactly — an integer beyond float64 (2^53+1) survives install and uninstall — and a malformed or trailing-data file is refused as before | JAIL | `internal/installer/json_numbers.go` (`unmarshalKeepingNumbers`), `internal/installer/json_numbers_test.go` | |
| `pfm install` owns the `pfm-statusline` and `tmux-title-renudge` host overlays end to end: staged as embedded `assets/bin/*` (mode 0755), symlinked at `~/.local/bin/NAME` to the managed copy, idempotent replace of a wrong link or a stale regular file, and unwired cleanly on uninstall | JAIL | `internal/installer/installer_test.go` (`TestApplyIsSelfContainedIdempotentAndReversible`), `internal/installer/installer.go` (`wireHostOverlays`, `InspectHostOverlays`) | |
| `updateSettings` points a fresh or legacy `statusLine.command` (empty, `statusline-command.sh`, bare/absolute `pfm statusline`) at the `pfm-statusline` overlay; a genuinely custom command is preserved on apply AND uninstall | JAIL | `internal/installer/settings_wiring_test.go` (`TestStatusLineRewriteOwnsTheOverlayAndPreservesCustomCommands`) | issue #14 F1 |
| `pfm doctor` fails (not warns) when either host-overlay symlink is missing/displaced, or a configured account's `statusLine.command` still names the raw `pfm statusline` instead of the overlay | JAIL | `cmd/pfm/doctor.go` (`printHostOverlayDoctor`), `cmd/pfm/doctor_host_overlay_test.go` | issue #14 F1 |
| installer retires a renamed-away global Claude agent identity (`agents/frr.md`, the `frr`→`rr` rename) only when it is a symlink or its frontmatter `name:` still reads the retired name — never a same-named user-authored agent | JAIL | `internal/installer/installer.go` (`retireRenamedGlobalAgents`), `internal/installer/installer_test.go` | issue #14 F5 |
| `reloadLaunchAgentWithLabel` skips bootout+bootstrap entirely when a job is already loaded and its plist did not change (both `launchdLabel` and `mcpLaunchdLabel`), still reloads a loaded job whose plist DID change, still bootstraps a not-loaded job without ever issuing a bootout, retries `bootstrapWithRetry` through a teardown still in flight, and keeps the "service is now DOWN" wording distinct from the plain "not loaded" wording used when nothing was running to begin with | JAIL | `internal/installer/launchd.go` (`reloadLaunchAgentWithLabel`, `bootstrapWithRetry`), `internal/installer/launchd_reload_test.go` | `pfm install` stopped a running `pfm mcp serve` daemon |
| every pfm service unit (both launchd agents, both systemd services) takes its PATH from ONE `servicePath` (`~/.local/bin`, the Homebrew prefixes, the system dirs), rendered at install — none spells its own — launchd's bare PATH hid tmux: every chat tool a Codex chat called over `pfm mcp serve` answered as if no chat were live, and `pfm name-sync` planned "0 windows" and exited 0 on every run, so no rename reached a tmux window or a VS Code tab | JAIL | `internal/installer/launchd_test.go` (`TestMCPLaunchAgentGivesTheDaemonAPathThatFindsTmux`, `TestEveryServiceUnitTakesTheOneServicePath`, `TestNameSyncLaunchAgentGivesTheJobAPathThatFindsTmux`), `internal/installer/mcp_wiring_test.go` | Codex chats could not message any chat; renames never reached windows |
| a tmux that cannot START fails the whole socket probe and every name resolution with the cause named — never "no chats" or "matched no live chat"; a tmux that ran and failed on one server stays a per-socket warning / a plain miss | JAIL | `internal/tmux/tmux.go` (`CouldNotRun`), `internal/gather/tmuxprobe_test.go`, `internal/resolve/resolve_test.go` (`TestResolveFailsLoudWhenTmuxCannotRun`, `TestResolveStillMissesPastADeadSocket`) | Codex chats could not message any chat |
| `pfm mcp serve` exits 75 when its own binary is replaced (renamed or copied over), after in-flight calls finish, so its supervisor restarts it on the new build; a vanished binary is reported once and never restarted onto; a plain close exits 0 | JAIL | `internal/binwatch/binwatch_test.go` | the daemon served a two-day-old build through installs |
| fenced e2e install/update/uninstall requires `PFM_DEV_FENCE=1`, stages the source plus every HOME/state path under `t.TempDir()`, uses `--skip-harvest`, and asserts the exact skip line | JAIL | `e2e/install_e2e_test.go`, `scripts/e2e-linux.sh`, `.github/workflows/install-verify.yml` | |

## L — Dream runtime resources

| flow | safety | expected behavior (source) | regression |
| -------------------------------------------------------------------------------------------------------------------------- | ------ | -------------------------------------------------------------------------------------------------------------------- | ---------- |
| a night with `HOME` and `PFM_HOME` isolated from the source tree resolves both prompts and the tracer lane from the binary | JAIL | `internal/dream/dream_test.go:TestNightRunsWithEmbeddedResourcesAndNoProfessorHome`, `prompts/embed.go` | defect RED |
| `--resources` beats organ-local content, organ-local beats embedded, and a partial overlay falls through per file | JAIL | `internal/dream/resources/resources_test.go:TestResourcesLayerPerFileAndPreservePriority` | |
| lane enumeration merges every layer by sorted entry name and a same-named override appears once | JAIL | `internal/dream/resources/resources_test.go:TestResourcesReadDirMergesAndFirstDeclarationWins` | |
| missing override roots fall through; real disk errors and symlink resources fail closed | JAIL | `internal/dream/resources/resources_test.go` | |
| embedded prompt and lane bytes match the four moved source files exactly | JAIL | `prompts/embed_test.go:TestDreamerEmbeddedFilesMatchMovedBytes` | |
| default repo is the current Git top level; discovery failure names `--repo ROOT` | JAIL | `internal/dream/organ/organ_test.go`, `cmd/pfm/dream_command_test.go` | |
| `dream morning` reads `--repos` / XDG config and a missing list names its path plus line format | JAIL | `internal/dream/morning_test.go:TestMorningMissingRepositoryListNamesPathAndFormat`, `cmd/pfm/dream_command_test.go` | |

---

## Flows that CANNOT be jailed

Schedule these deliberately on a scratch project directory. Rows tagged `REAL-SESSION` are here verbatim; the rest are `JAIL+tmux` rows whose jail proves only the mechanics (synthetic screen text, fake `/proc`) and which need one authentic engine run before they count as covered.

**Needs a real `codex` process:**

1. live-codex store-only **resumed** thread — identity, name, window (`resolve/codex.go:49-85`).
2. live-codex store-only **fresh** thread that exports `CODEX_THREAD_ID`.
3. Codex writing a rollout and immediately closing it (the fd-scan blind spot).
4. Codex rename inside the TUI → `threads.name` update.
5. Codex rename landing only in `session_index.jsonl` (**B4**).
6. `codex resume <name>` creating a paginated thread that writes no rollout.
7. Two codex chats born in the SAME directory within the birth window.
8. Codex approval / plan-overlay modals during an inject.
9. ✅ DONE — Codex composer edge measured for literal and bracketed-paste transport; the auto-file boundary fixtures cover the false-green class. Re-run on any Codex upgrade.
10. `pfm chat kill self --exit` `/quit` flush on a REAL codex seat.
11. `pfm name-sync` converging a live cx window name onto the VS Code tab.
12. Codex-origin `pfm chat inject` signature via ancestry recovery.
13. `chat_ls` state=`busy` for a genuinely generating codex pane.
14. `cx` launcher self-switch when already inside its own server.
15. ✅ DONE — `pfm chat new --engine cx --name X` against the REAL codex TUI (0.147): confirmed live end to end. It found three things no stub had: the TUI boots into a hooks/trust modal that swallows keystrokes, its modal selection cursor is the SAME glyph as the composer (so readiness needs the status line too), and the rename field arrives pre-filled. All three are fixtured now. Re-run this experiment on any codex upgrade.
16. ✅ DONE — the two-way surface against BOTH real engines: `chat new --await` (inline and `--prompt-file`, multi-line), three-turn `ask` conversations on a codex and a claude seat, `--json`, `--progress`, a `--timeout` that expires with the question still in the record, and the read verbs over the same live chats. 22 checks, all green. Re-run on any engine upgrade — `ask` is only as true as the transcript shapes both engines write.
17. ✅ DONE — Codex 0.147 `/clear` emits no immediate hook; the first prompt in the fresh chat emits `SessionStart(source=clear)` with the new payload session id and the live tmux pane. The inherited `CODEX_THREAD_ID` can still name an older thread, so clear-kill must use the payload and the pane binding. Re-run this experiment on any Codex upgrade.
18. A chat spawned through `pfm-mcp.service` survives `systemctl --user restart pfm-mcp.service`; the jail proves the scope argv and loud missing-helper refusal, but only a real user manager can prove cgroup survival.

**Needs a real `claude` process:** 16. Agent row: a real non-primary-config-dir claude with `--session-id`. 17. Agent takeover through hidden `pfm internal agent-open` (`claude agents --json`). 18. The daemon-agent guard on the resume-inject path. 19. ✅ DONE — `/clear` emits `SessionEnd(reason=clear)` then `SessionStart(source=clear)` while `/exit` emits `SessionEnd(reason=prompt_input_exit)`; re-run on any Claude Code upgrade. 20. Open gate: a live chat whose birth account ≠ primary. 21. `pfm chat reload` full reboot-in-place (`respawn-pane -k`) + `--then` delivery. 22. Trust-prompt handling on a fresh config-dir/cwd pair. 23. ✅ DONE — `/chat:branch` authentic `--fork-session` starts idle on its own detached server with the parent model and no caller-pane mutation. 24. `pfm chat new NAME` teammate spawn and immutable socket identity. 25. `⚡1h` badge read from a live process's `/proc` environ. 26. ✅ DONE — `[Pasted text #N]` collapse measured for literal and bracketed-paste transport in a real Claude composer; auto-file fixtures cover the edge. Re-run on any Claude Code upgrade. 27. Dim-SGR placeholder vs a real draft (mash guard). 28. Selector/permission modal handling. 28b. `pfm reap`'s busy probe against REAL `claude agents --json`; the jail proves fail-closed plumbing but not the live daemon's JSON shape, so re-run it on any Claude Code upgrade. 28c. ✅ DONE — Ctrl+S composer-stash semantics against a REAL claude session (`internal/inject/claude_stash_real_test.go`, `TestRealClaudeStashSemantics`, skipped unless `PFM_REAL_CLAUDE=1`; v2.1.257, 2026-09-03): typed draft + C-s empties the composer; C-s on an empty composer pops it back; a `/help` command submit AND (`PFM_REAL_CLAUDE_TURN=1`) a real message submit both restore the stashed draft (the latter also prints an explicit "Draft restored" UI hint, seen only on that async-turn-boundary restore, not on the synchronous pops); stashing a second draft while one is already parked OVERWRITES it — last-write-wins, one slot, no warning. The observed semantics are pinned as a comment block at the top of the file and referenced from engine.go's stash-guard comment. Re-run on any Claude Code upgrade.

**Needs real multi-account state:** 31. Transcripts under a SEPARATE account root (not a symlink back to account 1). 32. Statusline badge computation across accounts.

**Needs a real self-update across the v0.74.0 config migration (issue #24 findings 3/4):** 33. A rollback that crosses the migration boundary on a genuinely pre-split host (`config.json` only, real accounts, MCP servers actually enabled) — `iso e2e` proves the mechanics post-split only (`TestInstallInitUpdateUninstallE2E` "update" starts at the previous release, already post-split); the fenced rehearsal is: in an `iso shell`, install v0.73.x from source, run `pfm update --to <this tag> --repo <stage whose candidate's doctor is forced to fail by a broken required dep>`, and assert the MCP launch agent plist and `config.json` are both restored.

**Needs a real self-update rolling back from a ≤0.76.0 host (issue #24 finding 2):** 34. A rollback to a genuinely pre-M4 pfm — the jail proves `unknownPFMHookCommand` strips/reports the entry once THIS binary runs `pfm install --yes` or `pfm doctor`, but the OLD binary rollback restores is never rebuilt here, so nothing jailed proves the two hooks actually strand a real ≤0.76.0 install; the fenced rehearsal is: in an `iso shell`, install v0.76.0 (or older) from source, force a `pfm update` rollback (candidate doctor made to fail), confirm `pfm internal exit-close`/`pfm internal exit-intercept` survive in the account `settings.json` on the restored OLD binary, and that the release note's hand-removal steps clear them before a chat is started.

**Needs a real host whose shell exports `CLAUDE_CONFIG_DIR` (issue #24 finding 5):** 35. `pfm install --yes` then `pfm doctor | grep 'mcp client='` on a genuine host that exports `CLAUDE_CONFIG_DIR` — the jail proves the resolver, writer, and doctor row against fixtures, but nothing jailed proves a REAL `claude` process actually reads `<CLAUDE_CONFIG_DIR>/.claude.json` rather than `$HOME/.claude.json`, which is the reporter's own inode/mtime evidence, not something Claude Code's docs confirm for this file; confirm every registry row carries a reason and that `claude mcp list`, run inside a chat the `claude` launcher shim started under that exported var, lists both `chat` and `harvester`.

**Needs a real, OAuth/subscription-logged-in `claude` process (issue #24 finding 6):** 36. `pfm doctor --verbose` on a genuine OAuth-logged-in macOS host — the jail proves the throwaway-`CLAUDE_CONFIG_DIR` plumbing and the CANNOT CAPTURE / evidence-file wiring against a fake CLI, but nothing jailed proves a REAL Claude Code build actually treats a fresh `CLAUDE_CONFIG_DIR` as logged out and routes to `ANTHROPIC_BASE_URL` instead of a cached OAuth session; confirm `harness-prompt` reads `matches baseline` (or a named `DRIFT`) rather than the old `CHECK FAILED to run (… timed out …)`, and re-run on any Claude Code upgrade — a CLI regression here reads `CANNOT CAPTURE` instead of silently reverting to the timeout.

**Needs a real host with several live Claude chats running an older `versions/` build (issue #24 finding 8):** 37. `pfm install` (preview) against a genuine `~/.local/share/claude/versions/` several builds deep, with real `claude` chats attached to a build that is not the newest — the jail proves the plan and protection logic against fake process tables (`gather.ProcImage` fixtures), but nothing jailed proves `lsof`/`/proc` actually resolve a REAL claude process's executing image back to its version file on this host; confirm the preview's `remove`/`keep` lines match reality — on the reporting host this was 6 versions retained (1.3 GB), 4 live chats on a build that was not the newest, and a plan that removed 3 while naming every live pid it protected.

**Needs a real VS Code product loading its own extension index (issue #24 finding 9a/9b):** 38. `pfm install --yes --vscode` on a genuine macOS host with VS Code Insiders present, then a COLD Insiders launch (not a reload) — the jail proves `registerVSCodeExtension` writes and re-verifies the `identifier.id=professor.professor` entry against fixture indexes, but nothing jailed proves a REAL VS Code build actually scans `extensions.json` and loads the linked folder from it rather than the directory walk 9a showed it skips; confirm `code-insiders --list-extensions | grep professor` lists it and `pfm doctor | grep 'vscode product'` reports `index=registered` for every linked product.

**Needs a real VS Code terminal-rendering host (issue #24 findings 10/11/12):** 39. The operator matrix on the reporter's VS Code version — press the keybinding (`ctrl+shift+alt+t` / `cmd+shift+alt+t`), run **Professor: New Chat Terminal** from the command palette, and pick **Professor** from the terminal `+` dropdown, with a window reload between each round — the jail proves the command body delegates through `workbench.action.terminal.newWithProfile` and the keybinding is contributed, but nothing jailed proves VS Code actually RENDERS the resulting tab with the cycling icon rather than the default profile's `mortar-board`, since the extension API only echoes back the `creationOptions` it was passed, never the rendered tab; confirm all three surfaces produce a tab whose icon differs from the default profile's.

---

## Residual known divergences

1. **Store-only rows carry no parent link** (`index/codexstate.go`) — a live-state audit found no real instance because Codex ≥0.146 resume continues the same thread id. Re-open if Codex changes that behavior; detect it by grouping same-cwd rows with the same first message.
2. **`mcpserv/backend.go:303-308` counts `Agent` as live** while `ui/model.go:467-471` does not. An agent row is capture-probed for busy state in MCP and treated as non-live in the TUI.
3. **Stats `TOKENS` is transcript traffic, not cost.** Claude assistant records add `cache_read_input_tokens` on every turn, so a long chat repeatedly counts its cached prefix. The open product question is whether this total should remain traffic or become a billed-cost metric; the live-rate change deliberately leaves the total unchanged.
4. **`lerpRGB` and `luma` still contract on an FMA architecture** (`ui/cosmoscanvas.go:20-22,37` remain the only fused-multiply-add sites in the package's arm64 assembly). Measured, not assumed: re-rendering every golden with EVERY fusable expression in `ui/cosmos.go` and `ui/cosmoscanvas.go` contracted the way arm64's rules contract them moved exactly one byte, and it was the Bezier's. Colour reaches the frame through `quantRGB`'s 8-step quantisation, which absorbs a 1-ULP difference everywhere the current fixtures land. Re-open if a golden goes red on an FMA host at a colour escape rather than a glyph.

---

## Highest-risk joins

Ranked by identity resolution across resume/store/fork edges, kill-store integrity, and live-vs-indexed disagreement.

| # | flow | why it catches bugs |
| --- | ------------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 1 | **Live codex → indexed row join** (`compose/compose.go:492-513`) | The join walks path → resolver ThreadID (`liveCodexID`); a process neither resolves is still a guess. Every Codex row bug passes through here. |
| 2 | **Store-only thread lineage** (`index/codexstate.go:98-130`) | A resumed store-only thread is given itself as its lineage root, so it can never merge with the conversation it continues. Kills, names and prompt counts all split. |
| 3 | **Codex name precedence across two writers** (`index/index.go`, `naming/naming.go`) | Name provenance (`cx_names.source`/`renamed_at`, schema v4) settles store-vs-file, but three walkers still differ on lineage breadth, and `reloadCxNames` still wipes and re-folds in two transactions. |
| 4 | **Agent-row identity** (`gather/agents.go:12-69` → `compose/compose.go:541-590` → `kill/manager.go:138-165`) | An agent row is the only kind whose ID can exist with no index row behind it. Every id-keyed operation must vouch an engine for it; a new call site that forgets recreates the silent-refusal bug. |
| 5 | **Codex thread ↔ pane pairing** (`resolve/codex.go:49-85`, `gather/codexproc.go`) | The argv `resume <uuid>` rung leads and the ±120s window only backstops fresh threads — but a resumed thread with a scrubbed argv still falls to the window. |
| 6 | **Live-vs-indexed refresh race in the picker** (`pipeline.go:315-424`, `ui/model.go:247-271`) | Three snapshots stream into an open TUI while the user is toggling kills. A row that changes identity between frames takes the pending kill with it. |
| 7 | **`collapseLiveServers` + `ServerCount`** (`compose/compose.go:759-803`) | Silently merges rows by `engine+ID`. Two genuinely different chats that resolve to the same id (see #1) disappear into one row; the loser is unreachable. |
| 8 | **`--exit` finisher choreography** (`kill/finisher.go:94-245`) | Detached, delayed, and it re-asserts a kill AFTER an index refresh. It races the engine's own transcript flush and reaps teammates by an id that may already have been canonicalized. |
| 9 | **Primary-account round trip** (`pipeline.go:553-631`, `shared/shared.go:441-482`, `shim/pfm.zsh`) | The store and launcher must enforce the same account roster. |
