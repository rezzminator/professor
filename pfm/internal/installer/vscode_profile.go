package installer

import "reflect"

const (
	vscodePathKey         = "path"
	vscodeProfileArgsKey  = "args"
	vscodeProfileEnvKey   = "env"
	vscodeProfileIconKey  = "icon"
	vscodeProfileColorKey = "color"
	vscodeProfileIcon     = "mortar-board"
	vscodeProfileColor    = "terminal.ansiMagenta"
	vscodeShellPath       = "/bin/zsh"
	vscodeAutoOpenEnv     = "PFM_AUTO_OPEN"
)

// vscodeLegacyProfiles lists every FULL profile shape pfm has EVER written as
// the canonical "PFM" profile, oldest first. A profile that matches one of
// these exactly is pfm's own earlier install caught up by an upgrade, not an
// operator edit — see isLegacyVSCodeProfile. Each entry is appended, never
// rewritten, the day vscodeProfile's own current shape changes: the previous
// CURRENT shape becomes a new legacy entry so an install still holding it
// keeps upgrading. The five entries are CC_AUTO_OPEN only, PFM_AUTO_OPEN
// only, three identity names without icon/color, those three with icon/color
// (v0.78.0), and five identity names with icon/color (develop). Canonical
// identity names are literal and pinned equal to claudelaunch.IdentityHygiene()
// by a test. When it fails, append the current shape here before updating
// the canonical literals; legacy entries never follow the live registry.
var vscodeLegacyProfiles = []map[string]any{
	{
		vscodePathKey:        vscodeShellPath,
		vscodeProfileArgsKey: []any{"-l"},
		vscodeProfileEnvKey:  map[string]any{"CC_AUTO_OPEN": MCPClientPFM},
	},
	{
		vscodePathKey:        vscodeShellPath,
		vscodeProfileArgsKey: []any{"-l"},
		vscodeProfileEnvKey:  map[string]any{vscodeAutoOpenEnv: MCPClientPFM},
	},
	{
		vscodePathKey:        vscodeShellPath,
		vscodeProfileArgsKey: []any{"-l"},
		vscodeProfileEnvKey:  vscodeNullEnv("CLAUDE_CODE_SESSION_ID", "CLAUDECODE", "CLAUDE_CODE_CHILD_SESSION"),
	},
	{
		vscodePathKey:         vscodeShellPath,
		vscodeProfileArgsKey:  []any{"-l"},
		vscodeProfileEnvKey:   vscodeNullEnv("CLAUDE_CODE_SESSION_ID", "CLAUDECODE", "CLAUDE_CODE_CHILD_SESSION"),
		vscodeProfileIconKey:  vscodeProfileIcon,
		vscodeProfileColorKey: vscodeProfileColor,
	},
	{
		vscodePathKey:        vscodeShellPath,
		vscodeProfileArgsKey: []any{"-l"},
		vscodeProfileEnvKey: vscodeNullEnv(
			"CLAUDE_CODE_SESSION_ID", "CLAUDECODE", "CLAUDE_CODE_CHILD_SESSION", claudeConfigDirEnv, "CODEX_THREAD_ID",
		),
		vscodeProfileIconKey:  vscodeProfileIcon,
		vscodeProfileColorKey: vscodeProfileColor,
	},
}

func vscodeNullEnv(names ...string) map[string]any {
	env := map[string]any{vscodeAutoOpenEnv: MCPClientPFM, "TMUX": nil, "TMUX_PANE": nil}
	for _, name := range names {
		env[name] = nil
	}
	return env
}

func isLegacyVSCodeProfile(profile any) bool {
	for _, legacy := range vscodeLegacyProfiles {
		if reflect.DeepEqual(profile, legacy) {
			return true
		}
	}
	return false
}

// vscodeProfileConflicts reports whether an existing "PFM" profile blocks
// `pfm install --vscode` when pfm does not own it: it is neither the current
// canonical shape nor one pfm ever wrote (vscodeLegacyProfiles), so it is the
// operator's own and is never overwritten. mergeVSCodeSettings refuses on it
// and InspectVSCode reports it, so the installer's verdict and doctor's fix
// are one rule.
func vscodeProfileConflicts(profile any, hasProfile bool) bool {
	return hasProfile && !reflect.DeepEqual(profile, vscodeProfile()) && !isLegacyVSCodeProfile(profile)
}

func vscodeProfile() map[string]any {
	return map[string]any{
		vscodePathKey:        vscodeShellPath,
		vscodeProfileArgsKey: []any{"-l"},
		// A terminal opened straight from this profile is a shell the operator
		// typed into, never a nested chat — but it inherits VS Code's own
		// process env, which (when VS Code was itself launched from inside a
		// chat) carries that chat's identity markers. `null` is how VS Code
		// deletes an inherited env var. The pinned literals supply the identity
		// markers, alongside the TMUX pair naming its tmux server.
		vscodeProfileEnvKey: vscodeIdentityEnv(),
		// icon/color mark a terminal this settings profile still builds —
		// one VS Code creates without running workbench.action.terminal.new:
		// Ctrl+` on an empty panel, a split, a double-click on the tabs' empty
		// area, the + dropdown's plain "New Terminal" entry, or a + pressed
		// before the extension host is up — with the single, unchanging
		// mortar-board/magenta pair, outside the extension's cycle (15 icons,
		// 6 colours), which the + button and Ctrl+Shift+` reach through the
		// extension. Safe to add
		// unconditionally because of the relinquish rule above: an operator's
		// own edit to this profile after install is never fought.
		vscodeProfileIconKey:  vscodeProfileIcon,
		vscodeProfileColorKey: vscodeProfileColor,
	}
}

func vscodeIdentityEnv() map[string]any {
	return vscodeNullEnv(
		"CLAUDE_CODE_SESSION_ID", "CLAUDECODE", "CLAUDE_CODE_CHILD_SESSION",
		claudeConfigDirEnv, "PFM_CLAUDE_CONFIG_DIR_DEFAULT", "CODEX_THREAD_ID",
	)
}
