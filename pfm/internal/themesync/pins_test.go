package themesync

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestPinnableKeysAreTheKeysTheRenderersEmit(t *testing.T) {
	keys, err := renderedPinKeys()
	if err != nil {
		t.Fatal(err)
	}
	applied := hibiscusApplied(t)
	bookkeeping := []string{"@pfm-theme", "@pfm-theme-generation", "@pfm-theme-chips", "@pfm-state-colour"}
	rendered := map[string]bool{}
	for _, option := range tmuxOptions(applied, Dark, 1) {
		rendered[option.Name] = true
	}
	for _, name := range bookkeeping {
		if !rendered[name] {
			t.Errorf("tmuxOptions no longer renders %s: the bookkeeping list is stale", name)
		}
		if _, pinnable := keys.tmux[name]; pinnable {
			t.Errorf("tmux bookkeeping option %s is pinnable", name)
		}
	}
	if len(keys.tmux) != len(rendered)-len(bookkeeping) {
		t.Errorf("tmux pinnable keys = %d, want the %d rendered minus the %d bookkeeping",
			len(keys.tmux), len(rendered), len(bookkeeping))
	}
	for name := range keys.tmux {
		if !rendered[name] {
			t.Errorf("tmux key %s is pinnable but tmuxOptions does not render it", name)
		}
	}
	for name, kind := range map[string]pinKind{
		"@host_accent": pinColour, "@p_red": pinColour, "@pfm-c-bg": pinColour, "@pfm-state-working": pinColour,
		"status-style": pinText, "window-status-current-style": pinText, "mode-style": pinText,
	} {
		if got, ok := keys.tmux[name]; !ok || got != kind {
			t.Errorf("tmux key %s = kind %v (present %v), want kind %v", name, got, ok, kind)
		}
	}

	tokens := tokenMap(applied.Primary.herdrTokens())
	if len(keys.herdr) != len(tokens) {
		t.Errorf("herdr pinnable keys = %d, want the %d tokens herdrTokens emits", len(keys.herdr), len(tokens))
	}
	for name := range tokens {
		if keys.herdr[name] != pinColour {
			t.Errorf("herdr token %s is not a pinnable colour", name)
		}
	}
	contract := applied.vscodeContract(Dark, 1)
	for name := range contract.Colors {
		if keys.vscodeColors[name] != pinHex {
			t.Errorf("vscode colour %s is not a pinnable hex", name)
		}
	}
	for name := range contract.Chrome {
		if keys.vscodeChrome[name] != pinHex {
			t.Errorf("vscode chrome %s is not a pinnable hex", name)
		}
	}
	if len(keys.vscodeColors) != len(contract.Colors) || len(keys.vscodeChrome) != len(contract.Chrome) {
		t.Errorf("vscode pinnable keys = %d colours, %d chrome; the contract renders %d and %d",
			len(keys.vscodeColors), len(keys.vscodeChrome), len(contract.Colors), len(contract.Chrome))
	}
}

func TestValidatePinsAcceptsEveryRenderedKeyWithItsKindOfValue(t *testing.T) {
	keys, err := renderedPinKeys()
	if err != nil {
		t.Fatal(err)
	}
	fill := func(kinds map[string]pinKind) map[string]string {
		out := map[string]string{}
		for name, kind := range kinds {
			out[name] = "bg=colour1,fg=#abcdef"
			switch kind {
			case pinGlyph:
				out[name] = "⭕"
			case pinColour, pinHex:
				out[name] = "#abcdef"
			case pinText:
			}
		}
		return out
	}
	all := &SurfacePins{
		Tmux: fill(keys.tmux), Herdr: &HerdrPins{Tokens: fill(keys.herdr), TabGlyphs: fill(keys.herdrGlyphs)},
		VSCode: &VSCodePins{Colors: fill(keys.vscodeColors), Chrome: fill(keys.vscodeChrome)},
	}
	if err := validatePins(all); err != nil {
		t.Errorf("every rendered key pinned: %v", err)
	}
	if err := validatePins(nil); err != nil {
		t.Errorf("no pins: %v", err)
	}
	// A hex with an alpha channel is VS Code's; the other surfaces take opaque colours.
	if err := validatePins(
		&SurfacePins{VSCode: &VSCodePins{Chrome: map[string]string{"sash.hoverBorder": "#ff2e9380"}}},
	); err != nil {
		t.Errorf("an #rrggbbaa chrome pin: %v", err)
	}
}

