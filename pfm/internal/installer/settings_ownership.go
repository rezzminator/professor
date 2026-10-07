package installer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

const (
	settingsHookOwnershipName    = "settings-hook-ownership.json"
	settingsHookOwnershipVersion = 1
)

func settingsHookOwnershipPath(managedRoot string) string {
	return filepath.Join(managedRoot, settingsHookOwnershipName)
}

type settingsHookKey struct {
	Event   string
	Matcher string
	Command string
}

type settingsHookCounts map[settingsHookKey]int

type settingsHookOwnershipRecord struct {
	Path    string `json:"path"`
	Event   string `json:"event"`
	Matcher string `json:"matcher"`
	Command string `json:"command"`
	Count   int    `json:"count"`
}

type settingsHookOwnershipDocument struct {
	Version int                           `json:"version"`
	Hooks   []settingsHookOwnershipRecord `json:"hooks"`
}

func countSettingsHookCommands(document map[string]any) settingsHookCounts {
	counts := settingsHookCounts{}
	events, _ := document["hooks"].(map[string]any)
	for event, eventValue := range events {
		entries, _ := eventValue.([]any)
		for _, entryValue := range entries {
			entry, _ := entryValue.(map[string]any)
			matcher, _ := entry["matcher"].(string)
			hooks, _ := entry["hooks"].([]any)
			for _, hookValue := range hooks {
				hook, _ := hookValue.(map[string]any)
				command, _ := hook["command"].(string)
				if command != "" {
					counts[settingsHookKey{Event: event, Matcher: matcher, Command: command}]++
				}
			}
		}
	}
	return counts
}

// settingsDocumentHasMixedOwnershipEntry reports whether any hook entry (one
// matcher-group's "hooks" array) mixes an installer-owned-by-command-text
// hook with one that is not. See nextSettingsHookOwnership's retroactive-
// claim gate for why this — not whole-document purity — is the signal it
// needs.
func settingsDocumentHasMixedOwnershipEntry(document map[string]any, pfmBinary string) bool {
	events, _ := document["hooks"].(map[string]any)
	for _, eventValue := range events {
		entries, _ := eventValue.([]any)
		for _, entryValue := range entries {
			entry, _ := entryValue.(map[string]any)
			if settingsHookEntryHasMixedOwnership(entry, pfmBinary) {
				return true
			}
		}
	}
	return false
}

func settingsHookEntryHasMixedOwnership(entry map[string]any, pfmBinary string) bool {
	hooks, _ := entry["hooks"].([]any)
	hasOwned, hasForeign := false, false
	for _, hookValue := range hooks {
		hook, _ := hookValue.(map[string]any)
		command, _ := hook["command"].(string)
		if command == "" {
			continue
		}
		if installerOwnedHookCommand(command, pfmBinary) {
			hasOwned = true
		} else {
			hasForeign = true
		}
	}
	return hasOwned && hasForeign
}

func nextSettingsHookOwnership(
	before, after, owned settingsHookCounts,
	pfmBinary string,
	uninstall bool,
	hasMixedOwnershipEntry bool,
) settingsHookCounts {
	if uninstall {
		return settingsHookCounts{}
	}
	next := settingsHookCounts{}
	for key, afterCount := range after {
		if !installerOwnedHookCommand(key.Command, pfmBinary) {
			continue
		}
		added := afterCount - before[key]
		if added < 0 {
			added = 0
		}
		count := owned[key] + added
		// Historical ledger rows may predate the receipt. Claim a canonical
		// template key only when no hook entry mixes it with operator hooks;
		// a mixed entry cannot establish which command the operator owns.
		if count == 0 && afterCount > 0 && !hasMixedOwnershipEntry && installerOwnedHookKey(key, pfmBinary) {
			count = afterCount
		}
		if count > afterCount {
			count = afterCount
		}
		if count > 0 {
			next[key] = count
		}
	}
	return next
}

func installerOwnedHookCommand(command, pfmBinary string) bool {
	home := filepath.Dir(filepath.Dir(filepath.Dir(pfmBinary)))
	for _, hook := range claudeHookTemplates(home) {
		if hook.Command == command {
			return true
		}
	}
	return false
}

