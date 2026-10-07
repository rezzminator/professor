package ui

import (
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/compose"
)

// The orbit gauge is the tiny live picture of a chat at work: a core for the
// chat itself and one arc, circling it, for each of its sub-agents that is
// mid-turn — so how many agents are working reads as how many arcs are turning,
// at a glance, without a number.
//
//	◉◐◓◑◒+    the chat is mid-turn, with at least five agents out (+ is "more")
//	◌◐◓       only its agents are working: the chat is orchestrating
//	◉         the chat itself is working, no agents
//	          (blank)  idle: nothing is moving, so nothing is drawn
//
// The arcs are the four half-disc glyphs ◐ ◓ ◑ ◒ stepped clockwise, each one
// step behind its neighbour, so the arcs read as a wave running along them and
// not as a row of identical spinners. Brightness ripples along them on a second,
// slower beat. Only Geometric Shapes are used: the Block Elements and Braille
// that a gauge would usually reach for are the ones a VS Code terminal's glyph
// atlas corrupts (microsoft/vscode#332859).
const (
	// deckWorkW is the gauge's column: core, four arcs, and the "more" mark.
	deckWorkW   = 6
	workMaxArcs = 4
	// workFrameNS is the animation's frame: the ambient sky tick's period, the
	// finest the picker's clock moves in. An arc advances every second frame.
	workFrameNS = int64(125 * time.Millisecond)
	// workWavePeriod is how many frames the brightness ripple takes to cycle.
	workWavePeriod = 12.0
	// workWaveLag is how far, in radians, each arc trails its neighbour.
	workWaveLag = 0.9
)

// workArcs are the half-disc glyphs in clockwise order: solid enough to read at
// one cell, and a spinner everyone already knows how to read.
var workArcs = [workMaxArcs]string{"◐", "◓", "◑", "◒"}

// workActive reports whether a row has anything moving to draw: a chat that is
// mid-turn or has agents mid-turn. Only a live chat can be either.
func workActive(row compose.Row) bool {
	return row.Kind.IsLiveSeat() && (row.Working || row.AgentsWorking > 0)
}

// workWave is the brightness of the i-th cell at a frame, 0..1.
func workWave(frame int64, cell int) float64 {
	phase := 2*math.Pi*float64(frame%int64(workWavePeriod))/workWavePeriod - float64(cell)*workWaveLag
	return 0.5 + 0.5*math.Sin(phase)
}

// workGauge paints a row's orbit gauge, exactly deckWorkW cells, at the frame
// nowNS falls in. hot is the chat's own colour and cold the colour of a dim
// cell; bg is the row's background.
func workGauge(row compose.Row, nowNS int64, hot, cold, bg string) []span {
	if !workActive(row) {
		return []span{{text: strings.Repeat(" ", deckWorkW), paint: tone{bg: bg}}}
	}
	palette := configuredPalette
	frame := nowNS / workFrameNS
	spans := make([]span, 0, deckWorkW)
	core, corePaint := "◌", tone{fg: blendHex(cold, hot, 0.45+0.25*workWave(frame, 0)), bg: bg}
	if row.Working {
		core = "●"
		if (frame/4)%2 == 0 {
			core = "◉"
		}
		corePaint = tone{fg: blendHex(hot, "#ffffff", 0.35*workWave(frame, 0)), bg: bg, bold: true}
	}
	spans = append(spans, span{text: core, paint: corePaint})
	arcs := min(row.AgentsWorking, workMaxArcs)
	for index := range workMaxArcs {
		if index >= arcs {
			spans = append(spans, span{text: " ", paint: tone{bg: bg}})
			continue
		}
		glyph := workArcs[(int(frame/2)+index)%workMaxArcs]
		shade := blendHex(cold, hot, 0.6+0.4*workWave(frame, index+1))
		spans = append(spans, span{text: glyph, paint: tone{fg: shade, bg: bg, bold: true}})
	}
	if row.AgentsWorking > workMaxArcs {
		return append(spans, span{text: "+", paint: tone{fg: palette.Accent, bg: bg, bold: true}})
	}
	return append(spans, span{text: " ", paint: tone{bg: bg}})
}

// workSummary is the words beside the gauge in the dossier.
func workSummary(row compose.Row) string {
	agents := ""
	switch {
	case row.AgentsWorking == 1:
		agents = "1 agent"
	case row.AgentsWorking > 1:
		agents = strconv.Itoa(row.AgentsWorking) + " agents"
	}
	switch {
	case row.Working && agents != "":
		return "mid-turn · " + agents
	case row.Working:
		return "mid-turn"
	case agents != "":
		return agents + " working"
	}
	return ""
}
