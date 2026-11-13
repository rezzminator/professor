package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	pfmstats "github.com/rezzminator/professor/pfm/internal/stats"
)

// TestNoWindowCardIsNeutralAndFailureKeepsWarning pins that the informational
// "no limit window" status carries no warning glyph while a real failure does.
func TestNoWindowCardIsNeutralAndFailureKeepsWarning(t *testing.T) {
	model := NewModel(fixtureSnapshot(80))
	model.tab = TabLimits
	model.stats = pfmstats.Snapshot{Limits: []pfmstats.AccountLimits{
		{Account: 1, Engine: pfmengine.Claude, Status: "no limit window on this plan", NoWindow: true},
		{Account: 2, Engine: pfmengine.Claude, Status: "account 2 limits unavailable: boom"},
	}}
	plain := ansi.Strip(model.renderLimitsPanel(80, 12))
	for _, line := range strings.Split(plain, "\n") {
		switch {
		case strings.Contains(line, "no limit window"):
			if line != "│  no limit window on this plan                                                │" {
				t.Fatalf("no-window line has a warning glyph: %q", line)
			}
		case strings.Contains(line, "boom"):
			if line != "│  ⚠ account 2 limits unavailable: boom                                        │" {
				t.Fatalf("failure line lost its warning glyph: %q", line)
			}
		}
	}
	if !strings.Contains(plain, "no limit window") || !strings.Contains(plain, "boom") {
		t.Fatalf("missing lines:\n%s", plain)
	}
}
