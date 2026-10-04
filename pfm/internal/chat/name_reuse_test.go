package chat

import (
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/compose"
	"github.com/rezzminator/professor/pfm/internal/inject"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// TestMatchPrefersTheLiveSeatOverAKilledOne pins rule 1 at every door that
// resolves through Match (status CLI and MCP chat_status, watch, kill, open):
// a killed seat whose pane the roster still lists is dead to its name, so the
// live chat sharing it is the answer, not an ambiguity.
func TestMatchPrefersTheLiveSeatOverAKilledOne(t *testing.T) {
	rows := []compose.Row{
		{
			Kind:       compose.LiveClaude,
			ID:         "killed-id",
			Name:       "probe",
			Socket:     "cc-1",
			PaneID:     "%0",
			Killed:     true,
			ActivityNS: 9,
		},
		{Kind: compose.LiveClaude, ID: "live-id", Name: "probe", Socket: "cc-2", PaneID: "%0", ActivityNS: 1},
	}
	chat, found, err := Match(rows, "probe")
	if err != nil || !found || chat.ID != "live-id" || !chat.Live {
		t.Fatalf("Match()=(%+v,%t,%v), want the live seat", chat, found, err)
	}
}

// TestMatchAfterKillTheNameBelongsToTheNewChat pins rule 4: `chat kill X`
// tombstones X, and `chat new --name X` takes the name — the booting chat is
// what X means to its own await and to every later verb.
func TestMatchAfterKillTheNameBelongsToTheNewChat(t *testing.T) {
	rows := []compose.Row{
		{
			Kind:       compose.LiveCodex,
			ID:         "old-thread",
			Name:       "X",
			Socket:     "cx-1",
			PaneID:     "%0",
			Killed:     true,
			ActivityNS: 5,
		},
		{Kind: compose.ResumeCodex, ID: "old-thread", Name: "X", Killed: true, ActivityNS: 5},
		{Kind: compose.Booting, Name: "X", Socket: "cx-2", SessionName: "cx-2", ActivityNS: 6},
	}
	chat, found, err := Match(rows, "X")
	if err != nil || !found || chat.Socket != "cx-2" || !chat.Live {
		t.Fatalf("Match()=(%+v,%t,%v), want the new chat that took the name", chat, found, err)
	}
}

// TestRosterTargetNamesTheNewestDeadMatch pins rule 2 at the inject doors
// (CLI and MCP chat_inject): a name only dead rows answer to is a roster miss
// that names the newest dead chat and says it is dead.
func TestRosterTargetNamesTheNewestDeadMatch(t *testing.T) {
	testjail.Fleet(t)
	rows := []compose.Row{
		{Kind: compose.ResumeClaude, ID: "old-id", Name: "PFM_SYNC", ActivityNS: 1},
		{Kind: compose.ResumeClaude, ID: "new-id", Name: "PFM_SYNC", ActivityNS: 2},
	}
	_, code, detail, err := rosterTarget(resolvedPaths(t), rows, "", "PFM_SYNC")
	if err != nil || code != inject.CodeUnknown || !strings.Contains(detail, "is dead") ||
		!strings.Contains(detail, "new-id") || strings.Contains(detail, "old-id") {
		t.Fatalf("rosterTarget()=(%d,%q,%v), want a miss naming the newest dead chat", code, detail, err)
	}
}
