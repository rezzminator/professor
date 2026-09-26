package run

import (
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// pfm stages its own system prompt (--system-prompt-file / --system-prompt);
// Claude Code's own output style would otherwise double-apply a persona on
// top of it. Every headless Claude run — `pfm headless exec`, `pfm ask` — is
// a door of its own (it never renders through action.ClaudeSpawn), so it must
// disable Claude Code's output style too, the same as the fleet's other
// spawn door.
func TestArgumentsCarriesOutputStyleDefaultSettings(t *testing.T) {
	args, err := arguments(Request{Engine: pfmengine.Claude})
	if err != nil {
		t.Fatalf("arguments() error = %v", err)
	}
	if !containsPair(args, "--settings", pfmengine.OutputStyleDefaultSettings) {
		t.Fatalf("Claude args %#v lack --settings %s", args, pfmengine.OutputStyleDefaultSettings)
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
	args, err := arguments(Request{Engine: pfmengine.Claude, Args: []string{"--settings", "/tmp/mine.json"}})
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
