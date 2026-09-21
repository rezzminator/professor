package compose

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/resolve"
)

// TestBuildCosmosResolvesParticipantsAndAggregatesEdges pins the current
// user ruling directly against the old one it replaced: a traffic-free LIVE
// row (Quiet) now IS a cosmos node, seeded by BuildCosmos's live-row walk
// before events are ever considered — the previous law, asserted by this
// same test until this change, was the exact opposite ("traffic-free live
// row became a cosmos node" was a FAILURE message).
//
// Every row here also carries its own ActivityNS, deliberately EARLIER than
// where its events end up, so the three chatty rows' final LastNS proves the
// event walk still wins once real traffic is newer than the seed — the seed
// is a floor, never a ceiling — while Quiet, which gets no events at all,
// keeps exactly its seeded ActivityNS (45) rather than falling back to
// nowNS (100): that distinguishes "the seed timestamp was asserted" from
// "the fallback happened to also produce a plausible number".
func TestBuildCosmosResolvesParticipantsAndAggregatesEdges(t *testing.T) {
	rows := []Row{
		{Kind: LiveClaude, ID: "alpha-id", Name: "Alpha", Socket: "cc-alpha", PaneID: "%1", ActivityNS: 5},
		{Kind: LiveCodex, ID: "beta-id", Name: "Beta", Socket: "cx-beta", PaneID: "%2", ActivityNS: 15},
		{Kind: LiveClaude, ID: "child-id", Name: "Child", Socket: "cc-child", PaneID: "%3", ActivityNS: 30},
		{Kind: LiveClaude, ID: "quiet-id", Name: "Quiet", Socket: "cc-quiet", PaneID: "%4", ActivityNS: 45},
	}
	events := []fleetdb.CommsEvent{
		{
			AtNS:           10,
			Kind:           fleetdb.KindInject,
			SenderUUID:     "alpha-id",
			SenderLabel:    "stale alpha",
			Target:         "Beta",
			ReceiverSocket: "cx-beta",
			ReceiverPane:   "%2",
			Message:        "first",
		},
		{
			AtNS:           20,
			Kind:           fleetdb.KindInject,
			SenderSession:  "alpha-id",
			Target:         "Beta",
			ReceiverSocket: "cx-beta",
			ReceiverPane:   "%2",
			Message:        "second",
		},
		{
			AtNS:           40,
			Kind:           fleetdb.KindSpawn,
			SenderSession:  "beta-id",
			Target:         "Child",
			ReceiverSocket: "cc-child",
			Message:        "initial prompt",
		},
	}

	graph := BuildCosmos(rows, events, 100, true)
	if graph.Err != "" || len(graph.Warnings) != 0 {
		t.Fatalf("BuildCosmos() state = err %q warnings %v", graph.Err, graph.Warnings)
	}
	wantNodes := []CosmosNode{
		{
			Key:    "chat:id:alpha-id",
			Label:  resolve.Named("Alpha"),
			Engine: "cc",
			RowKey: "alpha-id",
			Live:   true,
			LastNS: 20,
		},
		{Key: "chat:id:beta-id", Label: resolve.Named("Beta"), Engine: "cx", RowKey: "beta-id", Live: true, LastNS: 40},
		{
			Key:    "chat:id:child-id",
			Label:  resolve.Named("Child"),
			Engine: "cc",
			RowKey: "child-id",
			Live:   true,
			LastNS: 40,
		},
		{
			Key:    "chat:id:quiet-id",
			Label:  resolve.Named("Quiet"),
			Engine: "cc",
			RowKey: "quiet-id",
			Live:   true,
			LastNS: 45,
		},
	}
	if !reflect.DeepEqual(graph.Nodes, wantNodes) {
		t.Fatalf("nodes = %#v, want %#v", graph.Nodes, wantNodes)
	}
	wantEdges := []CosmosEdge{
		{
			From:        "chat:id:beta-id",
			To:          "chat:id:child-id",
			Kind:        fleetdb.KindSpawn,
			Count:       1,
			LastNS:      40,
			LastMessage: "initial prompt",
		},
		{
			From:        "chat:id:alpha-id",
			To:          "chat:id:beta-id",
			Kind:        fleetdb.KindInject,
			Count:       2,
			LastNS:      20,
			LastMessage: "second",
		},
	}
	if !reflect.DeepEqual(graph.Edges, wantEdges) {
		t.Fatalf("edges = %#v, want %#v", graph.Edges, wantEdges)
	}
}

// TestBuildCosmosSeedLiveSkipsNonLiveKilledAndBGRows pins the three ways a
// row is excluded from live seeding even though it is traffic-free exactly
// like Quiet above: a RESUMABLE row carries no live pane at all (liveRow
// false), a KILLED live row is positively known gone, and a BG live row is
// deliberately excluded from the picker's default view — none of the three
// earns a node from the live seed, and with no events either, BuildCosmos
// must produce an entirely empty graph.
func TestBuildCosmosSeedLiveSkipsNonLiveKilledAndBGRows(t *testing.T) {
	rows := []Row{
		{Kind: ResumeClaude, ID: "resumable-id", Name: "Resumable", ActivityNS: 5},
		{
			Kind:       LiveClaude,
			ID:         "killed-id",
			Name:       "KilledLive",
			Socket:     "cc-killed",
			PaneID:     "%1",
			Killed:     true,
			ActivityNS: 5,
		},
		{Kind: LiveClaude, ID: "bg-id", Name: "BGLive", Socket: "cc-bg", PaneID: "%2", BG: true, ActivityNS: 5},
	}
	graph := BuildCosmos(rows, nil, 100, true)
	if len(graph.Nodes) != 0 {
		t.Fatalf(
			"nodes = %#v, want none: a resumable row, a killed live row, and a BG live row must none of them seed",
			graph.Nodes,
		)
	}
}

