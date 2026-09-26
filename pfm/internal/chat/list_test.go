package chat

import (
	"testing"

	"github.com/rezzminator/professor/pfm/internal/compose"
)

// TestListNeverListsThePickerPlaceholders pins which rows chat_ls drops: only
// the picker's "start a new chat" actions, which have no chat behind them. A
// Booting row is a real chat with a live pane — it answers inject by name — so
// it is listed, never reported absent for its first minute.
func TestListNeverListsThePickerPlaceholders(t *testing.T) {
	for _, kind := range []compose.Kind{compose.NewClaude, compose.NewCodex, compose.NewOpenCode} {
		if !placeholderRow(kind) {
			t.Errorf("placeholder kind %s would be listed", kind)
		}
	}
	for _, kind := range []compose.Kind{
		compose.Booting, compose.LiveClaude, compose.LiveCodex, compose.LiveSplit, compose.Agent,
		compose.ResumeClaude, compose.ResumeCodex, compose.ResumeOpenCode,
	} {
		if placeholderRow(kind) {
			t.Errorf("real row kind %s would be dropped", kind)
		}
	}
}

// TestSelectLiveOnlyDropsKilledAndResumableRows pins the CLI's live listing:
// a killed live chat (by tombstone or by name) is counted, never listed, and a
// resumable row is neither.
func TestSelectLiveOnlyDropsKilledAndResumableRows(t *testing.T) {
	rows := []compose.Row{
		{Kind: compose.LiveClaude, ID: "live", CWD: "/work/alpha"},
		{Kind: compose.LiveClaude, ID: "killed", CWD: "/work/alpha", Killed: true},
		{Kind: compose.LiveCodex, ID: "name-killed", CWD: "/work/alpha", NameKilled: true},
		{Kind: compose.ResumeClaude, ID: "resumable", CWD: "/work/alpha"},
	}
	result := Select(rows, 9, ListRequest{LiveOnly: true})
	if len(result.Rows) != 1 || result.Rows[0].ID != "live" || result.KilledCount != 2 {
		t.Fatalf("live-only select = %+v, want only the live row and 2 killed", result)
	}
	if every := Select(rows, 9, ListRequest{}); len(every.Rows) != 4 || every.KilledCount != 9 {
		t.Fatalf(
			"unfiltered select = %d rows killed %d, want 4 rows and the scan's tally",
			len(every.Rows),
			every.KilledCount,
		)
	}
}
