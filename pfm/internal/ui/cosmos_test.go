package ui

import (
	"context"
	"errors"
	"math"
	bits2 "math/bits"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"hostops/pfm/internal/compose"
	"hostops/pfm/internal/resolve"
	"hostops/pfm/internal/shared"
)

type countingCosmosSampler struct {
	events []shared.CommsEvent
	err    error
	calls  int
	since  int64
}

func (sampler *countingCosmosSampler) Sample(_ context.Context, sinceNS int64) ([]shared.CommsEvent, error) {
	sampler.calls++
	sampler.since = sinceNS
	return append([]shared.CommsEvent(nil), sampler.events...), sampler.err
}

func TestCosmosCanvasQuantizesEveryColorWithoutCursorAddressing(t *testing.T) {
	canvas := NewCanvas(2, 1)
	canvas.SetCell(0, 0, 'X', RGB{101, 102, 103}, true)
	frame := canvas.render()
	if strings.Contains(frame, "\x1b[1;1H") {
		t.Fatalf("canvas frame used cursor addressing: %q", frame)
	}
	if !strings.Contains(frame, "\x1b[1;38;2;104;104;104mX") {
		t.Fatalf("canvas color was not step-8 quantized: %q", frame)
	}
	if !strings.HasSuffix(frame, " \x1b[0m") {
		t.Fatalf("canvas did not return one complete row: %q", frame)
	}
}

func TestCosmosBrokenStatesRemainDistinct(t *testing.T) {
	snapshot := fixtureSnapshot(80)
	snapshot.NoSky = true

	empty := NewModel(snapshot)
	empty.tab = TabCosmos
	emptyText := ansi.Strip(empty.renderCosmosPanel(80, 9))
	if !strings.Contains(emptyText, "no chat traffic recorded yet") || strings.Contains(emptyText, "unreachable") {
		t.Fatalf("healthy empty cosmos was not distinct:\n%s", emptyText)
	}

	snapshot.Cosmos.Err = "sqlite: database is locked"
	broken := NewModel(snapshot)
	broken.tab = TabCosmos
	brokenText := ansi.Strip(broken.renderCosmosPanel(80, 9))
	if !strings.Contains(brokenText, "⚠ comms ledger unreachable: sqlite: database is locked") ||
		strings.Contains(brokenText, "no chat traffic recorded yet") {
		t.Fatalf("ledger failure was not distinct:\n%s", brokenText)
	}

	snapshot.Cosmos.Err = ""
	snapshot.Cosmos.Warnings = []string{compose.CosmosTruncationWarning}
	warned := NewModel(snapshot)
	warned.tab = TabCosmos
	warnedText := ansi.Strip(warned.renderCosmosPanel(80, 9))
	if !strings.Contains(warnedText, compose.CosmosTruncationWarning) || strings.Contains(warnedText, "unreachable") {
		t.Fatalf("truncated cosmos was not distinct:\n%s", warnedText)
	}
}

func TestCosmosTinyPaneFallsBackToNewestEdges(t *testing.T) {
	model := cosmosTestModel(19, true)
	plain := ansi.Strip(model.renderCosmosPanel(19, 7))
	if !strings.Contains(plain, "RR") || !strings.Contains(plain, "hello cosmos") {
		t.Fatalf("tiny pane dropped truthful edge detail:\n%s", plain)
	}
}

func TestCosmosParentlessSpawnIsNotEmptyInEitherLayout(t *testing.T) {
	snapshot := fixtureSnapshot(80)
	snapshot.NoSky = true
	snapshot.Cosmos = compose.BuildCosmos(snapshot.Rows, []shared.CommsEvent{{
		AtNS: fixtureNowNS, Kind: shared.KindSpawn, Target: "orphan",
		ReceiverSocket: "cx-orphan",
	}}, fixtureNowNS, false)
	if len(snapshot.Cosmos.Nodes) != 1 || len(snapshot.Cosmos.Edges) != 0 {
		t.Fatalf("parentless spawn fixture = %#v, want one node and zero edges", snapshot.Cosmos)
	}
	model := NewModel(snapshot)
	model.tab = TabCosmos

	tests := []struct {
		name          string
		width, height int
	}{
		{name: "full", width: 80, height: 9},
		{name: "compact", width: 19, height: 7},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plain := ansi.Strip(model.renderCosmosPanel(test.width, test.height))
			if strings.Contains(plain, "no chat traffic") {
				t.Fatalf("parentless spawn rendered as empty universe:\n%s", plain)
			}
		})
	}
}

