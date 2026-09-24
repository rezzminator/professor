package installer

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// The states of one doctor hook row (docs/design/hooks/hooks.md § The pfm
// doctor check). MISSING reuses HostOverlayMissing's "missing".
const (
	stateOK = "ok"
	// stateHookDrift is a pfm hook present in a shape pfm install converges
	// away: a wrong event or matcher, another binary path, an executable
	// that does not resolve, a duplicate, a missing async flag. A failure.
	stateHookDrift = "hook-drift"
	// stateDrift is the ownership ledger disagreeing with the file. A warning.
	stateDrift = "drift"
	// stateStale is a retired or unknown pfm hook still registered.
	stateStale = "stale"
	// stateUnreadable is a file that exists but could not be read, parsed or
	// shape-checked: an error, never absence, so it carries no MISSING rows.
	stateUnreadable = "unreadable"
	// stateNoClaudeConfig is a machine config naming no Claude config dir:
	// nothing to probe, said out loud rather than passed silently.
	stateNoClaudeConfig = "no-claude-config"
)

// HookProbeResult is one doctor hook row. What, Want and Got name the
// difference a hook-drift row found, and Want/Got carry the ledger and file
// counts of a ledger drift row.
type HookProbeResult struct {
	Hook  ExpectedHook
	State string
	Error string
	What  string
	Want  string
	Got   string
}

// hookDocument is the typed view of one settings.json the probe judges.
type hookDocument struct {
	typed settingsHookCounts // hooks of type "command"
	all   settingsHookCounts // every hook with a command, any type
	async settingsHookCounts // typed hooks carrying "async": true
}

