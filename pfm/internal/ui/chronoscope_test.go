package ui

import (
	"math"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"hostops/pfm/internal/compose"
	"hostops/pfm/internal/fleetdb"
	"hostops/pfm/internal/resolve"
)

// U1: TestChronoscopeScrubCutsThePastFromTheSameEvents pins the chronoscope's
// core cut: scrubbing the playhead back builds a graph from a PREFIX of the
// same sampled timeline, never touching the live graph model.cosmos keeps.
func TestChronoscopeScrubCutsThePastFromTheSameEvents(t *testing.T) {
	model := cosmosGoldenModel(120)
	model.cosmosEvents = cosmosGoldenEvents(model.cosmosNowNS, model.rows)
	model.rebuildCosmosTimeline()

	liveNodes, liveEdges := len(model.cosmos.Nodes), len(model.cosmos.Edges)
	if liveNodes != 3 || liveEdges != 2 {
		t.Fatalf("setup: live graph = %d nodes, %d edges, want 3 and 2", liveNodes, liveEdges)
	}

	past, _ := applyKey(t, model, printableKey('['))
	if past.cosmosPast == nil {
		t.Fatal("[ did not enter replay: cosmosPast is nil")
	}
	cut := past.viewGraph()
	if len(cut.Nodes) != 2 || len(cut.Edges) != 1 {
		t.Fatalf(
			"5-minute-back cut = %d nodes, %d edges, want 2 and 1 (the 10m-old spawn present, the 300ms-old inject absent)\n%#v",
			len(cut.Nodes),
			len(cut.Edges),
			cut,
		)
	}
	// The live graph model.cosmos must never be mutated by a scrub — only
	// model.cosmosPast is a new graph.
	if len(past.cosmos.Nodes) != liveNodes || len(past.cosmos.Edges) != liveEdges {
		t.Fatalf(
			"scrub mutated the LIVE graph: nodes=%d edges=%d, want %d and %d",
			len(past.cosmos.Nodes),
			len(past.cosmos.Edges),
			liveNodes,
			liveEdges,
		)
	}

	live, _ := applyKey(t, past, printableKey(']'))
	if live.cosmosPast != nil {
		t.Fatalf("] landing on now did not return to live: cosmosPast=%#v", live.cosmosPast)
	}
	if live.cosmosViewNS != live.cosmosNowNS {
		t.Fatalf("cosmosViewNS = %d, want cosmosNowNS %d after returning to live", live.cosmosViewNS, live.cosmosNowNS)
	}
	restored := live.viewGraph()
	if len(restored.Nodes) != 3 || len(restored.Edges) != 2 {
		t.Fatalf("live graph after return = %d nodes, %d edges, want 3 and 2", len(restored.Nodes), len(restored.Edges))
	}
}

// U2: TestChronoscopeRefusesByNameBeforeTheFirstSample pins the refusal
// contract: a scrub key pressed before the ledger has ever been sampled
// names the reason rather than doing nothing, and the query row shows it.
func TestChronoscopeRefusesByNameBeforeTheFirstSample(t *testing.T) {
	model := cosmosGoldenModel(80)
	if model.cosmosTimeline != nil {
		t.Fatalf("setup: a fresh model already has a timeline: %#v", model.cosmosTimeline)
	}
	pressed, _ := applyKey(t, model, printableKey('['))
	if pressed.cosmosPast != nil {
		t.Fatalf("scrub before the first sample entered replay anyway: %#v", pressed.cosmosPast)
	}
	if !strings.Contains(pressed.cosmosStatus, "has not been sampled") {
		t.Fatalf("cosmosStatus = %q, want it to name the unsampled ledger", pressed.cosmosStatus)
	}
	query := ansi.Strip(pressed.renderQuery(80))
	if !strings.Contains(query, "has not been sampled") {
		t.Fatalf("query row = %q, does not carry the refusal", query)
	}
}

// U3: TestChronoscopeClampsAtTheWindowFloor pins the 24h ledger boundary: no
// amount of scrubbing back can pass CosmosWindow, and the floor names itself.
func TestChronoscopeClampsAtTheWindowFloor(t *testing.T) {
	model := cosmosGoldenModel(80)
	model.cosmosEvents = cosmosGoldenEvents(model.cosmosNowNS, model.rows)
	model.rebuildCosmosTimeline()

	for i := 0; i < 30; i++ {
		model, _ = applyKey(t, model, printableKey('{'))
	}
	floor := model.cosmosNowNS - int64(compose.CosmosWindow)
	if model.cosmosViewNS != floor {
		t.Fatalf("cosmosViewNS = %d, want the window floor %d after 30 coarse scrubs", model.cosmosViewNS, floor)
	}
	if !strings.Contains(model.cosmosStatus, "24h") {
		t.Fatalf("cosmosStatus = %q, want it to name the 24h floor", model.cosmosStatus)
	}
}

// U4: TestChronoscopePlayAdvancesAtSixtyTimesAndSnapsToLive pins playback:
// one real second of tick advances the playhead sixty ledger-seconds, and
// reaching now snaps cleanly back to live. --no-sky refuses play by name.
func TestChronoscopePlayAdvancesAtSixtyTimesAndSnapsToLive(t *testing.T) {
	model := cosmosSkyGoldenModel(80)
	model.cosmosEvents = cosmosGoldenEvents(model.cosmosNowNS, model.rows)
	model.rebuildCosmosTimeline()

	playing, _ := applyKey(t, model, printableKey(' '))
	if playing.cosmosPast == nil || !playing.cosmosPlaying {
		t.Fatalf("space did not start playback: past=%#v playing=%v", playing.cosmosPast, playing.cosmosPlaying)
	}
	wantStart := playing.cosmosNowNS - int64(time.Hour)
	if playing.cosmosViewNS != wantStart {
		t.Fatalf("space did not seat the playhead an hour back: got %d, want %d", playing.cosmosViewNS, wantStart)
	}

	before := playing.cosmosViewNS
	updated, cmd := playing.Update(cosmosTickMsg{
		generation: playing.cosmosTickGeneration,
		nowNS:      playing.cosmosNowNS + int64(time.Second),
	})
	if cmd == nil {
		t.Fatal("a live cosmos tick returned no follow-up command")
	}
	ticked := updated.(Model)
	advanced := ticked.cosmosViewNS - before
	if math.Abs(float64(advanced)-float64(60*time.Second)) > float64(2*time.Millisecond) {
		t.Fatalf("one real second advanced the playhead by %s, want ~60s", time.Duration(advanced))
	}

	// Keep ticking, real time advancing 1s each frame, until the playhead
	// (advancing 60 ledger-seconds per tick) must have crossed "now".
	now := ticked.cosmosNowNS
	model = ticked
	for i := 0; i < 65; i++ {
		now += int64(time.Second)
		updated, _ := model.Update(cosmosTickMsg{generation: model.cosmosTickGeneration, nowNS: now})
		model = updated.(Model)
	}
	if model.cosmosPast != nil || model.cosmosPlaying {
		t.Fatalf(
			"playback did not snap back to live after crossing now: past=%#v playing=%v",
			model.cosmosPast,
			model.cosmosPlaying,
		)
	}

	noSky := cosmosGoldenModel(80) // NoSky (skyEnabled=false)
	noSky.cosmosEvents = cosmosGoldenEvents(noSky.cosmosNowNS, noSky.rows)
	noSky.rebuildCosmosTimeline()
	refused, _ := applyKey(t, noSky, printableKey(' '))
	if refused.cosmosPast != nil || refused.cosmosPlaying {
		t.Fatalf("--no-sky started playback anyway: past=%#v playing=%v", refused.cosmosPast, refused.cosmosPlaying)
	}
	if !strings.Contains(refused.cosmosStatus, "--no-sky") {
		t.Fatalf("cosmosStatus = %q, want it to name --no-sky", refused.cosmosStatus)
	}
}

