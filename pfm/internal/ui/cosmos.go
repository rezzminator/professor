package ui

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
)

type star struct {
	px, py int
	phase  float64
	speed  float64
}

type cosmosPoint struct{ x, y float64 }

// cosmosFrame is one layout pass: every node's braille-pixel seat plus the
// star anchors and system radii the seats were computed against, so the
// renderer can draw orbit guides on exactly the geometry the planets follow.
type cosmosFrame struct {
	points             map[string]cosmosPoint
	starOrder          []string
	starPoints         map[string]cosmosPoint
	systemRx, systemRy float64
	cx, cy             float64
	// rings lists, per star, the distinct ring factors its planets sit on —
	// the tracks the guide loop draws, so a split system shows every ring
	// its planets actually follow.
	rings map[string][]float64
	// hidden marks nodes seated outside the focused system: placed off-canvas
	// so their edges leave the frame toward their own star, never drawn as
	// bodies themselves.
	hidden map[string]bool
}

// cosmosMoonOrbit is the shared moon ring radius, widened with the brood.
func cosmosMoonOrbit(count int) float64 { return 5 + 2.5*float64(count) }

// cosmosRingCap is how many planets share a ring before a system splits;
// recent chats take inner tracks and quiet ones take outer tracks.
const cosmosRingCap = 6

// cosmosRingFactor is ring r's radius as a fraction of the system's outer
// track: one ring fills the track; several spread evenly from 0.55 outward.
func cosmosRingFactor(ring, rings int) float64 {
	if rings <= 1 {
		return 1
	}
	return 0.55 + 0.45*float64(ring)/float64(rings-1)
}

// cosmosRingSpeed is Kepler's third law in one line: angular speed falls
// with radius to the 3/2, so an outer ring revolves slower than an inner
// one — the physics the eye already expects from a solar system.
func cosmosRingSpeed(factor float64) float64 { return math.Pow(math.Max(0.2, factor), -1.5) }

type cosmosSampleMsg struct {
	generation uint64
	events     []fleetdb.CommsEvent
	err        error
}

type (
	cosmosSampleTickMsg struct{ generation uint64 }
	cosmosTickMsg       struct {
		generation uint64
		nowNS      int64
	}
)

func (model *Model) startCosmosSample() tea.Cmd {
	if model.cosmosSampler == nil || model.cosmosLoading {
		return nil
	}
	model.statsGeneration++
	generation := model.statsGeneration
	model.cosmosLoading = true
	sampler := model.cosmosSampler
	sinceNS := model.nowNS - int64(compose.CosmosWindow)
	return func() tea.Msg {
		events, err := sampler.Sample(context.Background(), sinceNS)
		return cosmosSampleMsg{generation: generation, events: events, err: err}
	}
}

func cosmosWaitCmd(generation uint64) tea.Cmd {
	return tea.Tick(cosmosRefreshInterval, func(time.Time) tea.Msg {
		return cosmosSampleTickMsg{generation: generation}
	})
}

// cosmosTickInterval is the animation clock: ~15fps, fast enough that comets,
// wind, and twinkle read as motion rather than a slideshow. Safe mode drops
// back to 8fps because heavy braille truecolor churn corrupts SIBLING
// terminals through VS Code's shared WebGL glyph atlas
// (microsoft/vscode#332859, xtermjs/xterm.js#6043). Honesty note: no
// app-side output shaping is PROVEN to prevent that corruption — the atlas
// is a shared, cumulative GPU resource one app cannot bound — so safe mode
// only slows the pressure (fewer frames, coarser colours via Canvas.Quant)
// until the upstream atlas-eviction fix (xterm.js 6.1.0) ships in stable
// VS Code; the reliable user-side cure is
// terminal.integrated.gpuAcceleration "off". The mode announces itself in
// the panel title, so the degraded state is never silent.
func (model Model) cosmosTickInterval() time.Duration {
	if model.cosmosSafe {
		return 125 * time.Millisecond
	}
	return 66 * time.Millisecond
}

func cosmosTickCmd(generation uint64, interval time.Duration) tea.Cmd {
	return tea.Tick(interval, func(now time.Time) tea.Msg {
		return cosmosTickMsg{generation: generation, nowNS: now.UnixNano()}
	})
}

// cosmosPhase derives a stable personal phase from a node or edge key so
// ambient motion never synchronizes across the universe — every seat, wind
// particle, and breath runs on its own clock, and a pinned test clock still
// renders the same frame every time.
func cosmosPhase(key string) float64 {
	var hash uint32 = 2166136261
	for _, b := range []byte(key) {
		hash ^= uint32(b)
		hash *= 16777619
	}
	return float64(hash%4096) / 4096 * 2 * math.Pi
}

// cosmosOrbits reads the spawn lineage out of the visible graph: every chat
// spawned by another visible chat is that parent's moon. Each parent keeps a
// stable, key-sorted list of its moons so a moon holds the same orbital slot
// no matter how the edge slice reorders on new activity.
func cosmosOrbits(
	edges []compose.CosmosEdge,
	nodes map[string]compose.CosmosNode,
) (map[string]string, map[string][]string) {
	parents := make(map[string]string)
	children := make(map[string][]string)
	for _, edge := range edges {
		if edge.Kind != fleetdb.KindSpawn || edge.From == edge.To {
			continue
		}
		parent, parentSeen := nodes[edge.From]
		child, childSeen := nodes[edge.To]
		if !parentSeen || !childSeen {
			continue
		}
		if parent.Home != child.Home {
			// Residence wins: a chat spawned into another star's system is a
			// planet of the star it lives under, never a moon dragged along
			// by its parent — the spawn lineage stays fully visible as the
			// dashed edge arcing between the two systems.
			continue
		}
		if _, taken := parents[edge.To]; taken {
			continue
		}
		parents[edge.To] = edge.From
		children[edge.From] = append(children[edge.From], edge.To)
	}
	for _, moons := range children {
		sort.Strings(moons)
	}
	return parents, children
}

func wrapDelta(delta float64) float64 {
	for delta > math.Pi {
		delta -= 2 * math.Pi
	}
	for delta < -math.Pi {
		delta += 2 * math.Pi
	}
	return delta
}

// cosmosRevolution is the base angular speed of a system's outer ring: one
// revolution every ~3.5 minutes, slow enough to feel orbital, fast enough
// to see. Inner rings turn faster by cosmosRingSpeed.
const cosmosRevolution = 0.03

func (model *Model) advanceCosmos(nowNS int64) {
	if nowNS <= model.cosmosNowNS {
		return
	}
	dt := float64(nowNS-model.cosmosNowNS) / float64(time.Second)
	for _, seat := range model.cosmosSeats {
		seat.Angle += wrapDelta(seat.Target-seat.Angle) * math.Min(1, dt*5)
		seat.Ring += (seat.RingTarget - seat.Ring) * math.Min(1, dt*3)
		seat.Orbit += dt * cosmosRevolution * cosmosRingSpeed(seat.Ring)
	}
	model.cosmosNowNS = nowNS
	model.advanceChronoscope(dt)
}

// applyCosmosGraph is the single place model.cosmos is ever replaced. Every
// caller hands it a live graph — compose.BuildCosmos(..., live=true) — whose
// own contract already drops every dead or hidden node and every edge
// naming one before returning (see BuildCosmos's doc comment): the live sky
// never shows a dead or hidden chat, no blink, no fade, no debris, so there
// is nothing left here to diff, date, or prune. This method used to be that
// diff — watching a node's own alive-to-dead transition across renders to
// drive a blink-then-fade animation before dropping it — but compose now
// makes the only call that ever needed making (dead or not), upstream of
// this method, once for every caller instead of once per renderer.
func (model *Model) applyCosmosGraph(next compose.CosmosGraph) {
	model.cosmos = next
}

