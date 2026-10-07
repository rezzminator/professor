package ui

import (
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/rezzminator/professor/pfm/internal/compose"
)

func TestHeatOfIsOneWhenHotZeroWhenColdAndFallsMonotonically(t *testing.T) {
	if got := heatOf(0); got != 1 {
		t.Errorf("heatOf(0) = %v, want 1", got)
	}
	if got := heatOf(int64(heatHot)); got != 1 {
		t.Errorf("heatOf(heatHot) = %v, want 1", got)
	}
	if got := heatOf(int64(heatCold)); got != 0 {
		t.Errorf("heatOf(heatCold) = %v, want 0", got)
	}
	if got := heatOf(int64(400 * 24 * time.Hour)); got != 0 {
		t.Errorf("heatOf(400d) = %v, want 0", got)
	}
	previous := 1.0
	for age := time.Minute; age < heatCold; age = age * 5 / 4 {
		heat := heatOf(int64(age))
		if heat > previous || heat < 0 || heat > 1 {
			t.Fatalf("heatOf(%v) = %v after %v: want a monotone fall inside [0,1]", age, heat, previous)
		}
		previous = heat
	}
	if hour := heatOf(int64(time.Hour)); hour < 0.5 || hour > 0.65 {
		t.Errorf("heatOf(1h) = %v: the log scale should leave an hour more than half warm", hour)
	}
}

func TestRowAgeNSPrefersTheActivityClockAndNeverGoesNegative(t *testing.T) {
	now := int64(1_000_000_000_000)
	cases := []struct {
		name string
		row  compose.Row
		now  int64
		want int64
	}{
		{"activity clock wins", compose.Row{ActivityNS: now - 5_000, AgeNS: 99}, now, 5_000},
		{"falls back to the row's own age", compose.Row{AgeNS: 77}, now, 77},
		{"no clock yet uses the row's age", compose.Row{ActivityNS: 10, AgeNS: 42}, 0, 42},
		{"a clock behind the activity clamps to zero", compose.Row{ActivityNS: now + 9_000}, now, 0},
	}
	for _, test := range cases {
		if got := rowAgeNS(test.row, test.now); got != test.want {
			t.Errorf("%s: rowAgeNS = %d, want %d", test.name, got, test.want)
		}
	}
}

func TestHasRecencyExcludesActionRows(t *testing.T) {
	for _, kind := range []compose.Kind{
		compose.NewClaude, compose.NewCodex, compose.NewOpenCode,
		compose.ProfessorUpdate, compose.ProfessorUpdateFailed,
	} {
		if hasRecency(compose.Row{Kind: kind, ActivityNS: 5}) {
			t.Errorf("%v is an action row and must sit outside the timeline", kind)
		}
	}
	if !hasRecency(compose.Row{Kind: compose.LiveClaude, ActivityNS: 5}) {
		t.Error("a live chat with an activity time belongs on the timeline")
	}
	if hasRecency(compose.Row{Kind: compose.ResumeClaude}) {
		t.Error("a chat with no activity time has no place on the timeline")
	}
}

func TestHeatColumnStaysOnTheAxis(t *testing.T) {
	for cells := 1; cells <= 60; cells++ {
		for _, heat := range []float64{-1, 0, 0.25, 0.5, 0.999, 1, 5} {
			column := heatColumn(heat, cells)
			if column < 0 || column >= cells {
				t.Fatalf("heatColumn(%v, %d) = %d: off the axis", heat, cells, column)
			}
		}
		if cells > 1 {
			if heatColumn(0, cells) != 0 || heatColumn(1, cells) != cells-1 {
				t.Fatalf("cells=%d: stale must sit at the left edge and fresh at the right", cells)
			}
		}
	}
}

func TestHeatRulerIsAlwaysEightCellsAndFillsWithHeat(t *testing.T) {
	previousLit := -1
	for _, heat := range []float64{0, 0.01, 0.2, 0.5, 0.8, 1} {
		ruler := heatRuler(heat, "#ff9e64", "#475569", "#64748b", "")
		if got := lipgloss.Width(ruler); got != heatRulerCells {
			t.Fatalf("heat %v: ruler is %d cells wide, want %d", heat, got, heatRulerCells)
		}
		lit := strings.Count(ansi.Strip(ruler), "▰")
		if lit < previousLit {
			t.Fatalf("heat %v lights %d cells, fewer than a cooler row's %d", heat, lit, previousLit)
		}
		previousLit = lit
	}
	if got := strings.Count(ansi.Strip(heatRuler(0, "#fff", "#000", "#888", "")), "▰"); got != 0 {
		t.Errorf("a month-old chat keeps no lit cell, got %d", got)
	}
	if got := strings.Count(ansi.Strip(heatRuler(0.001, "#fff", "#000", "#888", "")), "▰"); got != 1 {
		t.Errorf("a barely-warm chat keeps one lit cell, got %d", got)
	}
	if got := strings.Count(ansi.Strip(heatRuler(1, "#fff", "#000", "#888", "")), "▰"); got != heatRulerCells {
		t.Errorf("a chat active just now fills the ruler, got %d", got)
	}
}

func TestHeatShadeRunsFromColdToHot(t *testing.T) {
	if got := heatShade("#ff0000", "#000000", 1); got != "#ff0000" {
		t.Errorf("full heat = %s, want the engine colour", got)
	}
	if got := heatShade("#ff0000", "#000000", 0); got != "#000000" {
		t.Errorf("no heat = %s, want the cold colour", got)
	}
}
