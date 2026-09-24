package installer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

// compactPass runs one settings pass and decodes its output.
func compactPass(
	t *testing.T,
	raw, home string,
	uninstall bool,
	owned settingsHookCounts,
	window int,
) (string, bool, settingsHookCounts, map[string]any) {
	t.Helper()
	compact := compactSettings{window: window, gate: window > 0} // the per-party pair's shape
	updated, changed, next, err := updateSettingsWindow([]byte(raw), home, uninstall, owned, compact)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(updated, &document); err != nil {
		t.Fatal(err)
	}
	return string(updated), changed, next, document
}

func compactEnvValue(document map[string]any) (any, bool) {
	env, _ := document["env"].(map[string]any)
	value, present := env[autoCompactWindowEnv]
	return value, present
}

// preCompactCommands lists every PreCompact hook command in order.
func preCompactCommands(document map[string]any) []string {
	var commands []string
	for _, entry := range hookEntries(document, hookEventPreCompact, false) {
		hooks, _ := entry["hooks"].([]any)
		for _, value := range hooks {
			hook, _ := value.(map[string]any)
			command, _ := hook[configCommandKey].(string)
			commands = append(commands, command)
		}
	}
	return commands
}

func countCommand(commands []string, wanted string) int {
	count := 0
	for _, command := range commands {
		if command == wanted {
			count++
		}
	}
	return count
}

// TestCompactThresholdsWireEnvAndGateOnlyWhileSet pins the auto-compact wiring
// through a host's whole life: set writes the window and the gate, a re-run is
// idempotent, a threshold change updates the window, unset and uninstall
// remove exactly what pfm wrote.
func TestCompactThresholdsWireEnvAndGateOnlyWhileSet(t *testing.T) {
	home := filepath.Join("neutral", "home")
	gate := compactGateHook(home).Command

	first, changed, owned, document := compactPass(t, `{}`, home, false, nil, 100000)
	if !changed {
		t.Fatal("first pass with thresholds set reported no change")
	}
	if value, _ := compactEnvValue(document); value != "100000" {
		t.Fatalf("env.%s=%#v, want the string \"100000\":\n%s", autoCompactWindowEnv, value, first)
	}
	if got := countCommand(preCompactCommands(document), gate); got != 1 {
		t.Fatalf("PreCompact gate hooks=%d, want 1:\n%s", got, first)
	}

	again, changed, owned, _ := compactPass(t, first, home, false, owned, 100000)
	if changed || again != first {
		t.Fatalf("re-run changed=%t, want an idempotent no-op:\n%s", changed, again)
	}

	moved, changed, owned, document := compactPass(t, again, home, false, owned, 120000)
	if !changed {
		t.Fatal("a threshold change reported no change")
	}
	if value, _ := compactEnvValue(document); value != "120000" {
		t.Fatalf("env.%s=%#v after the change, want \"120000\":\n%s", autoCompactWindowEnv, value, moved)
	}

	unset, changed, _, document := compactPass(t, moved, home, false, owned, 0)
	if !changed {
		t.Fatal("unsetting the thresholds reported no change")
	}
	if _, present := compactEnvValue(document); present {
		t.Fatalf("pfm's env value survived unset:\n%s", unset)
	}
	if _, present := document["env"]; present {
		t.Fatalf("the env object pfm created survived its last key:\n%s", unset)
	}
	if got := countCommand(preCompactCommands(document), gate); got != 0 {
		t.Fatalf("pfm's PreCompact gate survived unset (%d):\n%s", got, unset)
	}

	removed, _, next, document := compactPass(t, moved, home, true, owned, 0)
	if _, present := compactEnvValue(document); present {
		t.Fatalf("pfm's env value survived uninstall:\n%s", removed)
	}
	if got := countCommand(preCompactCommands(document), gate); got != 0 {
		t.Fatalf("pfm's PreCompact gate survived uninstall (%d):\n%s", got, removed)
	}
	if len(next) != 0 {
		t.Fatalf("uninstall left ownership %#v", next)
	}
}