// TestBuildCosmosSeedLiveFalseSeedsNothing pins the replay contract: the
// exact same rows that TestBuildCosmosResolvesParticipantsAndAggregatesEdges
// proves DO seed under live=true must seed NOTHING under live=false — the
// shape the chronoscope replay path always calls with, because the past is
// the ledger of what happened, not today's roster of who is currently
// sitting at a pane.
func TestBuildCosmosSeedLiveFalseSeedsNothing(t *testing.T) {
	rows := []Row{
		{Kind: LiveClaude, ID: "quiet-id", Name: "Quiet", Socket: "cc-quiet", PaneID: "%1", ActivityNS: 5},
		{Kind: LiveSplit, Name: "split", Socket: "cc-split", SplitCount: 2, ActivityNS: 5},
	}
	graph := BuildCosmos(rows, nil, 100, false)
	if len(graph.Nodes) != 0 {
		t.Fatalf("nodes = %#v, want none: live=false must not seed a node even for live rows", graph.Nodes)
	}
}

// TestBuildCosmosSeededRowMergesWithLaterEventOnSameRowKey pins the merge
// rule directly: a seeded row that also matches an event by RowKey must
// converge on exactly ONE node (same key), never a duplicate, with
// LastNS = max(row.ActivityNS, event.AtNS) — proved in both directions,
// since builder.touch only ever advances a node's clock forward and never
// backward.
func TestBuildCosmosSeededRowMergesWithLaterEventOnSameRowKey(t *testing.T) {
	t.Run("event newer than the seed wins", func(t *testing.T) {
		rows := []Row{{Kind: LiveClaude, ID: "dual-id", Name: "Dual", Socket: "cc-dual", PaneID: "%1", ActivityNS: 10}}
		events := []fleetdb.CommsEvent{
			{AtNS: 50, Kind: fleetdb.KindInject, SenderUUID: "dual-id", Target: "Someone", Message: "hi"},
		}
		graph := BuildCosmos(rows, events, 100, true)
		matches := 0
		for _, node := range graph.Nodes {
			if node.Key != "chat:id:dual-id" {
				continue
			}
			matches++
			if node.LastNS != 50 {
				t.Fatalf("LastNS = %d, want the event's AtNS 50 (newer than the seed's ActivityNS 10)", node.LastNS)
			}
		}
		if matches != 1 {
			t.Fatalf("dual-id produced %d node(s), want exactly 1: %#v", matches, graph.Nodes)
		}
	})
	t.Run("seed newer than the event wins", func(t *testing.T) {
		rows := []Row{{Kind: LiveClaude, ID: "dual-id", Name: "Dual", Socket: "cc-dual", PaneID: "%1", ActivityNS: 80}}
		events := []fleetdb.CommsEvent{
			{AtNS: 30, Kind: fleetdb.KindInject, SenderUUID: "dual-id", Target: "Someone", Message: "hi"},
		}
		graph := BuildCosmos(rows, events, 100, true)
		matches := 0
		for _, node := range graph.Nodes {
			if node.Key != "chat:id:dual-id" {
				continue
			}
			matches++
			if node.LastNS != 80 {
				t.Fatalf(
					"LastNS = %d, want the seed's ActivityNS 80 (newer than the event's AtNS 30 — touch never moves backward)",
					node.LastNS,
				)
			}
		}
		if matches != 1 {
			t.Fatalf("dual-id produced %d node(s), want exactly 1: %#v", matches, graph.Nodes)
		}
	})
}

// TestBuildCosmosSeedLiveStarsQuietProjectWithZeroTraffic pins the star
// assembly's promise for a seeded-only home: a quiet live row in a project
// with no events at all still earns a Stars entry (every distinct Home
// among the built nodes gets one, full stop — the same rule
// TestBuildCosmosStarsCountTrafficOncePerHomeInsideTheHour pins for
// event-warmed homes), with Home set and TrafficHour == 0 because
// builder.warm — the only thing that increments TrafficHour — is never
// called by the seeding loop; only the event walk warms.
func TestBuildCosmosSeedLiveStarsQuietProjectWithZeroTraffic(t *testing.T) {
	rows := []Row{
		{
			Kind:       LiveClaude,
			ID:         "solo-id",
			Name:       "Solo",
			Socket:     "cc-solo",
			PaneID:     "%1",
			Project:    "solohome",
			ActivityNS: 10,
		},
	}
	graph := BuildCosmos(rows, nil, 100, true)
	star, ok := graph.Stars["solohome"]
	if !ok {
		t.Fatalf("Stars missing entry for solohome: %#v", graph.Stars)
	}
	if star.Home != "solohome" {
		t.Fatalf("star.Home = %q, want %q", star.Home, "solohome")
	}
	if star.TrafficHour != 0 {
		t.Fatalf("star.TrafficHour = %d, want 0: seeding a node never warms a star", star.TrafficHour)
	}
}

// TestBuildCosmosSeedLiveFallsBackToNowNSWhenActivityIsZero pins the
// documented fallback explicitly: a live row with no ActivityNS at all
// (the zero value — a row whose activity clock was never populated) seeds
// its node at nowNS, not at 0, which would otherwise read as a chat
// billions of nanoseconds dead the instant it was seeded.
func TestBuildCosmosSeedLiveFallsBackToNowNSWhenActivityIsZero(t *testing.T) {
	rows := []Row{{Kind: LiveClaude, ID: "zero-id", Name: "Zero", Socket: "cc-zero", PaneID: "%1", ActivityNS: 0}}
	graph := BuildCosmos(rows, nil, 555, true)
	node, found := nodeWithKey(graph, "chat:id:zero-id")
	if !found {
		t.Fatalf("seeded node missing: %#v", graph.Nodes)
	}
	if node.LastNS != 555 {
		t.Fatalf("LastNS = %d, want nowNS 555 (ActivityNS was 0, the documented fallback)", node.LastNS)
	}
}

