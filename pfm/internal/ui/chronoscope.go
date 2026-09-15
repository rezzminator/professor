package ui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"hostops/pfm/internal/compose"
	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/shared"
)

// cosmosPlayRate is the chronoscope's playback speed: one real second replays
// one minute of ledger history, so a busy hour unfolds in sixty seconds and
// the whole 24h window passes in twenty-four minutes.
const cosmosPlayRate = 60.0

const (
	cosmosScrubFine   = 5 * time.Minute
	cosmosScrubCoarse = time.Hour
	// cosmosPastCutInterval bounds how stale a playing replay's cut may get
	// between prefix changes: the graph is re-cut at least once per minute of
	// ledger time (one real second) so grace windows and star heat, which
	// read the clock the cut was made at, keep pace with the playhead.
	cosmosPastCutInterval = time.Minute
)

// viewGraph is the graph the sky renders: the replay cut while the playhead
// is off now, the live graph otherwise.
func (model Model) viewGraph() compose.CosmosGraph {
	if model.cosmosPast != nil {
		return *model.cosmosPast
	}
	return model.cosmos
}

// rebuildCosmosTimeline re-sorts the sampled window oldest-first. The
// sampler hands events newest-first (the query's ORDER BY), and a playhead
// cuts a PREFIX — the events that had already happened by then. The result
// is non-nil even for an empty sample: a nil timeline means "never sampled",
// which the scrub keys refuse on by name.
func (model *Model) rebuildCosmosTimeline() {
	timeline := make([]shared.CommsEvent, 0, len(model.cosmosEvents))
	timeline = append(timeline, model.cosmosEvents...)
	sort.SliceStable(timeline, func(left, right int) bool {
		if timeline[left].AtNS != timeline[right].AtNS {
			return timeline[left].AtNS < timeline[right].AtNS
		}
		return timeline[left].ID < timeline[right].ID
	})
	model.cosmosTimeline = timeline
}

func (model Model) cosmosPrefix(viewNS int64) int {
	return sort.Search(len(model.cosmosTimeline), func(index int) bool {
		return model.cosmosTimeline[index].AtNS > viewNS
	})
}

// rebuildCosmosPast re-cuts the replay graph at the playhead; a no-op when
// live. compose.BuildCosmos is stateless and takes its clock as an argument,
// which is the whole reason the past is cheap: the function that builds
// "now" builds 13:42 when handed 13:42 and the events that existed by then.
// The cut is against the rows of NOW, deliberately — a chat that has died
// since renders as a ghost of itself (Dead, dim, never animated), which is
// the truth: it was talking then and it is gone now.
func (model *Model) rebuildCosmosPast() {
	if model.cosmosPast == nil {
		return
	}
	count := model.cosmosPrefix(model.cosmosViewNS)
	graph := compose.BuildCosmos(model.rows, model.cosmosTimeline[:count], model.cosmosViewNS, false)
	for _, warning := range model.cosmos.Warnings {
		if warning == compose.CosmosTruncationWarning {
			// The sample holds the newest events of the window, so a replay
			// far enough back is cut from an incomplete record: the past
			// inherits the live warning rather than rendering a quieter sky
			// that reads as "nothing happened".
			graph.Warnings = append(graph.Warnings, warning)
		}
	}
	model.cosmosPast = &graph
	model.cosmosPastCount = count
	model.cosmosPastCutNS = model.cosmosViewNS
}

func (model Model) updateCosmosKey(key string) (tea.Model, tea.Cmd) {
	model.cosmosStatus = ""
	switch key {
	case "o":
		model.toggleClassicSky()
	case "up", "k":
		model.moveCosmosSelection(-1)
	case keyDown, "j":
		model.moveCosmosSelection(1)
	case "enter":
		return model.openCosmosSelection()
	case "s":
		model.cycleCosmosFocus()
	case "[":
		model.scrubCosmos(-cosmosScrubFine)
	case "]":
		model.scrubCosmos(cosmosScrubFine)
	case "{":
		model.scrubCosmos(-cosmosScrubCoarse)
	case "}":
		model.scrubCosmos(cosmosScrubCoarse)
	case "space":
		model.toggleCosmosPlay()
	case "n":
		model.cosmosReturnToNow()
	}
	return model, nil
}

