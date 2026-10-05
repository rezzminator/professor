package ui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

func TestTempoStarsAndRulerFitEveryWidth(t *testing.T) {
	model := selectChat(t, deckModel(160, 38), "P:AUDIT")
	for width := 1; width <= 200; width++ {
		if got := lipgloss.Width(model.tempoStars(width)); got != width {
			t.Fatalf("tempoStars(%d) is %d cells wide", width, got)
		}
	}
	for width := 3; width <= 200; width++ {
		ruler := model.tempoRuler(width)
		if got := lipgloss.Width(ruler); got != width {
			t.Fatalf("tempoRuler(%d) is %d cells wide: %q", width, got, ansi.Strip(ruler))
		}
		plain := ansi.Strip(ruler)
		if !strings.HasPrefix(plain, "╰") || !strings.HasSuffix(plain, "╯") {
			t.Fatalf("tempoRuler(%d) must keep both corners: %q", width, plain)
		}
	}
	if got := model.tempoStars(0); got != "" {
		t.Errorf("a zero-width axis draws nothing, got %q", got)
	}
	if got := model.tempoRuler(2); got != "" {
		t.Errorf("a ruler with no inner cells draws nothing, got %q", got)
	}
}

func TestTempoRulerLabelsItsTicksAndMarksTheSelectedChat(t *testing.T) {
	model := selectChat(t, deckModel(160, 38), "P:AUDIT")
	plain := ansi.Strip(model.tempoRuler(100))
	for _, label := range []string{"┴7d", "┴1d", "┴1h", "┴10m"} {
		if !strings.Contains(plain, label) {
			t.Errorf("ruler %q lacks the tick %q", plain, label)
		}
	}
	if !strings.Contains(plain, "▲3m") {
		t.Errorf("the selected chat is a triangle with its age beside it, got %q", plain)
	}
	// A chat with no place on the timeline leaves the ruler unmarked.
	first := deckModel(160, 38)
	if row, _ := first.selectedRow(); hasRecency(row) {
		t.Fatalf("fixture drift: the first row is %v", row.Kind)
	}
	if strings.Contains(ansi.Strip(first.tempoRuler(100)), "▲") {
		t.Error("an action row is not on the timeline and must not be marked")
	}
}

func TestTempoStarsMarkTheSelectedChatAtItsColumn(t *testing.T) {
	model := selectChat(t, deckModel(160, 38), "P:AUDIT")
	cells := 100
	row, _ := model.selectedRow()
	column := heatColumn(heatOf(rowAgeNS(row, model.nowNS)), cells)
	plain := []rune(ansi.Strip(model.tempoStars(cells)))
	if plain[column] != '◆' {
		t.Fatalf("the selected chat belongs at column %d, line is %q", column, string(plain))
	}
	if got := strings.Count(string(plain), "◆"); got != 1 {
		t.Errorf("exactly one diamond, got %d", got)
	}
}

func TestTempoBinsCountVisibleChatsAndWeighLiveOnes(t *testing.T) {
	model := deckModel(160, 38)
	bins := model.tempoBins(60)
	total, live := 0, 0
	for index := range bins {
		total += bins[index].count
		live += bins[index].live
	}
	want, wantLive := 0, 0
	for _, rowIndex := range model.filtered {
		row := model.rows[rowIndex]
		if hasRecency(row) {
			want++
			if row.Kind.IsLiveSeat() {
				wantLive++
			}
		}
	}
	if total != want || live != wantLive || want == 0 {
		t.Fatalf("bins hold %d chats (%d live), the list shows %d (%d live)", total, live, want, wantLive)
	}
	var bin tempoBin
	bin.add(pfmengine.Claude, false)
	bin.add(pfmengine.Codex, true)
	if bin.dominant() != pfmengine.Codex {
		t.Errorf("a live chat outweighs a resumable one, dominant = %q", bin.dominant())
	}
	if (&tempoBin{}).dominant() != "" {
		t.Error("an empty bin has no dominant engine")
	}
}

func TestTempoFollowsTheFilter(t *testing.T) {
	model := deckModel(160, 38)
	all := strings.Count(ansi.Strip(model.tempoStars(100)), "•") + strings.Count(ansi.Strip(model.tempoStars(100)), "●")
	model.query.SetValue("harbor")
	model.refilter("", 0)
	few := strings.Count(ansi.Strip(model.tempoStars(100)), "•") + strings.Count(ansi.Strip(model.tempoStars(100)), "●")
	if few >= all || few == 0 {
		t.Fatalf("filtering to one project must thin the axis: %d stars of %d", few, all)
	}
}

func TestTempoStarGrowsWithCount(t *testing.T) {
	want := map[int]string{0: "•", 1: "•", 2: "●", 3: "●", 4: "◉", 9: "◉"}
	for count, glyph := range want {
		if got := tempoStar(count); got != glyph {
			t.Errorf("tempoStar(%d) = %q, want %q", count, got, glyph)
		}
	}
}

func TestMergeSpansFoldsNeighboursOfOneTone(t *testing.T) {
	a, b := tone{fg: "#111111"}, tone{fg: "#222222"}
	merged := mergeSpans([]span{{"x", a}, {"y", a}, {"z", b}, {"w", a}})
	if len(merged) != 3 || merged[0].text != "xy" || merged[1].text != "z" || merged[2].text != "w" {
		t.Fatalf("merged = %#v", merged)
	}
}

func TestTempoIgnoresRowsWithoutAnActivityTime(t *testing.T) {
	model := deckModel(160, 38)
	model.rows = append(model.rows, compose.Row{Kind: compose.ResumeClaude, ID: "x", Name: "ghost", Project: "zzz"})
	model.rebuild("", 0)
	total := 0
	for _, bin := range model.tempoBins(40) {
		total += bin.count
	}
	baseline := 0
	for _, bin := range deckModel(160, 38).tempoBins(40) {
		baseline += bin.count
	}
	if total != baseline {
		t.Fatalf("a chat with no activity time landed on the axis: %d vs %d", total, baseline)
	}
}
