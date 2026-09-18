package config

import (
	"fmt"

	pfmengine "hostops/pfm/internal/engine"
)

// applyNativeCursor resolves claude.nativeCursor for one scope. When true,
// every Claude launch carries CLAUDE_CODE_NATIVE_CURSOR=1, so the terminal
// draws its own cursor instead of Claude Code painting one; the default is
// false, which leaves the launch environment as it is.
//
// index -1 is the top-level claude block: an unset key keeps the default.
// index >= 0 is accounts[index].claude: an unset key inherits the resolved
// top-level value (inherited) rather than the bool zero value, so
// EffectiveClaude can apply the account value unconditionally — the same
// false-zero trap cache1h has.
func applyNativeCursor(prefs *ClaudePrefs, raw *bool, inherited bool, sources map[string]Source, index int) {
	if raw == nil {
		if index >= 0 {
			prefs.NativeCursor = inherited
		}
		return
	}
	prefs.NativeCursor = *raw
	key := engineConfigKey(pfmengine.Claude, "nativeCursor")
	if index >= 0 {
		key = fmt.Sprintf("accounts[%d].claude.nativeCursor", index)
	}
	sources[key] = SourceFile
}
