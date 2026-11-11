package installer

import (
	"fmt"
	"sort"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	"github.com/rezzminator/professor/pfm/internal/retired"
)

func accountSettingsLeftovers(
	lists retired.Lists,
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
	leftovers := settingsLeftovers(lists, document, home, owned, ledgerReadable)
	return leftovers, nil
}

func settingsLeftovers(
	lists retired.Lists,
	document map[string]any,
	home string,
	owned settingsHookCounts,
	ledgerReadable bool,
) []string {
	commands := map[string]bool{}
	for _, hook := range claudeHookTemplates(home) {
		commands[hook.Command] = true
	}
	pfmBinary := home + "/.local/bin/pfm"
	found := map[string]bool{}
	for key, count := range countSettingsHookCommands(document) {
		if count > 0 &&
			(commands[key.Command] || isRetiredHookCommand(lists, key.Command, pfmBinary) ||
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
