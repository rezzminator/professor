package themesync

import (
	"encoding/json"
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

func TestDeriveVariantMapsHibiscusOntoHerdrAndTheTerminalPalette(t *testing.T) {
	hibiscus := hibiscusApplied(t).Primary
	wantHerdr := map[string]string{
		"accent": "#7aa2f7", "panel_bg": "#1a1b26", "sidebar_bg": "#1f2335", "active_row_bg": "#283457",
		"selection_bg": "#283457", "surface0": "#1f2335", "surface1": "#292e42", "surface_dim": "#1f2335",
		"overlay0": "#565f89", "overlay1": "#7b84af", "text": "#c0caf5", "subtext0": "#7b84af",
		"mauve": "#bb9af7", "green": "#7b84af", "yellow": "#7aa2f7", "red": "#e0af68", "blue": "#9ece6a",
		"teal": "#9ece6a", "peach": "#ec937b",
	}
	got := tokenMap(hibiscus.herdrTokens())
	if len(got) != 19 {
		t.Fatalf("herdr tokens = %d, want 19", len(got))
	}
	for name, want := range wantHerdr {
		if got[name] != want {
			t.Errorf("herdr %s = %s, want %s", name, got[name], want)
		}
	}
	wantANSI := "#1a1b26 #f7768e #9ece6a #e0af68 #7aa2f7 #bb9af7 #7dcfff #c0caf5 #565f89"
	if joined := strings.Join(hibiscus.ANSI[:9], " "); joined != wantANSI {
		t.Errorf("ANSI 0-8 = %s, want %s", joined, wantANSI)
	}
	if hibiscus.ANSI[15] != "#c0caf5" || hibiscus.ANSI[9] != hibiscus.ANSI[1] {
		t.Errorf("bright half = %v", hibiscus.ANSI[8:])
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
			"overlay0": "#999999", "green": "#c1c1c1", "yellow": "#75a3d0", "red": "#d49104", "teal": "#4eba65",
		}},
		{"light", light, map[string]string{
			"panel_bg": "#ffffff", "text": "#000000", "overlay0": "#666666",
			"green": "#5b5b5b", "yellow": "#305d8c", "red": "#7a5402", "teal": "#256730",
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
// hibiscus custom one the muted overlay stays legible on the raised surface
// (overlay0/surface0 >= 2.5) and each state hue on the panel (>= 3).
func TestHerdrMappingContrastGuard(t *testing.T) {
	variants := map[string]Variant{"custom:hibiscus-night": hibiscusApplied(t).Primary}
	for name := range builtinThemes {
		variant, _, err := builtinVariant(name)
		if err != nil {
			t.Fatal(err)
		}
		variants[name] = variant
	}
	if len(variants) != 7 {
		t.Fatalf("guard covers %d themes, want the 6 built-ins and hibiscus", len(variants))
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
// on the background or on another sidebar background (or the muted overlay
// under 2.5:1 on the surface) moves in HSL lightness, 1% steps, to the first
// step that clears the guard; a colour already clear stays byte-identical. The
// expected hexes were worked out by hand from that rule.
func TestLiftMovesOnlyTheHuesUnderTheGuard(t *testing.T) {
	cases := []struct {
		theme                               string
		working, blocked, done, idle, muted string
		herdrGreen, herdrTeal, herdrYellow  string
	}{
		{"light-ansi", "#0000bb", "#343400", "#003e00", "#363636", "#7f7f7f", "#363636", "#003e00", "#0000bb"},
		{"light-daltonized", "#0059b3", "#715a00", "#005f8f", "#5b5b5b", "#666666", "#5b5b5b", "#005f8f", "#0059b3"},
		{"dark", "#75a3d0", "#d49104", "#4eba65", "#c1c1c1", "#999999", "#c1c1c1", "#4eba65", "#75a3d0"},
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

// fixtureFunk is an invented theme carrying the hue relationships of a loud
// blacklight theme: a deep plum background, a raised plum surface, a plum
// selection and a mid-pink overlay, with a blue and a yellow that sit under
// 3:1 on the overlay and an inactive pink that does too.
const fixtureFunk = `{"name":"Fixture Funk","base":"dark","overrides":{"claude":"#fe66b7",` +
	`"inverseText":"#260b1e","userMessageBackground":"#4d143c","selectionBg":"#781c5c","subtle":"#a63784",` +
	`"inactive":"#df9bbd","inactiveShimmer":"#f3d2e5","text":"#ffd6ee","permission":"#8292f7","success":"#3de8a8",` +
	`"red_FOR_SUBAGENTS_ONLY":"#fb2d1a","green_FOR_SUBAGENTS_ONLY":"#3de8a8","yellow_FOR_SUBAGENTS_ONLY":"#f7cc3a",` +
	`"blue_FOR_SUBAGENTS_ONLY":"#8292f7","purple_FOR_SUBAGENTS_ONLY":"#c77dff","cyan_FOR_SUBAGENTS_ONLY":"#38d6f5",` +
	`"orange_FOR_SUBAGENTS_ONLY":"#ff7a5c"}}` + "\n"

// customTokens resolves a custom theme file the way resolveCustom does: its
// base's tokens with the file's overrides on top.
func customTokens(t *testing.T, raw string) map[string]string {
	t.Helper()
	var file customFile
	if err := json.Unmarshal([]byte(raw), &file); err != nil {
		t.Fatal(err)
	}
	tokens := map[string]string{}
	for token, colour := range builtinThemes[file.Base] {
		tokens[token] = colour
	}
	for token, colour := range file.Overrides {
		tokens[token] = colour
	}
	return tokens
}

func stateThemes(t *testing.T) map[string]map[string]string {
	t.Helper()
	themes := map[string]map[string]string{
		"custom:hibiscus-night": customTokens(t, fixtureHibiscus),
		"custom:fixture-funk":   customTokens(t, fixtureFunk),
	}
	for name, tokens := range builtinThemes {
		themes[name] = tokens
	}
	if len(themes) != 8 {
		t.Fatalf("state hues cover %d themes, want the 6 built-ins, hibiscus and the funk fixture", len(themes))
	}
	return themes
}

// TestStateHuesComeFromTheThemeAndClearEverySidebarBackground pins D3: working
// is the theme's blue, blocked its yellow, done its success, idle its
// inactiveShimmer and unknown its inactive; a pick that already measures 3:1
// on every background Herdr's sidebar paints stays byte-identical, any other
// moves in lightness alone (same hue) to a colour that does.
func TestStateHuesComeFromTheThemeAndClearEverySidebarBackground(t *testing.T) {
	picks := []struct{ state, token string }{
		{"working", "blue_FOR_SUBAGENTS_ONLY"},
		{"blocked", "yellow_FOR_SUBAGENTS_ONLY"},
		{"done", "success"},
		{"idle", "inactiveShimmer"},
		{"unknown", "inactive"},
	}
	// honoured is how many of the sidebar backgrounds, in priority order, a
	// theme's hues can all clear: dark-ansi's overlay is the near-white
	// #e5e5e5 beside a black background and a #7f7f7f surface, and no colour
	// is 3:1 on all of those, so the hover row gives way.
	honoured := map[string]int{
		"dark": 4, "light": 4, "dark-ansi": 3, "light-ansi": 4, "dark-daltonized": 4, "light-daltonized": 4,
		"custom:hibiscus-night": 4, "custom:fixture-funk": 4,
	}
	for name, tokens := range stateThemes(t) {
		t.Run(name, func(t *testing.T) {
			variant, err := deriveVariant(name, tokens)
			if err != nil {
				t.Fatal(err)
			}
			got := map[string]string{
				"working": variant.State.Working, "blocked": variant.State.Blocked, "done": variant.State.Done,
				"idle": variant.State.Idle, "unknown": variant.State.Unknown,
			}
			backgrounds := variant.sidebarBackgrounds()
			if len(backgrounds) != 4 {
				t.Fatalf("sidebar backgrounds = %v, want surface, selection, background and overlay", backgrounds)
			}
			for _, pick := range picks {
				raw, err := parseColor(tokens[pick.token])
				if err != nil {
					t.Fatal(err)
				}
				clearsAll := true
				for _, background := range backgrounds[:honoured[name]] {
					if contrast(t, raw.String(), background) < 3 {
						clearsAll = false
					}
					if ratio := contrast(t, got[pick.state], background); ratio < 3 {
						t.Errorf(
							"%s: %s %s on %s = %.2f, want >= 3",
							name,
							pick.state,
							got[pick.state],
							background,
							ratio,
						)
					}
				}
				if clearsAll && got[pick.state] != raw.String() {
					t.Errorf(
						"%s: %s = %s, want its %s %s untouched",
						name,
						pick.state,
						got[pick.state],
						pick.token,
						raw,
					)
				}
				if !clearsAll {
					if got[pick.state] == raw.String() {
						t.Errorf(
							"%s: %s = %s is under 3:1 on a sidebar background and was not lifted",
							name,
							pick.state,
							raw,
						)
					}
					liftedColour, _ := parseColor(got[pick.state])
					rawHue, rawSaturation, _ := raw.hsl()
					hue, _, _ := liftedColour.hsl()
					distance := math.Abs(hue - rawHue)
					if rawSaturation > 0.1 && math.Min(distance, 6-distance) > 0.1 {
						t.Errorf(
							"%s: %s lifted %s -> %s moved the hue (sextants) %.2f -> %.2f",
							name,
							pick.state,
							raw,
							got[pick.state],
							rawHue,
							hue,
						)
					}
				}
			}
		})
	}
}

// TestFixtureFunkStateHuesAreTheWorkedOutLifts pins the exact hexes of the
// funk fixture, whose blue and unknown sit under 3:1 on the overlay: the lift
// goes lighter (darker would sink them into the plum background), the yellow,
// success and idle stay.
func TestFixtureFunkStateHuesAreTheWorkedOutLifts(t *testing.T) {
	variant, err := deriveVariant("custom:fixture-funk", customTokens(t, fixtureFunk))
	if err != nil {
		t.Fatal(err)
	}
	want := StateColors{Working: "#adb8fa", Blocked: "#f7cc3a", Done: "#3de8a8", Idle: "#f3d2e5", Unknown: "#e3a7c5"}
	if variant.State != want {
		t.Errorf("state = %+v, want %+v", variant.State, want)
	}
}

func TestLiftColorAllKeepsAClearColourAndMovesTheRest(t *testing.T) {
	cases := []struct {
		name, colour string
		backgrounds  []string
		want         string
	}{
		{"clear on every background", "#ffffff", []string{"#000000", "#111111", "#202020"}, "#ffffff"},
		{"lighter, away from a dark first background", "#555555", []string{"#000000", "#404040"}, "#8b8b8b"},
		{"darker, away from a light first background", "#aaaaaa", []string{"#ffffff", "#e0e0e0"}, "#7f7f7f"},
		{"the opposite direction when only it clears", "#ffffff", []string{"#000000", "#ffffff"}, "#949494"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := liftColorAll("t", "idle", tc.colour, tc.backgrounds, 3)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("liftColorAll = %s, want %s", got, tc.want)
			}
			for _, background := range tc.backgrounds {
				if ratio := contrast(t, got, background); ratio < 3 {
					t.Errorf("%s on %s = %.2f, want >= 3", got, background, ratio)
				}
			}
		})
	}
}

// A set of backgrounds no lightness clears gives way from its last member, so
// the hue still clears the ones that come first.
func TestLiftColorAllDropsTheLastBackgroundsNoLightnessClears(t *testing.T) {
	got, err := liftColorAll("mid", "working", "#ffffff", []string{"#000000", "#ffffff", "#808080"}, 3)
	if err != nil || got != "#949494" {
		t.Fatalf("liftColorAll = %q, %v, want #949494 (clear of black and white alone)", got, err)
	}
}

// The sidebar's own background (sidebar_bg, Surface) is the one every mark and
// row sits on, so it is the last one a state hue gives up: a panel and a
// sidebar no lightness clears together keep the hue clear of the sidebar.
func TestStateHueStaysClearOfTheSidebarWhenNoLightnessClearsEvery(t *testing.T) {
	variant := Variant{Background: "#545454", Surface: "#969696", SelectionBackground: "#969696", Overlay: "#969696"}
	got, err := liftColorAll("split", "working", "#8292f7", variant.sidebarBackgrounds(), 3)
	if err != nil {
		t.Fatal(err)
	}
	if ratio := contrast(t, got, variant.Surface); ratio < 3 {
		t.Fatalf("working %s on sidebar_bg %s = %.2f, want >= 3", got, variant.Surface, ratio)
	}
}

func TestLiftColorAllErrorsNameWhatFailed(t *testing.T) {
	_, err := liftColorAll("mid", "working", "#808080", []string{"#000000", "#ffffff"}, 22)
	if err == nil || !strings.Contains(err.Error(), `theme "mid" token "working"`) ||
		!strings.Contains(err.Error(), "contrast ratio") || !strings.Contains(err.Error(), "#000000") {
		t.Fatalf("unreachable guard err = %v, want the theme, the token, the ratio and the background", err)
	}
	if _, err := liftColorAll("t", "done", "limegreen", []string{"#000000"}, 3); err == nil ||
		!strings.Contains(err.Error(), "limegreen") {
		t.Fatalf("bad colour err = %v, want it to name limegreen", err)
	}
	if _, err := liftColorAll("t", "done", "#ffffff", []string{"#000000", "navy"}, 3); err == nil ||
		!strings.Contains(err.Error(), "navy") {
		t.Fatalf("bad background err = %v, want it to name navy", err)
	}
	if _, err := liftColorAll(
		"t",
		"done",
		"#ffffff",
		nil,
		3,
	); err == nil ||
		!strings.Contains(err.Error(), `token "done"`) {
		t.Fatalf("no background err = %v, want it to name the token", err)
	}
}