func TestCosmosSamplerIsLazyAndRetainsGraphOnFailure(t *testing.T) {
	sampler := &countingCosmosSampler{events: []shared.CommsEvent{{
		AtNS: fixtureNowNS - int64(time.Second), Kind: shared.KindInject,
		SenderUUID: "11111111-1111-4111-8111-111111111111",
		Target:     "RR", Message: "fresh",
	}}}
	snapshot := fixtureSnapshot(120)
	snapshot.NoSky = true
	snapshot.CosmosSampler = sampler
	model := NewModel(snapshot)
	if sampler.calls != 0 {
		t.Fatalf("picker boot sampled cosmos: calls=%d", sampler.calls)
	}

	for range 3 {
		var command tea.Cmd
		model, command = applyKey(t, model, specialKey(tea.KeyTab))
		if model.tab != TabCosmos && sampler.calls != 0 {
			t.Fatalf("non-cosmos tab sampled cosmos: tab=%d calls=%d", model.tab, sampler.calls)
		}
		if model.tab == TabCosmos {
			if command == nil {
				t.Fatal("entering cosmos did not start its sampler")
			}
			message := command()
			updated, _ := model.Update(message)
			model = updated.(Model)
		}
	}
	if sampler.calls != 1 || sampler.since != fixtureNowNS-int64(compose.CosmosWindow) || len(model.cosmos.Edges) != 1 {
		t.Fatalf("cosmos sample calls=%d since=%d graph=%#v", sampler.calls, sampler.since, model.cosmos)
	}

	before := model.cosmos
	model.cosmosLoading = true
	updated, _ := model.Update(cosmosSampleMsg{
		generation: model.statsGeneration,
		err:        errors.New("read failed"),
	})
	model = updated.(Model)
	if model.cosmos.Err != "read failed" || len(model.cosmos.Nodes) != len(before.Nodes) ||
		len(model.cosmos.Edges) != len(before.Edges) {
		t.Fatalf("failed sample did not preserve last graph: before=%#v after=%#v", before, model.cosmos)
	}
}

func TestCosmosTickOnlyRunsWhileFocusedAndSkyEnabled(t *testing.T) {
	model := cosmosTestModel(120, false)
	before := model.cosmosNowNS
	updated, command := model.Update(cosmosTickMsg{
		generation: model.cosmosTickGeneration,
		nowNS:      before + int64(125*time.Millisecond),
	})
	model = updated.(Model)
	if model.cosmosNowNS != before || command != nil {
		t.Fatalf("off-tab cosmos tick advanced clock: now=%d command=%v", model.cosmosNowNS, command)
	}

	model.tab = TabCosmos
	updated, command = model.Update(cosmosTickMsg{
		generation: model.cosmosTickGeneration,
		nowNS:      before + int64(125*time.Millisecond),
	})
	model = updated.(Model)
	if model.cosmosNowNS == before || command == nil {
		t.Fatalf("focused cosmos tick now=%d command=%v", model.cosmosNowNS, command)
	}

	disabled := cosmosTestModel(120, true)
	disabled.tab = TabCosmos
	updated, command = disabled.Update(cosmosTickMsg{
		generation: disabled.cosmosTickGeneration,
		nowNS:      disabled.cosmosNowNS + int64(125*time.Millisecond),
	})
	disabled = updated.(Model)
	if command != nil || disabled.cosmosNowNS != fixtureNowNS {
		t.Fatalf("NoSky cosmos tick advanced: now=%d command=%v", disabled.cosmosNowNS, command)
	}
}

