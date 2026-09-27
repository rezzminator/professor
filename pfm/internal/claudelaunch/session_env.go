package claudelaunch

import (
	"strconv"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

// SessionEnv returns the plugin values for a passthrough Claude session.
func SessionEnv(prefs pfmconfig.ClaudePrefs) []string {
	return []string{
		envFunctionHooks + "=1",
		envAutoCompactWindow + "=" + strconv.FormatInt(prefs.AutoCompactWindow, 10),
	}
}
