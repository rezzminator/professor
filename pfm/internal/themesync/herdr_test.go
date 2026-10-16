package themesync

import (
	"strings"
	"testing"
)

func TestHerdrRenderAutoForcedDarkAndLightSide(t *testing.T) {
	gold := goldApplied(t)
	dark := herdrRender(gold, 7)
	for _, want := range []string{
		"# >>> pfm theme · custom:professor-gold · gen 7 >>>\n[theme]\n",
		"name = \"tokyo-night\"\nauto_switch = false\n\n[theme.custom]\naccent = \"#7aa2f7\"\npanel_bg = \"#1a1b26\"\n",
	} {
		if !strings.Contains(dark, want) {
			t.Errorf("dark block lacks %q:\n%s", want, dark)
		}
	}
	if !strings.HasSuffix(dark, "# <<< pfm theme <<<\n") || strings.Contains(dark, "[theme.custom.light]") {
		t.Errorf("dark block wrong:\n%s", dark)
	}
	light, err := resolveValue("light", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	lightBlock := herdrRender(light.Applied, 7)
	if !strings.Contains(lightBlock, "name = \"tokyo-night-day\"\nauto_switch = false\n") ||
		!strings.Contains(lightBlock, "[theme.custom]\naccent = \"#d77757\"\npanel_bg = \"#ffffff\"") ||
		strings.Contains(lightBlock, "[theme.custom.light]") {
		t.Errorf("light block wrong:\n%s", lightBlock)
	}
	auto, err := resolveValue("auto", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	autoBlock := herdrRender(auto.Applied, 7)
	for _, want := range []string{
		"# >>> pfm theme · auto · gen 7 >>>\n[theme]\n",
		"name = \"tokyo-night\"\nauto_switch = true\ndark_name = \"tokyo-night\"\nlight_name = \"tokyo-night-day\"\n",
		"[theme.custom]\naccent = \"#d77757\"\npanel_bg = \"#000000\"\n",
		"[theme.custom.light]\naccent = \"#d77757\"\npanel_bg = \"#ffffff\"\n",
	} {
		if !strings.Contains(autoBlock, want) {
			t.Errorf("auto block lacks %q:\n%s", want, autoBlock)
		}
	}
}

func TestHerdrRenderCoversEveryDocumentedCustomToken(t *testing.T) {
	auto, err := resolveValue("auto", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	block := herdrRender(auto.Applied, 1)
	for _, token := range []string{
		"accent", "panel_bg", "sidebar_bg", "active_row_bg", "selection_bg", "surface0", "surface1", "surface_dim",
		"overlay0", "overlay1", "text", "subtext0", "mauve", "green", "yellow", "red", "blue", "teal", "peach",
	} {
		if strings.Count(block, "\n"+token+" = \"#") != 2 {
			t.Errorf("token %s should appear once per side (2):\n%s", token, block)
		}
	}
}

const herdrWithTheme = "onboarding = false\n\n[ui]\nstatus_indicators = \"dots\"\n\n" +
	"[theme]\nname = \"solarized\"\nauto_switch = false\n\n# VS Code shortcuts\n# second line\n[keys]\nnew_tab = \"prefix+c\"\n"

func TestHerdrInstallReplacesTheThemeSpanAndRemoveRestoresBytes(t *testing.T) {
	block := herdrRender(goldApplied(t), 2)
	updated, snapshot, err := herdrInstall(herdrWithTheme, block)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Span != "[theme]\nname = \"solarized\"\nauto_switch = false\n" || snapshot.Added != "" {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	if strings.Contains(updated, "name = \"solarized\"") ||
		!strings.Contains(updated, "# >>> pfm theme · custom:professor-gold · gen 2 >>>") {
		t.Fatalf("theme span not replaced:\n%s", updated)
	}
	if !strings.Contains(updated, "# <<< pfm theme <<<\n\n# VS Code shortcuts\n# second line\n[keys]\n") {
		t.Fatalf("trailing blank and comment lines before [keys] were not preserved:\n%s", updated)
	}
	restored, ok := herdrRemove(updated, snapshot)
	if !ok || restored != herdrWithTheme {
		t.Fatalf("remove = ok %v\n%q\nwant\n%q", ok, restored, herdrWithTheme)
	}
}

func TestHerdrInstallWithCustomSubtablesAndNoTrailingNewline(t *testing.T) {
	original := "[theme]\nname = \"catppuccin\"\n[theme.custom]\naccent = \"#fff\"\n[theme.custom.light]\ntext = \"#000\""
	updated, snapshot, err := herdrInstall(original, herdrRender(goldApplied(t), 1))
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Span != original {
		t.Fatalf("span = %q, want the whole three-table region %q", snapshot.Span, original)
	}
	if strings.Contains(updated, "catppuccin") || strings.Contains(updated, "#fff\"") {
		t.Fatalf("old theme tables survived:\n%s", updated)
	}
	if restored, ok := herdrRemove(updated, snapshot); !ok || restored != original {
		t.Fatalf("remove = %v %q, want %q", ok, restored, original)
	}
}

func TestHerdrInstallWithNoThemeAppendsAndRemoveStripsExactly(t *testing.T) {
	for _, original := range []string{"", "a = 1\n", "a = 1", "a = 1\n\n", "[keys]\nx = 1\n"} {
		block := herdrRender(goldApplied(t), 1)
		updated, snapshot, err := herdrInstall(original, block)
		if err != nil {
			t.Fatalf("%q: %v", original, err)
		}
		if snapshot.Span != "" || !strings.HasSuffix(updated, block) || snapshot.Added == "" {
			t.Fatalf("%q: snapshot %+v, updated %q", original, snapshot, updated)
		}
		if restored, ok := herdrRemove(updated, snapshot); !ok || restored != original {
			t.Fatalf("%q: remove = %v %q", original, ok, restored)
		}
	}
}

func TestHerdrReplaceSwapsAnExistingBlockInPlaceAndKeepsUserEdits(t *testing.T) {
	first, snapshot, err := herdrInstall(herdrWithTheme, herdrRender(goldApplied(t), 1))
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(first, "new_tab = \"prefix+c\"", "new_tab = \"prefix+t\"", 1)
	second, replaced := herdrReplace(edited, herdrRender(goldApplied(t), 2))
	if !replaced || strings.Count(second, ">>> pfm theme") != 1 || !strings.Contains(second, "gen 2 >>>") {
		t.Fatalf("replace = %v\n%s", replaced, second)
	}
	restored, ok := herdrRemove(second, snapshot)
	if !ok || restored != strings.Replace(herdrWithTheme, "new_tab = \"prefix+c\"", "new_tab = \"prefix+t\"", 1) {
		t.Fatalf("remove after replace and a user edit = %v\n%q", ok, restored)
	}
	if _, replaced := herdrReplace(herdrWithTheme, "x"); replaced {
		t.Fatal("replace on a file with no pfm block must report false")
	}
}

func TestHerdrInstallRefusesShapesItCannotRestoreByteForByte(t *testing.T) {
	cases := map[string]string{
		"split theme tables":    "[theme]\nname = \"a\"\n[keys]\nx = 1\n[theme.custom]\naccent = \"#fff\"\n",
		"dotted root theme key": "theme.name = \"a\"\n[keys]\nx = 1\n",
		"root theme assignment": "theme = { name = \"a\" }\n",
	}
	for name, original := range cases {
		t.Run(name, func(t *testing.T) {
			if _, _, err := herdrInstall(original, herdrRender(goldApplied(t), 1)); err == nil {
				t.Fatal("expected an error, got nil")
			}
		})
	}
}

func TestHerdrExtractReadsTheStampedGeneration(t *testing.T) {
	block := herdrRender(goldApplied(t), 9)
	updated, _, err := herdrInstall(herdrWithTheme, block)
	if err != nil {
		t.Fatal(err)
	}
	got, generation, found := herdrExtract(updated)
	if !found || generation != 9 || got != block {
		t.Fatalf("extract = %v gen %d\n%q\nwant\n%q", found, generation, got, block)
	}
	if _, _, found := herdrExtract(herdrWithTheme); found {
		t.Fatal("a file without markers must report not found")
	}
}
