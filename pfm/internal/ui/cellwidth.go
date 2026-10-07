package ui

import (
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

// cellWidth is ansi.StringWidth for a line a frame has already built: escape
// sequences are skipped and printable ASCII counted on the spot, and only the
// runs of other characters — box rules, gauge glyphs, emoji — go to the
// grapheme-aware measurer. A frame measures hundreds of lines a tick, nearly
// all of them ASCII under their colour codes, and the grapheme walk was the
// largest single cost of drawing one.
//
// A run that opens with a character that joins to what precedes it (a variation
// selector, a joiner, a combining mark, the keycap) is not safe to measure
// apart from its ASCII neighbour, so the whole string is measured the slow way.
func cellWidth(value string) int {
	width := 0
	for index := 0; index < len(value); {
		char := value[index]
		switch {
		case char == 0x1b:
			index = skipEscape(value, index)
		case char < 0x80:
			if char >= 0x20 && char != 0x7f {
				width++
			}
			index++
		default:
			end := index
			for end < len(value) && value[end] >= 0x80 {
				end++
			}
			run := value[index:end]
			if first, _ := utf8.DecodeRuneInString(run); joinsBackwards(first) && index > 0 {
				return ansi.StringWidth(value)
			}
			width += runWidth(run)
			index = end
		}
	}
	return width
}

// joinsBackwards reports a rune that attaches to the grapheme before it.
func joinsBackwards(r rune) bool {
	return r == 0xFE0F || r == 0xFE0E || r == 0x200D || r == 0x20E3 || (r >= 0x300 && r <= 0x36F)
}

// skipEscape returns the index just past the escape sequence that starts at
// start: a CSI ends at its final byte, an OSC at BEL or ST, any other escape
// after its one following character. An unterminated sequence runs to the end.
func skipEscape(value string, start int) int {
	index := start + 1
	if index >= len(value) {
		return index
	}
	switch value[index] {
	case '[':
		for index++; index < len(value); index++ {
			if value[index] >= 0x40 && value[index] <= 0x7e {
				return index + 1
			}
		}
	case ']':
		for index++; index < len(value); index++ {
			if value[index] == 0x07 {
				return index + 1
			}
			if value[index] == 0x1b && index+1 < len(value) && value[index+1] == '\\' {
				return index + 2
			}
		}
	default:
		return index + 1
	}
	return len(value)
}

// runWidth measures a run of non-ASCII text. The characters a frame is drawn
// with — box rules, gauge and ruler shapes, arrows, typographic marks — are one
// cell each, so a run made only of them is counted rune by rune; any other run
// (emoji, CJK, combining marks) goes to the grapheme-aware measurer.
func runWidth(run string) int {
	cells := 0
	for _, r := range run {
		if !singleCell(r) {
			return ansi.StringWidth(run)
		}
		cells++
	}
	return cells
}

// singleCell reports a rune that every width model draws in exactly one cell.
func singleCell(r rune) bool {
	switch {
	case r >= 0xA1 && r <= 0xFF && r != 0xAD: // Latin-1 letters and marks, not the soft hyphen
		return true
	case r >= 0x2010 && r <= 0x2027: // dashes, quotes, bullet, ellipsis (not the joiners at 200B-200F)
		return true
	case r >= 0x2190 && r <= 0x21FF: // arrows
		return true
	case r >= 0x2500 && r <= 0x257F: // box drawing
		return true
	case r >= 0x25A0 && r <= 0x25FA: // geometric shapes up to the wide squares
		return true
	}
	return false
}

// glyphCells is the width of one character's text, as ansi.StringWidth counts
// it: printable ASCII and the single-cell runes on the spot, anything else
// (an emoji, a control) through the measurer.
func glyphCells(text string) int {
	if len(text) == 1 && text[0] >= 0x20 && text[0] < 0x7f {
		return 1
	}
	if r, _ := utf8.DecodeRuneInString(text); r >= 0x80 && singleCell(r) {
		return 1
	}
	return ansi.StringWidth(text)
}
