// Package sky renders the pfm fleet picker's orbiting-engine corner widget.
// Every live engine is a distinct body in a strip of terminal cells (~18x3).
// Each frame is a pure function of the clock and live-chat counts; the widget
// never gathers data itself.
// Births and deaths are injected by the caller as Events.
package sky

import (
	"math"
	"sort"
	"strconv"
	"strings"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// EventKind labels a fleet event the caller observed.
type EventKind uint8

const (
	EventBirth EventKind = iota // a chat was born: shooting star across the sky
	EventDeath                  // a chat died: fading red ember at the edge
)

// Event is one injected fleet event. StartNS is on the same clock as
// Options.TimeNS (the moment the caller observed the event).
type Event struct {
	Kind    EventKind
	StartNS int64
}

// Animation clock constants, exported so callers can prune expired events.
const (
	OrbitPeriodNS int64 = 10_000_000_000 // one full revolution: ~10s
	CometDurNS    int64 = 700_000_000    // birth comet lifetime
	EmberDurNS    int64 = 900_000_000    // death ember lifetime
)

// Expired reports whether e no longer affects any frame at nowNS. Frame
// ignores expired events regardless; this is for the caller's pruning.
func (e Event) Expired(nowNS int64) bool {
	d := CometDurNS
	if e.Kind == EventDeath {
		d = EmberDurNS
	}
	return nowNS-e.StartNS >= d
}

// Options is the complete input of one frame; Frame is pure in it.
type Options struct {
	// Counts is the engine-generic live roster. ClaudeCount/CodexCount remain
	// compatibility inputs for callers that have not yet materialized a map.
	Counts      map[pfmengine.ID]int
	ClaudeCount int // live Claude chats: 0 = absent, 1-2 dim, 3-6 bright, 7+ ringed
	CodexCount  int // live Codex chats, same scale

	Width, Height int // widget size in cells; the design target is 18x3

	TimeNS int64 // the clock; the orbit is a pure function of it

	Events []Event // recent births/deaths; expired ones are ignored

	Colorize bool // ANSI colors via lipgloss; false = plain runes (tests)
}

const (
	depthFar    = -0.15 // orbit sin below this = far side: render one level dimmer
	twinkleNS   = 350_000_000
	ryFrac      = 0.9 // vertical orbit amplitude as a fraction of half-height
	cometTailLn = 3
)

// Frame renders one frame as Height lines of exactly Width cells.
// Returns nil when the region is too small to mean anything.
func Frame(o Options) []string {
	if o.Width < 8 || o.Height < 2 {
		return nil
	}
	g := newGrid(o.Width, o.Height)
	drawBackground(g)
	drawBodies(g, o)
	drawEvents(g, o)
	return g.render(o.Colorize)
}

// Snapshot is the legacy two-engine one-line static form. New callers use
// SnapshotCounts so every registered engine gets a cluster.
// Count 0 renders as a lone faint dot.
func Snapshot(claude, codex int) string {
	return snapHalf(claude, pfmengine.Claude) + " " + snapHalf(codex, pfmengine.Codex)
}

// SnapshotCounts renders every live registered engine in stable ID order.
// The two original engine slots remain visible at zero for statusline
// continuity; later engines appear as soon as they have a live chat.
func SnapshotCounts(counts map[pfmengine.ID]int) string {
	parts := make([]string, 0, len(pfmengine.All()))
	for _, id := range pfmengine.All() {
		if id != pfmengine.Claude && id != pfmengine.Codex && counts[id] <= 0 {
			continue
		}
		parts = append(parts, snapHalf(counts[id], id))
	}
	return strings.Join(parts, " ")
}

func snapHalf(n int, id pfmengine.ID) string {
	if n <= 0 {
		return styleTab[clsBgStar].Render("·")
	}
	var glyph string
	var lvl int
	switch tier(n) {
	case 1:
		glyph, lvl = "·", 1
	case 2:
		glyph, lvl = "✦", 2
	default:
		glyph, lvl = "✹", 3
	}
	return styleTab[bodyCls(id, lvl)].Render(glyph + strconv.Itoa(n))
}

// tier maps a live-chat count to visual weight.
func tier(count int) int {
	switch {
	case count <= 0:
		return 0
	case count <= 2:
		return 1
	case count <= 6:
		return 2
	default:
		return 3
	}
}

// ---- grid ----

type grid struct {
	w, h  int
	glyph []rune
	cls   []styleClass
}

func newGrid(w, h int) *grid {
	g := &grid{w: w, h: h, glyph: make([]rune, w*h), cls: make([]styleClass, w*h)}
	for i := range g.glyph {
		g.glyph[i] = ' '
	}
	return g
}

// set paints one cell; out-of-bounds writes are clipped silently.
func (g *grid) set(x, y int, r rune, c styleClass) {
	if x < 0 || x >= g.w || y < 0 || y >= g.h {
		return
	}
	i := y*g.w + x
	g.glyph[i] = r
	g.cls[i] = c
}

// render folds the grid into lines, run-length grouping style spans so the
// ANSI output stays compact.
func (g *grid) render(colorize bool) []string {
	lines := make([]string, g.h)
	var sb strings.Builder
	run := make([]rune, 0, g.w)
	for y := 0; y < g.h; y++ {
		sb.Reset()
		runCls := clsNone
		flush := func() {
			if len(run) == 0 {
				return
			}
			s := string(run)
			if colorize && runCls != clsNone {
				s = styleTab[runCls].Render(s)
			}
			sb.WriteString(s)
			run = run[:0]
		}
		for x := 0; x < g.w; x++ {
			i := y*g.w + x
			if g.cls[i] != runCls {
				flush()
				runCls = g.cls[i]
			}
			run = append(run, g.glyph[i])
		}
		flush()
		lines[y] = sb.String()
	}
	return lines
}

// ---- layers ----

// drawBackground scatters a few fixed faint stars so the widget reads as a
// sky even when the fleet is empty. Positions are fractions of the region,
// so any size gets the same constellation.
func drawBackground(g *grid) {
	pts := [][2]float64{{0.18, 0.0}, {0.68, 1.0}, {0.93, 0.25}}
	for _, p := range pts {
		x := int(p[0] * float64(g.w-1))
		y := int(p[1] * float64(g.h-1))
		g.set(x, y, '·', clsBgStar)
	}
}

// drawBodies places every live engine on a shared orbit. Each body's radius
// is proportional to the activity in the other bodies, so a busy engine sits
// nearer the middle while lighter peers swing wide.
// A body whose count is 0 is absent; a lone survivor rests at the center.
func drawBodies(g *grid, o Options) {
	counts := make(map[pfmengine.ID]int, len(o.Counts)+2)
	for id, count := range o.Counts {
		counts[id] = count
	}
	if _, present := counts[pfmengine.Claude]; !present {
		counts[pfmengine.Claude] = o.ClaudeCount
	}
	if _, present := counts[pfmengine.Codex]; !present {
		counts[pfmengine.Codex] = o.CodexCount
	}
	theta := 2 * math.Pi * float64(o.TimeNS) / float64(OrbitPeriodNS)
	cx := float64(g.w-1) / 2
	cy := float64(g.h-1) / 2
	rxMax := float64(g.w)/2 - 2 // margin for the tier-3 sparkle ring
	if rxMax < 1 {
		rxMax = 1
	}
	ryMax := cy * ryFrac

	type body struct {
		e        pfmengine.ID
		count    int
		r        float64
		sin, cos float64
	}
	active := make([]body, 0, len(counts))
	total := 0
	for _, id := range pfmengine.All() {
		if count := counts[id]; count > 0 {
			active = append(active, body{e: id, count: count})
			total += count
		}
	}
	if len(active) == 0 {
		return
	}
	maxCounterweight := 0
	for _, candidate := range active {
		maxCounterweight = max(maxCounterweight, total-candidate.count)
	}
	for index := range active {
		phase := theta + 2*math.Pi*float64(index)/float64(len(active))
		active[index].sin = math.Sin(phase)
		active[index].cos = math.Cos(phase)
		if len(active) > 1 && maxCounterweight > 0 {
			active[index].r = float64(total-active[index].count) / float64(maxCounterweight)
		}
	}
	// Far bodies draw first so nearer bodies remain legible on overlap.
	sort.SliceStable(active, func(left, right int) bool { return active[left].sin < active[right].sin })
	for _, b := range active {
		fx := cx + b.r*rxMax*b.cos
		fy := cy + b.r*ryMax*b.sin
		far := len(active) > 1 && b.sin < depthFar
		drawBody(g, b.e, b.count, fx, fy, far, o.TimeNS)
	}
}

// drawBody paints one star at its tier's visual weight. far drops every
// part one brightness level — the depth cue that sells the orbit.
func drawBody(g *grid, e pfmengine.ID, count int, fx, fy float64, far bool, timeNS int64) {
	t := tier(count)
	if t == 0 {
		return
	}
	shift := 0
	if far {
		shift = -1
	}
	lvl := func(l int) styleClass { return bodyCls(e, l+shift) }
	x, y := int(math.Round(fx)), int(math.Round(fy))
	switch t {
	case 1: // 1-2 chats: a small dim glyph
		g.set(x, y, '·', lvl(1))
	case 2: // 3-6 chats: bright core + 1-2 sparkles
		g.set(x, y, '✦', lvl(2))
		g.set(x+1, y, twinkle(timeNS, x+1), lvl(1))
		if count >= 5 {
			g.set(x-1, y, twinkle(timeNS, x-1), lvl(1))
		}
	default: // 7+: bold core + sparkle ring
		g.set(x, y, '✦', lvl(3))
		ring := [4][2]int{{-1, 0}, {1, 0}, {0, -1}, {0, 1}}
		for i, d := range ring {
			g.set(x+d[0], y+d[1], twinkle(timeNS, x+i), lvl(1))
		}
	}
}

// twinkle picks a sparkle rune from the clock and a position salt, so the
// glitter shimmers without any state.
func twinkle(timeNS int64, salt int) rune {
	if (timeNS/twinkleNS+int64(salt))%4 == 0 {
		return '+'
	}
	return '·'
}

// drawEvents paints every live event; expired or future ones are skipped.
// Comets draw last: a birth outshines everything for its 700ms.
func drawEvents(g *grid, o Options) {
	for _, e := range o.Events {
		if e.Kind == EventDeath {
			drawEmber(g, e, o.TimeNS)
		}
	}
	for _, e := range o.Events {
		if e.Kind == EventBirth {
			drawComet(g, e, o.TimeNS)
		}
	}
}

// drawComet streaks a shooting star across the full width. The descent
// direction alternates with the event's start slot so twin births differ.
func drawComet(g *grid, e Event, nowNS int64) {
	p := float64(nowNS-e.StartNS) / float64(CometDurNS)
	if p < 0 || p >= 1 {
		return
	}
	x0, x1 := -2.0, float64(g.w+1)
	y0, y1 := 0.0, float64(g.h-1)
	if (e.StartNS/CometDurNS)%2 != 0 {
		y0, y1 = y1, y0
	}
	hx := x0 + p*(x1-x0)
	hy := y0 + p*(y1-y0)
	dx, dy := x1-x0, y1-y0
	n := math.Hypot(dx, dy)
	ux, uy := dx/n, dy/n

	// One-cell tail spacing keeps the streak contiguous: at 8fps the comet
	// lives ~5 frames, and a gapped tail reads as noise, not motion.
	tailCls := [cometTailLn]styleClass{clsCometTail, clsCometFade, clsCometFade}
	tailRune := [cometTailLn]rune{'*', '·', '·'}
	for k := cometTailLn; k >= 1; k-- {
		tx := hx - ux*float64(k)
		ty := hy - uy*float64(k)
		g.set(int(math.Round(tx)), int(math.Round(ty)), tailRune[k-1], tailCls[k-1])
	}
	g.set(int(math.Round(hx)), int(math.Round(hy)), '✦', clsCometHead)
}

// drawEmber fades a dying red spark at the right edge: hot → dim → dark.
func drawEmber(g *grid, e Event, nowNS int64) {
	p := float64(nowNS-e.StartNS) / float64(EmberDurNS)
	if p < 0 || p >= 1 {
		return
	}
	x, y := g.w-2, g.h/2
	switch {
	case p < 1.0/3:
		g.set(x, y, '✦', clsEmberHot)
		g.set(x-1, y, '·', clsEmberMid)
	case p < 2.0/3:
		g.set(x, y, '*', clsEmberMid)
	default:
		g.set(x, y, '·', clsEmberDark)
	}
}