func TestValidatePinsRejectsAnUnknownKeyOrABadValueNamingBoth(t *testing.T) {
	cases := []struct {
		name string
		pins SurfacePins
		want []string
	}{
		{
			"unknown tmux key",
			SurfacePins{Tmux: map[string]string{"@pfm-nope": "#ffffff"}},
			[]string{"tmux", "@pfm-nope"},
		},
		{
			"tmux bookkeeping @pfm-theme",
			SurfacePins{Tmux: map[string]string{"@pfm-theme": "x"}},
			[]string{"tmux", "@pfm-theme"},
		},
		{
			"tmux bookkeeping generation",
			SurfacePins{Tmux: map[string]string{"@pfm-theme-generation": "9"}},
			[]string{"tmux", "@pfm-theme-generation"},
		},
		{
			"tmux bookkeeping chip marker",
			SurfacePins{Tmux: map[string]string{"@pfm-theme-chips": "0"}},
			[]string{"tmux", "@pfm-theme-chips"},
		},
		{
			"tmux bookkeeping state format",
			SurfacePins{Tmux: map[string]string{"@pfm-state-colour": "x"}},
			[]string{"tmux", "@pfm-state-colour"},
		},
		{
			"bad tmux chip colour",
			SurfacePins{Tmux: map[string]string{"@host_accent": "purple"}},
			[]string{"tmux", "@host_accent", "purple"},
		},
		{
			"bad tmux @pfm-c colour",
			SurfacePins{Tmux: map[string]string{"@pfm-c-bg": "#12"}},
			[]string{"tmux", "@pfm-c-bg", "#12"},
		},
		{
			"empty tmux style",
			SurfacePins{Tmux: map[string]string{"status-style": " "}},
			[]string{"tmux", "status-style", "empty"},
		},
		{
			"unknown herdr token",
			SurfacePins{Herdr: &HerdrPins{Tokens: map[string]string{"accent_fg": "#ffffff"}}},
			[]string{"herdr", "accent_fg"},
		},
		{
			"bad herdr colour",
			SurfacePins{Herdr: &HerdrPins{Tokens: map[string]string{"accent": "ultraviolet"}}},
			[]string{"herdr", "accent", "ultraviolet"},
		},
		{
			"unknown herdr tab glyph state",
			SurfacePins{Herdr: &HerdrPins{TabGlyphs: map[string]string{"blink": "⭕"}}},
			[]string{"herdr tabGlyphs", "blink"},
		},
		{
			"empty herdr tab glyph",
			SurfacePins{Herdr: &HerdrPins{TabGlyphs: map[string]string{"blocked": ""}}},
			[]string{"herdr tabGlyphs", "blocked", "empty"},
		},
		{
			"herdr tab glyph over the prefix budget",
			SurfacePins{Herdr: &HerdrPins{TabGlyphs: map[string]string{"blockedOff": "🔴🔴🔴🔴🔴🔴🔴🔴🔴"}}},
			[]string{"herdr tabGlyphs", "blockedOff", "budget"},
		},
		{
			"unknown vscode colour",
			SurfacePins{VSCode: &VSCodePins{Colors: map[string]string{"terminal.nope": "#ffffff"}}},
			[]string{"vscode colors", "terminal.nope"},
		},
		{
			"chrome key under colors",
			SurfacePins{VSCode: &VSCodePins{Colors: map[string]string{"titleBar.activeBackground": "#ffffff"}}},
			[]string{"vscode colors", "titleBar.activeBackground"},
		},
		{
			"unknown vscode chrome",
			SurfacePins{VSCode: &VSCodePins{Chrome: map[string]string{"editor.background": "#ffffff"}}},
			[]string{"vscode chrome", "editor.background"},
		},
		{
			"colour key under chrome",
			SurfacePins{VSCode: &VSCodePins{Chrome: map[string]string{"terminal.background": "#ffffff"}}},
			[]string{"vscode chrome", "terminal.background"},
		},
		{
			"bad vscode colour",
			SurfacePins{VSCode: &VSCodePins{Colors: map[string]string{"terminal.background": "red"}}},
			[]string{"vscode colors", "terminal.background", "red"},
		},
		{
			"vscode hex of five digits",
			SurfacePins{VSCode: &VSCodePins{Chrome: map[string]string{"sash.hoverBorder": "#12345"}}},
			[]string{"vscode chrome", "sash.hoverBorder", "#12345"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validatePins(&tc.pins)
			if err == nil {
				t.Fatalf("validatePins accepted %+v", tc.pins)
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q lacks %q", err, want)
				}
			}
		})
	}
}

