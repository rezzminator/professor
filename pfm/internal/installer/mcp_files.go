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
