package hookentry

import (
	"testing"
)

// An undecodable stored state is named and rewritten by the next event, never
// left to drop every later report of the session.
func TestHerdrStateRewritesAnUndecodableStoredState(t *testing.T) {
	rig := newChatRig(t)
	rig.tmux.stored = "{"
	got := rig.engine("working", `{"session_id":"s1"}`)
	want := "pfm internal herdr-state working: decode @pfm_herdr of cc-1 (\"{\"), this event rewrites it: " +
		"unexpected end of JSON input\n"
	if got.rc != 0 || got.stderr != want {
		t.Fatalf("rc=%d stderr=%q, want 0 %q", got.rc, got.stderr, want)
	}
	if state := rig.tmux.storedState(t); state.State != "working" || state.SessionID != "s1" {
		t.Fatalf("stored @pfm_herdr = %q, want the working state and session written", rig.tmux.stored)
	}
}
