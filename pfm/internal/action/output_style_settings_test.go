package action

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	"github.com/rezzminator/professor/pfm/internal/cmdparse"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
)

func TestClaudeSpawnCommandUsesRunnerStartBoundary(t *testing.T) {
	fake := &deps.FakeRunner{}
	binary := "/scripted/claude"
	fake.ScriptLookPath(binary, binary, nil)
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
	if !slices.Contains(command.Env, "CACHE_LIVE_CONTROL_MAIN_TTL=5m") {
		t.Fatalf("process env lacks CACHE_LIVE_CONTROL_MAIN_TTL=5m: %q", command.Env)
	}
	if value, ok := parsed.SettingsEnv["CACHE_LIVE_CONTROL_MAIN_TTL"]; ok {
		t.Fatalf("settings env carries CACHE_LIVE_CONTROL_MAIN_TTL=%q; a settings reload would undo /cache", value)
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

// TestClaudeSpawnFoldsTheCallerSettingsIntoItsOwn is the regression for the
// silently-discarded --settings bug: Claude reads one --settings, the last, so
// the fleet's own pair appended behind a caller's --settings discarded the
// caller's. Both renderers carry exactly ONE --settings word: the fleet's
// settings with the caller's keys folded in, the caller's winning.
func TestClaudeSpawnFoldsTheCallerSettingsIntoItsOwn(t *testing.T) {
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
	command, err := spawn.Command(context.Background())
	if err != nil {
		t.Fatalf("command spawn: %v", err)
	}
	for renderer, words := range map[string][]string{"shell": shellWords(t, shell), "argv": command.Args} {
		var payloads []string
		for index, word := range words {
			if word == "--settings" && index+1 < len(words) {
				payloads = append(payloads, words[index+1])
			}
		}
		if len(payloads) != 1 {
			t.Fatalf("%s spawn %q carries %d --settings words, want 1", renderer, words, len(payloads))
		}
		var settings map[string]any
		if err := json.Unmarshal([]byte(payloads[0]), &settings); err != nil {
			t.Fatalf("%s spawn --settings %q: %v", renderer, payloads[0], err)
		}
		if got := settings["outputStyle"]; got != "mine" {
			t.Fatalf("%s spawn outputStyle = %#v, want the caller's \"mine\"", renderer, got)
		}
		if _, held := settings["hooks"]; !held {
			t.Fatalf("%s spawn --settings %q lost the fleet's hooks", renderer, payloads[0])
		}
	}
}

// A launch that already staged the professor prompt but somehow lost the
// settings flag would double-apply a persona; the launcher-run door
// (action.LauncherRun, the argv-preserving shim spawn) must carry the flag
// too, since it is a distinct constructor from ClaudeSpawn's exported fields.
func TestLauncherRunCarriesDefaultOutputStyle(t *testing.T) {
	home := t.TempDir()
	shell, err := LauncherRun(
		"/opt/claude/real",
		nil,
		t.TempDir(),
		home,
		pfmconfig.Config{},
		pfmconfig.ClaudePrefs{},
		"",
	)
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
				pfmconfig.Config{}, pfmconfig.ClaudePrefs{PermissionMode: pfmconfig.PermissionBypass}, "")
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

func shellWords(t *testing.T, run string) []string {
	t.Helper()
	words, err := cmdparse.SplitShellWords(launchHead(run))
	if err != nil {
		t.Fatalf("parse shell launch %q: %v", run, err)
	}
	return words
}

// launchHead is a launch line up to its fallback tail, the first " || " outside
// quotes: a quoted " || ", as in a hook command inside the --settings JSON, is
// part of a word.
func launchHead(run string) string {
	var quote rune
	escaped := false
	for index, character := range run {
		switch {
		case escaped:
			escaped = false
		case quote == '\'':
			if character == '\'' {
				quote = 0
			}
		case character == '\\':
			escaped = true
		case quote == '"':
			if character == '"' {
				quote = 0
			}
		case character == '\'' || character == '"':
			quote = character
		case strings.HasPrefix(run[index:], " || "):
			return run[:index]
		}
	}
	return run
}

func parsedShell(t *testing.T, run string) claudelaunch.Parsed {
	t.Helper()
	parsed, err := claudelaunch.Parse(append([]string{"claude"}, shellWords(t, run)...))
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

// launchEnv is the environment a launch line assigns its Claude process: the
// NAME=value operands of the leading env word, up to the binary.
func launchEnv(t *testing.T, run string) map[string]string {
	t.Helper()
	env := map[string]string{}
	words := shellWords(t, run)
	for index := 0; index < len(words); index++ {
		switch word := words[index]; {
		case index == 0 && word == "env":
		case word == "-u":
			index++
		default:
			name, value, found := strings.Cut(word, "=")
			if !found || name == "" || strings.Contains(name, "/") {
				return env
			}
			env[name] = value
		}
	}
	return env
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
