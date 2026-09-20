package action

import (
	"strings"
	"testing"

	pfmconfig "hostops/pfm/internal/config"
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
	return []Purpose{PurposeInteractive, PurposeResume, PurposeProbe, PurposeQuery}
}

func TestClaudeSpawnCarriesDefaultSubagentSpawnDepth(t *testing.T) {
	home := t.TempDir()
	machine := configuredMachinePolicy(home)
	for _, purpose := range subagentPurposes() {
		spawn := ClaudeSpawn{Purpose: purpose, Account: 42, Home: home, Machine: machine}
		shell, err := spawn.ShellCommand()
		if err != nil {
			t.Fatalf("%s shell spawn: %v", purpose, err)
		}
		if want := " " + spawnDepthName + "=" + Quote("8") + " "; !strings.Contains(shell, want) {
			t.Fatalf("%s shell spawn %q lacks %q", purpose, shell, want)
		}
		if strings.Contains(shell, concurrencyName) {
			t.Fatalf("%s shell spawn %q carries %s with no key in the config", purpose, shell, concurrencyName)
		}
		environment := spawn.Environment([]string{"PATH=/usr/bin"})
		if got := lastEnvironmentValue(environment, spawnDepthName); got != "8" {
			t.Fatalf("%s environment resolves %s=%q, want 8", purpose, spawnDepthName, got)
		}
		if got := lastEnvironmentValue(environment, concurrencyName); got != "" {
			t.Fatalf("%s environment resolves %s=%q, want it absent", purpose, concurrencyName, got)
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
			t.Fatalf("%s shell spawn: %v", purpose, err)
		}
		for name, want := range map[string]string{spawnDepthName: "5", concurrencyName: "32"} {
			if assignment := " " + name + "=" + Quote(want) + " "; !strings.Contains(shell, assignment) {
				t.Fatalf("%s shell spawn %q lacks %q", purpose, shell, assignment)
			}
			// exec keeps the LAST duplicate, so the door's assignment has to
			// land after an inherited lower cap.
			environment := spawn.Environment([]string{name + "=1", "PATH=/usr/bin"})
			if got := lastEnvironmentValue(environment, name); got != want {
				t.Fatalf("%s environment resolves %s=%q, want %q", purpose, name, got, want)
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
		"/cfg",
		t.TempDir(),
		pfmconfig.ClaudePrefs{MaxSubagentSpawnDepth: 12, MaxConcurrentSubagents: 40},
	)
	if err != nil {
		t.Fatalf("LauncherRun: %v", err)
	}
	for name, want := range map[string]string{spawnDepthName: "12", concurrencyName: "40"} {
		if assignment := " " + name + "=" + Quote(want) + " "; !strings.Contains(run, assignment) {
			t.Fatalf("launcher run %q lacks %q", run, assignment)
		}
	}
}
