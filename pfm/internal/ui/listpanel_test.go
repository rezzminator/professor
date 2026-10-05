package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/rezzminator/professor/pfm/internal/compose"
)

func TestRenderListPanelIsExactlyTheRequestedSize(t *testing.T) {
	for _, size := range [][2]int{{40, 6}, {60, 8}, {80, 9}, {100, 14}, {200, 30}} {
		model := deckModel(size[0], size[1]+6)
		lines := strings.Split(model.renderListPanel(size[0], size[1]), "\n")
		if len(lines) != size[1] {
			t.Errorf("a %dx%d panel has %d lines", size[0], size[1], len(lines))
		}
		for number, line := range lines {
			if got := ansi.StringWidth(line); got != size[0] {
				t.Errorf("%dx%d panel line %d spans %d cells: %q", size[0], size[1], number, got, ansi.Strip(line))
			}
		}
	}
}

func TestProjectTalliesCountLiveAndResumableChatsPerProject(t *testing.T) {
	tallies := deckModel(120, 30).projectTallies()
	if got := tallies["atlas"]; got != (projectTally{live: 3, resumable: 2}) {
		t.Errorf("atlas holds three live chats and two resumable ones, got %+v", got)
	}
	if got := tallies["lumen"]; got != (projectTally{resumable: 2}) {
		t.Errorf("lumen holds two resumable chats, got %+v", got)
	}
	if _, ok := tallies["nowhere"]; ok {
		t.Error("a project with no visible chat has no tally")
	}
}

func TestProjectHeaderShowsItsCensusAndLightsTheSearch(t *testing.T) {
	model := deckModel(120, 30)
	header := ansi.Strip(model.projectHeader("atlas", 0, 60, projectTally{live: 3, resumable: 2}))
	if !strings.HasPrefix(header, "╭─ atlas ") || !strings.Contains(header, "● 3  ↻ 2") {
		t.Errorf("the header names the project and counts its chats: %q", header)
	}
	if got := ansi.StringWidth(header); got != 60 {
		t.Errorf("the header spans %d cells, want 60", got)
	}
	bare := ansi.Strip(model.projectHeader("lumen", 1, 40, projectTally{}))
	if strings.Contains(bare, "●") || strings.Contains(bare, "↻") {
		t.Errorf("a project with no chats shows no census: %q", bare)
	}
	narrow := ansi.Strip(model.projectHeader("a-project-with-a-very-long-name-indeed", 0, 24, projectTally{live: 1}))
	if got := ansi.StringWidth(narrow); got != 24 || !strings.Contains(narrow, "…") {
		t.Errorf("a long name is clipped to the header's width (%d cells): %q", got, narrow)
	}
	for _, runeValue := range "atl" {
		model, _ = applyKey(t, model, printableKey(runeValue))
	}
	lit := tone{fg: configuredPalette.Warn, bold: true}.render("atl")
	if !strings.Contains(model.projectHeader("atlas", 0, 60, projectTally{}), lit) {
		t.Error("the matched letters of a project name are lit")
	}
}

func TestEmptyListLineNamesWhyNothingIsShown(t *testing.T) {
	model := deckModel(120, 30)
	for _, runeValue := range "zzzq" {
		model, _ = applyKey(t, model, printableKey(runeValue))
	}
	line := ansi.Strip(model.emptyListLine(80))
	if !strings.Contains(line, "no matches for “zzzq”") || !strings.Contains(line, "⌃U") {
		t.Errorf("a filter with no match says so and how to clear it: %q", line)
	}
	empty := Snapshot{View: compose.AllView, NowNS: fixtureNowNS, Width: 120, Height: 30}
	if got := ansi.Strip(NewModel(empty).emptyListLine(80)); !strings.Contains(got, "no chats here yet") {
		t.Errorf("an empty fleet says so: %q", got)
	}
	if got := ansi.StringWidth(model.emptyListLine(80)); got != 80 {
		t.Errorf("the empty line spans %d cells, want 80", got)
	}
}

func TestTempoAxisNeedsRoomUnderTheRows(t *testing.T) {
	model := deckModel(120, 30)
	roomy := ansi.Strip(model.renderListPanel(100, 9))
	if !strings.Contains(roomy, "┴1h") {
		t.Errorf("a panel with seven inner lines carries the tempo ruler:\n%s", roomy)
	}
	cramped := ansi.Strip(model.renderListPanel(100, 8))
	if strings.Contains(cramped, "┴") {
		t.Errorf("a panel with six inner lines keeps every line for the rows:\n%s", cramped)
	}
}

func TestSelectedRowStaysInViewWhileTheCursorWalksTheFleet(t *testing.T) {
	model := NewModel(largeSnapshot(60))
	model.nowNS = fixtureNowNS
	model, _ = applyKey(t, model, specialKey(tea.KeyDown))
	for step := 0; step < 70; step++ {
		row, ok := model.selectedRow()
		if !ok {
			t.Fatalf("step %d: no selected row", step)
		}
		panel := ansi.Strip(model.renderListPanel(100, 14))
		if !strings.Contains(panel, row.Name) {
			t.Fatalf("step %d: the selected chat %q scrolled out of view:\n%s", step, row.Name, panel)
		}
		model, _ = applyKey(t, model, specialKey(tea.KeyDown))
	}
}

func TestProjectBannerIsNotRepeatedWithinOneProject(t *testing.T) {
	panel := ansi.Strip(deckModel(120, 40).renderListPanel(100, 34))
	for _, project := range []string{"atlas", "harbor", "lumen", "quartz"} {
		if got := strings.Count(panel, fmt.Sprintf("╭─ %s ", project)); got != 1 {
			t.Errorf("project %q has %d banners, want one:\n%s", project, got, panel)
		}
	}
}
