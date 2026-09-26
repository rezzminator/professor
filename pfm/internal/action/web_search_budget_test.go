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
	for _, purpose := range []Purpose{PurposeInteractive, PurposeResume, PurposeProbe, PurposeQuery} {
		spawn := ClaudeSpawn{Purpose: purpose, Account: 42, Home: home, Machine: machine}
		shell, err := spawn.ShellCommand()
		if err != nil {
			t.Fatalf("%s shell spawn: %v", purpose, err)
		}
		if want := " " + maxWebSearchesName + "=" + Quote(maxWebSearchesValue) + " "; !strings.Contains(shell, want) {
			t.Fatalf("%s shell spawn %q lacks %q", purpose, shell, want)
		}
		// An inherited lower cap must not win: exec keeps the LAST duplicate,
		// so the door's assignment has to land after the inherited entry.
		environment := spawn.Environment([]string{maxWebSearchesName + "=5", "PATH=/usr/bin"})
		if got := lastEnvironmentValue(environment, maxWebSearchesName); got != maxWebSearchesValue {
			t.Fatalf("%s spawn environment %q resolves %s=%q, want %q",
				purpose, environment, maxWebSearchesName, got, maxWebSearchesValue)
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
