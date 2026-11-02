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
	"github.com/rezzminator/professor/pfm/internal/paths"
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
// pfm-launched claude process can read: one per configured account at its
// ConfigDir/.claude.json,
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
	if len(ownership.OpenCodeRegistrations) == 0 && len(ownership.OpenCodePending) == 0 {
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			return nil
		} else if err != nil {
			return err
		}
		return installer.change("remove "+path, func() error { return os.Remove(path) })
	}
	encoded, err := json.MarshalIndent(ownership, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	if sameFile(path, encoded, 0o600) {
		return nil
	}
	return installer.change("write "+path, func() error {
		return atomicfile.Write(path, encoded, 0o600)
	})
}

// claudeMCPAccounts includes every configured, roster-only and ambient seat.
func (installer *engine) claudeMCPAccounts() ([]pfmconfig.Account, string) {
	accounts := append([]pfmconfig.Account(nil), installer.options.ClaudeAccounts...)
	for _, dir := range installer.options.RosterConfigDirs {
		accounts = append(accounts, pfmconfig.Account{ConfigDir: dir})
	}
	if installer.options.ConfigDir != "" {
		accounts = append(accounts, pfmconfig.Account{ConfigDir: installer.options.ConfigDir})
	}
	env := installer.options.Env
	if env == nil {
		env = paths.OSEnv{}
	}
	ambient := env.Get("CLAUDE_CONFIG_DIR")
	return accounts, ambient
}

// claudeMCPLockAliases recovers lexical locks lost to physical deduplication.
// Every Claude registry writer uses this same alias set.
func (installer *engine) claudeMCPLockAliases(path, physical string) []string {
	accounts, ambient := installer.claudeMCPAccounts()
	aliases := []string{path, physical, filepath.Join(installer.options.Home, ".claude.json")}
	for _, account := range accounts {
		aliases = append(aliases, filepath.Join(account.ConfigDir, ".claude.json"))
	}
	if strings.TrimSpace(ambient) != "" {
		aliases = append(aliases, filepath.Join(ambient, ".claude.json"))
	}
	var locks []string
	for _, alias := range aliases {
		if physicalSettingsPath(alias) == physical {
			locks = append(locks, alias)
		}
	}
	return locks
}
