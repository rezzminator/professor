package inject

import (
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// claudeBox is the input box and status rows every Claude capture ends with.
const claudeBox = "\n────────────────────────\n❯ \n────────────────────────\n  ◆ Opus 5.5 │ pfm\n  ⏵⏵ bypass permissions on · ← for agents"

// A Claude turn is busy only on shapes the engine draws — its spinner timer,
// a running spinner verb (…), elapsed time after an ellipsis, or "esc to
// interrupt". Transcript prose that happens to say "N tokens" or "· Ns", and
// the rows of background agents working behind an idle input box, are not a
// running turn: a reload held on them waited out its bound and refused, and
// a Claude Code 2.1.283 spinner past one minute ("(1m 50s · ↓ 9.3k tokens)")
// matched none of the old arms at all.
func TestClaudeBusyReadsTheSpinnerRowNotTranscriptProse(t *testing.T) {
	for _, row := range []struct {
		name    string
		capture string
		want    bool
	}{
		{"spinner past one minute", "⏺ Running the suite\n✻ Thundering… (1m 50s · ↓ 9.3k tokens)" + claudeBox, true},
		{"spinner before its timer", "✢ Tempering…" + claudeBox, true},
		{"older spinner with its hint", "✻ Thinking… (12s · ↓ 1.2k tokens · esc to interrupt)" + claudeBox, true},
		{"bare interrupt footer", "working on it\n  esc to interrupt\n", true},
		{"nested spawning spinner", "● Agent \"X\" finished · 46s\n  Spawning … · 51s" + claudeBox, true},
		{
			"final answer quoting token counts",
			"⏺ The proof passes.\n  The Haiku agent wrote 24,768 tokens of 5-minute cache.\n" +
				"  | agent cache writes | 5m = 24,768 tokens |\n✻ Worked for 38s" + claudeBox,
			false,
		},
		{"final answer quoting a duration", "⏺ Done.\n  the agent finished · 46s after launch" + claudeBox, false},
		{
			"background agent working behind an idle box",
			"✻ Waiting for 1 background agent to finish" + claudeBox +
				"\n  ◯ general-purpose  T1: port the pieces      45s · ↓ 900 tokens",
			false,
		},
		{"finished spinner", "✻ Worked for 12s" + claudeBox, false},
		{"indented prose with an ellipsis", "⏺ Notes\n  · still thinking about it…" + claudeBox, false},
	} {
		if got := IsBusyFor(pfmengine.Claude, row.capture); got != row.want {
			t.Errorf("IsBusyFor(Claude, %s) = %t, want %t", row.name, got, row.want)
		}
		if got := IsFooterBusy(pfmengine.Claude, row.capture); got != row.want {
			t.Errorf("IsFooterBusy(Claude, %s) = %t, want %t", row.name, got, row.want)
		}
	}
}
