package action

import (
	"strings"
	"testing"
)

// Claude Code caps WebSearch at 200 calls per session unless
// CLAUDE_CODE_MAX_WEB_SEARCHES_PER_SESSION raises it, and a research chat that
// exhausts the cap stops searching mid-task. These pin the lift on both
// renderers of the spawn door, for every purpose.
const (
	maxWebSearchesName  = "CLAUDE_CODE_MAX_WEB_SEARCHES_PER_SESSION"
	maxWebSearchesValue = "9007199254740991"
)

func TestClaudeSpawnCarriesWebSearchBudget(t *testing.T) {
	home := t.TempDir()
	machine := configuredMachinePolicy(home)
	machine.Claude.WebSearchesPerSession = 9007199254740991
	for _, purpose := range []Purpose{PurposeInteractive, PurposeResume, PurposeLauncher, PurposeQuery} {
		spawn := ClaudeSpawn{Purpose: purpose, Account: 42, Home: home, Machine: machine}
		shell, err := spawn.ShellCommand()
		if err != nil {
			t.Fatalf("%v shell spawn: %v", purpose, err)
		}
		if got := parsedShell(t, shell).SettingsEnv[maxWebSearchesName]; got != maxWebSearchesValue {
			t.Fatalf("%v settings budget = %q", purpose, got)
		}
	}
}

func lastEnvironmentValue(environment []string, name string) string {
	value := ""
	for _, entry := range environment {
		if key, rest, found := strings.Cut(entry, "="); found && key == name {
			value = rest
		}
	}
	return value
}
