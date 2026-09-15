package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// writeAtomic is the harvester's config writer (harvester.go, migration.go), kept verbatim:
// the harvester is out of the atomicfile migration by decision, so this file
// stays in the C6 baseline. Every other config write goes through atomicfile.
func writeAtomic(path string, content []byte) (returnErr error) {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create config directory %s: %w", directory, err)
	}
	file, err := os.CreateTemp(directory, ".config.json.tmp-*")
	if err != nil {
		return fmt.Errorf("create config scratch beside %s: %w", path, err)
	}
	temporary := file.Name()
	defer func() {
		if err := os.Remove(temporary); err != nil && !errors.Is(err, fs.ErrNotExist) {
			returnErr = errors.Join(returnErr, fmt.Errorf("remove config scratch %s: %w", temporary, err))
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return fmt.Errorf("secure config scratch %s: %w", temporary, err)
	}
	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		return fmt.Errorf("write config scratch %s: %w", temporary, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close config scratch %s: %w", temporary, err)
	}
	if err := os.Rename(temporary, path); err != nil {
		return fmt.Errorf("install config %s: %w", path, err)
	}
	return nil
}
