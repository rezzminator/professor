package ui

import (
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/rezzminator/professor/pfm/internal/compose"
)

func TestDossierShowsOnlyOnAWideEnoughTerminal(t *testing.T) {
	for width, want := range map[int]bool{20: false, 40: false, 111: false, 112: true, 160: true} {
		if got := (Model{width: width}).showDossier(); got != want {
			t.Errorf("showDossier at %d = %v, want %v", width, got, want)
		}
	}
}

func TestDossierPanelFillsItsFrameForEveryRowKind(t *testing.T) {
	model := deckModel(160, 38)
	rows := append(deckRowKinds(), model.rows...)
	for _, height := range []int{4, 5, 6, 8, 11, 14, 20, 27, 40} {
		for _, row := range rows {
			model.rows = []compose.Row{row}
			model.rebuild("", 0)
			panel := model.renderDossier(height)
			lines := strings.Split(panel, "\n")
			if len(lines) != height {
				t.Fatalf("%v at height %d: %d lines", row.Kind, height, len(lines))
			}
			for _, line := range lines {
				if got := lipgloss.Width(line); got != dossierWidth {
					t.Fatalf("%v at height %d: a line is %d cells wide: %q", row.Kind, height, got, ansi.Strip(line))
				}
			}
		}
	}
}

func TestDossierSaysNothingSelectedOnAnEmptyList(t *testing.T) {
	model := deckModel(160, 38)
	model.rows = nil
	model.rebuild("", 0)
	if panel := ansi.Strip(model.renderDossier(12)); !strings.Contains(panel, "nothing selected") {
		t.Errorf("empty dossier = %q", panel)
	}
}

func TestDossierQuotesTheLastPromptAndSaysWhenThereIsNone(t *testing.T) {
	model := selectChat(t, deckModel(160, 38), "P:BUILDER")
	panel := ansi.Strip(model.renderDossier(30))
	if !strings.Contains(panel, "┃ Wire the tempo axis") || !strings.Contains(panel, "viewport scrolls.") {
		t.Errorf("the prompt is quoted whole behind a rail:\n%s", panel)
	}
	bare := selectChat(t, deckModel(160, 38), "P:BUILDER-2")
	if panel := ansi.Strip(bare.renderDossier(30)); !strings.Contains(panel, "no prompt recorded") {
		t.Errorf("a chat with no prompt says so:\n%s", panel)
	}
}

func TestDossierCutsAVeryLongPromptWithAnEllipsis(t *testing.T) {
	model := selectChat(t, deckModel(160, 38), "P:BUILDER")
	for index := range model.rows {
		if model.rows[index].Name == "P:BUILDER" {
			model.rows[index].LastPrompt = strings.Repeat("word ", 200)
		}
	}
	lines := model.dossierPrompt(model.rows[model.filtered[model.cursor]], 36, dossierPromptMax)
	if len(lines) != dossierPromptMax {
		t.Fatalf("a long prompt takes %d lines, got %d", dossierPromptMax, len(lines))
	}
	if last := ansi.Strip(joinSpans(lines[len(lines)-1])); !strings.Contains(last, "…") {
		t.Errorf("the cut must be visible, last line %q", last)
	}
}

func TestDossierDropsDetailBeforeItDropsTheHead(t *testing.T) {
	model := selectChat(t, deckModel(160, 38), "P:BUILDER")
	row, _ := model.selectedRow()
	tall := model.dossierLines(row, 36, 40)
	short := model.dossierLines(row, 36, 9)
	if len(short) > 9 {
		t.Fatalf("a short pane must fit its height, got %d lines", len(short))
	}
	if len(tall) <= len(short) {
		t.Fatalf("a tall pane shows more than a short one: %d vs %d", len(tall), len(short))
	}
	head := ansi.Strip(joinSpans(short[0]))
	if !strings.Contains(head, "LIVE") || !strings.Contains(ansi.Strip(joinSpans(short[1])), "P:BUILDER") {
		t.Errorf("the status chip and the name survive a short pane: %q", head)
	}
}

