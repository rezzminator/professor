package installer

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/codexappendix"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

const (
	hookEventUserPromptSubmit = "UserPromptSubmit"
	hookExploreMatcher        = "Agent|Task"
	// hookRRDirMatcher names the agents whose prompt reads the RR-DIR line:
	// templates/global/agents/rr.md and the variants declared from it.
	hookRRDirMatcher = "rr|super-rr"
	stateBroken      = "broken"
	stateDrift       = "drift"
	stateStale       = "stale"
)

// ExpectedHook is one hook the installer converges and owns.
type ExpectedHook struct {
	Target  string
	File    string
	Event   string
	Matcher string
	Command string
	Name    string
}

// HookProbeResult keeps missing, broken, and ownership drift distinct.
type HookProbeResult struct {
	Hook  ExpectedHook
	State string
	Error string
}

// ExpectedHooks is the installer's exported source of truth for doctor. The
// installer writes configured account settings only; it never writes a
// project-level .claude/settings.json or invents an engine account.
func ExpectedHooks(home string, config pfmconfig.Config) []ExpectedHook {
	type target struct {
		name string
		file string
	}
	targets := make([]target, 0, len(config.Accounts))
	for _, account := range config.Accounts {
		targets = append(targets, target{
			name: fmt.Sprintf("claude[%d]", account.ID),
			file: filepath.Join(account.ConfigDir, "settings.json"),
		})
	}
	seen := map[string]bool{}
	templates := claudeHookTemplates(home)
	result := make([]ExpectedHook, 0, len(targets)*len(templates))
	for _, target := range targets {
		physical := physicalSettingsPath(target.file)
		if seen[physical] {
			continue
		}
		seen[physical] = true
		for _, hook := range templates {
			hook.Target = target.name
			hook.File = target.file
			result = append(result, hook)
		}
	}
	for _, account := range config.CodexAccounts {
		file := filepath.Join(account.Home, "hooks.json")
		physical := physicalSettingsPath(file)
		if seen[physical] {
			continue
		}
		seen[physical] = true
		hook := codexHookTemplate(home)
		hook.Target = fmt.Sprintf("codex[%d]", account.ID)
		hook.File = file
		result = append(result, hook)
	}
	return result
}

func claudeHookTemplates(home string) []ExpectedHook {
	binary := filepath.Join(home, ".local", "bin", "pfm")
	return []ExpectedHook{
		{Event: "SessionStart", Command: binary + " internal launcher-repair", Name: "launcher-repair"},
		{Event: hookEventUserPromptSubmit, Command: binary + " usage-hook", Name: "usage"},
		{Event: "SessionEnd", Command: binary + " internal clear-kill", Name: "clear-kill"},
		{Event: "SessionEnd", Command: binary + " internal exit-close", Name: "exit-close"},
		{
			Event:   "PreToolUse",
			Matcher: hookExploreMatcher,
			Command: binary + " internal explore-deny",
			Name:    "explore-deny",
		},
		{
			Event:   "SubagentStart",
			Matcher: hookRRDirMatcher,
			Command: binary + " internal rr-dir",
			Name:    "rr-dir",
		},
		{Event: hookEventUserPromptSubmit, Command: binary + " internal epic-inject", Name: "epic-inject"},
		{Event: hookEventUserPromptSubmit, Command: binary + " internal reload-intercept", Name: "reload-intercept"},
		{Event: hookEventUserPromptSubmit, Command: binary + " internal exit-intercept", Name: "exit-intercept"},
		{Event: hookEventUserPromptSubmit, Command: binary + " internal compact-nudge", Name: "compact-nudge"},
	}
}

func commandByName(hooks []ExpectedHook, name string) string {
	for _, hook := range hooks {
		if hook.Name == name {
			return hook.Command
		}
	}
	panic("installer expected hook is missing: " + name)
}

func physicalSettingsPath(path string) string {
	candidate := filepath.Clean(path)
	var missing []string
	for {
		physical, err := filepath.EvalSymlinks(candidate)
		if err == nil {
			for i := len(missing) - 1; i >= 0; i-- {
				physical = filepath.Join(physical, missing[i])
			}
			return filepath.Clean(physical)
		}
		parent := filepath.Dir(candidate)
		if !errors.Is(err, os.ErrNotExist) || parent == candidate {
			return filepath.Clean(path)
		}
		missing = append(missing, filepath.Base(candidate))
		candidate = parent
	}
}

