package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// LoadSeed previews the config pfm install would seed without writing target.
func LoadSeed(example, target, home string, projectRoots []string, codexHomes ...string) (_ Config, err error) {
	content, err := os.ReadFile(example)
	if err != nil {
		return Config{}, fmt.Errorf("read seed config %s: %w", example, err)
	}
	dir, err := os.MkdirTemp("", "pfm-seed-")
	if err != nil {
		return Config{}, fmt.Errorf("create seed preview directory: %w", err)
	}
	defer func() {
		if removeErr := os.RemoveAll(dir); removeErr != nil {
			err = errors.Join(err, fmt.Errorf("remove seed preview directory %s: %w", dir, removeErr))
		}
	}()
	staged := filepath.Join(dir, FileName)
	if err := os.WriteFile(staged, content, 0o600); err != nil {
		return Config{}, fmt.Errorf("stage seed config %s: %w", staged, err)
	}
	loaded, err := Load(staged, home, projectRoots, codexHomes...)
	if err != nil {
		return Config{}, fmt.Errorf("load seed config %s for %s: %w", example, target, err)
	}
	loaded.SeedContent = append([]byte(nil), content...)
	loaded.Path = filepath.Clean(target)
	return loaded, nil
}
