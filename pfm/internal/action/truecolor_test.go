package action

import (
	"testing"
)

// Claude Code drops to 256 colours whenever TMUX is set unless
// CLAUDE_CODE_TMUX_TRUECOLOR is present, and every pfm chat lives in a tmux
// pane. These pin the variable on both renderers of the spawn door, for every
// purpose.
const truecolorName = "CLAUDE_CODE_TMUX_TRUECOLOR"

func TestClaudeSpawnCarriesTmuxTruecolor(t *testing.T) {
	home := t.TempDir()
	machine := configuredMachinePolicy(home)
	machine.Claude.TmuxTruecolor = true
	for _, purpose := range []Purpose{PurposeInteractive, PurposeResume, PurposeLauncher, PurposeQuery} {
		spawn := ClaudeSpawn{Purpose: purpose, Account: 42, Home: home, Machine: machine}
		shell, err := spawn.ShellCommand()
		if err != nil {
			t.Fatalf("%v shell spawn: %v", purpose, err)
		}
		if got := parsedShell(t, shell).SettingsEnv[truecolorName]; got != "1" {
			t.Fatalf("%v settings truecolor = %q", purpose, got)
		}
		environment, err := spawn.Environment([]string{"PATH=/usr/bin"})
		if err != nil {
			t.Fatal(err)
		}
		if got := lastEnvironmentValue(environment, truecolorName); got != "" {
			t.Fatalf("%v process environment truecolor = %q", purpose, got)
		}
	}
}
