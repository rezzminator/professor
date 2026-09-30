package installer

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// subagentStatusLineKey is Claude Code's settings key for the per-row body
// of its agent panel.
const subagentStatusLineKey = "subagentStatusLine"

func rewriteCommandFields(value any, rewrite func(string) string) bool {
	changed := false
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if key == configCommandKey {
				if command, ok := child.(string); ok {
					updated := rewrite(command)
					if updated != command {
						typed[key] = updated
						changed = true
					}
				}
			}
			if rewriteCommandFields(child, rewrite) {
				changed = true
			}
		}
	case []any:
		for _, child := range typed {
			if rewriteCommandFields(child, rewrite) {
				changed = true
			}
		}
	}
	return changed
}

// rewriteMemoryHelperHookPaths changes only complete, no-argument shell
// command forms for a memory-wire helper whose old file was independently
// proven installer-owned and paired with a ready destination. It deliberately
// does not use rewriteCommandFields: command-looking values outside hooks and
// compound shell commands are operator content.
func rewriteMemoryHelperHookPaths(raw []byte, paths map[string]string, home string) ([]byte, bool, error) {
	var document map[string]any
	if err := unmarshalKeepingNumbers(raw, &document); err != nil {
		return nil, false, err
	}
	if document == nil {
		return nil, false, fmt.Errorf("settings must be an object")
	}
	commands := make(map[string]string)
	for oldPath, newPath := range paths {
		addMemoryHelperCommandForms(commands, oldPath, newPath)
		defaultOld := filepath.Join(home, ".claude", "scripts", "cc-memory-wire.sh")
		if filepath.Clean(oldPath) == filepath.Clean(defaultOld) {
			addMemoryHelperCommandForms(
				commands,
				"$HOME/.claude/scripts/cc-memory-wire.sh",
				"$HOME/.claude/scripts/memory-wire.sh",
			)
		}
	}

	changed := false
	events, ok := document["hooks"].(map[string]any)
	if _, present := document["hooks"]; present && !ok {
		return nil, false, fmt.Errorf("settings hooks must be an object")
	}
	for _, eventValue := range events {
		entries, ok := eventValue.([]any)
		if !ok {
			return nil, false, fmt.Errorf("settings hook event must be an array")
		}
		for _, entryValue := range entries {
			entry, ok := entryValue.(map[string]any)
			if !ok {
				return nil, false, fmt.Errorf("settings hook entry must be an object")
			}
			hooks, ok := entry["hooks"].([]any)
			if !ok {
				return nil, false, fmt.Errorf("settings hook entry hooks must be an array")
			}
			for _, hookValue := range hooks {
				hook, ok := hookValue.(map[string]any)
				if !ok {
					return nil, false, fmt.Errorf("settings hook must be an object")
				}
				command, _ := hook[configCommandKey].(string)
				if replacement, ok := commands[command]; ok && hook[configTypeKey] == commandType {
					hook[configCommandKey] = replacement
					changed = true
				} else {
					// Refusal is intentionally more conservative than rewriting:
					// split quotes and alternate HOME spellings still reference the
					// same owned helper, even though we do not parse shell programs.
					unquoted := strings.NewReplacer(`"`, "", "'", "").Replace(command)
					for oldPath := range paths {
						referencesOld := strings.Contains(unquoted, oldPath)
						if relative, err := filepath.Rel(
							home,
							oldPath,
						); err == nil && relative != ".." &&
							!strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
							for _, prefix := range []string{"$HOME/", "${HOME}/", "~/"} {
								referencesOld = referencesOld ||
									strings.Contains(unquoted, prefix+filepath.ToSlash(relative))
							}
						}
						if referencesOld {
							return nil, false, fmt.Errorf(
								"memory helper hook requires manual migration before retiring %s: %q",
								oldPath,
								command,
							)
						}
					}
				}
			}
		}
	}
	if !changed {
		return raw, false, nil
	}
	updated, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, false, fmt.Errorf("encode settings: %w", err)
	}
	return append(updated, '\n'), true, nil
}

func addMemoryHelperCommandForms(commands map[string]string, oldPath, newPath string) {
	for _, shell := range []string{"", "sh ", "bash "} {
		for _, quote := range []string{"", `"`, `'`} {
			commands[shell+quote+oldPath+quote] = shell + quote + newPath + quote
		}
	}
}

