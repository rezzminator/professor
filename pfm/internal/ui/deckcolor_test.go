package ui

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestBlendHexEndpointsAndMidpoint(t *testing.T) {
	cases := []struct {
		from, to string
		at       float64
		want     string
	}{
		{"#000000", "#ffffff", 0, "#000000"},
		{"#000000", "#ffffff", 1, "#ffffff"},
		{"#000000", "#ffffff", 0.5, "#7f7f7f"},
		{"#102030", "#102030", 0.7, "#102030"},
		{"#000000", "#ffffff", 2, "#ffffff"},
		{"#000000", "#ffffff", -1, "#000000"},
	}
	for _, test := range cases {
		if got := blendHex(test.from, test.to, test.at); got != test.want {
			t.Errorf("blendHex(%s, %s, %v) = %s, want %s", test.from, test.to, test.at, got, test.want)
		}
	}
}

func TestPadCellsFitExactWidth(t *testing.T) {
	values := []string{
		"",
		"a",
		"short",
		"exactly ten",
		"a considerably longer value than any column",
		"界面界面",
		"🚀 fleet",
	}
	for width := 0; width <= 24; width++ {
		for _, value := range values {
			left := padRightCells(value, width)
			right := padLeftCells(value, width)
			if got := lipgloss.Width(left); got != width {
				t.Errorf("padRightCells(%q, %d) is %d cells wide: %q", value, width, got, left)
			}
			if got := lipgloss.Width(right); got != width {
				t.Errorf("padLeftCells(%q, %d) is %d cells wide: %q", value, width, got, right)
			}
		}
	}
	if got := padRightCells("abcdef", 4); got != "abc…" {
		t.Errorf("a long value must end in an ellipsis, got %q", got)
	}
	if got := padLeftCells("7", 3); got != "  7" {
		t.Errorf("padLeftCells right-aligns, got %q", got)
	}
}

func TestToneRendersNothingForEmptyText(t *testing.T) {
	if got := (tone{fg: "#ffffff", bg: "#000000", bold: true}).render(""); got != "" {
		t.Fatalf("an empty run must leave no escape sequence behind, got %q", got)
	}
}

func TestToneCarriesItsColoursAndWeight(t *testing.T) {
	rendered := tone{fg: "#102030", bg: "#405060", bold: true, italic: true}.render("x")
	for _, want := range []string{"38;2;16;32;48", "48;2;64;80;96"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("render = %q, want it to carry %s", rendered, want)
		}
	}
	if got := ansi.Strip(rendered); got != "x" {
		t.Errorf("stripped render = %q, want x", got)
	}
	over := tone{fg: "#102030"}.onBackground("#405060")
	if over.bg != "#405060" || over.fg != "#102030" {
		t.Errorf("onBackground = %#v, want the same foreground over the new background", over)
	}
	if kept := (tone{bg: "#111111"}).onBackground(""); kept.bg != "#111111" {
		t.Errorf("an empty background must leave the tone's own, got %#v", kept)
	}
}

func TestSpansWidthMatchesJoinedWidth(t *testing.T) {
	spans := []span{
		{text: "ab", paint: tone{fg: "#ff0000"}},
		{text: "界", paint: tone{bg: "#00ff00"}},
		{text: " "},
	}
	if got, want := spansWidth(spans), lipgloss.Width(joinSpans(spans)); got != want {
		t.Fatalf("spansWidth = %d, joined width = %d", got, want)
	}
}

func TestChipOfPadsOneCellEachSide(t *testing.T) {
	chip := chipOf("LIVE", tone{fg: "#000000", bg: "#ffffff"})
	if got := ansi.Strip(chip); got != " LIVE " {
		t.Fatalf("chip = %q, want one cell of air each side", got)
	}
}

// The direct SGR writer must say exactly what lipgloss would: the lane scripts
// and every golden read these bytes, and a terminal reads them the same way.
func TestToneRenderMatchesLipglossForEveryAttributeCombination(t *testing.T) {
	colours := []string{"", "#0b1020", "#FFAA00", "#00ff7f"}
	for _, fg := range colours {
		for _, bg := range colours {
			for flags := 0; flags < 8; flags++ {
				paint := tone{fg: fg, bg: bg, bold: flags&1 != 0, dim: flags&2 != 0, italic: flags&4 != 0}
				if got, want := paint.render("run"), paint.style().Render("run"); got != want {
					t.Errorf("%+v: direct %q, lipgloss %q", paint, got, want)
				}
			}
		}
	}
}

func TestToneRenderFallsBackToLipglossForAColourItCannotSpell(t *testing.T) {
	for _, bad := range []string{"red", "#12345", "#12345g", "123456", "#1234567"} {
		paint := tone{fg: bad, bold: true}
		if got, want := paint.render("x"), paint.style().Render("x"); got != want {
			t.Errorf("fg %q: render %q, lipgloss %q", bad, got, want)
		}
	}
	if got := (tone{}).render("plain"); got != "plain" {
		t.Errorf("a tone with nothing to say writes the bare text, got %q", got)
	}
}

// hexOfRGB spells a colour byte for byte as the palette's "#%02x%02x%02x" does.
func TestHexOfRGBSpellsWhatThePaletteFormatWould(t *testing.T) {
	for value := range 256 {
		colour := RGB{uint8(value), uint8(255 - value), uint8(value * 7)}
		if got, want := hexOfRGB(colour), fmt.Sprintf("#%02x%02x%02x", colour.R, colour.G, colour.B); got != want {
			t.Fatalf("hexOfRGB(%v) = %q, want %q", colour, got, want)
		}
	}
}
