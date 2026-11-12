// Package claudelaunch owns the registry and rendering of managed Claude launches.
//
// Entry points: Render, Parse, Resolve, RenderHeadless. Files, one line each:
//   - knobs.go is the launch registry: every flag, env value and --settings key a Claude chat starts with.
//   - render.go renders one managed launch (Render) and checks an account's config dir.
//   - owner_settings.go folds the caller's own --settings into the one --settings a launch carries.
//   - parse.go decodes a Claude command line; remote_control.go reapplies Remote Control to argv.
//   - resolve.go reports each registry row's value and winning source.
//   - headless.go renders per-run preferences of a headless launch.
//   - hooks.go is the hook templates a launch registers; mcpfile.go writes the MCP config file.
//   - configdir.go inspects an account's config dir and the login default's sentinel.
//   - cost.go arms the cost ledger's OpenTelemetry exporters.
//   - fullscreen.go, theme.go and shell.go decide the fullscreen, theme pin and launch shell.
//   - session_env.go is a session's plugin values and shell; probe.go isolates the doctor's capture.
package claudelaunch