// U5: TestChronoscopeReplayRendersADeadChatAsAGhostNotAnAlarm pins replay
// death honesty (cosmos.go's drawCosmosUniverse Dead branch, and
// compose.BuildCosmos's own live-drop doc comment): a row Killed right now,
// with ledger activity in the past, is dropped from the LIVE graph by
// BuildCosmos(live=true) itself — the exact shape every real caller builds
// model.cosmos from (cmd/pfm/ls_pipeline.go, ui/model.go's own refresh) — but
// still renders — dim and static, never the removed alarm blink — when the
// chronoscope is scrubbed back to when it was talking, cut with live=false.
func TestChronoscopeReplayRendersADeadChatAsAGhostNotAnAlarm(t *testing.T) {
	snapshot := fixtureSnapshot(120)
	snapshot.NoSky = false // the sky must be ON: the ghost rule must hold even then
	rows := append([]compose.Row(nil), snapshot.Rows...)
	rows[0].Killed = true
	rows[0].Name = "Ghosty"
	snapshot.Rows = rows

	oldEvent := fleetdb.CommsEvent{
		AtNS: fixtureNowNS - int64(10*time.Minute), Kind: fleetdb.KindInject,
		SenderUUID: rows[0].ID, Target: rows[1].Name, ReceiverSocket: rows[1].Socket,
		Message: "dying words",
	}
	snapshot.Cosmos = compose.BuildCosmos(snapshot.Rows, []fleetdb.CommsEvent{oldEvent}, fixtureNowNS, true)

	model := NewModel(snapshot)
	model.tab = TabCosmos
	deadKey := "chat:id:" + rows[0].ID

	for _, node := range model.cosmos.Nodes {
		if node.Key == deadKey {
			t.Fatalf(
				"the live graph kept the killed row's node — BuildCosmos(live=true) should have dropped it: %#v",
				node,
			)
		}
	}

	model.cosmosEvents = []fleetdb.CommsEvent{oldEvent}
	model.rebuildCosmosTimeline()
	replay, _ := applyKey(t, model, printableKey('['))
	if replay.cosmosPast == nil {
		t.Fatal("[ did not enter replay")
	}
	// The live graph had only RR seated (Ghosty was dropped by BuildCosmos
	// itself); entering replay adds Ghosty as a brand-new seat at its own
	// target while RR's pre-existing seat only starts EASING toward its own
	// new target — by design, seats never teleport. Settle both here so the
	// render below checks the settled picture a viewer actually sees, not
	// the one coincidental transient frame where an old and a new seat
	// overlap.
	for _, seat := range replay.cosmosSeats {
		seat.Angle = seat.Target
		seat.Ring = seat.RingTarget
	}
	deadNode, found := cosmosNodeByKey(replay.viewGraph(), deadKey)
	if !found {
		t.Fatalf("the replayed past dropped the node entirely: %#v", replay.viewGraph().Nodes)
	}
	if !deadNode.Dead {
		t.Fatalf("replayed node should be Dead (its row is Killed right now): %#v", deadNode)
	}

	if !strings.Contains(ansi.Strip(replay.View().Content), "Ghosty") {
		t.Fatal("the ghost's label never reached the rendered frame")
	}

	canvas := NewCanvas(114, 22)
	now := time.Unix(0, replay.cosmosNowNS)
	view := time.Unix(0, replay.cosmosViewNS)
	graph := replay.viewGraph()
	replay.drawCosmosUniverse(canvas, graph, now, view)
	nodes := cosmosNodeMap(graph.Nodes)
	frame := cosmosLayout(
		canvas,
		replay.cosmosSeats,
		nodes,
		graph.Edges,
		now,
		replay.skyEnabled,
		replay.classicSky,
		replay.cosmosFocus,
	)
	point, ok := frame.points[deadKey]
	if !ok {
		t.Fatal("the layout seated no point for the dead node")
	}
	colX, colY := int(point.x)/2, int(point.y)/4
	if colX < 0 || colX >= canvas.Cols || colY < 0 || colY >= canvas.Rows {
		t.Fatalf("the ghost's glyph landed off-canvas at (%d,%d)", colX, colY)
	}
	got := canvas.cells[colY*canvas.Cols+colX].fg
	want := scaleRGB(cosmosNodeColor(deadNode), 0.35)
	if got != want {
		t.Fatalf("replayed dead glyph color = %#v, want the static ghost color %#v (not the alarm blink)", got, want)
	}
}

// U6: TestChronoscopeEmptyPastNamesTheMomentNotTheLedger pins the three
// distinct broken/empty states a replay can be in, so an operator never
// mistakes one for another.
func TestChronoscopeEmptyPastNamesTheMomentNotTheLedger(t *testing.T) {
	t.Run("scrubbed before the first event names the moment", func(t *testing.T) {
		model := cosmosGoldenModel(80)
		model.cosmosEvents = []fleetdb.CommsEvent{}
		model.rebuildCosmosTimeline()
		pressed, _ := applyKey(t, model, printableKey('['))
		text := ansi.Strip(pressed.renderCosmosPanel(80, 9))
		at := time.Unix(0, pressed.cosmosViewNS).Format("15:04:05")
		if !strings.Contains(text, "no chat traffic recorded before "+at) {
			t.Fatalf("panel = %q, want it to name the moment %q", text, at)
		}
		if strings.Contains(text, "no chat traffic recorded yet") {
			t.Fatal("empty replay read as the never-sampled-ledger message")
		}
		if strings.Contains(text, "unreachable") {
			t.Fatal("a healthy empty replay claimed the ledger was unreachable")
		}
	})

	t.Run("ledger health is a now-fact and rides over the replay", func(t *testing.T) {
		model := cosmosGoldenModel(80)
		model.cosmosEvents = []fleetdb.CommsEvent{}
		model.rebuildCosmosTimeline()
		pressed, _ := applyKey(t, model, printableKey('['))
		pressed.cosmos.Err = "sqlite: database is locked"
		text := ansi.Strip(pressed.renderCosmosPanel(80, 9))
		if !strings.Contains(text, "⚠ comms ledger unreachable: sqlite: database is locked") {
			t.Fatalf("panel = %q, want the unreachable banner even while replaying", text)
		}
		if strings.Contains(text, "no chat traffic recorded before") {
			t.Fatal("the unreachable banner did not take priority over the empty-past message")
		}
	})

	t.Run("a truncation warning survives the cut into the replay graph", func(t *testing.T) {
		model := cosmosGoldenModel(80)
		model.cosmosEvents = cosmosGoldenEvents(model.cosmosNowNS, model.rows)
		model.rebuildCosmosTimeline()
		model.cosmos.Warnings = append(model.cosmos.Warnings, compose.CosmosTruncationWarning)
		pressed, _ := applyKey(t, model, printableKey('['))
		if pressed.cosmosPast == nil {
			t.Fatal("[ did not enter replay")
		}
		found := false
		for _, warning := range pressed.cosmosPast.Warnings {
			if warning == compose.CosmosTruncationWarning {
				found = true
			}
		}
		if !found {
			t.Fatalf("replay graph did not inherit the live truncation warning: %#v", pressed.cosmosPast.Warnings)
		}
	})
}

// U7: TestNavigatorSelectionCyclesAndReconciles pins ↑/↓ selection order,
// wraparound, the reticle's HUD card, and the reconcile-not-retarget rule:
// a refresh that drops the selected node clears the reticle instead of
// jumping it to whatever remains.
func TestNavigatorSelectionCyclesAndReconciles(t *testing.T) {
	model := cosmosTestModel(120, true)
	model.tab = TabCosmos
	nodes := model.viewGraph().Nodes
	if len(nodes) != 2 {
		t.Fatalf("setup: want exactly 2 nodes, got %d: %#v", len(nodes), nodes)
	}

	down1, _ := applyKey(t, model, specialKey(tea.KeyDown))
	if down1.cosmosSelected != nodes[0].Key {
		t.Fatalf("first ↓ selected %q, want the first visible node %q", down1.cosmosSelected, nodes[0].Key)
	}
	down2, _ := applyKey(t, down1, specialKey(tea.KeyDown))
	if down2.cosmosSelected != nodes[1].Key {
		t.Fatalf("second ↓ selected %q, want the second node %q", down2.cosmosSelected, nodes[1].Key)
	}
	down3, _ := applyKey(t, down2, specialKey(tea.KeyDown))
	if down3.cosmosSelected != nodes[0].Key {
		t.Fatalf("third ↓ did not wrap: got %q, want %q", down3.cosmosSelected, nodes[0].Key)
	}

	fresh := cosmosTestModel(120, true)
	fresh.tab = TabCosmos
	up, _ := applyKey(t, fresh, specialKey(tea.KeyUp))
	if up.cosmosSelected != nodes[len(nodes)-1].Key {
		t.Fatalf("↑ from no selection = %q, want the last node %q", up.cosmosSelected, nodes[len(nodes)-1].Key)
	}

	hud := down2.cosmosSelectionHUD()
	if !strings.Contains(hud, "RR") || !strings.Contains(hud, "codex") || !strings.Contains(hud, "alpha") ||
		!strings.Contains(hud, "in 1") || !strings.Contains(hud, "out 0") {
		t.Fatalf("cosmosSelectionHUD() = %q, missing one of label, engine, home, in, out", hud)
	}

	newSnapshot := fixtureSnapshot(120)
	newSnapshot.Cosmos = compose.BuildCosmos(newSnapshot.Rows, []fleetdb.CommsEvent{{
		AtNS: fixtureNowNS, Kind: fleetdb.KindInject,
		SenderUUID: newSnapshot.Rows[1].ID, Target: newSnapshot.Rows[4].Name, ReceiverSocket: "",
		Message: "unrelated traffic",
	}}, fixtureNowNS, false)
	updated, _ := down1.Update(RefreshMsg{Snapshot: newSnapshot})
	refreshed := updated.(Model)
	if refreshed.cosmosSelected != "" {
		t.Fatalf(
			"a refresh that dropped the selected node left cosmosSelected = %q, want it cleared",
			refreshed.cosmosSelected,
		)
	}
	stillThere := false
	for _, node := range refreshed.cosmos.Nodes {
		if node.Key == nodes[1].Key {
			stillThere = true
		}
	}
	if !stillThere {
		t.Fatal("setup: the OTHER node should have survived the refresh, proving this was a reconcile, not a wipe")
	}
}

