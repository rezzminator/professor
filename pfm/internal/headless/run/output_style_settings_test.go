package run

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

func TestArgumentsUsesOnlyPassedClaudeSettings(t *testing.T) {
	machine := pfmconfig.Config{Claude: pfmconfig.ClaudePrefs{Theme: "from-config", MaxSubagentSpawnDepth: 9}}
	for _, test := range []struct {
		name     string
		settings map[string]any
		want     string
	}{
		{"nothing passed", nil, `{"outputStyle":"default"}`},
		{"passed values", map[string]any{"theme": "t", "maxSubagentSpawnDepth": 4}, `{"env":{"CLAUDE_CODE_MAX_SUBAGENT_SPAWN_DEPTH":"4"},"outputStyle":"default","theme":"t"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			args, err := arguments(Request{Engine: pfmengine.Claude, Config: machine, Settings: test.settings})
			if err != nil {
				t.Fatal(err)
			}
			for index, arg := range args {
				if arg != "--settings" || index+1 >= len(args) {
					continue
				}
				var got, want any
				if err := json.Unmarshal([]byte(args[index+1]), &got); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal([]byte(test.want), &want); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("settings = %s, want %s", args[index+1], test.want)
				}
				return
			}
			t.Fatalf("missing settings in %q", args)
		})
	}
}

// pfm stages its own system prompt (--system-prompt-file / --system-prompt);
// Claude Code's own output style would otherwise double-apply a persona on
// top of it. Every headless Claude run — `pfm headless exec`, `pfm ask` — is
// a door of its own (it never renders through action.ClaudeSpawn), so it must
// disable Claude Code's output style too, the same as the fleet's other
// spawn door.
func TestArgumentsCarriesDefaultOutputStyle(t *testing.T) {
	args, err := arguments(Request{Engine: pfmengine.Claude})
	if err != nil {
		t.Fatalf("arguments() error = %v", err)
	}
	parsed, err := claudelaunch.Parse(append([]string{"claude"}, args...))
	if err != nil || parsed.Settings["outputStyle"] != "default" {
		t.Fatalf("Claude args %#v lack default output style: %#v, %v", args, parsed.Settings, err)
	}

	codexArgs, err := arguments(Request{Engine: pfmengine.Codex})
	if err != nil {
		t.Fatalf("arguments() error = %v", err)
	}
	for _, argument := range codexArgs {
		if argument == "--settings" {
			t.Fatalf("Codex args carried Claude's --settings flag: %#v", codexArgs)
		}
	}
}

// TestArgumentsKeepsACallerSuppliedSettingsFlag is the regression for the
// silently-discarded --settings bug: arguments() used to append LaunchArgs
// AFTER request.Args, so a caller-supplied --settings (a headless caller
// passing its own file through request.Args) lost to the fleet's own
// --settings {"outputStyle":"default"} appended behind it. The caller's value
// must now survive and the fleet's default payload must not appear at all.
func TestArgumentsKeepsACallerSuppliedSettingsFlag(t *testing.T) {
	args, err := arguments(Request{
		Engine: pfmengine.Claude, Args: []string{"--settings", "/tmp/mine.json"},
		Settings: map[string]any{"callerFileWins": true},
	})
	if err != nil {
		t.Fatalf("arguments() error = %v", err)
	}
	if !containsPair(args, "--settings", "/tmp/mine.json") {
		t.Fatalf("Claude args %#v lack the caller's --settings value", args)
	}
	count := 0
	for _, argument := range args {
		if argument == "--settings" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("Claude args %#v carry %d --settings words, want exactly 1", args, count)
	}
}
