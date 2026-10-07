package action

import (
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

// TestClaudeSpawnCarriesThemedSettingsWhenConfigured pins both renderers
// (ShellCommand, Command/argv) to the merged --settings payload once
// prefs.Theme resolves non-empty for the spawning account — the
// EffectiveClaude(Account) binary pattern, not a top-level-only read.
func TestClaudeSpawnCarriesThemedSettingsWhenConfigured(t *testing.T) {
	home := t.TempDir()
	machine := configuredMachinePolicy(home)
	machine.Accounts[0].Claude = &pfmconfig.ClaudePrefs{Theme: "custom:professor-silver"}
	spawn := ClaudeSpawn{Purpose: PurposeInteractive, Account: 42, Home: home, Machine: machine}
	shell, err := spawn.ShellCommand()
	if err != nil {
		t.Fatalf("shell spawn: %v", err)
	}
	if got := parsedShell(t, shell).Settings["theme"]; got != "custom:professor-silver" {
		t.Fatalf("shell theme = %#v", got)
	}
	if got := parsedSpawn(t, spawn).Settings["theme"]; got != "custom:professor-silver" {
		t.Fatalf("direct theme = %#v", got)
	}
}

// An account with no configured theme still carries the default output style.
func TestClaudeSpawnCarriesDefaultOutputStyleWithoutTheme(t *testing.T) {
	home := t.TempDir()
	machine := configuredMachinePolicy(home)
	spawn := ClaudeSpawn{Purpose: PurposeInteractive, Account: 42, Home: home, Machine: machine}
	shell, err := spawn.ShellCommand()
	if err != nil {
		t.Fatalf("shell spawn: %v", err)
	}
	if got := parsedShell(t, shell).Settings["outputStyle"]; got != "default" {
		t.Fatalf("shell output style = %#v", got)
	}
	if got := parsedSpawn(t, spawn).Settings["outputStyle"]; got != "default" {
		t.Fatalf("direct output style = %#v", got)
	}
}
