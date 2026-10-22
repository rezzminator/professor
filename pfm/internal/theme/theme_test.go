package theme

import (
	"fmt"
	"reflect"
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/naming"
	"github.com/rezzminator/professor/pfm/internal/themesync"
)

func TestVariantMappingUsesOnlyDerivedColoursAndFillsEveryRole(t *testing.T) {
	v := themesync.Variant{
		Background: "#101020", Foreground: "#eeeef0", SelectionForeground: "#eeeeff", SelectionBackground: "#404050",
		Surface: "#202030", Overlay: "#303040", Accent: "#b080e0", Orange: "#e09060", Muted: "#9090a0",
		State: themesync.StateColors{Working: "#80b0e0", Done: "#80c090", Blocked: "#d0b070"},
	}
	colours := map[string]bool{}
	for _, value := range []string{
		v.Background, v.Foreground, v.SelectionForeground, v.SelectionBackground,
		v.Surface, v.Overlay, v.Accent, v.Orange, v.Muted, v.State.Working, v.State.Done, v.State.Blocked,
	} {
		colours[value] = true
	}
	for i := range v.ANSI {
		v.ANSI[i] = fmt.Sprintf("#%02x1020", i)
		colours[v.ANSI[i]] = true
	}
	palette := FromVariant(v)
	value := reflect.ValueOf(palette)
	for i := 0; i < value.NumField(); i++ {
		field := value.Field(i)
		if field.Kind() == reflect.String && !colours[field.String()] {
			t.Errorf("%s = %q, want a carried Variant colour", value.Type().Field(i).Name, field.String())
		}
	}
	for _, row := range []map[pfmengine.ID]string{palette.EngineRow, palette.StatsEngine} {
		for _, id := range pfmengine.All() {
			if !colours[row[id]] {
				t.Errorf("engine %s = %q, want a carried Variant colour", id, row[id])
			}
		}
	}
	if palette.EngineRow[pfmengine.Claude] != v.Orange || palette.EngineRow[pfmengine.Codex] != v.State.Working ||
		palette.EngineRow[pfmengine.OpenCode] != v.State.Done || palette.SelectedForeground != v.SelectionForeground {
		t.Fatal("engine identity or selected text lost its semantic colour")
	}
}

func TestLoadEmbeddedPalettes(t *testing.T) {
	defaultPalette := Load("default")
	if defaultPalette.EngineRow[pfmengine.Codex] != "#38bdf8" || defaultPalette.AgentRow != "#fb923c" ||
		defaultPalette.StatsEngine[pfmengine.Claude] != "#ff9e64" ||
		defaultPalette.StatsCPU != "#4ade80" {
		t.Fatalf("default palette = %#v", defaultPalette)
	}
	if tokyo := Load("tokyo-night"); reflect.DeepEqual(tokyo, defaultPalette) {
		t.Fatal("tokyo-night palette unexpectedly equals default")
	}
}

func TestEveryEngineHasThemeColours(t *testing.T) {
	for _, paletteName := range []string{"default", "tokyo-night"} {
		palette := Load(paletteName)
		for _, id := range pfmengine.All() {
			if palette.EngineRow[id] == "" {
				t.Errorf("%s engine %s has no row colour", paletteName, id)
			}
			if palette.StatsEngine[id] == "" {
				t.Errorf("%s engine %s has no stats colour", paletteName, id)
			}
		}
	}
}

// The picker's recency ramp blends toward HeatCold; an unset one would blend
// toward an empty string and paint every stale row in a broken colour.
func TestEveryPaletteHasAHeatColdEnd(t *testing.T) {
	for _, paletteName := range []string{"default", "tokyo-night"} {
		palette := Load(paletteName)
		if len(palette.HeatCold) != 7 || palette.HeatCold[0] != '#' {
			t.Errorf("%s HeatCold = %q, want a #rrggbb colour", paletteName, palette.HeatCold)
		}
		if palette.HeatCold == palette.Header {
			t.Errorf("%s HeatCold equals the header colour, so the ramp has no cold end", paletteName)
		}
	}
}

func TestUnknownPaletteFallsBackToDefault(t *testing.T) {
	if got, want := Load("not-a-palette"), Load("default"); !reflect.DeepEqual(got, want) {
		t.Fatalf("unknown palette = %#v, want default %#v", got, want)
	}
}

func TestConfiguredEmojiAndLegacyMedalsAreRecognized(t *testing.T) {
	if !naming.ContainsMedalFor("🔖 custom", []string{"custom"}) {
		t.Fatal("configured emoji was not recognized")
	}
	if !naming.ContainsMedalFor("🔖 🥉 legacy", nil) {
		t.Fatal("legacy medal was not recognized")
	}
}

// TestEngineIdentityHuesHoldAcrossPalettes pins the identity the palettes now
// promise: Claude orange, Codex white, OpenCode green, in every theme. It reads
// the hue, not the exact shade, so a theme stays free to restyle.
func TestEngineIdentityHuesHoldAcrossPalettes(t *testing.T) {
	wantHue := map[pfmengine.ID]string{
		pfmengine.Claude: "orange", pfmengine.Codex: "blue", pfmengine.OpenCode: "green",
	}
	for _, paletteName := range []string{"default", "tokyo-night"} {
		palette := Load(paletteName)
		for _, row := range []struct {
			label  string
			colors map[pfmengine.ID]string
		}{{"row", palette.EngineRow}, {"stats", palette.StatsEngine}} {
			for id, want := range wantHue {
				if got := hueOf(row.colors[id]); got != want {
					t.Errorf("%s %s %s colour %s is %s, want %s",
						paletteName, row.label, id, row.colors[id], got, want)
				}
			}
		}
	}
}

// TestEngineRowsNeverShareAHexWithAgentRows guards the exact collision the
// identity re-colour introduced: an agent sub-row rendered in the same hex as
// the engine row above it is unreadable, and a palette edit is where that
// happens silently.
func TestEngineRowsNeverShareAHexWithAgentRows(t *testing.T) {
	for _, paletteName := range []string{"default", "tokyo-night"} {
		palette := Load(paletteName)
		for _, id := range pfmengine.All() {
			if palette.EngineRow[id] == palette.AgentRow {
				t.Errorf("%s: engine %s row and agent row are both %s",
					paletteName, id, palette.AgentRow)
			}
		}
	}
}

// hueOf buckets a hex colour coarsely. It exists so the identity test asserts
// "orange", never "#ff9e64" — pinning the exact shade would make every theme
// tweak a test edit and teach the next author to update the expectation
// instead of reading it.
func hueOf(hex string) string {
	if len(hex) != 7 || hex[0] != '#' {
		return "unparseable"
	}
	var rgb [3]int
	for i := 0; i < 3; i++ {
		if _, err := fmt.Sscanf(hex[1+i*2:3+i*2], "%02x", &rgb[i]); err != nil {
			return "unparseable"
		}
	}
	red, green, blue := rgb[0], rgb[1], rgb[2]
	high, low := red, red
	for _, v := range rgb {
		if v > high {
			high = v
		}
		if v < low {
			low = v
		}
	}
	if high-low < 24 {
		if high > 200 {
			return "white"
		}
		return "grey"
	}
	switch {
	case red == high && green > blue && green < red-24:
		return "orange"
	case green == high && blue >= red:
		return "green"
	case green == high:
		return "green"
	case red == high:
		return "red"
	default:
		return "blue"
	}
}
