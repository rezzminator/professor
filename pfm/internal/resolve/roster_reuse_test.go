package resolve

import (
	"context"
	"strings"
	"testing"
)

// TestResolveRosterNameDeadOnlyMeansTheNewestDeadRow pins rule 2: two dead
// rows under one reused name are not an ambiguity — the newest is the chat
// the name means, and it comes back not live so the caller reports it dead.
func TestResolveRosterNameDeadOnlyMeansTheNewestDeadRow(t *testing.T) {
	got, found, err := ResolveRosterName([]RosterCandidate{
		{Name: "PFM_SYNC", ID: "old", ActivityNS: 100},
		{Name: "PFM_SYNC", ID: "new", ActivityNS: 200},
	}, "PFM_SYNC")
	if err != nil || !found || got.ID != "new" || got.Live {
		t.Fatalf("ResolveRosterName()=(%+v,%t,%v), want the newest dead row, not live", got, found, err)
	}
}

// TestResolveRosterNameDeadTieStaysAmbiguousAndListsBoth pins the honest
// edge of rule 2: dead rows tied for newest cannot be told apart, so the
// refusal names every one of them rather than guessing.
func TestResolveRosterNameDeadTieStaysAmbiguousAndListsBoth(t *testing.T) {
	_, found, err := ResolveRosterName([]RosterCandidate{
		{Name: "seat", ID: "one", ActivityNS: 7},
		{Name: "seat", ID: "two", ActivityNS: 7},
	}, "seat")
	if found || err == nil || !strings.Contains(err.Error(), "one") || !strings.Contains(err.Error(), "two") {
		t.Fatalf("ResolveRosterName() found=%t err=%v, want a tie listing both", found, err)
	}
}

// TestResolveRosterNameLiveCollisionListsOnlyTheLiveSeats pins rule 3: two
// live seats under one name stay ambiguous, and a newer dead row neither
// breaks the tie nor pads the candidate list.
func TestResolveRosterNameLiveCollisionListsOnlyTheLiveSeats(t *testing.T) {
	_, found, err := ResolveRosterName([]RosterCandidate{
		{Name: "seat", ID: "one", Socket: "cx-1", Pane: "%1", Live: true, ActivityNS: 1},
		{Name: "seat", ID: "two", Socket: "cx-2", Pane: "%2", Live: true, ActivityNS: 2},
		{Name: "seat", ID: "gone", ActivityNS: 9},
	}, "seat")
	if found || err == nil {
		t.Fatalf("ResolveRosterName() found=%t err=%v, want the live collision refused", found, err)
	}
	if !strings.Contains(err.Error(), "matches 2 chats") || strings.Contains(err.Error(), "gone") {
		t.Fatalf("ambiguity=%q, want exactly the two live seats", err)
	}
}

// TestLadderMissCarriesTheRosterDeadDetail pins the inject doors (CLI and MCP
// chat_inject, chat_resolve): when every rung misses, the roster's account of
// a dead match is the answer, never the bare "matched no live chat".
func TestLadderMissCarriesTheRosterDeadDetail(t *testing.T) {
	const dead = `"probe" matched no live chat; its newest match is dead: thread id t-2`
	ladder := Ladder{
		Roster: RosterFunc(func(context.Context, string, string) (Seat, int, string, error) {
			return Seat{}, CodeUnknown, dead, nil
		}),
		Raw: &ladderRaw{outcomes: map[Kind]Outcome{
			Session: {Code: CodeUnknown}, Label: {Code: CodeUnknown}, CxWindow: {Code: CodeUnknown},
		}},
	}
	_, code, detail, err := ladder.Resolve(context.Background(), "probe", LadderOptions{})
	if err != nil || code != CodeUnknown || detail != dead {
		t.Fatalf("Resolve() code=%d detail=%q err=%v, want the roster's dead detail", code, detail, err)
	}
}