// TestCompactThresholdsLeaveOperatorEnvAndHooks pins the operator's side: their
// own window value and their own PreCompact hooks survive set, unset and
// uninstall alike.
func TestCompactThresholdsLeaveOperatorEnvAndHooks(t *testing.T) {
	home := filepath.Join("neutral", "home")
	gate := compactGateHook(home).Command
	operator := `{"env":{"` + autoCompactWindowEnv + `":"90000","OTHER":"1"},` +
		`"hooks":{"PreCompact":[{"matcher":"","hooks":[{"type":"command","command":"my-precompact"}]}]}}`

	set, _, owned, document := compactPass(t, operator, home, false, nil, 100000)
	if value, _ := compactEnvValue(document); value != "90000" {
		t.Fatalf("set clobbered the operator's window: %#v:\n%s", value, set)
	}
	if ownedCompactEnvValue(owned) != "" {
		t.Fatalf("set claimed the operator's window: %#v", owned)
	}
	commands := preCompactCommands(document)
	if countCommand(commands, "my-precompact") != 1 || countCommand(commands, gate) != 1 {
		t.Fatalf("PreCompact=%q, want the operator's hook kept beside pfm's gate:\n%s", commands, set)
	}

	for _, pass := range []struct {
		name      string
		uninstall bool
	}{{"unset", false}, {"uninstall", true}} {
		t.Run(pass.name, func(t *testing.T) {
			out, _, _, document := compactPass(t, set, home, pass.uninstall, owned, 0)
			if value, _ := compactEnvValue(document); value != "90000" {
				t.Fatalf("%s removed the operator's window: %#v:\n%s", pass.name, value, out)
			}
			commands := preCompactCommands(document)
			if countCommand(commands, "my-precompact") != 1 || countCommand(commands, gate) != 0 {
				t.Fatalf("PreCompact=%q after %s, want only the operator's hook:\n%s", commands, pass.name, out)
			}
		})
	}

	// A gate the operator wired by hand is theirs: unset keeps it.
	handWired := `{"hooks":{"PreCompact":[{"matcher":"","hooks":[{"type":"command","command":"` + gate + `"}]}]}}`
	kept, _, _, document := compactPass(t, handWired, home, false, nil, 0)
	if countCommand(preCompactCommands(document), gate) != 1 {
		t.Fatalf("unset removed a hand-wired, unowned gate:\n%s", kept)
	}
}

// TestCompactWindowComesFromTheConfigAndGatesExpectedHooks pins the config
// side: the window is the lower threshold, either unset means none, and doctor
// expects the gate exactly while both are set.
func TestCompactWindowComesFromTheConfigAndGatesExpectedHooks(t *testing.T) {
	home := t.TempDir()
	for _, testCase := range []struct {
		name, claude string
		want         int
	}{
		{"both set", `{"autoCompactMain":"150k","autoCompactSubagent":"100k"}`, 100000},
		{"main only", `{"autoCompactMain":"150k"}`, 0},
		{"neither", `{}`, 0},
		{"the plain window", `{"autoCompactWindow":"600k"}`, 600000},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			content := `{"version": 2, "claude": ` + testCase.claude + `}`
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := loadCompactSettings(Options{Home: home, MCPConfigPath: path})
			if err != nil {
				t.Fatal(err)
			}
			if got.window != testCase.want {
				t.Fatalf("window=%d, want %d", got.window, testCase.want)
			}
		})
	}
	machine := pfmconfig.Config{Accounts: []pfmconfig.Account{{ID: 1, ConfigDir: filepath.Join(home, ".claude")}}}
	gate := compactGateHook(home).Command
	hasGate := func(hooks []ExpectedHook) bool {
		for _, hook := range hooks {
			if hook.Command == gate && hook.Event == hookEventPreCompact {
				return true
			}
		}
		return false
	}
	if hasGate(ExpectedHooks(home, machine)) {
		t.Fatal("ExpectedHooks demands the compact gate with the thresholds unset")
	}
	machine.Claude.AutoCompactMain, machine.Claude.AutoCompactSubagent = 150000, 100000
	if !hasGate(ExpectedHooks(home, machine)) {
		t.Fatal("ExpectedHooks misses the compact gate with both thresholds set")
	}
}

// claude.autoCompactWindow alone writes the window and wires no gate: every
// party compacts at Claude Code's own point for that window.
func TestAutoCompactWindowWritesThePlainWindow(t *testing.T) {
	window := compactFor(pfmconfig.ClaudePrefs{AutoCompactWindow: 600000})
	if window != (compactSettings{window: 600000}) {
		t.Fatalf("compactFor(window 600000) = %+v, want the window without the gate", window)
	}
	pair := compactFor(pfmconfig.ClaudePrefs{AutoCompactMain: 600000, AutoCompactSubagent: 150000})
	if pair != (compactSettings{window: 150000, gate: true}) {
		t.Fatalf("compactFor(pair 600000/150000) = %+v, want window 150000 with the gate", pair)
	}
	updated, _, _, err := updateSettingsWindow([]byte(`{}`), t.TempDir(), false, nil, window)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(updated), `"CLAUDE_CODE_AUTO_COMPACT_WINDOW": "600000"`) {
		t.Fatalf("settings = %s, want env CLAUDE_CODE_AUTO_COMPACT_WINDOW 600000", updated)
	}
}
