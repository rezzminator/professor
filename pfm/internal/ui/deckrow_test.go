package ui

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/rezzminator/professor/pfm/internal/compose"
	"github.com/rezzminator/professor/pfm/internal/theme"
)

func TestDeckPlanAccountsForEveryCell(t *testing.T) {
	for _, chip := range []bool{false, true} {
		for _, grouped := range []bool{false, true} {
			for width := 30; width <= 240; width++ {
				plan := deckPlan(width, grouped, chip)
				used := plan.lead + plan.name + plan.gap + deckClusterWidth(plan.columns)
				if plan.badges > 0 {
					used += plan.badges + 1
				}
				if used != width {
					t.Fatalf("chip=%v grouped=%v width=%d: plan spends %d cells: %#v", chip, grouped, width, used, plan)
				}
				if plan.name < deckNameMin && width >= 44 {
					t.Fatalf("width=%d: name squeezed to %d: %#v", width, plan.name, plan)
				}
				if plan.name > deckNameMax {
					t.Fatalf("width=%d: name %d outgrew its cap", width, plan.name)
				}
			}
		}
	}
}

func TestDeckPlanShedsColumnsFromTheRightInOrder(t *testing.T) {
	has := func(plan deckLayout, column deckColumn) bool {
		for _, present := range plan.columns {
			if present == column {
				return true
			}
		}
		return false
	}
	wide := deckPlan(120, false, false)
	for _, column := range []deckColumn{deckColPrompts, deckColSize, deckColRuler, deckColAge} {
		if !has(wide, column) {
			t.Errorf("a 120-cell row drops column %d", column)
		}
	}
	if wide.badges != deckBadgesW {
		t.Errorf("a wide row keeps its full badge column, got %d", wide.badges)
	}
	narrow := deckPlan(40, false, false)
	if has(narrow, deckColRuler) {
		t.Errorf("a 40-cell row sheds the ruler first: %#v", narrow)
	}
	if !has(narrow, deckColAge) {
		t.Error("the age survives every width")
	}
	if narrow.name < deckNameMin {
		t.Errorf("the name keeps its minimum at 40 cells, got %d", narrow.name)
	}
	tight := deckPlan(32, false, false)
	if has(tight, deckColSize) || has(tight, deckColPrompts) || has(tight, deckColModel) ||
		!has(tight, deckColWork) {
		t.Errorf("a 32-cell row sheds the size, prompts and model before the work gauge: %#v", tight)
	}
	bare := deckPlan(24, false, false)
	if len(bare.columns) != 1 || !has(bare, deckColAge) {
		t.Errorf("a 24-cell row keeps only the age: %#v", bare)
	}
	selected := deckPlan(60, false, true)
	if !has(selected, deckColChip) || has(selected, deckColRuler) {
		t.Errorf("a selected row trades the ruler for the chip: %#v", selected)
	}
}

