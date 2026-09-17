package inject

import "testing"

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
			if got := lastComposerLine(test.capture); got != test.want {
				t.Fatalf("lastComposerLine() = %q, want %q", got, test.want)
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
	if !deliveryProven(before, after, message, false, true) {
		t.Fatalf("collapsed-paste placeholder did not count as proof for the paste transport")
	}
	if deliveryProven(before, after, message, false, false) {
		t.Fatalf("collapsed-paste placeholder incorrectly counted as proof for the literal transport")
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
