package installer

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
)

func readMCPFile(path string) ([]byte, bool, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if info, statErr := os.Lstat(path); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
			return nil, false, fmt.Errorf("MCP config is a dangling symlink: %s", path)
		} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
			return nil, false, statErr
		}
		return nil, false, nil
	}
	return raw, err == nil, err
}

// Recheck the planned preimage before preserving a backup and atomically writing
// the physical file. Native clients may update their registry during installation.
// backedUpWritePaths names what a write through writeMCPFile or
// writeCodexConfig changes: the physical file (a config symlink is written
// through, never replaced) and, when it existed, the pre-professor sidecar the
// write keeps. An unresolvable link journals path itself; the write resolves
// it again and returns that error.
func (installer *engine) backedUpWritePaths(path string, existed bool) []string {
	if !existed {
		return []string{path}
	}
	target, err := filepath.EvalSymlinks(path)
	if err != nil {
		return []string{path}
	}
	return []string{target, availableBackup(target, installer.stamp)}
}

// changeMCPFile is change for one writeMCPFile write, journaling the file and
// its sidecar first.
func (installer *engine) changeMCPFile(message, path string, original, wanted []byte, existed bool) error {
	return installer.changePaths(message, installer.backedUpWritePaths(path, existed), func() error {
		return installer.writeMCPFile(path, original, wanted, existed)
	})
}

func (installer *engine) writeMCPFile(path string, original, wanted []byte, existed bool) error {
	latest, present, err := readMCPFile(path)
	if err != nil || present != existed || !bytes.Equal(latest, original) {
		return fmt.Errorf("MCP config changed while planning install: %s; retry", path)
	}
	if existed {
		target, err := filepath.EvalSymlinks(path)
		if err != nil {
			return err
		}
		path = target
		if err := copyBackup(path, availableBackup(path, installer.stamp)); err != nil {
			return err
		}
	}
	return atomicfile.Write(path, wanted, 0o600)
}
