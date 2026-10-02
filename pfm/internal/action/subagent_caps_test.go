package action

import (
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

// Claude Code stops a sub-agent from spawning sub-agents past 3 levels and
// runs at most 20 at once, and reports neither refusal. These pin the lift on
// both renderers of the spawn door, for every purpose: the depth always, the
// concurrency cap only when the config named one.
const (
	spawnDepthName  = "CLAUDE_CODE_MAX_SUBAGENT_SPAWN_DEPTH"
	concurrencyName = "CLAUDE_CODE_MAX_CONCURRENT_SUBAGENTS"
)

func subagentPurposes() []Purpose {
	return []Purpose{PurposeInteractive, PurposeResume, PurposeLauncher, PurposeQuery}
}

func TestClaudeSpawnCarriesDefaultSubagentSpawnDepth(t *testing.T) {
	home := t.TempDir()
	machine := configuredMachinePolicy(home)
	for _, purpose := range subagentPurposes() {
		spawn := ClaudeSpawn{Purpose: purpose, Account: 42, Home: home, Machine: machine}
		shell, err := spawn.ShellCommand()
		if err != nil {
			t.Fatalf("%v shell spawn: %v", purpose, err)
		}
		settings := parsedShell(t, shell).SettingsEnv
		if settings[spawnDepthName] != "8" {
			t.Fatalf("%v settings spawn depth = %q", purpose, settings[spawnDepthName])
		}
		if settings[concurrencyName] != "" {
			t.Fatalf("%v settings concurrency = %q", purpose, settings[concurrencyName])
		}
	}
}

func TestClaudeSpawnCarriesConfiguredSubagentCaps(t *testing.T) {
	home := t.TempDir()
	machine := configuredMachinePolicy(home)
	machine.Claude.MaxSubagentSpawnDepth, machine.Claude.MaxConcurrentSubagents = 5, 32
	for _, purpose := range subagentPurposes() {
		spawn := ClaudeSpawn{Purpose: purpose, Account: 42, Home: home, Machine: machine}
		shell, err := spawn.ShellCommand()
		if err != nil {
			t.Fatalf("%v shell spawn: %v", purpose, err)
		}
		for name, want := range map[string]string{spawnDepthName: "5", concurrencyName: "32"} {
			if got := parsedShell(t, shell).SettingsEnv[name]; got != want {
				t.Fatalf("%v settings %s = %q, want %q", purpose, name, got, want)
			}
		}
	}
}

// The managed launcher hands the door a one-account roster it assembled
// itself, so the caps have to survive that shape too.
func TestLauncherRunCarriesSubagentCaps(t *testing.T) {
	run, err := LauncherRun(
		"/opt/claude/claude",
		[]string{"--resume", "abc"},
		t.TempDir(),
		t.TempDir(),
		pfmconfig.Config{}, pfmconfig.ClaudePrefs{MaxSubagentSpawnDepth: 12, MaxConcurrentSubagents: 40},
	)
	if err != nil {
		t.Fatalf("LauncherRun: %v", err)
	}
	for name, want := range map[string]string{spawnDepthName: "12", concurrencyName: "40"} {
		if got := parsedShell(t, run).SettingsEnv[name]; got != want {
			t.Fatalf("launcher settings %s = %q, want %q", name, got, want)
		}
	}
}
