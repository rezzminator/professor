# Ruled arch exceptions

A baseline entry that a wave ADDED (not measured from the old tree) carries its reason here, one line each: check, file, reason. The ratchet never reads this file.

- C11 `internal/paths/paths.go`: the legacy `fleet.db` literal (`legacyDBName`, read through `paths.LegacyStateDB` and `paths.LegacyCacheDB`) is how the layout move finds and migrates the old database files.
- C17 `parse`, `render`, `renderheadless`, `resolve` in `internal/claudelaunch/`: Parse/Render/RenderHeadless/Resolve are the names docs/design/engines/claude-launch.md mandates.
- C23 `harness-prompts/compose/main.go`: a build-time CLI whose stderr is its interface.
- C23 `internal/statusline/cache_window.go`: the statusline writes nothing to stdout but the rendered line, so launch-record failures go to stderr beside the visible `💾⚠` segment marker.
