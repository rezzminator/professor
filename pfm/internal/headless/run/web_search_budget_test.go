package run

import (
	"strings"
	"testing"

	pfmengine "hostops/pfm/internal/engine"
)

// A headless Claude run is a door of its own (it never renders through
// action.ClaudeSpawn), so it must lift Claude Code's 200-call WebSearch cap
// too — for inherited and explicit environments alike — while a Codex run
// carries nothing of Claude's.
func TestSetEnvironmentCarriesClaudeWebSearchBudget(t *testing.T) {
	const name, value = "CLAUDE_CODE_MAX_WEB_SEARCHES_PER_SESSION", "9007199254740991"
	for _, explicit := range []bool{false, true} {
		var claude []string
		setEnvironment([]string{name + "=5", "PATH=/usr/bin"}, pfmengine.Claude, "/cfg", explicit, &claude)
		if got := lastEnvironmentValue(claude, name); got != value {
			t.Fatalf(
				"claude headless environment (explicit=%t) %q resolves %s=%q, want %q",
				explicit,
				claude,
				name,
				got,
				value,
			)
		}
		var codex []string
		setEnvironment([]string{"PATH=/usr/bin"}, pfmengine.Codex, "/cfg", explicit, &codex)
		if got := lastEnvironmentValue(codex, name); got != "" {
			t.Fatalf("codex headless environment (explicit=%t) %q carries Claude's %s=%q", explicit, codex, name, got)
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