func TestBuildCosmosParentlessSpawnCreatesOnlyTheChildNode(t *testing.T) {
	graph := BuildCosmos(nil, []fleetdb.CommsEvent{{
		AtNS: 60, Kind: fleetdb.KindSpawn, Target: "Orphan",
		ReceiverSocket: "cx-orphan", Message: "born",
	}}, 100, false)
	if len(graph.Edges) != 0 {
		t.Fatalf("parentless spawn edges = %#v", graph.Edges)
	}
	want := []CosmosNode{{
		Key: "chat:session:cx-orphan", Label: resolve.Named("Orphan"), LastNS: 60,
	}}
	if !reflect.DeepEqual(graph.Nodes, want) {
		t.Fatalf("parentless nodes = %#v, want %#v", graph.Nodes, want)
	}
}

func TestBuildCosmosTimestampTieKeepsNewestFirstMessage(t *testing.T) {
	graph := BuildCosmos(nil, []fleetdb.CommsEvent{
		{ID: 2, AtNS: 70, Kind: fleetdb.KindInject, SenderLabel: "Alpha", Target: "Beta", Message: "newest id"},
		{ID: 1, AtNS: 70, Kind: fleetdb.KindInject, SenderLabel: "Alpha", Target: "Beta", Message: "older id"},
	}, 100, false)
	want := []CosmosEdge{{
		From: "chat:name:Alpha", To: "chat:name:Beta", Kind: fleetdb.KindInject,
		Count: 2, LastNS: 70, LastMessage: "newest id",
	}}
	if !reflect.DeepEqual(graph.Edges, want) {
		t.Fatalf("tie edges = %#v, want %#v", graph.Edges, want)
	}
}

func TestBuildCosmosUnknownKindWarnsInsteadOfVanishingSilently(t *testing.T) {
	graph := BuildCosmos(nil, []fleetdb.CommsEvent{{ID: 9, AtNS: 80, Kind: "reload"}}, 100, false)
	if len(graph.Nodes) != 0 || len(graph.Edges) != 0 || len(graph.Warnings) != 1 ||
		!strings.Contains(graph.Warnings[0], `event 9`) ||
		!strings.Contains(graph.Warnings[0], `unknown kind "reload"`) {
		t.Fatalf("unknown-kind graph = %#v", graph)
	}
}

// TestBuildCosmosRetiredGroupKindSkipsSilently pins the retired chat-group
// feature's historical-row contract: internal/fleetdb no longer has a
// KindGroup const, but a fleet.db row recorded before the purge can still
// carry the literal string "group" forever. That is known history, not an
// unknown kind, so BuildCosmos must skip it with no node, no edge, and — on
// the orchestrator's ruling — no warning either: cosmos warnings pin to the
// UI as first-warning-plus-count, so a permanent historical row would
// otherwise drown a real "unknown kind" warning for good. The sibling test
// above, TestBuildCosmosUnknownKindWarnsInsteadOfVanishingSilently, pins the
// other half: a genuinely unknown kind still warns.
func TestBuildCosmosRetiredGroupKindSkipsSilently(t *testing.T) {
	graph := BuildCosmos(nil, []fleetdb.CommsEvent{{ID: 9, AtNS: 80, Kind: "group"}}, 100, false)
	if graph.Err != "" || len(graph.Nodes) != 0 || len(graph.Edges) != 0 || len(graph.Warnings) != 0 {
		t.Fatalf("retired group-kind graph = %#v, want a silent skip", graph)
	}
}

// TestBuildCosmosDeadChatConvergesSpawnAndInjectOnOneGhostNode is the
// regression for the three-nodes-per-chat bug: a spawn event recorded the
// receiver's tmux session as a BARE NAME (chat_new_command.go:152's pre-fix
// shape) while an inject event for the very same chat, once it has no
// surviving row to resolve against (it died), states its own identity as
// that SAME bare session name via sender_session. Before the fix these two
// facts lived in two different fallback key spaces (chat:pane:<bare-name>\t
// vs chat:id:<session>) and produced two ghosts for one chat — plus a third
// whenever a live-shaped full socket path was also seen. This asserts they
// converge on exactly one.
func TestBuildCosmosDeadChatConvergesSpawnAndInjectOnOneGhostNode(t *testing.T) {
	const deadSession = "cc-1787827285-1466858-781"
	events := []fleetdb.CommsEvent{
		{
			AtNS: 10, Kind: fleetdb.KindSpawn, SenderSession: "parent-session",
			Target: "ghost", ReceiverSocket: deadSession, // bare session name: the pre-fix writer's shape
			Message: "spawned",
		},
		{
			AtNS: 20, Kind: fleetdb.KindInject, SenderSession: deadSession,
			Target: "someone-else", ReceiverSocket: "/tmp/tmux-1000/other-session", ReceiverPane: "%0",
			Message: "the dead chat's last reply",
		},
	}
	// No rows at all: the chat has fully vanished from the roster, exactly
	// the state the operator described after killing ten test chats.
	graph := BuildCosmos(nil, events, 100, false)
	var matches []CosmosNode
	for _, node := range graph.Nodes {
		if strings.Contains(node.Key, deadSession) {
			matches = append(matches, node)
		}
	}
	if len(matches) != 1 {
		t.Fatalf("dead chat %q produced %d node(s), want exactly 1\nmatches = %#v\nfull graph.Nodes = %#v",
			deadSession, len(matches), matches, graph.Nodes)
	}
}

// TestBuildCosmosLiveChatStillYieldsOneNodeAcrossSpawnAndInject is the
// non-regression half: a chat with a surviving row must still resolve to
// exactly one node through both a spawn and an inject event, so the
// fallback fix above cannot be mistaken for a change to the live path.
func TestBuildCosmosLiveChatStillYieldsOneNodeAcrossSpawnAndInject(t *testing.T) {
	rows := []Row{
		{
			Kind:   LiveClaude,
			ID:     "ghost-id",
			Name:   "ghost",
			Socket: "/tmp/tmux-1000/cc-1787827285-1466858-781",
			PaneID: "%0",
		},
	}
	events := []fleetdb.CommsEvent{
		{
			AtNS: 10, Kind: fleetdb.KindSpawn, SenderSession: "parent-session",
			Target: "ghost", ReceiverSocket: "/tmp/tmux-1000/cc-1787827285-1466858-781", ReceiverPane: "%0",
			Message: "spawned",
		},
		{
			AtNS: 20, Kind: fleetdb.KindInject, SenderUUID: "ghost-id",
			Target: "someone-else", ReceiverSocket: "/tmp/tmux-1000/other-session", ReceiverPane: "%0",
			Message: "reply",
		},
	}
	graph := BuildCosmos(rows, events, 100, false)
	count := 0
	for _, node := range graph.Nodes {
		if node.Live && nodeLabel(node) == "ghost" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("live chat produced %d live node(s) labeled ghost, want 1\ngraph.Nodes = %#v", count, graph.Nodes)
	}
}

