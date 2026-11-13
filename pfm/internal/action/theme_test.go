package action

import (
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

// TestClaudeSpawnCarriesThemeSettings pins both renderers (ShellCommand,
// Command/argv) to the merged --settings payload: once prefs.Theme resolves
// non-empty for the spawning account (the EffectiveClaude(Account) binary
// pattern, not a top-level-only read) it carries the theme, and an account
// with no configured theme still carries the default output style.
func TestClaudeSpawnCarriesThemeSettings(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, theme, key, want string
	}{
		{name: "configured theme", theme: "custom:lagoon", key: "theme", want: "custom:lagoon"},
		{name: "no theme keeps the default output style", key: "outputStyle", want: "default"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			home := t.TempDir()
			machine := configuredMachinePolicy(home)
			if test.theme != "" {
				machine.Accounts[0].Claude = &pfmconfig.ClaudePrefs{Theme: test.theme}
			}
			spawn := ClaudeSpawn{Purpose: PurposeInteractive, Account: 42, Home: home, Machine: machine}
			shell, err := spawn.ShellCommand()
			if err != nil {
				t.Fatalf("shell spawn: %v", err)
			}
			if got := parsedShell(t, shell).Settings[test.key]; got != test.want {
				t.Fatalf("shell %s = %#v, want %q", test.key, got, test.want)
			}
			if got := parsedSpawn(t, spawn).Settings[test.key]; got != test.want {
				t.Fatalf("direct %s = %#v, want %q", test.key, got, test.want)
			}
		})
	}
}