// mergeCosmosSeats re-targets every seat against the graph the sky renders —
// the live graph, or the replay cut while the playhead is off now. Seats are
// keyed by chat, so a chat keeps its seat across the two: scrubbing into the
// past shows the same body gliding, not a stranger seated in its place.
func (model *Model) mergeCosmosSeats() {
	previous := model.cosmosSeats
	if previous == nil {
		previous = make(map[string]*cosmosSeat)
	}
	graph := model.viewGraph()
	hadSeats := len(previous) != 0
	next := make(map[string]*cosmosSeat, len(graph.Nodes))
	newKeys := make(map[string]bool, len(graph.Nodes))
	chats := graph.Nodes
	assign := func(nodes []compose.CosmosNode, offset, ring float64) {
		for index, node := range nodes {
			target := offset
			if len(nodes) != 0 {
				target += 2 * math.Pi * float64(index) / float64(len(nodes))
			}
			if old := previous[node.Key]; old != nil {
				seatCopy := *old
				seatCopy.Target = target
				seatCopy.RingTarget = ring
				next[node.Key] = &seatCopy
				continue
			}
			next[node.Key] = &cosmosSeat{Angle: target, Target: target, Ring: ring, RingTarget: ring}
			newKeys[node.Key] = true
		}
	}
	if model.classicSky {
		// The first cosmos' seating, kept verbatim: every chat evenly on ONE
		// shared ring starting at the top — no per-star systems, no moon
		// parking, no inner rings.
		assign(chats, -math.Pi/2, 1)
	} else {
		nodeMap := cosmosNodeMap(graph.Nodes)
		parents, _ := cosmosOrbits(graph.Edges, nodeMap)
		roots := make([]compose.CosmosNode, 0, len(chats))
		moons := make([]compose.CosmosNode, 0, len(chats))
		for _, node := range chats {
			if parents[node.Key] != "" {
				moons = append(moons, node)
				continue
			}
			roots = append(roots, node)
		}
		// Roots ring their own star, not one global circle: each star's planets
		// are spaced evenly among THEMSELVES, so a three-chat project reads as a
		// three-planet system regardless of how crowded the next star over is.
		byHome := make(map[string][]compose.CosmosNode)
		for _, node := range roots {
			byHome[node.Home] = append(byHome[node.Home], node)
		}
		homes := make([]string, 0, len(byHome))
		for home := range byHome {
			homes = append(homes, home)
		}
		sort.Strings(homes)
		for _, home := range homes {
			// Past cosmosRingCap the system splits into rings, the most
			// recently active chats innermost — a warm inner system, a cold
			// outer one. Each ring is spaced among itself and offset from
			// the last so two rings never line their planets up radially.
			planets := byHome[home]
			sort.SliceStable(planets, func(left, right int) bool {
				if planets[left].LastNS != planets[right].LastNS {
					return planets[left].LastNS > planets[right].LastNS
				}
				return planets[left].Key < planets[right].Key
			})
			rings := (len(planets) + cosmosRingCap - 1) / cosmosRingCap
			for ring := 0; ring < rings; ring++ {
				start, end := ring*cosmosRingCap, min(len(planets), (ring+1)*cosmosRingCap)
				offset := -math.Pi/2 + math.Pi*float64(ring)/float64(maxInt(1, rings))
				assign(planets[start:end], offset, cosmosRingFactor(ring, rings))
			}
		}
		// A moon's seat is only the fallback for the day its parent vanishes:
		// while the parent renders, cosmosLayout overrides the moon's position
		// with the parent-anchored orbit, and the ring target parked here is
		// where the orphan eases once the parent is gone. Parents seat before
		// their moons — a moon of a moon reads its parent's seat, which must
		// already exist, whatever order the graph listed them in.
		depth := func(key string) int {
			count := 0
			for parent := parents[key]; parent != "" && count <= 8; parent = parents[parent] {
				count++
			}
			return count
		}
		sort.SliceStable(moons, func(left, right int) bool {
			return depth(moons[left].Key) < depth(moons[right].Key)
		})
		for _, node := range moons {
			target, ring := 0.0, 1.0
			if parent := next[parents[node.Key]]; parent != nil {
				target, ring = parent.Target, parent.RingTarget
			}
			if old := previous[node.Key]; old != nil {
				seatCopy := *old
				seatCopy.Target = target
				seatCopy.RingTarget = ring
				next[node.Key] = &seatCopy
				continue
			}
			next[node.Key] = &cosmosSeat{Angle: target, Target: target, Ring: ring, RingTarget: ring}
			newKeys[node.Key] = true
		}
	}
	for _, edge := range graph.Edges {
		if !hadSeats || edge.Kind != fleetdb.KindSpawn || !newKeys[edge.To] {
			continue
		}
		parent, child := next[edge.From], next[edge.To]
		if parent != nil && child != nil {
			child.Angle = parent.Angle
			child.Orbit = parent.Orbit
		}
	}
	model.cosmosSeats = next
	model.reconcileCosmosSelection()
}

// toggleClassicSky flips the cosmos between the orbital and the first-cosmos
// layout and re-seats immediately: ring targets differ between the two skies.
// With the sky on, the seats' easing carries the transition; --no-sky never
// ticks the easing, so there the seats snap straight to their new targets —
// a deliberate mode change may teleport, a graph refresh never does.
func (model *Model) toggleClassicSky() {
	model.classicSky = !model.classicSky
	model.mergeCosmosSeats()
	model.settleCosmosSeatsWithoutSky()
}

// renderCosmosSubheader is the tab's one-line census: what the sky is
// showing right now — the focused system's chats and edges when `s` has
// narrowed it, with the edges that LEAVE the system counted rather than
// silently absent, since those are drawn running off the frame.
func (model Model) renderCosmosSubheader() string {
	graph := model.viewGraph()
	if !model.cosmosFocused() {
		return fmt.Sprintf(" cosmos · %d chats · %d edges · last 24h", len(graph.Nodes), len(graph.Edges))
	}
	visible := cosmosNodeMap(model.cosmosVisibleNodes())
	inside, leaving := 0, 0
	for _, edge := range graph.Edges {
		_, fromIn := visible[edge.From]
		_, toIn := visible[edge.To]
		switch {
		case fromIn && toIn:
			inside++
		case fromIn || toIn:
			leaving++
		}
	}
	line := fmt.Sprintf(
		" cosmos · ⌖ %s · %d chats · %d edges",
		cosmosHomeLabel(model.cosmosFocus),
		len(visible),
		inside,
	)
	if leaving > 0 {
		line += fmt.Sprintf(" · %d leave the system", leaving)
	}
	return line + " · last 24h"
}

func (model Model) renderCosmosPanel(width, height int) string {
	innerWidth := maxInt(1, width-2)
	innerHeight := maxInt(1, height-2)
	if width < 20 || height < 8 {
		return model.renderCompactCosmos(width, innerWidth, innerHeight)
	}
	canvas := NewCanvas(innerWidth, innerHeight)
	title := " cosmos "
	if model.cosmosSafe {
		// Safe mode names itself — a viewer must never wonder why the sky
		// is calmer inside VS Code.
		canvas.Quant = 16
		title = " cosmos · safe "
	}
	// Two clocks: `now` drives the ambient sky (twinkle, revolution, wind,
	// breath) and never stops; `view` is the ledger moment the sky shows,
	// and it is what every message-derived light — edge fade, comets,
	// shockwaves, arrival flashes — measures its age against. Live, they are
	// the same instant; under the chronoscope, view is the past.
	now := time.Unix(0, model.cosmosNowNS)
	view := time.Unix(0, model.cosmosViewNS)
	graph := model.viewGraph()
	visible := model.cosmosVisibleNodes()
	if model.skyEnabled {
		drawCosmosStars(canvas, now)
	}
	// Legend and mode chip land BEFORE the bodies: they are text the label
	// nudge must see, so a planet's name steps around them instead of being
	// paved over by them.
	drawCosmosLegend(canvas, model.classicSky)
	if chip := model.cosmosModeChip(); chip != "" {
		canvas.Text(
			1,
			0,
			ellipsizeRunes(chip, maxInt(0, canvas.Cols-2)),
			rgbFromHex(configuredCosmosPalette.Warn),
			true,
		)
	}
	if len(visible) != 0 {
		model.drawCosmosUniverse(canvas, graph, now, view)
	}
	if model.cosmos.Err != "" {
		// Ledger health is a fact about NOW, so the banner rides over a
		// replayed past as well: the record the past was cut from is the
		// same unreachable ledger.
		canvas.DimAll(0.30)
		model.drawCosmosBanner(canvas)
	} else if primary, secondary, empty := model.cosmosEmptyState(graph, len(visible)); empty {
		centerCosmosText(canvas, canvas.Rows/2-1, primary, cosmosDimColor(), false)
		centerCosmosText(canvas, canvas.Rows/2+1, secondary, scaleRGB(cosmosDimColor(), 0.7), false)
	} else {
		drawCosmosTicker(canvas, graph)
	}
	if len(graph.Warnings) > 0 {
		warning := "⚠ " + graph.Warnings[0]
		if len(graph.Warnings) > 1 {
			warning += fmt.Sprintf(" (+%d)", len(graph.Warnings)-1)
		}
		canvas.Text(1, canvas.Rows-1, ellipsizeRunes(warning, maxInt(0, canvas.Cols-2)), cosmosDimColor(), false)
	}
	return framePanel(title, strings.Split(canvas.render(), "\n"), width)
}