func TestCosmosRapidAwayAndBackDropsTheOldAnimationChain(t *testing.T) {
	model := cosmosTestModel(120, false)
	model.tab = TabCosmos
	model.cosmosTickGeneration = 4
	staleGeneration := model.cosmosTickGeneration

	left, _ := model.switchTab(1)
	model = left.(Model)
	returned, command := model.switchTab(-1)
	model = returned.(Model)
	if model.tab != TabCosmos || command == nil || model.cosmosTickGeneration == staleGeneration {
		t.Fatalf(
			"rapid return did not start a fresh cosmos chain: tab=%d generation=%d command=%v",
			model.tab,
			model.cosmosTickGeneration,
			command,
		)
	}

	before := model.cosmosNowNS
	updated, staleCommand := model.Update(cosmosTickMsg{
		generation: staleGeneration,
		nowNS:      before + int64(125*time.Millisecond),
	})
	model = updated.(Model)
	if model.cosmosNowNS != before || staleCommand != nil {
		t.Fatalf("stale cosmos chain survived rapid return: now=%d command=%v", model.cosmosNowNS, staleCommand)
	}
}

func TestCosmosSeatSurvivesGraphRefreshAndSpawnStartsAtParent(t *testing.T) {
	model := cosmosTestModel(120, true)
	parent := "chat:id:11111111-1111-4111-8111-111111111111"
	model.cosmosSeats[parent].Angle = 0.42
	model.cosmosSeats[parent].Target = 0.42
	child := compose.CosmosNode{Key: "chat:name:child", Label: resolve.Named("child")}
	model.cosmos.Nodes = append(model.cosmos.Nodes, child)
	model.cosmos.Edges = append(model.cosmos.Edges, compose.CosmosEdge{
		From: parent, To: child.Key, Kind: shared.KindSpawn, LastNS: fixtureNowNS,
	})
	model.mergeCosmosSeats()
	if model.cosmosSeats[parent].Angle != 0.42 {
		t.Fatalf("parent seat reset across merge: %#v", model.cosmosSeats[parent])
	}
	if model.cosmosSeats[child.Key].Angle != 0.42 {
		t.Fatalf("spawn child was not born at parent angle: %#v", model.cosmosSeats[child.Key])
	}
}

// The planetary law: every moon of one parent shares ONE orbit — the same
// aspect-corrected distance from the parent, evenly spaced from its siblings,
// the ring widening with the brood. --no-sky holds the system perfectly
// still; the sky rotates it slowly around the parent.
func TestCosmosMoonsShareOneEvenlySpacedOrbit(t *testing.T) {
	model := cosmosTestModel(120, true)
	parent := "chat:id:11111111-1111-4111-8111-111111111111"
	parentHome := ""
	for _, node := range model.cosmos.Nodes {
		if node.Key == parent {
			parentHome = node.Home
		}
	}
	moonA := compose.CosmosNode{Key: "chat:name:moon-a", Label: resolve.Named("moon-a"), Home: parentHome}
	moonB := compose.CosmosNode{Key: "chat:name:moon-b", Label: resolve.Named("moon-b"), Home: parentHome}
	model.cosmos.Nodes = append(model.cosmos.Nodes, moonA, moonB)
	model.cosmos.Edges = append(model.cosmos.Edges,
		compose.CosmosEdge{From: parent, To: moonA.Key, Kind: shared.KindSpawn, LastNS: fixtureNowNS},
		compose.CosmosEdge{From: parent, To: moonB.Key, Kind: shared.KindSpawn, LastNS: fixtureNowNS},
	)
	model.mergeCosmosSeats()
	canvas := NewCanvas(118, 24)
	nodes := cosmosNodeMap(model.cosmos.Nodes)
	now := time.Unix(0, fixtureNowNS)
	points := cosmosLayout(canvas, model.cosmosSeats, nodes, model.cosmos.Edges, now, false, false, "").points

	anchor := points[parent]
	orbitOf := func(p cosmosPoint) float64 {
		return math.Hypot(p.x-anchor.x, p.y-anchor.y)
	}
	wantOrbit := 5 + 2.5*2
	if got := orbitOf(points[moonA.Key]); math.Abs(got-wantOrbit) > 0.001 {
		t.Fatalf("moon A orbit = %v, want %v", got, wantOrbit)
	}
	if got := orbitOf(points[moonB.Key]); math.Abs(got-wantOrbit) > 0.001 {
		t.Fatalf("moon B orbit = %v, want %v", got, wantOrbit)
	}
	// Two moons evenly spaced means directly opposite: their midpoint is the
	// parent itself.
	midX := (points[moonA.Key].x + points[moonB.Key].x) / 2
	midY := (points[moonA.Key].y + points[moonB.Key].y) / 2
	if math.Abs(midX-anchor.x) > 0.001 || math.Abs(midY-anchor.y) > 0.001 {
		t.Fatalf(
			"moons are not evenly spaced around the parent: mid=(%v,%v) parent=(%v,%v)",
			midX,
			midY,
			anchor.x,
			anchor.y,
		)
	}

	// --no-sky is a still frame: a later clock renders the identical system.
	later := cosmosLayout(
		canvas,
		model.cosmosSeats,
		nodes,
		model.cosmos.Edges,
		now.Add(5*time.Second),
		false,
		false,
		"",
	).points
	if later[moonA.Key] != points[moonA.Key] {
		t.Fatalf("no-sky moon moved: %#v vs %#v", later[moonA.Key], points[moonA.Key])
	}

	// The sky orbits the moon around its parent — position changes, the
	// orbit distance does not.
	skyLater := cosmosLayout(
		canvas,
		model.cosmosSeats,
		nodes,
		model.cosmos.Edges,
		now.Add(5*time.Second),
		true,
		false,
		"",
	).points
	if skyLater[moonA.Key] == points[moonA.Key] {
		t.Fatalf("sky moon did not orbit")
	}
	skyAnchor := skyLater[parent]
	if got := math.Hypot(
		skyLater[moonA.Key].x-skyAnchor.x,
		skyLater[moonA.Key].y-skyAnchor.y,
	); math.Abs(
		got-wantOrbit,
	) > 0.001 {
		t.Fatalf("orbiting moon left its orbit: %v, want %v", got, wantOrbit)
	}
}

