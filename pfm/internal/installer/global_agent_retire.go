package installer

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/codexgen"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// retireOrphanGlobalAgents prunes undeclared variants and their generated files.
func (installer *engine) retireOrphanGlobalAgents(installed []codexgen.GlobalAgentInstalled) error {
	generatedDir := paths.GeneratedClaudeAgentsDir(installer.options.Home)
	keep := make(map[string]bool, len(installed))
	for _, entry := range installed {
		if withinGlobalSource(entry.Source, generatedDir) {
			keep[filepath.Base(entry.Source)] = true
		}
	}

	config := installer.options.ConfigDir
	registry := filepath.Join(config, "agents")
	entries, err := os.ReadDir(registry)
	if errors.Is(err, fs.ErrNotExist) {
		entries, err = nil, nil
	}
	if err != nil {
		return fmt.Errorf("inspect global agent registry %s: %w", registry, err)
	}
	for _, entry := range entries {
		path := filepath.Join(registry, entry.Name())
		info, err := os.Lstat(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect global agent %s: %w", path, err)
		}
		if info.Mode()&os.ModeSymlink == 0 {
			continue
		}
		target, err := os.Readlink(path)
		if err != nil {
			return fmt.Errorf("read global agent link %s: %w", path, err)
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(path), target)
		}
		target = filepath.Clean(target)

		if withinGlobalSource(target, generatedDir) {
			if !keep[filepath.Base(target)] {
				if err := installer.retire(
					path,
					"retired global agent variant — "+entry.Name()+" is no longer declared in variants.json",
				); err != nil {
					return err
				}
			}
			continue
		}

	}

	generatedEntries, err := os.ReadDir(generatedDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect generated global agent variants %s: %w", generatedDir, err)
	}
	for _, entry := range generatedEntries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		if keep[entry.Name()] {
			continue
		}
		if err := installer.retire(
			filepath.Join(generatedDir, entry.Name()),
			"undeclared generated agent variant",
		); err != nil {
			return err
		}
	}
	return nil
}

func (installer *engine) retireRenamedCodexAgents() error {
	for _, home := range installer.codexHomes() {
		for _, name := range retiredGlobalAgents {
			path := filepath.Join(home, "agents", name+".toml")
			content, err := os.ReadFile(path)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return fmt.Errorf("read retired Codex agent %s: %w", path, err)
			}
			if !codexgen.GeneratedGlobalRole(content) {
				installer.skip(path + " is not a generated agent; preserved")
				continue
			}
			if err := installer.retire(path, "renamed global agent"); err != nil {
				return err
			}
		}
	}
	return nil
}
