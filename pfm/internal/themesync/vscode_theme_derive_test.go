package themesync

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// shippedThemesDir is the blueprint's templates/themes/, every profile it ships.
const shippedThemesDir = "../../../templates/themes"

// shippedProfiles reads every theme the blueprint ships, keyed by its
// directory's name: each one's profile as ReadProfileDir assembles it.
func shippedProfiles(t *testing.T) map[string][]byte {
	t.Helper()
	entries, err := os.ReadDir(shippedThemesDir)
	if err != nil {
		t.Fatalf("list %s: %v", shippedThemesDir, err)
	}
	profiles := map[string][]byte{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		content, err := ReadProfileDir(filepath.Join(shippedThemesDir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		profiles[entry.Name()] = content
	}
	if len(profiles) == 0 {
		t.Fatalf("no theme in %s: the gate below would judge nothing", shippedThemesDir)
	}
	return profiles
}

// cieLab is a colour in CIE L*a*b* (D65), the space deltaE measures in.
func cieLab(t *testing.T, hex string) [3]float64 {
	t.Helper()
	colour, err := parseColor(hex[:7])
	if err != nil {
		t.Fatalf("parse %q: %v", hex, err)
	}
	linear := func(channel uint8) float64 {
		value := float64(channel) / 255
		if value <= 0.04045 {
			return value / 12.92
		}
		return math.Pow((value+0.055)/1.055, 2.4)
	}
	r, g, b := linear(colour.red), linear(colour.green), linear(colour.blue)
	x := (0.4124*r + 0.3576*g + 0.1805*b) / 0.95047
	y := 0.2126*r + 0.7152*g + 0.0722*b
	z := (0.0193*r + 0.1192*g + 0.9505*b) / 1.08883
	f := func(value float64) float64 {
		if value > 0.008856 {
			return math.Cbrt(value)
		}
		return 7.787*value + 16.0/116
	}
	return [3]float64{116*f(y) - 16, 500 * (f(x) - f(y)), 200 * (f(y) - f(z))}
}

// deltaE is CIE76: the distance between two colours in L*a*b*.
func deltaE(t *testing.T, a, b string) float64 {
	left, right := cieLab(t, a), cieLab(t, b)
	l, x, y := left[0]-right[0], left[1]-right[1], left[2]-right[2]
	return math.Sqrt(l*l + x*x + y*y)
}

func ratio(t *testing.T, a, b string) float64 {
	t.Helper()
	fg, err := parseColor(a[:7])
	if err != nil {
		t.Fatal(err)
	}
	bg, err := parseColor(b[:7])
	if err != nil {
		t.Fatal(err)
	}
	return contrastRatio(fg, bg)
}

// renderedDocument is a written theme file read back.
type renderedDocument struct {
	Name                string                         `json:"name"`
	Type                string                         `json:"type"`
	Colors              map[string]string              `json:"colors"`
	TokenColors         []VSCodeTokenColor             `json:"tokenColors"`
	SemanticTokenColors map[string]VSCodeSemanticStyle `json:"semanticTokenColors"`
}

// scopeColour is the foreground the rendered rules give one exact scope, the
// last rule naming it winning as in VS Code.
func scopeColour(document renderedDocument, scope string) string {
	colour := ""
	for _, rule := range document.TokenColors {
		for _, named := range rule.Scope {
			if named == scope && rule.Settings.Foreground != "" {
				colour = rule.Settings.Foreground
			}
		}
	}
	return colour
}

// TestEveryShippedProfileRendersAReadableDistinctTheme is the quality gate
// over every profile in templates/themes/: the theme renders and validates,
// covers every workbench area, passes CheckVSCodeReadability (the check
// `pfm theme check` runs: body text at 7:1, every syntax colour at 4.5:1,
// comments 3.5:1, on the editor, the current line and the selection, and the
// workbench text on its fill), and keeps the five roles a reader tells code
// apart by distinct (CIE76 deltaE >= 12).
func TestEveryShippedProfileRendersAReadableDistinctTheme(t *testing.T) {
	t.Parallel() // read-only over the shipped themes; no process state
	for stem, content := range shippedProfiles(t) {
		t.Run(stem, func(t *testing.T) {
			file, err := RenderVSCodeTheme(stem, content)
			if err != nil {
				t.Fatal(err)
			}
			var document renderedDocument
			if err := json.Unmarshal(file.Content, &document); err != nil {
				t.Fatalf("rendered theme is not JSON: %v", err)
			}
			if len(document.Colors) < 300 || len(document.TokenColors) < 30 || len(document.SemanticTokenColors) < 20 {
				t.Errorf("theme holds %d colours, %d token rules, %d semantic rules: not a whole theme",
					len(document.Colors), len(document.TokenColors), len(document.SemanticTokenColors))
			}
			for _, key := range []string{
				"editor.background", "sideBar.background", "panel.background", "tab.activeBackground",
				"list.activeSelectionBackground", "titleBar.activeBackground", "activityBar.background",
				"statusBar.background", "terminal.background", "editorSuggestWidget.background",
			} {
				if document.Colors[key] == "" {
					t.Errorf("colors[%q] unset", key)
				}
			}
			if err := CheckVSCodeReadability(file.Content); err != nil {
				t.Error(err)
			}
			roles := map[string]string{
				"keyword": scopeColour(document, "keyword"), "string": scopeColour(document, "string"),
				"function": scopeColour(document, "entity.name.function"),
				"type":     scopeColour(document, "entity.name.type"),
				"number":   scopeColour(document, "constant.numeric"),
			}
			names := []string{"keyword", "string", "function", "type", "number"}
			for i, left := range names {
				if roles[left] == "" {
					t.Fatalf("no rule colours %s", left)
				}
				for _, right := range names[i+1:] {
					if d := deltaE(t, roles[left], roles[right]); d < 12 {
						t.Errorf("%s %s and %s %s are %.1f apart (deltaE < 12): code reads alike",
							left, roles[left], right, roles[right], d)
					}
				}
			}
		})
	}
}

// TestADerivedThemeCoversEveryBuiltinBase: a palette with no override at all
// still renders a whole, valid, readable theme on every one of Claude's
// built-in bases, dark ones as vs-dark and light ones as vs.
func TestADerivedThemeCoversEveryBuiltinBase(t *testing.T) {
	for base := range builtinThemes {
		t.Run(base, func(t *testing.T) {
			file, err := RenderVSCodeTheme("plain", []byte(`{"name":"Plain","base":"`+base+`","overrides":{}}`))
			if err != nil {
				t.Fatal(err)
			}
			want := "vs-dark"
			if strings.HasPrefix(base, "light") {
				want = "vs"
			}
			if file.UITheme != want {
				t.Errorf("uiTheme = %q, want %q", file.UITheme, want)
			}
			var document renderedDocument
			if err := json.Unmarshal(file.Content, &document); err != nil {
				t.Fatal(err)
			}
			if len(document.Colors) < 300 {
				t.Errorf("a base-only palette renders %d colours", len(document.Colors))
			}
			// The derived layers alone: the chrome the contract paints over
			// them (vscodeContract) is not derive's to hold.
			tokens, side := customPalette(base, map[string]string{})
			variant, err := deriveVariant(CustomValue("plain"), tokens)
			if err != nil {
				t.Fatal(err)
			}
			derived, err := deriveVSCodeTheme("plain", tokens, variant, side)
			if err != nil {
				t.Fatal(err)
			}
			content, err := json.Marshal(derived)
			if err != nil {
				t.Fatal(err)
			}
			if err := CheckVSCodeReadability(content); err != nil {
				t.Error(err)
			}
		})
	}
}

func TestMixColourAndOnColour(t *testing.T) {
	if got := mixColour("#000000", "#ffffff", 0.5); got != "#808080" {
		t.Errorf("mix = %s, want #808080", got)
	}
	if got := mixColour("not a colour", "#ffffff", 0.5); got != "not a colour" {
		t.Errorf("mix of an unparsable colour = %s, want it back", got)
	}
	if got := withAlpha("#123456", 0x80); got != "#12345680" {
		t.Errorf("withAlpha = %s", got)
	}
	if got := onColour("#ff6ba7", "#230d18", "#381b29"); got != "#230d18" {
		t.Errorf("text on a bright pink = %s, want the first candidate that reads: #230d18", got)
	}
	if got := onColour("#808080", "#7f7f7f"); got != "#000000" {
		t.Errorf("text on mid grey = %s, want black, the furthest", got)
	}
}
