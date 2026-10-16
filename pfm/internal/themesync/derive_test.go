package themesync

import (
	"math"
	"strings"
	"testing"
)

func tokenMap(tokens []herdrToken) map[string]string {
	out := map[string]string{}
	for _, token := range tokens {
		out[token.name] = token.value
	}
	return out
}

func TestDeriveVariantMapsGoldOntoHerdrAndTheTerminalPalette(t *testing.T) {
	gold := goldApplied(t).Primary
	wantHerdr := map[string]string{
		"accent": "#7aa2f7", "panel_bg": "#1a1b26", "sidebar_bg": "#1f2335", "active_row_bg": "#283457",
		"selection_bg": "#283457", "surface0": "#1f2335", "surface1": "#292e42", "surface_dim": "#1f2335",
		"overlay0": "#565f89", "overlay1": "#7b84af", "text": "#c0caf5", "subtext0": "#7b84af",
		"mauve": "#bb9af7", "green": "#7b84af", "yellow": "#7aa2f7", "red": "#bb9af7", "blue": "#9ece6a",
		"teal": "#9ece6a", "peach": "#ec937b",
	}
	got := tokenMap(gold.herdrTokens())
	if len(got) != 19 {
		t.Fatalf("herdr tokens = %d, want 19", len(got))
	}
	for name, want := range wantHerdr {
		if got[name] != want {
			t.Errorf("herdr %s = %s, want %s", name, got[name], want)
		}
	}
	wantANSI := "#1a1b26 #f7768e #9ece6a #e0af68 #7aa2f7 #bb9af7 #7dcfff #c0caf5 #565f89"
	if joined := strings.Join(gold.ANSI[:9], " "); joined != wantANSI {
		t.Errorf("ANSI 0-8 = %s, want %s", joined, wantANSI)
	}
	if gold.ANSI[15] != "#c0caf5" || gold.ANSI[9] != gold.ANSI[1] {
		t.Errorf("bright half = %v", gold.ANSI[8:])
	}
}

func TestDeriveVariantOfTheTwoDefaultBuiltins(t *testing.T) {
	dark, _, err := builtinVariant("dark")
	if err != nil {
		t.Fatal(err)
	}
	light, _, err := builtinVariant("light")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name    string
		variant Variant
		want    map[string]string
	}{
		{"dark", dark, map[string]string{
			"accent": "#d77757", "panel_bg": "#000000", "text": "#ffffff",
			"overlay0": "#999999", "green": "#c1c1c1", "yellow": "#d77757", "red": "#b1b9f9", "teal": "#4eba65",
		}},
		{"light", light, map[string]string{
			"panel_bg": "#ffffff", "text": "#000000", "overlay0": "#666666",
			"green": "#8e8e8e", "yellow": "#d77757", "red": "#5769f7", "teal": "#2c7a39",
		}},
	}
	for _, tc := range cases {
		got := tokenMap(tc.variant.herdrTokens())
		for name, want := range tc.want {
			if got[name] != want {
				t.Errorf("%s herdr %s = %s, want %s", tc.name, name, got[name], want)
			}
		}
	}
}

func TestDeriveVariantNamesAMissingOrBadToken(t *testing.T) {
	tokens := map[string]string{}
	for token, colour := range builtinThemes["dark"] {
		tokens[token] = colour
	}
	delete(tokens, "success")
	if _, err := deriveVariant("test", tokens); err == nil || !strings.Contains(err.Error(), `"success"`) {
		t.Fatalf("missing token error = %v", err)
	}
	tokens["success"] = "limegreen"
	if _, err := deriveVariant("test", tokens); err == nil || !strings.Contains(err.Error(), "limegreen") {
		t.Fatalf("bad colour error = %v", err)
	}
}

func luminance(t *testing.T, hex string) float64 {
	t.Helper()
	colour, err := parseColor(hex)
	if err != nil {
		t.Fatal(err)
	}
	channel := func(value uint8) float64 {
		v := float64(value) / 255
		if v <= 0.03928 {
			return v / 12.92
		}
		return math.Pow((v+0.055)/1.055, 2.4)
	}
	return 0.2126*channel(colour.red) + 0.7152*channel(colour.green) + 0.0722*channel(colour.blue)
}

