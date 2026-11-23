# pfm (Professor-Fleet-Manager) TESTPLAN — tiers, the fake engine's reach and the known gaps

What the tests beside the code do not say for themselves: the tokens a test's tier is named by, the composer transport boundaries, the real-Claude behaviours the fake Claude plays, the flows no suite plays yet, the known divergences, the highest-risk joins, the activity log's proofs, the shared seams and the host fixtures. A flow's coverage is read from its tests; this file holds no per-flow rows.

Go paths are relative to `~/.professor/pfm/` (the engine lives at the repo root — it is a program, not a template); embedded helper paths live under `internal/installer/assets/`. Absolute paths are absolute.

## Legend — the SAFETY tokens

| Token | Meaning |
| ------------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `JAIL` | Fully exercisable in a path jail: `internal/paths/env.go` overrides (`PFM_HOME`, `PFM_CONFIG`, `PFM_STATE_DB`, `PFM_CACHE_DB`, `PFM_SID_DIR`, `PFM_CLAUDE_ROOTS`, `PFM_CODEX_ROOT`, `PFM_OPENCODE_ROOT`, `PFM_TMUX_DIR`, `PFM_PROC_ROOT`, `PFM_MANAGED_SETTINGS_DIR`) plus the test knobs `PFM_TEST_NOW_NS` (`internal/fleet/scan.go`), `PFM_TEST_FRESH_SOCKET` (`internal/spawn/socket.go`), and `PFM_TEST_KILL_CONFIRM_EVERY_MS` (`internal/kill/manager.go`). Reference harness: `pfm/testdata/e2e.sh`. |
| `JAIL+tmux` | Needs a real tmux **server on a scratch socket inside the jail's `TMUX_TMPDIR`** — never a live fleet socket. Reference harness: `internal/*/tmux_jail_test.go` (e.g. `internal/kill/tmux_jail_test.go`). |
| `JAIL+sh` | zsh/bash fixture with a fake `$HOME`, scratch db and an EMPTY socket dir. References: `internal/installer/shim/shim_jail_test.go` and `internal/installer/installer_test.go`. Window-name convergence is covered by the Go `probe-*` tmux jails. |
| `LIVE-READ` | Read-only observation of live state (`pfm ls --tsv`, `--plain`, `--killed`, `doctor`, `sqlite3 -readonly`). Never mutates. |
| `CI` | Runs only on a GitHub Actions runner (`.github/workflows/ci.yml`), never in the fence: macOS, or a fresh machine whose HOME the run owns. Proven only on a pull request into `main`, or by a hand-started `ci.yml` run on any branch (`-f exclude=` skips jobs). |
| **`UNPLAYED`** | A named gap: the fake engine does not yet play this flow. No suite runs it against a real engine. |

## Legend — the regression tags

The four identity/state regressions that established this plan. A flow tagged with one must retain a fixture.

- **B1** — resumed store-only codex thread renders the wrong name.
- **B2** — workflow twin threads: a kill resurrects as a doppelgänger row.
- **B3** — an agent-row kill is a no-op.
- **B4** — a rename that lands only in `~/.codex/session_index.jsonl` never reaches the picker.

---

## Table of contents