// TestBuildCosmosDoesNotPruneDeadNodesByTime is now two truths cut from the
// same events, since the LIVE ruling did not touch replay's promise: REPLAY
// (live=false) still refuses to prune a dead node by age — BuildCosmos has
// no memory across calls, so DiedNS (last edge activity) is the only fact
// it can honestly report, and that fact cannot answer "when did it die" — a
// chat killed after a long idle period would already be past any fixed
// window on the very first call that notices it, pruning it before a
// replay ever got to show it as the ghost it was. A dead node, and every
// edge naming it, survive REPLAY regardless of age.
//
// LIVE (live=true) is the opposite promise this change adds: the sky of
// now never shows a dead or hidden chat at all, so the exact same events —
// aged the exact same ten hours — produce no node for the dead identity
// and no edge naming it.
func TestBuildCosmosDoesNotPruneDeadNodesByTime(t *testing.T) {
	events := []fleetdb.CommsEvent{
		{
			AtNS: 0, Kind: fleetdb.KindInject, SenderLabel: "Ghost",
			Target: "Someone", Message: "long gone",
		},
	}
	// Ten hours past a zero-timestamp event would have been many orders of
	// magnitude past CosmosRowGrace under the old nowNS-based prune. It
	// changes nothing for replay here now.
	replay := BuildCosmos(nil, events, int64(10*time.Hour), false)
	if len(replay.Nodes) != 2 || len(replay.Edges) != 1 {
		t.Fatalf(
			"BuildCosmos(live=false) dropped a dead node or its edge by age: nodes=%#v edges=%#v",
			replay.Nodes,
			replay.Edges,
		)
	}
	for _, node := range replay.Nodes {
		if !node.Dead {
			t.Fatalf("node %#v should be Dead: no row survives for either fallback identity", node)
		}
		if node.DiedNS != 0 {
			t.Fatalf("node %#v DiedNS should be 0 (its only touch was AtNS 0), not a fabricated later time", node)
		}
	}

	live := BuildCosmos(nil, events, int64(10*time.Hour), true)
	if len(live.Nodes) != 0 || len(live.Edges) != 0 {
		t.Fatalf(
			"BuildCosmos(live=true) kept a dead node or its edge from the SAME events: nodes=%#v edges=%#v",
			live.Nodes,
			live.Edges,
		)
	}
}

// TestBuildCosmosKilledRowIsDroppedLiveButKeptInReplay is the LIVE half of
// the ruling for the OTHER kind of death: a matched row's Killed bool is
// positive evidence, not a grace-window guess, and needs no waiting —
// BuildCosmos(live=true) drops that node (and every edge naming it)
// outright, exactly as it drops a grace-expired unmatched identity. Replay
// keeps it as the ghost it was, Dead and edged, same as always.
func TestBuildCosmosKilledRowIsDroppedLiveButKeptInReplay(t *testing.T) {
	rows := []Row{{Kind: LiveClaude, ID: "killed-id", Name: "Killed", Socket: "cc-killed", PaneID: "%1", Killed: true}}
	events := []fleetdb.CommsEvent{{
		AtNS: 10, Kind: fleetdb.KindInject, SenderUUID: "killed-id",
		Target: "Someone", Message: "last words",
	}}

	live := BuildCosmos(rows, events, 100, true)
	if _, found := nodeWithKey(live, "chat:id:killed-id"); found {
		t.Fatalf("live graph kept the killed row's node: %#v", live.Nodes)
	}
	for _, edge := range live.Edges {
		if edge.From == "chat:id:killed-id" || edge.To == "chat:id:killed-id" {
			t.Fatalf("live graph kept an edge naming the killed row's node: %#v", live.Edges)
		}
	}

	replay := BuildCosmos(rows, events, 100, false)
	node, found := nodeWithKey(replay, "chat:id:killed-id")
	if !found {
		t.Fatalf("replay dropped the killed row's node: %#v", replay.Nodes)
	}
	if !node.Dead {
		t.Fatalf("replay node for a killed row should be Dead: %#v", node)
	}
}

// TestBuildCosmosNoRowMatchIsPendingWithinGraceThenDead pins the boundary of
// the grace window in BOTH modes: a node with no matching row is PENDING
// (Dead false) while nowNS - LastNS is within CosmosRowGrace — long enough
// for the row layer to index a freshly spawned chat — the same in live and
// in replay, since a newborn must not blink out of the live sky either. One
// tick past the grace window the two modes diverge exactly as the ruling
// says: live drops the node entirely, replay keeps it and marks it Dead. A
// matched row's Dead (row.Killed) is positive evidence and does not wait;
// this boundary only applies to the ambiguous no-row-match case.
func TestBuildCosmosNoRowMatchIsPendingWithinGraceThenDead(t *testing.T) {
	events := []fleetdb.CommsEvent{{
		AtNS: 0, Kind: fleetdb.KindInject, SenderLabel: "Newborn",
		Target: "Someone", Message: "hi",
	}}
	grace := int64(CosmosRowGrace)

	for _, mode := range []struct {
		name string
		live bool
	}{{"replay", false}, {"live", true}} {
		t.Run(mode.name, func(t *testing.T) {
			pending := BuildCosmos(nil, events, grace, mode.live)
			node, found := nodeWithKey(pending, "chat:name:Newborn")
			if !found {
				t.Fatal(
					"pending node missing from the graph entirely: both modes must show a node still inside its grace window",
				)
			}
			if node.Dead {
				t.Fatalf("node exactly at the grace boundary should still be PENDING: %#v", node)
			}

			gone := BuildCosmos(nil, events, grace+1, mode.live)
			goneNode, goneFound := nodeWithKey(gone, "chat:name:Newborn")
			if mode.live {
				if goneFound {
					t.Fatalf("live graph kept a node one tick past its own grace window: %#v", goneNode)
				}
				return
			}
			if !goneFound {
				t.Fatal("replay dropped a dead node instead of keeping it as a ghost")
			}
			if !goneNode.Dead {
				t.Fatalf("node one tick past the grace window should be Dead: %#v", goneNode)
			}
		})
	}
}

