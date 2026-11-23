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
	if err := installer.retireEmbeddedAssets(); err != nil {
		return err
	}
	if err := installer.retireLegacyCommands(lists.Commands); err != nil {
		return err
	}
	for _, file := range lists.ConfigFiles {
		pattern := filepath.Join(installer.options.ConfigDir, filepath.FromSlash(file.Path))
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return fmt.Errorf("match retired config file %s: %w", pattern, err)
		}
		for _, match := range matches {
			if err := installer.retireConfigFile(match, file.Reason); err != nil {
				return err
			}
		}
	}
	return nil
}

// retireConfigFile removes one retired config file: a regular file or a link
// is backed up under the state dir first (a link as a link to the same
// target), since an operator may have written or linked their own at a name pfm
// once owned; anything else goes as it is. A path this pass already retired
// (retireEmbeddedAssets, which names it through the config dir's physical
// path) is not retired twice.
func (installer *engine) retireConfigFile(path, reason string) error {
	physical := filepath.Join(paths.PhysicalPath(filepath.Dir(path)), filepath.Base(path))
	if installer.removedPaths[filepath.Clean(path)] || installer.removedPaths[physical] {
		return nil
	}
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect retired config file %s: %w", path, err)
	}
	link := info.Mode()&os.ModeSymlink != 0
	if !info.Mode().IsRegular() && !link {
		return installer.retire(path, reason)
	}
	backup := availableBackup(
		filepath.Join(installer.options.Home, ".local", "state", "pfm", "retired-config", filepath.Base(path)),
		installer.stamp,
	)
	installer.markRemoved(path)
	return installer.change("retire "+path+" ("+reason+"; backup: "+backup+")", func() error {
		if err := backupRetiredConfigFile(path, backup, link); err != nil {
			return fmt.Errorf("backup retired config file %s: %w", path, err)
		}
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("remove retired config file %s after backup %s: %w", path, backup, err)
		}
		return nil
	})
}

// backupRetiredConfigFile saves path at backup: a link as a link to its
// target, a regular file as a copy.
func backupRetiredConfigFile(path, backup string, link bool) error {
	if !link {
		return copyBackup(path, backup)
	}
	target, err := os.Readlink(path)
	if err != nil {
		return fmt.Errorf("read link %s: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(backup), 0o700); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(backup), err)
	}
	if err := os.Symlink(target, backup); err != nil {
		return fmt.Errorf("link %s: %w", backup, err)
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

// embeddedRetirement is one asset an older pfm embedded and staged in the
// managed root, asset relative to it; link, relative to a Claude config
// directory, is where install linked it.
type embeddedRetirement struct {
	link, asset, linkReason, assetReason string
	// removeEmptyDir retires the link's directory once the link leaves it empty.
	removeEmptyDir bool
}

// embeddedRetirements are the embedded assets install and uninstall retire.
var embeddedRetirements = []embeddedRetirement{
	{
		link: "skills/handoff/SKILL.md", asset: "handoff.skill.md", removeEmptyDir: true,
		linkReason: "the /handoff skill is a global command now", assetReason: "embedded /handoff skill retired",
	},
	// Uninstall retires the link here alone; install's configFiles row for it
	// finds it gone.
	{
		link: "commands/reload.md", asset: "reload.command.md",
		linkReason: "the /reload command is /pfm:reload now", assetReason: "embedded /reload command retired",
	},
}

// embeddedLinkOwned reports whether link points at retirement's staged copy.
func (installer *engine) embeddedLinkOwned(link string, retirement embeddedRetirement) bool {
	owned := filepath.Join(installer.managedRoot, retirement.asset)
	target, linked := resolvedLink(link)
	return linked && paths.PhysicalPath(target) == paths.PhysicalPath(owned)
}

// retireEmbeddedAssets removes what each retired embedded asset left: its link
// in every Claude config directory the installer fans out to, then the stale
// managed copy. An entry at the link's place that is not that link stays. Not
// gated on live chats: it unlinks, as the installer does when it replaces its
// own links.
func (installer *engine) retireEmbeddedAssets() error {
	configDirs := []string{installer.options.ConfigDir}
	for _, account := range installer.options.ClaudeAccounts {
		configDirs = append(configDirs, account.ConfigDir)
	}
	for _, retirement := range embeddedRetirements {
		seen := map[string]bool{}
		for _, configDir := range configDirs {
			if strings.TrimSpace(configDir) == "" {
				continue
			}
			link := filepath.Join(paths.PhysicalPath(configDir), filepath.FromSlash(retirement.link))
			if seen[link] {
				continue
			}
			seen[link] = true
			if !installer.embeddedLinkOwned(link, retirement) {
				continue
			}
			if err := installer.retire(link, retirement.linkReason); err != nil {
				return err
			}
			if retirement.removeEmptyDir {
				if err := installer.retireEmptyDirTolerant(filepath.Dir(link)); err != nil {
					return err
				}
			}
		}
		if err := installer.retire(
			filepath.Join(installer.managedRoot, retirement.asset), retirement.assetReason,
		); err != nil {
			return err
		}
	}
	return nil
}
