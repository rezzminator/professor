package reload

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// This file is reload's READER of the pane: every question the choreography
// asks about what a chat currently shows — is its input box up, is our text in
// it, is a menu or the exit dialog in front of it — is answered here and
// nowhere else, so the exit path and the --then path can never disagree about
// what the same screen means.

// composerDrawn reports whether the chat has drawn its input box — the one
// proof that the pane can take a keystroke at all. deliverThen waits on it for
// the reborn pane; waitCallerIdle waits on it for the one being rebooted.
// (internal/spawn's composerReady answers a narrower, Codex-only question:
// that a BOOTING Codex TUI reached its idle composer and footer.)
func composerDrawn(capture string) bool {
	return lastReloadComposerLine(capture) != ""
}

// composerShowsExit reads the composer's own DRAFT, never the whole line: a
// pane that echoed the keystrokes before its TUI came up draws the marker
// AFTER them ("/exit❯ "), and a whole-line read took that for a rendered
// draft and pressed Enter on a chat that had received nothing.
func composerShowsExit(capture string) bool {
	draft := composerDraft(lastReloadComposerLine(capture))
	return strings.Contains(strings.Join(strings.Fields(draft), " "), "/exit")
}

// composerDraft is the text after a composer line's last ❯/› marker.
func composerDraft(line string) string {
	cut := -1
	for index, character := range line {
		if character == '❯' || character == '›' {
			cut = index + utf8.RuneLen(character)
		}
	}
	if cut < 0 {
		return ""
	}
	return line[cut:]
}

// exitDialogPattern is the selected row of Claude Code's background-work
// exit confirmation ("❯ 1. Exit and stop tasks"). The marker has to sit on
// the Exit row: a human who moved it to "Stay" gets that choice respected.
var exitDialogPattern = regexp.MustCompile(`❯[\s\v]*\d+\.[\s\v]*Exit`)

func exitDialogOpen(capture string) bool {
	return exitDialogPattern.MatchString(capture)
}

func selectorOpen(capture string) bool {
	selector := regexp.MustCompile(`❯[\s\v]*\d+\.`)
	for _, line := range strings.Split(capture, "\n") {
		if selector.MatchString(line) {
			return true
		}
	}
	return false
}

// composerText returns the ACTIVE composer's WHOLE draft: the marker line plus
// every wrapped continuation line beneath it, up to the box's closing rule.
//
// A one-line read was the bug this replaces. Claude and Codex both wrap a long
// draft inside the input box and print the ❯/› marker on the FIRST line only,
// so a check that scanned the marker line alone saw the draft's head and never
// its tail — and deliverThen proves delivery by the TAIL, which is the half
// that proves nothing was truncated in transit. Every steer worth sending after
// a reload is long enough to wrap, so the proof could never be satisfied and
// the follow-up sat in the composer waiting for a human finger.
//
// The block ends at the box's horizontal rule. When a render carries no closing
// rule the block runs to the end of the capture: the callers only ever ask
// whether their OWN text is present, so trailing status rows cost nothing,
// while a missing continuation line costs the whole delivery.
func composerText(capture string) string {
	lines := strings.Split(capture, "\n")
	start := -1
	for index := len(lines) - 1; index >= 0; index-- {
		if strings.Contains(lines[index], "❯") || strings.Contains(lines[index], "›") {
			start = index
			break
		}
	}
	if start < 0 {
		return ""
	}
	block := lines[start : start+1]
	for index := start + 1; index < len(lines); index++ {
		if composerBoxRule(lines[index]) {
			break
		}
		block = lines[start : index+1]
	}
	return strings.Join(block, "\n")
}

// composerBoxRule reports whether a captured line is one of the input box's
// horizontal rules — visible content that is nothing but box-drawing glyphs.
// Matching the CLASS (U+2500-U+257F) rather than one theme's glyph keeps a
// restyled border from silently reopening the wrap bug.
func composerBoxRule(line string) bool {
	drawn := false
	for _, character := range line {
		switch {
		case unicode.IsSpace(character):
		case character >= 0x2500 && character <= 0x257F:
			drawn = true
		default:
			return false
		}
	}
	return drawn
}

// squashSpace drops every space so a comparison survives the composer's line
// wrapping. Collapsing to single spaces survives a wrap at a word boundary and
// NOT one inside a word, and a token wider than the box — a long path or URL,
// the substance of most steers — is wrapped mid-word.
func squashSpace(value string) string {
	return strings.Join(strings.Fields(value), "")
}

func lastReloadComposerLine(capture string) string {
	lines := strings.Split(capture, "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		if strings.Contains(lines[index], "❯") || strings.Contains(lines[index], "›") {
			return lines[index]
		}
	}
	return ""
}
