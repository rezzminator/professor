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
// icon/color.
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
		// icon/color give the canonical PFM profile the same visual identity
		// the Professor extension's own contributed profile carries (15
		// cycling icons, 6 colours) — mortar-board/magenta is the single,
		// unchanging pair for the ONE settings profile pfm writes. Safe to add
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