// drawCosmosLegend keys the glyphs in the top-right corner of the sky — the
// three engines, and the sun wherever suns are drawn — when the canvas is
// wide enough to spare the room. Structure, not decoration: the same in
// --no-sky. The classic sky has no suns, so its legend names none.
func drawCosmosLegend(canvas *Canvas, classic bool) {
	legend := "✳ claude  ▲ codex  ◆ opencode  ✹ project"
	if classic {
		legend = "✳ claude  ▲ codex  ◆ opencode"
	}
	width := len([]rune(legend))
	if canvas.Cols < width+24 {
		return
	}
	canvas.Text(canvas.Cols-1-width, 0, legend, scaleRGB(cosmosDimColor(), 0.8), false)
}

// cosmosEmptyState names why the sky has no bodies to draw, in words that
// never read alike — nothing recorded yet, nothing recorded by the replayed
// moment, or the focused system empty at this moment while the galaxy is
// not. A ledger the picker could not READ is not an empty state and is not
// answered here: that is the banner's job. empty is false when there is
// something to draw.
func (model Model) cosmosEmptyState(graph compose.CosmosGraph, visible int) (primary, secondary string, empty bool) {
	switch {
	case len(graph.Nodes) == 0 && model.cosmosPast != nil:
		at := time.Unix(0, model.cosmosViewNS).Format("15:04:05")
		return "no chat traffic recorded before " + at, "] steps forward · space plays · n returns to now", true
	case len(graph.Nodes) == 0:
		return "no chat traffic recorded yet — the ledger fills as chats talk", "cosmos maps conversation · pfm ls maps existence", true
	case visible == 0:
		// A replay before the focused star's first spawn, or a system whose
		// every chat has since been dropped: this system is empty, the
		// galaxy is not.
		return "⌖ " + cosmosHomeLabel(
			model.cosmosFocus,
		) + " has no chats at this moment", "s cycles systems · n returns to now", true
	}
	return "", "", false
}

// renderCompactCosmos is the sky for a pane too small to draw one: the mode
// chip, then the newest exchanges as text — cut to the focused system's
// conversations exactly as the full panel and the subheader above it are,
// and naming each empty state in the same words the full panel uses.
func (model Model) renderCompactCosmos(width, innerWidth, innerHeight int) string {
	graph := model.viewGraph()
	visible := cosmosNodeMap(model.cosmosVisibleNodes())
	lines := make([]string, 0, innerHeight)
	dim := func(text string) string {
		return dimStyle.Render(fillLine(ansiTruncateRunes(text, innerWidth), innerWidth))
	}
	if chip := model.cosmosModeChip(); chip != "" {
		lines = append(lines, warnStyle.Render(fillLine(ansiTruncateRunes(chip, innerWidth), innerWidth)))
	}
	if model.cosmos.Err != "" {
		lines = append(lines, warnStyle.Render(fillLine(
			ansiTruncateRunes("⚠ comms ledger unreachable: "+model.cosmos.Err, innerWidth), innerWidth,
		)))
	} else if primary, secondary, empty := model.cosmosEmptyState(graph, len(visible)); empty {
		lines = append(lines, dim(primary))
		if len(lines) < innerHeight {
			lines = append(lines, dim(secondary))
		}
	} else {
		nodes := cosmosNodeMap(graph.Nodes)
		listed := 0
		for _, edge := range graph.Edges {
			if listed >= 3 || len(lines) >= innerHeight {
				break
			}
			_, fromIn := visible[edge.From]
			_, toIn := visible[edge.To]
			if !fromIn && !toIn {
				continue // neither end in the focused system: not this system's conversation
			}
			listed++
			from, to := nodes[edge.From], nodes[edge.To]
			labelWidth := maxInt(1, (innerWidth-3)/2)
			identity := ellipsizeRunes(
				from.Label.String(),
				labelWidth,
			) + " → " + ellipsizeRunes(
				to.Label.String(),
				labelWidth,
			)
			lines = append(lines, fillLine(ansiTruncateRunes(identity, innerWidth), innerWidth))
			if edge.LastMessage != "" && len(lines) < innerHeight {
				lines = append(lines, dimStyle.Render(fillLine(
					ansiTruncateRunes(edge.LastMessage, innerWidth), innerWidth,
				)))
			}
		}
	}
	if len(graph.Warnings) > 0 && len(lines) < innerHeight {
		lines = append(lines, dimStyle.Render(fillLine(
			ansiTruncateRunes("⚠ "+graph.Warnings[0], innerWidth), innerWidth,
		)))
	}
	if len(lines) > innerHeight {
		lines = lines[:innerHeight]
	}
	for len(lines) < innerHeight {
		lines = append(lines, strings.Repeat(" ", innerWidth))
	}
	return framePanel(" cosmos ", lines, width)
}

func drawCosmosStars(canvas *Canvas, now time.Time) {
	t := float64(now.UnixNano()) / 1e9
	starColor := rgbFromHex(configuredCosmosPalette.CosmosStar)
	// Far layer: dimmer, slower, sparser — depth behind the main twinkle.
	far := rand.New(rand.NewSource(56))
	for range canvas.Cols * canvas.Rows / 32 {
		px, py := far.Intn(canvas.PW()), far.Intn(canvas.PH())
		phase := far.Float64() * 2 * math.Pi
		speed := 0.2 + far.Float64()*0.5
		canvas.Dot(px, py, scaleRGB(starColor, 0.10+0.16*(0.5+0.5*math.Sin(t*speed+phase))))
	}
	rng := rand.New(rand.NewSource(55))
	count := canvas.Cols * canvas.Rows / 20
	for range count {
		star := star{
			px: rng.Intn(canvas.PW()), py: rng.Intn(canvas.PH()),
			phase: rng.Float64() * 2 * math.Pi,
			speed: 0.6 + rng.Float64()*1.8,
		}
		brightness := 0.18 + 0.42*(0.5+0.5*math.Sin(t*star.speed+star.phase))
		canvas.Dot(star.px, star.py, scaleRGB(starColor, brightness))
	}
	// A few bright stars that flare into a four-point sparkle at their peak.
	bright := rgbFromHex(configuredCosmosPalette.CosmosBright)
	poles := rand.New(rand.NewSource(57))
	for range 4 {
		px, py := poles.Intn(canvas.PW()), poles.Intn(canvas.PH())
		phase := poles.Float64() * 2 * math.Pi
		glow := 0.5 + 0.5*math.Sin(t*0.7+phase)
		canvas.Dot(px, py, scaleRGB(bright, 0.20+0.50*glow))
		if glow > 0.75 {
			cross := scaleRGB(bright, (glow-0.75)*2.2)
			canvas.Dot(px+1, py, cross)
			canvas.Dot(px-1, py, cross)
			canvas.Dot(px, py+1, cross)
			canvas.Dot(px, py-1, cross)
		}
	}
	drawCosmosMeteor(canvas, now)
}