// scrubCosmos moves the playhead by delta, clamped to the sampled window.
// Landing on or past now returns to live — there is no future to show.
func (model *Model) scrubCosmos(delta time.Duration) {
	if model.cosmosTimeline == nil {
		model.cosmosStatus = "the ledger has not been sampled yet — the first sample lands within 2s of focusing"
		return
	}
	from := model.cosmosViewNS
	if model.cosmosPast == nil {
		from = model.cosmosNowNS
	}
	target := from + int64(delta)
	if target >= model.cosmosNowNS {
		model.cosmosReturnToNow()
		return
	}
	if floor := model.cosmosNowNS - int64(compose.CosmosWindow); target < floor {
		target = floor
		model.cosmosStatus = "the ledger window ends here — 24h back is as far as the cosmos sees"
	}
	model.seekCosmos(target)
}

func (model *Model) seekCosmos(viewNS int64) {
	if model.cosmosPast == nil {
		model.cosmosPast = &compose.CosmosGraph{}
	}
	model.cosmosViewNS = viewNS
	model.rebuildCosmosPast()
	model.mergeCosmosSeats()
	model.settleCosmosSeatsWithoutSky()
}

// toggleCosmosPlay starts or pauses playback. Pressed while live it means
// "replay the last hour": the playhead jumps back an hour and runs forward
// to now, where it seats itself back in the present.
func (model *Model) toggleCosmosPlay() {
	if !model.skyEnabled {
		model.cosmosStatus = "play needs the animation clock — this picker runs --no-sky; [ ] { } still scrub"
		return
	}
	if model.cosmosTimeline == nil {
		model.cosmosStatus = "the ledger has not been sampled yet — the first sample lands within 2s of focusing"
		return
	}
	if model.cosmosPlaying {
		model.cosmosPlaying = false
		return
	}
	if model.cosmosPast == nil {
		start := model.cosmosNowNS - int64(cosmosScrubCoarse)
		if floor := model.cosmosNowNS - int64(compose.CosmosWindow); start < floor {
			start = floor
		}
		model.seekCosmos(start)
	}
	model.cosmosPlaying = true
}

// advanceChronoscope moves a playing playhead by dt real seconds at
// cosmosPlayRate and re-cuts the graph when the cut would differ — the
// event prefix grew, or a minute of ledger time passed since the last cut.
func (model *Model) advanceChronoscope(dt float64) {
	if model.cosmosPast == nil {
		model.cosmosViewNS = model.cosmosNowNS
		return
	}
	if !model.cosmosPlaying {
		return
	}
	model.cosmosViewNS += int64(dt * cosmosPlayRate * float64(time.Second))
	if model.cosmosViewNS >= model.cosmosNowNS {
		model.cosmosReturnToNow()
		return
	}
	stale := model.cosmosViewNS-model.cosmosPastCutNS >= int64(cosmosPastCutInterval)
	if stale || model.cosmosPrefix(model.cosmosViewNS) != model.cosmosPastCount {
		model.rebuildCosmosPast()
		model.mergeCosmosSeats()
	}
}

func (model *Model) cosmosReturnToNow() {
	model.cosmosPast = nil
	model.cosmosPlaying = false
	model.cosmosViewNS = model.cosmosNowNS
	model.cosmosPastCount = 0
	model.mergeCosmosSeats()
	model.settleCosmosSeatsWithoutSky()
}

// adoptCosmosClock advances the ledger clock without changing a replay's
// playhead. Wall-clock, sample, and animation messages can arrive out of order;
// an older timestamp never rewinds the live clock.
func (model *Model) adoptCosmosClock(nowNS int64) {
	if nowNS <= model.cosmosNowNS {
		return
	}
	model.cosmosNowNS = nowNS
	if model.cosmosPast == nil {
		model.cosmosViewNS = nowNS
	}
}

