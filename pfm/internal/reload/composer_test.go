package reload

import (
	"strings"
	"testing"
)

func TestLastComposerLineFindsCodexCommandAbovePopupWhitespace(t *testing.T) {
	capture := "Codex\n› /exit\n" + strings.Repeat("\n", 30)
	if got := lastReloadComposerLine(capture); got != "› /exit" {
		t.Fatalf("lastComposerLine()=%q, want the visible Codex command", got)
	}
}

// TestComposerTextReadsAWrappedDraftAndStopsAtTheBoxRule pins the render taken
// from a live pane at the moment of a refusal: marker plus non-breaking space
// on line one, continuations indented beneath, the box rule closing the block,
// status rows below it that must stay OUT of the read.
func TestComposerTextReadsAWrappedDraftAndStopsAtTheBoxRule(t *testing.T) {
	rule := strings.Repeat("─", 40)
	capture := strings.Join([]string{
		"Chat",
		rule,
		"❯  Continue the reload-then reproduction. This prompt is",
		"  deliberately long enough to wrap across several rendered",
		"  composer lines. END OF REPRO PROMPT MARKER.",
		rule,
		"  bypass permissions on (shift+tab to cycle)",
	}, "\n")
	got := composerText(capture)
	if got != `❯  Continue the reload-then reproduction. This prompt is
  deliberately long enough to wrap across several rendered
  composer lines. END OF REPRO PROMPT MARKER.` {
		t.Fatalf("composerText lost the wrapped tail: %q", got)
	}
	if got != `❯  Continue the reload-then reproduction. This prompt is
  deliberately long enough to wrap across several rendered
  composer lines. END OF REPRO PROMPT MARKER.` {
		t.Fatalf("composerText read past the box rule into the status rows: %q", got)
	}
}

// The typed-/exit proof reads the composer's own draft, never the whole line.
// A pane that echoed the keystrokes before its TUI came up shows "/exit❯ " —
// the text sits BEFORE the marker, in nothing's draft — and reading the line
// whole turned that into a rendered /exit and a blind Enter.
func TestComposerExitProofReadsOnlyTheDraftAfterTheMarker(t *testing.T) {
	for _, row := range []struct {
		name    string
		capture string
		want    bool
	}{
		{"claude draft", "Claude\n❯ /exit", true},
		{"codex draft", "Codex\n› /exit", true},
		{"cooked-tty echo ahead of the marker", "Claude\n/exit❯ ", false},
		{"empty composer", "Claude\n❯ ", false},
		{"/exit appended to a restored draft", "Claude\n❯ an old draft/exit", false},
		{"stash marker on the status row below", "Claude\n❯ /exit\n────\n  Opus │ pfm › stashed", true},
		{"focused agent row below the box", "Claude\n❯ /exit\n❯ ⏺ main", true},
	} {
		if got := composerShowsExit(row.capture); got != row.want {
			t.Errorf("composerShowsExit(%q) = %t, want %t (%s)", row.capture, got, row.want, row.name)
		}
	}
}

// composerDrawn is the "can this pane take a keystroke at all" door: a blank
// pane is a chat that has not drawn its input box, never an idle one.
func TestComposerDrawnNeedsAnInputBoxOnThePane(t *testing.T) {
	for _, row := range []struct {
		capture string
		want    bool
	}{
		{"", false},
		{"Claude\nStarting…", false},
		{"Claude\n❯ ", true},
		{"Codex\n› ", true},
	} {
		if got := composerDrawn(row.capture); got != row.want {
			t.Errorf("composerDrawn(%q) = %t, want %t", row.capture, got, row.want)
		}
	}
}

// A menu stands in front of the composer, so only the last marker row can be
// one: a numbered prompt the chat echoes above its input box is transcript.
func TestSelectorOpenReadsTheRowInFrontOfTheComposer(t *testing.T) {
	rule := strings.Repeat("─", 40)
	for _, row := range []struct {
		name    string
		capture string
		want    bool
	}{
		{"menu with no composer", "Claude\n Proceed?\n ❯ 1. Yes\n   2. No", true},
		{"menu below a stale composer row", "Claude\n❯ \n❯ 1. choose", true},
		{"numbered prompt echoed above an empty composer", "❯ 1. keep the path\n  2. do it\n● Done\n❯ ", false},
		{
			"numbered prompt echoed above a boxed composer",
			"❯ 1. keep the path\n  2. do it\n● Done\n" + rule + "\n❯ \n" + rule + "\n  Opus · high",
			false,
		},
		{"empty composer", "Claude\n❯ ", false},
	} {
		if got := selectorOpen(row.capture); got != row.want {
			t.Errorf("%s: selectorOpen(%q) = %t, want %t", row.name, row.capture, got, row.want)
		}
	}
}
