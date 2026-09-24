package installer

import (
	"fmt"
	"path/filepath"
	"strconv"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

// Claude Code has one auto-compact window for a chat and every sub-agent it
// runs. pfm install sets that window to the lower of the two configured
// thresholds (settings env CLAUDE_CODE_AUTO_COMPACT_WINDOW) and wires a
// PreCompact hook, `pfm internal compact-gate`, that blocks a compaction the
// compacting party's own threshold has not reached. Both exist only while
// both thresholds are set; either unset removes exactly what pfm wrote.
const (
	autoCompactWindowEnv = "CLAUDE_CODE_AUTO_COMPACT_WINDOW"
	settingsEnvKey       = "env"
	hookEventPreCompact  = "PreCompact"
	// compactEnvOwnershipEvent marks an ownership-ledger record that owns a
	// settings env value rather than a hook: Matcher names the variable and
	// Command the exact value pfm wrote. No Claude hook event carries this
	// name, so hook removal never matches it.
	compactEnvOwnershipEvent = "settings.env"
)

// compactGateHook is the PreCompact template, wired only while both
// thresholds are set (ExpectedHooks and updateSettingsWindow add it then).
func compactGateHook(home string) ExpectedHook {
	return ExpectedHook{
		Event:   hookEventPreCompact,
		Command: filepath.Join(home, ".local", "bin", "pfm") + " internal compact-gate",
		Name:    "compact-gate",
	}
}

// compactWindowFor is the window the harness compacts at: the lower of the
// two thresholds, or 0 when either is unset.
func compactWindowFor(prefs pfmconfig.ClaudePrefs) int {
	mainTokens, subTokens, ok := prefs.CompactThresholds()
	if !ok {
		return 0
	}
	return min(mainTokens, subTokens)
}

// loadCompactWindow reads the thresholds from the machine config the command
// caller resolved. No config path (a direct caller) means unset; uninstall
// needs none, since it removes what the ledger owns regardless.
func loadCompactWindow(options Options) (int, error) {
	if options.MCPConfigPath == "" || options.Mode == ModeUninstall {
		return 0, nil
	}
	loaded, err := pfmconfig.Load(options.MCPConfigPath, options.Home, nil)
	if err != nil {
		return 0, fmt.Errorf("load auto-compact thresholds from %s: %w", options.MCPConfigPath, err)
	}
	return compactWindowFor(loaded.Claude), nil
}

func compactEnvOwnershipKey(value string) settingsHookKey {
	return settingsHookKey{Event: compactEnvOwnershipEvent, Matcher: autoCompactWindowEnv, Command: value}
}

// ownedCompactEnvValue is the window value the ledger says pfm wrote, or "".
func ownedCompactEnvValue(owned settingsHookCounts) string {
	for key, count := range owned {
		if count > 0 && key.Event == compactEnvOwnershipEvent && key.Matcher == autoCompactWindowEnv {
			return key.Command
		}
	}
	return ""
}

// currentCompactEnvValue is the settings env value as a string; present is
// false when the variable is absent. A non-string value is never pfm's.
func currentCompactEnvValue(document map[string]any) (value string, present bool) {
	env, _ := document[settingsEnvKey].(map[string]any)
	raw, present := env[autoCompactWindowEnv]
	value, _ = raw.(string)
	return value, present
}

// removeOwnedCompactEnv deletes the window variable only when it still holds
// the value pfm wrote; an operator's own value stays. An env object the
// removal empties goes with it.
func removeOwnedCompactEnv(document map[string]any, owned settingsHookCounts) bool {
	ownedValue := ownedCompactEnvValue(owned)
	current, present := currentCompactEnvValue(document)
	if ownedValue == "" || !present || current != ownedValue {
		return false
	}
	env, _ := document[settingsEnvKey].(map[string]any)
	delete(env, autoCompactWindowEnv)
	if len(env) == 0 {
		delete(document, settingsEnvKey)
	}
	return true
}

// convergeCompactEnv sets the window while the thresholds are set and removes
// pfm's own value when they are not, recording pfm's ownership in next. It
// writes only when the variable is absent or holds the value pfm wrote.
func convergeCompactEnv(document map[string]any, window int, owned, next settingsHookCounts) bool {
	if window <= 0 {
		return removeOwnedCompactEnv(document, owned)
	}
	want := strconv.Itoa(window)
	current, present := currentCompactEnvValue(document)
	if present && (current == "" || current != ownedCompactEnvValue(owned)) {
		return false
	}
	next[compactEnvOwnershipKey(want)] = 1
	if present && current == want {
		return false
	}
	env, _ := document[settingsEnvKey].(map[string]any)
	if env == nil {
		env = map[string]any{}
		document[settingsEnvKey] = env
	}
	env[autoCompactWindowEnv] = want
	return true
}

// removeOwnedCompactGate removes the PreCompact hooks the ledger owns once the
// thresholds are unset; an operator's own PreCompact hooks stay.
func removeOwnedCompactGate(document map[string]any, home string, owned settingsHookCounts) bool {
	gate := compactGateHook(home)
	key := settingsHookKey{Event: gate.Event, Matcher: gate.Matcher, Command: gate.Command}
	if owned[key] == 0 {
		return false
	}
	return removeOwnedSettingsHooks(document, settingsHookCounts{key: owned[key]})
}