// ProbeExpectedHooks proves each expected pfm hook in every configured Claude
// settings file, reports any retired or unknown pfm hook in those files and
// in every configured Codex home's hooks.json, and cross-checks the same
// ownership ledger uninstall reads.
func ProbeExpectedHooks(home string, config pfmconfig.Config) []HookProbeResult {
	var results []HookProbeResult
	if len(config.Accounts) == 0 {
		results = append(results, HookProbeResult{
			Hook:  ExpectedHook{Target: pfmengine.MustLookup(pfmengine.Claude).LongName},
			State: stateNoClaudeConfig,
		})
	}
	ownershipPath := settingsHookOwnershipPath(managedRootForHome(home))
	ownership, _, ownershipErr := readSettingsHookOwnership(ownershipPath)
	ledgerReadable := ownershipErr == nil
	if !ledgerReadable {
		results = append(results, HookProbeResult{
			Hook:  ExpectedHook{Target: "ownership", File: ownershipPath},
			State: stateUnreadable, Error: ownershipErr.Error(),
		})
	}
	pfmBinary := filepath.Join(home, ".local", "bin", "pfm")
	var order []string
	byFile := map[string][]ExpectedHook{}
	expectedKeys := map[string]bool{}
	for _, hook := range ExpectedHooks(home, config) {
		physical := physicalSettingsPath(hook.File)
		if _, seen := byFile[physical]; !seen {
			order = append(order, physical)
		}
		byFile[physical] = append(byFile[physical], hook)
		expectedKeys[physical+"\x00"+hook.Event+"\x00"+hook.Matcher+"\x00"+hook.Command] = true
	}
	executables := map[string]string{}
	for _, physical := range order {
		var fileOwnership settingsHookCounts
		if ledgerReadable {
			fileOwnership = ownership[physical]
		}
		results = append(results,
			probeClaudeSettings(byFile[physical], fileOwnership, ledgerReadable, pfmBinary, executables)...)
	}
	results = append(results, probeCodexHooks(home, config, pfmBinary)...)
	for path, counts := range ownership {
		for key, count := range counts {
			if key.Event == compactEnvOwnershipEvent {
				continue // an owned settings env value, not a hook
			}
			if expectedKeys[path+"\x00"+key.Event+"\x00"+key.Matcher+"\x00"+key.Command] {
				continue
			}
			results = append(results, HookProbeResult{
				Hook: ExpectedHook{
					Target: "ownership", File: path,
					Event: key.Event, Matcher: key.Matcher, Command: key.Command, Name: "unexpected",
				},
				State: stateDrift, Want: strconv.Itoa(count), Got: "not-expected",
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

// readHookFile reads a settings.json or hooks.json. absent is true only when
// nothing is there; a dangling symlink is an error, never absence.
func readHookFile(path string) (raw []byte, absent bool, err error) {
	raw, err = os.ReadFile(path)
	if err == nil {
		return raw, false, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, false, fmt.Errorf("read %s: %w", path, err)
	}
	info, statErr := os.Lstat(path)
	if statErr == nil && info.Mode()&os.ModeSymlink != 0 {
		return nil, false, fmt.Errorf("%s is a dangling symlink", path)
	}
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return nil, false, fmt.Errorf("inspect %s: %w", path, statErr)
	}
	return nil, true, nil
}

// probeClaudeSettings judges one physical settings file against the expected
// hooks it must carry (all of hooks share that file).
func probeClaudeSettings(
	hooks []ExpectedHook,
	ownership settingsHookCounts,
	ledgerReadable bool,
	pfmBinary string,
	executables map[string]string,
) []HookProbeResult {
	first := hooks[0]
	unreadable := func(err error) []HookProbeResult {
		return []HookProbeResult{{
			Hook:  ExpectedHook{Target: first.Target, File: first.File},
			State: stateUnreadable, Error: err.Error(),
		}}
	}
	raw, absent, err := readHookFile(first.File)
	if err != nil {
		return unreadable(err)
	}
	var results []HookProbeResult
	if absent {
		for _, hook := range hooks {
			results = append(
				results,
				HookProbeResult{Hook: hook, State: string(HostOverlayMissing), Error: "settings file absent"},
			)
			key := settingsHookKey{Event: hook.Event, Matcher: hook.Matcher, Command: hook.Command}
			if ledgerReadable && ownership[key] != 0 {
				results = append(results, HookProbeResult{
					Hook: hook, State: stateDrift, Want: strconv.Itoa(ownership[key]), Got: "absent",
				})
			}
		}
		return results
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		return unreadable(fmt.Errorf("parse %s: %w", first.File, err))
	}
	parsed, issue := inspectHookDocument(document)
	if issue != "" {
		return unreadable(errors.New(issue))
	}

	pairs := map[string]map[settingsHookKey]bool{}
	firstRow := map[string]int{}
	for index, hook := range hooks {
		if pairs[hook.Command] == nil {
			pairs[hook.Command] = map[settingsHookKey]bool{}
			firstRow[hook.Command] = index
		}
		pairs[hook.Command][settingsHookKey{Event: hook.Event, Matcher: hook.Matcher}] = true
	}
	strays := func(command string) []settingsHookKey {
		var found []settingsHookKey
		for key, count := range parsed.all {
			if count > 0 && key.Command == command &&
				!pairs[command][settingsHookKey{Event: key.Event, Matcher: key.Matcher}] {
				found = append(found, key)
			}
		}
		sortHookKeys(found)
		return found
	}
	commandMissingSomewhere := func(command string) bool {
		for pair := range pairs[command] {
			if parsed.typed[settingsHookKey{Event: pair.Event, Matcher: pair.Matcher, Command: command}] == 0 {
				return true
			}
		}
		return false
	}
	for index, hook := range hooks {
		key := settingsHookKey{Event: hook.Event, Matcher: hook.Matcher, Command: hook.Command}
		count := parsed.typed[key]
		stray := strays(hook.Command)
		switch {
		case count == 0:
			results = append(results, absentHookResult(hook, parsed, stray))
		case count > 1:
			results = append(results, hookDrift(hook, "count", "1", strconv.Itoa(count)))
		case len(stray) > 0 && index == firstRow[hook.Command] && !commandMissingSomewhere(hook.Command):
			results = append(results, placementDrift(hook, stray[0]))
		case hook.Async && parsed.async[key] < count:
			results = append(results, hookDrift(hook, "async", "true", "false"))
		default:
			results = append(results, executableResult(hook, executables))
		}
		if ledgerReadable && ownership[key] != count {
			results = append(results, HookProbeResult{
				Hook: hook, State: stateDrift, Want: strconv.Itoa(ownership[key]), Got: strconv.Itoa(count),
			})
		}
	}
	for _, key := range sortedHookKeys(parsed.all) {
		if stale, found := staleHookResult(first.Target, first.File, key, pfmBinary, "", false); found {
			results = append(results, stale)
		}
	}
	return results
}

// absentHookResult judges an expected hook with no typed copy at its own
// (event, matcher): moved, repointed, mistyped, or genuinely missing.
func absentHookResult(hook ExpectedHook, parsed hookDocument, stray []settingsHookKey) HookProbeResult {
	if parsed.all[settingsHookKey{Event: hook.Event, Matcher: hook.Matcher, Command: hook.Command}] > 0 {
		return hookDrift(hook, "type", commandType, "not-"+commandType)
	}
	if len(stray) > 0 {
		return placementDrift(hook, stray[0])
	}
	binary, arguments, hasArguments := strings.Cut(hook.Command, " ")
	if hasArguments {
		for _, key := range sortedHookKeys(parsed.all) {
			other, otherArguments, found := strings.Cut(key.Command, " ")
			if found && other != binary && otherArguments == arguments {
				return hookDrift(hook, "binary", binary, other)
			}
		}
	}
	return HookProbeResult{Hook: hook, State: string(HostOverlayMissing)}
}

func hookDrift(hook ExpectedHook, what, want, got string) HookProbeResult {
	return HookProbeResult{Hook: hook, State: stateHookDrift, What: what, Want: want, Got: got}
}

// placementDrift names the first way a copy found outside the command's
// expected (event, matcher) set differs from this row's pair.
func placementDrift(hook ExpectedHook, found settingsHookKey) HookProbeResult {
	if found.Event != hook.Event {
		return hookDrift(hook, "event", hook.Event, found.Event)
	}
	return hookDrift(hook, "matcher", displayMatcher(hook.Matcher), displayMatcher(found.Matcher))
}

func displayMatcher(matcher string) string {
	if matcher == "" {
		return `""`
	}
	return matcher
}

// executableResult proves the command's first word resolves to a regular file
// with an execute bit; the verdict per path is cached across rows.
func executableResult(hook ExpectedHook, cache map[string]string) HookProbeResult {
	fields := strings.Fields(hook.Command)
	if len(fields) == 0 {
		return hookDrift(hook, "executable", "executable", "empty-command")
	}
	path := fields[0]
	verdict, cached := cache[path]
	if !cached {
		verdict = executableVerdict(path)
		cache[path] = verdict
	}
	if verdict != "" {
		return hookDrift(hook, "executable", "executable", verdict)
	}
	return HookProbeResult{Hook: hook, State: stateOK}
}

// executableVerdict is "" for a usable executable, else what is wrong; a stat
// that failed for another reason says so rather than claiming absence.
func executableVerdict(path string) string {
	info, err := os.Stat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return "absent"
	case err != nil:
		return "stat-failed(" + err.Error() + ")"
	case !info.Mode().IsRegular():
		return "not-regular-file"
	case info.Mode().Perm()&0o111 == 0:
		return "not-executable"
	}
	return ""
}

// probeCodexHooks reads every configured Codex home's hooks.json for pfm
// residue. pfm expects no Codex hook, so an absent file is healthy and only
// STALE and UNREADABLE rows come from here.
func probeCodexHooks(home string, config pfmconfig.Config, pfmBinary string) []HookProbeResult {
	var results []HookProbeResult
	seen := map[string]bool{}
	for _, account := range config.CodexAccounts {
		path := filepath.Join(account.Home, "hooks.json")
		physical := physicalSettingsPath(path)
		if seen[physical] {
			continue
		}
		seen[physical] = true
		target := fmt.Sprintf("codex[%d]", account.ID)
		unreadable := func(err error) {
			results = append(results, HookProbeResult{
				Hook: ExpectedHook{Target: target, File: path}, State: stateUnreadable, Error: err.Error(),
			})
		}
		raw, absent, err := readHookFile(path)
		if err != nil {
			unreadable(err)
			continue
		}
		if absent {
			continue
		}
		var document map[string]any
		if err := json.Unmarshal(raw, &document); err != nil {
			unreadable(fmt.Errorf("parse %s: %w", path, err))
			continue
		}
		if err := validateCodexHooks(document); err != nil {
			unreadable(err)
			continue
		}
		for _, key := range sortedHookKeys(countSettingsHookCommands(document)) {
			if stale, found := staleHookResult(target, path, key, pfmBinary, home, true); found {
				results = append(results, stale)
			}
		}
	}
	return results
}

// staleHookResult reports a retired or unknown pfm hook: the shared retired
// table, Codex's own retired SessionStart shapes, and pfm's own shape naming
// a subcommand this binary does not implement (issue #24 finding 2).
func staleHookResult(
	target, file string,
	key settingsHookKey,
	pfmBinary, home string,
	codex bool,
) (HookProbeResult, bool) {
	stale := func(name, detail string) (HookProbeResult, bool) {
		return HookProbeResult{
			Hook: ExpectedHook{
				Target: target, File: file,
				Event: key.Event, Matcher: key.Matcher, Command: key.Command, Name: name,
			},
			State: stateStale, Error: detail,
		}, true
	}
	if name, retired := retiredHookCommandName(key.Command); retired {
		return stale(name, "retired hook command is still present")
	}
	if codex && key.Event == "SessionStart" {
		if name, retired := codexRetiredSessionStartHookName(key.Command, home); retired {
			return stale(name, "retired Codex hook command is still present")
		}
	}
	if name, unknown := unknownPFMHookCommand(key.Command, pfmBinary); unknown {
		return stale("unknown:"+name,
			"hook names a pfm subcommand this pfm does not implement (left by a newer or rolled-back pfm)")
	}
	return HookProbeResult{}, false
}

func sortedHookKeys(counts settingsHookCounts) []settingsHookKey {
	keys := make([]settingsHookKey, 0, len(counts))
	for key, count := range counts {
		if count > 0 {
			keys = append(keys, key)
		}
	}
	sortHookKeys(keys)
	return keys
}

func sortHookKeys(keys []settingsHookKey) {
	sort.Slice(keys, func(left, right int) bool {
		l, r := keys[left], keys[right]
		return l.Event+"\x00"+l.Matcher+"\x00"+l.Command < r.Event+"\x00"+r.Matcher+"\x00"+r.Command
	})
}

// inspectHookDocument validates the JSON shape doctor relies on and counts
// the hooks in it. It returns the first shape fault (events in sorted order)
// as the file's UNREADABLE cause. The installer's ownership counter stays
// tolerant because it must also remove historical entries; doctor is stricter
// and only counts typed command hooks as present.
func inspectHookDocument(document map[string]any) (hookDocument, string) {
	parsed := hookDocument{typed: settingsHookCounts{}, all: settingsHookCounts{}, async: settingsHookCounts{}}
	hooksValue, present := document["hooks"]
	if !present {
		return parsed, ""
	}
	events, ok := hooksValue.(map[string]any)
	if !ok {
		return parsed, "hooks is not an object"
	}
	names := make([]string, 0, len(events))
	for event := range events {
		names = append(names, event)
	}
	sort.Strings(names)
	for _, event := range names {
		if issue := inspectHookEvent(event, events[event], parsed); issue != "" {
			return parsed, issue
		}
	}
	return parsed, ""
}

func inspectHookEvent(event string, eventValue any, parsed hookDocument) string {
	entries, ok := eventValue.([]any)
	if !ok {
		return fmt.Sprintf("event %s is not an array", event)
	}
	for entryIndex, entryValue := range entries {
		entry, ok := entryValue.(map[string]any)
		if !ok {
			return fmt.Sprintf("event %s entry %d is not an object", event, entryIndex)
		}
		matcher := ""
		if matcherValue, exists := entry["matcher"]; exists {
			if matcher, ok = matcherValue.(string); !ok {
				return fmt.Sprintf("event %s entry %d matcher is not a string", event, entryIndex)
			}
		}
		hookValues, ok := entry["hooks"].([]any)
		if !ok {
			return fmt.Sprintf("event %s entry %d hooks is not an array", event, entryIndex)
		}
		for hookIndex, hookValue := range hookValues {
			hook, ok := hookValue.(map[string]any)
			if !ok {
				return fmt.Sprintf("event %s entry %d hook %d is not an object", event, entryIndex, hookIndex)
			}
			command, ok := hook[configCommandKey].(string)
			if !ok || strings.TrimSpace(command) == "" {
				return fmt.Sprintf(
					"event %s entry %d hook %d command is not a non-empty string", event, entryIndex, hookIndex)
			}
			key := settingsHookKey{Event: event, Matcher: matcher, Command: command}
			parsed.all[key]++
			if hookType, ok := hook[configTypeKey].(string); ok && hookType == commandType {
				parsed.typed[key]++
				if async, ok := hook["async"].(bool); ok && async {
					parsed.async[key]++
				}
			}
		}
	}
	return ""
}

// HookProbeOverride is nil in production; a fleet test main may swap it for
// a deterministic stub exactly like ReportHooks' own probe (the same seam
// dependencyProbeOverride uses in cmd/pfm), so a jail can pin every hook "ok"
// without staging real settings.json content for it.
var HookProbeOverride func(home string, machine pfmconfig.Config) []HookProbeResult

// ReportHooks prints one doctor line per probe row and returns the (warnings,
// failures) it earned. MISSING, hook DRIFT, STALE and UNREADABLE are states
// `pfm install --yes` owns and did not produce, so each is a FAILURE; a ledger
// DRIFT row and the no-Claude-config line are advisory warnings. An
// UNREADABLE file prints one line and no per-hook rows. Operator hooks print
// nothing. When claudeAbsent, every claude[N] target collapses to ONE named
// skip line per account instead of its rows (one per registration in
// claudeHookTemplates), and earns neither — the installer never wires Claude
// hooks on a host with no Claude Code binary, so doctor must not fault it for
// that. Codex homes print only STALE and UNREADABLE rows, whether or not
// Claude is present.
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
		case stateOK:
			fmt.Fprintln(stdout, prefix+" ok")
		case string(HostOverlayMissing):
			failures++
			detail := ""
			if result.Error != "" {
				detail = " (" + result.Error + ")"
			}
			fmt.Fprintln(stdout, prefix+" MISSING — run pfm install"+detail)
		case stateHookDrift:
			failures++
			fmt.Fprintf(
				stdout,
				"%s DRIFT %s want=%s got=%s — run pfm install\n",
				prefix,
				result.What,
				result.Want,
				result.Got,
			)
		case stateDrift:
			warnings++
			fmt.Fprintf(stdout, "%s DRIFT ledger ownership=%s file=%s\n", prefix, result.Want, result.Got)
		case stateStale:
			failures++
			fmt.Fprintf(stdout, "%s STALE %s — run pfm install\n", prefix, hook.Name)
		case stateUnreadable:
			failures++
			fmt.Fprintf(stdout, "doctor: hook %s %s UNREADABLE error=%s\n", hook.Target, file, result.Error)
		case stateNoClaudeConfig:
			if claudeAbsent {
				fmt.Fprintln(
					stdout,
					"doctor: hook claude skipped (no Claude config dir configured, no Claude Code binary installed)",
				)
				continue
			}
			warnings++
			fmt.Fprintln(stdout, "doctor: hook claude none — no Claude config dir is configured in the machine config")
		default:
			failures++
			fmt.Fprintf(stdout, "%s UNKNOWN-STATE state=%q\n", prefix, result.State)
		}
	}
	return warnings, failures
}