// drawCosmosMeteor streaks one meteor across the sky roughly every half
// minute. Pure deterministic decoration: the time bucket alone decides
// whether, where, and along which diagonal it flies, so a pinned test clock
// always renders the same sky.
func drawCosmosMeteor(canvas *Canvas, now time.Time) {
	const bucketNS = int64(9 * time.Second)
	bucket := now.UnixNano() / bucketNS
	seed := uint64(bucket)*0x9E3779B97F4A7C15 + 0xD1B54A32D192ED03
	seed ^= seed >> 29
	if seed%3 != 0 {
		return
	}
	flight := float64(now.UnixNano()-bucket*bucketNS) / float64(bucketNS) / 0.16
	if flight >= 1 {
		return
	}
	headX := float64(seed >> 8 % uint64(canvas.PW()))
	headY := float64(seed >> 20 % uint64(maxInt(1, canvas.PH()/3)))
	directionX, directionY := 34.0, 13.0
	if seed%2 == 0 {
		directionX = -34
	}
	headX += directionX * flight
	headY += directionY * flight
	bright := rgbFromHex(configuredCosmosPalette.CosmosBright)
	starColor := rgbFromHex(configuredCosmosPalette.CosmosStar)
	for segment := 0; segment < 7; segment++ {
		trail := float64(segment) / 7
		color := scaleRGB(lerpRGB(bright, starColor, trail), (1-trail)*(1-0.5*flight))
		canvas.Dot(int(headX-directionX*trail*0.2), int(headY-directionY*trail*0.2), color)
	}
}

// cosmosCometDuration is the shared flight time for a comet along an edge —
// used both to animate the comet itself and to decide when the edge rail
// beneath it should read as "in flight". Spawn lineage flights run longer
// than a plain message so the two remain distinguishable at a glance.
func cosmosCometDuration(kind string) time.Duration {
	if kind == fleetdb.KindSpawn {
		return 1800 * time.Millisecond
	}
	return 1500 * time.Millisecond
}

// cosmosEdgeHaloHeat is the heat below which a rail loses its halo and
// runs as a single thread: a message about three minutes old, or an edge
// that never carried more than a couple of messages.
const cosmosEdgeHaloHeat = 0.2

// cosmosEdgeLight is the rail's memory of its traffic. fade is the line's
// brightness: full at the moment a message lands, settling over ~2.5
// minutes to a floor that stays readable against the sky — an edge from
// this morning is still a line, not a rumour. heat is what makes a rail
// SHINE — the white blend and the halo — and it comes from two places: a
// fresh message (fading over ~2 minutes) or accumulated traffic, so an edge
// that has carried a storm stays thick after the storm passes. Both are
// clamped to [0, 1].
func cosmosEdgeLight(ageSeconds float64, count int) (fade, heat float64) {
	if ageSeconds < 0 {
		ageSeconds = 0
	}
	fade = 0.42 + 0.58*math.Exp(-ageSeconds/150)
	heat = math.Exp(-ageSeconds / 120)
	if count > 1 {
		heat = math.Max(heat, math.Min(0.45, 0.08*float64(count-1)))
	}
	return math.Min(1, fade), math.Min(1, heat)
}