// settleCosmosSeatsWithoutSky snaps every seat onto its target when nothing
// will ever ease it: --no-sky never ticks, so a deliberate mode change there
// teleports on purpose — a graph refresh never does.
func (model *Model) settleCosmosSeatsWithoutSky() {
	if model.skyEnabled {
		return
	}
	for _, seat := range model.cosmosSeats {
		seat.Angle = seat.Target
		seat.Ring = seat.RingTarget
	}
}

// cosmosFocused reports whether a system focus is in force: a home is set
// and the sky has systems to focus — the classic sky has none, so there a
// remembered focus is dormant, never a filter the layout does not draw.
func (model Model) cosmosFocused() bool {
	return model.cosmosFocus != "" && !model.classicSky
}

// cosmosVisibleNodes is the navigator's order and the renderer's population:
// the view graph's nodes, cut to the focused system when one is in force.
func (model Model) cosmosVisibleNodes() []compose.CosmosNode {
	graph := model.viewGraph()
	if !model.cosmosFocused() {
		return graph.Nodes
	}
	visible := make([]compose.CosmosNode, 0, len(graph.Nodes))
	for _, node := range graph.Nodes {
		if node.Home == model.cosmosFocus {
			visible = append(visible, node)
		}
	}
	return visible
}

// cosmosHomes lists every NAMED star of the view graph, sorted — the stops
// `s` walks. The unknown star (Home "") is where ghosts live, not a project
// anyone can focus, so it is never a stop.
func (model Model) cosmosHomes() []string {
	seen := make(map[string]bool)
	for _, node := range model.viewGraph().Nodes {
		if node.Home != "" {
			seen[node.Home] = true
		}
	}
	homes := make([]string, 0, len(seen))
	for home := range seen {
		homes = append(homes, home)
	}
	sort.Strings(homes)
	return homes
}

func (model *Model) moveCosmosSelection(direction int) {
	nodes := model.cosmosVisibleNodes()
	if len(nodes) == 0 {
		model.cosmosSelected = ""
		model.cosmosStatus = "nothing to select — the sky is empty"
		return
	}
	index := -1
	for position, node := range nodes {
		if node.Key == model.cosmosSelected {
			index = position
			break
		}
	}
	switch {
	case index < 0 && direction > 0:
		index = 0
	case index < 0:
		index = len(nodes) - 1
	default:
		index = (index + direction + len(nodes)) % len(nodes)
	}
	model.cosmosSelected = nodes[index].Key
}

// reconcileCosmosSelection drops a reticle whose node left the visible sky —
// a refresh pruned it, the playhead moved before its birth, a focus change
// hid its system. It never re-targets: a selection is a choice, not a cursor
// that must always point somewhere.
func (model *Model) reconcileCosmosSelection() {
	if model.cosmosSelected == "" {
		return
	}
	for _, node := range model.cosmosVisibleNodes() {
		if node.Key == model.cosmosSelected {
			return
		}
	}
	model.cosmosSelected = ""
}

func (model Model) cosmosSelectedNode() (compose.CosmosNode, bool) {
	if model.cosmosSelected == "" {
		return compose.CosmosNode{}, false
	}
	for _, node := range model.viewGraph().Nodes {
		if node.Key == model.cosmosSelected {
			return node, true
		}
	}
	return compose.CosmosNode{}, false
}

// openCosmosSelection is the Chats tab's enter, reached from the sky: the
// selected node's fleet row becomes the picker's outcome. Every refusal
// names itself on the query row — a ghost has no row to open, and a row
// the fleet no longer lists cannot be opened from a ledger memory of it.
func (model Model) openCosmosSelection() (tea.Model, tea.Cmd) {
	node, ok := model.cosmosSelectedNode()
	if !ok {
		model.cosmosStatus = "nothing selected — ↑↓ picks a chat first"
		return model, nil
	}
	label := node.Label.String()
	if node.RowKey == "" {
		model.cosmosStatus = "enter needs a live chat — " + label + " is gone; only the ledger remembers it"
		return model, nil
	}
	for index := range model.rows {
		row := &model.rows[index]
		if compose.RowKey(*row) == node.RowKey {
			model.outcome = OutcomeSelected
			model.outcomeRow = *row
			return model, tea.Quit
		}
	}
	model.cosmosStatus = "enter refused — " + label + "'s row is no longer in the fleet"
	return model, nil
}

