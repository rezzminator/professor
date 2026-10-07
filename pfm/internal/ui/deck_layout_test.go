package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// The deck's one invariant: whatever the terminal, the frame is exactly
// max(40, width) cells wide on every line and max(12, height) lines tall. A line
// a cell too wide wraps the terminal and tears everything under it.
func TestDeckFrameKeepsItsSizeAtEveryTerminalSize(t *testing.T) {
	states := []struct {
		name  string
		setup func(*testing.T, Model) Model
	}{
		{"top", func(_ *testing.T, model Model) Model { return model }},
		{"live", func(t *testing.T, model Model) Model { return selectChat(t, model, "P:BUILDER") }},
		{"resumable", func(t *testing.T, model Model) Model { return selectChat(t, model, "schema migration plan") }},
		{"query", func(t *testing.T, model Model) Model {
			for _, runeValue := range "atl bld" {
				model, _ = applyKey(t, model, printableKey(runeValue))
			}
			return model
		}},
		{"nomatch", func(t *testing.T, model Model) Model {
			for _, runeValue := range "zzzq" {
				model, _ = applyKey(t, model, printableKey(runeValue))
			}
			return model
		}},
		{"receipt", func(t *testing.T, model Model) Model {
			model = selectChat(t, model, "P:AUDIT")
			model, _ = applyKey(t, model, controlKey('x'))
			return model
		}},
	}
	for _, state := range states {
		// 111 and 112 straddle the width where the dossier appears.
		for _, width := range []int{40, 41, 57, 80, 95, 96, 111, 112, 140, 200} {
			for _, height := range []int{12, 13, 24, 40} {
				model := state.setup(t, deckModel(width, height))
				model.nowNS = fixtureNowNS
				frame := model.View().Content
				lines := strings.Split(frame, "\n")
				if want := max(12, height); len(lines) != want {
					t.Fatalf("%s %dx%d: %d lines, want %d", state.name, width, height, len(lines), want)
				}
				for number, line := range lines {
					if got := ansi.StringWidth(line); got != width {
						t.Fatalf("%s %dx%d: line %d spans %d cells: %q",
							state.name, width, height, number, got, ansi.Strip(line))
					}
				}
			}
		}
	}
}

func TestDeckFrameHoldsEveryTabAtTheFloorAndAWideSize(t *testing.T) {
	for _, tab := range []Tab{TabChats, TabStats, TabLimits, TabCosmos} {
		for _, size := range [][2]int{{40, 12}, {80, 24}, {120, 30}, {200, 50}} {
			model := deckModel(size[0], size[1])
			model.tab = tab
			model.nowNS = fixtureNowNS
			lines := strings.Split(model.View().Content, "\n")
			if len(lines) != size[1] {
				t.Errorf("tab %d at %dx%d has %d lines", tab, size[0], size[1], len(lines))
			}
			for number, line := range lines {
				if got := ansi.StringWidth(line); got != size[0] {
					t.Errorf("tab %d at %dx%d: line %d spans %d cells", tab, size[0], size[1], number, got)
					break
				}
			}
		}
	}
}
