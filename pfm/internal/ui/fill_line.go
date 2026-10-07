package ui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// fillLine fits value to exactly width columns: a short line is padded, a long
// one is cut and ends in "…" so the cut is visible. A cut that drops only
// padding loses nothing and is not marked.
func fillLine(value string, width int) string {
	width = maxInt(0, width)
	// Nearly every line a frame fits is already exactly its width, or short of
	// it: one cheap measure answers without the cut-and-measure passes below.
	if measured := cellWidth(value); measured <= width {
		if measured < width {
			value += strings.Repeat(" ", width-measured)
		}
		return value
	}
	tail := ""
	if strings.TrimSpace(ansi.Strip(ansi.TruncateLeft(value, width, ""))) != "" {
		tail = "…"
	}
	value = ansi.Truncate(value, width, tail)
	padding := width - cellWidth(value)
	if padding > 0 {
		value += strings.Repeat(" ", padding)
	}
	return value
}
