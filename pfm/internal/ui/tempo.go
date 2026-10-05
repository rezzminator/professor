package ui

import (
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// The tempo axis is the list's time ruler. Every chat in view is a star on a
// log-time line — stale at the left edge, fresh at the right — coloured by its
// engine; the selected chat is the white diamond. Moving the cursor moves the
// diamond, so the operator sees WHERE in time a chat lives before opening it,
// and a glance along the line reads the fleet's rhythm: a cluster at the right
// is a busy hour, a long dark stretch is a weekend.
//
// It is two lines under the rows: the stars, then the ruler that is also the
// panel's bottom border.

// tempoMark is one labelled tick on the ruler.
type tempoMark struct {
	label string
	age   time.Duration
}

var tempoMarks = []tempoMark{
	{"7d", 7 * 24 * time.Hour},
	{"1d", 24 * time.Hour},
	{"6h", 6 * time.Hour},
	{"1h", time.Hour},
	{"10m", 10 * time.Minute},
	{"1m", time.Minute},
}

// tempoBin is the chats that land on one cell of the axis.
type tempoBin struct {
	count   int
	live    int
	weights map[pfmengine.ID]int
}

func (bin *tempoBin) add(engine pfmengine.ID, live bool) {
	if bin.weights == nil {
		bin.weights = make(map[pfmengine.ID]int, 2)
	}
	bin.count++
	weight := 1
	if live {
		bin.live++
		weight = 3
	}
	bin.weights[engine] += weight
}

// dominant is the engine with the most weight in the bin; ties break to the
// registry order so the colour never flickers between frames.
func (bin *tempoBin) dominant() pfmengine.ID {
	best, bestWeight := pfmengine.ID(""), 0
	for _, id := range pfmengine.All() {
		if weight := bin.weights[id]; weight > bestWeight {
			best, bestWeight = id, weight
		}
	}
	return best
}

// tempoStar is the glyph for a bin of this size.
func tempoStar(count int) string {
	switch {
	case count <= 1:
		return "•"
	case count <= 3:
		return "●"
	default:
		return "◉"
	}
}

// tempoBins drops every visible chat onto the axis, memoised for the last axis
// width per message that can change it and per clock reading (deckAgg). The
// slice is shared; callers only read it.
func (model Model) tempoBins(cells int) []tempoBin {
	agg := model.deck.agg
	if agg == nil {
		return model.dropOnAxis(cells)
	}
	if agg.binsRev != model.deck.rev || agg.binsNowNS != model.nowNS || agg.binsCells != cells || agg.bins == nil {
		agg.bins = model.dropOnAxis(cells)
		agg.binsRev, agg.binsNowNS, agg.binsCells = model.deck.rev, model.nowNS, cells
	}
	return agg.bins
}

func (model Model) dropOnAxis(cells int) []tempoBin {
	bins := make([]tempoBin, cells)
	for _, rowIndex := range model.filtered {
		row := model.rows[rowIndex]
		if !hasRecency(row) {
			continue
		}
		column := heatColumn(heatOf(rowAgeNS(row, model.nowNS)), cells)
		bins[column].add(compose.EngineForKind(row.Kind), row.Kind.IsLiveSeat())
	}
	return bins
}

// tempoStars renders the star line, exactly cells wide.
func (model Model) tempoStars(cells int) string {
	if cells <= 0 {
		return ""
	}
	palette := configuredPalette
	bins := model.tempoBins(cells)
	selectedColumn := -1
	if row, ok := model.selectedRow(); ok && hasRecency(row) {
		selectedColumn = heatColumn(heatOf(rowAgeNS(row, model.nowNS)), cells)
	}
	spans := make([]span, 0, cells)
	for column := range bins {
		bin := &bins[column]
		switch {
		case column == selectedColumn:
			spans = append(spans, span{text: "◆", paint: tone{fg: palette.Header, bold: true}})
		case bin.count == 0:
			spans = append(spans, span{text: " "})
		default:
			hex := palette.EngineRow[bin.dominant()]
			if bin.live == 0 {
				hex = blendHex(hex, palette.HeatCold, 0.45)
			}
			spans = append(spans, span{text: tempoStar(bin.count), paint: tone{fg: hex, bold: bin.live > 0}})
		}
	}
	return joinSpans(mergeSpans(spans))
}

// tempoRuler renders the ruler, exactly width cells including its corners, as
// the list panel's bottom border: ticks at one week, a day, six hours, an hour,
// ten minutes and a minute — each labelled where it fits — and a triangle under
// the selected chat's diamond with its age beside it.
func (model Model) tempoRuler(width int) string {
	inner := width - 2
	if inner <= 0 {
		return ""
	}
	palette := configuredPalette
	const (
		kindRule = iota
		kindTick
		kindSelected
	)
	glyphs := []rune(strings.Repeat("─", inner))
	kinds := make([]uint8, inner)
	occupied := make([]bool, inner+1)
	place := func(column int, text string, kind uint8) bool {
		label := []rune(text)
		end := column + len(label)
		if column < 0 || end > inner {
			return false
		}
		for index := max(0, column-1); index <= end && index < len(occupied); index++ {
			if occupied[index] {
				return false
			}
		}
		for offset, value := range label {
			glyphs[column+offset] = value
			kinds[column+offset] = kind
		}
		for index := column; index < end; index++ {
			occupied[index] = true
		}
		return true
	}
	if row, ok := model.selectedRow(); ok && hasRecency(row) {
		column := heatColumn(heatOf(rowAgeNS(row, model.nowNS)), inner)
		age := formatAge(row, model.nowNS)
		// The triangle and its age are one label, so the occupancy guard never
		// mistakes the triangle for a neighbour of its own age: age to the right,
		// else to the left of the triangle, else the triangle alone.
		_ = place(column, "▲"+age, kindSelected) ||
			place(column-len([]rune(age)), age+"▲", kindSelected) ||
			place(column, "▲", kindSelected)
	}
	for _, mark := range tempoMarks {
		column := heatColumn(heatOf(int64(mark.age)), inner)
		place(column, "┴"+mark.label, kindTick)
	}
	spans := []span{{text: "╰", paint: tone{fg: palette.Border}}}
	for index, value := range glyphs {
		paint := tone{fg: palette.Border}
		switch kinds[index] {
		case kindTick:
			paint = tone{fg: palette.Dim}
		case kindSelected:
			paint = tone{fg: palette.Header, bold: true}
		}
		spans = append(spans, span{text: string(value), paint: paint})
	}
	spans = append(spans, span{text: "╯", paint: tone{fg: palette.Border}})
	return joinSpans(mergeSpans(spans))
}

// mergeSpans folds neighbours with an identical tone into one run, so a line of
// a hundred cells renders as a handful of escape sequences, not a hundred.
func mergeSpans(spans []span) []span {
	merged := make([]span, 0, len(spans))
	for _, part := range spans {
		if last := len(merged) - 1; last >= 0 && merged[last].paint == part.paint {
			merged[last].text += part.text
			continue
		}
		merged = append(merged, part)
	}
	return merged
}
