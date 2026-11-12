package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/rezzminator/professor/pfm/internal/compose"
	"github.com/rezzminator/professor/pfm/internal/theme"
)

func gaugeText(row compose.Row, nowNS int64) string {
	return ansi.Strip(joinSpans(workGauge(row, nowNS, "#ff9e64", "#64748b", "")))
}

func TestWorkGaugeIsAlwaysExactlyItsWidth(t *testing.T) {
	for agents := 0; agents <= 9; agents++ {
		for _, state := range []compose.Row{{Working: true}, {}, {Working: true, Blocked: true}, {Blocked: true}} {
			row := compose.Row{
				Kind: compose.LiveClaude, Working: state.Working, Blocked: state.Blocked, AgentsWorking: agents,
			}
			for frame := int64(0); frame < 30; frame++ {
				if got := ansi.StringWidth(gaugeText(row, frame*workFrameNS)); got != deckWorkW {
					t.Fatalf("%+v frame %d: %d cells, want %d", row, frame, got, deckWorkW)
				}
			}
		}
	}
}

func TestWorkGaugeDrawsNothingWhileNothingMoves(t *testing.T) {
	for _, row := range []compose.Row{
		{Kind: compose.LiveClaude},
		{Kind: compose.ResumeClaude, Working: true, AgentsWorking: 3},
		{Kind: compose.NewClaude, Working: true},
		{Kind: compose.ResumeClaude, Blocked: true},
	} {
		if got := gaugeText(row, 0); strings.TrimSpace(got) != "" {
			t.Errorf("%+v draws %q, want blank", row, got)
		}
	}
}

func TestWorkGaugeCountsAgentsAsArcsAndMarksOverflow(t *testing.T) {
	arcs := func(text string) int {
		return strings.Count(text, "◐") + strings.Count(text, "◓") + strings.Count(text, "◑") + strings.Count(text, "◒")
	}
	for agents, want := range map[int]int{1: 1, 2: 2, 3: 3, 4: 4, 5: 4, 9: 4} {
		text := gaugeText(compose.Row{Kind: compose.LiveClaude, Working: true, AgentsWorking: agents}, 3*workFrameNS)
		if arcs(text) != want {
			t.Errorf("%d agents draw %d arcs in %q, want %d", agents, arcs(text), text, want)
		}
		if overflow := strings.Contains(text, "+"); overflow != (agents > workMaxArcs) {
			t.Errorf("%d agents: overflow mark = %v in %q", agents, overflow, text)
		}
	}
}

func TestWorkGaugeCoreSaysWhoIsWorking(t *testing.T) {
	both := gaugeText(compose.Row{Kind: compose.LiveClaude, Working: true, AgentsWorking: 1}, 0)
	if !strings.HasPrefix(both, "◉") && !strings.HasPrefix(both, "●") {
		t.Errorf("a chat that is itself mid-turn has a solid core: %q", both)
	}
	orchestrating := gaugeText(compose.Row{Kind: compose.LiveClaude, AgentsWorking: 2}, 0)
	if !strings.HasPrefix(orchestrating, "◌") {
		t.Errorf("a chat that only waits on its agents has a hollow core: %q", orchestrating)
	}
	// Herdr reads it blocked while its transcript still reads mid-turn: the
	// owner's answer holds it, so the core is the blocked mark on every frame.
	configureStyles(theme.Load("default"))
	t.Cleanup(func() { configureStyles(theme.Load("default")) })
	warn := theme.Load("default").Warn
	if warn == "" {
		t.Fatal("the default theme names no blocked colour (Palette.Warn)")
	}
	blocked := compose.Row{Kind: compose.LiveClaude, Working: true, Blocked: true}
	for frame := int64(0); frame < 8; frame++ {
		spans := workGauge(blocked, frame*workFrameNS, "#ff9e64", "#64748b", "")
		if got := ansi.Strip(joinSpans(spans)); got != "⚠     " || spans[0].paint.fg != warn {
			t.Fatalf("a blocked chat at frame %d draws %q in %q, want %q in the blocked colour %q",
				frame, got, spans[0].paint.fg, "⚠     ", warn)
		}
	}
}

func TestWorkGaugeArcsTurnAndTrailEachOther(t *testing.T) {
	row := compose.Row{Kind: compose.LiveClaude, Working: true, AgentsWorking: 4}
	seen := map[string]bool{}
	for frame := int64(0); frame < 8; frame++ {
		text := gaugeText(row, frame*workFrameNS)
		seen[text] = true
		arcs := []rune(text)[1:5]
		if arcs[0] == arcs[1] && arcs[1] == arcs[2] && arcs[2] == arcs[3] {
			t.Fatalf("frame %d: every arc is the same glyph, so the wave is lost: %q", frame, text)
		}
	}
	if len(seen) < 4 {
		t.Errorf("the gauge repeats after %d distinct frames, want it to keep turning", len(seen))
	}
	// Moving a quarter turn later puts each arc where its neighbour was.
	a, b := []rune(gaugeText(row, 0))[1], []rune(gaugeText(row, 2*workFrameNS))[1]
	if a == b {
		t.Errorf("an arc did not advance in two frames: %q → %q", string(a), string(b))
	}
}

func TestWorkGaugeUsesOnlyGlyphsTheTerminalDrawsOnItsOwn(t *testing.T) {
	for _, glyph := range append([]string{"◉", "●", "◌", "⚠", "+"}, workArcs[:]...) {
		for _, r := range glyph {
			if r >= 0x2580 && r <= 0x259F || r >= 0x2800 && r <= 0x28FF {
				t.Errorf("%q is a Block Element or Braille glyph, which corrupts VS Code's glyph atlas", glyph)
			}
		}
	}
}

func TestWorkSummaryNamesWhoIsWorking(t *testing.T) {
	for _, test := range []struct {
		row  compose.Row
		want string
	}{
		{compose.Row{Working: true}, "mid-turn"},
		{compose.Row{Working: true, AgentsWorking: 1}, "mid-turn · 1 agent"},
		{compose.Row{Working: true, AgentsWorking: 3}, "mid-turn · 3 agents"},
		{compose.Row{AgentsWorking: 2}, "2 agents working"},
		{compose.Row{Working: true, Blocked: true}, "waits on your answer"},
		{compose.Row{Working: true, Blocked: true, AgentsWorking: 2}, "waits on your answer · 2 agents working"},
		{compose.Row{}, ""},
	} {
		if got := workSummary(test.row); got != test.want {
			t.Errorf("workSummary(%+v) = %q, want %q", test.row, got, test.want)
		}
		label := map[bool]string{false: "working", true: "blocked"}[test.row.Blocked]
		if got := workLabel(test.row); got != label {
			t.Errorf("workLabel(%+v) = %q, want %q", test.row, got, label)
		}
	}
}

func TestLiveMarkerPulsesForAChatWorkingThroughItsAgents(t *testing.T) {
	if got := liveMarker(compose.Row{Kind: compose.LiveClaude, AgentsWorking: 2}, deckPulseNS); got != "◉" {
		t.Errorf("a chat whose agents are working pulses: %q", got)
	}
	blocked := compose.Row{Kind: compose.LiveClaude, Working: true, Blocked: true}
	if got, want := liveMarker(blocked, deckPulseNS), rowMarker(compose.LiveClaude); got != want {
		t.Errorf("a chat that waits on its owner holds still: %q, want %q", got, want)
	}
}
