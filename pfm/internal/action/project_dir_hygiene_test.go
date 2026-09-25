package action

import (
	"strings"
	"testing"
)

// A chat spawned from inside another chat's hook inherits that session's
// CLAUDE_PROJECT_DIR, and Claude Code keeps an inherited value instead of
// recomputing it: every $CLAUDE_PROJECT_DIR hook in the NEW project then runs
// the OLD project's script — a 127 when the path is absent, and, worse, a
// foreign guard when it exists. The fleet strip must drop it on both renderers
// of the spawn door so the harness computes it fresh.
const projectDirName = "CLAUDE_PROJECT_DIR"

func TestClaudeSpawnStripsInheritedProjectDir(t *testing.T) {
	home := t.TempDir()
	machine := configuredMachinePolicy(home)
	for _, purpose := range []Purpose{PurposeInteractive, PurposeResume, PurposeProbe, PurposeQuery} {
		spawn := ClaudeSpawn{Purpose: purpose, Account: 42, Home: home, Machine: machine}
		shell, err := spawn.ShellCommand()
		if err != nil {
			t.Fatalf("%s shell spawn: %v", purpose, err)
		}
		if want := " -u " + projectDirName + " "; !strings.Contains(shell, want) {
			t.Fatalf("%s shell spawn %q lacks %q", purpose, shell, want)
		}
		environment := spawn.Environment([]string{projectDirName + "=/srv/tester/.professor", "PATH=/usr/bin"})
		if got := lastEnvironmentValue(environment, projectDirName); got != "" {
			t.Fatalf("%s spawn environment %q kept %s=%q, want it stripped",
				purpose, environment, projectDirName, got)
		}
	}
}

func TestDerivedStripsAlsoDropProjectDir(t *testing.T) {
	for name, names := range map[string][]string{
		"fleet":    hygieneNames,
		"headless": headlessHygieneNames,
		"opencode": opencodeHygieneNames,
	} {
		found := false
		for _, entry := range names {
			if entry == projectDirName {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s strip list %v lacks %s", name, names, projectDirName)
		}
	}
}

// HygieneNames is how another package strips the fleet list without a copy of
// its own: it returns every name in hygieneNames, and the caller's slice is
// its own, so an append or overwrite there never edits the fleet strip.
func TestHygieneNamesReturnsACopyOfTheFleetList(t *testing.T) {
	names := HygieneNames()
	if strings.Join(names, " ") != strings.Join(hygieneNames, " ") {
		t.Fatalf("HygieneNames() = %v, want %v", names, hygieneNames)
	}
	names[0] = "OVERWRITTEN_BY_CALLER"
	if hygieneNames[0] == "OVERWRITTEN_BY_CALLER" {
		t.Fatal("HygieneNames() shares its backing array with hygieneNames")
	}
}
