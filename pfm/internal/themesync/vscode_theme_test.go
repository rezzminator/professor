package themesync

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestVSCodeColourRegistryParses(t *testing.T) {
	if len(vscodeColorIDs) < 900 || !vscodeColorIDs["editor.background"] ||
		!vscodeColorIDs["terminal.ansiBrightWhite"] {
		t.Fatalf("vscodeColorIDs holds %d ids, want VS Code's whole registry", len(vscodeColorIDs))
	}
	for _, bad := range []string{"editor.background\neditor.background\n", "editor..background\n"} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("parseVSCodeColorIDs(%q) did not panic", bad)
				}
			}()
			parseVSCodeColorIDs(bad)
		}()
	}
}

func TestValidateVSCodeThemeRefusesWhatVSCodeWouldIgnore(t *testing.T) {
	if err := validateVSCodeTheme(&VSCodeTheme{}); err != nil {
		t.Errorf("an empty theme layer = %v, want valid: derive fills it", err)
	}
	for name, tc := range map[string]struct{ theme, want string }{
		"unknown colour id":   {`{"colors":{"editor.backgroundd":"#000000"}}`, `"editor.backgroundd": not a VS Code colour id`},
		"bad colour value":    {`{"colors":{"editor.background":"black"}}`, `"black" is not #rgb`},
		"rule without scope":  {`{"tokenColors":[{"name":"x","settings":{"foreground":"#ffffff"}}]}`, "(x): no scope"},
		"bad scope":           {`{"tokenColors":[{"scope":["comment,,string"],"settings":{"foreground":"#fff"}}]}`, "not a TextMate scope"},
		"rule without style":  {`{"tokenColors":[{"scope":["comment"],"settings":{}}]}`, "neither foreground nor fontStyle"},
		"bad rule colour":     {`{"tokenColors":[{"scope":["comment"],"settings":{"foreground":"red"}}]}`, `foreground "red"`},
		"bad font style":      {`{"tokenColors":[{"scope":["comment"],"settings":{"fontStyle":"oblique"}}]}`, `"oblique"`},
		"bad selector":        {`{"semanticTokenColors":{"variable..readonly":"#ffffff"}}`, "not a semantic token selector"},
		"semantic sets none":  {`{"semanticTokenColors":{"variable":{}}}`, "sets no style"},
		"bad semantic colour": {`{"semanticTokenColors":{"variable":"blue"}}`, `foreground "blue"`},
	} {
		var theme VSCodeTheme
		if err := json.Unmarshal([]byte(tc.theme), &theme); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if err := validateVSCodeTheme(&theme); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want one naming %q", name, err, tc.want)
		}
	}
	var unknown VSCodeSemanticStyle
	if err := json.Unmarshal([]byte(`{"foreground":"#fff","glow":true}`), &unknown); err == nil {
		t.Error("a semantic style with an unknown key parsed")
	}
}

func TestMergeVSCodeThemeReplacesOnlyWhatTheProfileNames(t *testing.T) {
	italic, off := "italic", false
	derived := VSCodeTheme{
		Colors: map[string]string{"editor.background": "#000000", "editor.foreground": "#ffffff"},
		TokenColors: []VSCodeTokenColor{
			{Name: "Comment", Scope: []string{"comment"}, Settings: VSCodeTokenSettings{Foreground: "#777777"}},
			{
				Name:     "String",
				Scope:    []string{"string", "string.quoted"},
				Settings: VSCodeTokenSettings{Foreground: "#00ff00"},
			},
		},
		SemanticTokenColors: map[string]VSCodeSemanticStyle{
			"variable": {Foreground: "#ffffff"},
			"type":     {Foreground: "#ffff00"},
		},
	}
	own := VSCodeTheme{
		Colors: map[string]string{"editor.background": "#101010"},
		TokenColors: []VSCodeTokenColor{
			{Scope: []string{"comment", "string"}, Settings: VSCodeTokenSettings{FontStyle: &italic}},
		},
		SemanticHighlighting: &off,
		SemanticTokenColors:  map[string]VSCodeSemanticStyle{"type": {Foreground: "#ff00ff"}},
	}
	merged := mergeVSCodeTheme(derived, own)
	if merged.Colors["editor.background"] != "#101010" || merged.Colors["editor.foreground"] != "#ffffff" {
		t.Errorf("colors = %v, want the profile's background and derive's foreground", merged.Colors)
	}
	if len(merged.TokenColors) != 2 || merged.TokenColors[0].Name != "String" ||
		strings.Join(
			merged.TokenColors[0].Scope,
			",",
		) != "string.quoted" || merged.TokenColors[1].Settings.FontStyle == nil {
		t.Errorf(
			"tokenColors = %+v, want derive's comment rule dropped, its string rule left string.quoted, the profile's rule last",
			merged.TokenColors,
		)
	}
	if merged.SemanticTokenColors["type"].Foreground != "#ff00ff" ||
		merged.SemanticTokenColors["variable"].Foreground != "#ffffff" ||
		merged.SemanticHighlighting == nil ||
		*merged.SemanticHighlighting {
		t.Errorf("semantic = %v %v, want the profile's type, derive's variable, highlighting off",
			merged.SemanticTokenColors, merged.SemanticHighlighting)
	}
	if derived.Colors["editor.background"] != "#000000" {
		t.Error("merge wrote through into the derived theme")
	}
}

