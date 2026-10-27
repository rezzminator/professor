package call

import (
	"os"
	"strings"
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

func TestSelectorLineExactChatShGuards(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		capture string
		want    string
	}{
		{
			name:    "claude numbered selector",
			capture: "Question\n  ❯ 1. Allow once\n    2. Deny\n",
			want:    "❯ 1. Allow once",
		},
		{
			name: "codex hint selector",
			capture: "Run command?\n› 1. Yes\n  2. No\n" +
				"Press enter to confirm or esc to cancel\n",
			want: "› 1. Yes",
		},
		{
			name:    "codex stacked selector",
			capture: "› 1. Yes\n  2. No\n",
			want:    "› 1. Yes",
		},
		{
			name:    "codex numbered draft allowed",
			capture: "conversation\n› 1. do this next\n",
		},
		{
			name:    "bare claude composer allowed",
			capture: "conversation\n❯ \n",
		},
		{
			name:    "sent codex echo allowed",
			capture: "› 1. already sent\nresponse\n› \n",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := SelectorLine(test.capture); got != test.want {
				t.Fatalf("SelectorLine() = %q, want %q", got, test.want)
			}
		})
	}
}

// TestExitDialogOpenNeedsTheExitRowSelected is the one matcher both chat
// closers press Enter on: reload's worker and the kill-exit finisher. Only
// Claude Code's background-work confirmation with its Exit row selected
// reads open — a human who moved the marker to Stay, or a permission
// selector, gets no Enter.
func TestExitDialogOpenNeedsTheExitRowSelected(t *testing.T) {
	t.Parallel()
	const dialog = "   Background work is running\n" +
		"   The following will stop when you exit:\n" +
		"   shell · sleep 25 && echo done\n"
	tests := []struct {
		name    string
		capture string
		want    bool
	}{
		{name: "exit row selected", capture: dialog + "   ❯ 1. Exit and stop tasks\n     2. Stay\n", want: true},
		{name: "marker moved to stay", capture: dialog + "     1. Exit and stop tasks\n   ❯ 2. Stay\n"},
		{name: "permission selector", capture: "Do you want to proceed?\n ❯ 1. Yes\n   2. No\n"},
		{name: "idle composer", capture: "⏺ done\n────────\n❯ \n────────\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := ExitDialogOpen(test.capture); got != test.want {
				t.Fatalf("ExitDialogOpen(%q) = %v, want %v", test.capture, got, test.want)
			}
		})
	}
}

func TestIsBusyExactMarkers(t *testing.T) {
	t.Parallel()
	for _, capture := range []string{
		"esc to interrupt",
		"Working… (12s · ↓ 100 tokens)",
		"Working · 4s",
		"123 tokens",
	} {
		if !IsBusy(capture) {
			t.Errorf("IsBusy(%q) = false", capture)
		}
	}
	for _, capture := range []string{"✻ Worked for 12s", "❯", "idle"} {
		if IsBusy(capture) {
			t.Errorf("IsBusy(%q) = true", capture)
		}
	}
}

func TestHasDraftRecognizesUnicodeTextButNotFormatPlaceholders(t *testing.T) {
	t.Parallel()
	// Format controls are not visible draft text. The ASCII space between them
	// must not turn an otherwise invisible placeholder into a real draft.
	if hasDraft("❯ \u200b \u200b") {
		t.Fatal("ASCII space was counted as draft text")
	}
	if !hasDraft("❯ real draft") {
		t.Fatal("printable ASCII draft was missed")
	}
	for _, draft := range []string{"› 你好", "❯ مرحبا", "› 🫖"} {
		if !hasDraft(draft) {
			t.Errorf("visible Unicode draft %q was missed", draft)
		}
	}
}

func TestLastComposerLineUsesStructuralScreenOrder(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		capture string
		want    string
	}{
		{
			name:    "vitest marker cannot outrank later codex composer",
			capture: "└ ❯ suite.test.ts (3 tests | 1 failed)\n› ",
			want:    "› ",
		},
		{
			name:    "claude agent activity is not an editable composer",
			capture: "❯ \n❯ ● qa-cortex  Verifying results",
			want:    "❯ ",
		},
		{
			name: "the agents panel's selected main row is not the composer",
			capture: "──────── OPUS ─\n❯ \n────────\n  ↑/↓ to select · Enter to view\n❯ ⏺ main\n" +
				"  ◯ general-purpose (+3)  T1: port the pieces      12m 59s · ↓ 60.4k tokens",
			want: "❯ ",
		},
		{
			name:    "a selected agent row carrying a count is not the composer",
			capture: "❯ \n❯ ◯ general-purpose (+3)  T1: port the pieces",
			want:    "❯ ",
		},
		{
			name:    "a draft that opens with a bullet stays a draft",
			capture: "❯ \n❯ ⏺ mainly a typed note",
			want:    "❯ ⏺ mainly a typed note",
		},
		{
			name:    "mixed markers follow screen order",
			capture: "❯ old output\n\x1b[2m› \x1b[0m",
			want:    "\x1b[2m› \x1b[0m",
		},
		{
			name:    "carriage return is tolerated",
			capture: "conversation\r\n› draft\r",
			want:    "› draft\r",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := composerLastLine(test.capture); got != test.want {
				t.Fatalf("composerLastLine() = %q, want %q", got, test.want)
			}
		})
	}
}