// retiredHookCommands is the shared table of subcommands old account settings
// or Codex hooks may carry. The account stripper and Codex hook writer remove
// them; the Codex doctor probe reports them as STALE.
var retiredHookCommands = []struct {
	Name       string
	Subcommand string
}{
	{Name: "bb", Subcommand: "bb"},
	{Name: "bb", Subcommand: "chat bb"},
	{Name: "clear-hide", Subcommand: "internal clear-hide"},
	{Name: "compact-nudge", Subcommand: "internal compact-nudge"},
	{Name: "dream-agent-inject", Subcommand: "dream hook agent-inject"},
	{Name: "dream-nudge", Subcommand: "dream hook nudge"},
	{Name: "dream-codex-subagent-inject", Subcommand: "dream hook codex-subagent-inject"},
	{Name: "group", Subcommand: "chat group hook"},
}

// retiredHookShimHints are legacy shell-script hook file substrings that
// predate the pfm/cc-fleet binary hooks entirely, matched by substring
// rather than by binary-prefixed subcommand.
var retiredHookShimHints = []struct {
	Name string
	Hint string
}{
	{Name: "bb", Hint: "bb-hook.sh"},
	{Name: "dream-agent-inject", Hint: "dreamer-agent-inject.sh"},
	{Name: "dream-nudge", Hint: "dreamer-nudge.sh"},
}

// retiredHookCommandName reports whether command matches a retired hook
// table entry and, if so, the name to report it under.
func retiredHookCommandName(command string) (string, bool) {
	command = strings.TrimSpace(command)
	if command == "" {
		return "", false
	}
	for _, binary := range []string{MCPClientPFM, legacyFleetBinary} {
		for _, retired := range retiredHookCommands {
			full := binary + " " + retired.Subcommand
			if command == full || strings.HasSuffix(command, "/"+full) {
				return retired.Name, true
			}
		}
	}
	for _, hint := range retiredHookShimHints {
		if strings.Contains(command, hint.Hint) {
			return hint.Name, true
		}
	}
	return "", false
}

// subcommandRegistry is implementedSubcommands' type, named so a test can
// reset it to its unset zero value between cases.
type subcommandRegistry struct {
	set      bool
	topLevel map[string]bool
	internal map[string]bool
}

// implementedSubcommands is the seam cmd/pfm's main() fills in via
// SetImplementedSubcommands before argv dispatch — installer cannot import
// cmd/pfm (a main package), so this package-level registry, set once at
// process start, is how unknownPFMHookCommand learns what THIS binary's own
// dispatch actually reaches (issue #24 F1). Left unset (any test or tool
// that never calls SetImplementedSubcommands), the predicate fails CLOSED:
// it never reports a command unknown, so nothing in this package ever
// deletes a hook on the strength of a guess about implementation.
var implementedSubcommands subcommandRegistry

// SetImplementedSubcommands records the exact top-level and `internal <x>`
// subcommand names cmd/pfm's own dispatch (main.go's topLevelSubcommands and
// internalSubcommands) reaches — the single fact unknownPFMHookCommand needs
// to tell an operator's own hand-wired hook (`pfm doctor`, `pfm internal
// claude-version`) apart from genuine residue a rolled-back or
// newer-then-reverted pfm left behind. Call it once, before dispatching
// argv; every pfm process that skips this call keeps the fail-closed
// (never-unknown) default above.
func SetImplementedSubcommands(topLevel, internal []string) {
	implementedSubcommands.topLevel = make(map[string]bool, len(topLevel))
	for _, name := range topLevel {
		implementedSubcommands.topLevel[name] = true
	}
	implementedSubcommands.internal = make(map[string]bool, len(internal))
	for _, name := range internal {
		implementedSubcommands.internal[name] = true
	}
	implementedSubcommands.set = true
}

// subcommandIsImplemented reports whether the registry SetImplementedSubcommands
// filled in names word as a subcommand THIS binary's dispatch reaches — a
// no-op "yes" (fail closed toward keeping the hook) until the registry is
// set. word is the first token only: a hook naming "doctor --verbose" is
// judged on "doctor", the subcommand dispatch itself switches on.
func subcommandIsImplemented(isInternal bool, name string) bool {
	if !implementedSubcommands.set {
		return true
	}
	word, _, _ := strings.Cut(name, " ")
	if isInternal {
		return implementedSubcommands.internal[word]
	}
	return implementedSubcommands.topLevel[word]
}

