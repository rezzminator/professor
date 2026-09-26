package installer

import (
	"sort"
)

// dropMisplacedTemplateHooks removes misplaced or duplicate template hooks
// before the account stripper removes their remaining canonical copies.
// A mixed operator entry is left to removeOwnedSettingsHooks, which removes
// only the exact command keys the stripper classified.
func dropMisplacedTemplateHooks(document map[string]any, expected []ExpectedHook, pfmBinary string) bool {
	placements := templatePlacements(expected)
	events, _ := document["hooks"].(map[string]any)
	names := make([]string, 0, len(events))
	for event := range events {
		names = append(names, event)
	}
	sort.Strings(names)
	kept := map[settingsHookKey]bool{}
	changed := false
	for _, event := range names {
		entries, ok := events[event].([]any)
		if !ok {
			continue
		}
		keptEntries := make([]any, 0, len(entries))
		eventChanged := false
		for _, entryValue := range entries {
			entry, ok := entryValue.(map[string]any)
			if !ok {
				keptEntries = append(keptEntries, entryValue)
				continue
			}
			hooks, hooksOK := entry["hooks"].([]any)
			matcher, matcherOK := "", true
			if value, present := entry["matcher"]; present {
				matcher, matcherOK = value.(string)
			}
			if !hooksOK || !matcherOK {
				keptEntries = append(keptEntries, entryValue)
				continue
			}
			mixed := settingsHookEntryHasMixedOwnership(entry, pfmBinary)
			keptHooks := make([]any, 0, len(hooks))
			entryChanged := false
			for _, hookValue := range hooks {
				hook, _ := hookValue.(map[string]any)
				command, _ := hook[configCommandKey].(string)
				pairs, owned := placements[command]
				if !owned {
					keptHooks = append(keptHooks, hookValue)
					continue
				}
				key := settingsHookKey{Event: event, Matcher: matcher, Command: command}
				if pairs[[2]string{event, matcher}] {
					if !kept[key] {
						kept[key] = true
						keptHooks = append(keptHooks, hookValue)
						continue
					}
				} else if mixed {
					keptHooks = append(keptHooks, hookValue)
					continue
				}
				entryChanged = true
			}
			if !entryChanged {
				keptEntries = append(keptEntries, entryValue)
				continue
			}
			eventChanged = true
			if len(keptHooks) > 0 {
				entry["hooks"] = keptHooks
				keptEntries = append(keptEntries, entryValue)
			}
		}
		if !eventChanged {
			continue
		}
		changed = true
		if len(keptEntries) == 0 {
			delete(events, event)
		} else {
			events[event] = keptEntries
		}
	}
	return changed
}

// templatePlacements maps each template command to its (event, matcher) pairs.
func templatePlacements(expected []ExpectedHook) map[string]map[[2]string]bool {
	placements := map[string]map[[2]string]bool{}
	for _, wanted := range expected {
		if placements[wanted.Command] == nil {
			placements[wanted.Command] = map[[2]string]bool{}
		}
		placements[wanted.Command][[2]string{wanted.Event, wanted.Matcher}] = true
	}
	return placements
}
