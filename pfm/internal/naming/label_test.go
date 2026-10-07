package naming

import (
	"strings"
	"testing"
)

func TestLabelKilled(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		label string
		want  bool
	}{
		{"legacy exact prefix", "_HIDE", true},
		{"legacy prefix with a payload", "_HIDE headless worker 3", true},
		{"new kill prefix", "_KILL headless worker 3", true},
		{"lower case", "_hide worker", true},
		{"mixed case", "_HiDe worker", true},
		{"no underscore", "HIDE worker", false},
		{"prefix in the middle", "worker _HIDE", false},
		{"shorter than the prefix", "_HID", false},
		{"empty", "", false},
		{"underscore only", "_", false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := LabelKilled(testCase.label); got != testCase.want {
				t.Fatalf("LabelKilled(%q) = %t, want %t",
					testCase.label, got, testCase.want)
			}
		})
	}
}

func TestBookmarkLabelAcceptsConfiguredAccountEmoji(t *testing.T) {
	line := "🔖 🟣 Custom Seat │ neutral-project"
	if got := BookmarkLabelFor(line, []string{"🟣"}); got != "🟣 Custom Seat" {
		t.Fatalf("BookmarkLabelFor() = %q, want configured emoji label", got)
	}
}

// TestBookmarkLabelIgnoresADeliveredFooter is the regression for footer
// label poisoning, caught live: pane %0 of a chat named W5_TESTER had, in its
// visible capture, a message footer reading "🔖 W5_BUILDER" — the SENDER's
// label, written into the RECIPIENT's pane by inject's own signature. Because
// the LAST 🔖 line wins, that made a chat's resolved identity whatever the
// most recent message it received claimed its sender was called.
//
// The only thing that stopped it in the wild was ContainsMedalFor skipping
// the line, and that is a coincidence: the medal check exists to tell a
// statusline apart from prose, not to defend against a foreign identity, and
// any footer rendering beside a medal or a configured account emoji defeats
// it. This pins the actual defence — a line carrying the fleet's own
// delivered-footer marker is not the pane's own identity, medal or no medal.
func TestBookmarkLabelIgnoresADeliveredFooter(t *testing.T) {
	capture := strings.Join([]string{
		"🥇 │ 🔖 W5_TESTER │ ~/work",
		"USER: do the thing  — sid abcdef12 · " +
			DeliveredFooterMarker + "W5_BUILDER <message> 🔖 W5_BUILDER 🥇",
		"❯ ",
	}, "\n")
	if label := BookmarkLabel(capture); label != "W5_TESTER" {
		t.Fatalf("BookmarkLabel = %q, want the pane's OWN label; a delivered footer overwrote it", label)
	}
}

// TestBookmarkLabelIgnoresADeliveredFooterOnACodexPane is the harder half. A
// codex chat renders no 🔖 statusline at all, so every 🔖 on its screen came
// from somebody else. With no own-label line to win last, a footer is the
// only candidate — and the honest answer is "no label here", which lets the
// caller fall through to the window name rather than adopting a stranger's
// identity.
func TestBookmarkLabelIgnoresADeliveredFooterOnACodexPane(t *testing.T) {
	capture := strings.Join([]string{
		"› codex conversation",
		"USER: ship it  — sid abcdef12 · " +
			DeliveredFooterMarker + "W:ORCHESTRATOR <message> 🔖 W:ORCHESTRATOR 🥈",
		"› ",
	}, "\n")
	if label := BookmarkLabel(capture); label != "" {
		t.Fatalf("BookmarkLabel = %q, want \"\": a codex pane has no 🔖 of its own", label)
	}
}

func TestLaunchName(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		label string
		want  string
	}{
		{"plain", "Fix login", "Fix login"},
		{"empty", "", ""},
		{"blank", " \t\r\n ", ""},
		{"unnamed sentinel", Unnamed, ""},
		{"whitespace runs collapse", "fix\n  login\t\tnow\r\n", "fix login now"},
		{"control runes drop", "fi\x00x\x1b lo\x7fgin", "fix login"},
		{"control-only", "\x00\x1b", ""},
		{"trimmed", "  padded  ", "padded"},
		{"kill marker verbatim", "_KILL worker 3", "_KILL worker 3"},
		{"legacy kill marker verbatim", "_HIDE worker 3", "_HIDE worker 3"},
		{"clipped at the cap", strings.Repeat("é", LaunchNameRunes+50), strings.Repeat("é", LaunchNameRunes)},
		{
			"clip leaves no trailing space",
			strings.Repeat("a", LaunchNameRunes-1) + " tail",
			strings.Repeat("a", LaunchNameRunes-1),
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := LaunchName(testCase.label); got != testCase.want {
				t.Fatalf("LaunchName(%q) = %q, want %q", testCase.label, got, testCase.want)
			}
		})
	}
}