// TestBuildCosmosLiveEdgeFilterDropsOnlyTheDeadEndAndItsStar pins the live
// edge/star half of the drop: an edge between a surviving node and a
// dropped one must not draw either — the canvas has no seat left for the
// dead end — while the surviving node keeps whatever LastNS the ordinary
// event walk already gave it (the presence filter is a final membership
// pass over the built Nodes/Edges slices, not an early bypass that also
// skips touching the surviving end). Stars is computed from graph.Nodes
// AFTER the drop, so a home whose only node was dropped earns no star, and
// the survivor's own home keeps its.
func TestBuildCosmosLiveEdgeFilterDropsOnlyTheDeadEndAndItsStar(t *testing.T) {
	rows := []Row{
		{
			Kind:       LiveClaude,
			ID:         "alive-id",
			Name:       "Alive",
			Socket:     "cc-alive",
			PaneID:     "%1",
			Project:    "home",
			ActivityNS: 5,
		},
		{
			Kind:    LiveClaude,
			ID:      "dead-id",
			Name:    "Dead",
			Socket:  "cc-dead",
			PaneID:  "%2",
			Project: "lonely",
			Killed:  true,
		},
	}
	events := []fleetdb.CommsEvent{{
		AtNS: 40, Kind: fleetdb.KindInject, SenderUUID: "dead-id",
		Target: "Alive", ReceiverSocket: "cc-alive", Message: "last words",
	}}

	live := BuildCosmos(rows, events, 100, true)
	if len(live.Edges) != 0 {
		t.Fatalf("live graph kept an edge naming a dropped node: %#v", live.Edges)
	}
	alive, found := nodeWithKey(live, "chat:id:alive-id")
	if !found {
		t.Fatalf("the surviving node was dropped along with the dead one: %#v", live.Nodes)
	}
	if alive.LastNS != 40 {
		t.Fatalf(
			"surviving node LastNS = %d, want 40: the event walk touches it before the drop pass runs, and the drop must not roll that touch back",
			alive.LastNS,
		)
	}
	if _, ok := live.Stars["lonely"]; ok {
		t.Fatalf("a home whose only node was dropped still earned a star: %#v", live.Stars)
	}
	if _, ok := live.Stars["home"]; !ok {
		t.Fatalf("the surviving node's own home lost its star: %#v", live.Stars)
	}
}

// nodeLabel reads a node's human-facing label as text. It exists so the
// identity assertions below read the same before and after DisplayName
// stopped being a bare string.
func nodeLabel(node CosmosNode) string { return node.Label.String() }

// nodeWithKey finds one node by key, so a failure names the whole graph
// rather than panicking on an index.
func nodeWithKey(graph CosmosGraph, key string) (CosmosNode, bool) {
	for _, node := range graph.Nodes {
		if node.Key == key {
			return node, true
		}
	}
	return CosmosNode{}, false
}

// TestBuildCosmosResolvesReceiverAddressedByARawSessionID is the regression
// for the ghost the operator watched appear and vanish in the cosmos tab: a
// live, named chat received an MCP message and the graph minted a SECOND
// node labelled with that chat's raw tmux session id, which then aged out of
// the no-row grace window and was ruled GONE.
//
// Two independent misses produced it, and this event carries both. The
// receiver's recorded socket is a full PATH ("/tmp/tmux-1000/cc-…") while a
// live row's Socket is the BARE name tmux is addressed by (-L), so the pane
// index could never match; and the Target is the raw session id rather than
// the label, because the reply footer told the sender to address the chat
// that way, so the name index could never match either.
func TestBuildCosmosResolvesReceiverAddressedByARawSessionID(t *testing.T) {
	const session = "cc-1787705979-3980493-30867"
	rows := []Row{
		{Kind: LiveClaude, ID: "p-do-id", Name: "P:DO", Socket: session, PaneID: "%0"},
		{Kind: LiveClaude, ID: "sender-id", Name: "Sender", Socket: "cc-1787705979-3980493-1", PaneID: "%0"},
	}
	events := []fleetdb.CommsEvent{{
		AtNS: 10, Kind: fleetdb.KindInject,
		SenderUUID: "sender-id", SenderSession: "cc-1787705979-3980493-1", SenderLabel: "Sender",
		Target:         session,
		ReceiverSocket: "/tmp/tmux-1000/" + session, ReceiverPane: "%0",
		Message: "hello",
	}}

	graph := BuildCosmos(rows, events, 100, false)
	if len(graph.Nodes) != 2 {
		t.Fatalf(
			"a message between two known chats minted a node: got %d nodes, want 2\nnodes = %#v",
			len(graph.Nodes),
			graph.Nodes,
		)
	}
	node, found := nodeWithKey(graph, "chat:id:p-do-id")
	if !found {
		t.Fatalf("receiver did not resolve to its existing row\nnodes = %#v", graph.Nodes)
	}
	if label := nodeLabel(node); label != "P:DO" {
		t.Fatalf("receiver label = %q, want the chat's own label %q", label, "P:DO")
	}
	if !node.Live || node.Engine != "cc" {
		t.Fatalf("receiver node lost its row's liveness/engine: %#v", node)
	}
}

