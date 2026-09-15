package installer

import (
	"encoding/json"
	"fmt"
	"strings"

	"hostops/pfm/internal/codexappendix"
)

// codexClearMatcher is the matcher the retired Codex SessionStart clear-kill
// hook used to carry. It remains only so leftover entries can be removed; no
// current ownership path recognizes or writes this retired shape.
const codexClearMatcher = "startup|resume|clear"

// updateCodexHooks preserves personal handlers, retires clear-kill, and owns the appendix.
func updateCodexHooks(
	raw []byte,
	home string,
	uninstall bool,
	owned settingsHookCounts,
) ([]byte, bool, settingsHookCounts, error) {
	var document map[string]any
	if err := unmarshalKeepingNumbers(raw, &document); err != nil {
		return nil, false, nil, err
	}
	if err := validateCodexHooks(document); err != nil {
		return nil, false, nil, err
	}
	oldBinary := home + "/.local/bin/cc-fleet"
	pfmBinary := home + "/.local/bin/pfm"
	retiredCommands := map[string]bool{
		pfmBinary + " internal clear-kill":                  true,
		oldBinary + " internal clear-kill":                  true,
		pfmBinary + ` internal clear-kill --parent "$PPID"`: true,
		oldBinary + ` internal clear-kill --parent "$PPID"`: true,
	}

	before := countSettingsHookCommands(document)
	changed := false
	if uninstall {
		changed = removeOwnedSettingsHooks(document, owned)
	} else {
		changed = rewriteCommandFields(document, func(command string) string {
			if command == oldBinary || strings.HasPrefix(command, oldBinary+" ") {
				return pfmBinary + strings.TrimPrefix(command, oldBinary)
			}
			return command
		})
	}
	if removeRetiredHookCommands(document, pfmBinary) {
		changed = true
	}

	for _, entry := range hookEntries(document, "SessionStart", false) {
		hooks, _ := entry["hooks"].([]any)
		kept := hooks[:0]
		for _, hookValue := range hooks {
			hook, _ := hookValue.(map[string]any)
			command, _ := hook["command"].(string)
			if isRetiredHookCommand(command, pfmBinary) || retiredCommands[command] {
				changed = true
				continue
			}
			kept = append(kept, hookValue)
		}
		entry["hooks"] = kept
	}
	pruneEmptyHooks(document, "SessionStart")
	if hooks, ok := document["hooks"].(map[string]any); ok {
		if values, _ := hooks["SessionStart"].([]any); len(values) == 0 {
			delete(hooks, "SessionStart")
		}
	}

	if !uninstall &&
		!hasHookCommandWithMatcher(
			hookEntries(document, "SessionStart", false),
			codexappendix.Command(home),
			codexappendix.Matcher,
		) {
		appendHookWithMatcher(document, "SessionStart", codexappendix.Matcher, codexappendix.Command(home))
		changed = true
	}
	if !uninstall {
		for _, entry := range hookEntries(document, "SessionStart", false) {
			if entry["matcher"] != codexappendix.Matcher {
				continue
			}
			handlers, _ := entry["hooks"].([]any)
			for _, value := range handlers {
				handler, _ := value.(map[string]any)
				if handler["command"] == codexappendix.Command(home) {
					if handler["type"] != "command" || !jsonNumberIs(handler["timeout"], 10) {
						handler["type"] = "command"
						handler["timeout"] = float64(10)
						changed = true
					}
				}
			}
		}
	}
	nextOwned := nextSettingsHookOwnership(
		before,
		countSettingsHookCommands(document),
		owned,
		pfmBinary,
		uninstall,
		settingsDocumentHasMixedOwnershipEntry(document, pfmBinary),
	)
	if !changed {
		return raw, false, nextOwned, nil
	}
	updated, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, false, nil, fmt.Errorf("encode Codex hooks: %w", err)
	}
	return append(updated, '\n'), true, nextOwned, nil
}

func validateCodexHooks(document map[string]any) error {
	if document == nil {
		return fmt.Errorf("hooks document must be an object")
	}
	value, present := document["hooks"]
	if !present {
		return nil
	}
	events, ok := value.(map[string]any)
	if !ok {
		return fmt.Errorf("hooks must be an object")
	}
	for event, value := range events {
		entries, ok := value.([]any)
		if !ok {
			return fmt.Errorf("%s must be a matcher array", event)
		}
		for _, value := range entries {
			entry, ok := value.(map[string]any)
			if !ok {
				return fmt.Errorf("%s matcher must be an object", event)
			}
			if matcher, present := entry["matcher"]; present && matcher != nil {
				if _, ok := matcher.(string); !ok {
					return fmt.Errorf("%s matcher must be a string", event)
				}
			}
			handlers, ok := entry["hooks"].([]any)
			if !ok {
				return fmt.Errorf("%s hooks must be an array", event)
			}
			for _, value := range handlers {
				if _, ok := value.(map[string]any); !ok {
					return fmt.Errorf("%s handler must be an object", event)
				}
			}
		}
	}
	return nil
}
