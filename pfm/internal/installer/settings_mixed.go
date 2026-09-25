package installer

import (
	"encoding/json"
	"path/filepath"
	"sort"
)

// preservedHook is one template hook kept in an operator's mixed entry under
// an (event, matcher) that is none of the template's own placements.
type preservedHook struct {
	Event   string
	Matcher string
	Command string
}

// mixedTemplateCopies lists every template hook dropMisplacedTemplateHooks
// keeps because its entry is mixed, in event-name and array order. A copy
// under a template's own event satisfies that template (updateSettings
// appends no second one); every copy is named in install's report.
func mixedTemplateCopies(document map[string]any, expected []ExpectedHook, pfmBinary string) []preservedHook {
	placements := templatePlacements(expected)
	events, _ := document["hooks"].(map[string]any)
	names := make([]string, 0, len(events))
	for event := range events {
		names = append(names, event)
	}
	sort.Strings(names)
	var copies []preservedHook
	for _, event := range names {
		entries, _ := events[event].([]any)
		for _, entryValue := range entries {
			entry, ok := entryValue.(map[string]any)
			if !ok {
				continue
			}
			hooks, hooksOK := entry["hooks"].([]any)
			matcher, matcherOK := "", true
			if value, present := entry["matcher"]; present {
				matcher, matcherOK = value.(string)
			}
			if !hooksOK || !matcherOK || !settingsHookEntryHasMixedOwnership(entry, pfmBinary) {
				continue
			}
			for _, hookValue := range hooks {
				hook, _ := hookValue.(map[string]any)
				command, _ := hook[configCommandKey].(string)
				pairs, owned := placements[command]
				if !owned || pairs[[2]string{event, matcher}] {
					continue
				}
				copies = append(copies, preservedHook{Event: event, Matcher: matcher, Command: command})
			}
		}
	}
	return copies
}

// preservedMixedTemplateHooks decodes a settings document updateSettings
// has just produced (so it decodes) and lists its mixedTemplateCopies.
func preservedMixedTemplateHooks(raw []byte, expected []ExpectedHook, pfmBinary string) []preservedHook {
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil
	}
	return mixedTemplateCopies(document, expected, pfmBinary)
}

func hasPreservedCopy(copies []preservedHook, event, command string) bool {
	for _, kept := range copies {
		if kept.Event == event && kept.Command == command {
			return true
		}
	}
	return false
}

// reportPreservedMixedTemplateHooks names each kept mixed copy in updated,
// the settings bytes install is about to leave at candidate.
func (installer *engine) reportPreservedMixedTemplateHooks(updated []byte, candidate string) {
	for _, kept := range preservedMixedTemplateHooks(
		updated,
		claudeHookTemplates(installer.options.Home),
		filepath.Join(installer.options.Home, ".local", "bin", "pfm"),
	) {
		installer.skip("mixed " + kept.Event + " hook entry preserved with its existing matcher at " +
			candidate + ": " + kept.Command)
	}
}
