package action

import (
	"fmt"
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

// TestClaudeSpawnCarriesSubagentCaps pins the caps through the spawn door for
// every purpose, unconfigured (the default depth, no concurrency cap) and
// configured, and through the managed launcher, which hands the door a
// one-account roster it assembled itself, so the caps survive that shape too.
func TestClaudeSpawnCarriesSubagentCaps(t *testing.T) {
	t.Parallel()
	// spawnLines renders the spawn door for every purpose; caps, when given,
	// configure the depth and the concurrency cap, else the policy is untouched.
	spawnLines := func(caps ...int) func(t *testing.T) map[string]string {
		return func(t *testing.T) map[string]string {
			home := t.TempDir()
			machine := configuredMachinePolicy(home)
			if len(caps) == 2 {
				machine.Claude.MaxSubagentSpawnDepth, machine.Claude.MaxConcurrentSubagents = caps[0], caps[1]
			}
			lines := map[string]string{}
			for _, purpose := range subagentPurposes() {
				spawn := ClaudeSpawn{Purpose: purpose, Account: 42, Home: home, Machine: machine}
				shell, err := spawn.ShellCommand()
				if err != nil {
					t.Fatalf("%v shell spawn: %v", purpose, err)
				}
				lines[fmt.Sprint(purpose)] = shell
			}
			return lines
		}
	}
	for _, test := range []struct {
		name  string
		lines func(t *testing.T) map[string]string
		want  map[string]string
	}{
		{
			name:  "spawn door default depth and no concurrency cap",
			lines: spawnLines(),
			want:  map[string]string{spawnDepthName: "8", concurrencyName: ""},
		},
		{
			name:  "spawn door configured caps",
			lines: spawnLines(5, 32),
			want:  map[string]string{spawnDepthName: "5", concurrencyName: "32"},
		},
		{
			name: "launcher one-account roster",
			lines: func(t *testing.T) map[string]string {
				run, err := LauncherRun(
					"/opt/claude/claude",
					[]string{"--resume", "abc"},
					t.TempDir(),
					t.TempDir(),
					pfmconfig.Config{}, pfmconfig.ClaudePrefs{MaxSubagentSpawnDepth: 12, MaxConcurrentSubagents: 40}, "",
				)
				if err != nil {
					t.Fatalf("LauncherRun: %v", err)
				}
				return map[string]string{"launcher": run}
			},
			want: map[string]string{spawnDepthName: "12", concurrencyName: "40"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			for purpose, shell := range test.lines(t) {
				settings := parsedShell(t, shell).SettingsEnv
				for name, want := range test.want {
					if got := settings[name]; got != want {
						t.Fatalf("%s settings %s = %q, want %q", purpose, name, got, want)
					}
				}
			}
		})
	}
}
