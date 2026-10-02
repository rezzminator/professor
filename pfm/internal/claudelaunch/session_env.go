package claudelaunch

import (
	"strconv"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// SessionEnv returns the plugin values and the launch shell (ShellEnv) for a
// passthrough Claude session.
func SessionEnv(prefs pfmconfig.ClaudePrefs) []string {
	return append([]string{
		envFunctionHooks + "=1",
		envAutoCompactWindow + "=" + strconv.FormatInt(prefs.AutoCompactWindow, 10),
	}, ShellEnv(paths.OSEnv{}.Lookup)...)
}