func TestDossierActionsDimWhatTheRowWouldRefuse(t *testing.T) {
	model := deckModel(160, 38)
	cases := []struct {
		name string
		row  compose.Row
		want [5]bool
	}{
		{
			"live claude",
			compose.Row{Kind: compose.LiveClaude, ID: "x", Socket: "s"},
			[5]bool{true, true, true, true, true},
		},
		{"live split", compose.Row{Kind: compose.LiveSplit, Socket: "s"}, [5]bool{true, true, true, false, false}},
		{"resumable", compose.Row{Kind: compose.ResumeClaude, ID: "x"}, [5]bool{true, false, true, true, false}},
		{"booting", compose.Row{Kind: compose.Booting}, [5]bool{true, false, true, false, false}},
		{
			"killed by label",
			compose.Row{Kind: compose.ResumeClaude, ID: "x", NameKilled: true},
			[5]bool{true, false, true, false, false},
		},
		{
			"live without a socket",
			compose.Row{Kind: compose.LiveCodex, ID: "x"},
			[5]bool{true, true, true, true, false},
		},
		{"update failed", compose.Row{Kind: compose.ProfessorUpdateFailed}, [5]bool{false, false, false, false, false}},
	}
	for _, test := range cases {
		for index, want := range test.want {
			if got := model.dossierActionAvailable(index, test.row); got != want {
				t.Errorf("%s: action %d available = %v, want %v", test.name, index, got, want)
			}
		}
	}
}

func TestDossierMarksTheArmedActionAndLabelsTheCacheState(t *testing.T) {
	model := selectChat(t, deckModel(160, 38), "P:BUILDER")
	model.actionIndex = 3
	row, _ := model.selectedRow()
	actions := model.dossierActions(row, 36)
	if len(actions) != len(carouselActions) {
		t.Fatalf("one line per carousel action, got %d", len(actions))
	}
	armed := ansi.Strip(joinSpans(actions[3]))
	if !strings.Contains(armed, "◖") || !strings.Contains(armed, "kill") || !strings.Contains(armed, "◗") {
		t.Errorf("the armed action is boxed: %q", armed)
	}
	if other := ansi.Strip(joinSpans(actions[0])); strings.Contains(other, "◖") {
		t.Errorf("only the armed action is boxed: %q", other)
	}
	if cache := ansi.Strip(joinSpans(actions[2])); !strings.Contains(cache, "1h  on") {
		t.Errorf("the 1h line shows the cache state: %q", cache)
	}
	model.cache1H = false
	if cache := ansi.Strip(joinSpans(model.dossierActions(row, 36)[2])); !strings.Contains(cache, "1h  off") {
		t.Errorf("the 1h line shows the cache state: %q", cache)
	}
	row.Killed = true
	if kill := ansi.Strip(joinSpans(model.dossierActions(row, 36)[3])); !strings.Contains(kill, "unhide") {
		t.Errorf("a hidden chat offers to unhide: %q", kill)
	}
	for _, line := range actions {
		if got := spansWidth(line); got != 36 {
			t.Errorf("an action line spans %d cells, want 36", got)
		}
	}
}

func TestDossierStatusNamesEveryState(t *testing.T) {
	cases := map[string]compose.Row{
		"HIDDEN":    {Kind: compose.LiveClaude, Killed: true},
		"SPLIT":     {Kind: compose.LiveSplit},
		"LIVE":      {Kind: compose.LiveCodex},
		"AGENT":     {Kind: compose.Agent},
		"BOOTING":   {Kind: compose.Booting},
		"NEW":       {Kind: compose.NewOpenCode},
		"UPDATE":    {Kind: compose.ProfessorUpdate},
		"RESUMABLE": {Kind: compose.ResumeCodex},
	}
	for want, row := range cases {
		if got := dossierStatus(row); got != want {
			t.Errorf("dossierStatus(%v) = %q, want %q", row.Kind, got, want)
		}
	}
}

func TestLogFractionStaysOnTheGaugeAndKeepsOrder(t *testing.T) {
	if logFraction(0, 100) != 0 || logFraction(5, 0) != 0 || logFraction(-4, 9) != 0 {
		t.Error("nothing, or no peak, draws an empty gauge")
	}
	if got := logFraction(100, 100); got < 0.999 || got > 1.001 {
		t.Errorf("the peak fills the gauge, got %v", got)
	}
	if !(logFraction(10, 1_000_000) < logFraction(1_000, 1_000_000)) {
		t.Error("a bigger value fills more of the gauge")
	}
}

