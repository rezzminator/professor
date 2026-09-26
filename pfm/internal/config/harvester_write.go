package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
)

// writeAtomic is the harvester's config writer (harvester.go, migration.go), kept verbatim:
// the harvester is out of the atomicfile migration by decision, so this file
// stays in the C6 baseline. Every other config write goes through atomicfile.
func writeAtomic(path string, content []byte) (returnErr error) {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create config directory %s: %w", directory, err)
	}
	if err := atomicfile.Write(path, content, 0o600); err != nil {
		return fmt.Errorf("install config %s: %w", path, err)
	}
	return nil
}