func (model Model) drawCosmosUniverse(canvas *Canvas, graph compose.CosmosGraph, now, view time.Time) {
	nodes := cosmosNodeMap(graph.Nodes)
	frame := cosmosLayout(
		canvas,
		model.cosmosSeats,
		nodes,
		graph.Edges,
		now,
		model.skyEnabled,
		model.classicSky,
		model.cosmosFocus,
	)
	points, starOrder, starPoints, cx, cy := frame.points, frame.starOrder, frame.starPoints, frame.cx, frame.cy
	clock := float64(now.UnixNano()) / 1e9
	moonParents, moonChildren := cosmosOrbits(graph.Edges, nodes)
	// The navigator's spotlight: with a reticle set, the edges it touches
	// keep their light and every other edge steps back — the sky answers
	// "who does this one talk to" without a single line being removed.
	selected := model.cosmosSelected
	spotlight := func(edge compose.CosmosEdge, fade float64) float64 {
		if selected == "" || edge.From == selected || edge.To == selected {
			return fade
		}
		return fade * 0.45
	}
	// An edge with BOTH ends outside the focused system has nothing to show
	// here — and its rail, bent toward the centre, could still cross the
	// frame between two off-canvas exits, so it is skipped rather than
	// clipped. One end inside: drawn, and seen to leave.
	offFrame := func(edge compose.CosmosEdge) bool {
		return frame.hidden[edge.From] && frame.hidden[edge.To]
	}

	// Orbit guides: the discipline made visible — a faint dashed ellipse
	// under every planetary ring and every moon orbit, so a body is seen to
	// FOLLOW a track rather than float. Structure, not animation: drawn in
	// --no-sky too. The classic sky has no orbits, so it has no tracks.
	if !model.classicSky {
		guide := scaleRGB(rgbFromHex(configuredCosmosPalette.CosmosStar), 0.8)
		for _, home := range starOrder {
			anchor := starPoints[home]
			for _, factor := range frame.rings[home] {
				for step := 0; step < 72; step += 3 {
					angle := 2 * math.Pi * float64(step) / 72
					canvas.Dot(
						int(anchor.x+frame.systemRx*factor*math.Cos(angle)),
						int(anchor.y+frame.systemRy*factor*math.Sin(angle)),
						guide,
					)
				}
			}
		}
		for parentKey, moons := range moonChildren {
			anchor, ok := points[parentKey]
			if !ok || frame.hidden[parentKey] {
				continue
			}
			orbit := cosmosMoonOrbit(len(moons))
			for step := 0; step < 48; step += 3 {
				angle := 2 * math.Pi * float64(step) / 48
				canvas.Dot(int(anchor.x+orbit*math.Cos(angle)), int(anchor.y+orbit*math.Sin(angle)), guide)
			}
		}
	}

	for _, edge := range graph.Edges {
		from, to := nodes[edge.From], nodes[edge.To]
		fp, fok := points[edge.From]
		tp, tok := points[edge.To]
		if !fok || !tok || offFrame(edge) {
			continue
		}
		age := view.Sub(time.Unix(0, edge.LastNS)).Seconds()
		if age < 0 {
			age = 0
		}
		fade, heat := cosmosEdgeLight(age, edge.Count)
		dashed := false
		fromColor, toColor := cosmosNodeColor(from), cosmosNodeColor(to)
		if edge.Kind == fleetdb.KindSpawn {
			dashed = true
			fromColor = rgbFromHex(configuredCosmosPalette.CosmosLineage)
		}
		if model.skyEnabled {
			if flightAge := view.Sub(
				time.Unix(0, edge.LastNS),
			); flightAge >= 0 &&
				flightAge < cosmosCometDuration(edge.Kind) {
				fade = 1.0
			}
		}
		// Heat whitens the rail toward CosmosBright and wraps it in a halo —
		// the same curve translated one braille row up and down, dimmer —
		// so a live conversation reads as a thick, shining cable and a
		// day-old one as a thin thread, both still visible.
		bright := rgbFromHex(configuredCosmosPalette.CosmosBright)
		fromColor = lerpRGB(fromColor, bright, 0.40*heat)
		toColor = lerpRGB(toColor, bright, 0.40*heat)
		rail := cosmosEdgeRail(fp, tp, cx, cy)
		x0, y0, cpx, cpy, x1, y1 := rail.x0, rail.y0, rail.cpx, rail.cpy, rail.x1, rail.y1
		if heat >= cosmosEdgeHaloHeat {
			halo := spotlight(edge, fade*0.55*heat)
			canvas.Bezier(x0, y0-1, cpx, cpy-1, x1, y1-1, fromColor, toColor, halo, dashed)
			canvas.Bezier(x0, y0+1, cpx, cpy+1, x1, y1+1, fromColor, toColor, halo, dashed)
		}
		canvas.Bezier(x0, y0, cpx, cpy, x1, y1, fromColor, toColor, spotlight(edge, fade), dashed)
	}

	if model.skyEnabled {
		// Ambient wind: every edge carries a slow drift of dim particles from
		// sender to receiver, always — so a quiet universe breathes between
		// real messages. Brightness follows the edge's own memory (a fresh
		// edge blows brighter for ~15 minutes) and the drift stays tail-less
		// and dim so it can never be misread as a comet, which is a real
		// message in flight.
		for _, edge := range graph.Edges {
			fp, fok := points[edge.From]
			tp, tok := points[edge.To]
			if !fok || !tok || offFrame(edge) {
				continue
			}
			age := view.Sub(time.Unix(0, edge.LastNS)).Seconds()
			if age < 0 {
				age = 0
			}
			memory := 0.22 + 0.45*math.Exp(-age/900)
			phase := cosmosPhase(edge.From + "\x00" + edge.To + "\x00" + edge.Kind)
			period := 5 + 2.5*math.Sin(phase)
			fromColor, toColor := cosmosNodeColor(nodes[edge.From]), cosmosNodeColor(nodes[edge.To])
			particles := 2
			if edge.Kind == fleetdb.KindSpawn {
				fromColor = rgbFromHex(configuredCosmosPalette.CosmosLineage)
				particles = 1
			}
			rail := cosmosEdgeRail(fp, tp, cx, cy)
			x0, y0, cpx, cpy, x1, y1 := rail.x0, rail.y0, rail.cpx, rail.cpy, rail.x1, rail.y1
			for index := 0; index < particles; index++ {
				progress := math.Mod(clock/period+phase/(2*math.Pi)+float64(index)*0.5, 1)
				px, py := BezierPoint(x0, y0, cpx, cpy, x1, y1, progress)
				shimmer := 0.7 + 0.3*math.Sin(clock*2.4+phase+float64(index)*math.Pi)
				canvas.Dot(
					int(px),
					int(py),
					scaleRGB(lerpRGB(fromColor, toColor, progress), spotlight(edge, memory*shimmer)),
				)
			}
		}

		white := rgbFromHex(configuredCosmosPalette.CosmosBright)
		const tailSegments = 18
		for _, edge := range graph.Edges {
			duration := cosmosCometDuration(edge.Kind)
			age := view.Sub(time.Unix(0, edge.LastNS))
			if age < 0 || age >= duration || offFrame(edge) {
				continue
			}
			from, to := nodes[edge.From], nodes[edge.To]
			fp, fok := points[edge.From]
			tp, tok := points[edge.To]
			if !fok || !tok {
				continue
			}
			rail := cosmosEdgeRail(fp, tp, cx, cy)
			x0, y0, cpx, cpy, x1, y1 := rail.x0, rail.y0, rail.cpx, rail.cpy, rail.x1, rail.y1
			t := float64(age) / float64(duration)
			base := lerpRGB(cosmosNodeColor(from), cosmosNodeColor(to), t)
			if edge.Kind == fleetdb.KindSpawn {
				base = lerpRGB(rgbFromHex(configuredCosmosPalette.CosmosLineage), white, 0.4)
			}
			et := ease(t)
			for tail := 0; tail < tailSegments; tail++ {
				tt := et - float64(tail)*0.018
				if tt < 0 {
					break
				}
				px, py := BezierPoint(x0, y0, cpx, cpy, x1, y1, tt)
				strength := 1 - float64(tail)/tailSegments
				color := scaleRGB(lerpRGB(base, white, 0.85*strength), spotlight(edge, 0.55+0.45*strength))
				canvas.Dot(int(px), int(py), color)
				if tail == 0 {
					// The head is a burst, not a dim streak segment: a
					// full-white core plus a round ring of light — round
					// because braille sub-cells are half as wide as they
					// are tall, so the x offset carries the same 1.6
					// correction the shockwave rings below use.
					canvas.Dot(int(px), int(py), scaleRGB(white, spotlight(edge, 1)))
					const burstDots = 10
					const burstRadius = 1.6
					for index := 0; index < burstDots; index++ {
						angle := 2 * math.Pi * float64(index) / burstDots
						bx := px + burstRadius*math.Cos(angle)*1.6
						by := py + burstRadius*math.Sin(angle)
						canvas.Dot(int(bx), int(by), color)
					}
				}
			}
		}

		for _, edge := range graph.Edges {
			var duration, startRadius, endRadius, blend float64
			var dotCount int
			if edge.Kind == fleetdb.KindSpawn {
				duration, startRadius, endRadius, blend, dotCount = 1.6, 2, 11, 0.5, 26
			} else {
				// Inject rings stay tighter, brighter, and shorter-lived
				// than a spawn ring so the two events keep reading as
				// different things happening, not the same ring twice.
				duration, startRadius, endRadius, blend, dotCount = 1.0, 1.5, 7, 0.8, 18
			}
			age := view.Sub(time.Unix(0, edge.LastNS)).Seconds()
			if age < 0 || age >= duration || frame.hidden[edge.To] {
				continue
			}
			point, ok := points[edge.To]
			if !ok {
				continue
			}
			progress := age / duration
			radius := startRadius + progress*(endRadius-startRadius)
			color := scaleRGB(lerpRGB(cosmosNodeColor(nodes[edge.To]), white, blend), spotlight(edge, 1-progress))
			for index := 0; index < dotCount; index++ {
				angle := 2 * math.Pi * float64(index) / float64(dotCount)
				canvas.Dot(int(point.x+radius*math.Cos(angle)*1.6), int(point.y+radius*math.Sin(angle)), color)
			}
		}
	}

	// Arrival/departure flashes are sky-only, like every other animated
	// glow in this function: --no-sky renders a static frame, full stop.
	var inboundFlash, outboundFlash map[string]float64
	if model.skyEnabled {
		inboundFlash = newestDirectionalFlash(graph.Edges, view.UnixNano(), func(edge compose.CosmosEdge) string {
			return edge.To
		})
		outboundFlash = newestDirectionalFlash(graph.Edges, view.UnixNano(), func(edge compose.CosmosEdge) string {
			return edge.From
		})
	}
	white := rgbFromHex(configuredCosmosPalette.CosmosBright)
	orbitDepth := func(key string) int {
		depth := 0
		for parent := moonParents[key]; parent != "" && depth <= 8; parent = moonParents[parent] {
			depth++
		}
		return depth
	}
	// Deepest moons draw first so that where a system crowds, the parent's
	// glyph and label overwrite the moon's — the anchor of a system stays
	// readable, the way the bigger body wins an eclipse.
	ordered := make([]compose.CosmosNode, 0, len(graph.Nodes))
	population := make(map[string]int)
	for _, node := range graph.Nodes {
		if frame.hidden[node.Key] {
			continue
		}
		ordered = append(ordered, node)
		population[node.Home]++
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		return orbitDepth(ordered[i].Key) > orbitDepth(ordered[j].Key)
	})
	var glyphStamps, starStamps []struct {
		x, y  int
		glyph rune
		color RGB
	}
	// The stars: one sun per project, drawn before the planets, while the
	// star glyphs replay LAST in the re-stamp below — a sun can never be
	// erased by its planets. A sun's radiance is its population and its
	// colour is its traffic (see drawCosmosSun); the population counted is
	// what this frame draws. The star's NAME waits until every planet has
	// landed: it is the one label that must read in a crowded system, so
	// it picks its row around the planets rather than under them.
	var starLabels []struct {
		x, y int
		text string
	}
	for _, home := range starOrder {
		point := starPoints[home]
		color := drawCosmosSun(
			canvas,
			point,
			home,
			population[home],
			cosmosStarTemperature(graph.Stars[home], view.UnixNano()),
			clock,
			model.skyEnabled,
		)
		colX, colY := int(point.x)/2, int(point.y)/4
		canvas.SetCell(colX, colY, '✹', color, true)
		label := ellipsizeRunes(cosmosHomeLabel(home), maxInt(0, canvas.Cols-2))
		starLabels = append(starLabels, struct {
			x, y int
			text string
		}{colX - len([]rune(label))/2, colY, label})
		starStamps = append(starStamps, struct {
			x, y  int
			glyph rune
			color RGB
		}{colX, colY, '✹', color})
	}
	for _, node := range ordered {
		point, ok := points[node.Key]
		if !ok {
			continue
		}
		colX, colY := int(point.x)/2, int(point.y)/4
		base := cosmosNodeColor(node)
		brightness := 0.85
		if model.skyEnabled && !node.Dead {
			// Ambient breath: each node glows on its own slow clock so a
			// quiet universe still reads as alive, not frozen.
			brightness = 0.80 + 0.10*math.Sin(clock*1.3+cosmosPhase(node.Key))
		}
		color := scaleRGB(base, brightness)
		arrival := math.Max(inboundFlash[node.Key], outboundFlash[node.Key])
		if arrival > 0 {
			color = lerpRGB(base, white, arrival*0.95)
		}
		bold := arrival > 0.4
		if node.Dead {
			// Reachable only from a replay ghost: BuildCosmos(live=true)
			// never emits a Dead node (see its own doc comment), so the
			// live sky never reaches this branch at all. A chat dead NOW
			// shown at a moment it was alive is a ghost of itself — the
			// truth, not a death to re-enact — so it renders dimmed and
			// still, never animated.
			color, bold = scaleRGB(base, 0.35), false
		}
		if node.Key == selected {
			// The reticle: a ring of light around the chosen body, turning
			// slowly under the sky, still under --no-sky. Its label reads at
			// full strength so the name is never in doubt.
			halo := scaleRGB(white, 0.9)
			spin := 0.0
			if model.skyEnabled {
				spin = clock * 0.8
			}
			const haloDots = 12
			for index := 0; index < haloDots; index++ {
				angle := spin + 2*math.Pi*float64(index)/haloDots
				canvas.Dot(int(point.x+2.2*math.Cos(angle)*1.6), int(point.y+2.2*math.Sin(angle)), halo)
			}
			bold = true
		}
		canvas.SetCell(colX, colY, cosmosNodeGlyph(node), color, true)
		glyphStamps = append(glyphStamps, struct {
			x, y  int
			glyph rune
			color RGB
		}{colX, colY, cosmosNodeGlyph(node), color})
		// A label grows away from its own SUN, not away from the galactic
		// centre — otherwise every label in a right-side system points the
		// same way and prints into its siblings. Moons refine it one tier
		// down: away from their parent planet.
		rightward := point.x >= cx
		if anchor, ok := starPoints[node.Home]; ok {
			rightward = point.x >= anchor.x
		}
		if parentKey := moonParents[node.Key]; parentKey != "" && !model.classicSky {
			if anchor, ok := points[parentKey]; ok {
				rightward = point.x >= anchor.x
			}
		}
		label := clipCosmosLabel(node.Label.String(), rightward, colX, canvas.Cols)
		// The label rides dimmer than its glyph — the terminal's "smaller
		// font" — so a busy sky reads markers first, names second. An
		// arriving chat flashes its name at full strength, and a dead
		// node's alarm colour is never softened.
		labelColor := scaleRGB(color, 0.62)
		if node.Dead || arrival > 0.4 || node.Key == selected {
			labelColor = color
		}
		labelX := colX + 2
		if !rightward {
			labelX = colX - 1 - len([]rune(label))
		}
		// Collision nudge: a name that would print over another name, a
		// glyph, or the legend steps one row down, then one row up, before
		// it accepts the overlap. Rows outside the canvas are not a refuge —
		// a label nudged off-canvas is a label lost.
		labelRow := colY
		if width := len([]rune(label)); !canvas.TextFree(labelX, labelRow, width) {
			for _, candidate := range []int{colY + 1, colY - 1} {
				if candidate >= 0 && candidate < canvas.Rows && canvas.TextFree(labelX, candidate, width) {
					labelRow = candidate
					break
				}
			}
		}
		canvas.Text(labelX, labelRow, label, labelColor, bold)
		if arrival > 0.55 {
			canvas.SetCell(colX, colY-1, '✦', scaleRGB(white, arrival), false)
		}
	}
	// Star names land last among the text: the row under the sun, or the
	// next row down, or the row above — whichever holds no planet's glyph
	// or name yet — and under the sun regardless when every row is taken.
	for _, label := range starLabels {
		row := label.y + 1
		width := len([]rune(label.text))
		for _, candidate := range []int{label.y + 1, label.y + 2, label.y - 1} {
			if candidate >= 0 && candidate < canvas.Rows && canvas.TextFree(label.x, candidate, width) {
				row = candidate
				break
			}
		}
		canvas.Text(label.x, row, label.text, cosmosDimColor(), false)
	}
	// Glyphs win over labels: a later node's text may have paved over an
	// earlier node's marker (a moon sitting inside its parent's long label),
	// and a chat that exists must never render invisible — so every node's
	// glyph is re-stamped after all text has landed, parents last, suns
	// after everything.
	for _, stamp := range glyphStamps {
		canvas.SetCell(stamp.x, stamp.y, stamp.glyph, stamp.color, true)
	}
	for _, stamp := range starStamps {
		canvas.SetCell(stamp.x, stamp.y, stamp.glyph, stamp.color, true)
	}
}

