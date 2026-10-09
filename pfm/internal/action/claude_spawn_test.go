package action

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// installCostLauncher stands a fake pfm where the launch registry looks for the cost launcher.
func installCostLauncher(t *testing.T, home string) string {
	t.Helper()
	path := filepath.Join(home, ".local", "bin", "pfm")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := testjail.WriteExecutable(path, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestClaudeSpawnRunsTheCostLauncherInFrontOfClaude(t *testing.T) {
	home := t.TempDir()
	pfm := installCostLauncher(t, home)
	machine := configuredMachinePolicy(home)
	machine.Claude.CostLedger = true
	spawn := ClaudeSpawn{Purpose: PurposeInteractive, Account: 42, Home: home, Machine: machine}

	shell, err := spawn.ShellCommand()
	if err != nil {
		t.Fatal(err)
	}
	var words []string
	for _, word := range []string{pfm, "internal", "cost-launch", "--", "/opt/tools/claude enterprise"} {
		words = append(words, Quote(word))
	}
	launcher := strings.Join(words, " ")
	if !strings.Contains(shell, " "+launcher+" ") {
		t.Fatalf("ShellCommand lacks the launcher in front of claude %q: %.400s", launcher, shell)
	}
	if strings.Index(shell, "PFM_COST_LEDGER") >= strings.Index(shell, launcher) &&
		strings.Contains(shell, "PFM_COST_LEDGER") {
		t.Errorf("the marker assignment comes after the launcher; env words must lead: %.400s", shell)
	}

	command, err := spawn.Command(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	claude := deps.Executable("/opt/tools/claude enterprise")
	if command.Path != pfm {
		t.Errorf("Command Path = %q, want the launcher %q", command.Path, pfm)
	}
	if want := []string{pfm, "internal", "cost-launch", "--", claude}; !slices.Equal(command.Args[:len(want)], want) {
		t.Errorf("Command Args begin %q, want %q", command.Args[:min(len(want), len(command.Args))], want)
	}
	if len(command.Args) < 6 {
		t.Errorf("Command Args = %q, want claude's own argv after the launcher", command.Args)
	}
}

func TestClaudeSpawnWithoutAnInstalledLauncherStartsClaudeDirectlyAndSaysSo(t *testing.T) {
	home := t.TempDir()
	machine := configuredMachinePolicy(home)
	machine.Claude.CostLedger = true
	spawn := ClaudeSpawn{Purpose: PurposeInteractive, Account: 42, Home: home, Machine: machine}

	shell, err := spawn.ShellCommand()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(shell, "cost-launch") ||
		!strings.Contains(shell, " PFM_COST_LEDGER='failed' '/opt/tools/claude enterprise' ") {
		t.Errorf(
			"no launcher installed: want claude started directly after PFM_COST_LEDGER='failed', got %.400s",
			shell,
		)
	}
	command, err := spawn.Command(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if command.Path != deps.Executable("/opt/tools/claude enterprise") ||
		!slices.Contains(command.Env, "PFM_COST_LEDGER=failed") {
		t.Errorf(
			"no launcher installed: Path %q Env has the marker = %v",
			command.Path,
			slices.Contains(command.Env, "PFM_COST_LEDGER=failed"),
		)
	}
}