// Residence wins over lineage: a chat spawned into ANOTHER star's system is
// a planet of the star it lives under, never a moon of its parent — the
// spawn edge alone carries the bloodline between the systems.
func TestCosmosCrossStarSpawnIsAPlanetNotAMoon(t *testing.T) {
	nodes := map[string]compose.CosmosNode{
		"chat:name:parent": {Key: "chat:name:parent", Home: ".professor"},
		"chat:name:child":  {Key: "chat:name:child", Home: "atlas"},
		"chat:name:local":  {Key: "chat:name:local", Home: ".professor"},
	}
	edges := []compose.CosmosEdge{
		{From: "chat:name:parent", To: "chat:name:child", Kind: shared.KindSpawn},
		{From: "chat:name:parent", To: "chat:name:local", Kind: shared.KindSpawn},
	}
	parents, children := cosmosOrbits(edges, nodes)
	if parents["chat:name:child"] != "" {
		t.Fatalf("cross-star spawn became a moon of %q, want a free planet of its own star", parents["chat:name:child"])
	}
	if parents["chat:name:local"] != "chat:name:parent" || len(children["chat:name:parent"]) != 1 {
		t.Fatalf("same-star spawn should still be a moon: parents=%v children=%v", parents, children)
	}
}

// Every distinct Home is a star: one project alone holds the galactic
// centre, several share the ring — and every star seats at a distinct point.
func TestCosmosEveryHomeIsAStar(t *testing.T) {
	single := map[string]compose.CosmosNode{
		"chat:name:a": {Key: "chat:name:a", Home: ".professor"},
	}
	order, points := cosmosStarPoints(single, 100, 50, 60, 30)
	if len(order) != 1 || points[".professor"] != (cosmosPoint{x: 100, y: 50}) {
		t.Fatalf("single star should hold the centre: order=%v points=%v", order, points)
	}
	multi := map[string]compose.CosmosNode{
		"chat:name:a": {Key: "chat:name:a", Home: ".professor"},
		"chat:name:b": {Key: "chat:name:b", Home: "atlas"},
		"chat:name:c": {Key: "chat:name:c", Home: "/tmp"},
	}
	order, points = cosmosStarPoints(multi, 100, 50, 60, 30)
	if len(order) != 3 {
		t.Fatalf("want three stars, got %v", order)
	}
	distinct := map[cosmosPoint]bool{}
	for _, home := range order {
		distinct[points[home]] = true
	}
	if len(distinct) != 3 {
		t.Fatalf("stars share a seat: %v", points)
	}
}

