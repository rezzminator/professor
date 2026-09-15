package action

import (
	"context"
	"strings"
	"testing"

	pfmconfig "hostops/pfm/internal/config"
	pfmengine "hostops/pfm/internal/engine"
)

// pfm stages its own system prompt (--system-prompt-file); Claude Code's own
// output style (a project or user "outputStyle" setting) would otherwise
// double-apply a persona on top of it. Every purpose the door recognizes —
// interactive, resume, probe, query — must carry --settings
// {"outputStyle":"default"} on both renderers, since a probe or a query still
// starts a real Claude process even though it carries no prompt material of
// its own.
func TestClaudeSpawnCarriesOutputStyleDefaultSettings(t *testing.T) {
	home := t.TempDir()
	machine := configuredMachinePolicy(home)
	want := "'--settings' " + Quote(pfmengine.OutputStyleDefaultSettings)
	for _, purpose := range []Purpose{PurposeInteractive, PurposeResume, PurposeProbe, PurposeQuery} {
		spawn := ClaudeSpawn{Purpose: purpose, Account: 42, Home: home, Machine: machine}

		shell, err := spawn.ShellCommand()
		if err != nil {
			t.Fatalf("%s shell spawn: %v", purpose, err)
		}
		if !strings.Contains(shell, want) {
			t.Fatalf("%s shell spawn %q lacks %q", purpose, shell, want)
		}

		command, err := spawn.Command(context.Background())
		if err != nil {
			t.Fatalf("%s command spawn: %v", purpose, err)
		}
		if !containsFlagPair(command.Args, "--settings", pfmengine.OutputStyleDefaultSettings) {
			t.Fatalf(
				"%s command argv %#v lacks --settings %s",
				purpose,
				command.Args,
				pfmengine.OutputStyleDefaultSettings,
			)
		}
	}
}

// TestClaudeSpawnKeepsACallerSuppliedSettingsFlag is the regression for the
// silently-discarded --settings bug: ShellCommand and argv used to append the
// fleet's own --settings {"outputStyle":"default"} AFTER spawn.Args, and
// --settings is single-value, so a caller who typed their own --settings
// through pfm's launcher had it overridden by the fleet's pair with no
// warning. Both renderers must now keep the caller's file and carry exactly
// ONE --settings word, not the fleet's default payload.
func TestClaudeSpawnKeepsACallerSuppliedSettingsFlag(t *testing.T) {
	home := t.TempDir()
	machine := configuredMachinePolicy(home)
	spawn := ClaudeSpawn{
		Purpose: PurposeInteractive, Account: 42, Home: home, Machine: machine,
		Args: []string{"--settings", "/tmp/mine.json"},
	}

	shell, err := spawn.ShellCommand()
	if err != nil {
		t.Fatalf("shell spawn: %v", err)
	}
	if !strings.Contains(shell, "'--settings' '/tmp/mine.json'") {
		t.Fatalf("shell spawn %q lacks the caller's --settings value", shell)
	}
	if strings.Contains(shell, Quote(pfmengine.OutputStyleDefaultSettings)) {
		t.Fatalf("shell spawn %q still carries the fleet's default settings payload", shell)
	}
	if got := strings.Count(shell, "'--settings'"); got != 1 {
		t.Fatalf("shell spawn %q carries %d '--settings' words, want exactly 1", shell, got)
	}

	command, err := spawn.Command(context.Background())
	if err != nil {
		t.Fatalf("command spawn: %v", err)
	}
	if !containsFlagPair(command.Args, "--settings", "/tmp/mine.json") {
		t.Fatalf("command argv %#v lacks the caller's --settings value", command.Args)
	}
	settingsCount := 0
	for _, argument := range command.Args {
		if argument == "--settings" {
			settingsCount++
		}
	}
	if settingsCount != 1 {
		t.Fatalf("command argv %#v carries %d --settings words, want exactly 1", command.Args, settingsCount)
	}
}

// A launch that already staged the professor prompt but somehow lost the
// settings flag would double-apply a persona; the launcher-run door
// (action.LauncherRun, the argv-preserving shim spawn) must carry the flag
// too, since it is a distinct constructor from ClaudeSpawn's exported fields.
func TestLauncherRunCarriesOutputStyleDefaultSettings(t *testing.T) {
	home := t.TempDir()
	shell, err := LauncherRun("/opt/claude/real", nil, "/home/tester/.cc/1", home, pfmconfig.ClaudePrefs{})
	if err != nil {
		t.Fatalf("LauncherRun() error = %v", err)
	}
	want := "'--settings' " + Quote(pfmengine.OutputStyleDefaultSettings)
	if !strings.Contains(shell, want) {
		t.Fatalf("launcher run %q lacks %q", shell, want)
	}
}

func containsFlagPair(args []string, key, value string) bool {
	for index := 0; index+1 < len(args); index++ {
		if args[index] == key && args[index+1] == value {
			return true
		}
	}
	return false
}
