package paths

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// EnvKeepwarmStateDir overrides the keep-warm shield's (cache-live-control plugin) sessions directory.
const EnvKeepwarmStateDir = "CACHE_LIVE_CONTROL_KEEPWARM_STATE_DIR"

// KeepwarmStateFile mirrors the keep-warm shield's (cache-live-control plugin) per-main-session JSON destination:
// EnvKeepwarmStateDir, else XDG_STATE_HOME's, else home's sessions directory. As the writer
// (src/keepwarm/status.ts stateDirectory) does, a relative value is skipped for the next one, so the
// reader looks where the writer wrote. Overrides are directories, not files. Refusing unsafe IDs keeps
// a payload from escaping the sessions directory; producer and consumer use the same rule.
func KeepwarmStateFile(env Env, home, sessionID string) (string, error) {
	if sessionID == "" || strings.IndexFunc(sessionID, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-' && r != '_'
	}) >= 0 {
		return "", fmt.Errorf("keepwarm state file: unsafe session id %q", sessionID)
	}
	sessions := filepath.Join("cache-live-control", "keepwarm", "sessions")
	var relative []string
	for _, source := range []struct{ name, value, suffix string }{
		{EnvKeepwarmStateDir, env.Get(EnvKeepwarmStateDir), ""},
		{"XDG_STATE_HOME", env.Get("XDG_STATE_HOME"), sessions},
		{"the home directory", home, filepath.Join(".local", "state", sessions)},
	} {
		value := strings.TrimSpace(source.value)
		if value == "" {
			continue
		}
		if !filepath.IsAbs(value) {
			relative = append(relative, source.name)
			continue
		}
		return filepath.Join(value, source.suffix, sessionID+".json"), nil
	}
	if len(relative) != 0 {
		return "", fmt.Errorf("keepwarm state directory unresolved: %s not absolute", strings.Join(relative, ", "))
	}
	return "", errors.New("keepwarm state file: the home directory is unknown")
}