func deckRowKinds() []compose.Row {
	return []compose.Row{
		{
			Kind:        compose.LiveClaude,
			Name:        "live claude",
			Project:     "a",
			ActivityNS:  fixtureNowNS - int64(5*time.Second),
			PromptCount: 7,
			Size:        2048,
			Account:     1,
			C1H:         true,
		},
		{
			Kind:        compose.LiveCodex,
			Name:        "live codex",
			Project:     "a",
			ActivityNS:  fixtureNowNS - int64(time.Hour),
			PromptCount: 1234,
			Size:        9 << 20,
			Account:     2,
			ServerCount: 2,
		},
		{Kind: compose.LiveOpenCode, Name: "live opencode", Project: "a", ActivityNS: fixtureNowNS - int64(time.Hour)},
		{
			Kind:       compose.LiveSplit,
			Name:       "🚀🧭🛠️📦🧪✨ split",
			Project:    "a",
			SplitCount: 3,
			Accounts:   []int{1, 3},
			ActivityNS: fixtureNowNS - 1,
		},
		{
			Kind:       compose.Agent,
			Name:       "Agent 界面 needle 列对齐测试名字",
			Project:    "b",
			ActivityNS: fixtureNowNS - int64(26*time.Hour),
			Killed:     true,
		},
		{Kind: compose.Booting, Name: "booting", Project: "b"},
		{
			Kind:        compose.ResumeClaude,
			Name:        strings.Repeat("long name ", 12),
			Project:     "b",
			ActivityNS:  fixtureNowNS - int64(40*24*time.Hour),
			PromptCount: 99999,
			Size:        1 << 40,
		},
		{
			Kind:       compose.ResumeCodex,
			Name:       "resume codex",
			Project:    "b",
			ActivityNS: fixtureNowNS - int64(3*24*time.Hour),
			Reminded:   true,
		},
		{
			Kind:       compose.ResumeOpenCode,
			Name:       "resume opencode",
			Project:    "c",
			ActivityNS: fixtureNowNS - int64(2*time.Minute),
		},
		{Kind: compose.NewClaude, Name: "New Claude chat", Project: "c"},
		{Kind: compose.NewCodex, Name: "New Codex chat", Project: "c"},
		{Kind: compose.NewOpenCode, Name: "New OpenCode chat", Project: "c"},
		{Kind: compose.ProfessorUpdate, Name: "v9.9.9", Project: "~"},
		{Kind: compose.ProfessorUpdateFailed, Name: "check failing", Project: "~"},
		{Kind: compose.ResumeClaude, Project: "c", ActivityNS: fixtureNowNS - 1},
	}
}

func TestEveryRowKindFillsExactlyTheWidthItIsGiven(t *testing.T) {
	model := deckModel(120, 30)
	model.mergeNewChat = true
	for _, width := range []int{30, 31, 40, 47, 59, 72, 73, 100, 118, 119, 150, 200} {
		for _, row := range deckRowKinds() {
			for _, selected := range []bool{false, true} {
				for _, grouped := range []bool{false, true} {
					line := model.renderGroupedRow(row, selected, width, grouped)
					if got := lipgloss.Width(line); got != width {
						t.Fatalf("%v selected=%v grouped=%v at %d: row is %d cells wide: %q",
							row.Kind, selected, grouped, width, got, ansi.Strip(line))
					}
					if strings.Contains(line, "\n") {
						t.Fatalf("%v at %d wrapped onto a second line", row.Kind, width)
					}
				}
			}
		}
	}
}

func TestRightClusterColumnsAlignAcrossRows(t *testing.T) {
	model := deckModel(100, 30)
	rows := []compose.Row{deckRowKinds()[0], deckRowKinds()[1], deckRowKinds()[6], deckRowKinds()[7]}
	var ageColumns, rulerColumns []int
	for _, row := range rows {
		plain := ansi.Strip(model.renderGroupedRow(row, false, 100, false))
		ageColumns = append(ageColumns, lipgloss.Width(strings.TrimRight(plain, " ")))
		rulerColumns = append(rulerColumns, displayIndex(plain, "▰"))
	}
	for index := range rows {
		if ageColumns[index] != 100 {
			t.Errorf("row %d: the age column must end at the right edge, ends at %d", index, ageColumns[index])
		}
	}
	for index := 1; index < len(rulerColumns); index++ {
		if rulerColumns[index] != rulerColumns[0] && rulerColumns[index] >= 0 && rulerColumns[0] >= 0 {
			t.Errorf("ruler columns drift: %v", rulerColumns)
		}
	}
}

