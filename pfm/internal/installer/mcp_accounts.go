package installer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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

func (installer *engine) writeMCPClientJSON(names []string) ([]string, error) {
	// One reader for this ledger: loadMCPOwnership (mcp.go) already renders a
	// missing file as an empty ownership and every other failure — unreadable
	// or undecodable — as a named error, which is exactly what this used to
	// restate inline.
	ownership, err := installer.loadMCPOwnership()
	if err != nil {
		return nil, err
	}
	if ownership.Registrations == nil {
		ownership.Registrations = map[string]map[string]any{}
	}
	if ownership.Pending == nil {
		ownership.Pending = map[string]map[string]any{}
	}
	for _, receipts := range []*map[string]map[string]any{&ownership.Registrations, &ownership.Pending} {
		canonical := map[string]map[string]any{}
		for path, entries := range *receipts {
			physical := physicalSettingsPath(path)
			if canonical[physical] == nil {
				canonical[physical] = map[string]any{}
			}
			for name, registration := range entries {
				if prior, exists := canonical[physical][name]; exists && !sameJSONValue(prior, registration) {
					return nil, fmt.Errorf("conflicting MCP ownership aliases for %s in %s", name, physical)
				}
				canonical[physical][name] = registration
			}
		}
		*receipts = canonical
	}
	wantedPaths := map[string]bool{}
	reasons := map[string]string{}
	if len(names) > 0 {
		registries := installer.options.ClaudeRegistries
		registryReasons := installer.options.ClaudeRegistryReasons
		if registries == nil {
			accounts := make([]pfmconfig.Account, 0, len(installer.claudeConfigDirs()))
			for index, dir := range installer.claudeConfigDirs() {
				// A direct caller's fanout has no Implicit flag of its own; a
				// dir that cleans to the canonical ~/.claude carries the same
				// registry pfm's own implicit account does (the historical
				// ClaudeUserRegistry special case this fallback preserves).
				accounts = append(accounts, pfmconfig.Account{
					ID:        index + 1,
					ConfigDir: dir,
					Implicit:  filepath.Clean(dir) == filepath.Join(installer.options.Home, ".claude"),
				})
			}
			resolved := ClaudeUserRegistries(installer.options.Home, accounts, pfmconfig.AmbientClaudeConfigDir())
			registries = make([]string, 0, len(resolved))
			registryReasons = map[string]string{}
			for _, registry := range resolved {
				registries = append(registries, registry.Path)
				registryReasons[registry.Path] = registry.Reason
			}
		}
		for _, path := range registries {
			if strings.TrimSpace(path) != "" {
				physical := physicalSettingsPath(path)
				wantedPaths[physical] = true
				if reason, ok := registryReasons[path]; ok && reason != "" {
					reasons[physical] = reason
				}
			}
		}
	}
	paths := map[string]bool{}
	for path := range wantedPaths {
		paths[path] = true
	}
	for path := range ownership.Registrations {
		paths[path] = true
	}
	for path := range ownership.Pending {
		paths[path] = true
	}
	legacy := physicalSettingsPath(filepath.Join(installer.options.Home, ".mcp.json"))
	if len(ownership.Clients) > 0 {
		paths[legacy] = true
	}
	ordered := make([]string, 0, len(paths))
	for path := range paths {
		ordered = append(ordered, path)
	}
	sort.Strings(ordered)
	for _, path := range ordered {
		original, existed, err := readMCPFile(path)
		document := map[string]any{}
		if err == nil && existed {
			err = unmarshalKeepingNumbers(original, &document)
			if err == nil && document == nil {
				err = errors.New("registry must be an object")
			}
		}
		if err != nil {
			return nil, fmt.Errorf("read MCP registry %s: %w", path, err)
		}
		servers := map[string]any{}
		if value, present := document["mcpServers"]; present {
			var ok bool
			servers, ok = value.(map[string]any)
			if !ok || servers == nil {
				return nil, fmt.Errorf("MCP registry %s: mcpServers must be an object", path)
			}
		}
		before, _ := json.Marshal(document)
		owned := ownership.Registrations[path]
		if owned == nil {
			owned = map[string]any{}
		}
		for name, registration := range ownership.Pending[path] {
			if sameJSONValue(servers[name], registration) {
				owned[name] = registration
			}
		}
		// Migrate the names-only predecessor ledger only at its historical path.
		if path == legacy {
			for _, name := range ownership.Clients {
				if registration, ok := servers[name].(map[string]any); ok && installer.isPFMClient(name, registration) {
					owned[name] = registration
				}
			}
		}
		next := map[string]any{}
		wanted := map[string]bool{}
		if wantedPaths[path] {
			for _, name := range names {
				wanted[name] = true
			}
		}
		for name, registration := range owned {
			current, present := servers[name]
			if present && sameJSONValue(current, registration) {
				if wanted[name] {
					next[name] = registration
				} else {
					delete(servers, name)
				}
			}
		}
		for name := range wanted {
			registration := installer.mcpClientRegistration(name)
			_, present := servers[name]
			_, ours := next[name]
			if present && !ours {
				installer.skip("preserve conflicting manual MCP client " + name + " in " + path)
				continue
			}
			servers[name] = registration
			next[name] = registration
		}
		if len(servers) > 0 || document["mcpServers"] != nil {
			document["mcpServers"] = servers
		}
		after, _ := json.Marshal(document)
		// Keep the last receipt until the registry write succeeds. Pending exact
		// values cover a crash between that write and the final receipt commit.
		ownership.Pending[path] = next
		message := changeDescription(path, existed)
		okMessage := path + " wiring"
		// Append the registry's reason rather than replacing changeDescription's
		// create/rewrite wording or the plain "<path> wiring" ok line: both are
		// pinned verbatim by earlier regression tests (backup-claim and
		// owned-stdio-recognition), and a reason is extra context, not a
		// different report.
		if wantedPaths[path] && len(names) > 0 {
			if reason := reasons[path]; reason != "" {
				suffix := fmt.Sprintf(" — register %s (%s)", strings.Join(names, ","), reason)
				message += suffix
				okMessage += suffix
			}
		}
		if !bytes.Equal(before, after) {
			if err := installer.saveMCPOwnership(ownership); err != nil {
				return nil, err
			}
			encoded, err := json.MarshalIndent(document, "", "  ")
			if err != nil {
				return nil, err
			}
			if err := installer.change(message, func() error {
				return installer.writeMCPFile(path, original, append(encoded, '\n'), existed)
			}); err != nil {
				return nil, err
			}
		} else {
			installer.ok(okMessage)
		}
		if len(next) > 0 {
			ownership.Registrations[path] = next
		} else {
			delete(ownership.Registrations, path)
		}
		delete(ownership.Pending, path)
		if path == legacy {
			ownership.Clients = nil
		}
		if err := installer.saveMCPOwnership(ownership); err != nil {
			return nil, err
		}
	}
	if err := installer.saveMCPOwnership(ownership); err != nil {
		return nil, err
	}
	return names, nil
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
	return installer.change("write "+path, func() error { return atomicfile.Write(path, encoded, 0o600) })
}