func TestClipCosmosLabelTruncatesVisiblyWithinAvailableSpace(t *testing.T) {
	tests := []struct {
		name      string
		label     string
		rightward bool
		colX      int
		cols      int
		want      string
	}{
		{name: "fits rightward, unchanged", label: "RR", rightward: true, colX: 5, cols: 80, want: "RR"},
		{
			name:      "clips rightward with a visible ellipsis",
			label:     "123456789",
			rightward: true,
			colX:      73,
			cols:      80,
			want:      "1234…",
		},
		{
			name:      "clips leftward with a visible ellipsis",
			label:     "123456789",
			rightward: false,
			colX:      6,
			cols:      80,
			want:      "1234…",
		},
		{
			name:      "no space left produces empty, not the raw label",
			label:     "cc-1787827912-1607460-33758",
			rightward: true,
			colX:      79,
			cols:      80,
			want:      "",
		},
		{
			name:      "ample canvas still caps at fourteen runes",
			label:     "COSMOS:EXISTENTIALIST",
			rightward: true,
			colX:      5,
			cols:      120,
			want:      "COSMOS:EXISTE…",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := clipCosmosLabel(test.label, test.rightward, test.colX, test.cols)
			if got != test.want {
				t.Fatalf("clipCosmosLabel(%q, rightward=%v, colX=%d, cols=%d) = %q, want %q",
					test.label, test.rightward, test.colX, test.cols, got, test.want)
			}
		})
	}
}

func cosmosTestModel(width int, noSky bool) Model {
	snapshot := fixtureSnapshot(width)
	snapshot.NoSky = noSky
	snapshot.Cosmos = compose.BuildCosmos(snapshot.Rows, []shared.CommsEvent{{
		AtNS:       fixtureNowNS - int64(300*time.Millisecond),
		Kind:       shared.KindInject,
		SenderUUID: "11111111-1111-4111-8111-111111111111",
		Target:     "RR",
		Message:    "hello cosmos",
	}}, fixtureNowNS, false)
	return NewModel(snapshot)
}

// TestApplyCosmosGraphStoresGraphAsGiven pins applyCosmosGraph's new,
// entire job: a straight store, nothing diffed, dated, or pruned.
// compose.BuildCosmos already resolved every Dead/live question before the
// graph ever reaches here (see its own doc comment), so a node present in
// graph N is in model.cosmos verbatim, and a node simply absent from graph
// N+1 is gone on the very next frame — no lingering, no DiedNS rewrite, no
// detection bookkeeping of any kind.
func TestApplyCosmosGraphStoresGraphAsGiven(t *testing.T) {
	model := NewModel(fixtureSnapshot(80))
	model.cosmosNowNS = fixtureNowNS

	first := compose.CosmosGraph{
		Nodes: []compose.CosmosNode{
			{Key: "chat:id:X", Label: resolve.Named("X"), LastNS: fixtureNowNS},
			{Key: "chat:id:Y", Label: resolve.Named("Y"), LastNS: fixtureNowNS},
		},
		Edges: []compose.CosmosEdge{{From: "chat:id:X", To: "chat:id:Y", Kind: shared.KindInject}},
	}
	model.applyCosmosGraph(first)
	if len(model.cosmos.Nodes) != 2 || len(model.cosmos.Edges) != 1 {
		t.Fatalf("frame 1 was not stored as given: %#v", model.cosmos)
	}

	// Frame 2: X is simply gone from the incoming graph — no Dead flag, no
	// DiedNS, nothing to animate. It must be gone from model.cosmos too,
	// same frame, no lingering.
	second := compose.CosmosGraph{
		Nodes: []compose.CosmosNode{{Key: "chat:id:Y", Label: resolve.Named("Y"), LastNS: fixtureNowNS}},
	}
	model.applyCosmosGraph(second)
	if len(model.cosmos.Nodes) != 1 || model.cosmos.Nodes[0].Key != "chat:id:Y" {
		t.Fatalf("X lingered after being dropped from the incoming graph: %#v", model.cosmos.Nodes)
	}
	if len(model.cosmos.Edges) != 0 {
		t.Fatalf("an edge naming the vanished node lingered: %#v", model.cosmos.Edges)
	}
	if !reflect.DeepEqual(model.cosmos, second) {
		t.Fatalf("model.cosmos = %#v, want the exact graph handed in: %#v", model.cosmos, second)
	}
}

