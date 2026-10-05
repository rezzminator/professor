package ui

import (
	"fmt"
	"strconv"
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
// else (a named colour, say) still goes through lipgloss.
func (paint tone) render(text string) string {
	if text == "" {
		return ""
	}
	fg, fgOK := truecolorParams(paint.fg)
	bg, bgOK := truecolorParams(paint.bg)
	if !fgOK || !bgOK {
		return paint.style().Render(text)
	}
	var buf strings.Builder
	buf.Grow(len(text) + 48)
	sep := func() string {
		if buf.Len() == 0 {
			buf.WriteString("\x1b[")
			return ""
		}
		return ";"
	}
	if paint.bold {
		buf.WriteString(sep())
		buf.WriteByte('1')
	}
	// lipgloss writes italic before faint; the same order keeps the two paths
	// byte-identical.
	if paint.italic {
		buf.WriteString(sep())
		buf.WriteByte('3')
	}
	if paint.dim {
		buf.WriteString(sep())
		buf.WriteByte('2')
	}
	if fg != "" {
		buf.WriteString(sep())
		buf.WriteString("38;2;")
		buf.WriteString(fg)
	}
	if bg != "" {
		buf.WriteString(sep())
		buf.WriteString("48;2;")
		buf.WriteString(bg)
	}
	if buf.Len() == 0 {
		return text
	}
	buf.WriteByte('m')
	buf.WriteString(text)
	buf.WriteString("\x1b[m")
	return buf.String()
}

// truecolorParams spells a #rrggbb colour as the r;g;b of a truecolor SGR. An
// empty colour is valid and spells nothing; anything that is not #rrggbb is not
// ok, and the caller falls back to lipgloss for it.
func truecolorParams(hex string) (string, bool) {
	if hex == "" {
		return "", true
	}
	if len(hex) != 7 || hex[0] != '#' {
		return "", false
	}
	var channel [3]uint8
	for index := range channel {
		high, highOK := hexDigit(hex[1+2*index])
		low, lowOK := hexDigit(hex[2+2*index])
		if !highOK || !lowOK {
			return "", false
		}
		channel[index] = high<<4 | low
	}
	return strconv.Itoa(
		int(channel[0]),
	) + ";" + strconv.Itoa(
		int(channel[1]),
	) + ";" + strconv.Itoa(
		int(channel[2]),
	), true
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

func joinSpans(spans []span) string {
	var line strings.Builder
	for _, part := range spans {
		line.WriteString(part.paint.render(part.text))
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
	return fmt.Sprintf("#%02x%02x%02x", colour.R, colour.G, colour.B)
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
