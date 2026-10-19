package themesync

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/herdr"
)

// surfaces.herdr holds the custom tokens and the tabGlyphs object side by
// side; its canonical pins survive a state.json round trip unchanged, so a
// glyph pin never reads as a changed theme on every sync.
func TestHerdrPinsCarryTokensAndTabGlyphsThroughTheStateRoundTrip(t *testing.T) {
	profile, err := ParseProfile([]byte(profileJSON(
		`{"herdr":{"accent":"#FF2E93","tabGlyphs":{"blocked":"🟣","blockedOff":"🔘"}}}`,
	)))
	if err != nil {
		t.Fatal(err)
	}
	pins, err := canonicalPins(profile.Surfaces)
	if err != nil {
		t.Fatal(err)
	}
	want := &HerdrPins{
		Tokens:    map[string]string{"accent": "#ff2e93"},
		TabGlyphs: map[string]string{"blocked": "🟣", "blockedOff": "🔘"},
	}
	if pins == nil || !reflect.DeepEqual(pins.Herdr, want) {
		t.Fatalf("canonical herdr pins = %+v, want %+v", pins, want)
	}
	content, err := json.Marshal(Applied{Value: "custom:x", Pins: pins})
	if err != nil {
		t.Fatal(err)
	}
	var back Applied
	if err := json.Unmarshal(content, &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back.Pins, pins) {
		t.Errorf("state round trip = %+v, want %+v (%s)", back.Pins, pins, content)
	}
	glyphsOnly, err := canonicalPins(&SurfacePins{Herdr: &HerdrPins{TabGlyphs: map[string]string{"idle": "🌿"}}})
	if err != nil || glyphsOnly == nil || glyphsOnly.Herdr.Tokens != nil {
		t.Fatalf("glyph-only herdr pins = %+v, %v, want the glyphs with no tokens", glyphsOnly, err)
	}
	if block := herdrRender(Applied{Value: "dark", Pins: glyphsOnly}, 1); strings.Contains(block, "tabGlyphs") ||
		strings.Contains(block, "🌿") {
		t.Errorf("the Herdr config block carries the tab glyphs:\n%s", block)
	}
}

// `pfm theme check` runs ParseProfile: a glyph the tab label cannot carry, an
// unknown tab state or a malformed tabGlyphs refuses the profile, naming it.
func TestParseProfileRefusesABadTabGlyph(t *testing.T) {
	// A malformed shape fails the decode, before the profile has a name; a
	// bad value fails the pin check, which names the profile too.
	for name, tc := range map[string]struct {
		herdr, want string
		named       bool
	}{
		"empty glyph":   {`{"tabGlyphs":{"blocked":""}}`, `herdr tabGlyphs pin "blocked": empty`, true},
		"over budget":   {`{"tabGlyphs":{"blockedOff":"⭕⭕⭕⭕⭕⭕⭕⭕⭕"}}`, `"blockedOff": glyph`, true},
		"whitespace":    {`{"tabGlyphs":{"error":"🟥 x"}}`, "whitespace", true},
		"unknown state": {`{"tabGlyphs":{"blink":"⭕"}}`, `"blink": herdr tabGlyphs renders no such key`, true},
		"not an object": {`{"tabGlyphs":"⭕"}`, "herdr tabGlyphs: an object of glyphs by tab state", false},
		"number glyph":  {`{"tabGlyphs":{"idle":1}}`, "herdr tabGlyphs: an object of glyphs by tab state", false},
		"object token":  {`{"accent":{"fg":"#fff"}}`, `herdr pin "accent"`, false},
	} {
		_, err := ParseProfile([]byte(profileJSON(`{"herdr":` + tc.herdr + `}`)))
		if err == nil || !strings.Contains(err.Error(), tc.want) ||
			tc.named && !strings.Contains(err.Error(), "profile Hibiscus Night") {
			t.Errorf("%s: err = %v, want one naming %q (and the profile: %t)", name, err, tc.want, tc.named)
		}
	}
}

// The painter reads the applied theme's glyphs from state.json on every
// tick: a profile change synced is the next paint's glyphs, and no sync,
// a sync that is off, or no glyph pins is the defaults.
func TestHerdrTabGlyphsFollowTheAppliedTheme(t *testing.T) {
	h := newHarness(t)
	if glyphs, err := HerdrTabGlyphs(h.stateDir); err != nil || glyphs != (herdr.TabGlyphs{}) {
		t.Fatalf("before the sync is on: %+v, %v, want the defaults", glyphs, err)
	}
	h.writeProfile(profileJSON(`{"herdr":{"tabGlyphs":{"blocked":"🟣","blockedOff":"🔘"}}}`))
	h.on()
	glyphs, err := HerdrTabGlyphs(h.stateDir)
	if err != nil || glyphs != (herdr.TabGlyphs{Blocked: "🟣", BlockedOff: "🔘"}) {
		t.Fatalf("after on: %+v, %v, want the profile's glyphs", glyphs, err)
	}
	if got := herdr.DecoratedTabLabel("Chat", "blocked", "emoji", false, glyphs); got != "🔘 Chat" {
		t.Errorf("off phase drawn with the theme = %q", got)
	}

	path := h.writeProfile(profileJSON(`{"herdr":{"tabGlyphs":{"blockedOff":"⚪"}}}`))
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
	if report := h.sync(false); !report.Changed {
		t.Fatalf("a glyph change is no changed theme: %+v", report)
	}
	if glyphs, err := HerdrTabGlyphs(h.stateDir); err != nil || glyphs != (herdr.TabGlyphs{BlockedOff: "⚪"}) {
		t.Fatalf("after a glyph change: %+v, %v, want the new glyphs alone", glyphs, err)
	}

	if _, err := Off(context.Background(), h.env); err != nil {
		t.Fatalf("Off: %v", err)
	}
	if glyphs, err := HerdrTabGlyphs(h.stateDir); err != nil || glyphs != (herdr.TabGlyphs{}) {
		t.Fatalf("after off: %+v, %v, want the defaults", glyphs, err)
	}
}

// An unreadable state file is an error naming it, never the defaults.
func TestHerdrTabGlyphsNamesAnUnreadableState(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, StateFileName), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := HerdrTabGlyphs(dir); err == nil || !strings.Contains(err.Error(), StateFileName) {
		t.Fatalf("an unparseable state = %v, want an error naming %s", err, StateFileName)
	}
	bad := `{"enabled":true,"applied":{"value":"custom:x","title":"x","side":"dark",` +
		`"primary":{"state":{}},"pins":{"herdr":{"tabGlyphs":{"blocked":"a b"}}}}}`
	if err := os.WriteFile(filepath.Join(dir, StateFileName), []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := HerdrTabGlyphs(dir); err == nil || !strings.Contains(err.Error(), `"blocked"`) {
		t.Fatalf("a state holding a bad glyph = %v, want an error naming the key", err)
	}
}

// A FIFO in place of state.json is an error, never a read that blocks every
// tab's status on the painter's tick.
func TestHerdrTabGlyphsRefusesANonRegularState(t *testing.T) {
	dir := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(dir, StateFileName), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := HerdrTabGlyphs(dir); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("a FIFO state = %v, want a not-a-regular-file error", err)
	}
}
