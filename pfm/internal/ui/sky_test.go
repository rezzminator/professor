package ui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/rezzminator/professor/pfm/internal/compose"
	"github.com/rezzminator/professor/pfm/internal/sky"
)

func TestSkyHeaderAtThreeFixedTimesAndKillSwitch(t *testing.T) {
	snapshot := fixtureSnapshot(120)
	model := NewModel(snapshot)
	frames := make([]string, 3)
	for index, now := range []int64{0, 2_000_000_000, 4_000_000_000} {
		model.nowNS = now
		frames[index] = model.renderHeader(120)
		lines := strings.Split(frames[index], "\n")
		if len(lines) != 3 {
			t.Fatalf("time %d header lines=%d, want 3", now, len(lines))
		}
		for lineIndex, line := range lines {
			if lipgloss.Width(line) != 120 {
				t.Fatalf("time %d line %d width=%d", now, lineIndex, lipgloss.Width(line))
			}
		}
	}
	if frames[0] == frames[1] || frames[1] == frames[2] || frames[0] == frames[2] {
		t.Fatalf("fixed-time sky headers did not animate")
	}

	snapshot.NoSky = true
	disabled := NewModel(snapshot)
	if disabled.skyEnabled || strings.Contains(ansi.Strip(disabled.renderHeader(120)), "✦") {
		t.Fatalf("--no-sky left the widget active:\n%s", ansi.Strip(disabled.renderHeader(120)))
	}
}

func TestRefreshDiffFiresBirthCometAndDeathEmber(t *testing.T) {
	snapshot := fixtureSnapshot(120)
	model := NewModel(snapshot)
	refresh := snapshot
	refresh.NowNS += 1_000_000_000
	refresh.Rows = append([]compose.Row(nil), snapshot.Rows...)
	refresh.Rows = append(refresh.Rows, compose.Row{
		Kind: compose.LiveCodex, ID: "new-live", Socket: "probe-new-live",
		Name: "new live", Project: "alpha",
	})
	model.applyRefresh(refresh)
	if len(model.skyEvents) != 1 || model.skyEvents[0].Kind != sky.EventBirth {
		t.Fatalf("birth events = %#v", model.skyEvents)
	}
	model.nowNS = refresh.NowNS + sky.CometDurNS/2
	if !strings.Contains(ansi.Strip(model.renderHeader(120)), "*") {
		t.Fatalf("birth event did not render a comet:\n%s", ansi.Strip(model.renderHeader(120)))
	}

	death := refresh
	death.NowNS += 1_000_000_000
	death.Rows = append([]compose.Row(nil), snapshot.Rows...)
	model.applyRefresh(death)
	if len(model.skyEvents) == 0 || model.skyEvents[len(model.skyEvents)-1].Kind != sky.EventDeath {
		t.Fatalf("death events = %#v", model.skyEvents)
	}
}
