package claudelaunch

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"

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

// ConfigDirFromEnv reads the last account assignment, as the process does.
func ConfigDirFromEnv(env []string) string {
	for i := len(env) - 1; i >= 0; i-- {
		if value, found := strings.CutPrefix(env[i], configDirEnv+"="); found {
			return value
		}
	}
	return ""
}

// ConfigDirFromRun reads the registry's literal account assignment from a
// generated launch shell command. It handles quoting and compound launcher
// commands through the shell parser, without evaluating any shell code.
func ConfigDirFromRun(run string) (string, error) {
	file, err := syntax.NewParser().Parse(strings.NewReader(run), "launch")
	if err != nil {
		return "", fmt.Errorf("parse account launch command: %w", err)
	}
	var dir string
	syntax.Walk(file, func(node syntax.Node) bool {
		if err != nil {
			return false
		}
		if assign, ok := node.(*syntax.Assign); ok && assign.Name != nil && assign.Name.Value == configDirEnv {
			if assign.Value == nil {
				err = fmt.Errorf("account launch assignment has no value")
				return false
			}
			value, valueErr := expand.Literal(nil, assign.Value)
			if valueErr != nil || value == "" {
				err = fmt.Errorf("read account launch assignment: %v", valueErr)
				return false
			}
			if dir != "" && dir != value {
				err = fmt.Errorf("launch command names multiple Claude accounts")
				return false
			}
			dir = value
			return false
		}
		word, ok := node.(*syntax.Word)
		if !ok {
			return true
		}
		value, valueErr := expand.Literal(nil, word)
		if valueErr != nil {
			var rendered bytes.Buffer
			if printErr := syntax.NewPrinter().Print(&rendered, word); printErr != nil {
				err = fmt.Errorf("inspect account launch word: %w", printErr)
				return false
			}
			if strings.Contains(rendered.String(), configDirEnv+"=") {
				err = fmt.Errorf("account launch assignment must be literal: %w", valueErr)
			}
			return false
		}
		if candidate, found := strings.CutPrefix(value, configDirEnv+"="); found {
			if candidate == "" || (dir != "" && dir != candidate) {
				err = fmt.Errorf("launch command has an invalid Claude account assignment")
				return false
			}
			dir = candidate
		}
		return false
	})
	return dir, err
}
