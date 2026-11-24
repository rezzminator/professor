# The fake engine's reach

The fake engine stands in for Claude Code, Codex and OpenCode in every pfm suite; `mockengine.go` names what each file plays. This file holds what the code cannot say: the real-engine behaviour each fake step exists for, and the real behaviours no suite plays.

Go paths are relative to `pfm/`; a file named alone is in this package.

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

### Herdr seats

**Native Claude detection coverage:** probed live on Herdr 0.9.5 with Claude Code 2.1.317: Herdr detects Claude by the pane's foreground process group and reads its state from the screen; where Herdr lists a foreground without Claude, its agent status reads `unknown` and `agent.get` answers `agent_not_found`, so pfm reports there under `pfm`; the journey suite plays only panes Herdr sees Claude in. `herdr integration install claude` (script v11) adds one `SessionStart` hook that sends only `pane.report_agent_session` under `herdr:claude`, writes `{CLAUDE_CONFIG_DIR}/hooks/herdr-agent-state.sh`, keeps a symlinked `settings.json` a symlink and is idempotent per config dir, while two config dirs sharing one `settings.json` get two entries; a state pfm had reported under `herdr:claude` held over Herdr's own screen reading for more than four hours after an Esc. The installer, doctor and cutover run against a fake `herdr` and a fake socket only.

