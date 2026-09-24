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

// retireOrphanGlobalAgents prunes the registry links (and generated variant
// files) the Claude-side half of wireCodexAgents' fan-out created for agents
// the clone no longer ships. That fan-out links every ORIGINAL agent straight
// from <clone>/templates/global/agents/<name>.md, and renders every VARIANT
// variants.json declares into paths.GeneratedClaudeAgentsDir before linking
// it the same way (paths.GeneratedClaudeAgentsDir's own doc comment). Wiring
// forms no opinion about a name it has stopped declaring, so a variant
// removed from variants.json — or an original agent deleted from the clone —
// otherwise leaves a stale {config}/agents/<name>.md link (and, for a
// variant, its rendered file) behind forever, the same defect
// retireOrphanGlobalCommands closed for commands.
//
// Ownership is decided by the link's TARGET, never by its name, matching
// every other retire path in this package:
//   - a link resolving into paths.GeneratedClaudeAgentsDir is a variant; it
//     retires when its rendered file's name is no longer among the variants
//     this run's plan actually installed (installed.Source within that same
//     directory is the currently-declared roster, read from the plan rather
//     than re-parsed from variants.json, so this function agrees with
//     whatever wireCodexAgents just planned).
//   - a link resolving at <recorded professor repo>/templates/global/
//     agents/<its own name> is an original; it retires only when that source
//     file no longer exists.
//   - anything else — a regular file, a link resolving elsewhere, a live
//     link — is untouched, preserving an operator's own agent the same way
//     retireOrphanGlobalCommands preserves an operator's own command.
//
// A generated variant file with no surviving link (its variants.json entry
// removed entirely) is swept in the same pass: every *.md file directly
// under paths.GeneratedClaudeAgentsDir whose name is not in the currently
// declared roster is retired too.
//
// Its own broken states are distinguishable at the surface: a registry that
// cannot be read, a link that cannot be resolved, and a source that cannot
// be stat'd each return a wrapped error naming the path, so a failed look
// never renders as the silent no-op of a registry that simply had no orphan.
func (installer *engine) retireOrphanGlobalAgents(installed []codexgen.GlobalAgentInstalled) error {
	repos, err := installer.recordedProfessorSourceRepos()
	if err != nil {
		return err
	}
	repos = append(repos, filepath.Join(installer.options.Home, ".professor"))

	generatedDir := paths.GeneratedClaudeAgentsDir(installer.options.Home)
	keep := make(map[string]bool, len(installed))
	for _, entry := range installed {
		if withinGlobalSource(entry.Source, generatedDir) {
			keep[filepath.Base(entry.Source)] = true
		}
	}

	for _, config := range installer.claudeConfigDirs() {
		registry := filepath.Join(config, "agents")
		entries, err := os.ReadDir(registry)
		if errors.Is(err, fs.ErrNotExist) {
			continue
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

			owned := false
			for _, repo := range repos {
				if target == filepath.Join(repo, "templates", "global", "agents", entry.Name()) {
					owned = true
					break
				}
			}
			if !owned {
				continue
			}
			if _, err := os.Stat(target); err == nil {
				continue
			} else if !errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("inspect global agent source %s: %w", target, err)
			}
			if err := installer.retire(path, "retired global agent — "+target+" no longer ships"); err != nil {
				return err
			}
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