// cycleCosmosFocus walks `s` through the galaxy: whole sky → each star in
// sorted order → whole sky again.
func (model *Model) cycleCosmosFocus() {
	if model.classicSky {
		model.cosmosStatus = "the classic sky has no systems to focus — o restores them"
		return
	}
	homes := model.cosmosHomes()
	if len(homes) == 0 {
		model.cosmosFocus = ""
		model.cosmosStatus = "no system to focus — the sky is empty"
		return
	}
	next := ""
	if model.cosmosFocus == "" {
		next = homes[0]
	} else {
		for index, home := range homes {
			if home == model.cosmosFocus && index+1 < len(homes) {
				next = homes[index+1]
				break
			}
		}
	}
	model.cosmosFocus = next
	model.reconcileCosmosSelection()
}

func cosmosHomeLabel(home string) string {
	if home == "" {
		return "unknown"
	}
	return home
}

// cosmosModeChip names the sky's frame of reference on the canvas itself:
// which system fills it and which moment it shows. Empty when live and
// unfocused — the default needs no label.
func (model Model) cosmosModeChip() string {
	parts := make([]string, 0, 2)
	if model.cosmosFocused() {
		parts = append(parts, "⌖ "+cosmosHomeLabel(model.cosmosFocus))
	}
	if model.cosmosPast != nil {
		// Narrow glyphs only (East-Asian-Width N): the canvas seats one rune
		// per cell, and a wide emoji such as ⏪ would push this row's border
		// one column right in every terminal that sizes it honestly.
		at := time.Unix(0, model.cosmosViewNS).Format("15:04:05")
		if model.cosmosPlaying {
			parts = append(parts, fmt.Sprintf("▸▸ %s · %d×", at, int(cosmosPlayRate)))
		} else {
			parts = append(parts, fmt.Sprintf("⟲ %s · −%s", at, cosmosAgo(model.cosmosNowNS-model.cosmosViewNS)))
		}
	}
	return strings.Join(parts, " · ")
}

func cosmosAgo(ns int64) string {
	rounded := time.Duration(ns).Round(time.Minute)
	hours := int(rounded.Hours())
	minutes := int(rounded.Minutes()) % 60
	if hours == 0 {
		return fmt.Sprintf("%dm", minutes)
	}
	return fmt.Sprintf("%dh%02dm", hours, minutes)
}

// cosmosSelectionHUD is the query-row card for the node under the reticle:
// who it is, which engine and star, how much it has said and heard inside
// the window, and its newest exchange.
func (model Model) cosmosSelectionHUD() string {
	node, ok := model.cosmosSelectedNode()
	if !ok {
		return ""
	}
	graph := model.viewGraph()
	inbound, outbound := 0, 0
	var newest *compose.CosmosEdge
	for index := range graph.Edges {
		edge := &graph.Edges[index]
		touches := false
		if edge.To == node.Key {
			inbound += edge.Count
			touches = true
		}
		if edge.From == node.Key {
			outbound += edge.Count
			touches = true
		}
		if touches && (newest == nil || edge.LastNS > newest.LastNS) {
			newest = edge
		}
	}
	engine := "unknown engine"
	if id, err := pfmengine.Parse(node.Engine); err == nil {
		engine = strings.ToLower(pfmengine.MustLookup(id).Short)
	}
	hud := fmt.Sprintf("▸ %s · %s · ⋆ %s · in %d · out %d",
		node.Label.String(), engine, cosmosHomeLabel(node.Home), inbound, outbound)
	if newest != nil {
		hud += fmt.Sprintf(" · %s %q", time.Unix(0, newest.LastNS).Format("15:04:05"), newest.LastMessage)
	}
	// Three honest states, keyed on the fields compose actually sets: a ghost
	// has no row; a killed row says so; a row with no socket — resumable,
	// booting, idle — is not running, whatever its Dead flag says.
	switch {
	case node.RowKey == "":
		hud += " · ghost"
	case node.Killed:
		hud += " · killed"
	case node.Dead || !node.Live:
		hud += " · not running"
	}
	return hud
}
