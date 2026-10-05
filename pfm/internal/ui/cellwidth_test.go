package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// cellWidth is a faster ansi.StringWidth for frame lines; it must agree with it
// on everything a frame can hold.
func TestCellWidthAgreesWithTheGraphemeMeasurer(t *testing.T) {
	corpus := []string{
		"", "plain ascii", "\x1b[1;38;2;255;158;100mcoloured\x1b[m tail", "│ ● name ▰▰▱▱ 1s",
		"🥇 ⚡ ⇄ ⬢", "界面 needle 列对齐", "é combining", "1️⃣ keycap", "🏎️ vs16", "👩‍👩‍👧 zwj family",
		"tab\there", "bell\x07here", "\x1b]8;;https://example.invalid\x1b\\link\x1b]8;;\x1b\\ text",
		"\x1b[38;2;1;2;3", "trailing \x1b", "\x1bM single escape", "❤️ heart", "🇳🇱 flag", "ａｂｃ fullwidth",
		strings.Repeat("▰", 8) + strings.Repeat("▱", 8), "◖▶ open◗", "\u200bzero width",
	}
	for _, value := range corpus {
		if got, want := cellWidth(value), ansi.StringWidth(value); got != want {
			t.Errorf("cellWidth(%q) = %d, ansi.StringWidth = %d", value, got, want)
		}
	}
}

// Every rune the fast path counts as one cell is checked against the measurer,
// so a table entry that is wrong cannot hide behind the corpus above.
func TestSingleCellRunesAreOneCellToTheMeasurer(t *testing.T) {
	for r := rune(0x80); r <= 0x2600; r++ {
		if !singleCell(r) {
			continue
		}
		if got := ansi.StringWidth(string(r)); got != 1 {
			t.Errorf("singleCell(%U) is true but ansi.StringWidth = %d", r, got)
		}
	}
}

func TestCellWidthAgreesOnEveryLineOfRealFrames(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 30}, {160, 40}} {
		model := selectChat(t, deckModel(size[0], size[1]), "P:BUILDER")
		for _, line := range strings.Split(model.View().Content, "\n") {
			if got, want := cellWidth(line), ansi.StringWidth(line); got != want {
				t.Fatalf(
					"%dx%d: cellWidth = %d, ansi.StringWidth = %d for %q",
					size[0],
					size[1],
					got,
					want,
					ansi.Strip(line),
				)
			}
		}
	}
}

func TestFillLineKeepsItsContractOnTheFastPath(t *testing.T) {
	for _, test := range []struct {
		value string
		width int
		want  string
	}{
		{"abc", 3, "abc"},
		{"abc", 6, "abc   "},
		{"abcdef", 4, "abc…"},
		{"abc   ", 3, "abc"},
		{"\x1b[1mabc\x1b[m", 5, "\x1b[1mabc\x1b[m  "},
		{"", 2, "  "},
		{"x", 0, ""},
	} {
		if got := fillLine(test.value, test.width); got != test.want {
			t.Errorf("fillLine(%q, %d) = %q, want %q", test.value, test.width, got, test.want)
		}
	}
}

func BenchmarkCellWidth(b *testing.B) {
	line := "\x1b[38;2;100;116;139m│\x1b[m\x1b[1;38;2;255;158;100m ◉\x1b[m P:BUILDER-2 \x1b[38;2;148;163;184m🥉\x1b[m   214p  2.3M ▰▰▰▰▱▱▱▱   1s"
	b.Run("fast", func(b *testing.B) {
		for range b.N {
			_ = cellWidth(line)
		}
	})
	b.Run("ansi", func(b *testing.B) {
		for range b.N {
			_ = ansi.StringWidth(line)
		}
	})
}

// glyphCells must count one character exactly as the grapheme measurer does.
func TestGlyphCellsAgreesWithTheGraphemeMeasurer(t *testing.T) {
	for _, glyph := range []string{"a", " ", "~", "\t", "\x7f", "●", "↻", "⚙", "◐", "◆", "─", "é", "🥈", "界", "\u0301", "\u200d"} {
		if got, want := glyphCells(glyph), ansi.StringWidth(glyph); got != want {
			t.Errorf("glyphCells(%q) = %d, ansi.StringWidth = %d", glyph, got, want)
		}
	}
}
