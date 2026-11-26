package call

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

// This file reads Claude Code's input box from a pane capture: the rule above
// the composer row, the rule closing it, and the draft rows between. Codex
// draws no box.

// ComposerBoxed reports whether the active composer row sits inside Claude
// Code's input box: a top rule right above the row (ComposerBoxTopRule, a
// named chat's carrying its name) and a closing rule below it.
// A shell's prompt line is drawn with neither: once a fullscreen Claude exits,
// the terminal's primary screen shows the line that started it
// ("❯ claude --resume …"), which LastComposerLine takes for a composer row.
func ComposerBoxed(capture string) bool {
	lines := strings.Split(capture, "\n")
	row := lastComposerRow(lines)
	if composerTopWidth(lines, row) == 0 {
		return false
	}
	_, bounded := ComposerBoxText(capture)
	return bounded
}

// ComposerBoxEmpty reports whether the rows beneath the active composer row,
// down to the input box's closing rule, hold no text: a draft led by a newline
// leaves the marker row empty and its text a row lower, where ComposerEmpty
// never looks. False with no closing rule — the box has no known end, so its
// rows cannot be proven empty — and with no composer row at all. Claude Code
// draws the box; Codex does not. A row there is empty only when it shows no
// visible character at all: hasDraft's marker-row allowances (a lone spinner
// frame, Codex's braille sparkle) are a typed "-" or text on these rows.
func ComposerBoxEmpty(capture string) bool {
	text, bounded := ComposerBoxText(capture)
	return bounded && strings.IndexFunc(ansi.Strip(text), func(character rune) bool {
		return unicode.IsGraphic(character) && !unicode.IsSpace(character)
	}) < 0
}

// ComposerBoxText returns the styled continuation rows beneath the active
// composer marker, and whether a closing rule established their boundary.
// A marker typed inside a bounded draft is content, never a new composer.
func ComposerBoxText(capture string) (string, bool) {
	lines := strings.Split(capture, "\n")
	row := lastComposerRow(lines)
	if row < 0 {
		return "", false
	}
	topWidth := composerTopWidth(lines, row)
	for end := row + 1; end < len(lines); end++ {
		clean := ansi.Strip(strings.TrimSuffix(lines[end], "\r"))
		if ComposerBoxRule(clean) && utf8.RuneCountInString(strings.TrimSpace(clean)) >= topWidth {
			return strings.Join(lines[row+1:end], "\n"), true
		}
	}
	return "", false
}

// composerTopWidth is the rune width of the input box's top rule drawn right
// above lines[row], 0 when that line is no top rule.
func composerTopWidth(lines []string, row int) int {
	if row <= 0 {
		return 0
	}
	top := strings.TrimSpace(ansi.Strip(lines[row-1]))
	if !ComposerBoxTopRule(top) {
		return 0
	}
	return utf8.RuneCountInString(top)
}

// ComposerBoxTopRule reports whether a captured line is the input box's top
// rule: a plain rule, or one with a label set into it ("──── chat-name ─"), as
// Claude Code draws a named chat's name. The label sits between a lead of at
// least two box glyphs and a tail of at least one, a space either side, so a
// table row ("│ cell │") above a shell prompt never reads as the box.
func ComposerBoxTopRule(line string) bool {
	if ComposerBoxRule(line) {
		return true
	}
	runes := []rune(strings.TrimSpace(line))
	lead := 0
	for lead < len(runes) && boxGlyph(runes[lead]) {
		lead++
	}
	tail := len(runes)
	for tail > lead && boxGlyph(runes[tail-1]) {
		tail--
	}
	label := runes[lead:tail]
	return lead >= 2 && tail < len(runes) && len(label) >= 3 &&
		label[0] == ' ' && label[len(label)-1] == ' ' && strings.TrimSpace(string(label)) != ""
}

func boxGlyph(character rune) bool {
	return character >= 0x2500 && character <= 0x257F
}

// ComposerBoxRule reports whether a captured line is one of the input box's
// horizontal rules — visible content that is nothing but box-drawing glyphs.
// Matching the CLASS (U+2500-U+257F) rather than one theme's glyph keeps a
// restyled border from silently reopening the wrap bug.
func ComposerBoxRule(line string) bool {
	drawn := false
	for _, character := range line {
		switch {
		case unicode.IsSpace(character):
		case boxGlyph(character):
			drawn = true
		default:
			return false
		}
	}
	return drawn
}
