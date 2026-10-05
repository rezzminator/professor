package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/sky"
)

const (
	// skyWidgetWidth is the corner widget's width; the masthead gives it the
	// right edge of all three header lines.
	skyWidgetWidth = 18
	// mastheadSweepNS is how long the light takes to cross the masthead after a
	// keystroke, and mastheadSweepRadius how wide its glow is, in cells.
	mastheadSweepNS     = int64(1300 * time.Millisecond)
	mastheadSweepRadius = 12.0
	// mastheadBands quantises the gradient: the eye cannot tell 32 steps from
	// 100, and a line of runs costs far less to paint than a line of cells.
	mastheadBands = 32
	mastheadInk   = "#0b1020"
)

// renderHeader draws the three header lines — masthead, tabs and the tab's own
// context line — with the sky widget in the corner when it is on.
func (model Model) renderHeader(width int) string {
	contentWidth := width
	if model.skyEnabled {
		contentWidth = maxInt(1, width-skyWidgetWidth)
	}
	lines := []string{
		model.mastheadTitle(contentWidth),
		model.renderTabs(contentWidth),
	}
	switch model.tab {
	case TabStats:
		lines = append(lines, model.renderStatsHeader(contentWidth))
	case TabLimits:
		lines = append(lines, dimStyle.Render(fillLine(
			" Limits · live usage windows across every account",
			contentWidth,
		)))
	case TabCosmos:
		lines = append(lines, dimStyle.Render(fillLine(model.renderCosmosSubheader(), contentWidth)))
	default:
		lines = append(lines, model.chatsHeaderLine(contentWidth))
	}
	if !model.skyEnabled {
		return strings.Join(lines, "\n")
	}
	widget := sky.Frame(sky.Options{
		Counts:   liveEngineCounts(model.rows),
		Width:    skyWidgetWidth,
		Height:   3,
		TimeNS:   model.nowNS,
		Events:   model.skyEvents,
		Colorize: true,
	})
	for index := range lines {
		lines[index] = fillLine(lines[index], contentWidth) + widget[index]
	}
	return strings.Join(lines, "\n")
}

// fleetVitals counts what the masthead reports: live chats per engine, and the
// resumable, agent and booting rows.
type fleetVitals struct {
	live                       map[pfmengine.ID]int
	resumable, agents, booting int
}

// fleetVitals is memoised per message that can change it (deckAgg); callers
// only read the returned value.
func (model Model) fleetVitals() fleetVitals {
	agg := model.deck.agg
	if agg == nil {
		return model.countVitals()
	}
	if agg.vitalsRev != model.deck.rev || agg.vitals.live == nil {
		agg.vitals = model.countVitals()
		agg.vitalsRev = model.deck.rev
	}
	return agg.vitals
}

func (model Model) countVitals() fleetVitals {
	vitals := fleetVitals{live: liveEngineCounts(model.rows)}
	for index := range model.rows {
		switch model.rows[index].Kind {
		case compose.ResumeClaude, compose.ResumeCodex, compose.ResumeOpenCode:
			if model.visibleInView(model.rows[index]) {
				vitals.resumable++
			}
		case compose.Agent:
			vitals.agents++
		case compose.Booting:
			vitals.booting++
		}
	}
	// The sky counts an agent and a booting seat as running Claude processes;
	// the masthead names them on their own, so it must not count them twice.
	vitals.live[pfmengine.Claude] = max(0, vitals.live[pfmengine.Claude]-vitals.agents-vitals.booting)
	return vitals
}

// mastheadTitle is the first header line: the wordmark and the fleet's vitals
// over a gradient band that a keystroke sweeps with light.
func (model Model) mastheadTitle(width int) string {
	palette := configuredPalette
	ink := tone{fg: palette.Header}
	count := tone{fg: palette.Header, bold: true}
	word := tone{fg: palette.Muted}
	spans := []span{
		{text: " ◆ pfm ", paint: tone{fg: mastheadInk, bg: palette.Accent, bold: true}},
		{text: "  ", paint: ink},
	}
	vitals := model.fleetVitals()
	anyLive := false
	for _, id := range pfmengine.All() {
		if vitals.live[id] == 0 {
			continue
		}
		anyLive = true
		spans = append(spans,
			span{text: "● ", paint: tone{fg: palette.EngineRow[id], bold: true}},
			span{text: fmt.Sprintf("%d", vitals.live[id]), paint: count},
			span{text: " " + pfmengine.MustLookup(id).Short + "   ", paint: word},
		)
	}
	if !anyLive {
		spans = append(spans, span{text: "no live chats   ", paint: tone{fg: palette.Muted, italic: true}})
	}
	if vitals.resumable > 0 {
		spans = append(spans,
			span{text: "↻ ", paint: tone{fg: palette.Muted}},
			span{text: fmt.Sprintf("%d", vitals.resumable), paint: count},
			span{text: " resumable   ", paint: word},
		)
	}
	if vitals.agents > 0 {
		spans = append(spans,
			span{text: "⚙ ", paint: tone{fg: palette.AgentRow}},
			span{text: fmt.Sprintf("%d", vitals.agents), paint: count},
			span{text: " agents   ", paint: word},
		)
	}
	if vitals.booting > 0 {
		spans = append(spans,
			span{text: "◐ ", paint: tone{fg: palette.Warn}},
			span{text: fmt.Sprintf("%d", vitals.booting), paint: count},
			span{text: " booting", paint: word},
		)
	}
	return model.mastheadFill(spans, width)
}