// cosmosHotTraffic is the hourly message count at which a star burns at the
// blue-white end of its spectrum; anything above is simply as hot.
const cosmosHotTraffic = 30

// cosmosCooling is how long a star's ember takes to fade by e after its
// last message, and cosmosEmber how bright that ember starts: a star does
// not drop cold the second its hour of traffic slides out of the window —
// it cools the way a star should, over hours.
const (
	cosmosCooling = 3 * time.Hour
	cosmosEmber   = 0.35
)

// cosmosStarTemperature is a star's place on the spectrum in [0, 1]: the
// hour's traffic sets the heat; a slower ember, decaying from the last
// message the star saw, keeps a recently busy system warmer than one that
// has been silent for a day. Measured at viewNS, so a replayed past shows
// the heat it had then.
func cosmosStarTemperature(star compose.CosmosStar, viewNS int64) float64 {
	heat := math.Min(1, float64(star.TrafficHour)/cosmosHotTraffic)
	if star.LastNS > 0 && viewNS >= star.LastNS {
		ember := cosmosEmber * math.Exp(-float64(viewNS-star.LastNS)/float64(cosmosCooling))
		heat = math.Max(heat, ember)
	}
	return heat
}

// drawCosmosSun paints one project star and returns the colour its glyph
// should wear. Two facts drive it, kept apart on purpose. Radiance is the
// POPULATION — the count of chats living under this star: a lone chat gets
// a modest ember; a crowded project burns with a wider corona, diffraction
// spikes from three planets up, and periodic solar flares from five. Colour
// is the TEMPERATURE — cosmosStarTemperature's reading of the system's
// traffic, the spectral class in one lerp: a sleeping project stays a warm
// dwarf, a busy one goes blue-white the way a hotter star does. Every
// element derives from (clock, home, population, temperature) alone, so a
// pinned test clock renders the same sun.
func drawCosmosSun(
	canvas *Canvas,
	point cosmosPoint,
	home string,
	population int,
	temperature, clock float64,
	sky bool,
) RGB {
	sunColor := rgbFromHex(configuredCosmosPalette.CosmosSun)
	hot := rgbFromHex(configuredCosmosPalette.CosmosSunHot)
	bright := rgbFromHex(configuredCosmosPalette.CosmosBright)
	heat := math.Min(1, float64(population)/8)
	base := lerpRGB(lerpRGB(sunColor, hot, temperature), bright, 0.25*heat)
	if !sky {
		return scaleRGB(base, 0.75+0.25*heat)
	}
	phase := cosmosPhase(home)
	glow := (0.78 + 0.10*heat) + (0.14+0.08*heat)*math.Sin(clock*(0.8+0.2*heat)+phase)

	// Corona: dots multiply and widen with the family.
	coronaRadius := 2.4 + math.Min(2.6, 0.25*float64(population))
	coronaDots := min(14, 6+population)
	for index := 0; index < coronaDots; index++ {
		angle := clock*0.5 + phase + 2*math.Pi*float64(index)/float64(coronaDots)
		shimmer := 0.30 + 0.25*heat + 0.20*math.Sin(clock*2.1+phase+float64(index))
		canvas.Dot(
			int(point.x+coronaRadius*math.Cos(angle)*1.6),
			int(point.y+coronaRadius*math.Sin(angle)),
			scaleRGB(base, shimmer),
		)
	}

	// Diffraction spikes from three planets up: four arms of light that
	// twinkle independently, longer on hotter stars.
	if population >= 3 {
		armLength := 2 + min(4, population-2)
		for arm := 0; arm < 4; arm++ {
			angle := float64(arm) * math.Pi / 2
			twinkle := 0.5 + 0.5*math.Sin(clock*1.7+phase+float64(arm)*1.9)
			for step := 2; step <= armLength; step++ {
				strength := (1 - float64(step)/float64(armLength+1)) * (0.35 + 0.45*twinkle)
				canvas.Dot(
					int(point.x+float64(step)*math.Cos(angle)*1.6),
					int(point.y+float64(step)*math.Sin(angle)),
					scaleRGB(lerpRGB(base, bright, 0.4), strength),
				)
			}
		}
	}

	// Solar flares from five planets up: every few seconds an arc erupts
	// off the corona and fades — which bucket of time it is decides when
	// and where, so the eruption is pure clockwork.
	if population >= 5 {
		const flarePeriod = 7.0
		bucket := int64(clock / flarePeriod)
		seed := uint64(bucket)*0x9E3779B97F4A7C15 + uint64(len(home))*0xBF58476D1CE4E5B9
		seed ^= seed >> 29
		progress := math.Mod(clock, flarePeriod) / 2.2
		if seed%2 == 0 && progress < 1 {
			eruptAt := phase + float64(seed%628)/100
			reach := coronaRadius + 1.5 + 4*progress
			for index := 0; index < 5; index++ {
				angle := eruptAt + (float64(index)-2)*0.16
				strength := (1 - progress) * (1 - math.Abs(float64(index)-2)/3) * (0.4 + 0.4*heat)
				canvas.Dot(
					int(point.x+reach*math.Cos(angle)*1.6),
					int(point.y+reach*math.Sin(angle)),
					scaleRGB(lerpRGB(base, bright, 0.6), strength),
				)
			}
		}
	}
	return scaleRGB(base, glow)
}