// TestDeliveryProvenPlaceholderOnlyCountsForPasteTransport pins Task C's
// placeholder-proof rule directly at deliveryProven, the function the live
// transport ladder's final proof check calls: a capture holding only the
// composer's collapsed-paste placeholder (no trace of the message itself)
// is a positive delivery signal for the paste transport, and must NOT be
// silently widened into "any placeholder counts, however it got there" for
// the literal transport, which never asked tmux to bracket anything.
func TestDeliveryProvenPlaceholderOnlyCountsForPasteTransport(t *testing.T) {
	t.Parallel()
	before := "conversation\n❯ "
	after := "conversation\n[Pasted text #1 +12 lines]\n❯ "
	message := "the original long message body, never itself visible in the capture"
	if !deliveryProven(pfmengine.Claude, before, after, message, false, true) {
		t.Fatalf("collapsed-paste placeholder did not count as proof for the paste transport")
	}
	if deliveryProven(pfmengine.Claude, before, after, message, false, false) {
		t.Fatalf("collapsed-paste placeholder incorrectly counted as proof for the literal transport")
	}
}

func TestComposerEmptyPreservesMarkerCharactersInDraft(t *testing.T) {
	for _, test := range []struct{ marker, draft, tail string }{
		{"  ❯ ", "explain this symbol ›", ""},
		{"  ❯ ", "explain this symbol ❯", ""},
		{"  ❯ ", "open Settings › Privacy", ""},
		{"\t› ", "open Settings ❯ Privacy", ""},
		{"\x1b[32m  › \x1b[0m", "explain this symbol ›", ""},
		{"❯ ", "unfinished draft", "\n  ›"},
		{"❯ ", "unfinished draft", "\n  ❯"},
	} {
		t.Run(test.marker+test.draft, func(t *testing.T) {
			frame := "────────\n" + test.marker + test.draft + test.tail + "\n────────\n"
			empty, got, err := ComposerEmpty(frame, func() (string, error) { return frame, nil })
			if err != nil || empty || got != test.draft || ComposerDraftText(frame) != test.draft {
				t.Fatalf("ComposerEmpty = %v, %q, %v; want false, %q", empty, got, err, test.draft)
			}
		})
	}
}

func TestComposerBoxEmptyRejectsBoxDrawingDraftRows(t *testing.T) {
	for _, glyph := range []string{"─", "│"} {
		t.Run(glyph, func(t *testing.T) {
			frame := "────────\n❯ \n  " + glyph + "\n  unfinished draft\n────────\n"
			if ComposerBoxEmpty(frame) {
				t.Fatalf("box-drawing draft row %q was read as the closing rule", glyph)
			}
		})
	}
	if !ComposerBoxEmpty("❯ \n─\n") || !ComposerBoxEmpty("────────\n❯ \n────────\n") {
		t.Fatal("existing no-top-rule and empty-box frames must remain empty")
	}
}

func FuzzSelectorLine(f *testing.F) {
	for _, seed := range []string{
		"❯ 1. allow\n2. deny",
		"› 1. draft",
		"› 1. allow\n2. deny\nenter to confirm",
		"└ ❯ suite.test.ts\n› ",
		"❯ ● worker  Running\n› ",
		"› 你好",
		"\x00\xff❯",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(_ *testing.T, capture string) {
		_ = SelectorLine(capture)
	})
}

// Trust menu rows must never be stashed as drafts or accepted as empty input.
func TestUnnumberedTrustMenuIsNotAComposer(t *testing.T) {
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	body, err := os.ReadFile("../engine/testdata/claude-trust-unnumbered.txt")
	if err != nil {
		t.Fatal(err)
	}
	for _, selected := range []string{"No, exit", "Yes, I trust this folder"} {
		capture := string(body)
		if selected != "No, exit" {
			capture = strings.ReplaceAll(capture, "❯ No, exit", "  No, exit")
			capture = strings.ReplaceAll(capture, "   Yes, I trust this folder", " ❯ Yes, I trust this folder")
		}
		if got := SelectorLine(capture); got != "❯ "+selected {
			t.Errorf("selector=%q, want selected trust row", got)
		}
		if row := LastComposerLine(capture); row != "" || ComposerRowShown(capture) || hasDraft(row) {
			t.Errorf("trust menu read as editable input: row=%q", row)
		}
		empty, draft, err := ComposerEmpty(capture, func() (string, error) { return capture, nil })
		if empty || draft != "" || err != nil {
			t.Errorf("trust menu empty=%v draft=%q err=%v", empty, draft, err)
		}
		if draft := "❯ " + selected; !hasDraft(draft) || ComposerDraftText(draft) != selected {
			t.Errorf("an ordinary draft without dialog context was lost: %q", draft)
		}
		quotedChoice := capture + "\n────────\n❯ " + selected + "\n────────"
		if SelectorLine(quotedChoice) != "" || ComposerDraftText(quotedChoice) != selected {
			t.Error("quoted dialog overrode a bounded draft matching a trust choice")
		}
		quoted := capture + "\n────────\n❯ actual draft\n────────"
		if SelectorLine(quoted) != "" || ComposerDraftText(quoted) != "actual draft" {
			t.Error("quoted dialog outranked active composer")
		}
	}
}