func TestRenderVSCodeThemeNamesAndLayersTheTheme(t *testing.T) {
	profile := `{"name":"Tokyo Night · neon pink","base":"dark","overrides":{"claude":"#ff6ba7","inverseText":"#381b29"},` +
		`"surfaces":{"vscode":{"chrome":{"statusBar.background":"#112233"},"theme":{` +
		`"colors":{"sideBar.background":"#2a0f1c","titleBar.activeBackground":"#445566","terminal.background":"#010203"},` +
		`"tokenColors":[{"scope":["comment"],"settings":{"foreground":"#a87d90","fontStyle":"italic"}}]}}}}`
	file, err := RenderVSCodeTheme("tokyo-night", []byte(profile))
	if err != nil {
		t.Fatal(err)
	}
	if file.ID != "professor-tokyo-night" || file.Label != "Professor · Tokyo Night" ||
		file.UITheme != "vs-dark" ||
		file.Path != "./themes/professor-tokyo-night.json" ||
		file.Value != "custom:tokyo-night" {
		t.Errorf("file = %+v", file)
	}
	var document renderedDocument
	if err := json.Unmarshal(file.Content, &document); err != nil {
		t.Fatal(err)
	}
	if document.Name != file.Label || document.Type != "dark" {
		t.Errorf("document name/type = %q/%q", document.Name, document.Type)
	}
	for key, want := range map[string]string{
		// The profile's own colour wins over derive.
		"sideBar.background": "#2a0f1c",
		// A contract key the theme sets is a pin: the contract renders it, the theme carries the same.
		"titleBar.activeBackground": "#445566", "terminal.background": "#010203",
		// An explicit chrome pin beats the theme.
		"statusBar.background": "#112233",
		// Derive fills what nobody names.
		"editor.background": "#381b29",
	} {
		if document.Colors[key] != want {
			t.Errorf("colors[%q] = %q, want %q", key, document.Colors[key], want)
		}
	}
	if got := scopeColour(document, "comment"); got != "#a87d90" {
		t.Errorf("comment = %q, want the profile's own rule", got)
	}
	if !strings.HasSuffix(string(file.Content), "}\n") ||
		!strings.Contains(string(file.Content), `"$schema": "vscode://schemas/color-theme"`) {
		t.Errorf("content is not the indented theme document:\n%.200s", file.Content)
	}

	light, err := RenderVSCodeTheme("paper", []byte(`{"name":"Paper","base":"light","overrides":{}}`))
	if err != nil || light.UITheme != "vs" || !strings.Contains(string(light.Content), `"type": "light"`) ||
		light.Label != "Professor · Paper" {
		t.Errorf("a light profile = %+v, %v, want uiTheme vs and type light", light, err)
	}
	for name, tc := range map[string]struct{ stem, content, want string }{
		"path stem":     {"a/b", `{"name":"x","overrides":{}}`, "not a bare profile name"},
		"empty stem":    {"", `{"name":"x","overrides":{}}`, "not a bare profile name"},
		"bad profile":   {"x", `{"name":"x","overrides":{"nope":"#fff"}}`, `VS Code theme x: profile x: overrides "nope"`},
		"bad own theme": {"x", `{"name":"x","overrides":{},"surfaces":{"vscode":{"theme":{"colors":{"no.such":"#fff"}}}}}`, `"no.such"`},
	} {
		if _, err := RenderVSCodeTheme(
			tc.stem,
			[]byte(tc.content),
		); err == nil ||
			!strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want one naming %q", name, err, tc.want)
		}
	}
}

func TestVSCodeThemeIndexListsEveryTheme(t *testing.T) {
	empty, err := VSCodeThemeIndex(nil)
	if err != nil || string(empty) != "{\n  \"themes\": []\n}\n" {
		t.Errorf("an empty index = %q, %v", empty, err)
	}
	index, err := VSCodeThemeIndex([]VSCodeThemeFile{{
		ID: "professor-a", Label: "Professor · A", UITheme: "vs-dark", Path: "./themes/professor-a.json",
		Value: "custom:a", Content: []byte("ignored"),
	}})
	if err != nil || strings.Contains(string(index), "ignored") ||
		!strings.Contains(string(index), `"value": "custom:a"`) {
		t.Errorf("index = %s, %v, want the entry without its content", index, err)
	}
}
