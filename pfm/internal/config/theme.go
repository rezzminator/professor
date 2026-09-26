package config

import (
	"fmt"
	"strings"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// applyTheme resolves claude.theme for one scope. Present means override —
// the raw value must trim to a non-empty string, the same rule Binary
// enforces in decodeClaudePrefs, in the same error voice (the caller wraps
// the returned error in "config %s: %w" with result.Path, so the final
// message reads identically to a validation done inline); absent leaves
// prefs.Theme at its zero value. Unlike NativeCursor/Cache1H's inherited
// bool, EffectiveClaude copies Theme the way it copies Binary — only when an
// account's resolved value is non-empty (config.go:1320) — so no false-zero
// seeding is needed here: an account whose claude block never mentioned
// theme keeps the empty string, and EffectiveClaude falls through to the
// already-resolved top-level value on its own.
func applyTheme(prefs *ClaudePrefs, raw *string, scope string, index int, sources map[string]Source) error {
	if raw == nil {
		return nil
	}
	if strings.TrimSpace(*raw) == "" {
		return fmt.Errorf("%s.theme must be a non-empty string", configScope(scope, index))
	}
	prefs.Theme = *raw
	key := engineConfigKey(pfmengine.Claude, "theme")
	if index >= 0 {
		key = fmt.Sprintf("accounts[%d].claude.theme", index)
	}
	sources[key] = SourceFile
	return nil
}

// themeMarshalValue renders theme for Marshal's map[string]any tree as JSON
// null when empty, never a literal "" — applyTheme's raw==nil branch reads
// null back as unset, while "" would re-fail the non-empty check and break
// the round trip for every account that has never set a theme.
func themeMarshalValue(theme string) any {
	if theme == "" {
		return nil
	}
	return theme
}
