package run

import (
	"testing"

	pfmconfig "hostops/pfm/internal/config"
	pfmengine "hostops/pfm/internal/engine"
)

// A headless Claude run is a door of its own — it never renders through
// action.ClaudeSpawn — and it spawns sub-agents like any other chat, so it has
// to carry the same lifted ceilings. Codex reads neither name and gets
// neither.
func TestSubagentCapsCarryOnlyForClaude(t *testing.T) {
	const depthName, concurrencyName = "CLAUDE_CODE_MAX_SUBAGENT_SPAWN_DEPTH", "CLAUDE_CODE_MAX_CONCURRENT_SUBAGENTS"
	machine := pfmconfig.Config{
		Claude:   pfmconfig.Claude{MaxSubagentSpawnDepth: 9},
		Accounts: []pfmconfig.Account{{ID: 2, Claude: &pfmconfig.ClaudePrefs{MaxSubagentSpawnDepth: 5, MaxConcurrentSubagents: 32}}},
	}
	for _, explicit := range []bool{false, true} {
		var claude []string
		setEnvironment(
			[]string{depthName + "=1", "PATH=/usr/bin"},
			pfmengine.Claude,
			"/cfg",
			explicit,
			&claude,
			subagentCaps(Request{Engine: pfmengine.Claude, Account: 2, Config: machine})...,
		)
		for name, want := range map[string]string{depthName: "5", concurrencyName: "32"} {
			if got := lastEnvironmentValue(claude, name); got != want {
				t.Fatalf(
					"claude headless environment (explicit=%t) %q resolves %s=%q, want %q",
					explicit, claude, name, got, want,
				)
			}
		}
		var codex []string
		setEnvironment(
			[]string{"PATH=/usr/bin"},
			pfmengine.Codex,
			"/cfg",
			explicit,
			&codex,
			subagentCaps(Request{Engine: pfmengine.Codex, Account: 2, Config: machine})...,
		)
		for _, name := range []string{depthName, concurrencyName} {
			if got := lastEnvironmentValue(codex, name); got != "" {
				t.Fatalf("codex headless environment (explicit=%t) %q carries Claude's %s=%q", explicit, codex, name, got)
			}
		}
	}
}

// An account with no cap of its own still launches with the fleet default —
// the harness's own 3 is never what a managed headless run gets.
func TestSubagentCapsDefaultForUnconfiguredAccount(t *testing.T) {
	caps := subagentCaps(Request{Engine: pfmengine.Claude})
	want := "CLAUDE_CODE_MAX_SUBAGENT_SPAWN_DEPTH=8"
	if len(caps) != 1 || caps[0] != want {
		t.Fatalf("subagentCaps() = %v, want exactly [%s]", caps, want)
	}
}
