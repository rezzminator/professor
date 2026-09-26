package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// LoadSeed loads the config `pfm install` would create at target by copying
// example, without writing target: an install preview classifies against the
// same config the apply reloads after seeding. The example is loaded through a
// private copy named FileName, beside a link to target's harvester.config.json
// when one exists, so every value derived from the sibling files matches a load
// of target; the returned Path and Harvester.Path name target's files.
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
	harvester := HarvesterPath(target)
	if _, statErr := os.Stat(harvester); statErr == nil {
		if err := os.Symlink(harvester, HarvesterPath(staged)); err != nil {
			return Config{}, fmt.Errorf("stage harvester config %s: %w", harvester, err)
		}
	} else if !errors.Is(statErr, fs.ErrNotExist) {
		return Config{}, fmt.Errorf("inspect harvester config %s: %w", harvester, statErr)
	}
	loaded, err := Load(staged, home, projectRoots, codexHomes...)
	if err != nil {
		return Config{}, fmt.Errorf("load seed config %s for %s: %w", example, target, err)
	}
	loaded.Path = filepath.Clean(target)
	loaded.Harvester.Path = harvester
	return loaded, nil
}
