package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// Column budget of one deck row, left to right:
//
//	rail · marker · name (flexes) · badges │ work · model · prompts · size · heat ruler · age
//
// The right cluster sheds columns as the list narrows — ruler first, then
// size, then prompts, then the model — and the badges shrink before the name
// does, so the name, the live gauge and the age survive down to the 40-column
// floor.
const (
	deckNameMin   = 14
	deckNameMax   = 56
	deckBadgesW   = 12
	deckBadgesMin = 6
	deckPromptsW  = 5
	deckSizeW     = 5
	deckAgeW      = 4
	deckChipW     = 13
	// deckPulseNS is the period of a working chat's marker pulse.
	deckPulseNS = int64(500 * time.Millisecond)
	// unnamedChat stands in for a row that has no name yet.
	unnamedChat = "(unnamed)"
)

// deckColumn names one right-cluster column.
type deckColumn uint8

const (
	deckColPrompts deckColumn = iota
	deckColSize
	deckColRuler
	deckColChip
	deckColAge
	deckColWork
	deckColModel
)

func (column deckColumn) width() int {
	switch column {
	case deckColPrompts:
		return deckPromptsW
	case deckColSize:
		return deckSizeW
	case deckColRuler:
		return heatRulerCells
	case deckColChip:
		return deckChipW
	case deckColWork:
		return deckWorkW
	case deckColModel:
		return deckModelW
	default:
		return deckAgeW
	}
}

// deckLayout is the resolved column plan of one row width.
type deckLayout struct {
	lead    int
	name    int
	badges  int
	gap     int
	columns []deckColumn
}

// deckTiers lists the right clusters from richest to barest. The selected row
// trades its heat ruler for the armed action's chip, so the carousel is on the
// row at every width — the dossier lists the whole menu beside it.
func deckTiers(chip bool) [][]deckColumn {
	if chip {
		return deckChipTiers
	}
	return deckRulerTiers
}

// The tier tables are read for every row of every frame and never written, so
// they are built once.
var (
	deckChipTiers = [][]deckColumn{
		{deckColWork, deckColModel, deckColPrompts, deckColSize, deckColChip, deckColAge},
		{deckColWork, deckColModel, deckColPrompts, deckColChip, deckColAge},
		{deckColWork, deckColModel, deckColChip, deckColAge},
		{deckColWork, deckColChip, deckColAge},
		{deckColChip, deckColAge},
		{deckColAge},
	}
	deckRulerTiers = [][]deckColumn{
		{deckColWork, deckColModel, deckColPrompts, deckColSize, deckColRuler, deckColAge},
		{deckColWork, deckColModel, deckColPrompts, deckColSize, deckColAge},
		{deckColWork, deckColModel, deckColPrompts, deckColAge},
		{deckColWork, deckColModel, deckColAge},
		{deckColWork, deckColAge},
		{deckColAge},
	}
)

func deckClusterWidth(columns []deckColumn) int {
	total := 0
	for _, column := range columns {
		total += column.width() + 1
	}
	return total
}

// deckPlan picks the richest cluster, then the fullest badge column, that
// still leaves the name its minimum width.
func deckPlan(width int, grouped, chip bool) deckLayout {
	lead := 4
	if grouped {
		lead += 2
	}
	for _, columns := range deckTiers(chip) {
		cluster := deckClusterWidth(columns)
		for _, badges := range []int{deckBadgesW, deckBadgesMin, 0} {
			used := lead + cluster
			if badges > 0 {
				used += badges + 1
			}
			if rest := width - used; rest >= deckNameMin {
				name := min(rest, deckNameMax)
				return deckLayout{lead: lead, name: name, badges: badges, gap: rest - name, columns: columns}
			}
		}
	}
	columns := []deckColumn{deckColAge}
	return deckLayout{lead: lead, name: max(1, width-lead-deckClusterWidth(columns)), columns: columns}
}

// liveMarker is rowMarker for the deck: a live chat that is mid-turn, itself or
// through its agents (the picker read that from its transcript), pulses its
// dot. The clock only ticks while the picker is being watched, which is exactly
// when the pulse should move.
func liveMarker(row compose.Row, nowNS int64) string {
	if workActive(row) && (nowNS/deckPulseNS)%2 == 1 {
		return "◉"
	}
	return rowMarker(row.Kind)
}

