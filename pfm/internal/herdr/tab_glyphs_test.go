package herdr

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"
)

// The blocked and error pulse defaults stay in their own red: a hollow or
// lighter glyph for the off phase, never the white square of old.
func TestTabGlyphDefaultsPulseInTheirOwnColour(t *testing.T) {
	for state, want := range map[string][2]string{"blocked": {"🔴", "⭕"}, "error": {"🟥", "❌"}} {
		on := DecoratedTabLabel("Chat", state, "emoji", true, TabGlyphs{})
		off := DecoratedTabLabel("Chat", state, "emoji", false, TabGlyphs{})
		if on != want[0]+" Chat" || off != want[1]+" Chat" {
			t.Errorf("%s pulse = %q / %q, want %q / %q", state, on, off, want[0]+" Chat", want[1]+" Chat")
		}
	}
}

// A theme's glyphs draw both phases of the pulse and every steady state; a
// glyph it leaves out keeps its default, and text mode ignores them all.
func TestTabGlyphsFromTheThemeDrawEveryPhase(t *testing.T) {
	glyphs, err := ParseTabGlyphs(map[string]string{
		TabGlyphBlocked: "🟣", TabGlyphBlockedOff: "🔘", TabGlyphWorking: "⏳", TabGlyphErrorOff: "⛔",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		state string
		on    bool
		want  string
	}{
		{"blocked", true, "🟣 Chat"},
		{"blocked", false, "🔘 Chat"},
		{"working", true, "⏳ Chat"},
		{"working", false, "⏳ Chat"},
		{"error", true, "🟥 Chat"},
		{"error", false, "⛔ Chat"},
		{"idle", true, "🟢 Chat"},
		{"dead", true, "⬛ Chat"},
		{"mystery", true, "❔ Chat"},
	} {
		if got := DecoratedTabLabel("Chat", tc.state, "emoji", tc.on, glyphs); got != tc.want {
			t.Errorf("%s on=%t = %q, want %q", tc.state, tc.on, got, tc.want)
		}
	}
	if got := DecoratedTabLabel("Chat", "blocked", "text", false, glyphs); got != "[ ] Chat" {
		t.Errorf("text mode with theme glyphs = %q, want the text marker", got)
	}
	if got := DecoratedTabLabel("Chat", "blocked", TabStatusOff, true, glyphs); got != "Chat" {
		t.Errorf("off mode with theme glyphs = %q, want the bare name", got)
	}
}

// A glyph of several runes takes its whole width from the base: the label
// still ends at MaxTitleRunes with the glyph intact, and survives the tab
// label cleaning unchanged, so the painter never renames it again.
func TestTabGlyphOfManyRunesKeepsTheLabelInsideTheCap(t *testing.T) {
	for _, glyph := range []string{"👩‍💻", "🏳️‍🌈", "‼️", strings.Repeat("x", MaxTabGlyphRunes)} {
		glyphs, err := ParseTabGlyphs(map[string]string{TabGlyphBlocked: glyph})
		if err != nil {
			t.Fatalf("%q: %v", glyph, err)
		}
		long := DecoratedTabLabel(strings.Repeat("界", 100), "blocked", "emoji", true, glyphs)
		if utf8.RuneCountInString(long) != MaxTitleRunes || !strings.HasPrefix(long, glyph+" 界") {
			t.Errorf("%q: truncated label = %q (%d runes)", glyph, long, utf8.RuneCountInString(long))
		}
		if TabLabel(long) != long {
			t.Errorf("%q: cleaning rewrites the label %q to %q", glyph, long, TabLabel(long))
		}
	}
}

// A base whose cut lands on a space never leaves a trailing space: the label
// Herdr shows after cleaning must equal the one pfm rendered, or the next
// tick reads pfm's own label as the user's and stacks a second prefix.
func TestTabLabelCutOnASpaceSurvivesCleaning(t *testing.T) {
	for _, glyph := range []string{"🔴", strings.Repeat("x", MaxTabGlyphRunes)} {
		glyphs, err := ParseTabGlyphs(map[string]string{TabGlyphBlocked: glyph})
		if err != nil {
			t.Fatalf("%q: %v", glyph, err)
		}
		limit := MaxTitleRunes - utf8.RuneCountInString(glyph+" ")
		base := strings.Repeat("a", limit-1) + " " + strings.Repeat("b", 20)
		label := DecoratedTabLabel(base, "blocked", "emoji", true, glyphs)
		if TabLabel(label) != label {
			t.Errorf("%q: cleaning rewrites the label %q to %q", glyph, label, TabLabel(label))
		}
	}
}

func TestParseTabGlyphsRefusesWhatTheLabelCannotCarry(t *testing.T) {
	for name, tc := range map[string]struct {
		values map[string]string
		want   []string
	}{
		"empty":          {map[string]string{TabGlyphBlocked: ""}, []string{`"blocked"`, "empty"}},
		"over budget":    {map[string]string{TabGlyphIdle: strings.Repeat("🟢", 9)}, []string{`"idle"`, "9 runes", "budget"}},
		"inner space":    {map[string]string{TabGlyphDead: "⬛ ⬛"}, []string{`"dead"`, "whitespace"}},
		"control":        {map[string]string{TabGlyphError: "🟥\x1b"}, []string{`"error"`, "control"}},
		"invalid utf-8":  {map[string]string{TabGlyphWorking: "\xff"}, []string{`"working"`, "UTF-8"}},
		"unknown state":  {map[string]string{"blink": "⭕"}, []string{`"blink"`, "not a tab state"}},
		"one bad of two": {map[string]string{TabGlyphBlocked: "🔴", TabGlyphBlockedOff: " "}, []string{`"blockedOff"`}},
	} {
		glyphs, err := ParseTabGlyphs(tc.values)
		if err == nil {
			t.Errorf("%s: accepted as %+v", name, glyphs)
			continue
		}
		for _, want := range tc.want {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: error %q lacks %q", name, err, want)
			}
		}
	}
	if keys := strings.Join(TabGlyphKeys(), ","); keys !=
		"blocked,blockedOff,dead,error,errorOff,idle,unknown,working" {
		t.Errorf("tab glyph keys = %s", keys)
	}
}

