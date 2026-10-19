package themesync

import (
	"encoding/json"
	"strings"
	"testing"
)

// readabilityFixture is a rendered theme file holding just what the check reads.
func readabilityFixture(t *testing.T, colors map[string]string, rules []VSCodeTokenColor,
	semantic map[string]VSCodeSemanticStyle,
) []byte {
	t.Helper()
	content, err := json.Marshal(map[string]any{
		"colors": colors, "tokenColors": rules, "semanticTokenColors": semantic,
	})
	if err != nil {
		t.Fatal(err)
	}
	return content
}

func TestCheckVSCodeReadabilityNamesEveryPlaceUnderItsFloor(t *testing.T) {
	healthy := map[string]string{
		"editor.background": "#000000", "editor.foreground": "#ffffff",
		"editor.lineHighlightBackground": "#ffffff10",
		"input.background":               "#111111", "input.foreground": "#eeeeee",
	}
	keyword := []VSCodeTokenColor{{Name: "Keyword", Scope: []string{"keyword"}, Settings: VSCodeTokenSettings{
		Foreground: "#ffcc00",
	}}}
	if err := CheckVSCodeReadability(readabilityFixture(t, healthy, keyword, nil)); err != nil {
		t.Fatalf("a readable theme fails: %v", err)
	}

	for _, tc := range []struct {
		name     string
		colors   map[string]string
		rules    []VSCodeTokenColor
		semantic map[string]VSCodeSemanticStyle
		want     []string
	}{
		{
			name:   "input text the colour of its fill",
			colors: map[string]string{"input.background": "#2a241f", "input.foreground": "#2a241f"},
			want:   []string{`colors "input.foreground" #2a241f on input.background #2a241f: contrast 1.00 < 4.5`},
		},
		{
			name:   "an unset input foreground falls back to foreground",
			colors: map[string]string{"input.background": "#2a241f", "input.foreground": "", "foreground": "#3a342f"},
			want:   []string{`colors "foreground" #3a342f on input.background`},
		},
		{
			name:   "a keyword lost on the current line",
			colors: map[string]string{"editor.lineHighlightBackground": "#ffcc00ff"},
			rules:  keyword,
			want:   []string{`tokenColors[0] "Keyword" #ffcc00 on editor.lineHighlightBackground #ffcc00: contrast 1.00`},
		},
		{
			name:   "a comment at 4:1 passes, a keyword at 4:1 does not",
			colors: map[string]string{},
			rules: []VSCodeTokenColor{
				{Name: "Comment", Scope: []string{"comment"}, Settings: VSCodeTokenSettings{Foreground: "#737373"}},
				{Name: "Keyword", Scope: []string{"keyword"}, Settings: VSCodeTokenSettings{Foreground: "#737373"}},
			},
			semantic: map[string]VSCodeSemanticStyle{
				"comment": {Foreground: "#737373"}, "keyword": {Foreground: "#737373"},
			},
			want: []string{`tokenColors[1] "Keyword"`, `semanticTokenColors "keyword"`},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			colors := map[string]string{}
			for key, value := range healthy {
				colors[key] = value
			}
			for key, value := range tc.colors {
				if value == "" {
					delete(colors, key)
					continue
				}
				colors[key] = value
			}
			err := CheckVSCodeReadability(readabilityFixture(t, colors, tc.rules, tc.semantic))
			if err == nil {
				t.Fatal("an unreadable theme passes")
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error lacks %q:\n%v", want, err)
				}
			}
			if text := err.Error(); strings.Contains(text, `"Comment"`) ||
				strings.Contains(text, `semanticTokenColors "comment"`) {
				t.Errorf("a comment at its own floor is named: %v", err)
			}
		})
	}

	if err := CheckVSCodeReadability(readabilityFixture(t, map[string]string{"editor.foreground": "#ffffff"}, nil,
		nil)); err == nil || !strings.Contains(err.Error(), "nothing to measure text against") {
		t.Fatalf("a theme with no editor background must fail as unmeasurable, got %v", err)
	}
	if err := CheckVSCodeReadability([]byte(`{"colors": [`)); err == nil ||
		!strings.Contains(err.Error(), "does not read back") {
		t.Fatalf("a theme that does not parse must fail, got %v", err)
	}
}

