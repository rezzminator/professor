# Slow integration tests

## Contents

- [Test records](#test-records)

The durations below are historical observations under contention at `8bfb6cdc`. Source dependencies and dispositions describe the current test bodies. Tier placement is a decision for this train; it does not claim a move is complete.

## Test records

### TestInternalLaunchPrintExecsDirectlyWithoutTmux

**Source:** [pfm/cmd/pfm/internal_launch_test.go](../../../pfm/cmd/pfm/internal_launch_test.go) line 189 · **Observed:** 94.46s · **Class:** process/subprocess

jailTest; exec.Command(os.Args[0], -test.run=...); helper launch uses PFM_TEST_LAUNCH_REAL; reads PFM_TMUX_DIR.

A — launch proof is a real child process even though the path under test avoids tmux.

### TestJailedEvalAttachFromPlainAndNestedTmux

**Source:** [pfm/cmd/pfm/attach_jail_test.go](../../../pfm/cmd/pfm/attach_jail_test.go) line 51 · **Observed:** 44.80s · **Class:** process/tmux

newAttachJail; real pfm index; proveAttach drives tmux/script across plain, nested, and bunker modes.

A — real tmux and shell attach behavior is the contract.

### TestActionStress

**Source:** [pfm/internal/action/stress_test.go](../../../pfm/internal/action/stress_test.go) line 17 · **Observed:** 22.87s · **Class:** CPU plus shell subprocess

Synthesize 10,000 times; stressHostileProjectDirectories creates 1,000 dirs, then runs sh -n and sh -c for each.

A — the top-level body has roughly 2,000 real shell launches; the pure synthesis portion would need a separate U body.

### TestUpdateRunsPostBuildActionsThroughTheSelectedCandidate

**Source:** [pfm/cmd/pfm/update_command_test.go](../../../pfm/cmd/pfm/update_command_test.go) line 69 · **Observed:** 22.16s · **Class:** build/git subprocess

jailTest; newTaggedBuildFixture runs git; update.Run builds/runs candidate actions; reads marker.

A — update verification crosses the fixture repository and candidate process boundary.

### TestUpdateBuildsSelectedTagIntoOwnedBinaryAndSkipsHarvestProvisioning

**Source:** [pfm/cmd/pfm/update_command_test.go](../../../pfm/cmd/pfm/update_command_test.go) line 16 · **Observed:** 19.56s · **Class:** build/git subprocess

jailTest; tagged fixture git; update.Run; exec.Command(canonical, version); branch/revision checks.

A — real candidate build and executable invocation are exercised.

### TestStressResolveSixtySocketsTwoHundredPanes

**Source:** [pfm/internal/resolve/tmux_jail_test.go](../../../pfm/internal/resolve/tmux_jail_test.go) line 326 · **Observed:** 16.80s · **Class:** process/tmux

newResolveJail; start/split create 60 real tmux servers and 200 panes; New.Resolve; 200 ms settle.

A — resolution is measured against real tmux servers and pane output.

### TestMCPHandshakeAndAllToolsOverJailedStdio

**Source:** [pfm/internal/mcpserv/server_test.go](../../../pfm/internal/mcpserv/server_test.go) line 224 · **Observed:** 13.71s · **Class:** process/tmux/stdio

newStdioJail starts tmux/Python fixtures; buildFleetBinary runs go build; mcp.CommandTransport runs binary and tools.

A — real MCP binary, stdio, and jailed tmux round trip are the behavior.

### TestMachineConfigChangesTheActualLaunchCommands

**Source:** [pfm/cmd/pfm/chat_new_jail_test.go](../../../pfm/cmd/pfm/chat_new_jail_test.go) line 556 · **Observed:** 13.49s · **Class:** process/tmux

newRunJail; run chat new; engine stub and tmux; jail.await reads launch argv.

A — launch command construction is proven through a real jailed session.

### TestAskHoldsATwoWayConversation

**Source:** [pfm/cmd/pfm/two_way_jail_test.go](../../../pfm/cmd/pfm/two_way_jail_test.go) line 58 · **Observed:** 12.62s · **Class:** process/tmux

newRunJail; run chat new; statedTestSender; two run chat ask calls against tmux stub.

A — two-way conversation is exercised through a live jailed chat.

### TestInjectRefusesRosterAmbiguityWithThreadIDsAndSockets

**Source:** [pfm/cmd/pfm/same_name_resolution_jail_test.go](../../../pfm/cmd/pfm/same_name_resolution_jail_test.go) line 129 · **Observed:** 7.32s · **Class:** process/tmux

newRunJail; two spawnNamedCodexFixture calls; run chat inject; real candidate sockets.

A — ambiguity is observed from real jailed Codex panes and CLI resolution.

### TestInjectAndResolvePreferUniqueRosterNameOverDuplicateRawWindows

**Source:** [pfm/cmd/pfm/same_name_resolution_jail_test.go](../../../pfm/cmd/pfm/same_name_resolution_jail_test.go) line 87 · **Observed:** 5.39s · **Class:** process/tmux

newRunJail; named Codex fixture plus two pre-rollout tmux fixtures; run chat resolve/inject; pane capture.

A — roster/raw-window precedence is an end-to-end jailed process behavior.

### TestFourthEngineNeedsOnlyItsOwnPackage

**Source:** [pfm/cmd/pfm/fourth_engine_test.go](../../../pfm/cmd/pfm/fourth_engine_test.go) line 29 · **Observed:** 5.15s · **Class:** process/re-exec

parent re-execs os.Args[0] with fourthEngineHelper; registers engine surfaces; temp config/path checks.

A — isolation proof deliberately crosses a child test process.

### TestProbeDistinguishesOKMinimumGarbageMissingAndTimeout

**Source:** [pfm/internal/deps/probe_test.go](../../../pfm/internal/deps/probe_test.go) line 15 · **Observed:** 5.05s · **Class:** subprocess timeout

writeProbeStub; Probe -> probeOne -> boundedOutput -> exec.CommandContext; timeout stub runs /bin/sleep 30.

A — real child-process timeout, not CPU; keep in Tier A or add a separate injected runner unit.

### TestProbeSelfDoctorUnsupportedAndTimeoutAreHonest

**Source:** [pfm/internal/deps/probe_test.go](../../../pfm/internal/deps/probe_test.go) line 477 · **Observed:** 5.02s · **Class:** subprocess timeout

shell stubs from writeProbeStub; Probe -> self-doctor boundedOutputWithEnvironment; hung stub executes /bin/sleep 30.

A — self-doctor timeout classification is observed through real command execution, not CPU.

### TestProbeSelfDoctorTimeoutIsNotConflatedWithBroken

**Source:** [pfm/internal/deps/probe_test.go](../../../pfm/internal/deps/probe_test.go) line 576 · **Observed:** 5.02s · **Class:** subprocess timeout

two shell stubs; Probe runs version/help/summary via exec.CommandContext; summary timeout uses /bin/sleep 30.

A — real subprocess timeout and quick non-zero cases cross the unit boundary.

### TestVersionProbeTimeoutIsNotConflatedWithBroken

**Source:** [pfm/internal/deps/probe_test.go](../../../pfm/internal/deps/probe_test.go) line 80 · **Observed:** 5.01s · **Class:** subprocess timeout

writeProbeStub; Probe -> boundedOutput; hung stub is /bin/sleep 30, broken stub exits 7.

A — timeout-vs-broken is process behavior; the old CPU label is false.

### TestRunReportsACodexBuildThatCannotBeRenamed

**Source:** [pfm/cmd/pfm/chat_new_jail_test.go](../../../pfm/cmd/pfm/chat_new_jail_test.go) line 473 · **Observed:** 4.19s · **Class:** process/tmux

newRunJail; run chat new with Codex stub; tmux pane and marker reads.

A — live chat and failed rename are proven through the jailed process path.

### TestMCPMalformedFrameReturnsJSONRPCError

**Source:** [pfm/internal/mcpserv/server_test.go](../../../pfm/internal/mcpserv/server_test.go) line 918 · **Observed:** 4.00s · **Class:** process/stdio

setupBackendFixture; buildFleetBinary; exec.Command(binary, ..., mcp); StdinPipe/StdoutPipe JSON-RPC.

A — direct real MCP daemon process and pipes are under test.

### TestJailedThenWaiterDeliversAfterIdleExactlyOnce

**Source:** [pfm/internal/inject/tmux_jail_test.go](../../../pfm/internal/inject/tmux_jail_test.go) line 327 · **Observed:** 3.58s · **Class:** process/tmux

newInjectTmuxJail; Python busy/idle pane via tmux; inject.New with fake resolver/spawner but real TmuxInjector; DeliverThen/capture.

A — critical busy-to-idle delivery contract uses a real pane.

### TestMCPMetadataThreadIdentityRoutesDistinctCodexSeats

**Source:** [pfm/internal/mcpserv/metadata_identity_test.go](../../../pfm/internal/mcpserv/metadata_identity_test.go) line 273 · **Observed:** 3.29s · **Class:** process/tmux plus MCP

metadataIdentityService -> newStdioJail starts tmux/Python rows; in-memory MCP clients call capture/keys/inject.

A — MCP transport is in memory, but live seat fixtures are real tmux processes.

### TestBootingRowInteractivePickerJailed

**Source:** [pfm/cmd/pfm/booting_row_jail_test.go](../../../pfm/cmd/pfm/booting_row_jail_test.go) line 181 · **Observed:** 3.01s · **Class:** process/tmux

newBootingPickerJail; real tmux driver exec.Command; jail.startTarget; send-keys and pane/client polling.

A — interactive picker and attach are real tmux behavior.

### TestAskReportsATimeoutWithoutLosingDelivery

**Source:** [pfm/cmd/pfm/two_way_jail_test.go](../../../pfm/cmd/pfm/two_way_jail_test.go) line 96 · **Observed:** 2.91s · **Class:** process/tmux

newRunJail; run chat new/ask/read; muted engine stub; tmux transcript proof.

A — delivery and timeout are tested through the real jailed chat process.

### TestTerminalShellActionExecsInteractiveZsh

**Source:** [pfm/internal/action/dispatch_test.go](../../../pfm/internal/action/dispatch_test.go) line 28 · **Observed:** 2.78s · **Class:** subprocess/shell

writes .zshrc; re-execs test binary; helper invokes interactive zsh function and marker.

A — interactive zsh execution is explicitly the behavior under test.

### TestChatNewSpawnsANamedCodexChat

**Source:** [pfm/cmd/pfm/chat_new_jail_test.go](../../../pfm/cmd/pfm/chat_new_jail_test.go) line 393 · **Observed:** 2.51s · **Class:** process/tmux

newRunJail; run chat new --engine codex; Codex stub/tmux; reads name, prompt, socket, and shared comms.

A — new-chat creation and naming cross the jailed engine boundary.

### TestRunRefusesToCallAnUnheardPromptDelivered

**Source:** [pfm/cmd/pfm/two_way_jail_test.go](../../../pfm/cmd/pfm/two_way_jail_test.go) line 143 · **Observed:** 2.34s · **Class:** process/tmux

newRunJail; muted/deaf Claude stub; run chat new; launch rescue/recording over tmux.

A — proof depends on a real jailed chat and transcript side effect.

### TestChatInjectResolvesUnindexedLiveSessionAcrossProbeSockets

**Source:** [pfm/cmd/pfm/inject_cli_jail_test.go](../../../pfm/cmd/pfm/inject_cli_jail_test.go) line 86 · **Observed:** 2.25s · **Class:** process/tmux/Python

creates Python UI scripts; starts three tmux servers with exec.Command; run chat inject; capture/selector/file-backed checks.

A — socket discovery and injection are real process interactions.

### TestOSCTitleWriteNeverTouchesTheWindowName

**Source:** [pfm/internal/gather/rename_second_writer_jail_test.go](../../../pfm/internal/gather/rename_second_writer_jail_test.go) line 117 · **Observed:** 2.25s · **Class:** process/tmux/tty

startRenameProbeServer; tmux commands; TmuxProbe.RenameWindow; writes pane tty escape; polls tmux name/title.

A — tmux pane title/window behavior requires the real server and tty.

### TestRenameWindowLatchSurvivesAScreenTitleEscape

**Source:** [pfm/internal/gather/rename_second_writer_jail_test.go](../../../pfm/internal/gather/rename_second_writer_jail_test.go) line 152 · **Observed:** 2.19s · **Class:** process/tmux/tty

startRenameProbeServer; tmux allow-rename; TmuxProbe.RenameWindow; pane tty screen-title escape.

A — latch behavior is a real second-writer interaction.

### TestAutomaticRenameIsNotTheSecondWriter

**Source:** [pfm/internal/gather/rename_second_writer_jail_test.go](../../../pfm/internal/gather/rename_second_writer_jail_test.go) line 90 · **Observed:** 2.14s · **Class:** process/tmux

startRenameProbeServer; tmux automatic-rename option; TmuxProbe.RenameWindow; settled name query.

A — automatic-rename semantics are external tmux behavior.

### TestScreenTitleEscapeIsTheSecondWriterWhenAllowRenameIsOn

**Source:** [pfm/internal/gather/rename_second_writer_jail_test.go](../../../pfm/internal/gather/rename_second_writer_jail_test.go) line 136 · **Observed:** 2.14s · **Class:** process/tmux/tty

startRenameProbeServer; tmux allow-rename; real pane tty \ek...\e\\; settled window query.

A — screen-title writer behavior cannot be established with an in-process fake alone.

### TestCodexClearRefreshesBaselineAndRetainsFailedRetirement

**Source:** [pfm/internal/picker/refresh_codex_clear_retry_jail_test.go](../../../pfm/internal/picker/refresh_codex_clear_retry_jail_test.go) line 25 · **Observed:** 2.11s · **Class:** process/tmux/SQLite

jailTest; startCodexStatusPane real tmux; temp SQLite store; fleet.ReconcileCodexPanes; indexer reruns.

A — Codex pane status and retirement retry are coupled to real tmux output.

### TestRunAwaitsTheAnswerItAskedFor

**Source:** [pfm/cmd/pfm/two_way_jail_test.go](../../../pfm/cmd/pfm/two_way_jail_test.go) line 29 · **Observed:** 1.99s · **Class:** process/tmux

newRunJail; run chat new --await; tmux stub, transcript answer, attach summary.

A — await behavior is exercised over a real jailed chat.

### TestMCPMetadataSignsExplicitTargetWithoutRedirectingIt

**Source:** [pfm/internal/mcpserv/metadata_identity_test.go](../../../pfm/internal/mcpserv/metadata_identity_test.go) line 483 · **Observed:** 1.72s · **Class:** process/tmux plus MCP

metadataIdentityService -> real newStdioJail tmux/Python seats; in-memory MCP call; inject capture.

A — explicit target/provenance proof resolves and types into a real pane.

### TestRunRescuesAPromptStrandedByAnOverlay

**Source:** [pfm/cmd/pfm/chat_keys_jail_test.go](../../../pfm/cmd/pfm/chat_keys_jail_test.go) line 124 · **Observed:** 1.66s · **Class:** process/tmux

newRunJail; overlay engine stub; run chat new; rescue keys and transcript proof over tmux.

A — overlay rescue and recorded delivery use the live jailed chat boundary.

### TestJailedPickerEscDoesNotWritePendingKillOrPrimarySwitch

**Source:** [pfm/cmd/pfm/picker_cancel_jail_test.go](../../../pfm/cmd/pfm/picker_cancel_jail_test.go) line 20 · **Observed:** 1.66s · **Class:** process/tmux

newPickerCancelJail; direct tmux session script; send C-x/C-s/Escape; marker, primary, and kill-store reads.

A — picker input and side-effect suppression are interactive tmux behavior.

### TestStressTenSimultaneousKillExits

**Source:** [pfm/internal/kill/tmux_jail_test.go](../../../pfm/internal/kill/tmux_jail_test.go) line 251 · **Observed:** 1.60s · **Class:** process/tmux/SQLite

newKillTmuxJail; 10 tmux reader panes; SQLite killed rows; concurrent NewFinisher.Run; pane/crumb checks.

A — simultaneous process exit and tmux cleanup are the contract.

### TestReconcileCodexPanesRecordsTheNameItReAppliedAfterAClear

**Source:** [pfm/cmd/pfm/reconcile_codex_panes_jail_test.go](../../../pfm/cmd/pfm/reconcile_codex_panes_jail_test.go) line 1132 · **Observed:** 1.49s · **Class:** process/tmux/SQLite

startCodexStatusPane; temp SQLite; fleet.ReconcileCodexPanesWith; fake renamer plus real status pane.

A — live pane status is an external process surface.

### TestJailedLongCompactFocusFiresWithFullTranscript

**Source:** [pfm/internal/inject/tmux_jail_test.go](../../../pfm/internal/inject/tmux_jail_test.go) line 547 · **Observed:** 1.42s · **Class:** process/tmux/Python

newInjectTmuxJail; slow Python transcript pane; real TmuxInjector; long /compact paste and file polling.

A — long paste and focus firing are validated through the real pane.

### TestReconcileCodexPanesFollowsTheLiveProcessesCurrentRollout

**Source:** [pfm/cmd/pfm/reconcile_codex_panes_jail_test.go](../../../pfm/cmd/pfm/reconcile_codex_panes_jail_test.go) line 709 · **Observed:** 1.41s · **Class:** process/tmux/SQLite

startCodexStatusPane; temp SQLite rollouts/bindings; ReconcileCodexPanesWith; compose live-row check.

A — current process status line is supplied by real tmux.

### TestChatEndWarnsButSucceedsWhenRolePromptRemovalFails

**Source:** [pfm/cmd/pfm/chat_reload_command_test.go](../../../pfm/cmd/pfm/chat_reload_command_test.go) line 166 · **Observed:** 1.18s · **Class:** process/tmux/filesystem

newRunJail; real run chat new/end; sabotaged non-empty per-seat prompt path; tmux kill and warning check.

A — chat end and warning behavior depend on the real jailed chat process.

### TestReconcileCodexPanesSkipsDuplicateNameWithoutUsableBindingQuietly

**Source:** [pfm/cmd/pfm/reconcile_codex_panes_jail_test.go](../../../pfm/cmd/pfm/reconcile_codex_panes_jail_test.go) line 361 · **Observed:** 1.14s · **Class:** process/tmux/SQLite

startCodexStatusPane; temp SQLite names/rollouts; fleet.ReconcileCodexPanes; binding/warning checks.

A — duplicate-name handling is driven by a live tmux status pane.

### TestChatEndRemovesTheRoleSeatPrompt

**Source:** [pfm/cmd/pfm/chat_reload_command_test.go](../../../pfm/cmd/pfm/chat_reload_command_test.go) line 122 · **Observed:** 1.08s · **Class:** process/tmux/filesystem

newRunJail; real run chat new/end; agentrole.WriteSeatPrompt; tmux socket and per-seat prompt removal.

A — lifecycle and crumb cleanup are asserted after a real jailed chat kill.

### TestCommandSpawnerUsesNohupWhenSetsidIsAbsent

**Source:** [pfm/internal/kill/spawn_nohup_test.go](../../../pfm/internal/kill/spawn_nohup_test.go) line 23 · **Observed:** 1.07s · **Class:** subprocess/timeout

writes executable nohup fixture; CommandSpawner.Spawn; helper sleeps 1 s and writes done/argv; parent context cancellation.

A — nohup branch intentionally launches a detached child; sleep is child-process cost, not CPU.

### TestChatKeysDrivesALiveChat

**Source:** [pfm/cmd/pfm/chat_keys_jail_test.go](../../../pfm/cmd/pfm/chat_keys_jail_test.go) line 54 · **Observed:** 1.03s · **Class:** process/tmux

newRunJail; real run chat new/keys/read; tmux key presses and transcript polling.

A — key delivery is only proven against the live jailed pane.
