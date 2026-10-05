package ui

import (
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/theme"
)

// A live OpenCode seat renders as a live chat (●) in OpenCode's own colour and
// badge. Without an arm of its own it falls to the default "·" marker, which
// is the picker saying "this is not a running chat" about one that is.
func TestLiveOpenCodeRendersAsALiveOpenCodeRow(t *testing.T) {
	if got := rowMarker(compose.LiveOpenCode); got != "●" {
		t.Fatalf("rowMarker(LiveOpenCode) = %q, want ●", got)
	}
	row := compose.Row{Kind: compose.LiveOpenCode, Name: "live one"}
	model := Model{}
	if got := model.rowBadges(row); got != "◇" {
		t.Fatalf("rowBadges(LiveOpenCode) = %q, want the OpenCode badge ◇", got)
	}
	configureStyles(theme.Load("default"))
	rendered := model.renderGroupedRow(row, false, 120, false)
	engineColour, _, found := strings.Cut(tone{fg: engineHexOf(row)}.render("x"), "x")
	if !found || engineColour == "" {
		t.Fatal("the OpenCode engine colour renders no escape prefix to assert on")
	}
	if !strings.Contains(rendered, engineColour) {
		t.Fatalf("renderGroupedRow(LiveOpenCode) = %q, want OpenCode's own engine colour %q", rendered, engineColour)
	}
}

// The live-engine counter has a case per engine and NO default, so a kind
// without an arm is counted as nothing at all — the picker's engine tally
// silently omitting a running chat.
func TestLiveEngineCountsCountALiveOpenCodeSeat(t *testing.T) {
	counts := liveEngineCounts([]compose.Row{
		{Kind: compose.LiveOpenCode},
		{Kind: compose.LiveClaude},
		{Kind: compose.LiveCodex},
	})
	if counts[pfmengine.OpenCode] != 1 {
		t.Fatalf("counts = %v, want one OpenCode seat", counts)
	}
	if counts[pfmengine.Claude] != 1 || counts[pfmengine.Codex] != 1 {
		t.Fatalf("counts = %v, want the other two engines unchanged", counts)
	}
}
