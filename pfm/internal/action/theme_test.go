package action

import (
	"context"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// TestClaudeSpawnCarriesThemedSettingsWhenConfigured pins both renderers
// (ShellCommand, Command/argv) to the merged --settings payload once
// prefs.Theme resolves non-empty for the spawning account — the
// EffectiveClaude(Account) binary pattern, not a top-level-only read.
func TestClaudeSpawnCarriesThemedSettingsWhenConfigured(t *testing.T) {
	home := t.TempDir()
	machine := configuredMachinePolicy(home)
	machine.Accounts[0].Claude = &pfmconfig.ClaudePrefs{Theme: "custom:professor-silver"}
	want := "'--settings' " + Quote(pfmengine.ClaudeSettingsPayload("custom:professor-silver"))

	spawn := ClaudeSpawn{Purpose: PurposeInteractive, Account: 42, Home: home, Machine: machine}
	shell, err := spawn.ShellCommand()
	if err != nil {
		t.Fatalf("shell spawn: %v", err)
	}
	if !strings.Contains(shell, want) {
		t.Fatalf("shell spawn %q lacks the themed settings payload %q", shell, want)
	}

	command, err := spawn.Command(context.Background())
	if err != nil {
		t.Fatalf("command spawn: %v", err)
	}
	if !containsFlagPair(command.Args, "--settings", pfmengine.ClaudeSettingsPayload("custom:professor-silver")) {
		t.Fatalf("command argv %#v lacks the themed settings payload", command.Args)
	}
}

// TestClaudeSpawnCarriesThePlainConstWithoutATheme is the companion negative:
// an account with no configured theme must still carry the byte-identical
// OutputStyleDefaultSettings the golden command lines pin.
func TestClaudeSpawnCarriesThePlainConstWithoutATheme(t *testing.T) {
	home := t.TempDir()
	machine := configuredMachinePolicy(home)
	want := "'--settings' " + Quote(pfmengine.OutputStyleDefaultSettings)

	spawn := ClaudeSpawn{Purpose: PurposeInteractive, Account: 42, Home: home, Machine: machine}
	shell, err := spawn.ShellCommand()
	if err != nil {
		t.Fatalf("shell spawn: %v", err)
	}
	if !strings.Contains(shell, want) {
		t.Fatalf("shell spawn %q lacks the plain settings const %q", shell, want)
	}

	command, err := spawn.Command(context.Background())
	if err != nil {
		t.Fatalf("command spawn: %v", err)
	}
	if !containsFlagPair(command.Args, "--settings", pfmengine.OutputStyleDefaultSettings) {
		t.Fatalf("command argv %#v lacks the plain settings const", command.Args)
	}
}
