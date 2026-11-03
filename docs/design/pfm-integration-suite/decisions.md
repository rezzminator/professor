# Current design decisions

This file is the current source for accepted design rulings. The exact `ruling-*` headings are reference targets used by the ledgers. An unresolved owner input is stated as unresolved and is not treated as an accepted ruling.

## Contents

- [Journey and placement](#journey-and-placement)
- [Lifecycle and identity](#lifecycle-and-identity)
- [Execution and coverage](#execution-and-coverage)
- [Forecast scope conflict](#forecast-scope-conflict)
- [Verification status](#verification-status)

## Journey and placement

### ruling-place-submodule

A submodule chat opens in its own workspace, named for the submodule (`lantern` in the fixture). The folder without a `commondir` is its own place.

### ruling-place-workbench

A workbench chat rolls up to its owning project workspace (`atlas` in the fixture), as do nested folders beneath the project.

### ruling-typed-claude-launch-project-placement

A typed Claude launch from a Herdr pane uses `spawn.HerdrOpensInPlace` to choose between the caller pane and a new tab in the chat project workspace. When the caller is in another workspace, the chat starts in a new project tab and the original caller pane and workspace remain unchanged. This keeps launcher placement aligned with `action/executor.go` and Herdr reconciliation; `TestHerdrOpensInPlaceOnlyInItsPlaceWorkspace` pins the cross-workspace decision.

### ruling-cut-place-depth

Nested and home places walk only the place-sensitive beats. The combinations cut from that walk remain itemized and priced in `cuts.tsv`.

### ruling-cut-engine-place

Placement has no engine branch. Nested-place engine coverage walks Claude; the omitted place × engine × terminal interaction is recorded in `cuts.tsv`.

### ruling-cut-tmux-place

tmux has no placement dispatch. Nested-place folder-dependent tmux behavior is outside the journey and remains an explicit cut.

### ruling-cut-owner-places

The owner ruled on 2026-11-03 that the submodule, workbench and home places are not walked: each places a chat exactly as a project or subfolder does, so their beats repeat the project branch. `placementOf`'s handling of them stays in its unit tests; the shell-start=home forks remain under the project place, where they guard the chat placed in its own folder when the pane starts in home.

### ruling-cut-peer-place

Call pairs use project place only. Delivery branches on the target backend, not the target folder.

### ruling-cut-peer-os

A callee on another OS's Herdr variant is not represented in one root. Cross-OS peer calls are cut.

### ruling-cut-linux-native

Linux native panes are cut because shield-versus-native has no OS dispatch branch. Native panes are walked on macOS.

### ruling-cut-macos-shield

herdr-shield is Linux-only and degrades to a plain shell on macOS; the macOS shield variant is cut.

### ruling-cut-escape-engines

An escape beat walks the engine whose changed path it asserts. If another engine's path escapes, coverage expands to that engine.

## Lifecycle and identity

### ruling-uninstall-keeps-owner-files

Uninstall removes only files pfm installed. It preserves the owner's Herdr keybindings and herdr-shield configuration byte-for-byte.

### ruling-account-switch

`pfm chat reload --account N` starts the engine from account N's configuration directory. An unknown account is refused and the engine remains running.

### ruling-install-restarts-mcp

An applied Linux install restarts the enabled MCP daemon after its full config and client wiring transaction, even when the unit file did not change (`pfm/internal/installer/scheduler.go:193-203`). On macOS, an unchanged loaded MCP launch agent stays running when its health probe reports non-stale; the fixture asserts that actual reload policy and preserves the live chat (`pfm/internal/installer/launchd.go:132`, `pfm/internal/installer/launchd.go:195-213`; scheduler wiring is at `pfm/internal/installer/scheduler.go:112`).

### ruling-whoami-identity

CLI and MCP `whoami` identify the calling chat through its own process ancestry, not the parent shell's pane.

### ruling-agent-id-receipt

For a mock Claude `Agent` child, `claudeSession.executeTool` in `pfm/internal/mockengine/claude_agent.go` generates the child ID with `subagentIdentity` and returns it as `agentId`; `claudeSession.write` in `pfm/internal/mockengine/claude.go` records the ID as `AgentID` on `IsSidechain` transcript records, and `claudeSession.background` in `pfm/internal/mockengine/claude_lifecycle.go` uses the same identity helper for its sidechain records. The suite compares the actual sidechain transcript's `agentId` with the reporter's `active_child_ids`. The reporter observes the engine-owned ID; it does not generate it.

### ruling-gap-chat-reload-account-refused

`ValidateAccount` in `pfm/internal/reload/birth.go` refuses account 9 when the configured roster contains accounts 1, 2, and 3, but the refusal does not list that roster. `chat-reload/reload-into-missing-account-refused@*` keeps only the missing-roster-list assertion as a known product gap until a product flight adds it; its other assertions remain binding.

### ruling-gap-working-blink

`wantedIndicators` in `pfm/internal/herdr/indicators_paint.go` keeps `pfm_working` continuously present, while the blocked indicator alternates between its token and none. For the `chat-attention` rows listed under `ruling:ruling-gap-working-blink` in `gaps.tsv`, only the required `pfm_working` blink is a known product gap until a product flight adds it; every other assertion remains binding.

### ruling-gap-picker-blocked-state

The owner rules no new picker blocked-state feature for this flight. `compose.Row` carries `Working` and `AgentsWorking` only (`pfm/internal/compose/types.go`), Claude row facts populate those fields (`pfm/internal/rowfacts/claude.go`), and `pfm/internal/ui/dossier.go` renders the working gauge; `[B]` is Herdr tab decoration (`pfm/internal/herdr/tab_status.go`), not picker state. C67 records the actual idle/working picker gauge and marks comparison to a native blocked picker state unavailable for this known red-to-spec gap. Assertions of native Herdr blocked state and tab decoration remain strict. The separate `ruling-gap-working-blink` continues to apply.

### ruling-gap-codex-typed-reload

Codex typed `/reload` remains a known product gap until reload and exit interception are wired. The affected entries stay red-to-spec in `gaps.tsv` and do not block the suite.

### ruling-gap-opencode-reload

OpenCode reload remains a known product gap while the public reload command refuses it. Its reload entries stay red-to-spec in `gaps.tsv` and do not block the suite.

### ruling-gap-opencode-launcher

Typed OpenCode launch remains a known product gap until a shim exists. Its entries stay red-to-spec in `gaps.tsv` and do not block the suite.

### ruling-gap-opencode-tmux

OpenCode CLI birth through the tmux backend is refused and remains a known product gap in `gaps.tsv`; it does not block the suite.

### ruling-gap-install-deferral-session

`pfm install` defers a store change over a live chat by naming only the account directory (`pfm/internal/installer/live_chats.go`), never the live chat's session id. `machine-lifecycle/install-over-live-chat@*` asserts the id and stays a known product gap in `gaps.tsv` until a product flight adds it; it does not block the suite.

### ruling-gap-doctor-healthy-hooks

`pfm doctor` prints hook rows only on faults (`pfm/internal/installer/hook_probe.go`, `ProbeExpectedHooks`), so a healthy machine shows no ok row per engine and account. `machine-lifecycle/doctor-reads-clean@*` asserts those rows and stays a known product gap in `gaps.tsv` until a product flight adds them; it does not block the suite.

### ruling-gap-chat-ls-json

`pfm chat ls` has no machine-readable `--json` output and this flight adds none (no product features). The fleet-reader beats whose ledger text demands a machine-readable list stay known product gaps in `gaps.tsv` until a product flight adds it; every other beat asserts the public text output.

### ruling-gap-herdr-move-tab-key

The pinned Herdr 0.9.5 root key map has no cross-workspace move-tab entry, so the reader's moved-tab beats keep their key-entry requirement as a known missing-entry gap in `gaps.tsv`; no API call substitutes for the user's key.

### ruling-gap-darwin-empty-kinfo

A macOS process-table read returning an empty kinfo list or EIO has no product fault seam and no Go unit test holding the decision, so forecast cell `fleet-reader/forecast-seat-owner-herdr-seat-row-process-table-empty-kinfo-or-eio-os-macos` stays a known product gap in `gaps.tsv` until a product flight adds the seam or the unit test; a baseline read is never counted as exercising the error.

### owner-decision-herdr-plain-shell

The owner decision is pending: pfm offers no plain-shell entry in Herdr; a new pane always arms the picker. The Linux `herdr-shield` and macOS `herdr-native` plain-shell beats remain known gaps in `gaps.tsv`, owned by `owner` until that decision. They report as gaps and never as `blocked-by`.

### ruling-merge-shell-start

zsh is the shell for the owner's journey. bash is walked once per backend at `terminal-open` and once for a birth because adopters use bash.

### ruling-merge-workspace-state

Claude Herdr births at project place cover named, misnamed-exclusive, and mixed workspace states. Each state uses its own disjoint carried workspace so reconciliation in one branch cannot repair another branch's setup.

### ruling-merge-mcp-route

`machine-restart` covers daemon-proxy and local-fallback routing. With the MCP daemon down, all four backend directions run through local fallback, then run again after daemon recovery.

### ruling-merge-recipient-state

`chat-mesh` covers idle, working, draft, modal, and blocked recipient states. Non-idle state is established through the recipient's own pane, and expected delivery or refusal is asserted from a golden.

### ruling-merge-reload-lineage

In the same branch, after typed `/reload` and picker ctrl+o, separate entry beats exercise `pfm chat reload`, `--new`, and `--new --hide`. Resume preserves the session id; fresh changes it and leaves its predecessor visible; fresh-hidden hides exactly the carried predecessor.

### ruling-merge-lifecycle-cycles

`chat-ending` walks successive kill/resume cycles: pane exit to CLI unkill/open; CLI kill to MCP unkill/open; MCP kill to picker unhide/resume; picker kill to public resume; then end, resumed history, branch/recover, and archive. Each cycle starts after the previous resume proves the target identity.

### ruling-merge-carried-origins

Typed engine command, `pfm chat new`, and picker births remain live as carried origins in the mesh. In each backend direction, each origin acts as caller at least once.

## Execution and coverage

### ruling-c58-real-exec-failure

`chat-reload/forecast-session-state-continued-in-new-session-engine-claude-code-index-state-newest-path-lost@linux+herdr-shield+project+claude` first creates the native `continued-in` successor and proves its genuine indexed path. It then uses a run-owned seat-host stop only to order operations: prove the host PID and birth belong to this run, prove the original writer has ended and `reload_pending` is recorded, remove the recorded cwd before the successor OS exec, and observe the real exec failure, failed seat record, and `press Enter to retry, q to close` pane text. A child that started and then exited is not a failed birth; the failed replacement has literal `n/a:not-started` pid and birth. Restore cwd, retry through the public entry, hold the actual successor until the indexed path is restored, and verify the tail is intact. Cwd restoration and seat-host CONT run in `finally`; a remaining stopped host fails the test. No cached transcript record or process identity is fabricated. The setup and retry stay inside the beat's measured cost.

### ruling-mock-background-handoff

`reload-continued-chat-background-handoff@*` models native Claude background-job continuation: Claude appends a `continued-in` record to the old transcript, creates a successor session, and fires `SessionStart` in the new session while the launched seat retains its original launch id. The reload operation still addresses that original launch id and resumes the successor transcript. Ordinary `claude --continue` remains a separate same-session branch. The journey mock owns the native handoff behavior; this coverage is neither a mock gap nor a cut. The native behavior is represented by `claudeContinuedInRecord` in `pfm/internal/index/claude.go`, chain handling in `pfm/internal/fleet/continuation.go`, and reload resolution in `pfm/internal/chat/restored.go`.

### ruling-os-sequence-per-os

Each OS has its own root and full sequence; `os` is the first branch-key segment. A same-environment fork cannot cross machines.

### ruling-macos-landing-gate

Linux runs at every landing. Linux and macOS run as CI jobs on every pull request into `main`. macOS is not required on a `develop` landing; the owner decides when to run it because the publication rule forbids an unasked push.

### ruling-mac-root-inventory

The Mac runner inventory follows its runner image until the owner supplies an inventory read from the Mac. The owner's Mac topology has not been read here.

### ruling-dependency-per-tier

Unit fakes every external door. Hermetic uses real tmux, zsh, and SQLite with recorded Herdr and engine mocks. Live uses real Herdr, herdr-shield, tmux, zsh, and the service manager with recorded engines. The release leg uses real engines on dedicated test accounts and stamps remote conversations with the run id.

### ruling-release-llm

The owner must name the release model and reasoning setting per engine. This input remains unresolved.

### ruling-speed-targets

The sequence target is 2,700,000 ms per OS; lane targets are paper cost plus 20 percent. Unit and hermetic targets are measured and pinned at build step 3. The merged total must be recomputed from the current ledgers; the former base-only estimate is not the merged budget.

### ruling-roster-and-gaps

The roster is `owner`. Known product gaps stay in `gaps.tsv`, red-to-spec and non-blocking. Ruled ids are the seven listed in the design's landscape summary; ruled copy is `none`. Skip reasons and unobservable classes are recorded in their ledgers.

### ruling-logging

The existing `obs` package is the logging path, with the fields defined in section 10.

### ruling-burn-down

Each ratchet baseline for bare doors, clones, file size, and refused shapes falls by at least 10 percent per release.

### ruling-root-scale

The root seeds three projects plus the `~` workspace. Scaling to the owner's full project set is a weekly variant after the sequence is green.

### ruling-checkpoint-mechanism

Each lane snapshots the HELD machine filesystem, keyed by root and lane hashes. Restore restarts live processes and the next lane's prelude reopens chats.

### ruling-trust-stream-order

Forecast C67 stages a real Claude role with folder trust held, runs `pfm chat stream <exact native SID>` while trust remains blocked (follow is the default), and verifies trust plus session, pane, and process birth remain unchanged before accepting trust. It then runs the question and answer interaction and keeps the inherited blocked, chooser, and answer assertions.

### ruling-c08-native-detector-via-agent-explain

The owner rules that pinned Herdr's public `agent.explain(target=PANE)` is native detector evidence; no source-map key is required or invented. The C08 beat asserts the actual run's `agent='claude'`, `manifest_source='bundled'`, `state`, `matched_rule`, `visible_idle`, and `screen_detection_skipped=false` fields, with saved evidence. During native detection, pfm's own registered `pfm_legacy` `herdr-state` report sends its visible update. At native Claude end, pfm releases only its own `pfm` claim; Herdr's process sampler clears its detected Claude state. The actual restore record remains resumable. Reverting the `pfm/internal/hookentry/herdr_state.go:745` report-source hunk from `dee6ad92d0` in the private red tree makes the live beat fail; the end-release source is pinned separately by `ruling-c08-native-end-releases-pfm-only`.

### ruling-codex-denial-idle-and-end-title

For C78, the blocked approval prompt is the precondition proving the Codex subject ran. Pressing Esc denies the approval; Codex's real `Interrupt` hook maps to `herdr.EventIdle` in `pfm/internal/installer/codex_hooks_herdr.go`, so the next public pane report must be idle. Do not force `blocked_after_denied_approval` as the post-denial state or add a queued hold or follow-up turn. The live beat ends the chat that owns the tmux server and verifies that its pane is released with no agent claim. It does not read the terminal title after that server ends. `TestHerdrStateEndBlanksTheTmuxTitle` holds title blanking at unit tier under `tier-unit:chat-attention-end-title-blank`. C09 follows the same truthful interaction boundary for Claude: Claude has no interrupt hook for Escape, and the public report must settle to idle with owner=None without an invented follow-up turn. The private red for removing the Codex `Interrupt` mapping remains required; the title-action unit red is pending and neither red is claimed here.

### ruling-c78-public-state-unit-title

C78's live surface proves the real approval prompt reports blocked, Esc denial produces the next public idle report, and public end releases the pane with no agent claim. Terminal-title blanking is held only by `pfm/internal/hookentry/herdr_clients_test.go::TestHerdrStateEndBlanksTheTmuxTitle`, mapped as `tier-unit:chat-attention-end-title-blank` for `darwin-linux-launchd-systemd-proc-sysctl-setsid/C78`. Do not inspect a title after the owned server ends or use the keep-empty fault, which would change `tmux_server=chat_owned_server_ends_with_chat`. The private title regression is pending: remove the `reporter.blankTitle` append and require the named test to fail.

## Forecast scope conflict

The source retains 206 forecast cells. Tooling cells in the product map are held by exact named unit tests under `ruling-forecast-tooling-holders`; their records have unit-tier holders and make no live product-journey claim. os-doors/C31 remains open because current tests do not combine BSD stat with the macOS TMPDIR alias. darwin-linux-launchd-systemd-proc-sysctl-setsid/C101 remains open because no test makes the VS Code child exit before reading its whole stdin input. Both are known gaps under `ruling-gap-forecast-tooling-unheld`, owned until a product flight adds the exact holder.

The VSCode extension-host cells use `ruling-cut-vscode-unit-tier`: C30 retains its existing restore and uninstall holders; C94 is held by the lock-preservation unit test, C95 by workspace-scope tests, and C107 by independent-workspace history tests. C101 remains unheld under the tooling gap ruling; these unit tests do not claim a live editor or remote-extension-host journey.

### ruling-forecast-tooling-holders

The named development-tool forecast cells are held at unit tier by existing tests that execute the relevant tool behavior and assert the named outcome. Their cells remain in the source forecast ledger and do not count as live product journey coverage.

### ruling-gap-forecast-tooling-unheld

os-doors/C31 lacks a test combining BSD stat with the macOS TMPDIR `/var` to `/private/var` alias. darwin-linux-launchd-systemd-proc-sysctl-setsid/C101 lacks a VSCode test where the child exits before reading a whole stdin input. Both remain explicit known gaps owned by the product owner until a product flight adds the exact unit holder.

### ruling-forecast-guarded-site

A forecast factor naming a historical broken implementation remains metadata identifying the guarded site; the live beat establishes the real stopped-server or failed-read trigger and asserts the bounded result or explicit user-visible error, while the watched red removes that guard only in the private tree.

`stale_tmux_client` in C64 names the guarded historical site. The live beat attaches carried tmux Claude clients A and B through a real Herdr subject, suspends A, moves their shared physical terminal, and delivers A’s native event. It proves B’s agent session and title remain unchanged and that A’s event is not credited to B’s pane; a legitimate rebind before the event is accepted. The unit guard is separately watched at `pfm/internal/hookentry/herdr_clients.go::clientMayReport` through `TestHerdrStateReplacementKeepsItsPane/delayed_name`.

### ruling-reader-unset-theme

The live C27 fleet-reader journey enters through the carried owner CLI: `pfm theme status --json`, `pfm theme on --json`, then `pfm theme off --json`. Require the actual predecessor state to be disabled; an enabled predecessor is a setup failure, never toggled or skipped. A fresh run-owned tmux server with no `@host_accent` returns exit 1 and `invalid option: @host_accent`; `on` records literal `u:` in `@pfm-theme-prior-@host_accent`, and `off` restores the actually unset option. The owned generation advances once on and once off (two total) and is never rolled back. Preserve the exact global native options on every touched run-owned tmux server while retaining that generation advance. Restore all account settings and prove the carried and foreign bundle unchanged. `theme_prior=failed_read_as_unset` remains guarded historical-site metadata under `ruling-forecast-guarded-site`; the healthy live run does not execute the historical defect.

### ruling-cut-os-fault-unit-tier

kernel errno and pid-reuse faults need an injected OS failure no product seam offers; their decisions are held by the Go unit tests at the lowest layer, never aliased to a baseline read

The machine safety contract in os-doors/C21 is product scope. Its host-only factor is cut from the live journey under this ruling: the Linux journey is a real container and the fault door refuses its machine-global `/.dockerenv`. The holder from `856719f4` is `pfm/internal/testjail/fence_test.go:TestGateAdmitsOnlyTheFence (host shell exporting the fence variable and a store path), added by 856719f4`; `scripts/test-platform.sh` also checks `in_fence: a host shell exporting the fence variable and a store path is refused`. Neither unit proof is claimed as a host journey run or live database-byte proof.

## Deleted working directories

### ruling-deleted-cwd-rolls-up

A live chat whose working directory has been removed stays listed under the project resolved from the deleted path: a removed worktree maps to its repository, and a removed plain directory keeps its own name. The live journey uses a run-owned plain folder named `atlas-scratch-<run>`; its row keeps that project name, session, chat name, and status (`pfm/internal/compose/project.go:12`, `pfm/internal/compose/project_test.go:30`).

## Verification status

Build-order step 2 is still blocked: the prescribed fence and test entry points were rejected by the engine guard before execution. No check, baseline scan, planted self-test, or current-ledger verdict is claimed as passing. A run admitted by the fence must record each check's verdict before this step can be called complete.

### ruling-gap-tmux-attention-state-mark

`WindowNameFor` in `pfm/internal/gather/gather.go` clips the chat name, and `TmuxTitlesString` in `pfm/internal/config/tmux_titles.go` carries a name without adding chat-state marks. The requested working, idle, blocked, and error tmux window marks are a known product gap until a product flight adds them; the other live-state, picker, and transcript assertions remain binding.

### ruling-gap-opencode-archive

`pfm archive` keeps a killed OpenCode chat killed instead of archiving its files (`pfm/internal/archive/archive.go`, `pfm/cmd/pfm/archive_command.go`): OpenCode stores every session in one shared database, so moving it would archive every OpenCode session at once. `chat-ending/archive-after-end@*+*+project+opencode` asserts a per-session archive and stays a known product gap in `gaps.tsv` until a product flight adds one; it does not block the suite.

### ruling-gap-opencode-branch

`pfm chat branch` accepts Claude and Codex only (`pfm/internal/action/headless.go`, `pfm/cmd/pfm/chat_satellite_command.go`). `chat-ending/branch-after-end@*+*+project+opencode` asserts an OpenCode branch and stays a known product gap in `gaps.tsv` until a product flight adds one; it does not block the suite.

### ruling-gap-recover-codex-only

`pfm chat recover` reads a Codex thread or rollout and writes its recovery files under the Codex home (`pfm/cmd/pfm/chat_command.go`, `runChatRecover`); it launches no recovered process and recovers no Claude or OpenCode session. `chat-ending/recover-after-end@*+*+project+*` asserts both and stays a known product gap in `gaps.tsv` until a product flight adds them; it does not block the suite.

### ruling-herdr-resume-recipe

`pfm/internal/ui/launchwizard.go` `selectRow` deliberately skips the resume wizard for `spawn.BackendHerdr`: a native seat revives its stored recipe directly. Herdr ending resumes assert one Enter, the same session, account, model, effort and cwd, and no wizard on screen; tmux retains its resume wizard assertions.

### ruling-cut-vscode-unit-tier

No real VSCode app/server exists in the journey root, so extension-host cells are held at unit tier or remain explicit gaps. C30 is held by pfm/internal/themesync/vscode_restore_test.go::TestVSCodePendingRestoresWithoutStateOrInstallation; pfm/internal/themesync/vscode_restore_test.go::TestOffRetainsGlobalOwnershipWithoutStateOrVSCode; pfm/internal/installer/theme_sync_off_test.go::TestUninstallWaitsForVSCodeOwnershipOrExplicitLeave. These cover remote/global ownership discovery, disabled current.json with pending restoration, and refusal to uninstall without the leave flag while retaining recovery bytes. C94 is held by pfm/internal/installer/vscode_theme_lock_test.go::TestVSCodeThemeLocksPreserveOtherWindows; C95 by pfm/internal/installer/vscode_theme_test.go::TestVSCodeExtensionPaintsEveryWorkspaceKind and pfm/internal/installer/vscode_remote_test.go::TestVSCodeRemoteWindowServedFromTheClientPaintsNothing; C107 by pfm/internal/installer/vscode_native_test.go::TestNativeRemoteWorkspacesFollowIndependently and TestNativeLegacyHistorySeedsEachWorkspaceAndFailedSettingsRetry. C101 remains unheld under ruling-gap-forecast-tooling-unheld. These unit tests make no live editor claim.

### ruling-archive-skips-live

`pfm/internal/archive/archive.go` `Report.Live` deliberately skips killed chats that are still running. The live archive beat asserts the printed skipped count, unchanged process birth and files; process termination belongs to the kill beat.

The four restart race cells os-doors/C17 and darwin-linux-launchd-systemd-proc-sysctl-setsid/C50,C51,C92 use the exact unit holders named in their tier-unit beats under ruling-cut-os-fault-unit-tier. They inject no fabricated kernel records into a live journey.

### ruling-gap-herdr-api

Pinned Herdr 0.9.5 provides no per-pane respawn, recycle, or restart operation, guarantees neither close/create pane-id reuse nor API request logging or a cursor that could measure an exact dead-seat `pane.get` count or exact `agent.rename` request count, and exposes no scoped tab-colour channel; account 3's cobalt-dazzle `#5f86ff` accent is renderer input, not Herdr tabcolour. Public help, the pinned API schema, default config, and before/after saved-server restore observations confirm these limits; restarting the whole server changes a foreign sentinel and cannot serve as an isolated fault.

### ruling-zombie-keeper-paced-worker

The zombie escape measures children of the real detached paced statusline worker (`pfm statusline --paced KEY`), which starts `--refresh-gpt` children when its Codex usage cache and attempt marker are stale. Ten actual refreshes must leave no zombie children; the journey advances the owned cache marker through the shared mtime fault door between native idle ticks. The private regression reverses `deps.Runner.Start(Detach)` in `pfm/internal/statusline/process.go` to watch the assertion fail.

### ruling-cut-plugin-escapes

The cancelled-write escape in the owning plugin's `plugin-log.ts` belongs to that plugin's logging suite because the fix lives in the plugin. The journey suite keeps only pfm-owned behavior, while `tier-hermetic:log-filters-records` remains the `pfm log` filtering check through `report.log`.

### ruling-native-herdr-reporter-source

`report_source=pfm_legacy` names pfm's current registered `herdr-state` reporter source; `herdr_detection=native_claude_foreground` names Herdr's detection of a native Claude process. The public pinned agent.explain response exposes agent, manifest source, state, matched rule, visible-idle, and screen-detection-skipped fields, but no literal `herdr:claude` source-map key. C08 saves that actual response, observes the receiving-door Action from pfm's report, and verifies that visible state follows pfm's report while native detection is active. After the real native end, it verifies the pane no longer shows Claude working and that the restore record remains resumable. A lower-tier product routing test pins the emitted Herdr source JSON; C08 makes no claim that agent.explain exposes its source key. The row runs on macOS native Herdr and makes no Linux execution claim. The watched report-source regression reverts the `pfm/internal/hookentry/herdr_state.go:745` hunk from `dee6ad92d0`; the release-source field removal is equivalent because native end has no state and leaves the reporter source empty.

### ruling-c08-native-end-releases-pfm-only

At native Claude end, the action contains `release=true` and stored engine/session identity but no state. The native reporter therefore does not set its source for that action, and Herdr's receiver defaults the release to `pfm`. This is intentional: pfm releases only its own claim, while Herdr's process sampler retires the `herdr:claude` detection and preserves the resumable restore record. The unit test pins the complete `pane.release_agent` line with source `pfm`. Removing `Source: reporter.source` from the native release is an equivalent mutation; forcing `herdr.ClaudeSource` must fail the test. The live C08 assertion checks the visible state after the native process ends and confirms the pane remains resumable.

### ruling-c06-native-notification

`StepNotification` in `pfm/internal/mockengine/scenario.go` sends the Claude `Notification` hook with `notification_type=plugin_notification`. The journey observes the blocked state, releases the native step, then observes `Stop` return the chat to idle; it does not model a plugin UI answer.

### ruling-c12-c48-native-codex-subagent

The Codex subagent journey uses the mock `background_agent` rollout and invokes the registered raw `SessionStart` hook with the rollout's actual child `session_id`, `transcript_path`, and cwd. `codex_records.go` writes the native subagent rollout metadata; the product discovers the child from that rollout. The assertion makes no claim about ancestry under a Codex app-server daemon.

### ruling-escape-costsink-unit-tier

The `3de4b8f4` fleet-reading/statusline escape is limited to costsink partial-success behavior: the sink returns HTTP 200 with exact refusal details, and valid cost data remains accounted for through torn-tail reading and statusline spend aggregation. Existing Go tests hold these decisions at their lowest layer; no native exporter or synthetic HTTP journey is claimed, and private watched-failure proof remains pending.

### ruling-c10-split-hangup-order-unit-tier

The live C10 beat covers the native Claude hook payload over 1 MiB: the registered pfm reporter must deliver the visible state transition and the run-scoped log must contain no `unexpected end of JSON input` error. The separate `tmux_server=gone_at_session_end` ordering remains at unit tier because native callback-spawn versus tmux hangup order is unrecorded. `TestHerdrStateEndAfterTmuxServerGone`, `TestHerdrStateLargePayload`, and `TestReadHerdrPayloadValidatesTheWholeStream` hold the server-gone handling and whole-stream payload behavior for darwin-linux-launchd-systemd-proc-sysctl-setsid/C10. Commit 9d55594ce8 changed `pfm/internal/hookentry/herdr_state.go` and `pfm/internal/hookentry/herdr_origin.go`: the reporter reads a bounded 64 MiB hook stream, and a late event after tmux server loss is logged at INFO. The ledger makes no native shutdown-order claim and adds no known-gap row.

### ruling-unnamed-birth-keeps-launch-label

Unnamed picker births preserve an empty stored and public `pfm chat ls` name. A Herdr new tab uses the exact engine launch label (Claude: `New Claude chat`; Codex: `New Codex chat`; OpenCode: `New OpenCode chat`); every unnamed birth’s new inner tmux window uses the literal name `chat` (`pfm/internal/naming/naming.go:119 PlaceholderWindow`, used by `spawn.WindowName` and `hookentry/launch.go`). For typed Herdr birth, `BirthTabLabel` → `TabLabel` → `capTitle` (`pfm/internal/spawn/herdr.go:184`, `pfm/internal/herdr/wire.go:297`) sets the initial label from the fixture cwd leaf (`atlas` for the canonical project), regardless of a default-number or external label on the unrecorded shell tab. Once pfm has painted and recorded the tab, a subsequent owner label is preserved exactly. `TestHerdrSpawnTabLabelIsNeverEmpty` holds the non-empty cwd-basename fallback. Named Herdr labels are the plain user-supplied name with case preserved; no account glyph is added and no upper-casing occurs. Only an owner-set Herdr label applied after first paint and record is preserved; the original tmux caller terminal label stays byte-exact. A plain outside-multiplexer PTY records `n/a:outside-terminal` for the caller-label comparison. Named Herdr and tmux births use the exact plain user-supplied name, case preserved. Herdr's automatic title replaces the launch label later, before first paint (`pfm/internal/herdr/tab_status_test.go::TestTabStatusAutomaticTitleBeforeFirstPaintReplacesLaunchLabel`); the Herdr rename wire refuses an empty name (`pfm/internal/herdr/wire.go::AgentRename.Line`). These establish the later title transition and empty-name rename refusal, not an unnamed launch-label write policy.

### ruling-c115-account-settings-scope

`capture_previous` in `infra/fence/journey/root-baseline.py` retains the actual previous-install account `settings.json` input. The C115 behavior claim is limited to that account-layer matcher winning the identical current inline command; the previous install's inline launch `--settings` (prior `ensure_role` recipe) is not replayed, and the effective hook registry of a prior chat is not claimed.

### ruling-gap-c115-prior-inline-settings

The C115 row does not replay the previous install's inline launch `--settings` (prior `ensure_role` recipe) and does not claim the effective hook registry of a prior chat. This scoped gap remains until a product flight supplies the prior inline recipe.

### ruling-indicator-escape-beat-bound

Escape 57 uses the existing 15-second beat bounds: Herdr indicator CPU growth must stay below 1.5 seconds and written bytes below 5 MB while OpenCode is idle. Those limits detect the recorded 22–28% CPU and 6 MB/s store reread defect with margin: the defect would use at least 3.3 CPU seconds and about 90 MB of writes in that window. A separate 30-second window and per-file writer instrumentation add cost without improving detection, so the escape signature matches the beat.

### ruling-indicator-launch-cwd

The C33 `daemon_launch_cwd=removed_worktree` factor describes the indicator process startup cwd, captured in its launch receipt. The entry changes its running cwd to `/` before painting (`pfm/cmd/pfm/main.go:482`), so the live row also checks `process_cwd` for the same PID and start and expects `/`. The earlier interaction-faults source used a shorter cwd label; the canonical forecast names the launch-time value explicitly.

### ruling-reader-forecast-unit-tier

C116 maps to the actual CLI Claude birth (`chat.new-cli`) with the unreadable account settings and user OTLP endpoint observed in the child; undo this mapping only if the public producer changes.

C15, C22, C31, C76, C90, and C117 are held by the exact Go tests named in their tier-unit rows. Their live producers are unavailable for these reasons: no controllable in-flight compressed rollout descriptor, no owned other-UID/SIP process, no native OTLP exporter, no public no-report attach prehistory, no current empty-Source Native producer, and no controlled transcript stat/read window, respectively. These unit holders make no live product-journey claim.

C30 pins server alive, dead pane, PID 0; native tmux 3.4 produced no empty-PID dead descriptor.

The exact C10, C05, C43, and C91 parameters are held at the lowest existing Go unit layer named by each tier-unit beat. C10 classifies denied environ reads as foreign for another UID or for a same-UID terminal-less systemd user manager with capabilities the caller lacks. Its table keeps same-UID unprivileged, terminal-present privileged, and unreadable-stat rows as ordinary permission errors. C05 covers the read-only schema-5 report and schema-5 startup migration; the provisioned v0.86.0 binary already stamps schema 6, and editing the active database to schema 5 would fabricate the condition. C43 is the typed `/reload` shell-quoting interceptor table, not a `pfm ls` input. C91 names `reader=pfm_rollout_name_test`, the lowest-layer compressed-rollout filename-classifier parameter; the `.jsonl.zst` table entry yields the empty rollout ID. C11 is held by both `pfm/internal/gather/procfs_test.go::TestProcExecutablePathUsesKernelPathNotArgv` (kernel `exec_path` wins over mutable argv[0], with short, relative, and unterminated buffers rejected) and `pfm/internal/gather/signal_test.go::TestInstallOwns` (the no-mark install fallback accepts this install's binary and rejects another or unreadable executables). `CaptureProcessIdentity` remains a separate consumer and is not covered by this tier. C34 is held by `pfm/cmd/pfm/internal_clear_kill_jail_test.go::TestResumeUnkillHookLiftsAKillWhenTheThreadIsResumed`; the mock engine treats unsupported `/resume` as an ordinary prompt, and CLI `--resume` does not produce the in-app-resume factor. C42 is held by `pfm/internal/hookentry/herdr_codex_test.go::TestHerdrCodexReportsOnlyToTheRegisteredPane`; the shared Codex app-server has no SessionStart hook-child ancestry producer. C77 is held by `pfm/internal/hookentry/herdr_clients_test.go::TestHerdrStateRebindsAClientWhoseTerminalVanished`; the pinned Herdr has no isolated per-pane restart API, while restarting the whole owned server changes the foreign sentinel. These factors remain at unit tier and receive no native journey credit.

### ruling-reader-account-discovery-errors

`themesync.accountSettings` reads `~/.cc` with `os.ReadDir`; only `ENOENT` means the account directory is absent. Other directory-read errors, including an actual `.cc` self-link `ELOOP`, are reported as `themesync: list account settings: open PATH: ...`. Successful discovery keeps the existing directory and stray-file filtering. C26 drives the real directory-read failure and the follower keeps its prior valid WatchPaths; C99's settings-file `ELOOP` remains a separate file-read fault.

### ruling-reader-paced-refresh

This reader uses `ruling-zombie-keeper-paced-worker` for the actual paced-worker subject and its bounded cleanup contract. The C23 forecast label records the historically guarded detached-child site. A distinct carried Claude account-2 turn triggers the installed native statusline, which runs the real `pfm statusline --paced KEY` worker through ten actual Codex quota refreshes; the reader verifies zero zombie children after bounded polling. The watched private regression removes the detached-process wait and must produce a zombie; the live test does not require that defective state.

### ruling-herdr-keep-rename

In fleet-reader C01 and C85, `herdr_keep` is historical factor metadata: `herdr_keep -> herdr-shield, 8f368d45 pure rename, same relay`. The live check follows the provisioned alias to the shield executable and observes its real attach ancestry.

### ruling-reader-outside-launcher-version

The `launcher.version` capability is the installed `claude --version` shell entry. `ClaudeLaunch` resolves the caller through `Launch` before pass-through; the reader uses the actual installed launcher in a plain PTY, verifies the native argv and pinned version, and observes no engine birth or owned Herdr pane while the run-owned tmux server is stopped. The full carried and foreign bundle and owned terminal inventory remain unchanged through restoration.

This installed-launcher pass-through is one shared decision and runs once; engine and place combinations do not change its result. The once-only matrix excludes the other generated generic OS and terminal combinations, while Darwin-specific ABI lookup remains a separate live cell.

### ruling-reader-unnamed-mcp-seat-identity

The unnamed Claude chat is born through the public path with name="". MCP whoami reports status ok, session `seat:<actual seat_id>`, the actual transcript SID, and exact pane; CLI whoami is corroboration only. The bare-seat-ID factor is historical guarded metadata because current resolution emits the `seat:` prefix.

### ruling-reader-picker-quiet-witness

The parked-picker forecast requires a live picker PID, positive debug tmux.exec batches, a quiet window longer than two active five-second intervals at no more than 0.02 core CPU fraction, then bounded redraw without input. It claims no internal parked flag or synthetic clock.