func contrast(t *testing.T, a, b string) float64 {
	la, lb := luminance(t, a), luminance(t, b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// TestHerdrMappingContrastGuard pins P13: on every built-in theme and the
// gold custom one the muted overlay stays legible on the raised surface
// (overlay0/surface0 >= 2.5) and each state hue on the panel (>= 3).
func TestHerdrMappingContrastGuard(t *testing.T) {
	variants := map[string]Variant{"custom:professor-gold": goldApplied(t).Primary}
	for name := range builtinThemes {
		variant, _, err := builtinVariant(name)
		if err != nil {
			t.Fatal(err)
		}
		variants[name] = variant
	}
	if len(variants) != 7 {
		t.Fatalf("guard covers %d themes, want the 6 built-ins and gold", len(variants))
	}
	for name, variant := range variants {
		tokens := tokenMap(variant.herdrTokens())
		if ratio := contrast(t, tokens["overlay0"], tokens["surface0"]); ratio < 2.5 {
			t.Errorf(
				"%s: overlay0 %s on surface0 %s = %.2f, want >= 2.5",
				name,
				tokens["overlay0"],
				tokens["surface0"],
				ratio,
			)
		}
		for _, hue := range []string{"green", "yellow", "red", "teal"} {
			if ratio := contrast(t, tokens[hue], tokens["panel_bg"]); ratio < 3 {
				t.Errorf(
					"%s: %s %s on panel_bg %s = %.2f, want >= 3",
					name,
					hue,
					tokens[hue],
					tokens["panel_bg"],
					ratio,
				)
			}
		}
	}
}

// TestLiftMovesOnlyTheHuesUnderTheGuard pins the lift: a state hue under 3:1
// on the background (or the muted overlay under 2.5:1 on the surface) moves in
// HSL lightness, 1% steps away from the background, to the first step that
// clears the guard; a colour already clear stays byte-identical. The expected
// hexes were worked out by hand from that rule.
func TestLiftMovesOnlyTheHuesUnderTheGuard(t *testing.T) {
	cases := []struct {
		theme                               string
		working, blocked, done, idle, muted string
		herdrGreen, herdrTeal, herdrYellow  string
	}{
		{"light-ansi", "#ff0000", "#0000ee", "#009500", "#828282", "#7f7f7f", "#828282", "#009500", "#ff0000"},
		{"light-daltonized", "#e67300", "#3366ff", "#006699", "#8e8e8e", "#666666", "#8e8e8e", "#006699", "#e67300"},
		{"dark", "#d77757", "#b1b9f9", "#4eba65", "#c1c1c1", "#999999", "#c1c1c1", "#4eba65", "#d77757"},
	}
	for _, tc := range cases {
		t.Run(tc.theme, func(t *testing.T) {
			variant, _, err := builtinVariant(tc.theme)
			if err != nil {
				t.Fatal(err)
			}
			got := []string{
				variant.State.Working, variant.State.Blocked, variant.State.Done, variant.State.Idle, variant.Muted,
			}
			want := []string{tc.working, tc.blocked, tc.done, tc.idle, tc.muted}
			for index, name := range []string{"working", "blocked", "done", "idle", "muted"} {
				if got[index] != want[index] {
					t.Errorf("%s = %s, want %s", name, got[index], want[index])
				}
			}
			herdr := tokenMap(variant.herdrTokens())
			if herdr["green"] != tc.herdrGreen || herdr["teal"] != tc.herdrTeal || herdr["yellow"] != tc.herdrYellow {
				t.Errorf("herdr green/teal/yellow = %s/%s/%s, want %s/%s/%s",
					herdr["green"], herdr["teal"], herdr["yellow"], tc.herdrGreen, tc.herdrTeal, tc.herdrYellow)
			}
		})
	}
}

// A hue that cannot reach the guard at any lightness is an error naming the
// theme, the token and the ratio (no real background does at 3:1; a guard
// above 21:1 is the unreachable case).
func TestLiftErrorsWhenNoLightnessClearsTheGuard(t *testing.T) {
	_, err := liftColor("mid-grey", "idle", "#808080", "#808080", 22)
	if err == nil || !strings.Contains(err.Error(), `theme "mid-grey" token "idle"`) ||
		!strings.Contains(err.Error(), "contrast ratio 5.") {
		t.Fatalf("err = %v, want it to name the theme, the token and the ratio", err)
	}
	if got, err := liftColor("t", "idle", "#808080", "#ffffff", 3); err != nil || got != "#808080" {
		t.Fatalf("a colour already clear must stay byte-identical, got %q, %v", got, err)
	}
}
