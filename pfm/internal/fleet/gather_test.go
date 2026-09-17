package fleet

import (
	"bytes"
	"testing"

	pfmconfig "hostops/pfm/internal/config"
	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/paths"
)

// TestPrintWarnNamesTheProbe pins the one-shot warning line operators grep for.
func TestPrintWarnNamesTheProbe(t *testing.T) {
	var stderr bytes.Buffer
	PrintWarn(&stderr)("socket cc-3 did not answer")
	if got := stderr.String(); got != "pfm: tmux probe warning: socket cc-3 did not answer\n" {
		t.Fatalf("PrintWarn wrote %q", got)
	}
}

// TestKillDependenciesCopiesTheClaudeRoots pins the kill manager's view of the
// runtime, and that it owns its copy of the Claude roots: the manager must
// never alias a slice the caller keeps mutating.
func TestKillDependenciesCopiesTheClaudeRoots(t *testing.T) {
	runtime := pfmconfig.Runtime{
		Config: pfmconfig.Config{
			Path:          "/c/config.toml",
			CodexAccounts: []pfmconfig.CodexAccount{{ID: 1, Home: "/c/codex-1"}},
		},
		Paths: paths.Values{Roots: map[pfmengine.ID][]string{pfmengine.Claude: {"/c/claude-1"}}},
	}
	dependencies := KillDependencies(runtime)
	runtime.Paths.Roots[pfmengine.Claude][0] = "mutated"
	if len(dependencies.ClaudeRoots) != 1 || dependencies.ClaudeRoots[0] != "/c/claude-1" {
		t.Fatalf("ClaudeRoots = %v, want an independent copy", dependencies.ClaudeRoots)
	}
	if len(dependencies.CodexHomes) != 1 || dependencies.CodexHomes[0] != "/c/codex-1" ||
		dependencies.ConfigPath != "/c/config.toml" {
		t.Fatalf("dependencies = %+v", dependencies)
	}
}