// TestBuildCosmosResolvesSenderByItsTmuxSessionName covers the wild shape an
// inject-as-SENDER writes: sender_session only, no uuid and no label (a chat
// whose 🔖 statusline had not rendered when it stated its identity). The
// session name is the one identity that event carries, so it has to resolve.
func TestBuildCosmosResolvesSenderByItsTmuxSessionName(t *testing.T) {
	const session = "cc-1787705979-3980493-30867"
	rows := []Row{{Kind: LiveClaude, ID: "p-do-id", Name: "P:DO", Socket: session, PaneID: "%0"}}
	events := []fleetdb.CommsEvent{{
		AtNS: 10, Kind: fleetdb.KindInject, SenderSession: session,
		Target: "Somebody", Message: "hi",
	}}

	graph := BuildCosmos(rows, events, 100, false)
	node, found := nodeWithKey(graph, "chat:id:p-do-id")
	if !found {
		t.Fatalf("sender did not resolve to its existing row by session name\nnodes = %#v", graph.Nodes)
	}
	if label := nodeLabel(node); label != "P:DO" {
		t.Fatalf("sender label = %q, want %q", label, "P:DO")
	}
}

// TestBuildCosmosResolvesSpawnReceiverRecordedWithNoPane covers the third
// wild shape: a spawn writes the receiver's FULL socket path and no pane id
// at all (spawn.Result carries none), and its Target is the label the chat
// was born with — which chat_name may since have replaced. Socket identity
// is the only thing that survives a rename, so it has to resolve.
func TestBuildCosmosResolvesSpawnReceiverRecordedWithNoPane(t *testing.T) {
	const session = "cc-1787705979-3980493-30867"
	rows := []Row{
		{Kind: LiveClaude, ID: "parent-id", Name: "Parent", Socket: "cc-1787705979-3980493-1", PaneID: "%0"},
		{Kind: LiveClaude, ID: "child-id", Name: "P:DO", Socket: session, PaneID: "%0"},
	}
	events := []fleetdb.CommsEvent{{
		AtNS: 10, Kind: fleetdb.KindSpawn,
		SenderSession:  "cc-1787705979-3980493-1",
		Target:         "born-as-this",
		ReceiverSocket: "/tmp/tmux-1000/" + session,
		ReceiverPane:   "",
		Message:        "born",
	}}

	graph := BuildCosmos(rows, events, 100, false)
	if len(graph.Nodes) != 2 {
		t.Fatalf(
			"spawn between two known chats minted a node: got %d, want 2\nnodes = %#v",
			len(graph.Nodes),
			graph.Nodes,
		)
	}
	child, found := nodeWithKey(graph, "chat:id:child-id")
	if !found {
		t.Fatalf("spawn receiver did not resolve to its existing row\nnodes = %#v", graph.Nodes)
	}
	if label := nodeLabel(child); label != "P:DO" {
		t.Fatalf("spawn receiver label = %q, want the chat's CURRENT label %q", label, "P:DO")
	}
	if _, found := nodeWithKey(graph, "chat:id:parent-id"); !found {
		t.Fatalf("spawn parent did not resolve to its existing row\nnodes = %#v", graph.Nodes)
	}
}

// TestBuildCosmosUnresolvedIdentityNeverRendersAsAName pins the honesty rule
// the ghost broke: an identity that matched no row must render as something
// a human reads as "this did not resolve". A raw session id in the name
// position is indistinguishable from a chat actually called that — the whole
// reason the original defect went unnoticed until a node blinked out.
func TestBuildCosmosUnresolvedIdentityNeverRendersAsAName(t *testing.T) {
	const sender = "cc-1787705979-3980493-30867"
	const target = "cc-1787705979-3980493-999"
	graph := BuildCosmos(nil, []fleetdb.CommsEvent{{
		AtNS: 10, Kind: fleetdb.KindInject, SenderSession: sender,
		Target: target, Message: "neither end has a row",
	}}, 100, false)
	if len(graph.Nodes) != 2 {
		t.Fatalf("nodes = %#v, want one per unresolved end", graph.Nodes)
	}
	for _, node := range graph.Nodes {
		label := nodeLabel(node)
		if label == sender || label == target {
			t.Fatalf("unresolved identity rendered as a plausible chat name: %q", label)
		}
		if !strings.Contains(label, "unresolved") {
			t.Fatalf("unresolved node label %q does not say it failed to resolve", label)
		}
	}
}

// TestBuildCosmosSpawnResolvesASplitRowThatCarriesNoPaneID answers the one
// question e94c34b left open. That fix made spawn record the receiver's FULL
// socket path so it matched what inject already wrote; the worry was that a
// spawn edge which used to match by pane no longer would.
//
// It is true, and for exactly one row class. A spawn records no pane at all
// (spawn.Result carries none), and every row built from a tmux pane carries
// tmux's own "%N" — so paneKey(socket, "") could never equal those, before or
// after e94c34b. The exception is compose.splitRow, the ONLY row constructor
// that sets Socket and leaves PaneID empty: pre-fix, paneKey(bare-socket, "")
// matched it exactly; post-fix, paneKey(full-path, "") did not. So that one
// class did regress, silently, into a ghost node.
//
// Resolution by session name repairs it for both recorded shapes at once,
// which is what this pins.
func TestBuildCosmosSpawnResolvesASplitRowThatCarriesNoPaneID(t *testing.T) {
	const session = "cc-1787705979-3980493-30867"
	// A split row as compose.splitRow builds one: a socket, no pane id, and
	// no transcript id either.
	rows := []Row{{Kind: LiveSplit, Name: "alpha+beta", Socket: session, SplitCount: 2}}
	for _, recorded := range []struct {
		name   string
		socket string
	}{
		{"post-e94c34b full socket path", "/tmp/tmux-1000/" + session},
		{"pre-e94c34b bare session name", session},
	} {
		t.Run(recorded.name, func(t *testing.T) {
			graph := BuildCosmos(rows, []fleetdb.CommsEvent{{
				AtNS: 10, Kind: fleetdb.KindSpawn, SenderLabel: "Parent",
				Target: "child-at-birth", ReceiverSocket: recorded.socket,
				ReceiverPane: "", Message: "born",
			}}, 100, false)
			node, found := nodeWithKey(graph, "chat:pane:"+session+"\t")
			if !found {
				t.Fatalf("the split row was not resolved; the spawn minted a ghost instead\nnodes = %#v", graph.Nodes)
			}
			if label := nodeLabel(node); label != "alpha+beta" {
				t.Fatalf("split node label = %q, want the row's own name", label)
			}
			if !node.Live {
				t.Fatalf("resolved split node lost its liveness: %#v", node)
			}
		})
	}
}

