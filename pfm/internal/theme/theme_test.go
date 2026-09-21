package theme

import (
	"fmt"
	"reflect"
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/naming"
)

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
