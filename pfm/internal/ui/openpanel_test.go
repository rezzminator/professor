package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestOpenPanelKeepsNoVerticalEdgeOnTheRight(t *testing.T) {
	lines := []string{"alpha", "beta 🥉"}
	for _, spec := range []openPanelSpec{
		{title: " fleet 2 ", rail: true},
		{title: " dossier ", lead: 2},
	} {
		const width = 30
		framed := strings.Split(openPanel(spec, lines, width), "\n")
		if len(framed) != len(lines)+2 {
			t.Fatalf("%q: %d lines, want the content plus a top and a bottom rule", spec.title, len(framed))
		}
		for index, line := range framed {
			plain := ansi.Strip(line)
			if got := ansi.StringWidth(plain); got != width {
				t.Errorf("%q line %d is %d cells, want %d: %q", spec.title, index, got, width, plain)
			}
			if strings.ContainsAny(plain, "╮╯") || strings.HasSuffix(strings.TrimRight(plain, " "), "│") {
				t.Errorf("%q line %d has a right edge that an emoji would push off the grid: %q",
					spec.title, index, plain)
			}
		}
		if !strings.Contains(ansi.Strip(framed[0]), strings.TrimSpace(spec.title)) {
			t.Errorf("%q: the top rule lost its title: %q", spec.title, ansi.Strip(framed[0]))
		}
		if hasRail := strings.HasPrefix(ansi.Strip(framed[1]), "│"); hasRail != spec.rail {
			t.Errorf("%q: left rail = %v, want %v", spec.title, hasRail, spec.rail)
		}
	}
}
