package chat

import "testing"

// "killed <id>" alone cannot tell a closed pane from a row that was merely
// de-listed, and the socket-keyed OpenCode case is a third outcome again: the
// pane closes and NOTHING is recorded. Each one says which it was, on stdout,
// where every surface — CLI, MCP — reads it.
func TestKillOutcomeNamesWhatTheKillActuallyDid(t *testing.T) {
	closed := KillOutcome("ses_live", "ox-1700000000-42-7", "%0", true)
	if closed != "killed ses_live\tclosing pane %0 on socket ox-1700000000-42-7" {
		t.Fatalf("closed outcome = %q", closed)
	}
	delisted := KillOutcome("ses_cold", "", "", true)
	if delisted != "killed ses_cold\tde-listed only, no live pane closed" {
		t.Fatalf("de-listed outcome = %q", delisted)
	}
	socketKeyed := KillOutcome("ox-1700000000-42-7", "ox-1700000000-42-7", "%0", false)
	want := "closed ox-1700000000-42-7\tclosing pane %0 on socket ox-1700000000-42-7" +
		" — no kill recorded: this seat answers only to its socket"
	if socketKeyed != want {
		t.Fatalf("socket-keyed outcome = %q, want %q", socketKeyed, want)
	}
	// A recorded kill with only half an address is still a de-list: naming a
	// pane nothing was sent to would be the same lie in a new place.
	if half := KillOutcome("ses_live", "ox-1700000000-42-7", "", true); half !=
		"killed ses_live\tde-listed only, no live pane closed" {
		t.Fatalf("half-addressed outcome = %q", half)
	}
}
