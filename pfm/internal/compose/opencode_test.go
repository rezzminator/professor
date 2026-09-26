package compose

import (
	"testing"

	"github.com/rezzminator/professor/pfm/internal/store"
)

func openCodeFixture() []store.OpenCodeSession {
	return []store.OpenCodeSession{
		{ID: "ses_live", Title: "live one", Directory: "/work/a", ProjectDir: "/work/a", TimeUpdatedMS: 9_000},
		{
			ID:            "ses_titled",
			Title:         "titled",
			Directory:     "/work/b",
			ProjectDir:    "/work/b",
			FirstPrompt:   "fallback text",
			TimeUpdatedMS: 8_000,
		},
		{
			ID:            "ses_untitled",
			Title:         "",
			Directory:     "/work/c",
			ProjectDir:    "/work/c",
			FirstPrompt:   "untitled prompt body",
			TimeUpdatedMS: 7_000,
		},
		{
			ID:            "ses_child",
			ParentID:      "ses_titled",
			Title:         "child",
			Directory:     "/work/b",
			ProjectDir:    "/work/b",
			TimeUpdatedMS: 6_000,
		},
		{
			ID:             "ses_archived",
			Title:          "archived",
			Directory:      "/work/d",
			ProjectDir:     "/work/d",
			TimeUpdatedMS:  5_000,
			TimeArchivedMS: 4_000,
		},
	}
}

func TestOpenCodeSessionsBecomeResumeRows(t *testing.T) {
	output := Compose(Input{
		OpenCodeSessions: openCodeFixture(),
		Options:          Options{View: AllView},
	})
	rows := make(map[string]Row)
	for _, row := range output.Rows {
		if row.Kind == ResumeOpenCode {
			rows[row.ID] = row
		}
	}
	if len(rows) != 3 {
		t.Fatalf("resume-opencode rows = %d (%v), want 3", len(rows), rows)
	}
	live := rows["ses_live"]
	if live.CWD != "/work/a" || live.Name != "live one" {
		t.Errorf("ses_live = %+v", live)
	}
	if got := rows["ses_live"].ActivityNS; got != 9_000*1_000_000 {
		t.Errorf("ActivityNS = %d, want ms→ns converted", got)
	}
	if got := rows["ses_untitled"]; got.Name == "" {
		t.Error("untitled session lost its first-prompt fallback name")
	}
	if _, found := rows["ses_child"]; found {
		t.Error("subagent child earned a row")
	}
	if _, found := rows["ses_archived"]; found {
		t.Error("archived session earned a row")
	}
	for id, row := range rows {
		if EngineForKind(row.Kind) != "ox" {
			t.Errorf("%s: engine = %q, want ox", id, EngineForKind(row.Kind))
		}
	}
}

// An OpenCode session with prompts but zero assistant messages was opened
// and never answered — exactly as empty as a Claude transcript with no
// visible turns. It must be suppressed in the default view and counted in
// SuppressedCount, never shown as if it were a real chat.
func TestOpenCodeSessionWithNoAssistantReplyIsSuppressed(t *testing.T) {
	output := Compose(Input{
		OpenCodeSessions: []store.OpenCodeSession{
			{
				ID: "ses_unanswered", Title: "unanswered", Directory: "/work/a", ProjectDir: "/work/a",
				TimeUpdatedMS: 1, PromptCount: 1, AssistantCount: 0,
			},
		},
		Options: Options{View: DefaultView},
	})
	for _, row := range output.Rows {
		if row.ID == "ses_unanswered" {
			t.Fatalf("an unanswered OpenCode session earned a row: %+v", row)
		}
	}
	if output.SuppressedCount != 1 {
		t.Fatalf("SuppressedCount = %d, want 1 for the unanswered session", output.SuppressedCount)
	}
}

// An OpenCode session with at least one prompt AND at least one assistant
// reply is a real, answered chat and must be shown.
func TestOpenCodeSessionWithAssistantReplyIsShown(t *testing.T) {
	output := Compose(Input{
		OpenCodeSessions: []store.OpenCodeSession{
			{
				ID: "ses_answered", Title: "answered", Directory: "/work/a", ProjectDir: "/work/a",
				TimeUpdatedMS: 1, PromptCount: 1, AssistantCount: 1,
			},
		},
		Options: Options{View: DefaultView},
	})
	found := false
	for _, row := range output.Rows {
		if row.ID == "ses_answered" {
			found = true
			if row.PromptCount != 1 {
				t.Fatalf("answered session PromptCount = %d, want 1 (never fudged)", row.PromptCount)
			}
		}
	}
	if !found {
		t.Fatalf("an answered OpenCode session was suppressed: %+v", output)
	}
	if output.SuppressedCount != 0 {
		t.Fatalf("SuppressedCount = %d, want 0 for the answered session", output.SuppressedCount)
	}
}

func TestOpenCodeKilledSessionsAreOmittedAndCounted(t *testing.T) {
	killedAt := int64(12345)
	output := Compose(Input{
		OpenCodeSessions: []store.OpenCodeSession{
			{ID: "ses_dead", Title: "dead", Directory: "/work/x", ProjectDir: "/work/x", TimeUpdatedMS: 1},
			{ID: "ses_alive", Title: "alive", Directory: "/work/y", ProjectDir: "/work/y", TimeUpdatedMS: 2},
		},
		Killed:  []store.Killed{{ID: "ses_dead", KilledAt: killedAt}},
		Options: Options{View: AllView},
	})
	for _, row := range output.Rows {
		if row.ID == "ses_dead" && !row.Killed {
			t.Fatalf("killed oc session rendered as alive: %+v", row)
		}
	}
	if output.KilledCount != 1 {
		t.Fatalf("KilledCount = %d, want 1", output.KilledCount)
	}
}