// TestALightPaletteDrawsItsInputsOffClawdsInk: a light palette's Clawd is
// dark ink, so inputs, tabs and code blocks take a tint of the editor
// instead; a dark palette's Clawd, which body text reads on, still serves.
func TestALightPaletteDrawsItsInputsOffClawdsInk(t *testing.T) {
	for _, tc := range []struct {
		name, profile, clawd string
		takesClawd           bool
	}{
		{"light", `{"name":"Lantern","base":"light","overrides":{"clawd_background":"#2a241f"}}`, "#2a241f", false},
		{"dark", `{"name":"Night","base":"dark","overrides":{"clawd_background":"#05050a"}}`, "#05050a", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file, err := RenderVSCodeTheme("t", []byte(tc.profile))
			if err != nil {
				t.Fatal(err)
			}
			var document renderedDocument
			if err := json.Unmarshal(file.Content, &document); err != nil {
				t.Fatal(err)
			}
			input, text := document.Colors["input.background"], document.Colors["input.foreground"]
			if (input == tc.clawd) != tc.takesClawd {
				t.Errorf("input.background = %s with clawd_background %s: takes Clawd's colour %v, want %v",
					input, tc.clawd, input == tc.clawd, tc.takesClawd)
			}
			if got := ratio(t, text, input); got < vscodeTokenContrast {
				t.Errorf("input text %s on %s: contrast %.2f < %.1f", text, input, got, vscodeTokenContrast)
			}
			if err := CheckVSCodeReadability(file.Content); err != nil {
				t.Error(err)
			}
		})
	}
}

// TestDeriveLiftsSyntaxOffTheSelection: a keyword clear of the editor but
// lost on the selection is lifted until it reads there too.
func TestDeriveLiftsSyntaxOffTheSelection(t *testing.T) {
	tokens, side := customPalette("dark", map[string]string{})
	variant, err := deriveVariant("custom:t", tokens)
	if err != nil {
		t.Fatal(err)
	}
	roles, err := readVSCodeRoles("t", tokens, variant, side)
	if err != nil {
		t.Fatal(err)
	}
	roles.bg, roles.line, roles.selection, roles.accent = "#101010", "#101010", "#6060a0", "#8080ff"
	on := overColour(roles.bg, roles.selection, vscodeSelectionAlpha)
	if got := ratio(t, roles.accent, roles.bg); got < vscodeTokenContrast {
		t.Fatalf("fixture: the accent must clear the editor (%.2f)", got)
	}
	if got := ratio(t, roles.accent, on); got >= vscodeTokenContrast {
		t.Fatalf("fixture: the accent must be lost on the selection (%.2f)", got)
	}
	syntax, err := readVSCodeSyntax("t", map[string]string{}, roles)
	if err != nil {
		t.Fatal(err)
	}
	if got := ratio(t, syntax.keyword, on); got < vscodeTokenContrast-0.005 {
		t.Errorf("keyword %s on the selection %s: contrast %.2f < %.1f", syntax.keyword, on, got, vscodeTokenContrast)
	}
}

// TestGoTypeKeywordIsAKeyword: Go's `type` keyword (keyword.type.go) takes
// the keyword colour, not the primitive type colour keyword.type claims.
func TestGoTypeKeywordIsAKeyword(t *testing.T) {
	file, err := RenderVSCodeTheme("t", []byte(`{"name":"Night","base":"dark","overrides":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	var document renderedDocument
	if err := json.Unmarshal(file.Content, &document); err != nil {
		t.Fatal(err)
	}
	if got, want := scopeColour(document, "keyword.type.go"), scopeColour(document, "keyword"); got != want {
		t.Errorf("keyword.type.go = %q, want the keyword colour %q", got, want)
	}
}