**UNPLAYED — native Claude detection:** Herdr's own screen detection over an actual Claude background agent running for 60 s and real permission and `AskUserQuestion` dialogs, real-engine restore through Herdr's integration (across accounts the managed launcher resumes on the ambient or primary account, not the session's owner), the install cutover against a live Herdr, and real Codex/OpenCode screen behaviour. No real engine/account suite is permitted.

**UNPLAYED — native Codex queued questions and subagent hooks:** the fake Codex fires its own thread's trusted `hooks.json` handlers at `SessionStart`, `UserPromptSubmit`, `PreToolUse`, `PostToolUse`, `Stop` and `SessionEnd` (`internal/mockengine/codex_fire.go`), never `PermissionRequest` or `Interrupt`, and its child rollouts fire none; it does not emit a child's `PermissionRequest`, `PostToolUse`, `Stop` or `SessionEnd`, or model `request_user_input_async` queued while the parent remains Working. `internal/hookentry/herdr_codex_test.go` exercises synthetic payloads for child isolation, missing-rollout reporting and blocking/async question transitions; it does not prove native hook emission or Herdr screen-detection arbitration. No real engine/account suite is permitted.

- **KNOWN DEFECT:** real OpenCode 1.14.30 was installed at image build and its TUI rendered in an offline Herdr walk. Raw call refuses its empty box composer (`Ask anything...`) with exit 6: the generic composer guard recognizes Claude/Codex markers, not this OpenCode shape. Delivery remains unproven; the repair needs placeholder-versus-draft checks against the real styled composer, not a guessed mock.
- **MANUAL PROOF, held by no suite step:** terminal `IGNCR` discarded Enter while retaining typed raw text; call exited 6 with an explicit unconfirmed warning. A permission-style fake-engine prompt produced Herdr's native `agent_status=blocked`; status, ls, watch and call showed blocked/refusal, not idle/delivered. A real Herdr TUI received the fixed loss note with notifications enabled; a product mutant appending the private prompt marker failed the same screen assertion before the unchanged product passed.
- **UNPROVEN on a real Herdr** (the cases run only against a fake socket or a jailed PTY, or not at all):
  - Graceful versus forced close of a native seat: which of the two close paths ran.
  - `open` of a dead seat: the birth-rule fallback is jail-tested (`TestOpenNativeDeadDetachedRevivesOnItsOwnHerdrHost`, `TestOpenNativeDeadEndpointUsesBirthRule`), never observed on a real Herdr.
  - The lost and failed display forms in `pfm chat ls`, `status` and `watch` after a real dead backend or a killed seat.
  - Stealing an interrupted pre-seat-host ownership.
  - The same-pane open: a picker inside a Herdr pane opens every pick in its own pane (new and resumable rows exec in place, a live native chat moves into the picker's tab, a live tmux chat attaches there), which no real-picker walk has verified; a caller that does not front its pane still gets a new tab.
- The macOS lifetime fix (#5236) has no containing release to test against, so doctor only warns. A raw `--then` turn that finishes between two snapshots is refused (fail-closed), not proved. Named residuals: two simultaneous `--restored` recoveries of one session can each start an engine before the seat host marks it live, and a degraded shared state store aborts a native `open` before its tmux path (the fleet scan has no per-seat fallback).

### Codex process and UI

- Store-only resumed thread identity, name and window (`resolve/codex.go`).
- Store-only fresh thread exporting `CODEX_THREAD_ID`.
- Rollout writer that closes its fd immediately after writing.
- A TUI rename updating `threads.name`.
- A rename landing only in `session_index.jsonl`.
- `codex resume <name>` creating a paginated thread with no rollout.
- Two Codex chats born in the same directory within the birth window.
- Approval and plan-overlay modals during call.
- Actual Codex composer behavior at the literal and bracketed-paste boundary; auto-file fixtures cover pfm's side.
- `pfm chat kill self --exit` on a real Codex pane (no typed `/quit`: the fallback kill-pane closes it).
- `pfm name-sync` converging a cx window name onto a VS Code tab.
- Codex-origin call signature via ancestry recovery.
- `ls` reporting busy for a generating Codex pane.
- `cx` launcher self-switch inside its own server.
- Actual Codex TUI trust-modal and pre-filled rename behavior at chat birth; the mock covers pfm's launch path, not that UI.
- Actual Claude and Codex transcript shapes for multi-turn `chat new --await`, `ask`, timeout and read; the mock covers pfm's handling of their fixture transcripts.
- Codex `/clear` event timing and payload session id.
- A chat spawned through `pfm-mcp.service` surviving a real user-manager restart.

### Workbench engines

- **UNPLAYED** — Real Codex honouring the workbench `developer_instructions` and its mirror’s skills; fixtures prove argv and mirror artifacts.
- **UNPLAYED** — Real OpenCode merging the workbench prompt's `instructions` entry and running the seat plugin's `experimental.chat.system.transform`; Node fixtures prove the plugin on OpenCode 1.18.18's system shape, Go fixtures the environment and staging.

### Claude process and UI

- Agent row for a non-primary config dir using `--session-id`.
- Agent takeover through `pfm internal agent-open`.
- The daemon-agent guard on the resume-call path.
- Claude `/clear` and `/exit` hook event timing.
- Open gate for a live chat whose birth account differs from primary.
- Actual Claude reboot-in-place and `--then` delivery; the mock exercises pfm's side.
- Trust prompt on a fresh config-dir and cwd pair.
- Actual Claude `--fork-session` implementation; the mock covers pfm's branch path. The mock seeds the fork's transcript at launch, while Claude writes it at the fork's first prompt: `cmd/pfm/booting_row_jail_test.go` (`TestPromptlessForkListsAndResolvesByItsLaunchName`) plays that promptless shape.
- `pfm chat new NAME` teammate spawn and immutable socket identity in actual Claude.
- `⚡1h` badge read from an actual Claude launch record.
- Actual Claude composer behavior at the literal and bracketed-paste boundary; auto-file fixtures cover pfm's side.
- Dim-SGR placeholder versus a draft in actual Claude.
- Selector and permission modals in actual Claude.
- `pfm reap` busy probe against actual `claude agents --json` output.
- Actual `continued-in` transcript record and daemon-job session file shape: the fake Claude plays the shape pfm reads (`claude_background_job.go`); the real shape is not re-checked.
- **UNPLAYED** — Claude Code's Ctrl+S composer-stash semantics that pfm's stash guard assumes: one slot, so a later stash overwrites the earlier draft; Ctrl+S on an empty composer pops the draft; any confirmed submit restores it. Fake-tmux unit tests cover pfm's side, and the fake composer (`internal/mockengine/tty.go`) plays the parked-draft restore; Claude Code's side is unproven.
- **UNPLAYED** — the `--settings` value reaching the Claude process and hook children: no journey step plays it yet.
- **UNPLAYED** — cross-account `/pfm:reload` resuming the same transcript on a wired seat: no journey step plays it yet.

### Account and host

- Transcripts under a separate account root rather than a link to account 1.
- Statusline badge computation across actual accounts.
- OAuth Claude Code routing a throwaway `CLAUDE_CONFIG_DIR` to the local harness-prompt sink.
- Version cleanup recognizing the executing image of older, live Claude builds.
- VS Code Insiders loading the linked extension from its extension index on cold start.
- VS Code rendering the New Chat Terminal icon from the keybinding, palette and terminal dropdown.
- Real engine screens and records behind `blocked` and interrupted turns: an actual Claude or Codex permission dialog, question or modal on a seat whose transcript ends in a pending tool call (the pane rule reads the screen with `call.SelectorLine` and `ClaudeTrustDialog`), and an actual Codex `turn_aborted` rollout record and Claude `[Request interrupted by user…]` record from a real interrupted turn; the jail plays fixture shapes of each.
- `pfm internal schedule-fire` through real tmux: a live Claude or Codex chat receiving the schedule by session id under the setter's signature, and a dead one resumed detached on a fresh socket whose composer is awaited before the schedule is typed. The suite plays the wait, trust-dialog and failure logic against fake open / capture / call seams and the store, lock and CLI against the jail; no fixture engine renders a resumed composer that accepts the called line and proves it.
- The schedule timer under a real service manager: the suite renders and links the units and agent into a temp HOME against a fake runner; no fence runs a live systemd --user or launchd that ticks the timer and proves `schedule-fire` runs from it.
- The engine guard as the engines run it. Played for Codex: the fake Codex fires the `hooks.json` handlers pfm installs (`internal/installer/codex_hooks.go`) only while `config.toml`'s `hooks.state` trusts them at their current hash, as `pfm install` records it (`internal/mockengine/codex_fire.go` `loadCodexHooks`, `codex_hooks.go`); a trusted `PreToolUse` deny refuses the call before it runs, the pane draws the deny row and the rollout's `function_call_output` is the reason (`TestCodexFiresItsTrustedHooksAtEachBeat`). The journey's `install/day/guards/project/claude-from-picker/end/codex-in-project` types Codex turns whose `git commit` and `AGENTS.md` write `pfm internal engine-guard --engine codex` refuses, and whose `git status`, heredoc-written script and project script taking `$1` and calling its sibling by its own directory it lets run. Unplayed: real Codex's own rendering of a deny (the fake draws the shared deny row and records the bare reason), and the OpenCode engine guard, a plugin real OpenCode loads and the fake OpenCode cannot; its verdicts stay pinned at the unit tier (`internal/hookentry/engine_guard_test.go`).
