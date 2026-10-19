package herdr

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

// The blocked and error pulse defaults stay in their own red: a hollow or
// lighter glyph for the off frame, never the white square of old.
func TestTabGlyphDefaultsPulseInTheirOwnColour(t *testing.T) {
	for state, want := range map[string][2]string{"blocked": {"🔴", "⭕"}, "error": {"🟥", "❌"}} {
		on := DecoratedTabLabel("Chat", state, "emoji", 0, TabGlyphs{})
		off := DecoratedTabLabel("Chat", state, "emoji", 1, TabGlyphs{})
		if on != want[0]+" Chat" || off != want[1]+" Chat" {
			t.Errorf("%s pulse = %q / %q, want %q / %q", state, on, off, want[0]+" Chat", want[1]+" Chat")
		}
	}
}

// A theme's frames draw every frame of every state; a state it leaves out
// keeps its default, and text mode ignores them all.
func TestTabGlyphsFromTheThemeDrawEveryFrame(t *testing.T) {
	glyphs, err := ParseTabGlyphs(map[string]TabFrames{
		TabGlyphBlocked: {"🟣", "🔘"}, TabGlyphWorking: {"⏳"}, TabGlyphError: {"🟥", "⛔", "💥"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		state string
		frame int
		want  string
	}{
		{"blocked", 0, "🟣 Chat"},
		{"blocked", 1, "🔘 Chat"},
		{"blocked", 2, "🟣 Chat"},
		{"working", 0, "⏳ Chat"},
		{"working", 5, "⏳ Chat"},
		{"error", 1, "⛔ Chat"},
		{"error", 2, "💥 Chat"},
		{"error", 3, "🟥 Chat"},
		{"idle", 0, "🟢 Chat"},
		{"dead", 0, "⬛ Chat"},
		{"mystery", 0, "❔ Chat"},
	} {
		if got := DecoratedTabLabel("Chat", tc.state, "emoji", tc.frame, glyphs); got != tc.want {
			t.Errorf("%s frame %d = %q, want %q", tc.state, tc.frame, got, tc.want)
		}
	}
	if got := DecoratedTabLabel("Chat", "blocked", "text", 1, glyphs); got != "[ ] Chat" {
		t.Errorf("text mode with theme glyphs = %q, want the text marker", got)
	}
	if got := tabFrame("working", "text", 3, glyphs); got != 0 {
		t.Errorf("text mode working plays frame %d, want the still 0", got)
	}
	if got := DecoratedTabLabel("Chat", "blocked", TabStatusOff, 0, glyphs); got != "Chat" {
		t.Errorf("off mode with theme glyphs = %q, want the bare name", got)
	}
}

// The frame is the wall clock's half-second count modulo the frame count, so
// two painters reading the same moment draw the same frame.
func TestTabFrameFollowsTheWallClock(t *testing.T) {
	for _, tc := range []struct {
		millis int64
		want   int
	}{{0, 0}, {499, 0}, {500, 1}, {3_999, 7}, {4_000, 0}, {1_000_000_100, 0}, {1_000_001_600, 3}} {
		if got := tabFrame("working", "emoji", TabTick(tc.millis), TabGlyphs{}); got != tc.want {
			t.Errorf("working at %d ms plays frame %d, want %d", tc.millis, got, tc.want)
		}
	}
}

// A state's frames read from JSON as a string (one still frame) or an array,
// and one frame writes back as a string, so either spelling round-trips equal.
func TestTabFramesReadAStringOrAnArray(t *testing.T) {
	var glyphs TabGlyphs
	if err := json.Unmarshal([]byte(`{"idle":"🌿","working":["🌑","🌕"],"dead":["⬛"]}`), &glyphs); err != nil {
		t.Fatal(err)
	}
	want := TabGlyphs{Idle: TabFrames{"🌿"}, Working: TabFrames{"🌑", "🌕"}, Dead: TabFrames{"⬛"}}
	if !slices.Equal(glyphs.Idle, want.Idle) || !slices.Equal(glyphs.Working, want.Working) ||
		!slices.Equal(glyphs.Dead, want.Dead) {
		t.Fatalf("decoded %+v, want %+v", glyphs, want)
	}
	content, err := json.Marshal(glyphs)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != `{"working":["🌑","🌕"],"idle":"🌿","dead":"⬛"}` {
		t.Errorf("encoded %s", content)
	}
	var empty TabFrames
	if err := json.Unmarshal([]byte(`[]`), &empty); err != nil || empty == nil || len(empty) != 0 {
		t.Errorf("an empty array decodes to %#v, %v: want an empty, non-nil list ValidTabFrames refuses", empty, err)
	}
	for _, bad := range []string{`1`, `{"a":"b"}`, `[1]`} {
		var frames TabFrames
		if err := json.Unmarshal([]byte(bad), &frames); err == nil ||
			!strings.Contains(err.Error(), "a glyph string or an array of glyph frames") {
			t.Errorf("%s decodes to %#v, %v: want the shape named", bad, frames, err)
		}
	}
}

// A glyph of several runes takes its whole width from the base: the label
// still ends at MaxTitleRunes with the glyph intact, and survives the tab
// label cleaning unchanged, so the painter never renames it again.
func TestTabGlyphOfManyRunesKeepsTheLabelInsideTheCap(t *testing.T) {
	for _, glyph := range []string{"👩‍💻", "🏳️‍🌈", "‼️", strings.Repeat("x", MaxTabGlyphRunes)} {
		glyphs, err := ParseTabGlyphs(map[string]TabFrames{TabGlyphBlocked: {glyph}})
		if err != nil {
			t.Fatalf("%q: %v", glyph, err)
		}
		long := DecoratedTabLabel(strings.Repeat("界", 100), "blocked", "emoji", 0, glyphs)
		if utf8.RuneCountInString(long) != MaxTitleRunes || !strings.HasPrefix(long, glyph+" 界") {
			t.Errorf("%q: truncated label = %q (%d runes)", glyph, long, utf8.RuneCountInString(long))
		}
		if TabLabel(long) != long {
			t.Errorf("%q: cleaning rewrites the label %q to %q", glyph, long, TabLabel(long))
		}
	}
}

// Frames of different widths truncate the base against the widest one, so
// the name does not jitter between frames and every frame stays in the cap.
func TestTabFramesOfMixedWidthsKeepOneBase(t *testing.T) {
	frames := TabFrames{"🔴", strings.Repeat("x", MaxTabGlyphRunes), "‼️"}
	glyphs, err := ParseTabGlyphs(map[string]TabFrames{TabGlyphBlocked: frames})
	if err != nil {
		t.Fatal(err)
	}
	var bases []string
	for frame, glyph := range frames {
		label := DecoratedTabLabel(strings.Repeat("界", 100), "blocked", "emoji", frame, glyphs)
		base, found := strings.CutPrefix(label, glyph+" ")
		if !found || utf8.RuneCountInString(label) > MaxTitleRunes {
			t.Fatalf("frame %d: label %q (%d runes)", frame, label, utf8.RuneCountInString(label))
		}
		bases = append(bases, base)
	}
	if bases[0] != bases[1] || bases[1] != bases[2] {
		t.Errorf("the base shifts between frames: %q", bases)
	}
}

// A base whose cut lands on a space never leaves a trailing space, whichever
// frame is drawn: the label Herdr shows after cleaning must equal the one pfm
// rendered, or the next tick reads pfm's own label as the user's and stacks a
// second prefix.
func TestTabLabelCutOnASpaceSurvivesCleaning(t *testing.T) {
	for _, frames := range []TabFrames{
		{"🔴"},
		{strings.Repeat("x", MaxTabGlyphRunes)},
		{"🔴", strings.Repeat("x", MaxTabGlyphRunes), "‼️"},
	} {
		glyphs, err := ParseTabGlyphs(map[string]TabFrames{TabGlyphBlocked: frames})
		if err != nil {
			t.Fatalf("%q: %v", frames, err)
		}
		widest := 0
		for _, frame := range frames {
			widest = max(widest, utf8.RuneCountInString(frame))
		}
		limit := MaxTitleRunes - widest - 1
		base := strings.Repeat("a", limit-1) + " " + strings.Repeat("b", 20)
		for frame := range frames {
			label := DecoratedTabLabel(base, "blocked", "emoji", frame, glyphs)
			if TabLabel(label) != label {
				t.Errorf("%q frame %d: cleaning rewrites the label %q to %q", frames, frame, label, TabLabel(label))
			}
		}
	}
}

func TestParseTabGlyphsRefusesWhatTheLabelCannotCarry(t *testing.T) {
	for name, tc := range map[string]struct {
		values map[string]TabFrames
		want   []string
	}{
		"empty":         {map[string]TabFrames{TabGlyphBlocked: {""}}, []string{`"blocked"`, "frame 0", "empty"}},
		"no frames":     {map[string]TabFrames{TabGlyphWorking: {}}, []string{`"working"`, "no frames"}},
		"over the cap":  {map[string]TabFrames{TabGlyphWorking: slices.Repeat(TabFrames{"🌑"}, 13)}, []string{"13 frames", "12"}},
		"over budget":   {map[string]TabFrames{TabGlyphIdle: {strings.Repeat("🟢", 9)}}, []string{`"idle"`, "9 runes", "budget"}},
		"inner space":   {map[string]TabFrames{TabGlyphDead: {"⬛ ⬛"}}, []string{`"dead"`, "whitespace"}},
		"control":       {map[string]TabFrames{TabGlyphError: {"🟥\x1b"}}, []string{`"error"`, "control"}},
		"invalid utf-8": {map[string]TabFrames{TabGlyphWorking: {"\xff"}}, []string{`"working"`, "UTF-8"}},
		"unknown state": {map[string]TabFrames{"blink": {"⭕"}}, []string{`"blink"`, "not a tab state"}},
		"bad frame":     {map[string]TabFrames{TabGlyphBlocked: {"🔴", "⭕", " "}}, []string{`"blocked"`, "frame 2"}},
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
	if keys := strings.Join(TabGlyphKeys(), ","); keys != "blocked,dead,error,idle,unknown,working" {
		t.Errorf("tab glyph keys = %s", keys)
	}
}

// The painter draws a tab with the tick's theme frame and records it, so a
// name writer's rename between ticks keeps the theme's prefix.
func TestTabStatusPaintsAndRenamesWithTheThemeGlyphs(t *testing.T) {
	f := newTabStatusFixture()
	dir := t.TempDir()
	d := tabObservation("blocked")
	d.Glyphs = TabGlyphs{Blocked: TabFrames{"🟣", "🔘"}}
	for _, phase := range []struct {
		tick int64
		want string
	}{{0, "🟣 Chat"}, {1, "🔘 Chat"}} {
		if _, err := reconcileTab(context.Background(), "endpoint", dir, d, phase.tick, f.call); err != nil {
			t.Fatal(err)
		}
		if f.label != phase.want {
			t.Fatalf("tick %d: tab reads %q, want %q", phase.tick, f.label, phase.want)
		}
	}
	if err := writeSoleTabLabel(
		context.Background(), "endpoint", dir, d.Tab, "Named", true, func() uint64 { return 1 }, f.call,
	); err != nil {
		t.Fatal(err)
	}
	if f.label != "🔘 Named" {
		t.Fatalf("a name writer's rename reads %q, want the theme's off frame kept: %q", f.label, "🔘 Named")
	}
	// A theme switch shows on the next paint.
	d.Name, d.Glyphs = "Named", TabGlyphs{}
	if _, err := reconcileTab(context.Background(), "endpoint", dir, d, 0, f.call); err != nil {
		t.Fatal(err)
	}
	if f.label != "🔴 Named" {
		t.Fatalf("after the theme dropped its glyphs the tab reads %q, want the default %q", f.label, "🔴 Named")
	}
}