// TestApplyCosmosGraphNeverCarriesADeadNodeLiveButReplayRendersItDimmed is
// the end-to-end pin for the ruling itself: a live graph — the shape
// applyCosmosGraph is ALWAYS handed for model.cosmos, compose.BuildCosmos
// with live=true — never carries a Dead node or an edge naming one, all
// the way through the model that renders it. The same rows and events, cut
// with live=false and fed as the chronoscope's replay graph instead, keep
// the dead chat and render it dimmed and static (scaleRGB(base, 0.35), not
// bold) — the ghost of itself it truthfully is, never the removed alarm
// blink.
func TestApplyCosmosGraphNeverCarriesADeadNodeLiveButReplayRendersItDimmed(t *testing.T) {
	rows := []compose.Row{
		{Kind: compose.LiveClaude, ID: "killed-id", Name: "Killed", Socket: "cc-killed", PaneID: "%1", Killed: true},
	}
	events := []shared.CommsEvent{{
		AtNS: fixtureNowNS - int64(time.Hour), Kind: shared.KindInject,
		SenderUUID: "killed-id", Target: "Someone", Message: "last words",
	}}
	deadKey := "chat:id:killed-id"

	live := compose.BuildCosmos(rows, events, fixtureNowNS, true)
	model := NewModel(fixtureSnapshot(80))
	model.cosmosNowNS = fixtureNowNS
	model.rows = rows
	model.applyCosmosGraph(live)
	for _, node := range model.cosmos.Nodes {
		if node.Dead {
			t.Fatalf("a live graph carried a Dead node all the way to model.cosmos: %#v", node)
		}
		if node.Key == deadKey {
			t.Fatalf("the killed row's node reached the live model at all: %#v", node)
		}
	}
	for _, edge := range model.cosmos.Edges {
		if edge.From == deadKey || edge.To == deadKey {
			t.Fatalf("an edge naming the killed row's node reached the live model: %#v", edge)
		}
	}

	replay := compose.BuildCosmos(rows, events, fixtureNowNS, false)
	model.cosmosPast = &replay
	node, found := cosmosNodeByKey(model.viewGraph(), deadKey)
	if !found {
		t.Fatalf("the replay graph dropped the dead node: %#v", model.viewGraph().Nodes)
	}
	if !node.Dead {
		t.Fatalf("replay node for a killed row should be Dead: %#v", node)
	}

	// Seats must be merged against the replay graph (viewGraph() now
	// returns cosmosPast) before layout has a point to seat the dead node
	// at — mirroring what scrubCosmos does on every real `[` press.
	model.mergeCosmosSeats()
	canvas := NewCanvas(80, 24)
	now := time.Unix(0, fixtureNowNS)
	nodes := cosmosNodeMap(replay.Nodes)
	frame := cosmosLayout(canvas, model.cosmosSeats, nodes, replay.Edges, now, false, false, "")
	point, ok := frame.points[deadKey]
	if !ok {
		t.Fatal("the layout seated no point for the dead replay node")
	}
	model.drawCosmosUniverse(canvas, replay, now, now)
	colX, colY := int(point.x)/2, int(point.y)/4
	got := canvas.cells[colY*canvas.Cols+colX].fg
	want := scaleRGB(cosmosNodeColor(node), 0.35)
	if got != want {
		t.Fatalf(
			"replayed dead glyph color = %#v, want the static ghost color %#v (not the removed alarm blink)",
			got,
			want,
		)
	}
}