// cosmosLabelCap is the flat rune ceiling on every node label, canvas space
// notwithstanding: in a crowded system it is the labels, not the orbits,
// that turn the sky into noise, and fourteen runes keep enough of a name to
// recognize it while the glyph carries the rest.
const cosmosLabelCap = 14

// clipCosmosLabel bounds a node's label to the canvas space actually
// available in the direction it draws — and to cosmosLabelCap on top — so a
// long label (a long chat name, or the bracketed diagnostic an unresolved
// identity renders as) is clipped with a visible ellipsis instead of
// running off-canvas or smearing across whatever sits past the edge.
func clipCosmosLabel(label string, rightward bool, colX, cols int) string {
	available := cols - (colX + 2)
	if !rightward {
		available = colX - 1
	}
	if available < 0 {
		available = 0
	}
	if available > cosmosLabelCap {
		available = cosmosLabelCap
	}
	return ellipsizeRunes(label, available)
}

// cosmosStarPoints seats every star — one per distinct Home among the visible
// chats, the unknown "" home included — on the galactic ring, or alone at the
// centre when the whole fleet lives in one place. Sorted order keeps both the
// seating and the draw order deterministic.
func cosmosStarPoints(nodes map[string]compose.CosmosNode, cx, cy, rx, ry float64) ([]string, map[string]cosmosPoint) {
	seen := make(map[string]bool)
	for _, node := range nodes {
		seen[node.Home] = true
	}
	order := make([]string, 0, len(seen))
	for home := range seen {
		order = append(order, home)
	}
	sort.Strings(order)
	points := make(map[string]cosmosPoint, len(order))
	if len(order) == 1 {
		points[order[0]] = cosmosPoint{x: cx, y: cy}
		return order, points
	}
	for index, home := range order {
		// The ring starts at π — the LEFT of the canvas — so two stars land
		// left and right, not stacked top and bottom: a terminal is wide
		// before it is tall, and the wide axis is where systems fit.
		angle := math.Pi + 2*math.Pi*float64(index)/float64(len(order))
		points[home] = cosmosPoint{x: cx + rx*0.70*math.Cos(angle), y: cy + ry*0.62*math.Sin(angle)}
	}
	return order, points
}

func cosmosLayout(
	canvas *Canvas,
	seats map[string]*cosmosSeat,
	nodes map[string]compose.CosmosNode,
	edges []compose.CosmosEdge,
	now time.Time,
	sky, classic bool,
	focus string,
) cosmosFrame {
	top := 4.0
	bottom := float64((canvas.Rows - 4) * 4)
	cx := float64(canvas.PW()) / 2
	cy := (top + bottom) / 2
	rx := float64(canvas.PW())/2 - 26
	if rx < 12 {
		rx = 12
	}
	ry := (bottom-top)/2 - 5
	if ry < 6 {
		ry = 6
	}
	clock := float64(now.UnixNano()) / 1e9
	// The star tier: every chat orbits ITS OWN star — the project it lives
	// in. One star sits at the galactic centre with the full canvas as its
	// system; several stars share the ring, each with a tighter system.
	// The classic sky has no tier at all: empty star maps mean the sun and
	// guide loops downstream simply have nothing to draw.
	var starOrder []string
	starPoints := map[string]cosmosPoint{}
	hidden := map[string]bool{}
	systemRx, systemRy := rx, ry
	if !classic {
		starOrder, starPoints = cosmosStarPoints(nodes, cx, cy, rx, ry)
		if _, focused := starPoints[focus]; focus != "" && focused {
			// Focus: the chosen star takes the galactic centre and the full
			// system. Every other star keeps its bearing but moves just past
			// the frame — its chats collapse onto that exit point — so an
			// edge into a neighbouring system is seen to LEAVE toward it,
			// never to vanish. The exit sits 12px beyond the rectangle's
			// edge along the bearing, not on an ellipse: an ellipse's 45°
			// point lands inside a wide rectangle.
			for home, point := range starPoints {
				if home == focus {
					starPoints[home] = cosmosPoint{x: cx, y: cy}
					continue
				}
				bearing := math.Atan2(point.y-cy, point.x-cx)
				dx, dy := math.Cos(bearing), math.Sin(bearing)
				halfW, halfH := float64(canvas.PW())/2+12, float64(canvas.PH())/2+12
				reach := 1 / math.Max(math.Abs(dx)/halfW, math.Abs(dy)/halfH)
				starPoints[home] = cosmosPoint{x: cx + reach*dx, y: cy + reach*dy}
			}
			starOrder = []string{focus}
			for key, node := range nodes {
				if node.Home != focus {
					hidden[key] = true
				}
			}
		} else if len(starOrder) > 1 {
			// Sized against the 0.70/0.62 star ring above: adjacent systems
			// keep clear water between their rings instead of printing into
			// each other.
			systemRx, systemRy = rx*0.34, ry*0.42
		}
	}
	parents, children := cosmosOrbits(edges, nodes)
	points := make(map[string]cosmosPoint, len(seats))
	ringSets := make(map[string]map[float64]bool)
	for key, seat := range seats {
		angle, breath := seat.Angle, 1.0
		if sky {
			// Orbital sway: every seat drifts a couple of degrees around its
			// anchor on its own slow clock, so nodes float instead of
			// standing pinned. --no-sky reduces to the seat's exact values.
			phase := cosmosPhase(key)
			angle += 0.035 * math.Sin(clock*0.45+phase)
			breath = 1 + 0.015*math.Sin(clock*0.31+phase*1.7)
		}
		if classic {
			// The first cosmos: one shared ring around the galactic centre,
			// no anchor star and no rigid revolution — the seat's own angle
			// IS the position.
			points[key] = cosmosPoint{
				x: cx + rx*breath*math.Cos(angle),
				y: cy + ry*breath*math.Sin(angle),
			}
			continue
		}
		home := nodes[key].Home
		anchor, anchored := starPoints[home]
		if !anchored {
			anchor = cosmosPoint{x: cx, y: cy}
		}
		// The planets actually revolve: each ring turns rigidly around its
		// sun at its own Keplerian speed — the phase is the seat's own
		// accumulated Orbit (advanceCosmos), never the wall clock, so a
		// change of ring changes speed without a jump. Even spacing between
		// ring-mates is preserved by construction. --no-sky never ticks, so
		// there Orbit stays zero and the frame stays still.
		angle += seat.Orbit
		points[key] = cosmosPoint{
			x: anchor.x + systemRx*seat.Ring*breath*math.Cos(angle),
			y: anchor.y + systemRy*seat.Ring*breath*math.Sin(angle),
		}
		if parents[key] == "" && !hidden[key] {
			if ringSets[home] == nil {
				ringSets[home] = make(map[float64]bool)
			}
			ringSets[home][seat.RingTarget] = true
		}
	}
	// Planetary hierarchy: a spawned chat leaves the main ring and becomes
	// its parent's moon — the ring point computed above stays behind only as
	// the seat fallback. All moons of one parent share ONE orbit: the same
	// distance from the parent, evenly spaced from each other, the ring
	// widening with the brood so a bigger family never crowds. Like the
	// earth: the moon circles its planet while the planet keeps circling
	// the sun — and a moon's own children circle IT, one level down.
	if !classic && len(parents) != 0 {
		var place func(key string, depth int) cosmosPoint
		place = func(key string, depth int) cosmosPoint {
			parentKey := parents[key]
			if parentKey == "" || depth > 8 {
				return points[key]
			}
			anchor := place(parentKey, depth+1)
			moons := children[parentKey]
			slot := 0
			for index, moon := range moons {
				if moon == key {
					slot = index
					break
				}
			}
			orbit := cosmosMoonOrbit(len(moons))
			angle := 2*math.Pi*float64(slot)/float64(len(moons)) + cosmosPhase(parentKey)
			if sky {
				angle += clock * 0.09 // one slow revolution per ~70s
			}
			// No 1.6 aspect widening here on purpose: the orbit renders
			// vertical-major, so moons spend most of their revolution above
			// or below the parent's label row instead of ploughing through
			// the text beside it.
			return cosmosPoint{
				x: anchor.x + orbit*math.Cos(angle),
				y: anchor.y + orbit*math.Sin(angle),
			}
		}
		for key := range parents {
			points[key] = place(key, 0)
		}
	}
	// Out-of-focus bodies collapse onto their star's exit point AFTER moon
	// placement, so no moon of a hidden parent can swing back into frame.
	for key := range hidden {
		if anchor, ok := starPoints[nodes[key].Home]; ok {
			points[key] = anchor
		}
	}
	rings := make(map[string][]float64, len(ringSets))
	for home, set := range ringSets {
		factors := make([]float64, 0, len(set))
		for factor := range set {
			factors = append(factors, factor)
		}
		sort.Float64s(factors)
		rings[home] = factors
	}
	return cosmosFrame{
		points: points, starOrder: starOrder, starPoints: starPoints,
		systemRx: systemRx, systemRy: systemRy, cx: cx, cy: cy,
		rings: rings, hidden: hidden,
	}
}

