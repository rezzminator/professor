package action

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
)

func TestClaudeSpawnCommandUsesRunnerStartBoundary(t *testing.T) {
	fake := &deps.FakeRunner{}
	binary := deps.Executable("sh")
	fake.ScriptStart([]string{binary}, 42, nil, nil)
	spawn := ClaudeSpawn{
		Purpose: PurposeQuery,
		Account: 1,
		Machine: pfmconfig.Config{Claude: pfmconfig.ClaudePrefs{Binary: binary}},
		Runner:  fake,
	}
	command, err := spawn.Command(context.Background())
	if err != nil {
		t.Fatalf("Command() error = %v", err)
	}
	command.Stdout = &bytes.Buffer{}
	command.Stderr = &bytes.Buffer{}
	if err := command.Run(); err != nil {
		t.Fatalf("ProcessCommand.Run() error = %v", err)
	}
	calls := fake.Starts()
	if len(calls) != 1 || len(calls[0].Argv) == 0 || calls[0].Argv[0] != binary {
		t.Fatalf("Runner starts = %#v, want one call beginning with %q", calls, binary)
	}
	if calls[0].Opts.Stdout == nil || calls[0].Opts.Stderr == nil {
		t.Fatalf("Runner stdio = %#v, want caller streams", calls[0].Opts)
	}
}

