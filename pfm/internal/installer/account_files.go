package installer

import (
	"encoding/json"
	"fmt"
	"sort"

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

func stripAccountSettings(raw []byte, home string, owned settingsHookCounts) ([]byte, []string, error) {
	document, err := parseAccountDocument(raw)
	if err != nil {
		return nil, nil, err
	}
	if err := validateAccountHooks(document); err != nil {
		return nil, nil, err
	}
	removed := settingsLeftovers(document, home, owned, true)
	if len(removed) == 0 {
		return raw, nil, nil
	}
	commands := map[string]bool{}
	for _, hook := range claudeHookTemplates(home) {
		commands[hook.Command] = true
	}
	counts := settingsHookCounts{}
	for key, count := range countSettingsHookCommands(document) {
		if commands[key.Command] {
			counts[key] = count
		} else if owned[key] > 0 {
			counts[key] = owned[key]
		}
	}
	dropMisplacedTemplateHooks(document, claudeHookTemplates(home), home+"/.local/bin/pfm")
	removeOwnedSettingsHooks(document, counts)
	removeRetiredHookCommands(document, home+"/.local/bin/pfm")
	pruneAccountHooks(document)
	if status, ok := document["statusLine"].(map[string]any); ok && ownedStatusCommand(home, status[configCommandKey]) {
		delete(document, "statusLine")
	}
	if status, ok := document[subagentStatusLineKey].(map[string]any); ok &&
		status[configCommandKey] == claudelaunch.SubagentStatusLineCommand(home) {
		delete(document, subagentStatusLineKey)
	}
	updated, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, nil, fmt.Errorf("encode account settings: %w", err)
	}
	return append(updated, '\n'), removed, nil
}

func settingsLeftovers(document map[string]any, home string, owned settingsHookCounts, ledgerReadable bool) []string {
	commands := map[string]bool{}
	for _, hook := range claudeHookTemplates(home) {
		commands[hook.Command] = true
	}
	leftovers := []string{}
	for key, count := range countSettingsHookCommands(document) {
		if count > 0 &&
			(commands[key.Command] || isRetiredHookCommand(key.Command, home+"/.local/bin/pfm") || ledgerReadable && owned[key] > 0) {
			leftovers = append(leftovers, "hooks")
			break
		}
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
	return ok && (command == claudelaunch.StatusLineCommand(home) || command == home+"/.local/bin/pfm statusline")
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

func pruneAccountHooks(document map[string]any) {
	events, ok := document["hooks"].(map[string]any)
	if !ok {
		return
	}
	for event, value := range events {
		entries := value.([]any)
		if len(entries) == 0 {
			delete(events, event)
		}
	}
	if len(events) == 0 {
		delete(document, "hooks")
	}
}

func accountMCPLeftovers(raw []byte, owned []string) ([]string, error) {
	document, err := parseAccountDocument(raw)
	if err != nil {
		return nil, err
	}
	servers, err := accountMCPServers(document)
	if err != nil {
		return nil, err
	}
	return ownedMCPNames(servers, owned), nil
}

func stripAccountMCP(raw []byte, owned []string) ([]byte, []string, error) {
	document, err := parseAccountDocument(raw)
	if err != nil {
		return nil, nil, err
	}
	servers, err := accountMCPServers(document)
	if err != nil {
		return nil, nil, err
	}
	removed := ownedMCPNames(servers, owned)
	if len(removed) == 0 {
		return raw, nil, nil
	}
	for _, name := range owned {
		delete(servers, name)
	}
	if len(servers) == 0 {
		delete(document, "mcpServers")
	}
	updated, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, nil, fmt.Errorf("encode account MCP registry: %w", err)
	}
	return append(updated, '\n'), removed, nil
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

func ownedMCPNames(servers map[string]any, owned []string) []string {
	seen := map[string]bool{}
	for _, name := range owned {
		if _, present := servers[name]; present {
			seen[name] = true
		}
	}
	result := make([]string, 0, len(seen))
	for name := range seen {
		result = append(result, "mcpServers."+name)
	}
	sort.Strings(result)
	return result
}
