package action

import (
	"strings"
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
	for _, purpose := range []Purpose{PurposeInteractive, PurposeResume, PurposeProbe, PurposeQuery} {
		spawn := ClaudeSpawn{Purpose: purpose, Account: 42, Home: home, Machine: machine}
		shell, err := spawn.ShellCommand()
		if err != nil {
			t.Fatalf("%s shell spawn: %v", purpose, err)
		}
		if want := " " + truecolorName + "=" + Quote("1") + " "; !strings.Contains(shell, want) {
			t.Fatalf("%s shell spawn %q lacks %q", purpose, shell, want)
		}
		environment := spawn.Environment([]string{"PATH=/usr/bin"})
		if got := lastEnvironmentValue(environment, truecolorName); got != "1" {
			t.Fatalf("%s environment resolves %s=%q, want 1", purpose, truecolorName, got)
		}
	}
}
