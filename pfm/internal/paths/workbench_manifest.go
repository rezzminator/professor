package paths

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// HasWorkbenchManifest inspects a workbench marker without following links.
func HasWorkbenchManifest(dir string) (bool, error) {
	path := WorkbenchManifest(dir)
	for _, candidate := range []string{filepath.Dir(path), path} {
		info, err := os.Lstat(candidate)
		// ENOTDIR: a regular file named .professor holds no manifest.
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("inspect workbench %s: %w", candidate, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return false, nil
		}
	}
	return true, nil
}
