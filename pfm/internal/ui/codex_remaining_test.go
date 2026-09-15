package ui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	pfmengine "hostops/pfm/internal/engine"
	pfmstats "hostops/pfm/internal/stats"
)

// TestCodexLimitsShareTheClaudeScale pins one scale for the whole Limits
// table: every row reads "% used" and fills its bar by usage, Codex included.
// A Codex row reading "24% left" beside a Claude row reading "24% used" drew
// the same short bar for opposite meanings (2026-09-11).
func TestCodexLimitsShareTheClaudeScale(t *testing.T) {
	for _, width := range []int{38, 78, 118} {
		for _, used := range []float64{0, 61, 76, 100} {
			t.Run(fmt.Sprintf("width%d_used%.0f", width, used), func(t *testing.T) {
				model := NewModel(fixtureSnapshot(120))
				model.stats = pfmstats.Snapshot{Limits: []pfmstats.AccountLimits{
					{
						Account: 1,
						Engine:  pfmengine.Claude,
						Label:   "account 1",
						Windows: []pfmstats.Window{{Name: "7d", UsedPct: used}},
					},
					{
						Engine:  pfmengine.Codex,
						Label:   "Codex 1",
						Windows: []pfmstats.Window{{Name: "7d", UsedPct: used}},
					},
				}}
				lines := model.renderLimitCards(width)
				plain := ansi.Strip(strings.Join(lines, "\n"))
				want := fmt.Sprintf("%.0f%% used", used)
				if strings.Count(plain, want) != 2 || strings.Contains(plain, "% left") ||
					strings.Contains(plain, "5h") {
					t.Fatalf("Codex and Claude rows must both read %q on one scale:\n%s", want, plain)
				}
				var bars []string
				for _, line := range lines {
					stripped := ansi.Strip(line)
					if strings.Contains(stripped, "[") {
						start, end := strings.Index(stripped, "["), strings.LastIndex(stripped, "]")
						bars = append(bars, stripped[start:end+len("]")])
					}
				}
				if len(bars) != 2 || bars[0] != bars[1] {
					t.Fatalf("Codex bar must fill by usage exactly like the Claude bar at %.0f%%: %q", used, bars)
				}
				if used == 100 && !strings.Contains(bars[1], "FULL") {
					t.Fatalf("exhausted Codex quota must read FULL like an exhausted Claude window: %q", bars[1])
				}
			})
		}
	}
}
