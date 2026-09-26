package reload

import (
	"strings"
	"testing"
)

func TestLastComposerLineFindsCodexCommandAbovePopupWhitespace(t *testing.T) {
	capture := "Codex\n› /exit\n" + strings.Repeat("\n", 30)
	if got := lastReloadComposerLine(capture); !strings.Contains(got, "/exit") {
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
	if !strings.Contains(got, "END OF REPRO PROMPT MARKER.") {
		t.Fatalf("composerText lost the wrapped tail: %q", got)
	}
	if strings.Contains(got, "bypass permissions") {
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
