package paths

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// EnvCacheKeepwarmStateDir overrides the cache-keepwarm plugin's sessions directory.
const EnvCacheKeepwarmStateDir = "CACHE_KEEPWARM_STATE_DIR"

// CacheKeepwarmStateFile mirrors cache-keepwarm's per-main-session JSON destination.
// Overrides are directories, not files. Refusing unsafe IDs keeps a payload from
// escaping the sessions directory; producer and consumer use the same rule.
func CacheKeepwarmStateFile(env Env, home, sessionID string) (string, error) {
	if sessionID == "" || strings.IndexFunc(sessionID, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-' && r != '_'
	}) >= 0 {
		return "", fmt.Errorf("cache-keepwarm state file: unsafe session id %q", sessionID)
	}
	dir := strings.TrimSpace(env.Get(EnvCacheKeepwarmStateDir))
	if dir == "" {
		root := strings.TrimSpace(env.Get("XDG_STATE_HOME"))
		if root == "" {
			if home == "" {
				return "", errors.New("cache-keepwarm state file: the home directory is unknown")
			}
			root = filepath.Join(home, ".local", "state")
		}
		dir = filepath.Join(root, "cache-keepwarm", "sessions")
	}
	if !filepath.IsAbs(dir) {
		return "", fmt.Errorf("cache-keepwarm state directory %q is not absolute", dir)
	}
	return filepath.Join(dir, sessionID+".json"), nil
}
