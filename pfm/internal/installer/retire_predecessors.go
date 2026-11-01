package installer

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func (installer *engine) migrateLegacyCarrier(ctx context.Context) (returnErr error) {
	carrier := filepath.Join(installer.options.Home, ".claude", ".cc-ls-hidden")
	file, err := os.Open(carrier)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read retired kill carrier: %w", err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close retired kill carrier: %w", err))
		}
	}()
	seen := map[string]bool{}
	var ids []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		id := strings.TrimSpace(scanner.Text())
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan retired kill carrier: %w", err)
	}
	sort.Strings(ids)
	return installer.change(
		fmt.Sprintf("merge %d retired carrier kill(s) into SQLite without overwriting existing rows", len(ids)),
		func() (returnErr error) {
			values := paths.Values{
				Home:    installer.options.Home,
				StateDB: installer.options.StateDB,
			}
			state := fleetdb.OpenSharedState(ctx, values)
			defer func() {
				if err := state.Close(); err != nil {
					returnErr = errors.Join(
						returnErr,
						fmt.Errorf("close shared store after carrier retirement: %w", err),
					)
				}
			}()
			if err := state.Degraded(); err != nil {
				return fmt.Errorf("open shared store before carrier retirement: %w", err)
			}
			existing, err := state.KilledAt(ctx)
			if err != nil {
				return fmt.Errorf("read shared kills before carrier retirement: %w", err)
			}
			for _, id := range ids {
				if _, found := existing[id]; found {
					continue
				}
				if err := state.Kill(ctx, id, 0); err != nil {
					return fmt.Errorf("import retired carrier kill %q: %w", id, err)
				}
			}
			return nil
		},
	)
}

// retirePredecessors retires the kill carrier only when carrierMerged: a run
// whose merge into SQLite failed keeps it, or its killed chats would reappear.
func (installer *engine) retirePredecessors(carrierMerged bool) error {
	if err := installer.retireLegacyCommands(); err != nil {
		return err
	}
	config := installer.options.ConfigDir
	for _, name := range []string{
		"cx-kill.sh", "bb-hook.sh", "cx-heal.sh", "cx-recover.sh",
	} {
		if err := installer.retire(filepath.Join(config, "bin", name), "native pfm command"); err != nil {
			return err
		}
	}
	if err := installer.retireGlob(
		filepath.Join(config, "bin", "cx-recover.sh.pre-professor-*"),
		"retired recovery artifact",
	); err != nil {
		return err
	}
	for _, name := range []string{
		"statusline-command.sh",
		"statusline/segments.d/10-vertex-spend.sh",
		"statusline/segments.d/40-gpt-account.sh",
		"statusline/vertex-spend-refresh.py",
		"statusline/gpt-usage.py",
		"statusline/vertex_daily_tokens.py",
	} {
		if err := installer.retire(
			filepath.Join(config, filepath.FromSlash(name)),
			"native pfm statusline",
		); err != nil {
			return err
		}
	}
	for _, name := range []string{"dump.md", "chat-ops.sh", "group.sh"} {
		if err := installer.retire(
			filepath.Join(config, "commands", "chat", name),
			"native pfm chat command",
		); err != nil {
			return err
		}
	}

	// The per-engine harness-prompts tree replaced the flat staged prompts/
	// directory; a host installed before the move still carries its files.
	if err := installer.retireGlob(
		filepath.Join(installer.managedRoot, "prompts", "*"),
		"harness prompts moved to harness-prompts/",
	); err != nil {
		return err
	}
	if err := installer.retireEmptyDirTolerant(filepath.Join(installer.managedRoot, "prompts")); err != nil {
		return err
	}
	carrier := filepath.Join(installer.options.Home, ".claude", ".cc-ls-hidden")
	if !carrierMerged {
		installer.skip("keep retired kill carrier " + carrier + ": its kills did not merge into SQLite this run")
		return nil
	}
	for _, path := range []string{carrier, carrier + ".at", carrier + ".lock"} {
		if err := installer.retire(path, "SQLite is the sole kill store"); err != nil {
			return err
		}
	}
	return installer.retireGlob(carrier+".n.*", "retired carrier scratch")
}

// Retire executable predecessors across the configured account roster while
// leaving account credentials, transcript directories, and live sockets alone.
func (installer *engine) retireLegacyCommands() error {
	for _, name := range []string{legacyFleetBinary, "cc-ls", "cc-open", "cc-swap", "cc-revive", "cc-clean"} {
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
		for _, name := range []string{
			"cc-launch.sh", "cc-lib.sh", "cc-reseed.sh", "cc-account-swap.sh", "cc-account-swap-all.sh",
			"cc-ls.sh", "cc-open.sh", "cc-revive.sh", "cc-clean.sh", "cc-usage-hook.sh", "cc-fleet.zsh",
			"cc-kill.sh", "cc-archive.sh", "cc-reap.sh", "cc-name-sync.sh", "cc-portable.sh", "cc-db.sh",
			"cc-agent-open.sh", "cc-swap-chat.sh",
		} {
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
