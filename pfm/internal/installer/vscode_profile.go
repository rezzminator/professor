package installer

import (
	"reflect"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
)

const (
	vscodePathKey        = "path"
	vscodeProfileArgsKey = "args"
	vscodeProfileEnvKey  = "env"
	vscodeShellPath      = "/bin/zsh"
	vscodeAutoOpenEnv    = "PFM_AUTO_OPEN"
)

// vscodeLegacyProfiles lists every FULL profile shape pfm has EVER written as
// the canonical "PFM" profile, oldest first. A profile that matches one of
// these exactly is pfm's own earlier install caught up by an upgrade, not an
// operator edit — see isLegacyVSCodeProfile. Each entry is appended, never
// rewritten, the day vscodeProfile's own current shape changes: the previous
// CURRENT shape becomes a new legacy entry so an install still holding it
// keeps upgrading. The first two predate the chat-identity env keys below;
// the third is the shape vscodeProfile() itself wrote before M8 added
// icon/color; the fourth is the shape it wrote after M8, while the identity
// env still held three names, before IdentityHygiene grew to five.
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
	{vscodePathKey: vscodeShellPath, vscodeProfileArgsKey: []any{"-l"}, vscodeProfileEnvKey: vscodeLegacyIdentityEnv()},
	{
		vscodePathKey:        vscodeShellPath,
		vscodeProfileArgsKey: []any{"-l"},
		vscodeProfileEnvKey:  vscodeLegacyIdentityEnv(),
		"icon":               "mortar-board",
		"color":              "terminal.ansiMagenta",
	},
}

func vscodeLegacyIdentityEnv() map[string]any {
	env := map[string]any{vscodeAutoOpenEnv: MCPClientPFM, "TMUX": nil, "TMUX_PANE": nil}
	for _, name := range claudelaunch.IdentityHygiene()[:3] {
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
		// deletes an inherited env var. The registry supplies the identity
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
		"icon":  "mortar-board",
		"color": "terminal.ansiMagenta",
	}
}

func vscodeIdentityEnv() map[string]any {
	env := map[string]any{vscodeAutoOpenEnv: MCPClientPFM, "TMUX": nil, "TMUX_PANE": nil}
	for _, name := range claudelaunch.IdentityHygiene() {
		env[name] = nil
	}
	return env
}