// The painter draws a tab with the tick's theme glyphs and records them, so a
// name writer's rename between ticks keeps the theme's prefix.
func TestTabStatusPaintsAndRenamesWithTheThemeGlyphs(t *testing.T) {
	f := newTabStatusFixture()
	dir := t.TempDir()
	d := tabObservation("blocked")
	d.Glyphs = TabGlyphs{Blocked: "🟣", BlockedOff: "🔘"}
	for _, phase := range []struct {
		on   bool
		want string
	}{{true, "🟣 Chat"}, {false, "🔘 Chat"}} {
		if _, err := reconcileTab(context.Background(), "endpoint", dir, d, phase.on, f.call); err != nil {
			t.Fatal(err)
		}
		if f.label != phase.want {
			t.Fatalf("on=%t: tab reads %q, want %q", phase.on, f.label, phase.want)
		}
	}
	if err := writeSoleTabLabel(
		context.Background(), "endpoint", dir, d.Tab, "Named", true, func() uint64 { return 1 }, f.call,
	); err != nil {
		t.Fatal(err)
	}
	if f.label != "🔘 Named" {
		t.Fatalf("a name writer's rename reads %q, want the theme's off glyph kept: %q", f.label, "🔘 Named")
	}
	// A theme switch shows on the next paint.
	d.Name, d.Glyphs = "Named", TabGlyphs{}
	if _, err := reconcileTab(context.Background(), "endpoint", dir, d, true, f.call); err != nil {
		t.Fatal(err)
	}
	if f.label != "🔴 Named" {
		t.Fatalf("after the theme dropped its glyphs the tab reads %q, want the default %q", f.label, "🔴 Named")
	}
}