// TestBuildCosmosStarsCountTrafficOncePerHomeInsideTheHour pins the spectral
// class feature's counting rule (warm, cosmosBuilder.stars): an event counts
// ONCE per distinct Home among its endpoints — an intra-system message must
// not double-warm its own star, a cross-system message must warm BOTH ends —
// and only inside CosmosTrafficWindow of the clock BuildCosmos was actually
// handed, which is the one fact the chronoscope's replay depends on: heat at
// a past playhead is measured against THAT moment, not the real wall clock.
// A star with no recent traffic still gets a Stars entry (LastNS remembers
// it even when TrafficHour does not), and every distinct Home among the
// built nodes gets one, full stop.
func TestBuildCosmosStarsCountTrafficOncePerHomeInsideTheHour(t *testing.T) {
	rows := []Row{
		{Kind: LiveClaude, ID: "a1", Name: "A1", Socket: "cc-a1", Project: "alpha"},
		{Kind: LiveClaude, ID: "a2", Name: "A2", Socket: "cc-a2", Project: "alpha"},
		{Kind: LiveClaude, ID: "b1", Name: "B1", Socket: "cc-b1", Project: "beta"},
		{Kind: LiveClaude, ID: "g1", Name: "G1", Socket: "cc-g1", Project: "gamma"},
		{Kind: LiveClaude, ID: "g2", Name: "G2", Socket: "cc-g2", Project: "gamma"},
	}
	const nowNS = int64(240 * time.Hour) // an arbitrary clock, far from zero
	intraAlpha := fleetdb.CommsEvent{
		AtNS: nowNS - int64(1*time.Minute), Kind: fleetdb.KindInject,
		SenderUUID: "a1", Target: "A2", ReceiverSocket: "cc-a2", Message: "intra",
	}
	crossAlphaBeta := fleetdb.CommsEvent{
		AtNS: nowNS - int64(2*time.Minute), Kind: fleetdb.KindInject,
		SenderUUID: "a1", Target: "B1", ReceiverSocket: "cc-b1", Message: "cross",
	}
	oldGamma := fleetdb.CommsEvent{
		AtNS: nowNS - int64(2*CosmosTrafficWindow), Kind: fleetdb.KindInject,
		SenderUUID: "g1", Target: "G2", ReceiverSocket: "cc-g2", Message: "stale",
	}

	// Intra-system warms its ONE star ONCE, not twice: if warm() failed to
	// dedupe by Home, a1->a2 (both alpha) would count 2, not 1.
	intraOnly := BuildCosmos(rows, []fleetdb.CommsEvent{intraAlpha}, nowNS, false)
	if got := intraOnly.Stars["alpha"].TrafficHour; got != 1 {
		t.Fatalf("intra-system event warmed alpha %d times, want exactly 1: %#v", got, intraOnly.Stars["alpha"])
	}

	all := BuildCosmos(rows, []fleetdb.CommsEvent{intraAlpha, crossAlphaBeta, oldGamma}, nowNS, false)
	if got := all.Stars["alpha"].TrafficHour; got != 2 {
		t.Fatalf("alpha traffic = %d, want 2 (one intra + one cross)", got)
	}
	if got := all.Stars["beta"].TrafficHour; got != 1 {
		t.Fatalf("beta traffic = %d, want 1 (warmed by the cross-system event)", got)
	}
	if got := all.Stars["gamma"].TrafficHour; got != 0 {
		t.Fatalf("gamma traffic = %d, want 0: the only gamma event is older than CosmosTrafficWindow", got)
	}
	if got := all.Stars["gamma"].LastNS; got != oldGamma.AtNS {
		t.Fatalf("gamma LastNS = %d, want %d: an old touch still raises LastNS", got, oldGamma.AtNS)
	}
	if len(all.Stars) != 3 {
		t.Fatalf(
			"Stars = %#v, want exactly one entry per distinct Home (alpha, beta, gamma) — a zero-traffic home included",
			all.Stars,
		)
	}
	for _, home := range []string{"alpha", "beta", "gamma"} {
		if _, ok := all.Stars[home]; !ok {
			t.Fatalf("Stars missing entry for %q: %#v", home, all.Stars)
		}
	}

	// The chronoscope depends on this: BuildCosmos(rows, events, pastNS)
	// counts the trailing hour relative to whichever clock it was HANDED,
	// not a real wall clock the pure function never reads. The same event,
	// judged from two different clocks, must disagree on whether it is hot.
	hotFromThen := BuildCosmos(rows, []fleetdb.CommsEvent{intraAlpha}, intraAlpha.AtNS+30*int64(time.Minute), false)
	if got := hotFromThen.Stars["alpha"].TrafficHour; got != 1 {
		t.Fatalf("event 30m before the given clock should be hot: TrafficHour=%d", got)
	}
	coldFromThen := BuildCosmos(rows, []fleetdb.CommsEvent{intraAlpha}, intraAlpha.AtNS+2*int64(time.Hour), false)
	if got := coldFromThen.Stars["alpha"].TrafficHour; got != 0 {
		t.Fatalf("the SAME event, judged from a clock 2h later, should be cold: TrafficHour=%d", got)
	}
}