// badgeSpans paints the badge column, exactly width cells: as many badges as
// fit, then padding.
func badgeSpans(parts []badgePart, width int, bg string) []span {
	if width <= 0 {
		return nil
	}
	palette := configuredPalette
	spans := make([]span, 0, len(parts)*2+1)
	used := 0
	for _, part := range parts {
		cost := cellWidth(part.text)
		if used > 0 {
			cost++
		}
		if used+cost > width {
			break
		}
		if used > 0 {
			spans = append(spans, span{text: " ", paint: tone{bg: bg}})
		}
		paint := tone{fg: palette.Muted, bg: bg}
		switch part.kind {
		case badgeWarn:
			paint = tone{fg: palette.Warn, bg: bg}
		case badgeDim:
			paint = tone{fg: palette.Dim, bg: bg, italic: true}
		case badgePlain:
		}
		spans = append(spans, span{text: part.text, paint: paint})
		used += cost
	}
	if used < width {
		spans = append(spans, span{text: strings.Repeat(" ", width-used), paint: tone{bg: bg}})
	}
	return spans
}

// engineHexOf is the colour that identifies a row's engine.
func engineHexOf(row compose.Row) string {
	palette := configuredPalette
	if row.Kind == compose.Agent {
		return palette.AgentRow
	}
	if hex := palette.EngineRow[compose.EngineForKind(row.Kind)]; hex != "" {
		return hex
	}
	return palette.Accent
}

// nameSpans paints a row's name: the merged new-chat row shows the engine
// choice, every other row its name with the search matches lit.
func (model Model) nameSpans(row compose.Row, width int, name, bg string, base tone) []span {
	palette := configuredPalette
	if model.mergeNewChat && isNewChatActionKind(row.Kind) {
		spans := make([]span, 0, 8)
		labels := make([]string, 0, 4)
		used := 0
		for index, id := range model.newChatEnginesFor(row) {
			label := pfmengine.MustLookup(id).Short
			paint := tone{fg: palette.Muted, bg: bg}
			if id == model.effectiveNewChatEngine(row) {
				label = "[ " + label + " ]"
				paint = tone{fg: palette.Accent, bg: bg, bold: true}
			}
			labels = append(labels, label)
			if index > 0 {
				spans = append(spans, span{text: " ", paint: tone{bg: bg}})
				used++
			}
			spans = append(spans, span{text: label, paint: paint})
			used += ansi.StringWidth(label)
		}
		if used > width {
			// Too narrow for the whole choice: keep one honest run, cut with an
			// ellipsis, rather than let the row outgrow its column.
			return []span{{text: padRightCells(strings.Join(labels, " "), width), paint: base}}
		}
		if used < width {
			spans = append(spans, span{text: strings.Repeat(" ", width-used), paint: tone{bg: bg}})
		}
		return spans
	}
	shown := padRightCells(name, width)
	lit := tone{fg: palette.Warn, bg: bg, bold: true}
	return highlightSpans(shown, model.query.Value(), base, lit)
}

// renderGroupedRow renders one deck row, exactly width cells. grouped indents
// it under a name-group header.
func (model Model) renderGroupedRow(
	row compose.Row,
	selected bool,
	width int,
	grouped bool,
) string {
	if notice, ok := model.renderNoticeRow(row, selected, width, grouped); ok {
		return notice
	}
	palette := configuredPalette
	isAction := isNewChatActionKind(row.Kind)
	chip := selected && !isAction
	plan := deckPlan(width, grouped, chip)
	bg := ""
	if selected {
		bg = palette.Selected
	}
	name := cleanField(row.Name)
	if name == "" {
		name = unnamedChat
	}

	engineHex := engineHexOf(row)
	recent := hasRecency(row)
	age := rowAgeNS(row, model.nowNS)
	heat := heatOf(age)
	floor := 0.30
	if row.Kind.IsAddressable() {
		floor = 0.55
	}
	shade := engineHex
	if recent {
		shade = heatShade(engineHex, palette.HeatCold, floor+(1-floor)*heat)
	}
	if glow := model.deck.glow(row, model.nowNS); glow > 0 {
		// A chat that just arrived flares toward white and fades back into its
		// own colour over arrivalGlowNS (deckstate.go).
		shade = blendHex(shade, "#ffffff", arrivalFlarePeak*glow)
	}
	ink := tone{fg: shade, bg: bg}
	switch {
	case row.Reminded:
		shade = palette.LimitRed
		ink = tone{fg: shade, bg: bg, bold: true}
	case selected:
		ink = tone{fg: palette.Header, bg: bg, bold: true}
	case row.Killed:
		ink = tone{fg: palette.Dim, bg: bg, italic: true}
	}

	railPaint := tone{fg: blendHex(palette.Border, groupHexOf(model, row), 0.55), bg: bg}
	rail := "│"
	if selected {
		// The cursor is a chevron, not a bar: `› ● name` is the screen-scraped
		// contract of the lane scripts and the attach jail (tui_selected).
		rail = "›"
		railPaint = tone{fg: palette.Accent, bg: bg, bold: true}
	}
	markerPaint := tone{fg: shade, bg: bg, bold: row.Kind.IsLiveSeat()}
	switch {
	case row.Kind == compose.Booting:
		markerPaint = tone{fg: palette.Warn, bg: bg}
	case isAction:
		markerPaint = tone{fg: palette.Accent, bg: bg, bold: true}
	}

	spans := []span{
		{text: rail, paint: railPaint},
		{text: strings.Repeat(" ", plan.lead-3), paint: tone{bg: bg}},
		{text: liveMarker(row, model.nowNS), paint: markerPaint},
		{text: " ", paint: tone{bg: bg}},
	}
	spans = append(spans, model.nameSpans(row, plan.name, name, bg, ink)...)
	if plan.badges > 0 {
		spans = append(spans, span{text: " ", paint: tone{bg: bg}})
		spans = append(spans, badgeSpans(rowBadgeParts(row), plan.badges, bg)...)
	}
	if plan.gap > 0 {
		spans = append(spans, span{text: strings.Repeat(" ", plan.gap), paint: tone{bg: bg}})
	}
	for _, column := range plan.columns {
		spans = append(spans, span{text: " ", paint: tone{bg: bg}})
		spans = append(spans, model.columnSpans(column, row, recent, heat, shade, bg)...)
	}
	return joinSpans(spans)
}