// sweepCentre is where the keystroke's light is, in cells from the left edge,
// and whether it is on screen at all. The sweep is keyed to the activity clock,
// not to a frame counter, so it plays once per keystroke and the masthead is
// still the instant the picker goes quiet. A picker with no clock (plain, tests)
// never sweeps.
func (model Model) sweepCentre(width int) (float64, bool) {
	if model.activity == nil || model.nowNS <= 0 {
		return 0, false
	}
	elapsed := model.nowNS - model.activity.StampNS()
	if elapsed < 0 || elapsed >= mastheadSweepNS {
		return 0, false
	}
	progress := float64(elapsed) / float64(mastheadSweepNS)
	return -mastheadSweepRadius + progress*(float64(width)+2*mastheadSweepRadius), true
}

// mastheadFill lays spans over a left-to-right gradient of the header band,
// exactly width cells, with the sweep's glow added where it is.
func (model Model) mastheadFill(spans []span, width int) string {
	palette := configuredPalette
	type mastCell struct {
		text  string
		paint tone
	}
	cells := make([]mastCell, 0, width)
	used := 0
fill:
	for _, part := range spans {
		for _, value := range part.text {
			cellWidth := ansi.StringWidth(string(value))
			if used+cellWidth > width {
				break fill
			}
			cells = append(cells, mastCell{text: string(value), paint: part.paint})
			used += cellWidth
		}
	}
	for ; used < width; used++ {
		cells = append(cells, mastCell{text: " "})
	}
	centre, sweeping := model.sweepCentre(width)
	runs := make([]span, 0, 16)
	x := 0
	for _, cell := range cells {
		paint := cell.paint
		if paint.bg == "" {
			band := 0.0
			if width > 1 {
				band = float64(x*mastheadBands/width) / float64(mastheadBands-1)
			}
			bg := blendHex(palette.HeaderBg, palette.Selected, band)
			if sweeping {
				if distance := (float64(x) - centre) / mastheadSweepRadius; distance > -1 && distance < 1 {
					glow := (1 - distance*distance)
					bg = blendHex(bg, "#ffffff", 0.22*glow*glow)
				}
			}
			paint.bg = bg
		}
		if last := len(runs) - 1; last >= 0 && runs[last].paint == paint {
			runs[last].text += cell.text
		} else {
			runs = append(runs, span{text: cell.text, paint: paint})
		}
		x += ansi.StringWidth(cell.text)
	}
	return joinSpans(runs)
}

// renderTabs draws the tab chips on a flat strip of the header colour: the open
// tab is a solid chip, the rest dim. The strip is also what keeps the palette's
// HeaderBg on every frame whatever the masthead's sweep is doing.
func (model Model) renderTabs(width int) string {
	palette := configuredPalette
	strip := func(paint tone) tone {
		paint.bg = palette.HeaderBg
		return paint
	}
	names := []string{" Chats ", " Stats ", " Limits ", " cosmos "}
	// " tabs " is the readiness marker the lane scripts wait on (tui_open).
	spans := []span{{text: " tabs ", paint: strip(tone{fg: palette.Dim})}, {text: " ", paint: strip(tone{})}}
	for index, name := range names {
		if Tab(index) == model.tab {
			spans = append(spans, span{text: name, paint: tone{fg: mastheadInk, bg: palette.Accent, bold: true}})
		} else {
			spans = append(spans, span{text: name, paint: strip(tone{fg: palette.Muted})})
		}
		spans = append(spans, span{text: " ", paint: strip(tone{})})
	}
	spans = append(spans, span{text: "  tab/shift+tab", paint: strip(tone{fg: palette.Dim})})
	if rest := width - spansWidth(spans); rest > 0 {
		spans = append(spans, span{text: strings.Repeat(" ", rest), paint: strip(tone{})})
	}
	return joinSpans(spans)
}

// chatsHeaderLine is the Chats tab's third header line: the context the list is
// read in — account, cache, counts — or, when the reminder flags could not be
// read, that failure: an unreadable flag store never renders as "no reminders".
func (model Model) chatsHeaderLine(width int) string {
	palette := configuredPalette
	if model.reminderError != "" {
		return warnStyle.Render(fillLine(" Chats · reminder flags unreadable: "+model.reminderError, width))
	}
	headerAccount := model.primary
	headerMedal := accountMedal(headerAccount)
	if len(model.accountIDs) == 0 {
		if len(model.codexAccountIDs) != 0 {
			headerAccount = model.codexPrimary
			headerMedal = codexAccountMedal(headerAccount)
		} else if len(model.openCodeAccountIDs) != 0 {
			headerAccount = model.openCodePrimary
			headerMedal = accountMedal(headerAccount)
		}
	}
	cache := "🪫 5m"
	if model.cache1H {
		cache = "⚡ 1h"
	}
	dim := tone{fg: palette.Dim}
	value := tone{fg: palette.Header, bold: true}
	sep := span{text: " · ", paint: dim}
	spans := []span{
		{text: " Chats · ", paint: tone{fg: palette.Muted}},
		{text: headerMedal + " account ", paint: dim},
		{text: fmt.Sprintf("%d", headerAccount), paint: value},
		sep,
		{text: cache, paint: tone{fg: palette.Muted}},
		sep,
		{text: fmt.Sprintf("%d", len(model.rows)), paint: value},
		{text: " rows", paint: dim},
		sep,
		{text: fmt.Sprintf("%d", model.killedCount), paint: value},
		{text: " hidden", paint: dim},
		sep,
		{text: fmt.Sprintf("%d", model.suppressedCount), paint: value},
		{text: " empty", paint: dim},
	}
	if model.refreshing {
		spans = append(spans, sep, span{text: "⟳ refreshing", paint: tone{fg: palette.Accent}})
	}
	return fillLine(joinSpans(spans), width)
}