// U8: TestNavigatorEnterOpensTheSelectedRow pins `enter` as the Chats tab's
// own door reached from the sky: it opens a live row, refuses a ghost and a
// row the fleet no longer lists by name, and refuses silence when nothing
// is selected at all.
func TestNavigatorEnterOpensTheSelectedRow(t *testing.T) {
	model := cosmosTestModel(120, true)
	model.tab = TabCosmos
	var rrKey, rrRowKey string
	for _, node := range model.viewGraph().Nodes {
		if node.RowKey != "" {
			rrKey, rrRowKey = node.Key, node.RowKey
		}
	}
	if rrKey == "" {
		t.Fatal("setup: no node in the fixture graph carries a RowKey")
	}
	model.cosmosSelected = rrKey

	opened, cmd := applyKey(t, model, specialKey(tea.KeyEnter))
	if cmd == nil {
		t.Fatal("enter on a live node returned no command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("enter on a live node did not quit: cmd() = %#v", cmd())
	}
	result := opened.Result()
	if result.Kind != OutcomeSelected || result.Row.ID != rrRowKey {
		t.Fatalf("Result() = %#v, want OutcomeSelected with Row.ID %q", result, rrRowKey)
	}

	ghostModel := cosmosTestModel(120, true)
	ghostModel.tab = TabCosmos
	ghostModel.cosmos.Nodes = append(ghostModel.cosmos.Nodes, compose.CosmosNode{
		Key: "chat:name:ghost", Label: resolve.Named("ghost"),
	})
	ghostModel.cosmosSelected = "chat:name:ghost"
	refusedGhost, ghostCmd := applyKey(t, ghostModel, specialKey(tea.KeyEnter))
	if ghostCmd != nil {
		t.Fatal("enter on a ghost node returned a quit command")
	}
	if !strings.Contains(refusedGhost.cosmosStatus, "ghost") {
		t.Fatalf("cosmosStatus = %q, want it to name the ghost", refusedGhost.cosmosStatus)
	}
	if refusedGhost.outcome == OutcomeSelected {
		t.Fatal("enter on a ghost set OutcomeSelected anyway")
	}

	noneModel := cosmosTestModel(120, true)
	noneModel.tab = TabCosmos
	refusedNone, noneCmd := applyKey(t, noneModel, specialKey(tea.KeyEnter))
	if noneCmd != nil {
		t.Fatal("enter with nothing selected returned a quit command")
	}
	if !strings.Contains(refusedNone.cosmosStatus, "nothing selected") {
		t.Fatalf("cosmosStatus = %q, want it to name that nothing is selected", refusedNone.cosmosStatus)
	}
}

// TestOpenCosmosSelectionNamesTheGoneChatExactly pins openCosmosSelection's
// literal refusal text for a replay node with no RowKey — the ghost of a
// chat the fleet no longer lists at all, distinct from the row-since-closed
// refusal a few lines below it in chronoscope.go. This is the exact string
// an operator reads on the query row; a paraphrase drifting from it (e.g.
// the retired "is a ghost only the ledger remembers") would pass every
// substring check above without this one.
func TestOpenCosmosSelectionNamesTheGoneChatExactly(t *testing.T) {
	model := cosmosTestModel(120, true)
	model.tab = TabCosmos
	model.cosmosPast = &compose.CosmosGraph{Nodes: []compose.CosmosNode{
		{Key: "chat:name:vanished", Label: resolve.Named("Vanished"), Dead: true},
	}}
	model.cosmosSelected = "chat:name:vanished"

	refused, cmd := applyKey(t, model, specialKey(tea.KeyEnter))
	if cmd != nil {
		t.Fatal("enter on a replay ghost with no RowKey returned a quit command")
	}
	want := "enter needs a live chat — Vanished is gone; only the ledger remembers it"
	if refused.cosmosStatus != want {
		t.Fatalf("cosmosStatus = %q, want exactly %q", refused.cosmosStatus, want)
	}
}

// U9: TestNavigatorSpotlightDimsEveryOtherEdge pins the reticle's spotlight:
// selecting a node keeps ITS edges at full strength and dims every other
// edge to 0.45×, and draws a halo of light around the selected body.
func TestNavigatorSpotlightDimsEveryOtherEdge(t *testing.T) {
	snapshot := fixtureSnapshot(120)
	snapshot.NoSky = true // isolate the plain rail Bezier from comet/wind/twinkle noise
	rows := snapshot.Rows
	touching := fleetdb.CommsEvent{
		AtNS: fixtureNowNS - int64(time.Minute), Kind: fleetdb.KindInject,
		SenderUUID: rows[0].ID, Target: rows[1].Name, ReceiverSocket: rows[1].Socket,
		Message: "touches the selection",
	}
	unrelated := fleetdb.CommsEvent{
		AtNS: fixtureNowNS - int64(time.Minute), Kind: fleetdb.KindInject,
		SenderUUID: rows[4].ID, Target: rows[5].Name,
		Message: "does not touch the selection",
	}
	snapshot.Cosmos = compose.BuildCosmos(rows, []fleetdb.CommsEvent{touching, unrelated}, fixtureNowNS, false)
	model := NewModel(snapshot)
	model.tab = TabCosmos
	selectedKey := "chat:id:" + rows[0].ID

	graph := model.cosmos
	nodes := cosmosNodeMap(graph.Nodes)
	now := time.Unix(0, model.cosmosNowNS)
	frame := cosmosLayout(NewCanvas(114, 22), model.cosmosSeats, nodes, graph.Edges, now, false, false, "")

	var touchingEdge, unrelatedEdge compose.CosmosEdge
	for _, edge := range graph.Edges {
		if edge.From == selectedKey || edge.To == selectedKey {
			touchingEdge = edge
		} else {
			unrelatedEdge = edge
		}
	}
	if touchingEdge.Kind == "" || unrelatedEdge.Kind == "" {
		t.Fatalf("setup: expected one touching and one unrelated edge: %#v", graph.Edges)
	}

	midpoint := func(edge compose.CosmosEdge) (int, int) {
		fp, tp := frame.points[edge.From], frame.points[edge.To]
		rail := cosmosEdgeRail(fp, tp, frame.cx, frame.cy)
		x0, y0, cpx, cpy, x1, y1 := rail.x0, rail.y0, rail.cpx, rail.cpy, rail.x1, rail.y1
		mx, my := BezierPoint(x0, y0, cpx, cpy, x1, y1, 0.5)
		return int(mx), int(my)
	}
	touchIdx := func() int { x, y := midpoint(touchingEdge); return (y/4)*114 + x/2 }()
	unrelatedIdx := func() int { x, y := midpoint(unrelatedEdge); return (y/4)*114 + x/2 }()

	baseline := NewCanvas(114, 22)
	model.drawCosmosUniverse(baseline, graph, now, now)

	selected := model
	selected.cosmosSelected = selectedKey
	spotlighted := NewCanvas(114, 22)
	selected.drawCosmosUniverse(spotlighted, graph, now, now)

	if spotlighted.dotLuma[touchIdx] != baseline.dotLuma[touchIdx] {
		t.Fatalf("the selected node's OWN edge changed brightness when selected: before=%v after=%v",
			baseline.dotLuma[touchIdx], spotlighted.dotLuma[touchIdx])
	}
	if spotlighted.dotLuma[unrelatedIdx] >= baseline.dotLuma[unrelatedIdx] {
		t.Fatalf("an edge NOT touching the selection was not dimmed: before=%v after=%v",
			baseline.dotLuma[unrelatedIdx], spotlighted.dotLuma[unrelatedIdx])
	}

	point := frame.points[selectedKey]
	haloHits := 0
	for index := 0; index < 12; index++ {
		angle := 2 * math.Pi * float64(index) / 12
		px := int(point.x + 2.2*math.Cos(angle)*1.6)
		py := int(point.y + 2.2*math.Sin(angle))
		if px < 0 || py < 0 || px >= spotlighted.PW() || py >= spotlighted.PH() {
			continue
		}
		idx := (py/4)*spotlighted.Cols + px/2
		if spotlighted.dots[idx] != 0 {
			haloHits++
		}
	}
	if haloHits < 6 {
		t.Fatalf("halo dots around the selected node = %d/12 hit, want most of the ring lit", haloHits)
	}
}

// U10: TestSystemFocusCyclesAndPushesOtherStarsOffFrame pins `s`: the
// focused star takes the galactic centre, every OTHER system's chats move
// off-canvas (never simply vanish), and the subheader counts what is drawn.
func TestSystemFocusCyclesAndPushesOtherStarsOffFrame(t *testing.T) {
	snapshot := fixtureSnapshot(120)
	rows := snapshot.Rows
	intraAlpha := fleetdb.CommsEvent{
		AtNS: fixtureNowNS, Kind: fleetdb.KindInject,
		SenderUUID: rows[0].ID, Target: rows[1].Name, ReceiverSocket: rows[1].Socket,
		Message: "inside alpha",
	}
	crossToBeta := fleetdb.CommsEvent{
		AtNS: fixtureNowNS, Kind: fleetdb.KindSpawn,
		SenderUUID: rows[0].ID, Target: rows[4].Name,
		Message: "leaves for beta",
	}
	snapshot.Cosmos = compose.BuildCosmos(rows, []fleetdb.CommsEvent{intraAlpha, crossToBeta}, fixtureNowNS, false)
	model := NewModel(snapshot)
	model.tab = TabCosmos

	focused, _ := applyKey(t, model, printableKey('s'))
	if focused.cosmosFocus != "alpha" {
		t.Fatalf("first s focused %q, want the first sorted home %q", focused.cosmosFocus, "alpha")
	}

	canvas := NewCanvas(114, 22)
	now := time.Unix(0, focused.cosmosNowNS)
	graph := focused.viewGraph()
	nodes := cosmosNodeMap(graph.Nodes)
	frame := cosmosLayout(
		canvas,
		focused.cosmosSeats,
		nodes,
		graph.Edges,
		now,
		focused.skyEnabled,
		focused.classicSky,
		focused.cosmosFocus,
	)
	if frame.starPoints["alpha"] != (cosmosPoint{x: frame.cx, y: frame.cy}) {
		t.Fatalf("focused star = %#v, want the galactic centre (%v,%v)", frame.starPoints["alpha"], frame.cx, frame.cy)
	}
	for _, node := range graph.Nodes {
		if node.Home != "beta" {
			continue
		}
		if !frame.hidden[node.Key] {
			t.Fatalf("node %q of the unfocused system beta was not marked hidden", node.Key)
		}
		point := frame.points[node.Key]
		if point.x >= 0 && point.x < float64(canvas.PW()) && point.y >= 0 && point.y < float64(canvas.PH()) {
			t.Fatalf("hidden node %q still landed inside the frame: %#v", node.Key, point)
		}
	}

	want := " cosmos · ⌖ alpha · 2 chats · 1 edges · 1 leave the system · last 24h"
	if got := focused.renderCosmosSubheader(); got != want {
		t.Fatalf("subheader = %q, want %q", got, want)
	}

	toBeta, _ := applyKey(t, focused, printableKey('s'))
	if toBeta.cosmosFocus != "beta" {
		t.Fatalf("second s = %q, want the next sorted home %q", toBeta.cosmosFocus, "beta")
	}
	toWhole, _ := applyKey(t, toBeta, printableKey('s'))
	if toWhole.cosmosFocus != "" {
		t.Fatalf("s past the last home = %q, want the whole sky \"\"", toWhole.cosmosFocus)
	}
}

// U11: TestKeplerRingsSplitACrowdedSystemByActivity pins the ring split: a
// system past cosmosRingCap planets splits by activity, the busiest six
// take the inner track, physics slows the outer ring, a ring CHANGE eases
// rather than teleports, and the inner ring's revolution accumulates faster.
func TestKeplerRingsSplitACrowdedSystemByActivity(t *testing.T) {
	model := cosmosTestModel(120, true)
	nodes := make([]compose.CosmosNode, 9)
	for i := range nodes {
		name := "crowd" + string(rune('0'+i))
		nodes[i] = compose.CosmosNode{
			Key:    "chat:name:" + name,
			Label:  resolve.Named(name),
			Home:   "crowded",
			LastNS: int64(8 - i), // crowd0 newest ... crowd8 oldest
		}
	}
	model.cosmos.Nodes = nodes
	model.cosmos.Edges = nil
	model.mergeCosmosSeats()

	innerFactor := cosmosRingFactor(0, 2)
	outerFactor := cosmosRingFactor(1, 2)
	for i, node := range nodes {
		seat := model.cosmosSeats[node.Key]
		if seat == nil {
			t.Fatalf("no seat for %q", node.Key)
		}
		want := innerFactor
		if i >= 6 {
			want = outerFactor
		}
		if seat.RingTarget != want {
			t.Fatalf("node %d (LastNS=%d) RingTarget = %v, want %v", i, node.LastNS, seat.RingTarget, want)
		}
	}
	if cosmosRingSpeed(innerFactor) <= cosmosRingSpeed(outerFactor) {
		t.Fatalf(
			"inner ring speed %v should exceed outer ring speed %v (Kepler III)",
			cosmosRingSpeed(innerFactor),
			cosmosRingSpeed(outerFactor),
		)
	}

	canvas := NewCanvas(114, 22)
	now := time.Unix(0, model.cosmosNowNS)
	frame := cosmosLayout(canvas, model.cosmosSeats, cosmosNodeMap(nodes), nil, now, false, false, "")
	rings := frame.rings["crowded"]
	if len(rings) != 2 || rings[0] != innerFactor || rings[1] != outerFactor {
		t.Fatalf("rings[\"crowded\"] = %v, want [%v %v] sorted ascending", rings, innerFactor, outerFactor)
	}

	// Orbit accumulates faster on the inner ring: advance both an inner and
	// an outer seat by the same dt and compare how far each revolved.
	inner, outer := model.cosmosSeats[nodes[0].Key], model.cosmosSeats[nodes[8].Key]
	innerBefore, outerBefore := inner.Orbit, outer.Orbit
	model.advanceCosmos(model.cosmosNowNS + int64(time.Second))
	if delta := inner.Orbit - innerBefore; delta <= outer.Orbit-outerBefore {
		t.Fatalf("inner ring orbit delta %v did not exceed outer ring orbit delta %v", delta, outer.Orbit-outerBefore)
	}

	// A promotion to a new ring eases (Ring moves toward RingTarget) instead
	// of teleporting: demote the hottest ring back to the outer track and
	// re-merge, then check the physical Ring lags RingTarget right after.
	hotKey := nodes[0].Key
	promoted := model.cosmosSeats[hotKey]
	if promoted.Ring != promoted.RingTarget {
		t.Fatalf("setup: a freshly created seat should start settled: %#v", promoted)
	}
	nodes[0].LastNS = -100 // now the quietest: demoted to the outer ring
	model.cosmos.Nodes = nodes
	model.mergeCosmosSeats()
	reseated := model.cosmosSeats[hotKey]
	if reseated.RingTarget != outerFactor {
		t.Fatalf("setup: demoted node RingTarget = %v, want %v", reseated.RingTarget, outerFactor)
	}
	if reseated.Ring == reseated.RingTarget {
		t.Fatalf(
			"a ring change snapped instantly instead of easing: Ring already equals RingTarget (%v)",
			reseated.Ring,
		)
	}
	model.advanceCosmos(model.cosmosNowNS + int64(200*time.Millisecond))
	if reseated.Ring == innerFactor || reseated.Ring == outerFactor {
		t.Fatalf(
			"after a small tick the seat already reached an endpoint (%v) instead of easing partway between %v and %v",
			reseated.Ring,
			innerFactor,
			outerFactor,
		)
	}
}

// U12: TestLabelNudgeStepsAroundTextAndNeverLeavesTheCanvas pins
// Canvas.TextFree's collision table and the label nudge it drives: a
// colliding label steps to another row, and a label pinned at row 0 can
// never nudge to the nonexistent row -1 — it accepts the overlap instead of
// vanishing.
func TestLabelNudgeStepsAroundTextAndNeverLeavesTheCanvas(t *testing.T) {
	t.Run("TextFree table", func(t *testing.T) {
		canvas := NewCanvas(10, 5)
		canvas.Text(2, 2, "AB", RGB{1, 1, 1}, false)
		canvas.Dot(0, 0, RGB{1, 1, 1}) // braille-only cell in row 0

		tests := []struct {
			name    string
			x, y, n int
			want    bool
		}{
			{"a free row is free", 5, 2, 2, true},
			{"an occupied row is not free", 2, 2, 2, false},
			{"a braille-only cell counts as free", 0, 0, 1, true},
			{"a row below the canvas counts as free", 0, 99, 1, true},
			{"a row above the canvas counts as free", 0, -1, 1, true},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				if got := canvas.TextFree(test.x, test.y, test.n); got != test.want {
					t.Fatalf("TextFree(%d,%d,%d) = %v, want %v", test.x, test.y, test.n, got, test.want)
				}
			})
		}
	})

	t.Run("colliding labels land on different rows", func(t *testing.T) {
		model := cosmosTestModel(60, true)
		model.classicSky = true
		model.cosmos.Nodes = []compose.CosmosNode{
			{Key: "chat:name:AAAAAAAAAAAAAA", Label: resolve.Named("AAAAAAAAAAAAAA")},
			{Key: "chat:name:BBBBBBBBBBBBBB", Label: resolve.Named("BBBBBBBBBBBBBB")},
		}
		model.cosmos.Edges = nil
		model.mergeCosmosSeats()
		for _, seat := range model.cosmosSeats {
			seat.Angle, seat.Target = 0, 0 // force both nodes onto the exact same point
		}

		canvas := NewCanvas(60, 20)
		now := time.Unix(0, model.cosmosNowNS)
		model.drawCosmosUniverse(canvas, model.cosmos, now, now)
		lines := strings.Split(ansi.Strip(canvas.render()), "\n")

		lineOf := func(needle string) int {
			for index, line := range lines {
				if strings.Contains(line, needle) {
					return index
				}
			}
			return -1
		}
		rowA, rowB := lineOf("AAAA"), lineOf("BBBB")
		if rowA < 0 || rowB < 0 {
			t.Fatalf(
				"one of the colliding labels never rendered at all: rowA=%d rowB=%d\n%s",
				rowA,
				rowB,
				strings.Join(lines, "\n"),
			)
		}
		if rowA == rowB {
			t.Fatalf("colliding labels landed on the SAME row %d instead of nudging apart", rowA)
		}
	})

	t.Run("a label pinned at row 0 accepts the overlap instead of vanishing", func(t *testing.T) {
		model := cosmosTestModel(60, true)
		model.classicSky = true
		model.cosmos.Nodes = []compose.CosmosNode{
			{Key: "chat:name:AAAA", Label: resolve.Named("AAAA")},
			{Key: "chat:name:BBBB", Label: resolve.Named("BBBB")},
			{Key: "chat:name:CCCC", Label: resolve.Named("CCCC")},
		}
		model.cosmos.Edges = nil
		model.mergeCosmosSeats()
		for _, seat := range model.cosmosSeats {
			seat.Angle, seat.Target = -math.Pi/2, -math.Pi/2 // top of the ellipse
		}

		canvas := NewCanvas(60, 7) // short enough that the top seat lands on row 0
		now := time.Unix(0, model.cosmosNowNS)
		model.drawCosmosUniverse(canvas, model.cosmos, now, now)
		frame := cosmosLayout(canvas, model.cosmosSeats, cosmosNodeMap(model.cosmos.Nodes), nil, now, false, true, "")
		point := frame.points["chat:name:AAAA"]
		colY := int(point.y) / 4
		if colY != 0 {
			t.Skipf(
				"setup: forced point landed on row %d, not row 0 — geometry drifted, adjust the canvas height",
				colY,
			)
		}
		lines := strings.Split(ansi.Strip(canvas.render()), "\n")
		found := false
		for _, line := range lines {
			if strings.Contains(line, "CCCC") {
				found = true
			}
		}
		if !found {
			t.Fatalf(
				"the third colliding label at row 0 vanished instead of accepting the overlap:\n%s",
				strings.Join(lines, "\n"),
			)
		}
	})
}

