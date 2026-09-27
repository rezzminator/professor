package installer

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/codexappendix"
)

// codexClearMatcher is the matcher the retired Codex SessionStart clear-kill
// hook used to carry. It remains only so leftover entries can be removed; no
// current ownership path recognizes or writes this retired shape.
const codexClearMatcher = "startup|resume|clear"

// updateCodexHooks preserves personal handlers and retires pfm's own: the
// clear-kill hook, and the SessionStart appendix hook whose truncated,
// compaction-dropped delivery developer_instructions replaced.
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
			command, _ := hook[configCommandKey].(string)
			if _, retired := codexRetiredSessionStartHookName(command, home); retired ||
				isRetiredHookCommand(command, pfmBinary) {
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

// codexRetiredSessionStartHookName names a Codex SessionStart command pfm
// used to own and now only removes: the clear-kill hook in each of its shapes
// and binaries, and the appendix hook developer_instructions replaced. The
// writer above strips it and doctor's probe reports it STALE.
func codexRetiredSessionStartHookName(command, home string) (string, bool) {
	oldBinary := home + "/.local/bin/cc-fleet"
	pfmBinary := home + "/.local/bin/pfm"
	switch command {
	case pfmBinary + " internal clear-kill",
		oldBinary + " internal clear-kill",
		pfmBinary + ` internal clear-kill --parent "$PPID"`,
		oldBinary + ` internal clear-kill --parent "$PPID"`:
		return "codex-clear-kill", true
	case codexappendix.Command(home):
		return "codex-appendix", true
	}
	return "", false
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

func (installer *engine) wireCodexHooks() error {
	ownershipPath := settingsHookOwnershipPath(installer.managedRoot)
	ownership, ownershipRaw, err := readSettingsHookOwnership(ownershipPath)
	if err != nil {
		return fmt.Errorf("read settings hook ownership %s: %w", ownershipPath, err)
	}
	seen := map[string]bool{}
	if len(installer.codexHomes()) == 0 {
		installer.skip("no Codex accounts configured — hooks.json wiring has nothing to wire")
	}
	for _, codexHome := range installer.codexHomes() {
		path := filepath.Join(codexHome, "hooks.json")
		physical := physicalSettingsPath(path)
		if seen[physical] {
			continue
		}
		seen[physical] = true
		raw, readErr := os.ReadFile(path)
		existed := true
		if errors.Is(readErr, fs.ErrNotExist) {
			if info, statErr := os.Lstat(path); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("hooks file for Codex is a dangling symlink: %s", path)
			} else if statErr != nil && !errors.Is(statErr, fs.ErrNotExist) {
				return fmt.Errorf("inspect Codex hooks %s: %w", path, statErr)
			}
			existed = false
			raw = []byte("{\"hooks\":{}}\n")
			if installer.options.Mode == ModeUninstall {
				delete(ownership, physical)
				continue
			}
		} else if readErr != nil {
			return fmt.Errorf("read %s: %w", path, readErr)
		}
		updated, changed, nextOwned, updateErr := updateCodexHooks(
			raw,
			installer.options.Home,
			installer.options.Mode == ModeUninstall,
			ownership[physical],
		)
		if updateErr != nil {
			if installer.options.Mode == ModeUninstall && len(ownership[physical]) > 0 {
				return fmt.Errorf("refuse to strand owned hooks in invalid Codex hooks JSON at %s: %w", path, updateErr)
			}
			// A hooks file the operator broke by hand is skipped loudly and the
			// run continues. Only owned hooks that would be stranded justify
			// stopping — one unparseable seat file must not cost the machine
			// its MCP clients, log default, shell line and update metadata.
			installer.skip("invalid Codex hooks JSON at " + path + ": " + updateErr.Error())
			continue
		}
		if len(nextOwned) == 0 {
			delete(ownership, physical)
		} else {
			ownership[physical] = nextOwned
		}

		hookPaths, backup := []string{physical}, ""
		if existed {
			backup = availableBackup(path, installer.stamp)
			hookPaths = append(hookPaths, backup)
		}
		if !changed {
			installer.ok(path + " wiring")
		} else if err := installer.changePaths(changeDescription(path, existed), hookPaths, func() error {
			if existed {
				if err := copyBackup(path, backup); err != nil {
					return fmt.Errorf("backup %s: %w", path, err)
				}
			}
			return atomicfile.Write(physical, updated, 0o600)
		}); err != nil {
			return err
		}
		if err := installer.writeSettingsHookOwnership(ownershipPath, ownershipRaw, ownership); err != nil {
			return err
		}
		if len(ownership) == 0 {
			ownershipRaw = nil
		} else {
			ownershipRaw, err = encodeSettingsHookOwnership(ownership)
			if err != nil {
				return err
			}
		}
	}
	if err := installer.writeSettingsHookOwnership(ownershipPath, ownershipRaw, ownership); err != nil {
		return err
	}
	seenAccounts := map[string]bool{}
	for _, account := range installer.codexHomes() {
		physical := physicalSettingsPath(account)
		if seenAccounts[physical] {
			continue
		}
		seenAccounts[physical] = true
		// The SessionStart appendix hook is retired: the fleet prompt now
		// reaches Codex through developer_instructions. An install cleans up
		// after it exactly as an uninstall does — an existing install carries
		// the recorded trust until something takes it away.
		if !codexappendix.TrustRecorded(account) {
			continue
		}
		if err := installer.changePaths(
			"remove retired appendix hook trust "+account,
			// codexappendix.Unregister rewrites config.toml and removes its trust receipt.
			[]string{
				filepath.Join(account, "config.toml"),
				filepath.Join(account, ".professor-appendix-trust.json"),
			},
			func() error { return codexappendix.Unregister(account) },
		); err != nil {
			return err
		}
	}
	return nil
}
