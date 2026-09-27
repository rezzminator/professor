package installer

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

// ClaudeRegistry is one user-scope Claude Code registry (.claude.json) a
// pfm-launched `claude` process can actually read, and why: the account it
// belongs to (0 for the ambient entry, which is not tied to one account) and
// the human-readable reason ClaudeUserRegistries derived it from.
type ClaudeRegistry struct {
	Path    string
	Reason  string
	Account int
}

// ClaudeUserRegistries resolves every user-scope Claude Code registry a
// pfm-launched claude process can read: one per configured account (the
// implicit account — the one pfm spawns without CLAUDE_CONFIG_DIR — at
// $HOME/.claude.json, every other account at its own ConfigDir/.claude.json),
// plus the ambient CLAUDE_CONFIG_DIR the invoking shell exported, when that
// path is not already listed (the launcher shim passes it straight through —
// internal_launch.go). Deduplicated by physical path so one file is never
// listed twice under two reasons.
func ClaudeUserRegistries(home string, accounts []pfmconfig.Account, ambientConfigDir string) []ClaudeRegistry {
	seen := map[string]bool{}
	registries := make([]ClaudeRegistry, 0, len(accounts)+1)
	add := func(path, reason string, account int) {
		physical := physicalSettingsPath(path)
		if seen[physical] {
			return
		}
		seen[physical] = true
		registries = append(registries, ClaudeRegistry{Path: path, Reason: reason, Account: account})
	}
	for _, account := range accounts {
		if account.Implicit {
			add(filepath.Join(home, ".claude.json"),
				fmt.Sprintf("account %d (pfm spawns it without CLAUDE_CONFIG_DIR)", account.ID), account.ID)
			continue
		}
		add(
			filepath.Join(account.ConfigDir, ".claude.json"),
			fmt.Sprintf(
				"account %d (CLAUDE_CONFIG_DIR=%s when pfm spawns it)",
				account.ID,
				account.ConfigDir,
			),
			account.ID,
		)
	}
	if ambient := strings.TrimSpace(ambientConfigDir); ambient != "" {
		add(
			filepath.Join(ambient, ".claude.json"),
			fmt.Sprintf(
				"ambient CLAUDE_CONFIG_DIR=%s (the claude launcher passes it through — internal_launch.go)",
				ambient,
			),
			0,
		)
	}
	return registries
}

func (installer *engine) saveMCPOwnership(ownership mcpOwnership) error {
	path := installer.mcpOwnershipPath()
	if len(ownership.Clients) == 0 && len(ownership.Registrations) == 0 && len(ownership.Pending) == 0 &&
		len(ownership.OpenCodeRegistrations) == 0 && len(ownership.OpenCodePending) == 0 {
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			return nil
		} else if err != nil {
			return err
		}
		return installer.changePaths("remove "+path, []string{path}, func() error { return os.Remove(path) })
	}
	encoded, err := json.MarshalIndent(ownership, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	if sameFile(path, encoded, 0o600) {
		return nil
	}
	return installer.changePaths("write "+path, []string{path}, func() error {
		return atomicfile.Write(path, encoded, 0o600)
	})
}
