package installer

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
)

func (installer *engine) reconcileUnvisitedSettingsOwnership(
	ownership map[string]settingsHookCounts,
	seen map[string]bool,
) error {
	for path, owned := range ownership {
		if seen[path] {
			continue
		}
		raw, err := os.ReadFile(path)
		if errors.Is(err, fs.ErrNotExist) {
			delete(ownership, path)
			continue
		}
		if err != nil {
			return fmt.Errorf("read unvisited owned settings path %s: %w", path, err)
		}
		if installer.options.Mode == ModeUninstall {
			return fmt.Errorf("refuse to strand hooks in unvisited owned settings path %s", path)
		}
		var document map[string]any
		if err := unmarshalKeepingNumbers(raw, &document); err != nil {
			return fmt.Errorf("refuse to drop ownership for invalid settings JSON at %s: %w", path, err)
		}
		if removeOwnedSettingsHooks(document, owned) {
			updated, err := json.MarshalIndent(document, "", "  ")
			if err != nil {
				return fmt.Errorf("encode dropped settings seat %s: %w", path, err)
			}
			updated = append(updated, '\n')
			if err := installer.change("rewrite "+path+" (dropped seat; backup preserved)", func() error {
				backup := availableBackup(path, installer.stamp)
				if err := copyBackup(path, backup); err != nil {
					return fmt.Errorf("backup %s: %w", path, err)
				}
				return atomicfile.Write(path, updated, 0o600)
			}); err != nil {
				return err
			}
		}
		delete(ownership, path)
	}
	return nil
}

// appendTemplateHook adds one template hook in its own entry, carrying
// "async": true when the template runs in the background.
func appendTemplateHook(document map[string]any, wanted ExpectedHook) {
	hook := map[string]any{configTypeKey: commandType, configCommandKey: wanted.Command}
	if wanted.Async {
		hook[configAsyncKey] = true
	}
	hooks, _ := document["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
		document["hooks"] = hooks
	}
	values, _ := hooks[wanted.Event].([]any)
	hooks[wanted.Event] = append(values, map[string]any{
		"matcher": wanted.Matcher,
		"hooks":   []any{hook},
	})
}

// dropMisplacedTemplateHooks holds the one placement rule for every pfm-owned
// hook: a template command lives exactly once under each (event, matcher)
// pair its templates name — a set per command, since callmeter's one command
// is registered under seven events. A second instance under an expected pair
// is a duplicate and is removed wherever it sits; the first in event-name and
// array order is the one kept. An instance under any other event or matcher
// is a moved hook and is removed too, unless it shares its entry with an
// operator hook: that entry was hand-edited and stays as the operator wrote
// it, the same line the explore-deny matcher convergence holds. An entry
// emptied by a removal goes, and so does an event it empties. A hook whose
// command is no template's, or an entry whose shape is not the one the
// harness reads, is never touched. The ownership ledger follows from the
// before/after counts nextSettingsHookOwnership already takes.
func dropMisplacedTemplateHooks(document map[string]any, expected []ExpectedHook, pfmBinary string) bool {
	placements := map[string]map[[2]string]bool{}
	for _, wanted := range expected {
		if placements[wanted.Command] == nil {
			placements[wanted.Command] = map[[2]string]bool{}
		}
		placements[wanted.Command][[2]string{wanted.Event, wanted.Matcher}] = true
	}
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
