package engine

import "testing"

func TestClaudeSettingsPayloadEmptyThemeMatchesConst(t *testing.T) {
	if got := ClaudeSettingsPayload(""); got != OutputStyleDefaultSettings {
		t.Fatalf("ClaudeSettingsPayload(\"\") = %q, want the byte-identical const %q", got, OutputStyleDefaultSettings)
	}
}

func TestClaudeSettingsPayloadMergesTheme(t *testing.T) {
	want := `{"outputStyle":"default","theme":"custom:professor-silver"}`
	if got := ClaudeSettingsPayload("custom:professor-silver"); got != want {
		t.Fatalf("ClaudeSettingsPayload(custom:professor-silver) = %q, want %q", got, want)
	}
}

func TestClaudeSettingsPayloadEscapesTheme(t *testing.T) {
	want := `{"outputStyle":"default","theme":"a\"b"}`
	if got := ClaudeSettingsPayload(`a"b`); got != want {
		t.Fatalf("ClaudeSettingsPayload(a\"b) = %q, want %q", got, want)
	}
}

func TestLaunchArgsWithSettingsSwapsTheValue(t *testing.T) {
	want := []string{"--settings", `{"outputStyle":"default","theme":"dark"}`}
	got := LaunchArgsWithSettings(Claude, nil, ClaudeSettingsPayload("dark"))
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("LaunchArgsWithSettings(Claude, nil, themed) = %#v, want %#v", got, want)
	}
}

func TestLaunchArgsWithSettingsEmptyLeavesLaunchArgsForUnchanged(t *testing.T) {
	got := LaunchArgsWithSettings(Claude, nil, "")
	want := LaunchArgsFor(Claude, nil)
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("LaunchArgsWithSettings(Claude, nil, \"\") = %#v, want %#v", got, want)
	}
}

// TestLaunchArgsWithSettingsStillDropsACallerStatedSettingsFlag is the same
// drop rule LaunchArgsFor enforces: a caller who already states --settings
// keeps their own file, themed or not — LaunchArgsWithSettings must not
// resurrect the fleet's flag just because a theme is in hand.
func TestLaunchArgsWithSettingsStillDropsACallerStatedSettingsFlag(t *testing.T) {
	got := LaunchArgsWithSettings(Claude, []string{"--settings", "/tmp/mine.json"}, ClaudeSettingsPayload("dark"))
	if len(got) != 0 {
		t.Fatalf("LaunchArgsWithSettings(Claude, caller --settings, themed) = %#v, want empty", got)
	}
}
