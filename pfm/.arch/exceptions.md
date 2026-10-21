# Ruled arch exceptions

- C18 `internal/statusline/context_capacity.go`: `gatewayModelsPathEnv` preserves the external gateway's official `GPT_GATEWAY_CHATGPT_MODELS_PATH` environment name for its configured catalog path; this one literal is excepted without obfuscation, relocation, or a broad checker exclusion.

A baseline entry that a wave ADDED (not measured from the old tree) carries its reason here, one line each: check, file, reason. The ratchet never reads this file.

- C11 `internal/paths/paths.go`: the legacy `fleet.db` literal (`legacyDBName`, read through `paths.LegacyStateDB` and `paths.LegacyCacheDB`) is how the legacy-database host checks and the create refusal find the old files.
- C17 `parse`, `render`, `renderheadless`, `resolve` in `internal/claudelaunch/`: Parse/Render/RenderHeadless/Resolve are the names docs/design/engines/claude-launch.md mandates.
- C23 `internal/statusline/cache_window.go`: the statusline writes nothing to stdout but the rendered line, so launch-record failures go to stderr beside the visible `💾⚠` segment marker.
- C13 `internal/claudelaunch/doc.go`: a package-doc file with no code; its doc-prose test was deleted, and C9 keeps the package comment required.
- C13 `internal/hostfixture/doc.go`: a package-doc file with no code; its doc-prose test was deleted, and C9 keeps the package comment required.
- C3 `cmd/pfm` budget 8698: `ThemesOffline` in `cmd/pfm/install_command.go` wires `paths.ThemesOffline` into the installer options beside `SkillSourcesOffline`; `askAwaitTimings` in `cmd/pfm/chat_ask_command.go` is the Await poll and settle seam `TestAskHoldsATwoWayConversation` sets, since `--settle` takes whole seconds.
- C3 `cmd/pfm` budget 8740: `reloadRequestedAccount` in `cmd/pfm/chat_reload_args.go` and the roster check in `runChatReloadWithRuntime` (`cmd/pfm/chat_reload_command.go`) refuse `pfm chat reload --account N` for an account outside the roster before the worker is scheduled, where the caller still reads (exit 2 on stderr), instead of a "scheduled" that the worker later refuses into its log.
- C22 internal/testjail/profile.go: the test profiler measures real wall time and arms a real-time watchdog; a fake clock would hide the hang it exists to catch.
- C2 `internal/harvestpy/harvestpy_test.go` 1062 → 1063: the `testjail` import its executable writes need under C26 (`testjail.WriteExecutable`); the file already used its whole move slack.
- C3 `cmd/pfm` budget 8803: `chat new` and `chat branch` each call `plan.Report`, and `chat new --attach` calls `launchnotice.Pause` before the attach line, so the starter reads why the chat started without its fleet prompt and, on a terminal, presses Enter before the attach covers it (`internal/launchnotice` does the printing and the wait).
- C3 `cmd/pfm` budget 8814: the `--adopt-agents` flag in `cmd/pfm/install_command.go` (declaration, installer option, confirmation suffix) passes the engine-sync switch `plugins/engine-sync/docs/interface.md` § Global commands defines; `enginesync` does the argv.
- C3 `cmd/pfm` budget 8818: the `codex-transport` branch of `runInternal` (`cmd/pfm/main.go`) answers the shell's `codex` function with the transport `hookentry.CodexTransportArgs` decides, so a typed Codex runs in-process (`--no-daemon`) and its hooks reach its pane.
- C3 `cmd/pfm` budget 8826: `pfm restart` is a top-level verb (`restartCommand`, its `run` case, its usage line, the `internal/restart` import), and its detached worker is the `pfm internal restart-run` branch of `runInternal` (its `internalSubcommands` entry and C15 usage token on existing lines), so the worker's flags never sit on the public verb; `internal/restart` does the work.
