package ui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// fillLine fits value to exactly width columns: a short line is padded, a long
// one is cut and ends in "…" so the cut is visible. A cut that drops only
// padding loses nothing and is not marked.
func fillLine(value string, width int) string {
	width = maxInt(0, width)
	tail := ""
	if strings.TrimSpace(ansi.Strip(ansi.TruncateLeft(value, width, ""))) != "" {
		tail = "…"
	}
	value = ansi.Truncate(value, width, tail)
	padding := width - lipgloss.Width(value)
	if padding > 0 {
		value += strings.Repeat(" ", padding)
	}
	return value
}
