package compose

import (
	"testing"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
)

// TestBuildCosmosLiveDropsEventMatchedRowsThatAreNotLive pins the invariant
// the file states about the live sky — a node the picker would not list as
// live is not in the graph — against the event-matched path: an identity a
// comms event resolves to a row that is resumable, killed, or a BG job is
// positive evidence that the chat has no live pane, so every inject or spawn
// inside the window between such chats must not resurrect them as live
// stars. Replay keeps them, edges and all, as the ghosts they were.
func TestBuildCosmosLiveDropsEventMatchedRowsThatAreNotLive(t *testing.T) {
	rows := []Row{
		{Kind: LiveClaude, ID: "live-id", Name: "Live", Socket: "cc-live", PaneID: "%1", ActivityNS: 5},
		{Kind: ResumeClaude, ID: "resumable-id", Name: "Resumable", ActivityNS: 5},
		{Kind: ResumeCodex, ID: "resumable-codex-id", Name: "ResumableCodex", ActivityNS: 5},
		{Kind: LiveClaude, ID: "bg-id", Name: "BGLive", Socket: "cc-bg", PaneID: "%2", BG: true, ActivityNS: 5},
	}
	events := []fleetdb.CommsEvent{
		{AtNS: 10, Kind: fleetdb.KindCall, SenderUUID: "resumable-id", Target: "resumable-codex-id", Message: "a"},
		{AtNS: 11, Kind: fleetdb.KindCall, SenderUUID: "live-id", Target: "resumable-id", Message: "b"},
		{AtNS: 12, Kind: fleetdb.KindSpawn, SenderUUID: "live-id", Target: "bg-id", Message: "c"},
	}

	live := BuildCosmos(rows, events, 100, true)
	if len(live.Nodes) != 1 || live.Nodes[0].Key != "chat:id:live-id" {
		t.Fatalf("live nodes = %#v, want only chat:id:live-id: a resumable or BG row is not a live star", live.Nodes)
	}
	if len(live.Edges) != 0 {
		t.Fatalf("live edges = %#v, want none: every edge names a node the live sky dropped", live.Edges)
	}

	replay := BuildCosmos(rows, events, 100, false)
	for _, key := range []string{"chat:id:live-id", "chat:id:resumable-id", "chat:id:resumable-codex-id"} {
		if _, found := nodeWithKey(replay, key); !found {
			t.Fatalf("replay dropped %s, want the ghost it was: %#v", key, replay.Nodes)
		}
	}
	if len(replay.Edges) < 2 {
		t.Fatalf("replay edges = %#v, want the recorded traffic kept", replay.Edges)
	}
}