func TestCanonicalPinsWriteColoursAsHexAndDropEmptyMaps(t *testing.T) {
	got, err := canonicalPins(&SurfacePins{
		Tmux: map[string]string{
			"@host_accent": "#F0A", "@p_g0": "rgb(1, 2, 3)", "status-style": "bg=#0a0a0a,fg=default",
		},
		Herdr: &HerdrPins{
			Tokens:    map[string]string{"accent": "ansi:red"},
			TabGlyphs: map[string]string{"blocked": "🟣"},
		},
		VSCode: &VSCodePins{Colors: map[string]string{}, Chrome: map[string]string{"sash.hoverBorder": "#FF2E9380"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := &SurfacePins{
		Tmux: map[string]string{
			"@host_accent": "#ff00aa", "@p_g0": "#010203", "status-style": "bg=#0a0a0a,fg=default",
		},
		Herdr: &HerdrPins{
			Tokens:    map[string]string{"accent": "#cd0000"},
			TabGlyphs: map[string]string{"blocked": "🟣"},
		},
		VSCode: &VSCodePins{Chrome: map[string]string{"sash.hoverBorder": "#FF2E9380"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("canonical pins = %+v, want %+v", got, want)
	}
	for name, empty := range map[string]*SurfacePins{
		"nil": nil, "no maps": {}, "empty maps": {
			Tmux: map[string]string{}, Herdr: &HerdrPins{Tokens: map[string]string{}}, VSCode: &VSCodePins{},
		},
	} {
		if got, err := canonicalPins(empty); err != nil || got != nil {
			t.Errorf("%s: canonical pins = %+v, %v, want nil so a state.json round trip is stable", name, got, err)
		}
	}
}

func TestProfilePinsReadsOnlyACustomValuesProfile(t *testing.T) {
	home := t.TempDir()
	path := ProfilePath(home, "orchid")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(profileJSON(`{"herdr":{"accent":"#ff2e93"}}`)), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"", "dark", "auto", "orchid", "custom:absent"} {
		if got, err := profilePins(home, value); err != nil || got.pins != nil || got.file != "" {
			t.Errorf("profilePins(%q) = %+v, %v, want no pins and no file", value, got, err)
		}
	}
	got, err := profilePins(home, "custom:orchid")
	if err != nil || got.file != path || got.at.IsZero() || got.pins == nil ||
		got.pins.Herdr.Tokens["accent"] != "#ff2e93" {
		t.Errorf("profilePins(custom:orchid) = %+v, %v, want the pins read from %s", got, err, path)
	}
	for _, value := range []string{"custom:", "custom:../orchid", `custom:a\b`} {
		if _, err := profilePins(home, value); err == nil || !strings.Contains(err.Error(), "path separator") {
			t.Errorf("profilePins(%q) = %v, want a refusal naming the path separator", value, err)
		}
	}
}
