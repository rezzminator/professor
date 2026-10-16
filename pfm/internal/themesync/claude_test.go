package themesync

import (
	"strings"
	"testing"
)

func tokyoNight(t *testing.T) Palette {
	t.Helper()
	palette, err := PaletteByName("tokyo-night")
	if err != nil {
		t.Fatal(err)
	}
	return palette
}

func TestClaudeRenderComputesEveryTokenFromTheVariant(t *testing.T) {
	palette := tokyoNight(t)
	original := claudeFile{
		Name: "Professor Gold", Base: "dark",
		Overrides: map[string]string{"promptBorder": "#ffd60a", "promptBorderShimmer": "#fff7c2", "text": "#111111"},
	}
	got := palette.claudeRender(Dark, original, claudeTokenKeys, nil)
	if got.Name != "Professor Gold · Tokyo Night" || got.Base != "dark" {
		t.Fatalf("name/base = %q/%q", got.Name, got.Base)
	}
	if len(got.Overrides) != len(claudeTokenKeys) || len(claudeTokenKeys) != 47 {
		t.Fatalf("overrides = %d keys, token list %d; want 47 each", len(got.Overrides), len(claudeTokenKeys))
	}
	variant := palette.Dark
	want := map[string]string{
		"text":                variant.Foreground,
		"inverseText":         variant.Background,
		"error":               variant.State.Blocked,
		"success":             variant.State.Done,
		"warning":             variant.State.Working,
		"claude":              variant.Accent,
		"selectionBg":         variant.SelectionBackground,
		"diffAdded":           "#374235",
		"claudeShimmer":       mixHex(variant.Accent, variant.Foreground, 0.35),
		"promptBorder":        "#ffd60a",
		"promptBorderShimmer": "#fff7c2",
	}
	for key, value := range want {
		if got.Overrides[key] != value {
			t.Errorf("%s = %q, want %q", key, got.Overrides[key], value)
		}
	}
	for key, value := range got.Overrides {
		if _, err := parseHexColor(value); err != nil {
			t.Errorf("%s = %q is not a hex colour", key, value)
		}
	}
}

func TestClaudeRenderLightSideAndMissingOverlayKeys(t *testing.T) {
	palette := tokyoNight(t)
	got := palette.claudeRender(Light, claudeFile{Name: "Plain"}, claudeTokenKeys, nil)
	if got.Base != "light" || got.Name != "Plain · Tokyo Night" {
		t.Fatalf("base/name = %q/%q", got.Base, got.Name)
	}
	light := palette.Light
	if got.Overrides["promptBorder"] != light.Accent ||
		got.Overrides["promptBorderShimmer"] != mixHex(light.Accent, light.Foreground, 0.35) {
		t.Fatalf("overlay fallback = %q / %q", got.Overrides["promptBorder"], got.Overrides["promptBorderShimmer"])
	}
}

func TestClaudeRenderKeepsAnUnmappedBaseTokenVerbatim(t *testing.T) {
	palette := tokyoNight(t)
	tokens := []string{"text", "futureToken"}
	got := palette.claudeRender(Dark, claudeFile{Name: "X"}, tokens, map[string]string{"futureToken": "#123456"})
	if got.Overrides["futureToken"] != "#123456" || got.Overrides["text"] != palette.Dark.Foreground {
		t.Fatalf("overrides = %v", got.Overrides)
	}
	if len(got.Overrides) != 2 {
		t.Fatalf("overrides = %v, want exactly the 2 requested tokens", got.Overrides)
	}
}

func TestClaudeTokenKeysAreUniqueAndCompleteSet(t *testing.T) {
	if len(claudeTokenKeys) != 47 {
		t.Fatalf("claudeTokenKeys = %d keys, want 47", len(claudeTokenKeys))
	}
	seen := map[string]bool{}
	for _, key := range claudeTokenKeys {
		if seen[key] || strings.TrimSpace(key) == "" {
			t.Fatalf("token %q duplicated or blank", key)
		}
		seen[key] = true
	}
}