func TestLiveMarkerPulsesOnlyWhileTheChatIsMidTurn(t *testing.T) {
	working := compose.Row{Kind: compose.LiveClaude, Working: true}
	idle := compose.Row{Kind: compose.LiveClaude, ActivityNS: fixtureNowNS - int64(2*time.Second)}
	resumable := compose.Row{Kind: compose.ResumeClaude, Working: true}
	even, odd := int64(0), deckPulseNS
	base := fixtureNowNS - fixtureNowNS%(2*deckPulseNS)
	if got := liveMarker(working, base+even); got != "●" {
		t.Errorf("even phase = %q, want ●", got)
	}
	if got := liveMarker(working, base+odd); got != "◉" {
		t.Errorf("odd phase = %q, want ◉", got)
	}
	if got := liveMarker(idle, base+odd); got != "●" {
		t.Errorf("a live chat that is not mid-turn holds steady, however recently it wrote: %q", got)
	}
	if got := liveMarker(resumable, base+odd); got != "↻" {
		t.Errorf("a resumable chat never pulses, got %q", got)
	}
}

func TestSelectedRowPaintsItsHighlightUnderEverySegment(t *testing.T) {
	model := deckModel(120, 30)
	palette := theme.Load("default")
	selectedRGB := rgbFromHex(palette.Selected)
	want := "48;2;" + strconv.Itoa(
		int(selectedRGB.R),
	) + ";" + strconv.Itoa(
		int(selectedRGB.G),
	) + ";" + strconv.Itoa(
		int(selectedRGB.B),
	)
	row := deckRowKinds()[1]
	selected := model.renderGroupedRow(row, true, 100, false)
	plain := model.renderGroupedRow(row, false, 100, false)
	if !strings.Contains(selected, want) {
		t.Error("the selected row carries the Selected background")
	}
	if strings.Contains(plain, want) {
		t.Error("an unselected row paints no highlight")
	}
	for _, line := range strings.Split(strings.ReplaceAll(selected, "\x1b[m", "\x1b[m\n"), "\n") {
		if line != "" && !strings.Contains(line, want) {
			t.Fatalf("segment %q of the selected row lost the highlight background", line)
		}
	}
	if !strings.HasPrefix(ansi.Strip(selected), "›") || !strings.HasPrefix(ansi.Strip(plain), "│") {
		t.Errorf("rail glyphs: selected %q, plain %q", ansi.Strip(selected)[:4], ansi.Strip(plain)[:4])
	}
}

func TestSearchMatchesAreLitInTheRowName(t *testing.T) {
	model := deckModel(120, 30)
	row := compose.Row{
		Kind:       compose.ResumeClaude,
		Name:       "schema migration plan",
		Project:    "atlas",
		ActivityNS: fixtureNowNS - 1,
	}
	quiet := model.renderGroupedRow(row, false, 100, false)
	model.query.SetValue("mig")
	lit := model.renderGroupedRow(row, false, 100, false)
	warn := rgbFromHex(theme.Load("default").Warn)
	escape := "38;2;" + strconv.Itoa(int(warn.R)) + ";" + strconv.Itoa(int(warn.G)) + ";" + strconv.Itoa(int(warn.B))
	if strings.Contains(quiet, escape) {
		t.Error("with no query nothing is lit")
	}
	if !strings.Contains(lit, escape) {
		t.Error("the matched letters are painted in the warn colour")
	}
	if ansi.Strip(lit) != ansi.Strip(quiet) {
		t.Error("highlighting must not change a single character")
	}
}

func TestSelectedRowOffersTheCurrentActionAsAChipAtEveryWidth(t *testing.T) {
	narrow := deckModel(80, 28)
	row := deckRowKinds()[0]
	if chip := ansi.Strip(narrow.renderGroupedRow(row, true, 78, false)); !strings.Contains(chip, "◖▶ open◗") {
		t.Errorf("a selected row shows its armed action, got %q", chip)
	}
	if chip := ansi.Strip(narrow.renderGroupedRow(row, false, 78, false)); strings.Contains(chip, "◖") {
		t.Errorf("an unselected row shows no chip, got %q", chip)
	}
	wide := deckModel(160, 38)
	// The dossier lists every action beside the list; the row still carries the
	// armed one, because the lane scripts read the carousel off the selected row.
	if chip := ansi.Strip(wide.renderGroupedRow(row, true, 118, false)); !strings.Contains(chip, "◖▶ open◗") {
		t.Errorf("a selected row beside the dossier still shows its armed action: %q", chip)
	}
}

