package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/rezzminator/professor/pfm/internal/compose"
	"github.com/rezzminator/professor/pfm/internal/testjail"
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

func TestDossierQuotesTheLastPromptAndSaysWhenThereIsNone(t *testing.T) {
	model := selectChat(t, deckModel(160, 38), "P:BUILDER")
	panel := ansi.Strip(model.renderDossier(30))
	var quoted []string
	for _, line := range strings.Split(panel, "\n") {
		if _, text, ok := strings.Cut(line, "┃"); ok {
			quoted = append(quoted, strings.TrimSpace(text))
		}
	}
	const prompt = "Wire the tempo axis into the list panel and keep the cursor row visible when the viewport scrolls."
	if got := strings.Join(quoted, " "); got != prompt {
		t.Errorf("the prompt is quoted whole behind a rail, got %q:\n%s", got, panel)
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
	if last, want := ansi.Strip(joinSpans(lines[len(lines)-1])), "┃ word word word word word word wor…"; last != want {
		t.Errorf("the cut's last line = %q, want %q", last, want)
	}
}

func TestDossierActionsDimWhatTheRowWouldRefuse(t *testing.T) {
	model := deckModel(160, 38)
	cases := []struct {
		name string
		row  compose.Row
		want [4]bool
	}{
		{
			"live claude",
			compose.Row{Kind: compose.LiveClaude, ID: "x", Socket: "s"},
			[4]bool{true, true, true, true},
		},
		{"live split", compose.Row{Kind: compose.LiveSplit, Socket: "s"}, [4]bool{true, true, false, false}},
		{"resumable", compose.Row{Kind: compose.ResumeClaude, ID: "x"}, [4]bool{true, false, true, false}},
		{"booting", compose.Row{Kind: compose.Booting}, [4]bool{true, false, false, false}},
		{
			"killed by label",
			compose.Row{Kind: compose.ResumeClaude, ID: "x", NameKilled: true},
			[4]bool{true, false, false, false},
		},
		{
			"live without a socket",
			compose.Row{Kind: compose.LiveCodex, ID: "x"},
			[4]bool{true, true, true, false},
		},
		{"update failed", compose.Row{Kind: compose.ProfessorUpdateFailed}, [4]bool{false, false, false, false}},
	}
	for _, test := range cases {
		for index, want := range test.want {
			if got := model.dossierActionAvailable(index, test.row); got != want {
				t.Errorf("%s: action %d available = %v, want %v", test.name, index, got, want)
			}
		}
	}
}

func TestDossierMarksTheArmedActionAndOffersAHiddenChatUnhide(t *testing.T) {
	model := selectChat(t, deckModel(160, 38), "P:BUILDER")
	model.actionIndex = 2
	row, _ := model.selectedRow()
	actions := model.dossierActions(row, 36)
	if len(actions) != len(carouselActions) {
		t.Fatalf("one line per carousel action, got %d", len(actions))
	}
	armed := ansi.Strip(joinSpans(actions[2]))
	if !strings.Contains(armed, "◖") || !strings.Contains(armed, "kill") || !strings.Contains(armed, "◗") {
		t.Errorf("the armed action is boxed: %q", armed)
	}
	if other := ansi.Strip(joinSpans(actions[0])); strings.Contains(other, "◖") {
		t.Errorf("only the armed action is boxed: %q", other)
	}
	row.Killed = true
	if kill := ansi.Strip(joinSpans(model.dossierActions(row, 36)[2])); !strings.Contains(kill, "unhide") {
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
	if got := homeShort("/home/tester/x", testjail.Place("/home/test")); got != "/home/tester/x" {
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
		// 40 hours back is Saturday 23:00 seen on Monday: two midnights ago,
		// so it is not "yesterday" however few whole days have elapsed.
		{40 * time.Hour, "Sat 23:00"},
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

// The dossier's medal strip names the accounts a chat runs on and nothing else:
// the agent, split and here badges are not medals.
func TestRowAccountMedalsCarryOnlyTheRowsAccounts(t *testing.T) {
	agent := compose.Row{Kind: compose.Agent, Account: 2, SplitCount: 3, Here: true, Attached: true, C1H: true}
	if got, want := rowAccountMedals(agent), accountMedal(2); got != want {
		t.Errorf("rowAccountMedals(agent) = %q, want only its account's medal %q", got, want)
	}
	live := compose.Row{Kind: compose.LiveClaude, Accounts: []int{1, 3}, Here: true}
	if got, want := rowAccountMedals(live), accountMedal(1)+" "+accountMedal(3); got != want {
		t.Errorf("rowAccountMedals(live) = %q, want %q", got, want)
	}
	if got := rowAccountMedals(compose.Row{Kind: compose.LiveClaude, Account: 1, LaunchUnread: true}); got != "" {
		t.Errorf("an unread launch shows its warning, not a medal: %q", got)
	}
}

func TestFleetPeaksIgnoreActionRows(t *testing.T) {
	model := deckModel(160, 38)
	prompts, size := model.fleetPeaks()
	if prompts != 410 || size != 5_200_000 {
		t.Fatalf("peaks = %d prompts, %d bytes", prompts, size)
	}
}

func TestDossierRendersEveryRowKind(t *testing.T) {
	var out strings.Builder
	section := func(name string, lines []string) {
		out.WriteString("## " + name + "\n" + strings.Join(lines, "\n") + "\n")
	}
	model := deckModel(160, 38)
	section("update banner", []string{ansi.Strip(model.renderDossier(30))})
	section("new chat", []string{ansi.Strip(selectChat(t, model, "New Claude chat").renderDossier(30))})
	section("single engine", []string{ansi.Strip(NewModel(wizardSnapshot(nil, []int{3}, nil)).renderDossier(30))})
	section(
		"failed update check",
		dossierPurposeLinesText(dossierPurpose(compose.Row{Kind: compose.ProfessorUpdateFailed}), 36),
	)
	section("live chat purpose", []string{dossierPurpose(compose.Row{Kind: compose.LiveClaude})})
	builder := selectChat(t, deckModel(160, 38), "P:BUILDER")
	row, _ := builder.selectedRow()
	for _, height := range []int{40, 9} {
		var lines []string
		for _, line := range builder.dossierLines(row, 36, height) {
			lines = append(lines, ansi.Strip(joinSpans(line)))
		}
		section(fmt.Sprintf("P:BUILDER height %d", height), lines)
	}
	for _, test := range []struct{ note, failure string }{
		{note: "your last message was not answered — re-send it"},
		{failure: "HERDR UNREACHABLE own socket"},
	} {
		var lines []string
		for _, line := range model.dossierDetails(compose.Row{
			Kind: compose.LiveClaude, ID: "sid", Name: "NATIVE", Backend: "herdr", Socket: "native.sock",
			PaneID: "w1:p1", LastPrompt: "a private prompt", LostNote: test.note, BackendError: test.failure,
		}, 100, 0) {
			lines = append(lines, ansi.Strip(joinSpans(line)))
		}
		section("native "+test.note+test.failure, lines)
	}
	empty := deckModel(160, 38)
	empty.rows = nil
	empty.rebuild("", 0)
	section("empty list", []string{ansi.Strip(empty.renderDossier(12))})
	golden(t, "ui_dossier_kinds.txt", out.String())
}

func dossierPurposeLinesText(purpose string, inner int) []string {
	lines := dossierPurposeLines(purpose, inner)
	text := make([]string, len(lines))
	for index, line := range lines {
		text[index] = strings.TrimSpace(ansi.Strip(joinSpans(line)))
	}
	return text
}
