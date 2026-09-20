package installer

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"hostops/pfm/internal/paths"
)

// This file is the uninstall half of the machine-global fan-out in
// installer.go (wireGlobalCommands) and global_fanout.go (wireGlobalSkills).
// Install links every entry of <clone>/templates/global/commands and every
// global skill directory — plus the in-tree workflows/deep-rr skill — into
// the commands/ and skills/ registry of EVERY configured Claude account.
// Uninstall used to remove none of them, so a removed install still resolved
// /wave:*, /quality:* and the global skills into the clone from every
// account, against INSTALL.md's promise that uninstall removes the
// installer-owned links.
//
// Ownership is decided by the link's TARGET, never by its name — the rule
// unwireGeneratedCodexAgents and retireOrphanGlobalCommands already hold to.
// Only a symlink resolving into <clone>/templates/global/<registry> (or, for
// skills, at <clone>/workflows/<name>) is ours. A regular file is never a
// candidate, and a foreign link is kept — named when it occupies the name of
// a global this clone ships, so a kept entry is a reported decision rather
// than a silent omission.

const (
	globalCommandsRegistry = "commands"
	globalSkillsRegistry   = "skills"
	globalAgentsRegistry   = "agents"
)

// unwireGlobalRegistries removes the machine-global command, skill, and
// agent links from every configured Claude account. Agents belong here
// alongside commands and skills: wireCodexAgents (installer.go) links every
// <clone>/templates/global/agents/<name>.md into {config}/agents/<name>.md
// through the same ownership-by-target rule, and a run that skipped the
// agents/ registry left `~/.claude/agents/*.md` symlinks behind after a
// successful `pfm uninstall` — the same defect this file's commit fixed for
// commands and skills, at the one registry this loop had not yet visited.
func (installer *engine) unwireGlobalRegistries() error {
	repos, err := installer.recordedProfessorSourceRepos()
	if err != nil {
		return err
	}
	// The documented default clone location, the same fallback
	// retireOrphanGlobalCommands appends: an uninstall run with neither
	// --repo nor a marker still has to find the links a normal install made.
	repos = append(repos, filepath.Join(installer.options.Home, ".professor"))
	for _, config := range installer.claudeConfigDirs() {
		for _, registry := range []string{globalCommandsRegistry, globalSkillsRegistry, globalAgentsRegistry} {
			if err := installer.unwireGlobalRegistry(filepath.Join(config, registry), registry, repos); err != nil {
				return err
			}
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

// ownedGlobalLink reports whether target is a link the global fan-out
// created: one resolving into this clone's templates/global/<registry>, or —
// for skills only — the in-tree workflow directory wireGlobalSkills links by
// name.
func ownedGlobalLink(repos []string, registry, name, target string) bool {
	for _, repo := range repos {
		if withinGlobalSource(target, filepath.Join(repo, "templates", "global", registry)) {
			return true
		}
		if registry == globalSkillsRegistry && target == filepath.Join(repo, "workflows", name) {
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
		candidates := []string{filepath.Join(repo, "templates", "global", registry, name)}
		if registry == globalSkillsRegistry {
			candidates = append(candidates, filepath.Join(repo, "workflows", name))
		}
		for _, candidate := range candidates {
			if _, err := os.Lstat(candidate); err == nil {
				return true, nil
			} else if !errors.Is(err, fs.ErrNotExist) {
				return false, fmt.Errorf("inspect global %s source %s: %w", registry, candidate, err)
			}
		}
	}
	return false, nil
}

func withinGlobalSource(target, source string) bool {
	return target == source || strings.HasPrefix(target, source+string(filepath.Separator))
}
