package installer

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

// Uninstall removes clone registry links from the machine store. Ownership
// follows the resolved target; operator files and foreign links are preserved.

const (
	globalCommandsRegistry = "commands"
	globalSkillsRegistry   = "skills"
	globalAgentsRegistry   = "agents"
)

// unwireGlobalRegistries removes pfm links from the store registries.
// Source-fetched links are removed first because their targets belong to managed storage.
func (installer *engine) unwireGlobalRegistries() error {
	// The source-fetched skills' links resolve into the pfm-owned store, not
	// the clone: they and the store go first (unwireSkillSources).
	if err := installer.unwireSkillSources(); err != nil {
		return err
	}
	repos, err := installer.recordedProfessorSourceRepos()
	if err != nil {
		return err
	}
	// The documented default clone location, the same fallback
	// retireDeadRegistryLinks appends: an uninstall run with neither
	// --repo nor a marker still has to find the links a normal install made.
	repos = append(repos, filepath.Join(installer.options.Home, ".professor"))
	config := installer.options.ConfigDir
	for _, registry := range []string{globalCommandsRegistry, globalSkillsRegistry, globalAgentsRegistry} {
		if err := installer.unwireGlobalRegistry(filepath.Join(config, registry), registry, repos); err != nil {
			return err
		}
	}

	return nil
}

func (installer *engine) unwireGlobalRegistry(registryDir, registry string, repos []string) error {
	entries, err := os.ReadDir(registryDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect global %s registry %s: %w", registry, registryDir, err)
	}
	for _, entry := range entries {
		path := filepath.Join(registryDir, entry.Name())
		info, err := os.Lstat(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect global %s link %s: %w", registry, path, err)
		}
		if info.Mode()&os.ModeSymlink == 0 {
			continue
		}
		target, err := os.Readlink(path)
		if err != nil {
			return fmt.Errorf("read global %s link %s: %w", registry, path, err)
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(path), target)
		}
		target = filepath.Clean(target)
		owned := ownedGlobalLink(repos, registry, entry.Name(), target)
		// A variant agent's link resolves into the pfm-owned generated
		// directory, never the clone — the install wrote it all the same.
		if registry == globalAgentsRegistry &&
			withinGlobalSource(target, paths.GeneratedClaudeAgentsDir(installer.options.Home)) {
			owned = true
		}
		if owned {
			if err := installer.retire(path, "machine-global "+registry+" link"); err != nil {
				return err
			}
			continue
		}
		shipped, err := shipsGlobalName(repos, registry, entry.Name())
		if err != nil {
			return err
		}
		if shipped {
			installer.skip(path + " points outside the recorded clone (-> " + target + "); preserved")
		}
	}
	return nil
}

// ownedGlobalLink reports whether a link resolves inside a clone global registry.
func ownedGlobalLink(repos []string, registry, name, target string) bool {
	for _, repo := range repos {
		if withinGlobalSource(target, filepath.Join(repo, "templates", "global", registry)) {
			return true
		}
	}
	return false
}

// shipsGlobalName reports whether one of the recorded clones actually ships a
// global of this name — the test that separates "a foreign link sitting on a
// pfm name" (worth naming) from an operator's own unrelated link (not this
// step's business). A source that cannot be inspected is an error, never a
// quiet "does not ship".
func shipsGlobalName(repos []string, registry, name string) (bool, error) {
	for _, repo := range repos {
		candidate := filepath.Join(repo, "templates", "global", registry, name)
		if _, err := os.Lstat(candidate); err == nil {
			return true, nil
		} else if !errors.Is(err, fs.ErrNotExist) {
			return false, fmt.Errorf("inspect global %s source %s: %w", registry, candidate, err)
		}
	}
	return false, nil
}

func withinGlobalSource(target, source string) bool {
	return target == source || strings.HasPrefix(target, source+string(filepath.Separator))
}
