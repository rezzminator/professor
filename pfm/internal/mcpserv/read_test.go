package mcpserv

import (
	"testing"

	"github.com/rezzminator/professor/pfm/internal/transcript"
)

// TestBoundTurnsCarriesToolCalls is a REGRESSION test for chat_read over a
// stretch of tool work: transcript.Entry carries a tool call as Tool + Input
// with an empty Text, and boundTurns copied only Text — so a read of the four
// newest turns returned four {"role":"tool","text":""} blanks and bytes=0,
// which reads as a chat that said nothing rather than one that called tools.
func TestBoundTurnsCarriesToolCalls(t *testing.T) {
	entries := []transcript.Entry{
		{Role: transcript.RoleUser, Text: "run the gate", Timestamp: "2026-01-01T00:00:00Z"},
		{Role: transcript.RoleTool, Tool: "Bash", Input: "dev.sh verify pfm", Timestamp: "2026-01-01T00:00:01Z"},
		{Role: transcript.RoleAssistant, Text: "green", Timestamp: "2026-01-01T00:00:02Z"},
	}
	turns, bytes, truncated := boundTurns(entries, 1<<10)
	if truncated {
		t.Fatalf("boundTurns(budget 1KiB) truncated = true, want the three turns to fit")
	}
	want := []Turn{
		{Role: transcript.RoleUser, Text: "run the gate", Timestamp: "2026-01-01T00:00:00Z"},
		{
			Role:      transcript.RoleTool,
			Tool:      "Bash",
			Text:      "dev.sh verify pfm",
			Timestamp: "2026-01-01T00:00:01Z",
		},
		{Role: transcript.RoleAssistant, Text: "green", Timestamp: "2026-01-01T00:00:02Z"},
	}
	if len(turns) != len(want) {
		t.Fatalf("boundTurns() = %+v, want %+v", turns, want)
	}
	for index, turn := range turns {
		if turn != want[index] {
			t.Fatalf("turn %d = %+v, want %+v", index, turn, want[index])
		}
	}
	if wantBytes := len("run the gate") + len("dev.sh verify pfm") + len("green"); bytes != wantBytes {
		t.Fatalf("boundTurns() bytes = %d, want %d (the tool input counts against the budget)", bytes, wantBytes)
	}
}

// TestBoundTurnsCutsAToolInputToTheRemainingBudget pins that the byte budget
// applies to whatever text a turn emits, the tool input included, and that the
// cut turn is kept rather than dropped.
func TestBoundTurnsCutsAToolInputToTheRemainingBudget(t *testing.T) {
	entries := []transcript.Entry{
		{Role: transcript.RoleTool, Tool: "Read", Input: "0123456789", Timestamp: "2026-01-01T00:00:00Z"},
	}
	turns, bytes, truncated := boundTurns(entries, 4)
	if len(turns) != 1 || !truncated {
		t.Fatalf("boundTurns(budget 4) = %+v, truncated=%v; want the one turn kept and cut", turns, truncated)
	}
	if turns[0].Tool != "Read" || len(turns[0].Text) > 4 || turns[0].Text == "" {
		t.Fatalf("cut tool turn = %+v, want the tool named and its input cut to 4 bytes", turns[0])
	}
	if bytes != len(turns[0].Text) {
		t.Fatalf("boundTurns() bytes = %d, want %d", bytes, len(turns[0].Text))
	}
}