// ProbeExpectedHooks parses each expected file once, validates the canonical
// command path, then cross-checks the same ownership ledger uninstall reads.
func ProbeExpectedHooks(home string, config pfmconfig.Config) []HookProbeResult {
	expected := ExpectedHooks(home, config)
	ownershipPath := settingsHookOwnershipPath(managedRootForHome(home))
	ownership, _, ownershipErr := readSettingsHookOwnership(ownershipPath)
	if ownershipErr != nil {
		return []HookProbeResult{
			{
				Hook: ExpectedHook{
					Target: "ownership",
					File:   ownershipPath,
				},
				State: stateBroken,
				Error: ownershipErr.Error(),
			},
		}
	}
	type fileProbe struct {
		counts      settingsHookCounts
		allCounts   settingsHookCounts
		globalIssue string
		eventIssues map[string]string
		err         error
	}
	files := map[string]fileProbe{}
	fileTargets := map[string]string{}
	fileDisplayPaths := map[string]string{}
	for _, hook := range expected {
		physical := physicalSettingsPath(hook.File)
		if _, done := files[physical]; done {
			continue
		}
		fileTargets[physical] = hook.Target
		fileDisplayPaths[physical] = hook.File
		raw, err := os.ReadFile(hook.File)
		if err != nil {
			files[physical] = fileProbe{err: err}
			continue
		}
		var document map[string]any
		if err := json.Unmarshal(raw, &document); err != nil {
			files[physical] = fileProbe{err: fmt.Errorf("parse %s: %w", hook.File, err)}
			continue
		}
		counts, allCounts, globalIssue, eventIssues := inspectExpectedHookDocument(document)
		files[physical] = fileProbe{
			counts: counts, allCounts: allCounts,
			globalIssue: globalIssue, eventIssues: eventIssues,
		}
	}

	results := make([]HookProbeResult, 0, len(expected)*2)
	expectedKeys := map[string]bool{}
	for _, hook := range expected {
		physical := physicalSettingsPath(hook.File)
		key := settingsHookKey{Event: hook.Event, Matcher: hook.Matcher, Command: hook.Command}
		expectedKeys[physical+"\x00"+hook.Event+"\x00"+hook.Matcher+"\x00"+hook.Command] = true
		file := files[physical]
		if file.err != nil {
			state := stateBroken
			if errors.Is(file.err, os.ErrNotExist) {
				state = string(HostOverlayMissing)
			}
			results = append(results, HookProbeResult{Hook: hook, State: state, Error: file.err.Error()})
			if ownership[physical][key] != 0 {
				results = append(results, HookProbeResult{
					Hook: hook, State: stateDrift,
					Error: fmt.Sprintf("ownership=%d file=unreadable", ownership[physical][key]),
				})
			}
		} else if file.globalIssue != "" {
			results = append(results, HookProbeResult{Hook: hook, State: stateBroken, Error: file.globalIssue})
		} else if issue := file.eventIssues[hook.Event]; issue != "" {
			results = append(results, HookProbeResult{Hook: hook, State: stateBroken, Error: issue})
		} else if file.counts[key] == 0 {
			state, detail := missingOrStaleHook(file.allCounts, hook)
			results = append(results, HookProbeResult{Hook: hook, State: state, Error: detail})
		} else {
			results = append(results, HookProbeResult{Hook: hook, State: "ok"})
		}
		if file.err == nil && ownership[physical][key] != file.counts[key] {
			results = append(results, HookProbeResult{
				Hook: hook, State: stateDrift,
				Error: fmt.Sprintf("ownership=%d file=%d", ownership[physical][key], file.counts[key]),
			})
		}
	}
	// A retired command sitting in a file is invisible to the loop above —
	// it matches no expected hook — so walk every probed file's raw command
	// inventory once for any hook that matches the shared retired-command
	// table, regardless of whether the installer ever wrote or owned it. The
	// same walk also catches a hook of pfm's own shape naming a subcommand
	// this binary does not implement — what a rolled-back or newer-then-
	// reverted update leaves behind (issue #24 finding 2) — since that shape
	// matches no expected hook either.
	pfmBinary := filepath.Join(home, ".local", "bin", "pfm")
	for physical, file := range files {
		if file.err != nil {
			continue
		}
		for key, count := range file.allCounts {
			if count == 0 {
				continue
			}
			if name, retired := retiredHookCommandName(key.Command); retired {
				results = append(results, HookProbeResult{
					Hook: ExpectedHook{
						Target: fileTargets[physical], File: fileDisplayPaths[physical],
						Event: key.Event, Matcher: key.Matcher, Command: key.Command, Name: name,
					},
					State: stateStale, Error: "retired hook command is still present",
				})
				continue
			}
			if name, unknown := unknownPFMHookCommand(key.Command, pfmBinary); unknown {
				results = append(results, HookProbeResult{
					Hook: ExpectedHook{
						Target: fileTargets[physical], File: fileDisplayPaths[physical],
						Event: key.Event, Matcher: key.Matcher, Command: key.Command, Name: "unknown:" + name,
					},
					State: stateStale,
					Error: "hook names a pfm subcommand this pfm does not implement (left by a newer or rolled-back pfm) — run pfm install --yes",
				})
			}
		}
	}
	for path, counts := range ownership {
		for key, count := range counts {
			identity := path + "\x00" + key.Event + "\x00" + key.Matcher + "\x00" + key.Command
			if expectedKeys[identity] {
				continue
			}
			results = append(results, HookProbeResult{
				Hook: ExpectedHook{
					Target:  "ownership",
					File:    path,
					Event:   key.Event,
					Matcher: key.Matcher,
					Command: key.Command,
					Name:    "unexpected",
				},
				State: stateDrift,
				Error: fmt.Sprintf("ledger owns %d hook(s) absent from installer expectations", count),
			})
		}
	}
	sort.SliceStable(results, func(left, right int) bool {
		l, r := results[left], results[right]
		return l.Hook.Target+"\x00"+l.Hook.File+"\x00"+l.Hook.Event+"\x00"+l.Hook.Name+"\x00"+l.State <
			r.Hook.Target+"\x00"+r.Hook.File+"\x00"+r.Hook.Event+"\x00"+r.Hook.Name+"\x00"+r.State
	})
	return results
}

