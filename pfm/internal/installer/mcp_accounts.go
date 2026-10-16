package installer

import (
	"bytes"
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
	ownership.Registrations = nil
	ownership.Pending = nil
	path := installer.mcpOwnershipPath()
	if len(ownership.Clients) == 0 &&
		len(ownership.OpenCodeRegistrations) == 0 && len(ownership.OpenCodePending) == 0 {
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

func (installer *engine) dropClaudeMCPOwnership() error {
	ownership, err := installer.loadMCPOwnership()
	if err != nil {
		return err
	}
	if len(ownership.Registrations) > 0 || len(ownership.Pending) > 0 {
		return installer.saveMCPOwnership(ownership)
	}
	return nil
}

// cutOverClaudeMCP reconciles existing user registries; new chats use the
// launcher's strict MCP config, so an absent registry is never created here.
func (installer *engine) cutOverClaudeMCP() error {
	accounts, ambient := installer.claudeMCPAccounts()
	registries := ClaudeMCPRegistries(installer.options.Home, accounts, ambient)
	seen := map[string]bool{}
	names := enabledMCPNames(installer.options.MCPEnabled)
	if installer.options.Mode == ModeUninstall {
		names = nil
	}
	for _, registry := range registries {
		physical := physicalSettingsPath(registry.Path)
		if seen[physical] {
			continue
		}
		seen[physical] = true
		raw, existed, err := readMCPFile(registry.Path)
		if err != nil {
			return fmt.Errorf("read Claude MCP registry %s: %w", registry.Path, err)
		}
		if !existed {
			continue
		}
		var document map[string]json.RawMessage
		if err := json.Unmarshal(raw, &document); err != nil {
			return fmt.Errorf("parse Claude MCP registry %s: %w", registry.Path, err)
		}
		encoded, present := document["mcpServers"]
		if !present {
			continue
		}
		var servers map[string]json.RawMessage
		if err := json.Unmarshal(encoded, &servers); err != nil {
			return fmt.Errorf("parse Claude MCP servers %s: %w", registry.Path, err)
		}
		owned := false
		for _, name := range append(append([]string(nil), mcpLegacyNames...), pfmconfig.MCPRegistrationName(chatName), pfmconfig.MCPRegistrationName(mcpServerHarvester)) {
			entry, present := servers[name]
			if !present {
				continue
			}
			var shape map[string]any
			if err := json.Unmarshal(entry, &shape); err != nil {
				return fmt.Errorf("parse Claude MCP registration %s in %s: %w", name, registry.Path, err)
			}
			matches := isPFMLegacyClaudeShape(name, shape, installer.mcpChatCommand(), installer.options.MCPPort)
			if isFamilyRegistration(name) {
				matches = isExactStdioShape(shape, installer.mcpChatCommand(), mcpFamilyStdioArgs(name))
			}
			if matches {
				delete(servers, name)
				owned = true
			} else if name == professorName {
				fmt.Fprintf(
					installer.options.Stdout,
					"preserve foreign Claude MCP client professor in %s\n",
					registry.Path,
				)
			}
		}
		if !owned {
			continue
		}
		for _, name := range names {
			if _, present := servers[name]; present {
				return fmt.Errorf(
					"preserve foreign Claude MCP client %s in %s: cannot cut over owned registration",
					name,
					registry.Path,
				)
			}
			encoded, err := json.Marshal(
				map[string]any{
					configTypeKey:    stdioProtocol,
					configCommandKey: installer.mcpChatCommand(),
					"args":           mcpFamilyStdioArgs(name),
				},
			)
			if err != nil {
				return fmt.Errorf("encode Claude MCP registration %s: %w", name, err)
			}
			servers[name] = encoded
		}
		encoded, err = json.Marshal(servers)
		if err != nil {
			return fmt.Errorf("encode Claude MCP servers %s: %w", registry.Path, err)
		}
		document["mcpServers"] = encoded
		wanted, err := json.MarshalIndent(document, "", "  ")
		if err != nil {
			return fmt.Errorf("encode Claude MCP registry %s: %w", registry.Path, err)
		}
		wanted = append(wanted, '\n')
		if bytes.Equal(raw, wanted) {
			continue
		}
		locks := installer.claudeMCPLockAliases(registry.Path, physical)
		if err := installer.change("cut over Claude MCP registrations in "+registry.Path, func() error {
			return withClaudeMCPLocks(locks, func(check func() error) error {
				return installer.writeMCPFileIf(registry.Path, raw, wanted, true, check)
			})
		}); err != nil {
			return err
		}
	}
	return nil
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

// ClaudeMCPRegistries includes the historical default user registry alongside
// account and ambient registries, deduplicated by physical file.
func ClaudeMCPRegistries(home string, accounts []pfmconfig.Account, ambient string) []ClaudeRegistry {
	registries := ClaudeUserRegistries(home, accounts, ambient)
	fallback := filepath.Join(home, ".claude.json")
	for _, registry := range registries {
		if physicalSettingsPath(registry.Path) == physicalSettingsPath(fallback) {
			return registries
		}
	}
	return append(registries, ClaudeRegistry{Path: fallback, Reason: "default user registry"})
}
