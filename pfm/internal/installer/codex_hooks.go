package installer

import (
	"context"
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

// codexResumeUnkillHookName names the one Codex hook pfm owns: the claude hook
// template row whose command, matcher and event Codex's SessionStart entry
// copies, so the hook has a single spelling.
const codexResumeUnkillHookName = "resume-unkill"

// codexHookEvent is how Codex's hooks/list spells the SessionStart event.
const codexHookEvent = "sessionStart"

// codexClearMatcher is the matcher the retired Codex SessionStart clear-kill
// hook used to carry. It remains only so leftover entries can be removed; no
// current ownership path recognizes or writes this retired shape.
const codexClearMatcher = "startup|resume|clear"

// codexResumeUnkillHook is the expected SessionStart "resume" hook of a Codex
// account, read from the same template list the Claude settings use.
func codexResumeUnkillHook(home string) (ExpectedHook, error) {
	for _, hook := range claudeHookTemplates(home) {
		if hook.Name == codexResumeUnkillHookName {
			return hook, nil
		}
	}
	return ExpectedHook{}, fmt.Errorf(
		"no %q row in the hook templates: Codex hooks.json cannot be wired", codexResumeUnkillHookName,
	)
}

// codexHookHandlerCount counts the handlers carrying the hook's command inside
// an entry of the hook's event whose matcher is the hook's matcher.
func codexHookHandlerCount(document map[string]any, hook ExpectedHook) int {
	count := 0
	for _, entry := range hookEntries(document, hook.Event, false) {
		if matcher, _ := entry["matcher"].(string); matcher != hook.Matcher {
			continue
		}
		handlers, _ := entry["hooks"].([]any)
		for _, handlerValue := range handlers {
			handler, _ := handlerValue.(map[string]any)
			if command, _ := handler[configCommandKey].(string); command == hook.Command {
				count++
			}
		}
	}
	return count
}

// ensureCodexHook appends the hook as its own entry when no matching handler
// exists, creating the hooks object and the event array as needed, and reports
// whether it wrote.
func ensureCodexHook(document map[string]any, hook ExpectedHook) bool {
	if codexHookHandlerCount(document, hook) > 0 {
		return false
	}
	hooks, _ := document["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
		document["hooks"] = hooks
	}
	entries, _ := hooks[hook.Event].([]any)
	hooks[hook.Event] = append(entries, map[string]any{
		"matcher": hook.Matcher,
		"hooks": []any{map[string]any{
			"type": "command", configCommandKey: hook.Command, "timeout": 30,
		}},
	})
	return true
}

// updateCodexHooks preserves personal handlers, owns exactly one Codex hook —
// the SessionStart "resume" resume-unkill handler — and retires pfm's others:
// the clear-kill hook, and the SessionStart appendix hook whose truncated,
// compaction-dropped delivery developer_instructions replaced. An uninstall
// strips the owned hook through the ledger.
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

	if !uninstall {
		resumeUnkill, err := codexResumeUnkillHook(home)
		if err != nil {
			return nil, false, nil, err
		}
		stale := settingsHookCounts{}
		for key, count := range owned {
			if key.Event == resumeUnkill.Event && key.Matcher == resumeUnkill.Matcher &&
				key.Command != resumeUnkill.Command && strings.HasSuffix(key.Command, "/.local/bin/pfm internal resume-unkill") {
				stale[key] = count
			}
		}
		if removeOwnedSettingsHooks(document, stale) {
			changed = true
		}
		if ensureCodexHook(document, resumeUnkill) {
			changed = true
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
	configuredHomes := map[string]bool{}
	for _, home := range installer.codexHomes() {
		configuredHomes[physicalSettingsPath(filepath.Join(home, "hooks.json"))] = true
	}
	for path, owned := range ownership {
		if filepath.Base(path) == "hooks.json" && !configuredHomes[path] && len(owned) > 0 {
			return fmt.Errorf(
				"retired Codex account still owns hooks at %s; restore that account to the roster and uninstall its hooks before removing it",
				path,
			)
		}
	}
	seen := map[string]bool{}
	// carriesHook records, per physical hooks.json, whether the file the install
	// leaves behind holds the owned resume-unkill handler — the precondition of
	// asking Codex to trust it.
	carriesHook := map[string]bool{}
	uninstalling := installer.options.Mode == ModeUninstall
	if len(installer.codexHomes()) == 0 {
		installer.skip("no Codex accounts configured — hooks.json wiring has nothing to wire")
	}
	var loopErr error
	for _, codexHome := range installer.codexHomes() {
		loopErr = func() error {
			path := filepath.Join(codexHome, "hooks.json")
			physical := physicalSettingsPath(path)
			if seen[physical] {
				return nil
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
					return nil
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
					return fmt.Errorf(
						"refuse to strand owned hooks in invalid Codex hooks JSON at %s: %w",
						path,
						updateErr,
					)
				}
				// A hooks file the operator broke by hand is skipped loudly and the
				// run continues. Only owned hooks that would be stranded justify
				// stopping — one unparseable seat file must not cost the machine
				// its MCP clients, log default, shell line and update metadata.
				installer.skip("invalid Codex hooks JSON at " + path + ": " + updateErr.Error())
				return nil
			}
			if !uninstalling {
				final := raw
				if changed {
					final = updated
				}
				carries, err := codexHooksCarryResumeUnkill(final, installer.options.Home)
				if err != nil {
					return fmt.Errorf("inspect Codex hooks %s: %w", path, err)
				}
				carriesHook[physical] = carries
			}

			backup := ""
			if existed {
				backup = availableBackup(path, installer.stamp)
			}
			if !changed {
				installer.ok(path + " wiring")
			} else if err := installer.change(changeDescription(path, existed), func() error {
				if existed {
					if err := copyBackup(path, backup); err != nil {
						return fmt.Errorf("backup %s: %w", path, err)
					}
				}
				return atomicfile.Write(physical, updated, 0o600)
			}); err != nil {
				return err
			}
			// The ledger follows the file: a write that failed owns nothing new.
			if len(nextOwned) == 0 {
				delete(ownership, physical)
			} else {
				ownership[physical] = nextOwned
			}
			return nil
		}()
		if loopErr != nil {
			break
		}
	}
	// Claude settings rows go; a configured home's hooks file stays under
	// whatever name its link resolves to, the homes a stopped loop never
	// reached included.
	configured := map[string]bool{}
	for _, codexHome := range installer.codexHomes() {
		configured[physicalSettingsPath(filepath.Join(codexHome, "hooks.json"))] = true
	}
	for path := range ownership {
		if filepath.Base(path) != "hooks.json" && !configured[path] {
			delete(ownership, path)
		}
	}
	ledgerErr := installer.writeSettingsHookOwnership(ownershipPath, ownershipRaw, ownership)
	if err := errors.Join(loopErr, ledgerErr); err != nil {
		return err
	}
	return installer.wireCodexHookTrust(carriesHook)
}

// codexHooksCarryResumeUnkill reports whether a hooks.json body holds the
// owned resume-unkill handler.
func codexHooksCarryResumeUnkill(raw []byte, home string) (bool, error) {
	hook, err := codexResumeUnkillHook(home)
	if err != nil {
		return false, err
	}
	var document map[string]any
	if err := unmarshalKeepingNumbers(raw, &document); err != nil {
		return false, err
	}
	return codexHookHandlerCount(document, hook) > 0, nil
}

// wireCodexHookTrust records or removes Codex's trust of the resume-unkill hook
// per physical account, and cleans up after the retired appendix hook.
func (installer *engine) wireCodexHookTrust(carriesHook map[string]bool) error {
	if _, err := codexResumeUnkillHook(installer.options.Home); err != nil {
		return err
	}
	seenAccounts := map[string]bool{}
	for _, account := range installer.codexHomes() {
		physical := physicalSettingsPath(account)
		if seenAccounts[physical] {
			continue
		}
		seenAccounts[physical] = true
		err := func() error {
			// Retired appendix trust is cleaned up on install and uninstall.
			if codexappendix.TrustRecorded(account) {
				if err := installer.change(
					"remove retired appendix hook trust "+account,
					func() error { return codexappendix.Unregister(account) },
				); err != nil {
					return err
				}
			}
			return installer.wireResumeUnkillTrust(account, carriesHook)
		}()
		if err != nil {
			//nolint:staticcheck // Codex is a product name.
			failure := fmt.Errorf(
				"Codex hook trust for %s: %w",
				account,
				err,
			)
			installer.deferFailure(installer.fail(failure))
		}
	}
	return nil
}

func (installer *engine) wireResumeUnkillTrust(account string, carriesHook map[string]bool) error {
	if installer.options.Mode == ModeUninstall {
		if !codexappendix.HookTrustRecorded(account) {
			return nil
		}
		return installer.change(
			"remove resume-unkill hook trust "+account,
			func() error { return codexappendix.UnregisterHookTrust(account) },
		)
	}
	if !carriesHook[physicalSettingsPath(filepath.Join(account, "hooks.json"))] {
		return nil
	}
	if installer.options.CodexBinary == "" {
		installer.skip("Codex hook trust not recorded for " + account +
			": no Codex binary configured — the resume-unkill hook stays untrusted")
		return nil
	}
	hook, err := codexResumeUnkillHook(installer.options.Home)
	if err != nil {
		return err
	}
	return installer.change("trust resume-unkill hook "+account, func() error {
		if err := codexappendix.RegisterHookTrust(
			context.Background(), installer.options.CodexBinary, account, codexHookEvent, hook.Matcher, hook.Command,
		); err != nil {
			return fmt.Errorf("record resume-unkill hook trust for %s: %w", account, err)
		}
		return nil
	})
}