1. [Composer transport boundaries](#composer-transport-boundaries)
2. [Real-Claude behaviours the fake Claude plays](#real-claude-behaviours-the-fake-claude-plays)
3. [Flows the fake engine does not yet play](#flows-the-fake-engine-does-not-yet-play)
4. [Residual known divergences](#residual-known-divergences)
5. [Highest-risk joins](#highest-risk-joins)
6. [Activity log](#activity-log)
7. [Seams](#seams)
8. [Host fixtures](#host-fixtures)

---

## Composer transport boundaries

The current boundaries are `ClaudeInlineMax=720` and `CodexInlineMax=900` in `internal/call/types.go`. Fixture tests check pfm's transport choice and delivery receipts; actual engine composer thresholds remain UNPLAYED. An engine-side probe uses a scratch working directory and a fresh `probe-*` tmux socket, sends payloads with distinct head/tail markers, compares the transcript or rollout with pane capture, and checks `#{pane_dead}`. A failure edge is the first payload that collapses into a paste block and fails to reach the transcript or rollout on one Enter.

| engine | transport | boundary input | next size | composer symptom to probe |
| ------ | --------------------------- | ------------------------- | ------------- | -------------------------------------------------------------------- |
| Claude | literal `send-keys -l` | 1,024 chars | 1,025 chars | `[Pasted text #N]`; one Enter left the block in the composer |
| Claude | bracketed `paste-buffer -p` | 800 chars | 801 chars | `[Pasted text #N]`; one Enter left the block in the composer |
| Codex | literal `send-keys -l` | 1,000 chars | 1,001 chars | `[Pasted Content N chars]`; one Enter left the block in the composer |
| Codex | bracketed `paste-buffer -p` | 1,000 chars | 1,001 chars | `[Pasted Content N chars]`; one Enter left the block in the composer |

The per-engine inline boundary uses the smaller transport edge and rounds down at 90%: Claude `floor(801 × 0.9) = 720` runes; Codex `floor(1001 × 0.9) = 900` runes. The comparison is against the complete signed wire message, so a signature consumes part of the safety margin.

On live delivery this is the inline-`SendLiteral`-vs-bracketed-`SendPaste` boundary: a message over it reaches the pane through tmux's bracketed paste (`load-buffer` + `paste-buffer -p`), checked by a tail match in the capture or the fixture composer's collapsed-paste placeholder (`[Pasted text #N]` / `[Pasted Content N chars]`). The auto-file store (`~/.local/state/pfm/call-bodies`, mode 0600, pruned past seven days) is a rescue when paste delivery cannot be proven, reported through the receipt and `Result.AutoFilePath`. On the dormant/resume path (`PrepareForResume` writes into a transcript, with no live composer) the boundary remains inline-vs-file: an 8 KiB body becomes a byte-exact pointer. Slash commands travel in locked 512-rune literal chunks, with Enter after the final chunk; the `/compact` fixture checks the complete command.

## Real-Claude behaviours the fake Claude plays

Each row is a Claude Code behaviour a past fix depended on, played by the fake Claude (`internal/mockengine/`), held by its unit test and asserted by the journey step named; every journey assertion was watched red once against a broken product statement, except where its row says otherwise.

| Behaviour | Fake Claude | Unit test | Journey step |
| --- | --- | --- | --- |
| A refused first turn: `api_error` turn end, headless exit 1 with the error text | `claude_turn_end.go` | `TestClaudeTurnEndsShortOfAReply`, `TestClaudeHeadlessAPIErrorExitsOneWithTheErrorText` | plain-pfm/chat-new; herdr-native/claude-typed (native seat paints error) |
| The automatic AI title written after a turn | `claude_turn_end.go` | `TestClaudeAITitleStepNamesTheChatInPfmsIndex` | herdr-native/claude-typed (red against the index read of the title; the title arriving before the tab's first paint is not reproduced: the tab has painted by then, and the step stays green with that fix reverted) |
| Esc while working writes the interrupt record and fires no Stop | `claude_record.go` | `TestClaudeRecordKeysAreClaudeCodes` | herdr/…/turn (read entry) |
| The permission dialog: Esc or No denies with no hook; a hook denial opens no dialog | `claude_permission.go` | `TestClaudePermissionDialog`, `TestClaudePermissionDeniedByAHookOpensNoDialog` | herdr-attention/question |
| The agents panel holds the keyboard while background agents run: ← focuses, Esc releases | `agents.go` | `TestClaudeAgentsPanelHoldsTheKeyboard` | herdr-mesh/host (call into a chat with its panel open) |
| A Claude that dies at launch, and one held at the folder-trust dialog | `claude.go` | `TestClaudeLaunchCrashExitsBeforeAnyPrompt` | plain-pfm/chat-new |
| A promptless fork leaves no transcript until its first prompt | `claude_resume.go` | `TestClaudeForkOwnsTranscriptAndTitle` | herdr-mesh/host (fork listed and resolvable by name) |
| The `server/discover` probe before `initialize` on a stdio MCP server | `claude_mcp_discover.go` | `TestDiscoveringTransportProbesBeforeInitialize` | MCP steps (red unwatchable: go-sdk answers the probe itself) |
| The agent tool shell runs from Claude's zsh snapshot, not an interactive zsh | `claude_shell_snapshot.go` | `TestToolShellArgv` | guards/shims-in-tool-shell |
| `claude doctor` creates its config (`~/.claude/backups`) when probed logged out | `claude_config.go` | `TestClaudeDoctorStartsItsConfig` | install/…/doctor (state before and after) |
| Headless `-p` posts its system prompt to `ANTHROPIC_BASE_URL`, honouring the cwd's settings env; remote flags vary the prompt per config dir unless nonessential traffic is off | `claude_api.go` | `TestClaudeHeadlessAsksItsBaseURL`, `TestClaudeRemoteFlagsBucketPerConfigDir` | install/…/doctor (harness-prompt rows, both models one live hash) |
| A statusline run is aborted by the next tick and bounded at 5 s | `statusline.go` | `TestStatusRunBound` | plain-claude reload (red unwatchable: the inline render lands inside one tick in the fence) |
| Versioned builds under `~/.local/share/claude/versions/{build}` | `argv_engine.go` | `TestArgvEngine` | herdr-native reload-cli (recorded build pruned) |
| In-app `/resume {id}` switches the pane's session | `claude_resume.go` | `TestClaudeInAppResumeSwitchesThePane` | herdr/…/reload-new/resume-in-app |
| A background job: the conversation continues under a new session in a detached `claude bg-spare` daemon; the old transcript ends on `continued-in` | `claude_background_job.go` | `TestClaudeBackgroundJobContinuesUnderANewSession`, `TestServeBackgroundJobHoldsItsRecordWhileTheHostLives` | …/resume-in-app/background-job/{one-chat, schedule-follows, open-finds-seat} |

**UNPLAYED by the fake Claude:** an automatic title that lands before the native tab's first status paint (herdr-native/claude-typed writes its title after the tab's first paint); `/pfm:reload` in a chat served by a 2026-09 `bg-spare` with tmux ancestry (today's daemon is detached; one fake cannot be both); a launch whose fast child closes the last tmux pane before the waiter attaches (needs a pfm seam); Herdr's blocked state for a plugin's `AskUserQuestion` and a sub-agent's permission prompt (pfm sends no state on a pane Herdr sees Claude in, so the reporter path is unreachable); a task-notification turn is played by the fake (`TestClaudeGatedAgentsEndWithTheirOwnTurn`) but no journey step restores a chat with a loss note and then runs one.

## Flows the fake engine does not yet play

`UNPLAYED` names a gap, never a real-engine test tier. The fixture engine plays the journey suite (`pfm/journey/`, run by `infra/fence/journey/run.sh`); the flows below still lack the stated behavior or host condition. No suite and no CI job runs a real model.

**Herdr seat lane H (retired with the old live lane suite; the journey suite's Herdr branch now types through a real Herdr):** ran a pinned real Herdr 0.9.5 server (protocol 22) in the fence; every engine in it is a fake engine (the mock claude and codex), so nothing here is a real-provider run. Each row's behavior below was first failed by a product-door mutant (one changed statement in an isolated build of the product) and then passed in an unchanged-source control that closes with `EGRESS PASS`.

- Native seat birth: `H.birth` (real Herdr birth of a Claude seat), `H.seat-host` (canonical `claude` argv, foreground PID equals PGID, durable backend, socket, pane and session id exact, no new tmux server), `H.whoami`.
- Discovery: `H.discovery`, `H.status` and `H.watch` read the live seat; with an unreachable endpoint `H.unreachable-ls`, `H.unreachable-status` and `H.unreachable-watch` each exit nonzero visibly, never as absence or idle.
- Delivery: `H.tmux-to-tmux`, `H.tmux-to-herdr`, `H.herdr-to-tmux` and `H.herdr-to-herdr` deliver the signed initial message and a SAME-TARGET `--then` steer into the recipient's transcript; `H.codex-raw` confirms a Codex raw composer and the recipient transcript receives the marker; `H.busy-queued` shows a busy recipient queued visibly, not delivered, then received after its turn; `H.blocked` shows the Codex selection-menu refusal with draft and menu preserved.
- Seat controls: `H.keys` (literal text plus `Enter` in the transcript and an independently read pane), `H.capture`, `H.modal` (the real consumer committed the second choice after `Down` and `Enter`), `H.name`, `H.name-durable`, `H.name-pane` and `H.name-agent` (durable name, pane label and agent name each read from the real surface).
- Live open: `H.open-live` shows the target pane's focus move false to true with the same terminal and the same engine PID and start time, no relaunch; the product accepts Herdr's `agent_info` focus acknowledgement (`internal/chat/open_herdr.go`).
- Native reload: `H.reload` (same pane and terminal, new engine on model `sonnet`, `reload_pending` 1 to 0, same live session id) and `H.reload-failure` (the failure is visible on the worker CLI's stderr; the native notification UI is not asserted). Herdr gets `ctrl+s`, tmux keeps `C-s` (`internal/reload/command_herdr.go`).
- Lifecycle: `H.end`, `H.kill`, `H.unkill-kill`, `H.exit` and `H.unkill-exit` close or restore exactly the victim pane and its durable seat row (`kill --exit` for `H.exit`); the survivor's engine PID is unchanged.
- Unattended restoration: after a real server stop and restart with no driver typing, `H.open-restored` observes the process Herdr's restore executes, `pfm chat open --restored {SID}`, then the native engine resumed with that session id and the seat rebound live; `H.loss-status`, `H.loss-ls` and `H.loss-watch` show the producer-indexed loss note; `H.restore-owner` shows another real pane cannot take a restored session from its live owner; `H.loss-notification` shows the disabled-notification refusal visible, its diagnostic carrying no private prompt (`internal/chat/restored.go`, `internal/headless/watch.go`).

**Regular real-Herdr lifecycle control (retired with the old live lane suite):** the lane self-test built `pfm` and `cmd/mock-engine` in the Linux fence, starts pinned Herdr 0.9.5 (protocol 22) under a private HOME/state, and uses real `/proc` with a fake Claude process and no provider credentials. It checks birth's durable backend/socket/pane/session identity, the composer and idle status, an unsigned call recorded in the pfm transcript with the exact assistant reply in the real pane, and asynchronous `chat end` only after that pane, seat row and native child disappear. A missing socket must fail visibly without leaving a seat or creating a tmux fallback; server, socket, process and scratch cleanup are checked, including startup/failure cleanup. It ran in the lane-library self-test set of the templates tests and full gate, both gone with the lanes; the journey suite's Herdr branch replaces it. It covered only that lifecycle slice: the full H lane is not part of those gates, and its native-call composer assertion still fails; its other experimental controls and the open-live, reload and restoration probes are not certified by this test. Signed delivery, open-live, reload, restoration, `--then` and modal behavior, GUI behavior, and macOS/OpenCode behavior remain outside this control.

**Herdr manual walk (real server, fake providers):** separate from scripted H beats, the walk exercised automatic backend choice, managed launcher and agent-start birth, live picker focus, signed cross-backend calls and same-target steers, busy refusal and tmux queue receipt, raw Codex menu refusal and modal dismissal, naming, reload, end/kill/exit/unkill, restart loss notes and owner protection. Dead `open` was evaluated in a real shell and resumed the same SID; an interrupted pre-seat-host owner was reclaimed in another pane. A deliberately missing reload executable produced visible failed-seat diagnostics in ls/status/watch. The walk predates the same-pane open: a picker inside a Herdr pane now opens every pick in its own pane (new and resumable rows exec in place, a live native chat moves into the picker's tab, a live tmux chat attaches there; the `pfm chat new` on a native backend row above), which no real-picker walk has verified yet; a caller that does not front its pane still gets a new tab.

The walk found three regressions, each watched red before its fix: an engine handling PTY loss with exit 1 now yields seat-host interruption 129 only when the kernel reports terminal hangup, preserving its restoration recipe (`TestSeatHostPTY/hangup-exit-one`); status/watch validate the actual agent even after an answered transcript, and name the endpoint on pane failures (`TestHerdrStatusRequiresItsPaneAndNamesEndpoint`); public tmux-style key modifiers translate to Herdr's plus-style modifiers (`TestHerdrKeysCapabilities`). Ordinary startup/engine failures remain errors. A graceful EOF closed its native seat after the key fix; forced kill retained the interruption distinction and removed the killed seat.

**Native Claude detection coverage:** probed live on Herdr 0.9.5 with Claude Code 2.1.317: Herdr detects Claude by the pane's foreground process group and reads its state from the screen; where Herdr lists a foreground without Claude, its agent status reads `unknown` and `agent.get` answers `agent_not_found`, so pfm reports there under `pfm`; the journey suite plays only panes Herdr sees Claude in. `herdr integration install claude` (script v11) adds one `SessionStart` hook that sends only `pane.report_agent_session` under `herdr:claude`, writes `{CLAUDE_CONFIG_DIR}/hooks/herdr-agent-state.sh`, keeps a symlinked `settings.json` a symlink and is idempotent per config dir, while two config dirs sharing one `settings.json` get two entries; a state pfm had reported under `herdr:claude` held over Herdr's own screen reading for more than four hours after an Esc. The installer, doctor and cutover run against a fake `herdr` and a fake socket only.

**UNPLAYED — native Claude detection:** Herdr's own screen detection over an actual Claude background agent running for 60 s and real permission and `AskUserQuestion` dialogs, real-engine restore through Herdr's integration (across accounts the managed launcher resumes on the ambient or primary account, not the session's owner), the install cutover against a live Herdr, and real Codex/OpenCode screen behaviour. No real engine/account suite is permitted; historical fake-provider lane coverage below does not close these gaps.

**UNPLAYED — native Codex queued questions and subagent hooks:** the fake Codex fires its own thread's trusted `hooks.json` handlers at `SessionStart`, `UserPromptSubmit`, `PreToolUse`, `PostToolUse`, `Stop` and `SessionEnd` (`internal/mockengine/codex_fire.go`), never `PermissionRequest` or `Interrupt`, and its child rollouts fire none; it does not emit a child's `PermissionRequest`, `PostToolUse`, `Stop` or `SessionEnd`, or model `request_user_input_async` queued while the parent remains Working. `internal/hookentry/herdr_codex_test.go` exercises synthetic payloads for child isolation, missing-rollout reporting and blocking/async question transitions; it does not prove native hook emission or Herdr screen-detection arbitration. No real engine/account suite is permitted.

**Herdr seat gaps:**

- `herdr agent start` readiness is played by `H.agent-start` and a separate manual real-server walk: `agent_started` reports `interactive_ready=true` for the managed native launcher. `--then` sends ordered same-target steers, never an automatic transcript reply.
- **KNOWN DEFECT:** real OpenCode 1.14.30 was installed at image build and its TUI rendered in an offline Herdr walk. Raw call refuses its empty box composer (`Ask anything...`) with exit 6: the generic composer guard recognizes Claude/Codex markers, not this OpenCode shape. Delivery remains unproven; the repair needs placeholder-versus-draft checks against the real styled composer, not a guessed mock. Codex raw delivery is played by `H.codex-raw`, `H.busy-queued` and `H.blocked`.
- **MANUAL PROOF, not H beats:** terminal `IGNCR` discarded Enter while retaining typed raw text; call exited 6 with an explicit unconfirmed warning. A permission-style fake-engine prompt produced Herdr's native `agent_status=blocked`; status, ls, watch and call showed blocked/refusal, not idle/delivered.
- **UNPROVEN (named, not covered, not played):** lane H does not prove any of these five; the cases run only against a fake socket or a jailed PTY, or not at all.
  - Graceful versus forced close of a native seat: `H.end`, `H.kill` and `H.exit` prove the pane closes and the durable row follows, not which of the two close paths ran.
  - `open` of a dead seat: the birth-rule fallback is jail-tested (`TestOpenNativeDeadDetachedRevivesOnItsOwnHerdrHost`, `TestOpenNativeDeadEndpointUsesBirthRule`), never observed on a real Herdr.
  - The lost and failed display forms in `pfm chat ls`, `status` and `watch` after a real dead backend or a killed seat: only the unreachable-endpoint forms (`H.unreachable-ls`, `H.unreachable-status`, `H.unreachable-watch`) and the restored loss note (`H.loss-status`, `H.loss-ls`, `H.loss-watch`) are played.
  - Enabled notification privacy is now manually proven, but not an H beat: a real Herdr TUI received the fixed loss note with notifications enabled. A product mutant appending the private prompt marker failed the same screen assertion before the unchanged product passed. `H.loss-notification` itself still covers only disabled-notification refusal.
  - Stealing an interrupted pre-seat-host ownership: `H.restore-owner` proves a live owner cannot be taken, not the interrupted-owner case.
- Native CLI/MCP keys and capture adapters: jail `TestChatNativeKeysAndCaptureAdapters`; unit `TestHerdrResolvedAdapterFacades`; the real-pane effect of keys and capture is `H.keys` and `H.capture`.
- The macOS lifetime fix (#5236) has no containing release to test against, so doctor only warns. A raw `--then` turn that finishes between two snapshots is refused (fail-closed), not proved. Named residuals: two simultaneous `--restored` recoveries of one session can each start an engine before the seat host marks it live, and a degraded shared state store aborts a native `open` before its tmux path (the fleet scan has no per-seat fallback).

**Codex process and UI gaps:**

1. Store-only resumed thread identity, name and window (`resolve/codex.go`).
2. Store-only fresh thread exporting `CODEX_THREAD_ID`.
3. Rollout writer that closes its fd immediately after writing.
4. A TUI rename updating `threads.name`.
5. A rename landing only in `session_index.jsonl` (**B4**).
6. `codex resume <name>` creating a paginated thread with no rollout.
7. Two Codex chats born in the same directory within the birth window.
8. Approval and plan-overlay modals during call.
9. Actual Codex composer behavior at the literal and bracketed-paste boundary; auto-file fixtures cover pfm's side.
10. `pfm chat kill self --exit` on a real Codex pane (no typed `/quit`: the fallback kill-pane closes it).
11. `pfm name-sync` converging a cx window name onto a VS Code tab.
12. Codex-origin call signature via ancestry recovery.
13. `ls` reporting busy for a generating Codex pane.
14. `cx` launcher self-switch inside its own server.
15. Actual Codex TUI trust-modal and pre-filled rename behavior at chat birth; the mock covers pfm's launch path, not that UI.
16. Actual Claude and Codex transcript shapes for multi-turn `chat new --await`, `ask`, timeout and read; mock E1/E2 cover pfm's handling of their fixture transcripts.
17. Codex `/clear` event timing and payload session id.
18. A chat spawned through `pfm-mcp.service` surviving a real user-manager restart.

**Workbench engine gaps:**

1. **UNPLAYED** — Real Codex honouring the workbench `developer_instructions` and its mirror’s skills; fixtures prove argv and mirror artifacts.
2. **UNPLAYED** — Real OpenCode merging the workbench prompt's `instructions` entry and running the seat plugin's `experimental.chat.system.transform`; Node fixtures prove the plugin on OpenCode 1.18.18's system shape, Go fixtures the environment and staging.

**Claude process and UI gaps:**

16. Agent row for a non-primary config dir using `--session-id`.
17. Agent takeover through `pfm internal agent-open`.
18. The daemon-agent guard on the resume-call path.
19. Claude `/clear` and `/exit` hook event timing.
20. Open gate for a live chat whose birth account differs from primary.
21. Actual Claude reboot-in-place and `--then` delivery; E1 exercises pfm's side against the mock.
22. Trust prompt on a fresh config-dir and cwd pair.
23. Actual Claude `--fork-session` implementation; E1.22 covers pfm's branch path with the mock. The mock seeds the fork's transcript at launch, while Claude writes it at the fork's first prompt: `cmd/pfm/booting_row_jail_test.go` (`TestPromptlessForkListsAndResolvesByItsLaunchName`) plays that promptless shape.
24. `pfm chat new NAME` teammate spawn and immutable socket identity in actual Claude.
25. `⚡1h` badge read from an actual Claude launch record.
26. Actual Claude composer behavior at the literal and bracketed-paste boundary; auto-file fixtures cover pfm's side.
27. Dim-SGR placeholder versus a draft in actual Claude.
28. Selector and permission modals in actual Claude.

- **28b.** `pfm reap` busy probe against actual `claude agents --json` output.
- **28b2.** Actual `continued-in` transcript record and daemon-job session file shape: the fake Claude plays the shape pfm reads (`claude_background_job.go`); the real shape is not re-checked.
- **28c. UNPLAYED** — Claude Code's Ctrl+S composer-stash semantics that pfm's stash guard assumes: one slot, so a later stash overwrites the earlier draft; Ctrl+S on an empty composer pops the draft; any confirmed submit restores it. Fake-tmux unit tests cover pfm's side, and the fake composer (`internal/mockengine/tty.go`) plays the parked-draft restore; Claude Code's side is unproven.
- **29. UNPLAYED** — the `--settings` value reaching the Claude process and hook children: the retired lane E1.03 covered it on two fixture seats; no journey step plays it yet.
- **30. UNPLAYED** — cross-account `/pfm:reload` resuming the same transcript on a wired seat: the retired lane E1.03 covered it on two fixture seats; no journey step plays it yet.

**Account and host gaps:**

- **31.** Transcripts under a separate account root rather than a link to account 1.
- **32.** Statusline badge computation across actual accounts.
- **36.** OAuth Claude Code routing a throwaway `CLAUDE_CONFIG_DIR` to the local harness-prompt sink.
- **37.** Version cleanup recognizing the executing image of older, live Claude builds.
- **38.** VS Code Insiders loading the linked extension from its extension index on cold start.
- **39.** VS Code rendering the New Chat Terminal icon from the keybinding, palette and terminal dropdown.
- **40.** Real engine screens and records behind `blocked` and interrupted turns: an actual Claude or Codex permission dialog, question or modal on a seat whose transcript ends in a pending tool call (the pane rule reads the screen with `call.SelectorLine` and `ClaudeTrustDialog`), and an actual Codex `turn_aborted` rollout record and Claude `[Request interrupted by user…]` record from a real interrupted turn; the jail plays fixture shapes of each.
- **41.** `pfm internal schedule-fire` through real tmux: a live Claude or Codex chat receiving the schedule by session id under the setter's signature, and a dead one resumed detached on a fresh socket whose composer is awaited before the schedule is typed. The suite plays the wait, trust-dialog and failure logic against fake open / capture / call seams and the store, lock and CLI against the jail; no fixture engine renders a resumed composer that accepts the called line and proves it.
- **42.** The schedule timer under a real service manager: the suite renders and links the units and agent into a temp HOME against a fake runner; no fence runs a live systemd --user or launchd that ticks the timer and proves `schedule-fire` runs from it.
- **43.** The engine guard as the engines run it. Played for Codex: the fake Codex fires the `hooks.json` handlers pfm installs (`internal/installer/codex_hooks.go`) only while `config.toml`'s `hooks.state` trusts them at their current hash, as `pfm install` records it (`internal/mockengine/codex_fire.go` `loadCodexHooks`, `codex_hooks.go`); a trusted `PreToolUse` deny refuses the call before it runs, the pane draws the deny row and the rollout's `function_call_output` is the reason (`TestCodexFiresItsTrustedHooksAtEachBeat`). The journey's `install/day/guards/project/claude-from-picker/end/codex-in-project` types Codex turns whose `git commit` and `AGENTS.md` write `pfm internal engine-guard --engine codex` refuses, and whose `git status`, heredoc-written script and project script taking `$1` and calling its sibling by its own directory it lets run. Unplayed: real Codex's own rendering of a deny (the fake draws the shared deny row and records the bare reason), and the OpenCode engine guard, a plugin real OpenCode loads and the fake OpenCode cannot; its verdicts stay pinned at the unit tier (`internal/hookentry/engine_guard_test.go`).

---

## Residual known divergences

1. **Store-only rows carry no parent link** (`index/codexstate.go`). The fake engine covers current same-thread resume behavior; a distinct parent-link shape remains UNPLAYED. Detect it by grouping same-cwd rows with the same first message.
2. **`mcpserv/backend.go` counts `Agent` as live** while `ui/model.go` does not. An agent row is capture-probed for busy state in MCP and treated as non-live in the TUI.
3. **Stats `TOKENS` is transcript traffic, not cost.** Claude assistant records add `cache_read_input_tokens` on every turn, so a long chat repeatedly counts its cached prefix. The open product question is whether this total should remain traffic or become a billed-cost metric; the live-rate change deliberately leaves the total unchanged.
4. **`lerpRGB` and `luma` still contract on an FMA architecture** (`ui/cosmoscanvas.go` remain the only fused-multiply-add sites in the package's arm64 assembly). Measured, not assumed: re-rendering every golden with EVERY fusable expression in `ui/cosmos.go` and `ui/cosmoscanvas.go` contracted the way arm64's rules contract them moved exactly one byte, and it was the Bezier's. Colour reaches the frame through `quantRGB`'s 8-step quantisation, which absorbs a 1-ULP difference everywhere the current fixtures land. Re-open if a golden goes red on an FMA host at a colour escape rather than a glyph.

---

## Highest-risk joins

Ranked by identity resolution across resume/store/fork edges, kill-store integrity, and live-vs-indexed disagreement.

| # | flow | why it catches bugs |
| --- | ------------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 1 | **Live codex → indexed row join** (`compose/compose.go`) | The join walks path → resolver ThreadID (`liveCodexID`); a process neither resolves is still a guess. Every Codex row bug passes through here. |
| 2 | **Store-only thread lineage** (`index/codexstate.go`) | A resumed store-only thread is given itself as its lineage root, so it can never merge with the conversation it continues. Kills, names and prompt counts all split. |
| 3 | **Codex name precedence across two writers** (`index/index.go`, `naming/naming.go`) | Name provenance (`cx_names.source`/`renamed_at`, schema v4) settles store-vs-file, but three walkers still differ on lineage breadth, and `reloadCxNames` still wipes and re-folds in two transactions. |
| 4 | **Agent-row identity** (`gather/agents.go` → `compose/compose.go` → `kill/manager.go`) | An agent row is the only kind whose ID can exist with no index row behind it. Every id-keyed operation must vouch an engine for it; a new call site that forgets recreates the silent-refusal bug. |
| 5 | **Codex thread ↔ pane pairing** (`resolve/codex.go`, `gather/codexproc.go`) | The argv `resume <uuid>` rung leads and the ±120s window only backstops fresh threads — but a resumed thread with a scrubbed argv still falls to the window. |
| 6 | **Live-vs-indexed refresh race in the picker** (`internal/picker/pipeline.go`, `ui/model.go`) | Three snapshots stream into an open TUI while the user is toggling kills. A row that changes identity between frames takes the pending kill with it. |
| 7 | **`collapseLiveServers` + `ServerCount`** (`compose/compose.go`) | Silently merges rows by `engine+ID`. Two genuinely different chats that resolve to the same id (see #1) disappear into one row; the loser is unreachable. |
| 8 | **`--exit` finisher choreography** (`kill/finisher.go`, `Finisher.Run`) | Detached, delayed, and it re-asserts a kill AFTER an index refresh. It races the engine's own transcript flush and reaps teammates by an id that may already have been canonicalized. |
| 9 | **Primary-account round trip** (`internal/picker/pipeline.go`, `fleetdb/fleetdb.go`, `internal/installer/assets/shim/pfm.zsh`) | The store and launcher must enforce the same account roster. |

---

## Activity log

One structured destination for every corner of the engine. `internal/obs` is that destination: `obs.OpenLog` installs the process logger at `cmd/pfm` entry and brackets the verb with `cmd.start`/`cmd.exit`, `obs.Logger(ctx)` reads it, `obs.With(ctx, ...)` scopes it, `obs.Span(ctx, name)` times a unit of work, and `obs.Test(t)` hands a test the same handler the file gets. Part A is the foundation only — no existing `log.Printf` has moved yet; ratchet `C23-bare-log` (`.arch/bare-log.txt`, 88 call sites in 34 files) holds the line while part B migrates each corner.

| Concern | Where | Tier | Proof |
| --- | --- | --- | --- |
| One log root per pfm home, never mixed: the clone the install recorded (`{clone}/tmp/logs/pfm/`), else `<home>/.local/state/pfm/log/` (no record, any jail, or a clone that cannot take it) | `paths.LogRoot` | `JAIL` | `paths.TestLogRootFollowsTheRecordedClone`, `obs.TestOpenLogWritesUnderTheRecordedClone`, `obs.TestOpenLogWritesOneFilePerHome` |
| Segments of `log.maxMB`, rolled on the byte count one append leaves, no size check per record; the newest `log.keepFiles` × `log.maxMB` (80 MB) whole, older segments keep only WARN and ERROR in `pfm.kept/{day}.jsonl` | `obs` rotator (`rotate.go`, `retain.go`) | `JAIL` | `obs.TestRotatorRollsPastTheSegmentSize`, `obs.TestRetentionKeepsTheNewestKeepBytesWhole`, `obs.TestRetentionKeepsWarningsAndErrorsByteForByte`, `obs.TestRetentionFailureKeepsTheSegment`, `obs.TestRotatorWritersSharingAHomeKeepEveryRecordWhole`, `config.TestLogKeysFromFile` |
| `pfm install` moves the logs pfm wrote before the log root into it, once | `installer.moveLogsToRoot` | `JAIL` | `installer.TestMoveLogsToRootMovesTheStateFolderOnce` |
| Level per environment: `-alpha` → debug, release → info, `log.level`, then `PFM_LOG_LEVEL` | `obs.resolveLevel` | `JAIL` | `obs.TestOpenLogLevelPerEnvironment`, `obs.TestOpenLogReportsAnUnparsableLevelInsteadOfIgnoringIt` |
| `PFM_LOG=stderr` mirrors a foreground run | `obs.openDestination` | `JAIL` | `obs.TestOpenLogMirrorsToStderr` |
| Every record carries `ts level msg cmd pid version`, and `chat seat engine sock dur_ms err` where known | `obs.OpenLog`, `obs.With`, `obs.Span` | `JAIL` | `obs.TestOpenLogRecordsCarryEveryDeclaredField`, `obs.TestWithScopesAChatSeatEngineAndSocketOntoEveryRecord` |
| **Never a credential, a token, a prompt body or a transcript line** | `obs.Scrub` — an allow-list of field keys plus value scrubbing (`sk-`, `Bearer `, `oauth`, over 512 bytes) | `JAIL` | `obs.TestScrubRefusesCredentialsTokensPromptsAndTranscriptLines` |
| `pfm log [--since --level --chat --cmd --follow]` | `obs.ReadActivity`, `cmd/pfm/log_command.go` | `JAIL` | `obs.TestReadActivityFiltersBySinceLevelChatAndCmd, obs.TestReadActivityFollowReadsRecordsAppendedAfterItStarted`, `obs.TestReadActivityReadsTheKeptArchiveTheSegmentsAndTheCurrentInTimeOrder`, `cmd/pfm.TestLogVerbReadsWhatTheProcessWrote` |
| `pfm doctor` names the log path and its size | `doctor.printActivityLogDoctor` | `JAIL` | `doctor.TestPrintActivityLogDoctorNamesThePathAndSize`, `...SeparatesAbsenceFromAFailedLook`, `...WarnsOfAFallbackRoot` |
| Process entry brackets every verb | `cmd/pfm/main.go` `run()` | `JAIL` | `cmd/pfm.TestRunWritesCommandStartAndExit` |
| The ratchet itself | `scripts/arch-check.sh` C23 | `JAIL+sh` | `scripts/arch-check_test.sh` |

---

## Seams

The unit-test law: a unit under test has every dependency on the host, the account, an API or another process MOCKED at its seam. Four packages carry that seam for the whole module; the rule is the same in each — production code calls the real implementation, a test scripts the fake, and nothing outside these four packages talks to the bare host directly (ratchet C22, `pfm/scripts/arch-check.sh`, `pfm/.arch/host-doors.txt`).

| Package | Seam | Real | Fake |
| --- | --- | --- | --- |
| `internal/clock` | `Clock` (`Now`, `Sleep(ctx, d)`, `After`, `NewTimer`, `NewTicker`) | `clock.Real` — the wall clock | `clock.Fake` — deterministic; `Advance(d)` fires every due sleep/timer/ticker in due-time order, `Pending()` counts what is still registered, a `Sleep` blocks until `Advance` or `ctx` releases it |
| `internal/deps` | `Runner` (`Run(ctx, argv, opts) (RunResult, error)`, `LookPath`, `Start(ctx, argv, opts) (Process, error)` for a streaming or detached spawn; `StartOptions` owns ordinary stdin, stdin/stdout pipes, and optional process groups) | `deps.RealRunner` — `exec.CommandContext`; `Start(Detach: true)` runs `SysProcAttr{Setsid: true}` and releases the process right after Start; `KillGroup` targets the requested process group | `deps.FakeRunner` — scripted by argv prefix (longest match wins), `Calls()`/`Starts()` plus `LifecycleCalls()` ledgers, `ScriptInteractive` owns deterministic pipes, `ScriptStartOutput` plays a child's stdout, stderr and exit code at `Start`, and an unscripted streaming `Start` returns `UnscriptedError` |
| `internal/tmux` | the façade's command surface (`capture-pane`, `list-sessions`/`list-windows`/`list-panes` `-F`, `send-keys`, `display-message`, `respawn-pane`, `kill-server`) | `Command`/`Invocation` — unchanged | `tmux.Fake` — scripted by subcommand (`Script`) or subcommand+socket (`ScriptFor`), a call ledger (`Calls()`), an unscripted call is `ErrUnscripted`; the real façade is untouched |
| `internal/paths` | `Env` (`Get`, `Lookup`, `Home`, `Hostname`, `User`) | `paths.OSEnv` — `os.Getenv`/`LookupEnv`/`UserHomeDir`/`Hostname`/`user.Current`; `EnvOr`/`Home` are `EnvOrFrom`/`HomeFrom` over it | `paths.MapEnv` — an in-memory environment plus configurable `Home`/`Hostname`/`User` errors |
| `internal/store` | the opened `*store.Store` (cache plus shared state) handed to the code under test; the shared half alone a `*fleetdb.Store` | `store.Open`/`OpenContext` — both SQLite files the resolved paths name; `fleetdb.OpenSharedState` | `store.OpenMemory(options…)` — both databases in this process's memory through SQLite's own `memdb` VFS (`sqlitedb.OpenStoreMemory`, a `file:/pfm-memory-{n}?vfs=memdb` name a second handle shares), the same migrations and queries, no file, environment or home read; `fleetdb.OpenSharedStateMemory` the shared half alone; there `fleetdb.LockMigration` is a no-op and a seat-run recipe is refused, having no directory; in a test process (`testjail.Run` calls `sqlitedb.ServeTestMemory`) every path the door opens is served from such a memory database too, found through the marker line `pfm memory database {name}` the door writes at the path and held by one anchor connection until the `testjail` home that holds it is released (`sqlitedb/memory_jail_test.go`, `fleetdb.TestReadSeatsFindsTheSeatTheDoorServesFromMemory`); a test of the file itself (WAL, an older on-disk version, a backup's bytes, a corrupt file, another process) calls `testjail.DatabaseFiles` on its directory and keeps SQLite's file |
| B5a consumers (`doctor`, `installer`, `update`, `updatecheck`, `usagehook`, `mcpserv`, `binwatch`) | existing `Clock`, `Env`, `Runner`, and listener seams at package boundaries | production defaults (`clock.Real`, `paths.OSEnv`, `deps.RealRunner`, `net.Listen`) | package tests inject `clock.Fake`, `paths.MapEnv`, `deps.FakeRunner`/scripted Start, and listener functions; no behavior path constructs a second real Runner |
| B5b consumers (`action`, `archive`, `chat`, `config`, `fleet`, `headless`, `heal`, `picker`, `professor`, `resolve`, `store`) | `clock.Clock`, `paths.Env`, `deps.Runner`, and `atomicfile.Write` at package boundaries; direct Claude and headless process calls preserve streaming, stdin, cancellation, and process-group behavior | production defaults (`clock.Real`, `paths.OSEnv`, `deps.RealRunner`, `atomicfile`) | `action.TestClaudeSpawnCommandUsesRunnerStartBoundary`, `headless/run.TestRunUsesScriptedRunnerStartBoundary`, `headless/run.TestRunProcessWaitsForProcessAfterCancellation`, and `professor.TestStoreSHAWithRunnerUsesScriptedGit`; remaining package suites cover the existing clock/env/atomic behavior. |

Wave 3 batch B0 is additive only: the seams exist and are tested, but `installer.Options.Now/Sleep`, `kill.Dependencies.Now`, `reap.Dependencies.Now`, `picker.ActivityClock` and `statusline.Runtime.Now` are unchanged — later batches migrate each caller onto the shared seam. B3 migrates `kill.Dependencies.Now`, `stats.LimitsSampler.now`, `spawn`'s internal `sleep`/deadline doors, `stale`'s sweep wait and `statusline.Runtime.now` onto `clock.Real` as their fallback (the per-package `func() time.Time` field shape stays, only its default changes) and folds `kill.CommandSpawner`/`call.CommandThenSpawner`'s detach-and-release exec plumbing onto `deps.Runner.Start`; `installer.Options.Now/Sleep`, `reap.Dependencies.Now` and `picker.ActivityClock` remain for a later batch.

## Host fixtures

`internal/hostfixture` — the six host edge cases other packages jail against, each built on `testjail` + `paths.MapEnv` + `deps.FakeRunner` + `clock.Fake`, each with its own test proving the state it claims (`internal/hostfixture/doc.go` names all six in one line each):

| # | fixture | builds | first consumers |
| --- | --- | --- | --- |
| 1 | `NoHome` | `HOME`/`PFM_HOME` unset | `paths`, `installer`, `fleetdb`, `store` |
| 2 | `SymlinkedConfigDir` | `~/.claude` → a physical dir elsewhere in the jail | `paths.DevRepoGitDir`, `professor.storeSHA` |
| 4 | `NoTmux` | `deps.FakeRunner` returns ENOENT for tmux | `tmux`, `doctor`, `spawn`, `reload`, B5a doctor Runner probes |
| 7 | `OddPaths` | HOME sits under a directory with a space and a non-ASCII character | `paths`, `call` lock namespace, `fleet` scan, `archive` |
| 8 | `NoCreds` | the `.credentials.json` absent plus a Keychain (`security`) not-found script | `usagehook`, `doctor` seat rows and duplicate-login registry advisory, `headless`, `resolve` |
| 9 | `StaleArtifacts` | a dead tmux socket file, an exited process's pid file, a `pfm-cache.db-wal`, a leftover reload lock | `reap`, `stale`, `fleetdb`, `reload.InFlight`, `kill` |

`stats.NewSampler`'s Docker identity resolver is otherwise "production only" — its one caller wires a hardcoded `/var/run/docker.sock`, a daemon no fence has. `stats.NewSamplerWithDockerSocket` is the same construction with the socket path as a seam, so `TestNewSamplerWithDockerSocketWiresResolveDockerIdentities` proves that wiring end to end against a jailed unix socket instead of only testing `newDockerInspector` in isolation.
