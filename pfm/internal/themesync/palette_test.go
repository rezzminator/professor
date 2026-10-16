package themesync

import (
	"strings"
	"testing"
)

func TestPalettesAreTheThreeShippedOnesInDisplayOrder(t *testing.T) {
	var names []string
	for _, palette := range Palettes() {
		names = append(names, palette.Name)
	}
	if got, want := strings.Join(names, ","), "tokyo-night,solarized,catppuccin"; got != want {
		t.Fatalf("Palettes() names = %q, want %q", got, want)
	}
}

func TestEveryPaletteVariantIsCompleteAndValid(t *testing.T) {
	if len(Palettes()) != 3 {
		t.Fatalf("Palettes() = %d entries, want 3", len(Palettes()))
	}
	for _, palette := range Palettes() {
		for _, appearance := range []Appearance{Dark, Light} {
			variant := palette.Variant(appearance)
			if err := variant.validate(); err != nil {
				t.Errorf("%s %s: %v", palette.Name, appearance, err)
			}
			if variant.State.Blocked != variant.ANSI[1] || variant.State.Working != variant.ANSI[3] ||
				variant.State.Done != variant.ANSI[2] || variant.State.Idle != variant.Muted {
				t.Errorf(
					"%s %s: state colours %+v are not red/yellow/green/muted",
					palette.Name,
					appearance,
					variant.State,
				)
			}
		}
		if palette.Title == "" || palette.HerdrDark == "" || palette.HerdrLight == "" {
			t.Errorf("%s: title or Herdr names empty: %+v", palette.Name, palette)
		}
	}
}

func TestVariantValidateRejectsABadHex(t *testing.T) {
	variant := Palettes()[0].Dark
	variant.ANSI[7] = "blue"
	if err := variant.validate(); err == nil || !strings.Contains(err.Error(), "ansi[7]") {
		t.Fatalf("validate = %v, want an error naming ansi[7]", err)
	}
}

func TestPaletteHexesMatchTheirUpstreamSchemes(t *testing.T) {
	cases := []struct {
		name       string
		appearance Appearance
		background string
		foreground string
		ansi1      string
		ansi4      string
		herdr      string
	}{
		{"tokyo-night", Dark, "#1a1b26", "#c0caf5", "#f7768e", "#7aa2f7", "tokyo-night"},
		{"tokyo-night", Light, "#e1e2e7", "#3760bf", "#f52a65", "#2e7de9", "tokyo-night-day"},
		{"solarized", Dark, "#002b36", "#839496", "#dc322f", "#268bd2", "solarized"},
		{"solarized", Light, "#fdf6e3", "#657b83", "#dc322f", "#268bd2", "solarized-light"},
		{"catppuccin", Dark, "#1e1e2e", "#cdd6f4", "#f38ba8", "#89b4fa", "catppuccin"},
		{"catppuccin", Light, "#eff1f5", "#4c4f69", "#d20f39", "#1e66f5", "catppuccin-latte"},
	}
	for _, tc := range cases {
		t.Run(tc.name+"-"+string(tc.appearance), func(t *testing.T) {
			palette, err := PaletteByName(tc.name)
			if err != nil {
				t.Fatal(err)
			}
			variant := palette.Variant(tc.appearance)
			if variant.Background != tc.background || variant.Foreground != tc.foreground ||
				variant.ANSI[1] != tc.ansi1 || variant.ANSI[4] != tc.ansi4 {
				t.Fatalf("variant = bg %s fg %s ansi1 %s ansi4 %s, want %s %s %s %s",
					variant.Background, variant.Foreground, variant.ANSI[1], variant.ANSI[4],
					tc.background, tc.foreground, tc.ansi1, tc.ansi4)
			}
			herdr := palette.HerdrDark
			if tc.appearance == Light {
				herdr = palette.HerdrLight
			}
			if herdr != tc.herdr {
				t.Fatalf("herdr name = %q, want %q", herdr, tc.herdr)
			}
		})
	}
}

func TestPaletteByNameUnknownListsEveryKnownName(t *testing.T) {
	if _, err := PaletteByName("nord"); err == nil ||
		!strings.Contains(err.Error(), "nord") ||
		!strings.Contains(err.Error(), "tokyo-night, solarized, catppuccin") {
		t.Fatalf("PaletteByName(nord) err = %v, want one naming nord and every known palette", err)
	}
	if palette, err := PaletteByName("solarized"); err != nil || palette.Title != "Solarized" {
		t.Fatalf("PaletteByName(solarized) = %q, %v", palette.Title, err)
	}
}

func TestVariantOfAnAppearanceDefaultsToDark(t *testing.T) {
	palette := Palettes()[0]
	if palette.Variant(Appearance("")).Background != palette.Dark.Background {
		t.Fatal("an empty appearance should select the dark variant")
	}
}
