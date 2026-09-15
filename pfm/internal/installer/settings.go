package installer

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

func updateSettings(
	raw []byte,
	home string,
	uninstall bool,
	owned settingsHookCounts,
) ([]byte, bool, settingsHookCounts, error) {
	var document map[string]any
	if err := unmarshalKeepingNumbers(raw, &document); err != nil {
		return nil, false, nil, err
	}
	oldBinary := home + "/.local/bin/cc-fleet"
	pfmBinary := home + "/.local/bin/pfm"
	expected := claudeHookTemplates(home)
	clearCommand := commandByName(expected, "clear-kill")
	overlayStatusCommand := StatusLineOverlayCommand(home)
	usageCommand := commandByName(expected, "usage")
	exploreDenyCommand := commandByName(expected, "explore-deny")
	epicInjectCommand := commandByName(expected, "epic-inject")
	reloadInterceptCommand := commandByName(expected, "reload-intercept")
	exitInterceptCommand := commandByName(expected, "exit-intercept")
	exitCloseCommand := commandByName(expected, "exit-close")
	compactNudgeCommand := commandByName(expected, "compact-nudge")
	launcherRepairCommand := commandByName(expected, "launcher-repair")

	changed := false
	before := countSettingsHookCommands(document)
	if uninstall {
		if removeOwnedSettingsHooks(document, owned) {
			changed = true
		}
	} else {
		changed = rewriteCommandFields(document, func(command string) string {
			switch {
			case strings.Contains(command, "explore-deny.sh"):
				return exploreDenyCommand
			case command == oldBinary || strings.HasPrefix(command, oldBinary+" "):
				return pfmBinary + strings.TrimPrefix(command, oldBinary)
			default:
				return command
			}
		})
	}
	if removeRetiredHookCommands(document, pfmBinary) {
		changed = true
	}
	if _, present := document["cleanupPeriodDays"]; !present && !uninstall {
		document["cleanupPeriodDays"] = float64(36500)
		changed = true
	}

	// pfm's own `pfm statusline` historically read token usage from stale
	// internal state; overlayStatusCommand (~/.local/bin/pfm-statusline,
	// wired by wireHostOverlays) recomputes true occupancy and passes the
	// line through unmodified on any internal error, so it can never render
	// worse than the raw command. Every form this installer or its
	// predecessors have ever pointed statusLine.command at — empty, the
	// legacy shell script, or RawStatusLineCommand's bare/absolute
	// `pfm statusline` (the same two forms `pfm doctor` names by exact
	// string, so a host it flags red and a host `pfm install` repairs are
	// always in agreement) — converges on the overlay; a genuinely custom
	// command (an operator's own statusline) is left exactly as it is.
	status, _ := document["statusLine"].(map[string]any)
	currentStatus, _ := status["command"].(string)
	switch {
	case uninstall:
		if currentStatus == overlayStatusCommand || RawStatusLineCommand(home, currentStatus) {
			delete(document, "statusLine")
			changed = true
		}
	case currentStatus == "":
		document["statusLine"] = map[string]any{
			"type":                 "command",
			"command":              overlayStatusCommand,
			"padding":              float64(0),
			"refreshInterval":      float64(3),
			"hideVimModeIndicator": true,
		}
		changed = true
	case currentStatus != overlayStatusCommand &&
		(strings.Contains(currentStatus, "statusline-command.sh") || RawStatusLineCommand(home, currentStatus)):
		status["type"] = "command"
		status["command"] = overlayStatusCommand
		changed = true
	}

	entries := hookEntries(document, "UserPromptSubmit", !uninstall)
	seenUserPromptCommands := map[string]bool{}
	for _, entry := range entries {
		hooks, _ := entry["hooks"].([]any)
		kept := hooks[:0]
		for _, hookValue := range hooks {
			hook, _ := hookValue.(map[string]any)
			command, _ := hook["command"].(string)
			original := command
			if !uninstall && strings.Contains(command, "cc-usage-hook.sh") {
				command = usageCommand
			}
			if command != original {
				hook["command"] = command
				hook["type"] = "command"
				changed = true
			}
			if isRetiredHookCommand(command, pfmBinary) {
				changed = true
				continue
			}
			if !uninstall && (command == usageCommand || command == epicInjectCommand ||
				command == reloadInterceptCommand || command == exitInterceptCommand) {
				if seenUserPromptCommands[command] {
					changed = true
					continue
				}
				seenUserPromptCommands[command] = true
			}
			kept = append(kept, hookValue)
		}
		entry["hooks"] = kept
	}
	pruneEmptyHooks(document, "UserPromptSubmit")
	if !uninstall {
		for _, entry := range hookEntries(document, "PreToolUse", true) {
			hooks, _ := entry["hooks"].([]any)
			for _, hookValue := range hooks {
				hook, _ := hookValue.(map[string]any)
				command, _ := hook["command"].(string)
				if command == exploreDenyCommand {
					if entry["matcher"] != "Agent|Task" {
						if settingsHookEntryHasMixedOwnership(entry, pfmBinary) {
							continue
						}
						entry["matcher"] = "Agent|Task"
						changed = true
					}
				}
			}
		}
	}

	clearSeen := false
	for _, entry := range hookEntries(document, "SessionEnd", false) {
		hooks, _ := entry["hooks"].([]any)
		kept := hooks[:0]
		for _, hookValue := range hooks {
			hook, _ := hookValue.(map[string]any)
			command, _ := hook["command"].(string)
			if isRetiredHookCommand(command, pfmBinary) {
				changed = true
				continue
			}
			if command == clearCommand {
				if !uninstall && clearSeen {
					changed = true
					continue
				}
				clearSeen = true
			}
			kept = append(kept, hookValue)
		}
		entry["hooks"] = kept
	}
	pruneEmptyHooks(document, "SessionEnd")

	if !uninstall {
		if !hasHookCommandWithMatcher(hookEntries(document, "SessionStart", true), launcherRepairCommand, "") {
			appendHookWithMatcher(document, "SessionStart", "", launcherRepairCommand)
			changed = true
		}
		if !hasHookCommand(hookEntries(document, "UserPromptSubmit", true), usageCommand) {
			appendHook(document, "UserPromptSubmit", usageCommand)
			changed = true
		}
		if !clearSeen {
			appendHook(document, "SessionEnd", clearCommand)
			changed = true
		}
		if !hasHookCommandWithMatcher(hookEntries(document, "PreToolUse", true), exploreDenyCommand, "Agent|Task") {
			appendHookWithMatcher(document, "PreToolUse", "Agent|Task", exploreDenyCommand)
			changed = true
		}
		if !hasHookCommandWithMatcher(hookEntries(document, "UserPromptSubmit", true), epicInjectCommand, "") {
			appendHookWithMatcher(document, "UserPromptSubmit", "", epicInjectCommand)
			changed = true
		}
		if !hasHookCommandWithMatcher(hookEntries(document, "UserPromptSubmit", true), reloadInterceptCommand, "") {
			appendHookWithMatcher(document, "UserPromptSubmit", "", reloadInterceptCommand)
			changed = true
		}
		if !hasHookCommandWithMatcher(hookEntries(document, "UserPromptSubmit", true), exitInterceptCommand, "") {
			appendHookWithMatcher(document, "UserPromptSubmit", "", exitInterceptCommand)
			changed = true
		}
		if !hasHookCommandWithMatcher(hookEntries(document, "SessionEnd", true), exitCloseCommand, "") {
			appendHookWithMatcher(document, "SessionEnd", "", exitCloseCommand)
			changed = true
		}
		if !hasHookCommandWithMatcher(hookEntries(document, "UserPromptSubmit", true), compactNudgeCommand, "") {
			appendHookWithMatcher(document, "UserPromptSubmit", "", compactNudgeCommand)
			changed = true
		}
		if normalizeExpectedHookTypes(document, expected) {
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
		return nil, false, nil, fmt.Errorf("encode settings: %w", err)
	}
	return append(updated, '\n'), true, nextOwned, nil
}

func hasPreservedMixedExploreDenyMatcher(raw []byte, pfmBinary string) bool {
	var document map[string]any
	if json.Unmarshal(raw, &document) != nil {
		return false
	}
	exploreDeny := pfmBinary + " internal explore-deny"
	for _, entry := range hookEntries(document, "PreToolUse", false) {
		if entry["matcher"] == "Agent|Task" || !settingsHookEntryHasMixedOwnership(entry, pfmBinary) {
			continue
		}
		hooks, _ := entry["hooks"].([]any)
		for _, hookValue := range hooks {
			hook, _ := hookValue.(map[string]any)
			if hook["command"] == exploreDeny {
				return true
			}
		}
	}
	return false
}

func rewriteCommandFields(value any, rewrite func(string) string) bool {
	changed := false
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if key == "command" {
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
				command, _ := hook["command"].(string)
				if replacement, ok := commands[command]; ok && hook["type"] == "command" {
					hook["command"] = replacement
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

// retiredHookCommands is the installer's single table of subcommands a
// settings.json or Codex hooks.json hook entry may still carry from before a
// rename or a full retirement. Both wiring loops below strip any hook whose
// command matches one of these — from either binary name, prefixed by any
// path, or invoked bare via $PATH — and ProbeExpectedHooks (and the shared
// Codex path it also serves) reads the same table to flag a live host that
// still carries one as "stale" rather than saying nothing about it at all.
var retiredHookCommands = []struct {
	Name       string
	Subcommand string
}{
	{Name: "bb", Subcommand: "bb"},
	{Name: "bb", Subcommand: "chat bb"},
	{Name: "clear-hide", Subcommand: "internal clear-hide"},
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
	for _, binary := range []string{"pfm", "cc-fleet"} {
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
	isPFMBinary := head == pfmBinary || head == "pfm" || head == "cc-fleet" ||
		strings.HasSuffix(head, "/pfm") || strings.HasSuffix(head, "/cc-fleet")
	if !isPFMBinary {
		return "", false
	}
	home := filepath.Dir(filepath.Dir(filepath.Dir(pfmBinary)))
	for _, hook := range append(claudeHookTemplates(home), codexHookTemplate(home)) {
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

// UnknownPFMHookCommands parses a settings.json or Codex hooks.json document
// and returns the names (unknownPFMHookCommand's shape) of every hook
// command present that is of pfm's own shape but names a subcommand this
// binary neither implements nor recognizes as retired — the residue a
// stranded rollback leaves (issue #24 finding 2). A document this binary
// cannot parse returns nil, never a guess.
func UnknownPFMHookCommands(raw []byte, home string) []string {
	var document map[string]any
	if json.Unmarshal(raw, &document) != nil {
		return nil
	}
	pfmBinary := filepath.Join(home, ".local", "bin", "pfm")
	seen := map[string]bool{}
	events, _ := document["hooks"].(map[string]any)
	for _, eventValue := range events {
		entries, _ := eventValue.([]any)
		for _, entryValue := range entries {
			entry, _ := entryValue.(map[string]any)
			hooks, _ := entry["hooks"].([]any)
			for _, hookValue := range hooks {
				hook, _ := hookValue.(map[string]any)
				command, _ := hook["command"].(string)
				if name, ok := unknownPFMHookCommand(command, pfmBinary); ok {
					seen[name] = true
				}
			}
		}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
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
				command, _ := hook["command"].(string)
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

func hasHookCommand(entries []map[string]any, wanted string) bool {
	for _, entry := range entries {
		hooks, _ := entry["hooks"].([]any)
		for _, value := range hooks {
			hook, _ := value.(map[string]any)
			if hook["command"] == wanted {
				return true
			}
		}
	}
	return false
}

func hasHookCommandWithMatcher(entries []map[string]any, wanted, matcher string) bool {
	for _, entry := range entries {
		if entry["matcher"] != matcher {
			continue
		}
		hooks, _ := entry["hooks"].([]any)
		for _, value := range hooks {
			hook, _ := value.(map[string]any)
			if hook["command"] == wanted {
				return true
			}
		}
	}
	return false
}

func normalizeExpectedHookTypes(document map[string]any, expected []ExpectedHook) bool {
	changed := false
	for _, wanted := range expected {
		for _, entry := range hookEntries(document, wanted.Event, false) {
			matcher, _ := entry["matcher"].(string)
			if matcher != wanted.Matcher {
				continue
			}
			hooks, _ := entry["hooks"].([]any)
			for _, hookValue := range hooks {
				hook, _ := hookValue.(map[string]any)
				if hook["command"] == wanted.Command && hook["type"] != "command" {
					hook["type"] = "command"
					changed = true
				}
			}
		}
	}
	return changed
}

func appendHook(document map[string]any, event, command string) {
	appendHookWithMatcher(document, event, "", command)
}

func appendHookWithMatcher(document map[string]any, event, matcher, command string) {
	hooks, _ := document["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
		document["hooks"] = hooks
	}
	values, _ := hooks[event].([]any)
	hooks[event] = append(values, map[string]any{
		"matcher": matcher,
		"hooks": []any{map[string]any{
			"type": "command", "command": command,
		}},
	})
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
