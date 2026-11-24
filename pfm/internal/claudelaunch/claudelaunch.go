// Package claudelaunch owns the registry and rendering of managed Claude launches.
//
// Entry points: Render, Parse, Resolve, WriteResolved, RenderHeadless. Files, one line each:
//   - knobs.go is the launch registry: every flag, env value and --settings key a Claude chat starts with.
//   - defaults.go loads defaults.json: the plain settings every launch carries, the registry values install sets,
//     and the effective settings and env claude.settings lays over them; defaults_user_settings.go the
//     settings install also writes into each account's settings.json (UserSettingDefaults).
//   - render.go renders one managed launch (Render) and checks an account's config dir.
//   - owner_settings.go folds the project's claude.settings and the caller's --settings into the launch's one.
//   - parse.go decodes a Claude command line; remote_control.go reapplies Remote Control to argv.
//   - resolve.go reports each registry row's value and winning source; resolve_extra.go the claude.settings
//     no row names, and writes both as `pfm config claude` shows them.
//   - headless.go renders per-run preferences of a headless launch.
//   - hooks.go is the hook templates a launch registers; mcpfile.go writes the MCP config file.
//   - configdir.go inspects an account's config dir and the login default's sentinel.
//   - cost.go arms the cost ledger's OpenTelemetry exporters.
//   - fullscreen.go, theme.go and shell.go decide the fullscreen, theme pin and launch shell; login_path.go
//     merges in the login shell's PATH entries a launch lacks; login_path_cache.go holds each shell's answer;
//     login_environment.go builds the clean login environment and home folder the probe runs with.
//   - session_env.go is a session's plugin values and shell; probe.go isolates the doctor's capture.
package claudelaunch