func missingOrStaleHook(counts settingsHookCounts, hook ExpectedHook) (string, string) {
	if counts[settingsHookKey{Event: hook.Event, Matcher: hook.Matcher, Command: hook.Command}] > 0 {
		return stateBroken, "hook type is not command"
	}
	_, arguments, foundArguments := strings.Cut(hook.Command, " ")
	if !foundArguments {
		return string(HostOverlayMissing), "expected command absent"
	}
	wantedSuffix := " " + arguments
	for key, count := range counts {
		if count == 0 || key.Event != hook.Event || key.Matcher != hook.Matcher {
			continue
		}
		fields := strings.Fields(key.Command)
		if len(fields) > 0 && strings.HasSuffix(key.Command, wantedSuffix) {
			return stateBroken, fmt.Sprintf("command points at %s", fields[0])
		}
	}
	return string(HostOverlayMissing), "expected command absent"
}

// inspectExpectedHookDocument validates the JSON shape doctor relies on. The
// installer's ownership counter intentionally remains tolerant because it must
// also remove historical entries; doctor is stricter and only counts typed
// command hooks as healthy.
func inspectExpectedHookDocument(
	document map[string]any,
) (settingsHookCounts, settingsHookCounts, string, map[string]string) {
	typed := settingsHookCounts{}
	all := settingsHookCounts{}
	issues := map[string]string{}
	hooksValue, present := document["hooks"]
	if !present {
		return typed, all, "", issues
	}
	events, ok := hooksValue.(map[string]any)
	if !ok {
		return typed, all, "hooks is not an object", issues
	}
	for event, eventValue := range events {
		entries, ok := eventValue.([]any)
		if !ok {
			issues[event] = fmt.Sprintf("event %s is not an array", event)
			continue
		}
		for entryIndex, entryValue := range entries {
			entry, ok := entryValue.(map[string]any)
			if !ok {
				issues[event] = fmt.Sprintf("event %s entry %d is not an object", event, entryIndex)
				continue
			}
			matcher := ""
			if matcherValue, exists := entry["matcher"]; exists {
				var matcherOK bool
				matcher, matcherOK = matcherValue.(string)
				if !matcherOK {
					issues[event] = fmt.Sprintf("event %s entry %d matcher is not a string", event, entryIndex)
					continue
				}
			}
			hookValues, ok := entry["hooks"].([]any)
			if !ok {
				issues[event] = fmt.Sprintf("event %s entry %d hooks is not an array", event, entryIndex)
				continue
			}
			for hookIndex, hookValue := range hookValues {
				hook, ok := hookValue.(map[string]any)
				if !ok {
					issues[event] = fmt.Sprintf(
						"event %s entry %d hook %d is not an object",
						event,
						entryIndex,
						hookIndex,
					)
					continue
				}
				command, ok := hook[configCommandKey].(string)
				if !ok || strings.TrimSpace(command) == "" {
					issues[event] = fmt.Sprintf(
						"event %s entry %d hook %d command is not a non-empty string",
						event,
						entryIndex,
						hookIndex,
					)
					continue
				}
				key := settingsHookKey{Event: event, Matcher: matcher, Command: command}
				all[key]++
				if hookType, ok := hook[configTypeKey].(string); ok && hookType == commandType {
					typed[key]++
				}
			}
		}
	}
	return typed, all, "", issues
}

