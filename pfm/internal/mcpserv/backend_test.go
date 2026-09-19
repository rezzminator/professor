package mcpserv

import (
	"testing"

	"hostops/pfm/internal/compose"
)

// TestChatRowStateNamesTheKilledButLiveContradiction is the honesty half of the
// kill regression. A kill that closed nothing still wrote its tombstone row, so
// chat_ls handed back a row asserting BOTH things at once — killed:true beside
// state "idle" over a live kind — and a caller had no way to tell a real kill
// from a de-listing. The state names the contradiction instead of collapsing
// it into either half.
func TestChatRowStateNamesTheKilledButLiveContradiction(t *testing.T) {
	for _, kind := range []compose.Kind{
		compose.LiveClaude,
		compose.LiveCodex,
		compose.LiveOpenCode,
		compose.LiveSplit,
		compose.Agent,
		compose.Booting,
	} {
		row := compose.Row{Kind: kind, Killed: true}
		if state := chatRowState(row); state != "killed-but-live" {
			t.Errorf(
				"killed %s row reports state %q, want %q",
				kind, state, "killed-but-live",
			)
		}
	}
}

// TestChatRowStateKeepsEveryOtherVerdict pins the arms the contradiction check
// must not have swallowed: an unkilled live row is idle, a booting row says so,
// a resumable row stays resumable, and a killed row that is NOT live is an
// ordinary de-listed row with nothing to contradict.
func TestChatRowStateKeepsEveryOtherVerdict(t *testing.T) {
	cases := []struct {
		name string
		row  compose.Row
		want string
	}{
		{"live claude", compose.Row{Kind: compose.LiveClaude}, "idle"},
		{"live codex", compose.Row{Kind: compose.LiveCodex}, "idle"},
		{"live opencode", compose.Row{Kind: compose.LiveOpenCode}, "idle"},
		{"booting", compose.Row{Kind: compose.Booting}, "booting"},
		{"resumable", compose.Row{Kind: compose.ResumeClaude}, "resumable"},
		{
			"killed resumable",
			compose.Row{Kind: compose.ResumeClaude, Killed: true},
			"resumable",
		},
	}
	for _, testCase := range cases {
		if state := chatRowState(testCase.row); state != testCase.want {
			t.Errorf(
				"%s: state = %q, want %q",
				testCase.name, state, testCase.want,
			)
		}
	}
}