// U13: TestCosmosLegendNamesOnlyWhatTheSkyDraws pins the top-right legend:
// the orbital sky names the sun glyph, the classic sky (no suns) does not,
// and a canvas too narrow for the legend plus its margin draws none of it.
func TestCosmosLegendNamesOnlyWhatTheSkyDraws(t *testing.T) {
	orbital := ansi.Strip(cosmosGoldenModel(80).renderCosmosPanel(80, 9))
	if !strings.Contains(orbital, "✹ project") {
		t.Fatalf("orbital legend missing the sun glyph:\n%s", orbital)
	}
	classic := ansi.Strip(cosmosClassicGoldenModel(80).renderCosmosPanel(80, 9))
	if strings.Contains(classic, "✹ project") {
		t.Fatalf("classic legend still names the sun glyph:\n%s", classic)
	}
	if !strings.Contains(classic, "◆ opencode") {
		t.Fatalf("classic legend dropped the engine glyphs entirely:\n%s", classic)
	}
	narrow := ansi.Strip(cosmosGoldenModel(80).renderCosmosPanel(60, 9))
	if strings.Contains(narrow, "claude  ▲ codex") {
		t.Fatalf("a canvas too narrow for the legend drew it anyway:\n%s", narrow)
	}
}

// U14: TestCosmosKeysNeverGoSilent pins the no-silent-keystroke contract for
// the cosmos tab's own keys: on an empty sky, every refusal names itself on
// cosmosStatus, and the two unconditional keys (o, n) still change state.
func TestCosmosKeysNeverGoSilent(t *testing.T) {
	refusals := []string{"up", "down", "enter", "s", "[", "]", "{", "}", "space"}
	for _, key := range refusals {
		t.Run(key, func(t *testing.T) {
			model := NewModel(fixtureSnapshot(80))
			model.tab = TabCosmos
			pressed, _ := applyKey(t, model, specialOrPrintable(key))
			if pressed.cosmosStatus == "" {
				t.Fatalf("key %q on an empty sky went silent: no cosmosStatus set", key)
			}
		})
	}

	t.Run("o", func(t *testing.T) {
		model := NewModel(fixtureSnapshot(80))
		model.tab = TabCosmos
		pressed, _ := applyKey(t, model, printableKey('o'))
		if pressed.classicSky == model.classicSky {
			t.Fatal("o on an empty sky did not toggle classicSky")
		}
	})

	t.Run("n", func(t *testing.T) {
		model := NewModel(fixtureSnapshot(80))
		model.tab = TabCosmos
		model.cosmosPast = &compose.CosmosGraph{}
		model.cosmosViewNS = model.cosmosNowNS - int64(5*time.Minute)
		pressed, _ := applyKey(t, model, printableKey('n'))
		if pressed.cosmosPast != nil {
			t.Fatal("n on a scrubbed empty sky did not return to live")
		}
	})
}