type cosmosRail struct {
	x0, y0, cpx, cpy, x1, y1 float64
}

func cosmosEdgeRail(from, to cosmosPoint, cx, cy float64) cosmosRail {
	x0, y0, x1, y1 := from.x, from.y, to.x, to.y
	mx, my := (x0+x1)/2, (y0+y1)/2
	cpx := mx + (cx-mx)*0.30
	cpy := my + (cy-my)*0.30
	return cosmosRail{x0: x0, y0: y0, cpx: cpx, cpy: cpy, x1: x1, y1: y1}
}

func ease(t float64) float64 { return t * t * (3 - 2*t) }

func drawCosmosTicker(canvas *Canvas, graph compose.CosmosGraph) {
	nodes := cosmosNodeMap(graph.Nodes)
	limit := min(2, len(graph.Edges))
	for index := 0; index < limit; index++ {
		row := canvas.Rows - 2 - index
		color := cosmosDimColor()
		if index == 0 {
			color = rgbFromHex(configuredCosmosPalette.CosmosBright)
		}
		line := cosmosTickerLine(graph.Edges[index], nodes)
		canvas.Text(1, row, ellipsizeRunes(line, maxInt(0, canvas.Cols-2)), color, false)
	}
}

func cosmosTickerLine(edge compose.CosmosEdge, nodes map[string]compose.CosmosNode) string {
	from, to := nodes[edge.From], nodes[edge.To]
	return fmt.Sprintf("%s %c %s → %c %s · %s",
		time.Unix(0, edge.LastNS).Format("15:04:05"),
		cosmosNodeGlyph(from), from.Label.String(),
		cosmosNodeGlyph(to), to.Label.String(),
		edge.LastMessage,
	)
}

func (model Model) drawCosmosBanner(canvas *Canvas) {
	lines := []string{
		"⚠ comms ledger unreachable: " + model.cosmos.Err,
		"the sky below is the last recorded frame — not a live view",
	}
	width := 0
	for _, line := range lines {
		if n := len([]rune(line)); n > width {
			width = n
		}
	}
	width += 4
	if width > canvas.Cols-2 {
		width = canvas.Cols - 2
	}
	x := (canvas.Cols - width) / 2
	y := canvas.Rows/2 - 2
	alarm := rgbFromHex(configuredCosmosPalette.Warn)
	canvas.Text(x, y, "┌"+repeat('─', width-2)+"┐", alarm, false)
	for index, line := range lines {
		line = ellipsizeRunes(line, maxInt(0, width-2))
		color := rgbFromHex(configuredCosmosPalette.CosmosBright)
		if index > 0 {
			color = cosmosDimColor()
		}
		padding := width - 2 - len([]rune(line))
		left := padding / 2
		canvas.Text(x, y+1+index, "│"+repeat(' ', left)+line+repeat(' ', padding-left)+"│", color, index == 0)
		canvas.SetCell(x, y+1+index, '│', alarm, false)
		canvas.SetCell(x+width-1, y+1+index, '│', alarm, false)
	}
	canvas.Text(x, y+1+len(lines), "└"+repeat('─', width-2)+"┘", alarm, false)
}

func centerCosmosText(canvas *Canvas, row int, text string, color RGB, bold bool) {
	text = ellipsizeRunes(text, canvas.Cols)
	x := (canvas.Cols - len([]rune(text))) / 2
	if x < 0 {
		x = 0
	}
	canvas.Text(x, row, text, color, bold)
}

func repeat(char rune, count int) string {
	if count < 0 {
		count = 0
	}
	result := make([]rune, count)
	for index := range result {
		result[index] = char
	}
	return string(result)
}

func ellipsizeRunes(value string, maximum int) string {
	if maximum <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= maximum {
		return value
	}
	if maximum == 1 {
		return "…"
	}
	return string(runes[:maximum-1]) + "…"
}

func ansiTruncateRunes(value string, maximum int) string { return ellipsizeRunes(value, maximum) }

func cosmosNodeMap(nodes []compose.CosmosNode) map[string]compose.CosmosNode {
	result := make(map[string]compose.CosmosNode, len(nodes))
	for _, node := range nodes {
		result[node.Key] = node
	}
	return result
}

func newestDirectionalFlash(
	edges []compose.CosmosEdge,
	nowNS int64,
	endpoint func(compose.CosmosEdge) string,
) map[string]float64 {
	latest := make(map[string]int64)
	for _, edge := range edges {
		key := endpoint(edge)
		if edge.LastNS > latest[key] {
			latest[key] = edge.LastNS
		}
	}
	result := make(map[string]float64, len(latest))
	for key, atNS := range latest {
		age := float64(nowNS-atNS) / float64(time.Second)
		if age >= 0 && age < 1.5 {
			result[key] = math.Max(0, 1-age/1.5)
		}
	}
	return result
}

func cosmosNodeColor(node compose.CosmosNode) RGB {
	if id, err := pfmengine.Parse(node.Engine); err == nil {
		return rgbFromHex(configuredCosmosPalette.StatsEngine[id])
	}
	return rgbFromHex(configuredCosmosPalette.Muted)
}

func cosmosNodeGlyph(node compose.CosmosNode) rune {
	switch pfmengine.ID(node.Engine) {
	case pfmengine.Codex:
		return '▲'
	case pfmengine.OpenCode:
		return '◆'
	case pfmengine.Claude:
		return '✳'
	default:
		return '·'
	}
}

func cosmosDimColor() RGB { return rgbFromHex(configuredCosmosPalette.Dim) }
