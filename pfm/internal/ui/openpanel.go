package ui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// An open panel is the deck's frame with no vertical edge to the right of its
// content. A chat row carries emoji (account medals, ⚡, the model's effort),
// and a terminal that lays a line out by glyph advance rather than by cell
// pushes everything after an emoji a pixel or two off the grid: a right border
// drawn after the emoji then zigzags down the screen, row by row, between the
// rows that carry an emoji and the ones that do not. So the frame keeps only
// the edges that come BEFORE any emoji — the list's left rail, the top rule and
// the bottom rule — and lets the right end of every panel run open.

// openPanelSpec describes one open panel.
type openPanelSpec struct {
	title string
	// lead is the blank gutter before the panel, in cells: the dossier sits
	// that far from the list's last column.
	lead int
	// rail draws the left edge on every content line.
	rail bool
	// bottom is the panel's own bottom edge, exactly width cells; empty draws
	// a plain rule.
	bottom string
}

// openPanel frames lines (each already fitted to spec's content width) as one
// panel exactly width cells wide.
func openPanel(spec openPanelSpec, lines []string, width int) string {
	palette := configuredPalette
	edge := tone{fg: palette.Border}
	lead := strings.Repeat(" ", spec.lead)
	body := max(1, width-spec.lead)
	content := body
	if spec.rail {
		content--
	}
	top := "╭" + ansi.Truncate("─"+spec.title, body-1, "…")
	top += strings.Repeat("─", max(0, body-ansi.StringWidth(top)))
	bottom := spec.bottom
	if bottom == "" {
		bottom = lead + edge.render("╰"+strings.Repeat("─", body-1))
	}
	framed := make([]string, 0, len(lines)+2)
	framed = append(framed, lead+edge.render(top))
	rail := edge.render("│")
	for _, line := range lines {
		if spec.rail {
			framed = append(framed, lead+rail+fillLine(line, content))
			continue
		}
		framed = append(framed, lead+fillLine(line, content))
	}
	framed = append(framed, bottom)
	return strings.Join(framed, "\n")
}
