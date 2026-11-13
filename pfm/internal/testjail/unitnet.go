package testjail

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

// unitNetArmed is the unit net's one switch. On, a unit binary (tier_unit.go) runs on any host,
// inside the net unitNet lays before its first test; off, it re-runs in the fence as a jail binary
// does (fence.go). Off until the unit-isolation flights that convert the last unit tests still
// reaching the machine have landed: armed, each such test fails naming the variable it reached.
const unitNetArmed = false

// unitNetDirs point, each, at an empty directory of its own name inside the net: a test that still
// reads a home file, resolves a tool through PATH or dials a tmux socket fails on a path that names
// the variable it came through, and never reaches the machine's own.
var unitNetDirs = []string{
	"HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "XDG_RUNTIME_DIR",
	paths.EnvTmuxTmpDir, "PATH",
}

// ambientIdentity is the live seat a test process could inherit from the shell that started it: a
// tmux pane, a chat socket, a host Claude or Codex session, a systemd service (INVOCATION_ID, which
// a hosted CI runner's job inherits and which routes every chat spawn through systemd-run). The
// fence has none of them; a test that needs one sets it with t.Setenv. Literal names: testjail
// cannot import internal/resolve (import cycle).
var ambientIdentity = []string{
	"TMUX", "TMUX_PANE", "CHAT_CALL_SOCKET", "CLAUDE_CODE_SESSION_ID", "CODEX_THREAD_ID", "INVOCATION_ID",
}

// clearAmbientIdentity empties every ambientIdentity variable through setenv.
func clearAmbientIdentity(setenv func(name, value string) error) error {
	for _, name := range ambientIdentity {
		if err := setenv(name, ""); err != nil {
			return fmt.Errorf("clear %s: %w", name, err)
		}
	}
	return nil
}

// armUnitNet points every unitNetDirs variable at a directory of its name under dir and clears the
// ambient identity, through setenv.
func armUnitNet(dir string, setenv func(name, value string) error) error {
	for _, name := range unitNetDirs {
		path := filepath.Join(dir, name)
		if err := os.Mkdir(path, 0o700); err != nil {
			return fmt.Errorf("create the unit net's %s directory %s: %w", name, path, err)
		}
		if err := setenv(name, path); err != nil {
			return fmt.Errorf("point %s at %s: %w", name, path, err)
		}
	}
	return clearAmbientIdentity(setenv)
}

// unitNet lays the net under root for an armed unit binary: TMPDIR becomes a directory of the
// binary's own (a one-letter prefix, as unix socket paths under it keep macOS's 104 bytes) and
// armUnitNet's directories live in it. It returns the root the rest of Run builds under and the
// net's removal; any other binary keeps root and removes nothing.
func unitNet(root string) (string, func(), error) {
	if !unitBinary || !unitNetArmed {
		return root, func() {}, nil
	}
	dir, err := os.MkdirTemp(root, "u")
	if err != nil {
		return root, func() {}, fmt.Errorf("create the unit net under %s: %w", root, err)
	}
	remove := func() {
		if err := os.RemoveAll(dir); err != nil && !errors.Is(err, fs.ErrNotExist) {
			warnSetup("remove the unit net %s: %v", dir, err)
		}
	}
	if err := os.Setenv("TMPDIR", dir); err != nil {
		return dir, remove, fmt.Errorf("set TMPDIR to %s: %w", dir, err)
	}
	return dir, remove, armUnitNet(dir, os.Setenv)
}