// TestBuildCosmosNodeCarriesRowKeyOnlyWhenFound pins compose.CosmosNode.RowKey,
// the navigator's join key back to the fleet: a resolved node carries the
// matched row's own ID, and a ghost the directory could not resolve carries
// none — RowKey is never fabricated from anything else an event happened to
// record (a session name, a label), or `enter` would open the wrong chat, or
// crash trying to find a row that does not exist.
func TestBuildCosmosNodeCarriesRowKeyOnlyWhenFound(t *testing.T) {
	rows := []Row{{Kind: LiveClaude, ID: "row-1", Name: "Alpha", Socket: "cc-alpha", PaneID: "%1"}}
	events := []fleetdb.CommsEvent{{
		AtNS: 10, Kind: fleetdb.KindInject,
		SenderUUID: "row-1", Target: "ghost-name", Message: "hi",
	}}
	graph := BuildCosmos(rows, events, 100, false)

	resolved, found := nodeWithKey(graph, "chat:id:row-1")
	if !found {
		t.Fatalf("resolved sender missing: %#v", graph.Nodes)
	}
	if resolved.RowKey != "row-1" {
		t.Fatalf("resolved node RowKey = %q, want the matched row's own ID %q", resolved.RowKey, "row-1")
	}

	ghost, found := nodeWithKey(graph, "chat:name:ghost-name")
	if !found {
		t.Fatalf("unresolved receiver missing: %#v", graph.Nodes)
	}
	if ghost.RowKey != "" {
		t.Fatalf("ghost node RowKey = %q, want empty — nothing in the fleet resolved it", ghost.RowKey)
	}
}

// TestRowKeyIsTheOneJoinKeyIDThenSocketPaneThenCWDProject is the K3
// table for compose.RowKey, now the ONE implementation the picker and the
// cosmos both join through: an ID always wins, a row with none (a live
// split window) falls back to its socket and pane, and a row with neither
// falls back to where it lives.
func TestRowKeyIsTheOneJoinKeyIDThenSocketPaneThenCWDProject(t *testing.T) {
	tests := []struct {
		name string
		row  Row
		want string
	}{
		{
			name: "an ID always wins, whatever else the row carries",
			row:  Row{Kind: LiveClaude, ID: "row-1", Socket: "cc-x", PaneID: "%1", CWD: "/work/x", Project: "x"},
			want: "row-1",
		},
		{
			name: "no ID: socket and pane, the split-row shape",
			row:  Row{Kind: LiveSplit, Socket: "cc-split", PaneID: ""},
			want: LiveSplit.String() + "\x00cc-split\x00",
		},
		{
			name: "no ID, no socket: where it lives",
			row:  Row{Kind: ResumeClaude, CWD: "/work/beta", Project: "beta"},
			want: ResumeClaude.String() + "\x00/work/beta\x00beta",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := RowKey(test.row); got != test.want {
				t.Fatalf("RowKey(%#v) = %q, want %q", test.row, got, test.want)
			}
		})
	}
}

// TestBuildCosmosSplitRowResolvesToARowKeyNotAGhost pins F1's producer half
// directly: a live split-pane row (Socket set, ID always empty — the exact
// shape TestBuildCosmosSpawnResolvesASplitRowThatCarriesNoPaneID proves the
// spawn resolver still finds) must never carry an empty RowKey. Before the
// fix, chatNode read node.RowKey = answer.Chat.ID — and a split row's own ID
// is always "" — so this resolved, LIVE node was indistinguishable from a
// true ghost the instant the cosmos tab tried to open it.
func TestBuildCosmosSplitRowResolvesToARowKeyNotAGhost(t *testing.T) {
	const session = "cc-1787705979-3980493-30867"
	rows := []Row{{Kind: LiveSplit, Name: "alpha+beta", Socket: session, SplitCount: 2}}
	graph := BuildCosmos(rows, []fleetdb.CommsEvent{{
		AtNS: 10, Kind: fleetdb.KindSpawn, SenderLabel: "Parent",
		Target: "child-at-birth", ReceiverSocket: session, ReceiverPane: "", Message: "born",
	}}, 100, false)

	node, found := nodeWithKey(graph, "chat:pane:"+session+"\t")
	if !found {
		t.Fatalf("the split row was not resolved: %#v", graph.Nodes)
	}
	// The pre-fix formula, for comparison: exactly what chatNode used to
	// assign — the row's own bare ID, which is always empty for a split row.
	if preFixRowKey := rows[0].ID; node.RowKey == preFixRowKey {
		t.Fatalf(
			"node.RowKey = %q (the pre-fix formula's answer for every split row) — a live row is reading as a ghost again",
			node.RowKey,
		)
	}
	if want := RowKey(rows[0]); node.RowKey != want {
		t.Fatalf("node.RowKey = %q, want compose.RowKey(row) = %q — the ONE join key (K3)", node.RowKey, want)
	}
}

// TestBuildCosmosUnsignedSpawnWarmsOnlyTheChildsHome closes a coverage gap
// the review named but did not find broken (NOTES: "an unsigned-spawn's
// traffic warming ... has no test pinning this specific path against
// Stars"): a spawn event carrying NO sender identity at all (SenderSession,
// SenderUUID, and SenderLabel all empty — builder.warm's own unsigned-event
// warning case) still warms the CHILD's star exactly once, and never invents
// a parent star to warm alongside it, since no parent node is ever resolved
// for an unsigned spawn.
func TestBuildCosmosUnsignedSpawnWarmsOnlyTheChildsHome(t *testing.T) {
	rows := []Row{
		{Kind: LiveClaude, ID: "child-id", Name: "Child", Socket: "cc-child", Project: "childhome"},
	}
	event := fleetdb.CommsEvent{
		AtNS: 10, Kind: fleetdb.KindSpawn,
		Target: "Child", ReceiverSocket: "cc-child", // no SenderSession/SenderUUID/SenderLabel
	}
	graph := BuildCosmos(rows, []fleetdb.CommsEvent{event}, 100, false)

	if len(graph.Warnings) != 1 || !strings.Contains(graph.Warnings[0], "no sender identity") {
		t.Fatalf("unsigned spawn did not warn by name: %#v", graph.Warnings)
	}
	if got := graph.Stars["childhome"].TrafficHour; got != 1 {
		t.Fatalf("childhome traffic = %d, want exactly 1", got)
	}
	if len(graph.Stars) != 1 {
		t.Fatalf(
			"Stars = %#v, want exactly ONE entry (the child's home) — an unsigned spawn resolves no parent to warm",
			graph.Stars,
		)
	}
}
