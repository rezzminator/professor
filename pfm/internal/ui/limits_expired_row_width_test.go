package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	pfmstats "github.com/rezzminator/professor/pfm/internal/stats"
)

// TestExpiredLimitsRowStaysBoundedAtEveryWidth keeps the 2026-09-11 expiry
// note ("reset passed · awaiting refetch") inside the card at every rendered
// width — it is the longest reset note the Limits tab can carry.
func TestExpiredLimitsRowStaysBoundedAtEveryWidth(t *testing.T) {
	model := NewModel(fixtureSnapshot(80))
	model.tab = TabLimits
	now := time.Unix(0, model.nowNS)
	model.stats = pfmstats.Snapshot{Limits: []pfmstats.AccountLimits{{
		Account: 1, Emoji: "🥇", Engine: pfmengine.Claude,
		Windows: []pfmstats.Window{
			{Name: "5h", UsedPct: pfmstats.UnknownUsedPct, ResetNote: "reset passed · awaiting refetch"},
			{Name: "7d", UsedPct: 61, ResetAt: now.Add(3 * 24 * time.Hour)},
		},
	}}}
	for _, width := range []int{40, 60, 80, 120} {
		plain := ansi.Strip(model.renderLimitsPanel(width, 12))
		for _, line := range strings.Split(plain, "\n") {
			if got := ansi.StringWidth(line); got > width {
				t.Fatalf("width=%d produced a %d-wide line: %q", width, got, line)
			}
		}
	}
}
