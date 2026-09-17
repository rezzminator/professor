package resolve

import (
	"strings"
	"testing"
)

func TestResolveRosterNamePrefersTheUniqueLiveRow(t *testing.T) {
	live := RosterCandidate{Name: "seat", ID: "thread", Socket: "cx-1-2-3", Live: true}
	got, found, err := ResolveRosterName([]RosterCandidate{
		{Name: "seat", ID: "thread"}, live,
	}, "seat")
	if err != nil || !found || got != live {
		t.Fatalf("ResolveRosterName()=(%+v,%t,%v), want live row", got, found, err)
	}
}

func TestResolveRosterNameAmbiguityNamesStableAddresses(t *testing.T) {
	_, found, err := ResolveRosterName([]RosterCandidate{
		{Name: "seat", ID: "one", Socket: "cx-1", Pane: "%1", Live: true},
		{Name: "seat", ID: "two", Socket: "cx-2", Pane: "%2", Live: true},
	}, "seat")
	if found || err == nil {
		t.Fatalf("ResolveRosterName() found=%t err=%v, want ambiguity", found, err)
	}
	for _, want := range []string{"thread id", "one", "two", "cx-1", "cx-2"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("ambiguity=%q, want %q", err, want)
		}
	}
}

// TestResolveRosterSeatNamesTheSeatByIDThenBySeat pins the reverse lookup a
// delivery footer is built from: the thread id is authoritative, the seat
// (socket plus pane) answers when the identity has no id, and the name that
// comes back is exactly what ResolveRosterName's exact rung would take.
func TestResolveRosterSeatNamesTheSeatByIDThenBySeat(t *testing.T) {
	roster := []RosterCandidate{
		{
			Name:   "LUNA:ORCHESTRATOR",
			ID:     "5a3bb7cb-258d",
			Socket: "cc-1788256324-1866070-42739",
			Pane:   "%0",
			Live:   true,
		},
		{Name: "LUNA:BUILDER", ID: "builder", Socket: "cx-1-2-3", Pane: "%1", Live: true},
		{Name: "LUNA:BUILDER", ID: "builder", Socket: "cx-4-5-6", Pane: "%1", Live: true},
		{Name: "Retired", ID: "5a3bb7cb-258d", Socket: "cc-old", Pane: "%0"},
	}
	byID, found := ResolveRosterSeat(
		roster,
		Identity{ID: "5a3bb7cb-258d", SocketName: "cc-elsewhere", Pane: "%9"},
	)
	if !found || byID != "LUNA:ORCHESTRATOR" {
		t.Fatalf("by id = (%q,%t), want the live row's name", byID, found)
	}
	bySeat, found := ResolveRosterSeat(
		roster,
		Identity{SocketPath: "/tmp/tmux-1000/cc-1788256324-1866070-42739", Pane: "%0"},
	)
	if !found || bySeat != "LUNA:ORCHESTRATOR" {
		t.Fatalf("by seat = (%q,%t), want the seat's name", bySeat, found)
	}
	twoServers, found := ResolveRosterSeat(roster, Identity{ID: "builder"})
	if !found || twoServers != "LUNA:BUILDER" {
		t.Fatalf("two servers for one chat = (%q,%t), want their one agreed name", twoServers, found)
	}
	if name, found := ResolveRosterSeat(roster, Identity{ID: "nobody"}); found || name != "" {
		t.Fatalf("unknown id = (%q,%t), want not found", name, found)
	}
	if name, found := ResolveRosterSeat(
		roster,
		Identity{SocketName: "cc-1788256324-1866070-42739", Pane: "%7"},
	); found ||
		name != "" {
		t.Fatalf("wrong pane on a known socket = (%q,%t), want not found", name, found)
	}
	if name, found := ResolveRosterSeat(nil, Identity{ID: "5a3bb7cb-258d"}); found || name != "" {
		t.Fatalf("empty roster = (%q,%t), want not found", name, found)
	}
}

// TestResolveRosterSeatRefusesNamesAPeerCouldNotReplyTo keeps a socket, the
// unnamed sentinel, and two rows that disagree out of the answer: each would
// be advertised as a reply address and resolve to nothing, or to the wrong
// chat, so "no name" is the honest answer.
func TestResolveRosterSeatRefusesNamesAPeerCouldNotReplyTo(t *testing.T) {
	cases := map[string][]RosterCandidate{
		"machine address as name": {{Name: "cc-1787705979-3980493-30867", ID: "x", Live: true}},
		"unnamed sentinel":        {{Name: "(unnamed)", ID: "x", Live: true}},
		"blank name":              {{Name: "   ", ID: "x", Live: true}},
		"rows disagree": {
			{Name: "ONE", ID: "x", Live: true},
			{Name: "TWO", ID: "x", Live: true},
		},
	}
	for label, roster := range cases {
		if name, found := ResolveRosterSeat(roster, Identity{ID: "x"}); found || name != "" {
			t.Fatalf("%s: = (%q,%t), want not found", label, name, found)
		}
	}
}
