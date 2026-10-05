package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

func TestFleetVitalsCountLiveResumableAndAgentRows(t *testing.T) {
	vitals := deckModel(120, 30).fleetVitals()
	if vitals.live[pfmengine.Claude] != 3 || vitals.live[pfmengine.Codex] != 1 {
		t.Errorf("three live Claude chats and one live Codex chat, got %v", vitals.live)
	}
	// The agent runs on Claude, but the masthead names it once, as an agent.
	if vitals.agents != 1 || vitals.booting != 0 {
		t.Errorf("one agent and nothing booting, got agents=%d booting=%d", vitals.agents, vitals.booting)
	}
	if vitals.resumable < 5 {
		t.Errorf("the fixture holds at least five resumable chats, got %d", vitals.resumable)
	}
}

func TestMastheadTitleNamesTheWordmarkAndTheFleet(t *testing.T) {
	model := deckModel(120, 30)
	title := ansi.Strip(model.mastheadTitle(100))
	for _, want := range []string{"◆ pfm", "● 3 Claude", "● 1 Codex", "resumable", "1 agents"} {
		if !strings.Contains(title, want) {
			t.Errorf("the masthead lacks %q: %q", want, title)
		}
	}
	if got := ansi.StringWidth(title); got != 100 {
		t.Errorf("the masthead spans %d cells, want 100", got)
	}
	idle := NewModel(Snapshot{View: model.view, NowNS: fixtureNowNS, Width: 120, Height: 30})
	if got := ansi.Strip(idle.mastheadTitle(60)); !strings.Contains(got, "no live chats") {
		t.Errorf("an idle fleet says so: %q", got)
	}
}

func TestMastheadFillIsExactlyWidthCellsWhateverTheContent(t *testing.T) {
	model := deckModel(120, 30)
	spans := []span{{text: " ◆ pfm ", paint: tone{fg: mastheadInk, bg: "#7aa2f7"}}, {text: strings.Repeat("字", 40)}}
	for width := 1; width <= 90; width++ {
		if got := ansi.StringWidth(model.mastheadFill(spans, width)); got != width {
			t.Fatalf("mastheadFill(%d) spans %d cells", width, got)
		}
	}
}

func TestMastheadSweepFollowsTheActivityClock(t *testing.T) {
	model := deckModel(120, 30)
	model.nowNS = fixtureNowNS
	model.activity = nil
	if _, on := model.sweepCentre(100); on {
		t.Error("a picker with no clock never sweeps")
	}
	model.activity = NewActivityClock(time.Unix(0, fixtureNowNS-int64(200*time.Millisecond)))
	early, on := model.sweepCentre(100)
	if !on {
		t.Fatal("a keystroke 200ms ago is still sweeping")
	}
	model.activity = NewActivityClock(time.Unix(0, fixtureNowNS-int64(1000*time.Millisecond)))
	late, on := model.sweepCentre(100)
	if !on || late <= early {
		t.Errorf("the light moves right as time passes: early %.1f late %.1f", early, late)
	}
	model.activity = NewActivityClock(time.Unix(0, fixtureNowNS-mastheadSweepNS))
	if _, on := model.sweepCentre(100); on {
		t.Error("the sweep ends once its time is up")
	}
	swept := deckModel(120, 30)
	swept.nowNS = fixtureNowNS
	swept.activity = NewActivityClock(time.Unix(0, fixtureNowNS-int64(500*time.Millisecond)))
	rested := swept
	rested.activity = NewActivityClock(time.Unix(0, fixtureNowNS-10*mastheadSweepNS))
	lit, still := swept.mastheadTitle(100), rested.mastheadTitle(100)
	if lit == still {
		t.Error("the sweep paints light the resting masthead lacks")
	}
	if ansi.Strip(lit) != ansi.Strip(still) {
		t.Error("the sweep changes colour only, never the text")
	}
}

func TestRenderTabsLightsTheOpenTab(t *testing.T) {
	model := deckModel(120, 30)
	chip := tone{fg: mastheadInk, bg: configuredPalette.Accent, bold: true}
	for index, name := range []string{" Chats ", " Stats ", " Limits ", " cosmos "} {
		model.tab = Tab(index)
		line := model.renderTabs(80)
		if !strings.Contains(line, chip.render(name)) {
			t.Errorf("tab %d: %q is not the solid chip", index, name)
		}
		if got := ansi.StringWidth(line); got != 80 {
			t.Errorf("tab %d: the tab line spans %d cells, want 80", index, got)
		}
	}
}

func TestChatsHeaderLineKeepsItsContextTokens(t *testing.T) {
	model := deckModel(120, 30)
	line := ansi.Strip(model.chatsHeaderLine(100))
	for _, want := range []string{"account 2", "⚡ 1h", "rows", "1 hidden", "3 empty"} {
		if !strings.Contains(line, want) {
			t.Errorf("the context line lacks %q: %q", want, line)
		}
	}
	if strings.Contains(line, "refreshing") {
		t.Errorf("a settled picker is not refreshing: %q", line)
	}
	model.refreshing = true
	if line := ansi.Strip(model.chatsHeaderLine(100)); !strings.Contains(line, "⟳ refreshing") {
		t.Errorf("a refreshing picker says so: %q", line)
	}
	model.reminderError = "disk on fire"
	line = ansi.Strip(model.chatsHeaderLine(100))
	if !strings.Contains(line, "reminder flags unreadable: disk on fire") {
		t.Errorf("an unreadable flag store never reads as no reminders: %q", line)
	}
}

func TestRenderHeaderIsThreeFullLinesWithOrWithoutTheSky(t *testing.T) {
	for _, noSky := range []bool{false, true} {
		snapshot := deckFleet(120, 30)
		snapshot.NoSky = noSky
		model := NewModel(snapshot)
		for _, tab := range []Tab{TabChats, TabStats, TabLimits, TabCosmos} {
			model.tab = tab
			lines := strings.Split(model.renderHeader(120), "\n")
			if len(lines) != 3 {
				t.Fatalf("noSky=%v tab=%d: header has %d lines", noSky, tab, len(lines))
			}
			for number, line := range lines {
				if got := ansi.StringWidth(line); got != 120 {
					t.Errorf("noSky=%v tab=%d line %d spans %d cells", noSky, tab, number, got)
				}
			}
		}
	}
}
