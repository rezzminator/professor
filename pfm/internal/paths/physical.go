package paths

import (
	"errors"
	"os"
	"path/filepath"
)

// PhysicalPath resolves the deepest existing ancestor and keeps a missing tail.
func PhysicalPath(path string) string {
	candidate := filepath.Clean(path)
	var missing []string
	for {
		physical, err := filepath.EvalSymlinks(candidate)
		if err == nil {
			for i := len(missing) - 1; i >= 0; i-- {
				physical = filepath.Join(physical, missing[i])
			}
			return filepath.Clean(physical)
		}
		parent := filepath.Dir(candidate)
		if !errors.Is(err, os.ErrNotExist) || parent == candidate {
			return filepath.Clean(path)
		}
		missing = append(missing, filepath.Base(candidate))
		candidate = parent
	}
}