func TestMergedNewChatRowShowsTheEngineChoiceAndSurvivesNarrowWidths(t *testing.T) {
	model := deckModel(120, 30)
	row := compose.Row{Kind: compose.NewClaude, Name: "New Claude chat", Project: "~"}
	if plain := ansi.Strip(model.renderGroupedRow(row, false, 100, false)); !strings.Contains(plain, "[ Claude ]") {
		t.Errorf("the chosen engine is bracketed, got %q", plain)
	}
	if plain := ansi.Strip(model.renderGroupedRow(row, false, 30, false)); lipgloss.Width(plain) != 30 {
		t.Errorf("a cramped new-chat row still fits its width: %q", plain)
	}
}

func TestNoticeRowsKeepTheirBanners(t *testing.T) {
	model := deckModel(120, 30)
	update := ansi.Strip(
		model.renderGroupedRow(compose.Row{Kind: compose.ProfessorUpdate, Name: "v9.9.9"}, true, 110, false),
	)
	for _, want := range []string{"PROFESSOR UPDATE", "v9.9.9", "◖ Claude ◗", "Enter → guided upgrade"} {
		if !strings.Contains(update, want) {
			t.Errorf("update banner %q lacks %q", update, want)
		}
	}
	failed := ansi.Strip(
		model.renderGroupedRow(compose.Row{Kind: compose.ProfessorUpdateFailed, Name: "x"}, false, 110, false),
	)
	if !strings.Contains(failed, "PROFESSOR UPDATE CHECK FAILING") || strings.Contains(failed, "Enter") {
		t.Errorf("failed banner = %q", failed)
	}
}

func TestBadgeSpansFitTheirColumn(t *testing.T) {
	parts := rowBadgeParts(
		compose.Row{
			Kind:        compose.LiveCodex,
			ServerCount: 2,
			SplitCount:  3,
			Account:     2,
			C1H:         true,
			Attached:    true,
			Here:        true,
			Killed:      true,
		},
	)
	for width := 0; width <= 30; width++ {
		spans := badgeSpans(parts, width, "")
		if got := spansWidth(spans); got != width {
			t.Fatalf("badgeSpans(%d) spans %d cells", width, got)
		}
	}
}

func TestGroupHexAlternatesWithTheProjectOrdinal(t *testing.T) {
	model := deckModel(120, 30)
	palette := theme.Load("default")
	seen := map[string]bool{}
	for _, project := range []string{"atlas", "harbor", "lumen", "quartz"} {
		seen[groupHexOf(model, compose.Row{Project: project})] = true
	}
	if !seen[palette.GroupA] || !seen[palette.GroupB] || len(seen) != 2 {
		t.Errorf("projects alternate between the two group colours, saw %v", seen)
	}
}

// A chat that arrives in a refresh is drawn brighter than its settled self for
// arrivalGlowNS, then exactly like it: the flare the sky is woken to play out.
func TestArrivedRowFlaresThenSettlesIntoItsColour(t *testing.T) {
	model := deckModel(120, 30)
	model.nowNS = fixtureNowNS
	arrival := newArrival()
	model.deck.noteArrivals(nil, []compose.Row{arrival}, fixtureNowNS)
	settled := model
	settled.deck.arrivals = nil
	if model.renderGroupedRow(arrival, false, 100, false) == settled.renderGroupedRow(arrival, false, 100, false) {
		t.Error("a chat that just arrived is drawn exactly like a settled one: the flare never renders")
	}
	model.nowNS, settled.nowNS = fixtureNowNS+arrivalGlowNS, fixtureNowNS+arrivalGlowNS
	if model.renderGroupedRow(arrival, false, 100, false) != settled.renderGroupedRow(arrival, false, 100, false) {
		t.Error("the flare must be gone once arrivalGlowNS has passed")
	}
}