func installerOwnedHookKey(key settingsHookKey, pfmBinary string) bool {
	home := filepath.Dir(filepath.Dir(filepath.Dir(pfmBinary)))
	for _, hook := range claudeHookTemplates(home) {
		if hook.Event == key.Event && hook.Matcher == key.Matcher && hook.Command == key.Command {
			return true
		}
	}
	return false
}

func removeOwnedSettingsHooks(document map[string]any, owned settingsHookCounts) bool {
	if len(owned) == 0 {
		return false
	}
	remaining := make(settingsHookCounts, len(owned))
	for key, count := range owned {
		remaining[key] = count
	}
	changed := false
	events, _ := document["hooks"].(map[string]any)
	for event, eventValue := range events {
		entries, _ := eventValue.([]any)
		keptEntries := entries[:0]
		for _, entryValue := range entries {
			entry, _ := entryValue.(map[string]any)
			matcher, _ := entry["matcher"].(string)
			hooks, _ := entry["hooks"].([]any)
			keptHooks := hooks[:0]
			entryChanged := false
			for _, hookValue := range hooks {
				hook, _ := hookValue.(map[string]any)
				command, _ := hook["command"].(string)
				key := settingsHookKey{Event: event, Matcher: matcher, Command: command}
				if remaining[key] > 0 {
					remaining[key]--
					changed = true
					entryChanged = true
					continue
				}
				keptHooks = append(keptHooks, hookValue)
			}
			if entryChanged && len(keptHooks) == 0 {
				continue
			}
			if entryChanged {
				entry["hooks"] = keptHooks
			}
			keptEntries = append(keptEntries, entryValue)
		}
		events[event] = keptEntries
	}
	return changed
}

func readSettingsHookOwnership(path string) (map[string]settingsHookCounts, []byte, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]settingsHookCounts{}, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	var document settingsHookOwnershipDocument
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, nil, fmt.Errorf("decode ownership ledger: %w", err)
	}
	if document.Version != settingsHookOwnershipVersion {
		return nil, nil, fmt.Errorf("unsupported ownership ledger version %d", document.Version)
	}
	ownership := map[string]settingsHookCounts{}
	for index, record := range document.Hooks {
		if record.Path == "" || record.Event == "" || record.Command == "" || record.Count < 1 {
			return nil, nil, fmt.Errorf("invalid ownership record %d", index)
		}
		if ownership[record.Path] == nil {
			ownership[record.Path] = settingsHookCounts{}
		}
		key := settingsHookKey{Event: record.Event, Matcher: record.Matcher, Command: record.Command}
		ownership[record.Path][key] += record.Count
	}
	canonical := map[string]settingsHookCounts{}
	for path, counts := range ownership {
		physical := physicalSettingsPath(path)
		if canonical[physical] == nil {
			canonical[physical] = settingsHookCounts{}
		}
		for key, count := range counts {
			if prior, exists := canonical[physical][key]; exists && prior != count {
				return nil, nil, fmt.Errorf("conflicting hook ownership aliases for %s in %s", key.Event, physical)
			}
			canonical[physical][key] = count
		}
	}
	return canonical, raw, nil
}

func encodeSettingsHookOwnership(ownership map[string]settingsHookCounts) ([]byte, error) {
	document := settingsHookOwnershipDocument{Version: settingsHookOwnershipVersion}
	for path, counts := range ownership {
		for key, count := range counts {
			if count < 1 {
				continue
			}
			document.Hooks = append(document.Hooks, settingsHookOwnershipRecord{
				Path: path, Event: key.Event, Matcher: key.Matcher, Command: key.Command, Count: count,
			})
		}
	}
	sort.Slice(document.Hooks, func(left, right int) bool {
		leftRecord, rightRecord := document.Hooks[left], document.Hooks[right]
		leftKey := leftRecord.Path + "\x00" + leftRecord.Event + "\x00" + leftRecord.Matcher + "\x00" + leftRecord.Command
		rightKey := rightRecord.Path + "\x00" + rightRecord.Event + "\x00" + rightRecord.Matcher + "\x00" + rightRecord.Command
		return leftKey < rightKey
	})
	encoded, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode ownership ledger: %w", err)
	}
	return append(encoded, '\n'), nil
}

func sameSettingsHookOwnership(existing []byte, ownership map[string]settingsHookCounts) (bool, []byte, error) {
	encoded, err := encodeSettingsHookOwnership(ownership)
	if err != nil {
		return false, nil, err
	}
	return bytes.Equal(existing, encoded), encoded, nil
}
