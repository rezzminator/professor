package installer

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/retired"
)

// retirePredecessors removes the retired lists' configFiles under the Claude
// config dir and their commands. A failed read is deferred once by Run.
func (installer *engine) retirePredecessors() error {
	lists, _ := installer.retiredLists()
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