// cosmosNodeByKey names the node a test means, so a miss fails with the whole
// graph instead of panicking on an index.
func cosmosNodeByKey(graph compose.CosmosGraph, key string) (compose.CosmosNode, bool) {
	for _, node := range graph.Nodes {
		if node.Key == key {
			return node, true
		}
	}
	return compose.CosmosNode{}, false
}

// TestCosmosRendersARawSessionAddressedSendOnTheExistingNode is the
// end-to-end proof for the defect the operator watched happen: a chat sent an
// MCP message to a live, named chat, and the cosmos tab grew a node labelled
// with the RECEIVER's raw tmux session id, which then blinked out 2.7s later
// when the no-row grace window expired.
//
// It asserts all three things that have to be true, not just the one that was
// broken. The send must land on the two nodes that already exist and mint
// NONE. The node must render the chat's human label. And the send visual must
// still fire — a resolution fix that quietly stopped the comet would be a
// regression the node-count assertion alone would never catch.
func TestCosmosRendersARawSessionAddressedSendOnTheExistingNode(t *testing.T) {
	const receiverSession = "cx-1799999900-1-1" // fixture row 1 ("RR")
	build := func(noSky bool) Model {
		snapshot := fixtureSnapshot(80)
		snapshot.NoSky = noSky
		snapshot.Cosmos = compose.BuildCosmos(snapshot.Rows, []shared.CommsEvent{{
			AtNS:       fixtureNowNS - int64(300*time.Millisecond),
			Kind:       shared.KindInject,
			SenderUUID: "11111111-1111-4111-8111-111111111111",
			// The pathological shape: the caller addressed the chat by
			// the raw session id the old reply footer handed it, and the
			// receiver was recorded by its full socket PATH while a live
			// row carries the bare name.
			Target:         receiverSession,
			ReceiverSocket: "/tmp/tmux-1000/" + receiverSession,
			ReceiverPane:   "%0",
			Message:        "hello cosmos",
		}}, fixtureNowNS, false)
		model := NewModel(snapshot)
		model.tab = TabCosmos
		return model
	}

	graph := build(true).cosmos
	if len(graph.Nodes) != 2 {
		t.Fatalf("a send between two known chats produced %d nodes, want 2: %#v", len(graph.Nodes), graph.Nodes)
	}
	receiver, found := cosmosNodeByKey(graph, "chat:id:22222222-2222-4222-8222-222222222222")
	if !found {
		t.Fatalf("the receiver did not resolve to its existing row: %#v", graph.Nodes)
	}
	if got := receiver.Label.String(); got != "RR" {
		t.Fatalf("receiver rendered as %q, want its label %q", got, "RR")
	}
	for _, node := range graph.Nodes {
		if label := node.Label.String(); strings.Contains(label, receiverSession) {
			t.Fatalf("a raw session id reached a node label: %q", label)
		}
	}

	if !strings.Contains(build(true).View().Content, "RR") {
		t.Fatalf("the rendered cosmos frame does not show the receiver's label")
	}

	// The send visual. Comparing a sky-ON frame against a sky-OFF one would
	// prove nothing — the starfield alone makes those differ whether the
	// comet flew or was never wired — so this holds the sky ON for both
	// renders and moves only the CLOCK: once while the edge is in flight,
	// once a full comet duration past it. The universe canvas is where the
	// comet paints, and its dot count is the direct evidence.
	model := build(false)
	inFlight := cosmosUniverseDots(model, time.Unix(0, fixtureNowNS))
	landed := cosmosUniverseDots(
		model,
		time.Unix(0, fixtureNowNS+int64(cosmosCometDuration(shared.KindInject))),
	)
	if inFlight <= landed {
		t.Fatalf(
			"the send visual did not fire: universe dots in flight = %d, after landing = %d",
			inFlight, landed,
		)
	}
}

// cosmosUniverseDots renders the universe canvas at one instant and counts the
// subpixels it painted. The comet, its head burst and its tail are all dots,
// so this is what "the send animated" looks like from the outside.
func cosmosUniverseDots(model Model, now time.Time) int {
	canvas := NewCanvas(80, 24)
	model.drawCosmosUniverse(canvas, model.cosmos, now, now)
	painted := 0
	for _, bits := range canvas.dots {
		painted += bits2.OnesCount8(bits)
	}
	return painted
}
