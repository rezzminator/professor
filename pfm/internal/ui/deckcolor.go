package ui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// tone is one foreground/background pairing for a run of text. The zero
// background leaves the terminal's own, so a panel stays transparent and only
// the selected row, the chips and the masthead paint one.
type tone struct {
	fg, bg string
	bold   bool
	dim    bool
	italic bool
}

func (paint tone) style() lipgloss.Style {
	style := lipgloss.NewStyle()
	if paint.fg != "" {
		style = style.Foreground(lipgloss.Color(paint.fg))
	}
	if paint.bg != "" {
		style = style.Background(lipgloss.Color(paint.bg))
	}
	return style.Bold(paint.bold).Faint(paint.dim).Italic(paint.italic)
}

// render paints text; empty text renders to nothing so a hidden column leaves
// no stray escape sequence behind. A deck frame paints hundreds of runs, so the
// common case — #rrggbb colours and the three text attributes — writes its one
// SGR sequence directly instead of building a lipgloss style per run; anything
// else (a named colour, say) still goes through lipgloss (sgr.go).
func (paint tone) render(text string) string {
	if text == "" {
		return ""
	}
	if !truecolorOK(paint.fg) || !truecolorOK(paint.bg) {
		return paint.style().Render(text)
	}
	var buf strings.Builder
	buf.Grow(len(text) + 48)
	if !paint.writeSGR(&buf) {
		return text
	}
	buf.WriteString(text)
	buf.WriteString("\x1b[m")
	return buf.String()
}

func hexDigit(value byte) (uint8, bool) {
	switch {
	case value >= '0' && value <= '9':
		return value - '0', true
	case value >= 'a' && value <= 'f':
		return value - 'a' + 10, true
	case value >= 'A' && value <= 'F':
		return value - 'A' + 10, true
	}
	return 0, false
}

// onBackground returns the same tone over bg, the way a selected row lays its
// highlight under every segment instead of wrapping the line in one style
// (a wrap is cancelled by the first inner reset).
func (paint tone) onBackground(bg string) tone {
	if bg != "" {
		paint.bg = bg
	}
	return paint
}

// span is one painted run of a composed line.
type span struct {
	text  string
	paint tone
}

// joinSpans paints spans into one line, every run written straight into it.
func joinSpans(spans []span) string {
	size := 0
	for index := range spans {
		size += len(spans[index].text) + 48 // the longest opening sequence and its reset
	}
	var line strings.Builder
	line.Grow(size)
	for index := range spans {
		spans[index].paint.renderTo(&line, spans[index].text)
	}
	return line.String()
}

func spansWidth(spans []span) int {
	width := 0
	for _, part := range spans {
		width += cellWidth(part.text)
	}
	return width
}

// hexOfRGB spells a colour the way the palette does.
func hexOfRGB(colour RGB) string {
	const digits = "0123456789abcdef"
	spelled := [7]byte{
		'#',
		digits[colour.R>>4], digits[colour.R&0xf],
		digits[colour.G>>4], digits[colour.G&0xf],
		digits[colour.B>>4], digits[colour.B&0xf],
	}
	return string(spelled[:])
}

// blendHex mixes two palette colours; t=0 is from, t=1 is to.
func blendHex(from, to string, t float64) string {
	return hexOfRGB(lerpRGB(rgbFromHex(from), rgbFromHex(to), t))
}

// padRightCells fits text to exactly width cells, left-aligned: a long value
// ends in "…", a short one is padded.
func padRightCells(text string, width int) string {
	if width <= 0 {
		return ""
	}
	measured := cellWidth(text)
	if measured > width {
		text = ansi.Truncate(text, width, "…")
		measured = cellWidth(text)
	}
	if gap := width - measured; gap > 0 {
		text += strings.Repeat(" ", gap)
	}
	return text
}

// padLeftCells is padRightCells right-aligned, for the numeric columns.
func padLeftCells(text string, width int) string {
	if width <= 0 {
		return ""
	}
	measured := cellWidth(text)
	if measured > width {
		text = ansi.Truncate(text, width, "…")
		measured = cellWidth(text)
	}
	if gap := width - measured; gap > 0 {
		text = strings.Repeat(" ", gap) + text
	}
	return text
}

// chipOf is the picker's pill: bold text over a solid colour with one cell of
// air on each side.
func chipOf(text string, paint tone) string {
	paint.bold = true
	return paint.render(" " + text + " ")
}