func TestClaudeSpawnRendersRegistryPayload(t *testing.T) {
	home := t.TempDir()
	machine := configuredMachinePolicy(home)
	spawn := ClaudeSpawn{Purpose: PurposeInteractive, Account: 42, Home: home, Machine: machine}
	command, err := spawn.Command(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := claudelaunch.Parse(command.Args)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.SettingsEnv["CACHE_LIVE_CONTROL_MAIN_TTL"] != "5m" {
		t.Fatalf("settings env = %#v", parsed.SettingsEnv)
	}
	for _, entry := range command.Env {
		if strings.HasPrefix(entry, "CACHE_LIVE_CONTROL_MAIN_TTL=") {
			t.Fatalf("cache escaped settings into process env: %q", entry)
		}
	}
}

// pfm stages its own system prompt (--system-prompt-file); Claude Code's own
// output style (a project or user "outputStyle" setting) would otherwise
// double-apply a persona on top of it. Every purpose the door recognizes —
// interactive, resume, probe, query — must carry --settings
// {"outputStyle":"default"} on both renderers, since a probe or a query still
// starts a real Claude process even though it carries no prompt material of
// its own.
func TestClaudeSpawnCarriesDefaultOutputStyle(t *testing.T) {
	home := t.TempDir()
	machine := configuredMachinePolicy(home)
	for _, purpose := range []Purpose{PurposeInteractive, PurposeResume, PurposeLauncher, PurposeQuery} {
		spawn := ClaudeSpawn{Purpose: purpose, Account: 42, Home: home, Machine: machine}

		shell, err := spawn.ShellCommand()
		if err != nil {
			t.Fatalf("%v shell spawn: %v", purpose, err)
		}
		if got := parsedShell(t, shell).Settings["outputStyle"]; got != "default" {
			t.Fatalf("%v shell output style = %#v", purpose, got)
		}

		command, err := spawn.Command(context.Background())
		if err != nil {
			t.Fatalf("%v command spawn: %v", purpose, err)
		}
		parsed, err := claudelaunch.Parse(command.Args)
		if err != nil {
			t.Fatal(err)
		}
		if parsed.Settings["outputStyle"] != "default" {
			t.Fatalf(
				"%v command argv %#v lacks outputStyle default",
				purpose,
				command.Args,
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
func TestClaudeSpawnCarriesRegistrySettingsAfterCallerFlag(t *testing.T) {
	home := t.TempDir()
	machine := configuredMachinePolicy(home)
	spawn := ClaudeSpawn{
		Purpose: PurposeInteractive, Account: 42, Home: home, Machine: machine,
		Args: []string{"--settings", `{"outputStyle":"mine"}`},
	}

	shell, err := spawn.ShellCommand()
	if err != nil {
		t.Fatalf("shell spawn: %v", err)
	}
	if !strings.Contains(shell, "'--settings' '{\"outputStyle\":\"mine\"}'") {
		t.Fatalf("shell spawn %q lacks the caller's --settings value", shell)
	}
	if got := parsedShell(t, shell).Settings["outputStyle"]; got != "default" {
		t.Fatalf("registry output style = %#v", got)
	}

	command, err := spawn.Command(context.Background())
	if err != nil {
		t.Fatalf("command spawn: %v", err)
	}
	if !containsFlagPair(command.Args, "--settings", `{"outputStyle":"mine"}`) {
		t.Fatalf("command argv %#v lacks the caller's --settings value", command.Args)
	}
	settingsCount := 0
	for _, argument := range command.Args {
		if argument == "--settings" {
			settingsCount++
		}
	}
	if settingsCount != 2 {
		t.Fatalf("command argv %#v carries %d --settings words, want 2", command.Args, settingsCount)
	}
}

// A launch that already staged the professor prompt but somehow lost the
// settings flag would double-apply a persona; the launcher-run door
// (action.LauncherRun, the argv-preserving shim spawn) must carry the flag
// too, since it is a distinct constructor from ClaudeSpawn's exported fields.
func TestLauncherRunCarriesDefaultOutputStyle(t *testing.T) {
	home := t.TempDir()
	shell, err := LauncherRun("/opt/claude/real", nil, "/home/tester/.cc/1", home, pfmconfig.ClaudePrefs{})
	if err != nil {
		t.Fatalf("LauncherRun() error = %v", err)
	}
	if got := parsedShell(t, shell).Settings["outputStyle"]; got != "default" {
		t.Fatalf("launcher output style = %#v", got)
	}
}

func TestLauncherRunSessionRouting(t *testing.T) {
	for _, scenario := range []struct {
		name, wantSession, wantResume string
		args                          []string
		fresh                         bool
	}{
		{name: "fresh", fresh: true},
		{name: "resume", args: []string{"--resume", "R"}, wantResume: "R"},
		{name: "explicit", args: []string{"--session-id", "S"}, wantSession: "S"},
		{name: "continue", args: []string{"--continue"}},
		{name: "short continue", args: []string{"-c"}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			run, err := LauncherRun("/bin/claude", scenario.args, "", t.TempDir(),
				pfmconfig.ClaudePrefs{PermissionMode: pfmconfig.PermissionBypass})
			if err != nil {
				t.Fatal(err)
			}
			parsed := parsedShell(t, run)
			if parsed.Autonomy || parsed.Resume != scenario.wantResume ||
				(!scenario.fresh && parsed.SessionID != scenario.wantSession) ||
				(scenario.fresh && parsed.SessionID == "") {
				t.Fatalf("session=%q resume=%q autonomy=%t", parsed.SessionID, parsed.Resume, parsed.Autonomy)
			}
			if scenario.wantSession != "" && strings.Count(run, "'--session-id'") != 1 {
				t.Fatalf("duplicate session flag: %q", run)
			}
		})
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

func parsedShell(t *testing.T, run string) claudelaunch.Parsed {
	t.Helper()
	run, _, _ = strings.Cut(run, " || ")
	output, err := exec.Command("sh", "-c", "set -- "+run+"; printf '%s\\000' \"$@\"").Output()
	if err != nil {
		t.Fatalf("parse shell launch: %v", err)
	}
	words := strings.Split(strings.TrimSuffix(string(output), "\x00"), "\x00")
	parsed, err := claudelaunch.Parse(append([]string{"claude"}, words...))
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func parsedSpawn(t *testing.T, spawn ClaudeSpawn) claudelaunch.Parsed {
	t.Helper()
	command, err := spawn.Command(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := claudelaunch.Parse(command.Args)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