// specialOrPrintable builds the right tea.KeyMsg for a cosmos key string,
// matching how the model itself would receive it from a real keyboard.
func specialOrPrintable(key string) tea.KeyPressMsg {
	switch key {
	case "up":
		return specialKey(tea.KeyUp)
	case "down":
		return specialKey(tea.KeyDown)
	case "enter":
		return specialKey(tea.KeyEnter)
	case "space":
		return printableKey(' ')
	default:
		return printableKey(rune(key[0]))
	}
}

// TestNavigatorEnterOpensALiveSplitRowWithNoPaneID is F1's ui-side
// regression: a live, running LiveSplit row (fixtureSnapshot's row 2 — a
// real multi-pane tmux session, Socket set, ID always empty) must resolve
// to a node whose RowKey joins back to that exact row, must open on enter,
// and its HUD preview must never carry the ghost suffix.
func TestNavigatorEnterOpensALiveSplitRowWithNoPaneID(t *testing.T) {
	snapshot := fixtureSnapshot(120)
	rows := snapshot.Rows
	splitRow := rows[2]
	if splitRow.Kind != compose.LiveSplit || splitRow.ID != "" || splitRow.Socket == "" {
		t.Fatalf("setup: fixture row 2 must be a LiveSplit row with a socket and no pane ID: %#v", splitRow)
	}
	touching := fleetdb.CommsEvent{
		AtNS: fixtureNowNS, Kind: fleetdb.KindInject,
		SenderUUID: rows[0].ID, Target: splitRow.Name, ReceiverSocket: splitRow.Socket,
		Message: "hello split",
	}
	snapshot.Cosmos = compose.BuildCosmos(rows, []fleetdb.CommsEvent{touching}, fixtureNowNS, false)
	model := NewModel(snapshot)
	model.tab = TabCosmos

	splitNode, found := cosmosNodeByKey(model.cosmos, "chat:pane:"+splitRow.Socket+"\t")
	if !found {
		t.Fatalf("setup: the split row never resolved into a node: %#v", model.cosmos.Nodes)
	}
	wantRowKey := compose.RowKey(splitRow)
	if splitNode.RowKey == "" || splitNode.RowKey != wantRowKey {
		t.Fatalf(
			"split node RowKey = %q, want the row's own join key %q — a live row must never carry an empty RowKey",
			splitNode.RowKey,
			wantRowKey,
		)
	}

	model.cosmosSelected = splitNode.Key
	opened, cmd := applyKey(t, model, specialKey(tea.KeyEnter))
	if cmd == nil {
		t.Fatal("enter on a live split row returned no command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("enter on a live split row did not quit: cmd() = %#v", cmd())
	}
	result := opened.Result()
	if result.Kind != OutcomeSelected || compose.RowKey(result.Row) != wantRowKey {
		t.Fatalf("Result() = %#v, want OutcomeSelected with the split row (join key %q)", result, wantRowKey)
	}

	hud := model.cosmosSelectionHUD()
	if strings.Contains(hud, "ghost") {
		t.Fatalf("a live, running split row's HUD read as a ghost: %q", hud)
	}
}

// TestCosmosSelectionHUDNamesAllFourLifecycleStates pins F2: the HUD's
// switch keys the "not running" suffix off !node.Live (or node.Dead), never
// node.Dead alone — a resolved, non-killed chat with no active socket (an
// idle/resumable session) is neither a ghost nor Dead, and before the fix it
// fell through both switch arms and rendered indistinguishably from a chat
// actively running right now.
func TestCosmosSelectionHUDNamesAllFourLifecycleStates(t *testing.T) {
	snapshot := fixtureSnapshot(120)
	rows := snapshot.Rows
	events := []fleetdb.CommsEvent{
		{
			AtNS:           fixtureNowNS,
			Kind:           fleetdb.KindInject,
			SenderUUID:     rows[0].ID,
			Target:         rows[1].Name,
			ReceiverSocket: rows[1].Socket,
			Message:        "live and running",
		},
		{
			AtNS:       fixtureNowNS,
			Kind:       fleetdb.KindInject,
			SenderUUID: rows[4].ID,
			Target:     "resumable reply",
			Message:    "from a resumable row",
		},
	}
	snapshot.Cosmos = compose.BuildCosmos(rows, events, fixtureNowNS, false)
	model := NewModel(snapshot)
	model.tab = TabCosmos
	// The ghost and killed cases are appended by hand rather than routed
	// through a fresh model's own applyCosmosGraph: a Dead node never
	// witnessed alive is correctly DROPPED on first sight (see
	// TestApplyCosmosGraphDropsAnUnwitnessedDeadNodeOnFirstSight) — a
	// separate, already-covered rule this test is not re-proving. What this
	// test pins is cosmosSelectionHUD's own switch, given the exact node
	// shapes chatNode produces for each of the four lifecycle states.
	if !rows[3].Killed {
		t.Fatal("setup: fixture row 3 must be Killed for the killed-state case")
	}
	if rows[4].Socket != "" {
		t.Fatal("setup: fixture row 4 (ResumeClaude) must carry no socket for the not-running case")
	}
	model.cosmos.Nodes = append(model.cosmos.Nodes,
		compose.CosmosNode{Key: "chat:name:ghost", Label: resolve.Named("ghost")},
		compose.CosmosNode{
			Key: "chat:id:" + rows[3].ID, Label: resolve.Named(rows[3].Name),
			RowKey: compose.RowKey(rows[3]), Killed: true, Dead: true, Live: rows[3].Socket != "",
		},
	)

	tests := []struct {
		name    string
		key     string
		want    string
		exclude []string
	}{
		{"ghost", "chat:name:ghost", "· ghost", []string{"· killed", "· not running"}},
		{"killed", "chat:id:" + rows[3].ID, "· killed", []string{"· ghost", "· not running"}},
		{
			"not running: resolved, not killed, no live socket",
			"chat:id:" + rows[4].ID,
			"· not running",
			[]string{"· ghost", "· killed"},
		},
		{
			"live and running: no suffix at all",
			"chat:id:" + rows[1].ID,
			"",
			[]string{"· ghost", "· killed", "· not running"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			m := model
			m.cosmosSelected = test.key
			hud := m.cosmosSelectionHUD()
			if hud == "" {
				t.Fatalf("setup: no HUD rendered for key %q", test.key)
			}
			if test.want != "" && !strings.Contains(hud, test.want) {
				t.Fatalf("cosmosSelectionHUD() = %q, want it to contain %q", hud, test.want)
			}
			for _, excluded := range test.exclude {
				if strings.Contains(hud, excluded) {
					t.Fatalf("cosmosSelectionHUD() = %q, must not contain %q", hud, excluded)
				}
			}
		})
	}
}

// TestNoSkyChronoscopeClockTracksTheRefreshSnapshot pins F3: under --no-sky
// no animation tick ever runs, so model.cosmosNowNS must adopt the refresh
// stream's own snapshot clock (adoptCosmosClock, wired from applyRefresh) or
// the chronoscope's idea of "now" freezes at launch — a scrub then measures
// its window against a stale mark and a replay can never reach real history
// the sampler already holds.
func TestNoSkyChronoscopeClockTracksTheRefreshSnapshot(t *testing.T) {
	model := cosmosGoldenModel(80) // NoSky: skyEnabled is false, no tick ever fires
	if model.skyEnabled {
		t.Fatal("setup: cosmosGoldenModel is expected to be --no-sky")
	}
	model.cosmosEvents = cosmosGoldenEvents(model.cosmosNowNS, model.rows)
	model.rebuildCosmosTimeline()
	launchNowNS := model.cosmosNowNS

	refreshAt := func(nowNS int64) Snapshot {
		snapshot := cosmosGoldenSnapshot(80, true)
		snapshot.NowNS = nowNS
		snapshot.Cosmos = compose.BuildCosmos(snapshot.Rows, cosmosGoldenEvents(nowNS, snapshot.Rows), nowNS, false)
		return snapshot
	}

	t.Run("a live refresh advances both now and the live playhead", func(t *testing.T) {
		newNowNS := launchNowNS + int64(20*time.Minute)
		updated, _ := model.Update(RefreshMsg{Snapshot: refreshAt(newNowNS)})
		refreshed := updated.(Model)
		if refreshed.cosmosNowNS != newNowNS {
			t.Fatalf("cosmosNowNS = %d, want the fresher snapshot clock %d", refreshed.cosmosNowNS, newNowNS)
		}
		if refreshed.cosmosViewNS != newNowNS {
			t.Fatalf("cosmosViewNS = %d, want it to follow the live clock to %d", refreshed.cosmosViewNS, newNowNS)
		}

		scrubbed, _ := applyKey(t, refreshed, printableKey('['))
		want := newNowNS - int64(cosmosScrubFine)
		if scrubbed.cosmosViewNS != want {
			t.Fatalf(
				"cosmosViewNS after [ = %d, want %d (5m back from the FRESH now, not the launch-time now)",
				scrubbed.cosmosViewNS,
				want,
			)
		}
	})

	t.Run("an older snapshot clock never rewinds it", func(t *testing.T) {
		advanced := launchNowNS + int64(20*time.Minute)
		updated, _ := model.Update(RefreshMsg{Snapshot: refreshAt(advanced)})
		refreshed := updated.(Model)

		older := advanced - int64(5*time.Minute)
		rewound, _ := refreshed.Update(RefreshMsg{Snapshot: refreshAt(older)})
		result := rewound.(Model)
		if result.cosmosNowNS != advanced {
			t.Fatalf(
				"cosmosNowNS = %d, an OLDER snapshot clock (%d) rewound it from %d",
				result.cosmosNowNS,
				older,
				advanced,
			)
		}
	})

	t.Run("while replaying, a refresh advances now but leaves the playhead alone", func(t *testing.T) {
		replaying, _ := applyKey(t, model, printableKey('['))
		if replaying.cosmosPast == nil {
			t.Fatal("setup: [ did not enter replay")
		}
		viewBefore := replaying.cosmosViewNS

		newNowNS := launchNowNS + int64(20*time.Minute)
		updated, _ := replaying.Update(RefreshMsg{Snapshot: refreshAt(newNowNS)})
		refreshed := updated.(Model)
		if refreshed.cosmosNowNS != newNowNS {
			t.Fatalf(
				"cosmosNowNS = %d, want the fresher snapshot clock %d even while replaying",
				refreshed.cosmosNowNS,
				newNowNS,
			)
		}
		if refreshed.cosmosViewNS != viewBefore {
			t.Fatalf(
				"cosmosViewNS = %d, a refresh mid-replay must leave the playhead exactly where it was (%d)",
				refreshed.cosmosViewNS,
				viewBefore,
			)
		}
	})
}

// TestRenderCompactCosmosFocusFiltersEdgesToTheSystem pins F4: the compact
// fallback (used whenever width < 20 || height < 8) must narrow through the
// same focus-filtered node set the full-size panel and subheader already
// use — before the fix it read graph.Edges unfiltered, so a focused system's
// compact panel could list a conversation belonging entirely to another
// system.
func TestRenderCompactCosmosFocusFiltersEdgesToTheSystem(t *testing.T) {
	model := cosmosTestModel(19, true)
	model.cosmos = compose.CosmosGraph{
		Nodes: []compose.CosmosNode{
			{Key: "chat:name:Aone", Label: resolve.Named("Aone"), Home: "alpha"},
			{Key: "chat:name:Atwo", Label: resolve.Named("Atwo"), Home: "alpha"},
			{Key: "chat:name:Bone", Label: resolve.Named("Bone"), Home: "beta"},
			{Key: "chat:name:Btwo", Label: resolve.Named("Btwo"), Home: "beta"},
		},
		Edges: []compose.CosmosEdge{
			{
				From:        "chat:name:Aone",
				To:          "chat:name:Atwo",
				Kind:        fleetdb.KindInject,
				Count:       1,
				LastNS:      fixtureNowNS,
				LastMessage: "alpha chatter",
			},
			{
				From:        "chat:name:Bone",
				To:          "chat:name:Btwo",
				Kind:        fleetdb.KindInject,
				Count:       1,
				LastNS:      fixtureNowNS,
				LastMessage: "beta chatter",
			},
		},
	}
	model.cosmosFocus = "alpha"

	panel := ansi.Strip(model.renderCosmosPanel(19, 7))
	if !strings.Contains(panel, "Aone") || !strings.Contains(panel, "Atwo") {
		t.Fatalf("compact panel dropped the focused system's own edge:\n%s", panel)
	}
	if strings.Contains(panel, "Bone") || strings.Contains(panel, "Btwo") {
		t.Fatalf("compact panel listed an edge entirely inside another system while focused on alpha:\n%s", panel)
	}
}

// TestNavigatorSpotlightDimsTheCometBurstAndShockwaveRing extends U9
// (TestNavigatorSpotlightDimsEveryOtherEdge, which deliberately runs
// --no-sky to isolate the plain rail) into the two sky-only draw passes that
// rail-only test cannot reach: F5's fix routes the comet's head-burst dots
// and the arrival/departure shockwave ring through the SAME spotlight()
// closure the rail and ambient-wind passes already used, so an edge the
// reticle does not touch dims there too, not just on its rail.
func TestNavigatorSpotlightDimsTheCometBurstAndShockwaveRing(t *testing.T) {
	snapshot := fixtureSnapshot(120)
	snapshot.NoSky = false // sky ON: the burst and shockwave passes are sky-only
	rows := snapshot.Rows
	givesSelectionItsOwnNode := fleetdb.CommsEvent{
		AtNS: fixtureNowNS - int64(10*time.Minute), Kind: fleetdb.KindInject,
		SenderUUID: rows[0].ID, Target: rows[1].Name, ReceiverSocket: rows[1].Socket,
		Message: "old, not mid-comet, gives the selection its own node",
	}
	unrelated := fleetdb.CommsEvent{
		AtNS: fixtureNowNS - int64(300*time.Millisecond), Kind: fleetdb.KindInject,
		SenderUUID: rows[4].ID, Target: rows[5].Name,
		Message: "mid-comet, does not touch the selection",
	}
	snapshot.Cosmos = compose.BuildCosmos(
		rows,
		[]fleetdb.CommsEvent{givesSelectionItsOwnNode, unrelated},
		fixtureNowNS,
		false,
	)
	model := NewModel(snapshot)
	model.tab = TabCosmos
	selectedKey := "chat:id:" + rows[0].ID

	graph := model.cosmos
	nodes := cosmosNodeMap(graph.Nodes)
	now := time.Unix(0, model.cosmosNowNS)
	frame := cosmosLayout(NewCanvas(114, 22), model.cosmosSeats, nodes, graph.Edges, now, true, false, "")

	var edge compose.CosmosEdge
	found := false
	for _, candidate := range graph.Edges {
		if candidate.From == selectedKey || candidate.To == selectedKey {
			continue
		}
		edge = candidate
		found = true
	}
	if !found {
		t.Fatalf("setup: expected an edge that does not touch the selection: %#v", graph.Edges)
	}

	sumLuma := func(canvas *Canvas, colCenter, rowCenter, radius int) float64 {
		total := 0.0
		for dy := -radius; dy <= radius; dy++ {
			for dx := -radius; dx <= radius; dx++ {
				x, y := colCenter+dx, rowCenter+dy
				if x < 0 || y < 0 || x >= canvas.Cols || y >= canvas.Rows {
					continue
				}
				total += canvas.dotLuma[y*canvas.Cols+x]
			}
		}
		return total
	}

	baseline := NewCanvas(114, 22)
	model.drawCosmosUniverse(baseline, graph, now, now)
	selected := model
	selected.cosmosSelected = selectedKey
	spotlighted := NewCanvas(114, 22)
	selected.drawCosmosUniverse(spotlighted, graph, now, now)

	t.Run("comet head burst", func(t *testing.T) {
		duration := cosmosCometDuration(edge.Kind)
		age := now.Sub(time.Unix(0, edge.LastNS))
		tt := ease(float64(age) / float64(duration))
		fp, tp := frame.points[edge.From], frame.points[edge.To]
		rail := cosmosEdgeRail(fp, tp, frame.cx, frame.cy)
		x0, y0, cpx, cpy, x1, y1 := rail.x0, rail.y0, rail.cpx, rail.cpy, rail.x1, rail.y1
		hx, hy := BezierPoint(x0, y0, cpx, cpy, x1, y1, tt)
		col, row := int(hx)/2, int(hy)/4

		baseSum := sumLuma(baseline, col, row, 1)
		spotSum := sumLuma(spotlighted, col, row, 1)
		if baseSum <= 0 {
			t.Fatalf("setup: no light landed near the comet head at col=%d row=%d: baseline sum=%v", col, row, baseSum)
		}
		if spotSum >= baseSum {
			t.Fatalf("comet head burst was not dimmed by the spotlight: baseline=%v spotlighted=%v", baseSum, spotSum)
		}
	})

	t.Run("arrival shockwave ring", func(t *testing.T) {
		point, ok := frame.points[edge.To]
		if !ok {
			t.Fatal("setup: no seated point for the comet's receiver")
		}
		col, row := int(point.x)/2, int(point.y)/4

		baseSum := sumLuma(baseline, col, row, 3)
		spotSum := sumLuma(spotlighted, col, row, 3)
		if baseSum <= 0 {
			t.Fatalf(
				"setup: no light landed near the shockwave ring at col=%d row=%d: baseline sum=%v",
				col,
				row,
				baseSum,
			)
		}
		if spotSum >= baseSum {
			t.Fatalf(
				"the arrival shockwave ring was not dimmed by the spotlight: baseline=%v spotlighted=%v",
				baseSum,
				spotSum,
			)
		}
	})
}

// TestRenderCompactCosmosEmptyStatesAndHeightBound pins F6: the compact
// fallback shares cosmosEmptyState with the full panel, so a replay before
// the first event names the MOMENT ("recorded before HH:MM:SS"), never the
// "recorded yet" wording reserved for a ledger that has truly never talked;
// a focused-but-empty system names itself too; and the panel it returns
// never exceeds the requested innerHeight, however many edges compete for
// the space.
func TestRenderCompactCosmosEmptyStatesAndHeightBound(t *testing.T) {
	t.Run("replay before the first event names the moment, not \"yet\"", func(t *testing.T) {
		model := cosmosGoldenModel(80)
		model.cosmosEvents = []fleetdb.CommsEvent{}
		model.rebuildCosmosTimeline()
		pressed, _ := applyKey(t, model, printableKey('['))
		// width 80 keeps the message unabbreviated; height 7 (<8) is what
		// forces the compact fallback rather than the full-size panel.
		text := ansi.Strip(pressed.renderCosmosPanel(80, 7))
		at := time.Unix(0, pressed.cosmosViewNS).Format("15:04:05")
		if !strings.Contains(text, "no chat traffic recorded before "+at) {
			t.Fatalf("compact panel = %q, want it to name the moment %q", text, at)
		}
		if strings.Contains(text, "recorded yet") {
			t.Fatal("compact replay read as the never-sampled-ledger message")
		}
	})

	t.Run("a focused system with no chats at this moment names itself", func(t *testing.T) {
		model := cosmosTestModel(19, true)
		model.cosmos.Nodes = []compose.CosmosNode{
			{Key: "chat:name:Bone", Label: resolve.Named("Bone"), Home: "beta"},
		}
		model.cosmos.Edges = nil
		model.cosmosFocus = "alpha" // no node lives here; beta does
		text := ansi.Strip(model.renderCosmosPanel(80, 7))
		if !strings.Contains(text, "⌖ alpha has no chats at this moment") {
			t.Fatalf("compact panel = %q, want the focused-empty message", text)
		}
	})

	t.Run("never exceeds innerHeight lines", func(t *testing.T) {
		model := cosmosTestModel(19, true)
		nodes := make([]compose.CosmosNode, 0, 20)
		edges := make([]compose.CosmosEdge, 0, 10)
		for i := 0; i < 10; i++ {
			suffix := string(rune('a' + i))
			from, to := "chat:name:from-"+suffix, "chat:name:to-"+suffix
			nodes = append(nodes,
				compose.CosmosNode{Key: from, Label: resolve.Named("from-" + suffix), Home: "alpha"},
				compose.CosmosNode{Key: to, Label: resolve.Named("to-" + suffix), Home: "alpha"},
			)
			edges = append(edges, compose.CosmosEdge{
				From: from, To: to, Kind: fleetdb.KindInject, Count: 1,
				LastNS: fixtureNowNS - int64(i)*int64(time.Second), LastMessage: "message",
			})
		}
		model.cosmos = compose.CosmosGraph{Nodes: nodes, Edges: edges}
		text := model.renderCosmosPanel(19, 7)
		lines := strings.Split(text, "\n")
		if len(lines) != 7 { // top border + 5 body lines + bottom border
			t.Fatalf(
				"panel produced %d lines for a height-7 request, want exactly 7 (frame + innerHeight): %#v",
				len(lines),
				lines,
			)
		}
	})
}

// TestCosmosMoonOfMoonSeedsFromParentRegardlessOfNodeOrder pins F7: a
// same-home nested spawn chain (A -> B -> C, B a moon of A, C a moon of B)
// must seat C at B's own chain position even when graph.Nodes lists C
// BEFORE B — mergeCosmosSeats resolves moons in parent-depth order, not
// node order, so a parent's freshly written seat always exists before its
// child's is read.
func TestCosmosMoonOfMoonSeedsFromParentRegardlessOfNodeOrder(t *testing.T) {
	model := cosmosTestModel(120, true)
	parent := "chat:id:11111111-1111-4111-8111-111111111111"
	parentHome := ""
	for _, node := range model.cosmos.Nodes {
		if node.Key == parent {
			parentHome = node.Home
		}
	}
	moonB := compose.CosmosNode{Key: "chat:name:moon-b", Label: resolve.Named("moon-b"), Home: parentHome}
	moonC := compose.CosmosNode{Key: "chat:name:moon-c", Label: resolve.Named("moon-c"), Home: parentHome}
	// C listed BEFORE B on purpose: mergeCosmosSeats must not depend on
	// graph.Nodes' own order to find a parent's freshly written seat.
	model.cosmos.Nodes = append(model.cosmos.Nodes, moonC, moonB)
	model.cosmos.Edges = append(model.cosmos.Edges,
		compose.CosmosEdge{From: parent, To: moonB.Key, Kind: fleetdb.KindSpawn, LastNS: fixtureNowNS},
		compose.CosmosEdge{From: moonB.Key, To: moonC.Key, Kind: fleetdb.KindSpawn, LastNS: fixtureNowNS},
	)
	model.mergeCosmosSeats()

	seatB, seatC := model.cosmosSeats[moonB.Key], model.cosmosSeats[moonC.Key]
	if seatB == nil || seatC == nil {
		t.Fatalf("setup: expected seats for both moons: B=%#v C=%#v", seatB, seatC)
	}
	if seatC.Target != seatB.Target || seatC.RingTarget != seatB.RingTarget {
		t.Fatalf(
			"moon-of-moon seat = {Target:%v RingTarget:%v}, want its parent B's own seat {Target:%v RingTarget:%v}",
			seatC.Target,
			seatC.RingTarget,
			seatB.Target,
			seatB.RingTarget,
		)
	}
}

// TestCosmosStarTemperatureCoolsFromLastMessageNotJustTrafficCount is the
// spectral-cooling unit table for cosmosStarTemperature, pinned as its own
// boundary test rather than trusted to indirect golden coverage: zero
// traffic and no last message is stone cold, traffic at the hot ceiling is
// full white-hot, a star with zero current traffic that JUST talked still
// holds the ember floor, that ember decays by 1/e after one cosmosCooling
// interval, and a view point measured BEFORE the star's own last message
// never shows a future ember.
func TestCosmosStarTemperatureCoolsFromLastMessageNotJustTrafficCount(t *testing.T) {
	const now = int64(10 * time.Hour)
	tests := []struct {
		name   string
		star   compose.CosmosStar
		viewNS int64
		want   float64
	}{
		{"no traffic, no last message: stone cold", compose.CosmosStar{}, now, 0},
		{"traffic at the hot ceiling: full white-hot", compose.CosmosStar{TrafficHour: cosmosHotTraffic}, now, 1},
		{"idle but just talked: the ember floor", compose.CosmosStar{TrafficHour: 0, LastNS: now}, now, cosmosEmber},
		{
			"idle for one cooling interval: ember decayed by 1/e",
			compose.CosmosStar{TrafficHour: 0, LastNS: now - int64(cosmosCooling)},
			now,
			cosmosEmber / math.E,
		},
		{
			"viewed before the star ever spoke: traffic only, no future ember",
			compose.CosmosStar{TrafficHour: 0, LastNS: now + int64(time.Hour)},
			now,
			0,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := cosmosStarTemperature(test.star, test.viewNS)
			if math.Abs(got-test.want) > 0.0001 {
				t.Fatalf("cosmosStarTemperature(%#v, %d) = %v, want %v", test.star, test.viewNS, got, test.want)
			}
		})
	}
}