func codexHookTemplate(home string) ExpectedHook {
	return ExpectedHook{
		Event:   "SessionStart",
		Matcher: codexappendix.Matcher,
		Command: codexappendix.Command(home),
		Name:    "codex-appendix",
	}
}

// HookProbeOverride is nil in production; a fleet test main may swap it for
// a deterministic stub exactly like ReportHooks' own probe (the same seam
// dependencyProbeOverride uses in cmd/pfm), so a jail can pin every hook "ok"
// without staging real settings.json content for it.
var HookProbeOverride func(home string, machine pfmconfig.Config) []HookProbeResult

// ReportHooks prints one doctor line per expected hook, and returns the
// (warnings, failures) it earned. A hook missing/broken/stale is a state
// `pfm install --yes` owns and did not produce, so it is a FAILURE; a drift
// row (the ownership ledger naming a hook absent from expectations) is
// advisory and stays a warning. When claudeAbsent, every claude[N] target
// collapses to ONE named skip line per account instead of nine per-hook
// MISSING rows, and earns neither — the installer never wires Claude hooks
// on a host with no Claude Code binary, so doctor must not fault it for
// that. Codex targets are reported exactly as before regardless of Claude's
// presence.
func ReportHooks(stdout io.Writer, home string, machine pfmconfig.Config, claudeAbsent bool) (warnings, failures int) {
	var results []HookProbeResult
	if HookProbeOverride != nil {
		results = HookProbeOverride(home, machine)
	} else {
		results = ProbeExpectedHooks(home, machine)
	}
	skipped := map[string]bool{}
	for index := range results {
		result := &results[index]
		hook := result.Hook
		if claudeAbsent && strings.HasPrefix(hook.Target, "claude[") {
			if !skipped[hook.Target] {
				skipped[hook.Target] = true
				fmt.Fprintf(stdout, "doctor: hook %s skipped (no Claude Code binary installed)\n", hook.Target)
			}
			continue
		}
		file := filepath.Base(hook.File)
		if file == "." || file == "" {
			file = "(unknown)"
		}
		prefix := fmt.Sprintf("doctor: hook %s %s %s %s", hook.Target, file, hook.Event, hook.Name)
		switch result.State {
		case "ok":
			fmt.Fprintln(stdout, prefix+" ok")
		case string(HostOverlayMissing):
			failures++
			fmt.Fprintln(stdout, prefix+" MISSING — run pfm install")
		case stateBroken:
			failures++
			fmt.Fprintf(stdout, "%s broken error=%s\n", prefix, result.Error)
		case stateDrift:
			warnings++
			fmt.Fprintf(stdout, "%s drift error=%s\n", prefix, result.Error)
		case stateStale:
			failures++
			fmt.Fprintln(stdout, prefix+" stale — run pfm install")
		default:
			failures++
			fmt.Fprintf(stdout, "%s broken error=unknown hook state %q\n", prefix, result.State)
		}
	}
	return warnings, failures
}
