package claudelaunch

import (
	"slices"
	"strings"
	"testing"
)

func TestHookTemplates(t *testing.T) {
	home := t.TempDir()
	hooks := HookTemplates(home)
	if len(hooks) != 19 {
		t.Fatalf("registrations=%d, want 19", len(hooks))
	}
	for _, hook := range hooks {
		if !strings.HasPrefix(hook.Command, home+"/.local/bin/pfm ") {
			t.Errorf("command=%q", hook.Command)
		}
	}
}

func TestStoreGuardRidesBesideGitGuardOnBash(t *testing.T) {
	home := t.TempDir()
	var got []Hook
	for _, hook := range HookTemplates(home) {
		if hook.Event == hookEventPreToolUse && hook.Matcher == "Bash" {
			got = append(got, hook)
		}
	}
	want := []string{home + "/.local/bin/pfm internal git-guard", home + "/.local/bin/pfm internal store-guard"}
	if len(got) != len(want) {
		t.Fatalf("PreToolUse Bash hooks=%+v, want git-guard and store-guard", got)
	}
	for i, hook := range got {
		if hook.Command != want[i] {
			t.Errorf("PreToolUse Bash hook %d command=%q, want %q", i, hook.Command, want[i])
		}
	}
}

// The Herdr reporter rides every Claude launch: one row per lifecycle event,
// the per-turn and per-tool state flips async so a turn never waits on Herdr,
// PermissionRequest reports blocked with the tool it names, and the
// Notification row selects only the types the reporter maps.
func TestHerdrReporterRowsCoverTheClaudeLifecycle(t *testing.T) {
	home := t.TempDir()
	binary := home + "/.local/bin/pfm internal herdr-state --engine claude "
	want := []Hook{
		{Event: "SessionStart", Command: binary + "start", Name: "herdr-start"},
		{Event: "UserPromptSubmit", Command: binary + "working", Name: "herdr-working-prompt", Async: true},
		{Event: "PostToolUse", Command: binary + "working", Name: "herdr-working-tool", Async: true},
		{Event: "PostToolUseFailure", Command: binary + "working", Name: "herdr-working-tool-failure", Async: true},
		{Event: "PermissionRequest", Command: binary + "blocked", Name: "herdr-blocked", Async: true},
		{
			Event: "Notification", Matcher: "permission_prompt|elicitation_dialog|idle_prompt",
			Command: binary + "notification", Name: "herdr-notification", Async: true,
		},
		{Event: "Stop", Command: binary + "idle", Name: "herdr-idle"},
		{Event: "SessionEnd", Command: binary + "end", Name: "herdr-end"},
	}
	var got []Hook
	for _, hook := range HookTemplates(home) {
		if strings.HasPrefix(hook.Name, "herdr-") {
			got = append(got, hook)
		}
	}
	if !slices.Equal(got, want) {
		t.Fatalf("herdr rows = %+v, want %+v", got, want)
	}
}
