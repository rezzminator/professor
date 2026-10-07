package installer

import (
	"fmt"
	"sort"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
)

func accountSettingsLeftovers(
	raw []byte,
	home string,
	owned settingsHookCounts,
	ledgerReadable bool,
) ([]string, error) {
	document, err := parseAccountDocument(raw)
	if err != nil {
		return nil, err
	}
	if err := validateAccountHooks(document); err != nil {
		return nil, err
	}
	leftovers := settingsLeftovers(document, home, owned, ledgerReadable)
	return leftovers, nil
}

func settingsLeftovers(document map[string]any, home string, owned settingsHookCounts, ledgerReadable bool) []string {
	commands := map[string]bool{}
	for _, hook := range claudeHookTemplates(home) {
		commands[hook.Command] = true
	}
	pfmBinary := home + "/.local/bin/pfm"
	found := map[string]bool{}
	for key, count := range countSettingsHookCommands(document) {
		if count > 0 &&
			(commands[key.Command] || isRetiredHookCommand(key.Command, pfmBinary) ||
				ledgerReadable && owned[key] > 0 && strings.SplitN(key.Command, " ", 2)[0] == pfmBinary) {
			found[key.Command] = true
		}
	}
	hookCommands := make([]string, 0, len(found))
	for command := range found {
		hookCommands = append(hookCommands, command)
	}
	sort.Strings(hookCommands)
	leftovers := []string{}
	for _, command := range hookCommands {
		leftovers = append(leftovers, fmt.Sprintf("hook %q", command))
	}
	if status, ok := document["statusLine"].(map[string]any); ok && ownedStatusCommand(home, status[configCommandKey]) {
		leftovers = append(leftovers, "statusLine")
	}
	if status, ok := document[subagentStatusLineKey].(map[string]any); ok &&
		status[configCommandKey] == claudelaunch.SubagentStatusLineCommand(home) {
		leftovers = append(leftovers, "subagentStatusLine")
	}
	return leftovers
}

func ownedStatusCommand(home string, value any) bool {
	command, ok := value.(string)
	return ok &&
		(command == claudelaunch.StatusLineCommand(home) || command == home+"/.local/bin/pfm statusline" || command == "pfm statusline")
}

func parseAccountDocument(raw []byte) (map[string]any, error) {
	var document map[string]any
	if err := unmarshalKeepingNumbers(raw, &document); err != nil {
		return nil, err
	}
	if document == nil {
		return nil, fmt.Errorf("account file must be an object")
	}
	return document, nil
}

func validateAccountHooks(document map[string]any) error {
	value, present := document["hooks"]
	if !present {
		return nil
	}
	events, ok := value.(map[string]any)
	if !ok || events == nil {
		return fmt.Errorf("hooks must be an object")
	}
	for event, value := range events {
		entries, ok := value.([]any)
		if !ok {
			return fmt.Errorf("hooks.%s must be an array", event)
		}
		for _, value := range entries {
			entry, ok := value.(map[string]any)
			if !ok {
				return fmt.Errorf("hooks.%s entry must be an object", event)
			}
			hooks, ok := entry["hooks"].([]any)
			if !ok {
				return fmt.Errorf("hooks.%s entry hooks must be an array", event)
			}
			for _, value := range hooks {
				if _, ok := value.(map[string]any); !ok {
					return fmt.Errorf("hooks.%s hook must be an object", event)
				}
			}
		}
	}
	return nil
}

// mcpShaped reports whether a registration under name is in a pfm shape; nil
// judges by the ledger alone.
type mcpShaped func(name string, registration map[string]any) bool

// accountMCPLeftovers names the mcpServers entries pfm owns in a Claude MCP
// file: registrations matching their ledger records plus shape-matched ones.
func accountMCPLeftovers(raw []byte, owned map[string][]any, shaped mcpShaped) ([]string, error) {
	document, err := parseAccountDocument(raw)
	if err != nil {
		return nil, err
	}
	servers, err := accountMCPServers(document)
	if err != nil {
		return nil, err
	}
	return ownedMCPNames(servers, owned, shaped), nil
}

func accountMCPServers(document map[string]any) (map[string]any, error) {
	value, present := document["mcpServers"]
	if !present {
		return nil, nil
	}
	servers, ok := value.(map[string]any)
	if !ok || servers == nil {
		return nil, fmt.Errorf("mcpServers must be an object")
	}
	return servers, nil
}

// ledgerOwnedMCP returns the registrations recorded for one physical registry
// file. A key matches when it resolves to that file: older installs keyed the
// path as reached, and a link on the way (macOS /tmp is /private/tmp) makes
// that differ from the physical path.
func ledgerOwnedMCP(registrations map[string]map[string]any, physical string) map[string][]any {
	keys := []string{}
	for key := range registrations {
		if key != physical && physicalSettingsPath(key) != physical {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	owned := map[string][]any{}
	for _, key := range keys {
		for name, record := range registrations[key] {
			owned[name] = append(owned[name], record)
		}
	}
	return owned
}

func ownedMCPNames(servers map[string]any, owned map[string][]any, shaped mcpShaped) []string {
	seen := map[string]bool{}
	for name, records := range owned {
		current, present := servers[name]
		if !present {
			continue
		}
		for _, record := range records {
			if sameClaudeRegistration(current, record) {
				seen[name] = true
				break
			}
		}
	}
	if shaped != nil {
		for _, name := range []string{chatName, mcpServerHarvester, professorName} {
			if registration, ok := servers[name].(map[string]any); ok && shaped(name, registration) {
				seen[name] = true
			}
		}
	}
	result := make([]string, 0, len(seen))
	for name := range seen {
		result = append(result, "mcpServers."+name)
	}
	sort.Strings(result)
	return result
}

func sameClaudeRegistration(current, recorded any) bool {
	if registration, ok := current.(map[string]any); ok {
		current = withoutEmptyEnv(registration)
	}
	if registration, ok := recorded.(map[string]any); ok {
		recorded = withoutEmptyEnv(registration)
	}
	return sameJSONValue(current, recorded)
}
