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
		// The delta above already covers a hook this run wrote at a NEW
		// slot — including a legacy hook whose text `rewriteCommandFields`
		// converts to canonical in place without moving it to the
		// template's own event.
		// What it never credits on its own is a hook nothing changed this
		// run AND the ledger never recorded — an already fully-wired host
		// predating the ownership ledger, or one converged by an install
		// version before it existed for that hook
		// (TestInstallOwnershipLedgerClaimsHooksAlreadyPresentInSettings,
		// TestInstallOwnershipLedgerClaimsHooksDespiteForeignHooksPresent).
		// That hook belongs to the installer when — and ONLY when — it sits
		// at exactly the canonical (event, matcher, command) triple a
		// template wires (`installerOwnedHookKey` on THIS key), AND
		// `hasMixedOwnershipEntry` is false: no hook ENTRY anywhere in this
		// same file mixes an installer-owned-by-text command with a
		// foreign one in the SAME matcher-group array
		// (`settingsDocumentHasMixedOwnershipEntry`). That per-file gate,
		// unlike whole-document purity, does not fire on a foreign hook
		// that merely coexists in a SEPARATE entry at an event the
		// installer also wires (a real host's monitoring/SessionStart/
		// SessionEnd hooks alongside a fully-wired install — the exact
		// defect TestInstallOwnershipLedgerClaimsHooksDespiteForeignHooksPresent
		// pins), but DOES fire the moment an operator's hook shares an
		// entry with installer-adjacent text (the PostToolUse/
		// UserPromptSubmit manual copies
		// TestDreamHookPauseRetiresEveryCopyAndPreservesNeighbors
		// pins as permanently NOT owned) — proof, anywhere in the file,
		// that an operator hand-edited an installer-adjacent hook list, so
		// an untouched-but-canonical-looking neighbor is no longer
		// trustworthy evidence of a prior pfm install. It is computed from
		// `after` (the converged document) alone, never from a before/after
		// delta, so it is stable across repeat applies: an operator's
		// foreign sibling is never removed by wiring, so a file once
		// flagged mixed stays flagged on every later idle re-scan too —
		// unlike a `before`-vs-`after` "did this call write anything"
		// signal, which flips between a migrating first run and an idle
		// second one and would make the ledger and its uninstall behavior
		// depend on which apply happened to observe the file first.
		// Uninstall only ever removes what `owned` lists, so a wrongly
		// claimed key is a wrongly deleted operator hook.
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
	for _, hook := range append(claudeHookTemplates(home), codexHookTemplate(home)) {
		if hook.Command == command {
			return true
		}
	}
	return false
}

func installerOwnedHookKey(key settingsHookKey, pfmBinary string) bool {
	home := filepath.Dir(filepath.Dir(filepath.Dir(pfmBinary)))
	for _, hook := range append(claudeHookTemplates(home), codexHookTemplate(home)) {
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