// groupHexOf is the colour of the project's rail: the alternating A/B group
// colours, so a project's rows read as one block.
func groupHexOf(model Model, row compose.Row) string {
	palette := configuredPalette
	project := cleanField(row.Project)
	if project == "" {
		project = "?"
	}
	if model.projectOrdinal(project)%2 == 1 {
		return palette.GroupB
	}
	return palette.GroupA
}

// columnSpans paints one right-cluster column of a row.
func (model Model) columnSpans(
	column deckColumn,
	row compose.Row,
	recent bool,
	heat float64,
	shade, bg string,
) []span {
	palette := configuredPalette
	dim := tone{fg: palette.Dim, bg: bg}
	blank := func() []span {
		return []span{{text: strings.Repeat(" ", column.width()), paint: tone{bg: bg}}}
	}
	switch column {
	case deckColPrompts:
		if !recent {
			return blank()
		}
		return []span{{text: padLeftCells(fmt.Sprintf("%dp", row.PromptCount), deckPromptsW), paint: dim}}
	case deckColSize:
		if !recent {
			return blank()
		}
		return []span{{text: padLeftCells(sizeBadge(row), deckSizeW), paint: dim}}
	case deckColRuler:
		if !recent {
			return blank()
		}
		hot := engineHexOf(row)
		if row.Reminded {
			hot = palette.LimitRed
		}
		return []span{{text: heatRuler(heat, hot, palette.HeatCold, palette.Border, bg)}}
	case deckColChip:
		return []span{{
			text:  padRightCells(carouselCompact(model.actionIndex), deckChipW),
			paint: tone{fg: palette.Accent, bg: bg, bold: true},
		}}
	case deckColWork:
		return workGauge(row, model.nowNS, engineHexOf(row), palette.HeatCold, bg)
	case deckColModel:
		return modelCell(row, bg)
	default:
		if !recent {
			return blank()
		}
		return []span{{text: padLeftCells(formatAge(row, model.nowNS), deckAgeW), paint: tone{fg: shade, bg: bg}}}
	}
}

// renderNoticeRow gives action and failure notices their full-width treatment.
func (model Model) renderNoticeRow(row compose.Row, selected bool, width int, grouped bool) (string, bool) {
	if !isNoticeKind(row.Kind) {
		return "", false
	}
	pointer := "│ "
	if selected {
		pointer = "› "
	}
	if grouped {
		pointer += "  "
	}
	name := cleanField(row.Name)
	if name == "" {
		name = unnamedChat
	}
	if row.Kind == compose.ProfessorUpdateFailed {
		return renderProfessorUpdateFailedRow(pointer, name, selected, width), true
	}
	if row.Kind == compose.WorkbenchInvalid {
		return renderWorkbenchInvalidRow(pointer, name, selected, width), true
	}
	if model.mergeNewChat {
		ids := model.newChatEngines()
		labels := make([]string, 0, len(ids))
		for _, id := range ids {
			label := pfmengine.MustLookup(id).Short
			if id == model.newChatEngine {
				labels = append(labels, "◖ "+label+" ◗")
			} else {
				labels = append(labels, "[ "+label+" ]")
			}
		}
		name += "  " + strings.Join(labels, " ")
	}
	sparkle := "✦"
	if (model.nowNS/int64(500*time.Millisecond))%2 != 0 {
		sparkle = "✧"
	}
	content := pointer + sparkle + " PROFESSOR UPDATE " + sparkle + "  " + name + "  Enter → guided upgrade"
	line := fillLine(ansi.Truncate(content, width, "…"), width)
	if selected {
		return professorUpdateSelectedStyle.Render(line), true
	}
	return professorUpdateStyle.Render(line), true
}
