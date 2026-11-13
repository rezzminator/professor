package themesync

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// surfaces.herdr holds the custom tokens and the tabGlyphs object side by
// side; its canonical pins survive a state.json round trip unchanged, so a
// glyph pin never reads as a changed theme on every sync.
func TestHerdrPinsCarryTokensAndTabGlyphsThroughTheStateRoundTrip(t *testing.T) {
	profile, err := ParseProfile([]byte(profileJSON(
		`{"herdr":{"accent":"#FF2E93","tabGlyphs":{"blocked":["🟣","🔘"],"idle":"🌿"}}}`,
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
		TabGlyphs: map[string]herdr.TabFrames{"blocked": {"🟣", "🔘"}, "idle": {"🌿"}},
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
	glyphsOnly, err := canonicalPins(
		&SurfacePins{Herdr: &HerdrPins{TabGlyphs: map[string]herdr.TabFrames{"idle": {"🌿"}}}},
	)
	if err != nil || glyphsOnly == nil || glyphsOnly.Herdr.Tokens != nil {
		t.Fatalf("glyph-only herdr pins = %+v, %v, want the glyphs with no tokens", glyphsOnly, err)
	}
	if block := herdrRender(Applied{Value: "dark", Pins: glyphsOnly}, 1); strings.Contains(block, "tabGlyphs") ||
		strings.Contains(block, "🌿") {
		t.Errorf("the Herdr config block carries the tab glyphs:\n%s", block)
	}
}

func TestParseProfileRefusesABadTabGlyph(t *testing.T) {
	for name, tc := range map[string]struct{ herdr, want string }{
		"empty glyph":      {`{"tabGlyphs":{"blocked":""}}`, "profile Tokyo Night · neon pink: herdr tabGlyphs pin \"blocked\": frame 0: empty glyph"},
		"not an object":    {`{"tabGlyphs":"⭕"}`, "parse profile: herdr tabGlyphs: an object of glyph frames by tab state: json: cannot unmarshal string into Go value of type map[string]herdr.TabFrames"},
		"number glyph":     {`{"tabGlyphs":{"idle":1}}`, "parse profile: herdr tabGlyphs: an object of glyph frames by tab state: a glyph string or an array of glyph frames: json: cannot unmarshal number into Go value of type []string"},
		"number frame":     {`{"tabGlyphs":{"idle":["🟢",1]}}`, "parse profile: herdr tabGlyphs: an object of glyph frames by tab state: a glyph string or an array of glyph frames: json: cannot unmarshal number into .1 of type string"},
		"object token":     {`{"accent":{"fg":"#fff"}}`, "parse profile: herdr pin \"accent\": json: cannot unmarshal object into Go value of type string"},
		"animated blocked": {`{"tabGlyphs":{"blocked":["🔵","⚪"]}}`, ""},
		"animated error":   {`{"tabGlyphs":{"error":["🔵","⚪"]}}`, ""},
	} {
		_, err := ParseProfile([]byte(profileJSON(`{"herdr":` + tc.herdr + `}`)))
		if got := fmt.Sprint(err); err == nil && tc.want != "" || err != nil && got != tc.want {
			t.Errorf("%s: err = %q, want %q", name, got, tc.want)
		}
	}
}

// The painter reads the applied theme's glyphs from state.json on every
// tick: a profile change synced is the next paint's glyphs, and no sync,
// a sync that is off, or no glyph pins is the defaults.
func TestHerdrTabGlyphsFollowTheAppliedTheme(t *testing.T) {
	h := newHarness(t)
	if glyphs, err := HerdrTabGlyphs(h.stateDir); err != nil || !reflect.DeepEqual(glyphs, herdr.TabGlyphs{}) {
		t.Fatalf("before the sync is on: %+v, %v, want the defaults", glyphs, err)
	}
	h.writeProfile(profileJSON(`{"herdr":{"tabGlyphs":{"blocked":["🟣","🔘"]}}}`))
	h.on()
	glyphs, err := HerdrTabGlyphs(h.stateDir)
	if err != nil || !reflect.DeepEqual(glyphs, herdr.TabGlyphs{Blocked: herdr.TabFrames{"🟣", "🔘"}}) {
		t.Fatalf("after on: %+v, %v, want the profile's glyphs", glyphs, err)
	}
	if got := herdr.DecoratedTabLabel("Chat", "blocked", "emoji", 1, glyphs); got != "🔘 Chat" {
		t.Errorf("off phase drawn with the theme = %q", got)
	}

	path := h.writeProfile(profileJSON(`{"herdr":{"tabGlyphs":{"blocked":"⚪"}}}`))
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
	if report := h.sync(false); !report.Changed {
		t.Fatalf("a glyph change is no changed theme: %+v", report)
	}
	glyphs, err = HerdrTabGlyphs(h.stateDir)
	if err != nil || !reflect.DeepEqual(glyphs, herdr.TabGlyphs{Blocked: herdr.TabFrames{"⚪"}}) {
		t.Fatalf("after a glyph change: %+v, %v, want the new glyphs alone", glyphs, err)
	}

	if _, err := Off(context.Background(), h.env); err != nil {
		t.Fatalf("Off: %v", err)
	}
	if glyphs, err := HerdrTabGlyphs(h.stateDir); err != nil || !reflect.DeepEqual(glyphs, herdr.TabGlyphs{}) {
		t.Fatalf("after off: %+v, %v, want the defaults", glyphs, err)
	}
}

func TestHerdrTabGlyphsNamesAnUnreadableState(t *testing.T) {
	bad := `{"enabled":true,"applied":{"value":"custom:x","title":"x","side":"dark",` +
		`"primary":{"state":{}},"pins":{"herdr":{"tabGlyphs":{"blocked":"a b"}}}}}`
	for name, tc := range map[string]struct {
		write func(path string) error
		want  string
	}{
		"unparseable": {func(path string) error { return os.WriteFile(path, []byte("{not json"), 0o600) }, "themesync: parse {dir}/state.json: invalid character 'n' looking for beginning of object key string"},
		"bad glyph":   {func(path string) error { return os.WriteFile(path, []byte(bad), 0o600) }, "themesync: {dir}/state.json: tab glyph \"blocked\": frame 0: glyph \"a b\" holds whitespace or a control character"},
		"fifo":        {func(path string) error { return syscall.Mkfifo(path, 0o600) }, "themesync: {dir}/state.json is not a regular file (p---------)"},
	} {
		dir := t.TempDir()
		if err := tc.write(filepath.Join(dir, StateFileName)); err != nil {
			t.Fatal(err)
		}
		_, err := HerdrTabGlyphs(dir)
		if got := strings.ReplaceAll(fmt.Sprint(err), dir, "{dir}"); got != tc.want {
			t.Errorf("%s: err = %q, want %q", name, got, tc.want)
		}
	}
}

func TestHerdrTabGlyphsUnchangedStateAvoidsBodyWork(t *testing.T) {
	testjail.PauseFlightRecorder(t)
	h := newHarness(t)
	h.writeProfile(profileJSON(`{"herdr":{"tabGlyphs":{"blocked":["X","Y"]}}}`))
	h.on()
	glyphs, err := HerdrTabGlyphs(h.stateDir)
	if err != nil {
		t.Fatal(err)
	}
	glyphs.Blocked[0] = "caller mutation"
	if next, err := HerdrTabGlyphs(h.stateDir); err != nil || next.Blocked[0] != "X" {
		t.Fatalf("caller corrupted cached glyphs: %+v, %v", next, err)
	}
	allocations := testing.AllocsPerRun(10, func() {
		if _, err := HerdrTabGlyphs(h.stateDir); err != nil {
			t.Fatal(err)
		}
	})
	if allocations > 30 {
		t.Errorf("unchanged theme state decoded on paint: %.0f allocations; want <=30", allocations)
	}
}
