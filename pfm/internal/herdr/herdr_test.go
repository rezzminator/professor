package herdr

import (
	"slices"
	"strings"
	"testing"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

func TestHookCommandNamesTheEngineAndTheEvent(t *testing.T) {
	for id, want := range map[pfmengine.ID]string{
		pfmengine.Claude:   "/h/.local/bin/pfm internal herdr-state --engine claude working",
		pfmengine.Codex:    "/h/.local/bin/pfm internal herdr-state --engine codex working",
		pfmengine.OpenCode: "/h/.local/bin/pfm internal herdr-state --engine opencode working",
	} {
		if got := HookCommand("/h/.local/bin/pfm", id, EventWorking); got != want {
			t.Errorf("HookCommand(%s) = %q, want %q", id, got, want)
		}
	}
}

// Herdr's resume_argv rules: a plain command word first, no apostrophe or
// control character anywhere, at most 64 words and 8 KiB.
func TestResumeArgvObeysHerdrsRules(t *testing.T) {
	good, err := ResumeArgv("6b1a2c3d-0000-4000-8000-000000000001")
	if err != nil || !slices.Equal(good, []string{"pfm", "chat", "open", "6b1a2c3d-0000-4000-8000-000000000001"}) {
		t.Fatalf("ResumeArgv(uuid) = %v, %v, want pfm chat open {id}", good, err)
	}
	if good[0] != ResumeCommand || strings.Contains(good[0], "/") {
		t.Fatalf("first word %q must be a plain command name", good[0])
	}
	for _, test := range []struct {
		name, session, want string
	}{
		{"empty", "", "empty session id"},
		{"apostrophe", "it's", "apostrophe"},
		{"newline", "a\nb", "control character"},
		{"nul", "a\x00b", "control character"},
		{"escape", "a\x1bb", "control character"},
		{"too long", strings.Repeat("x", 9<<10), "exceed"},
		{"invalid utf-8", "a\xffb", "UTF-8"},
	} {
		if got, err := ResumeArgv(test.session); err == nil || !strings.Contains(err.Error(), test.want) || got != nil {
			t.Errorf("%s: ResumeArgv = %v, %v, want an error naming %q", test.name, got, err, test.want)
		}
	}
}

// A child that must never report as the parent's pane loses the Herdr names;
// one that can fire a reporter hook also loses the tmux pane names, because the
// reporter finds its pane through TMUX_PANE.
func TestStripPaneDropsTheHerdrNamesAndStripReporterAlsoTheTmuxPane(t *testing.T) {
	environ := []string{
		"KEEP=1", "HERDR_ENV=1", "HERDR_PANE_ID=w1:p1", "TMUX=/tmp/t,1,0", "HERDR_SOCKET_PATH=/tmp/h.sock",
		"TMUX_PANE=%3", "HERDR_BIN_PATH=/usr/local/bin/herdr", "HERDR_ENV_EXTRA=kept", "KEEP2=2",
	}
	if got, want := StripPane(environ), []string{
		"KEEP=1", "TMUX=/tmp/t,1,0", "TMUX_PANE=%3", "HERDR_ENV_EXTRA=kept", "KEEP2=2",
	}; !slices.Equal(got, want) {
		t.Errorf("StripPane = %q, want %q", got, want)
	}
	if got, want := StripReporter(environ), []string{
		"KEEP=1", "HERDR_ENV_EXTRA=kept", "KEEP2=2",
	}; !slices.Equal(got, want) {
		t.Errorf("StripReporter = %q, want %q", got, want)
	}
	if got := StripPane(nil); len(got) != 0 {
		t.Errorf("StripPane(nil) = %q, want empty", got)
	}
}

// An engine's kill timer leaves the reporter its whole deadline plus a margin
// for its start-up, its payload read and its error log.
func TestEngineKillTimeoutOutlastsTheReportDeadline(t *testing.T) {
	if EngineKillTimeout-ReportDeadline < time.Second {
		t.Fatalf("EngineKillTimeout %v leaves under 1s past ReportDeadline %v", EngineKillTimeout, ReportDeadline)
	}
}
