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
