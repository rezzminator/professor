package installer

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/retired"
)

// retirePredecessors removes the retired lists' configFiles under the Claude
// config dir and their commands. A failed read is deferred once by Run.
func (installer *engine) retirePredecessors() error {
	lists, _ := installer.retiredLists()
	if err := installer.retireHandoffSkill(); err != nil {
		return err
	}
	if err := installer.retireLegacyCommands(lists.Commands); err != nil {
		return err
	}
	for _, file := range lists.ConfigFiles {
		if err := installer.retireGlob(
			filepath.Join(installer.options.ConfigDir, filepath.FromSlash(file.Path)), file.Reason,
		); err != nil {
			return err
		}
	}
	return nil
}

// Retire executable predecessors across the configured account roster while
// leaving account credentials, transcript directories, and live sockets alone.
func (installer *engine) retireLegacyCommands(commands retired.Commands) error {
	for _, name := range commands.HomeBin {
		if err := installer.retireLegacyCommand(
			filepath.Join(installer.options.Home, ".local", "bin", name),
		); err != nil {
			return err
		}
	}
	configDirs := []string{installer.options.ConfigDir}
	for _, account := range installer.options.ClaudeAccounts {
		configDirs = append(configDirs, account.ConfigDir)
	}
	for _, configDir := range configDirs {
		if strings.TrimSpace(configDir) == "" {
			continue
		}
		for _, name := range commands.AccountBin {
			if err := installer.retireLegacyCommand(filepath.Join(configDir, "bin", name)); err != nil {
				return err
			}
		}
	}
	return nil
}

// Retired command names are reserved, but their files may have been customized.
// Preserve regular files outside PATH before removing their old executable name.
func (installer *engine) retireLegacyCommand(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect retired command %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return installer.retire(path, "retired command link; target preserved")
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("refuse to retire non-regular command %s", path)
	}
	backup := availableBackup(
		filepath.Join(installer.options.Home, ".local", "state", "pfm", "retired-commands", filepath.Base(path)),
		installer.stamp,
	)
	return installer.change("retire "+path+" (backup: "+backup+")", func() error {
		if err := copyBackup(path, backup); err != nil {
			return fmt.Errorf("backup retired command %s: %w", path, err)
		}
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("remove retired command %s after backup %s: %w", path, backup, err)
		}
		return nil
	})
}

// retireHandoffSkill removes what the embedded /handoff skill left: the link
// {config}/skills/handoff/SKILL.md into the managed root, the then-empty
// skills/handoff directory, and the stale managed handoff.skill.md. It walks
// every Claude config directory the installer fans out to. A skills/handoff
// entry that is not that link stays. Not gated on live chats: it unlinks, as
// the installer does when it replaces its own links.
func (installer *engine) retireHandoffSkill() error {
	owned := filepath.Join(installer.managedRoot, "handoff.skill.md")
	configDirs := []string{installer.options.ConfigDir}
	for _, account := range installer.options.ClaudeAccounts {
		configDirs = append(configDirs, account.ConfigDir)
	}
	seen := map[string]bool{}
	for _, configDir := range configDirs {
		if strings.TrimSpace(configDir) == "" {
			continue
		}
		dir := filepath.Join(paths.PhysicalPath(configDir), "skills", "handoff")
		if seen[dir] {
			continue
		}
		seen[dir] = true
		link := filepath.Join(dir, "SKILL.md")
		if target, linked := resolvedLink(link); linked && paths.PhysicalPath(target) == paths.PhysicalPath(owned) {
			if err := installer.retire(link, "the /handoff skill is a global command now"); err != nil {
				return err
			}
			if err := installer.retireEmptyDirTolerant(dir); err != nil {
				return err
			}
		}
	}
	return installer.retire(owned, "embedded /handoff skill retired")
}