func TestGaugeBarIsAlwaysItsFullWidth(t *testing.T) {
	for _, frac := range []float64{-1, 0, 0.04, 0.5, 0.96, 1, 7} {
		if got := spansWidth(gaugeBar(frac, 10, tone{}, tone{})); got != 10 {
			t.Errorf("gaugeBar(%v) spans %d cells", frac, got)
		}
	}
}

func TestPathHelpersWriteHomeAsTildeAndKeepTheTail(t *testing.T) {
	if got := homeShort("/home/tester/atlas/cmd", "/home/tester"); got != "~/atlas/cmd" {
		t.Errorf("homeShort = %q", got)
	}
	if got := homeShort("/home/tester/x", "/home/test"); got != "/home/tester/x" {
		t.Errorf("a sibling directory must not be shortened: %q", got)
	}
	if got := homeShort("/srv/x", ""); got != "/srv/x" {
		t.Errorf("no home leaves the path alone: %q", got)
	}
	if got := tailCells(
		"~/a/very/long/path/to/the/project",
		14,
	); lipgloss.Width(got) != 14 || !strings.HasPrefix(got, "…") ||
		!strings.HasSuffix(got, "project") {
		t.Errorf("tailCells = %q", got)
	}
	if got := tailCells("short", 14); got != "short" {
		t.Errorf("a short path is untouched: %q", got)
	}
	if tailCells("x", 0) != "" {
		t.Error("zero width renders nothing")
	}
}

func TestClockLabelReadsInTheReadersOwnTerms(t *testing.T) {
	restore := time.Local
	time.Local = time.UTC
	t.Cleanup(func() { time.Local = restore })
	now := time.Date(2026, 10, 5, 15, 0, 0, 0, time.UTC)
	at := func(back time.Duration) int64 { return now.Add(-back).UnixNano() }
	cases := []struct {
		back time.Duration
		want string
	}{
		{time.Hour, "today 14:00"},
		{20 * time.Hour, "yesterday 19:00"},
		{3 * 24 * time.Hour, "Fri 15:00"},
		{30 * 24 * time.Hour, "Sep 5"},
	}
	for _, test := range cases {
		if got := clockLabel(at(test.back), now.UnixNano()); got != test.want {
			t.Errorf("clockLabel(-%v) = %q, want %q", test.back, got, test.want)
		}
	}
	if clockLabel(0, now.UnixNano()) != "" {
		t.Error("no activity time, no label")
	}
}

func TestFleetPeaksIgnoreActionRows(t *testing.T) {
	model := deckModel(160, 38)
	prompts, size := model.fleetPeaks()
	if prompts != 410 || size != 5_200_000 {
		t.Fatalf("peaks = %d prompts, %d bytes", prompts, size)
	}
}

func TestDossierOfAnActionRowSaysWhatEnterDoes(t *testing.T) {
	model := deckModel(160, 38)
	banner := ansi.Strip(model.renderDossier(30))
	for _, want := range []string{"guided upgrade", "◖ ⏎ upgrade ◗"} {
		if !strings.Contains(banner, want) {
			t.Errorf("the update banner's pane lacks %q:\n%s", want, banner)
		}
	}
	for _, banned := range []string{"no prompt recorded", "kill", "reboot", "project"} {
		if strings.Contains(banner, banned) {
			t.Errorf("a banner is not a chat; its pane must not say %q:\n%s", banned, banner)
		}
	}
	fresh := ansi.Strip(selectChat(t, model, "New Claude chat").renderDossier(30))
	for _, want := range []string{"starts a new Claude chat", "◖ ⏎ start ◗"} {
		if !strings.Contains(fresh, want) {
			t.Errorf("the new-chat row's pane lacks %q:\n%s", want, fresh)
		}
	}
	failed := compose.Row{Kind: compose.ProfessorUpdateFailed, Name: "update check failed"}
	notice := strings.Join(dossierPurposeLinesText(dossierPurpose(failed), 36), " ")
	if !strings.Contains(notice, "notice, not a chat") {
		t.Errorf("a failed update check says it is a notice: %q", notice)
	}
	if dossierPurpose(compose.Row{Kind: compose.LiveClaude}) != "" {
		t.Error("a chat has no purpose line; it has a prompt")
	}
}

func dossierPurposeLinesText(purpose string, inner int) []string {
	lines := dossierPurposeLines(purpose, inner)
	text := make([]string, len(lines))
	for index, line := range lines {
		text[index] = strings.TrimSpace(ansi.Strip(joinSpans(line)))
	}
	return text
}
