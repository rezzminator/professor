package claudelaunch

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

// ConfigDirState classifies one account config dir. Launch, the install
// wiring, the host check and doctor read this one classification, so a dir
// one of them accepts is never refused by another.
type ConfigDirState string

const (
	ConfigDirOK         ConfigDirState = "ok"
	ConfigDirMissing    ConfigDirState = "missing"
	ConfigDirUnreadable ConfigDirState = "unreadable"
	ConfigDirNotDir     ConfigDirState = "not-dir"
	ConfigDirStore      ConfigDirState = "store"
)

// ConfigDir is the inspected state of one account config dir; Real is its
// resolved path once it resolves, Err the cause of ConfigDirUnreadable.
type ConfigDir struct {
	State ConfigDirState
	Real  string
	Err   error
}

// ConfigDirDefaultEnv names the sentinel the login default exports beside
// CLAUDE_CONFIG_DIR, holding the same value, only when the login shell found
// CLAUDE_CONFIG_DIR unset. Every pfm launch strips it (Hygiene,
// IdentityHygiene) and sets CLAUDE_CONFIG_DIR explicitly, so its presence
// means the value came from the login default, not from pfm or the operator.
const ConfigDirDefaultEnv = "PFM_CLAUDE_CONFIG_DIR_DEFAULT"

// InheritedConfigDir reports whether CLAUDE_CONFIG_DIR, read through getenv,
// is the login default: the sentinel is non-empty and names the same dir. A
// reader choosing an account or an engine treats it as unset; a reader of a
// Claude process's own environment keeps it, since that Claude runs on it.
func InheritedConfigDir(getenv func(string) string) bool {
	sentinel := strings.TrimSpace(getenv(ConfigDirDefaultEnv))
	value := strings.TrimSpace(getenv(configDirEnv))
	return sentinel != "" && value != "" && filepath.Clean(sentinel) == filepath.Clean(value)
}

// ClaudeStore is the shared Claude data directory.
func ClaudeStore(home string) string { return filepath.Join(home, ".claude") }

// InspectConfigDir resolves an account config dir through any symlink. It is
// usable when its real path is a directory that is neither store nor inside
// it. A link whose target cannot be resolved is unreadable, never missing:
// creating the dir would fail on the link, and the operator needs its cause.
func InspectConfigDir(store, dir string) ConfigDir {
	if _, err := os.Lstat(dir); errors.Is(err, os.ErrNotExist) {
		return ConfigDir{State: ConfigDirMissing}
	} else if err != nil {
		return ConfigDir{State: ConfigDirUnreadable, Err: err}
	}
	info, err := os.Stat(dir)
	if err != nil {
		return ConfigDir{State: ConfigDirUnreadable, Err: err}
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return ConfigDir{State: ConfigDirUnreadable, Err: err}
	}
	if !info.IsDir() {
		return ConfigDir{State: ConfigDirNotDir, Real: resolved}
	}
	storeReal := paths.PhysicalPath(store)
	if resolved == storeReal || strings.HasPrefix(resolved, storeReal+string(filepath.Separator)) {
		return ConfigDir{State: ConfigDirStore, Real: resolved}
	}
	return ConfigDir{State: ConfigDirOK, Real: resolved}
}