// unknownPFMHookCommand reports whether command is shaped like a hook this
// or a prior pfm binary would have written — "<pfmBinary> internal <name>"
// or "<pfmBinary> <name>", or the bare "pfm"/"cc-fleet" and any-path "/pfm"/
// "/cc-fleet" suffix forms retiredHookCommandName already accepts — naming a
// subcommand this binary's OWN dispatch does not implement, per the registry
// SetImplementedSubcommands fills in from cmd/pfm's topLevelSubcommands /
// internalSubcommands (issue #24 F1). Unlike the table-retired shapes, that
// combination only arises when a newer or rolled-back pfm wrote it: this
// binary can name the entry but not run it, and no exact-string removal path
// this binary owns ever strips it. The guard is on the binary token alone —
// a command that merely CONTAINS "pfm" elsewhere (an operator's own script
// invoked with a "--tag pfm" argument, say) never matches, because its first
// token is not one of these forms. A subcommand this binary DOES implement —
// an operator's own `pfm doctor` or `pfm internal claude-version` hook — is
// never reported unknown, whether or not it also happens to be one of the
// installer's own automatic templates.
func unknownPFMHookCommand(command, pfmBinary string) (string, bool) {
	command = strings.TrimSpace(command)
	if command == "" {
		return "", false
	}
	head, rest, found := strings.Cut(command, " ")
	if !found || strings.TrimSpace(rest) == "" {
		return "", false
	}
	isPFMBinary := head == pfmBinary || head == MCPClientPFM || head == legacyFleetBinary ||
		strings.HasSuffix(head, "/pfm") || strings.HasSuffix(head, "/cc-fleet")
	if !isPFMBinary {
		return "", false
	}
	home := filepath.Dir(filepath.Dir(filepath.Dir(pfmBinary)))
	for _, hook := range claudeHookTemplates(home) {
		if _, hookRest, ok := strings.Cut(hook.Command, " "); ok && hookRest == rest {
			return "", false
		}
	}
	if _, retired := retiredHookCommandName(command); retired {
		return "", false
	}
	name := rest
	isInternal := false
	if sub, ok := strings.CutPrefix(rest, "internal "); ok {
		name = sub
		isInternal = true
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return "", false
	}
	if subcommandIsImplemented(isInternal, name) {
		return "", false
	}
	return name, true
}

func isRetiredHookCommand(command, pfmBinary string) bool {
	if _, retired := retiredHookCommandName(command); retired {
		return true
	}
	_, unknown := unknownPFMHookCommand(command, pfmBinary)
	return unknown
}

// removeRetiredHookCommands strips retired automatic hooks from every event,
// not only from the event where the installer once wrote them. Operators and
// older installers may have copied a hook under another event; a real pause
// must not leave those copies firing while preserving unrelated neighbors.
// Alongside the table-retired shapes, it also strips a hook of pfm's own
// shape naming a subcommand THIS binary does not implement — what a
// rolled-back or newer-then-reverted update leaves behind
// (unknownPFMHookCommand).
func removeRetiredHookCommands(document map[string]any, pfmBinary string) bool {
	events, _ := document["hooks"].(map[string]any)
	changed := false
	for event, eventValue := range events {
		entries, ok := eventValue.([]any)
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
			hooks, ok := entry["hooks"].([]any)
			if !ok {
				keptEntries = append(keptEntries, entryValue)
				continue
			}
			keptHooks := make([]any, 0, len(hooks))
			entryChanged := false
			for _, hookValue := range hooks {
				hook, _ := hookValue.(map[string]any)
				command, _ := hook[configCommandKey].(string)
				if isRetiredHookCommand(command, pfmBinary) {
					entryChanged = true
					eventChanged = true
					changed = true
					continue
				}
				keptHooks = append(keptHooks, hookValue)
			}
			if entryChanged {
				if len(keptHooks) == 0 {
					continue
				}
				entry["hooks"] = keptHooks
			}
			keptEntries = append(keptEntries, entryValue)
		}
		if !eventChanged {
			continue
		}
		if len(keptEntries) == 0 {
			delete(events, event)
		} else {
			events[event] = keptEntries
		}
	}
	return changed
}

func hookEntries(document map[string]any, event string, create bool) []map[string]any {
	hooks, _ := document["hooks"].(map[string]any)
	if hooks == nil && create {
		hooks = map[string]any{}
		document["hooks"] = hooks
	}
	if hooks == nil {
		return nil
	}
	values, _ := hooks[event].([]any)
	if values == nil && create {
		values = []any{}
		hooks[event] = values
	}
	entries := make([]map[string]any, 0, len(values))
	for _, value := range values {
		if entry, ok := value.(map[string]any); ok {
			entries = append(entries, entry)
		}
	}
	return entries
}

func pruneEmptyHooks(document map[string]any, event string) {
	hooks, _ := document["hooks"].(map[string]any)
	if hooks == nil {
		return
	}
	values, _ := hooks[event].([]any)
	kept := values[:0]
	for _, value := range values {
		entry, _ := value.(map[string]any)
		entryHooks, _ := entry["hooks"].([]any)
		if len(entryHooks) > 0 {
			kept = append(kept, value)
		}
	}
	hooks[event] = kept
}
