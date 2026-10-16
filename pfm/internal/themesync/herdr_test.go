package themesync

import (
	"strings"
	"testing"
)

func TestHerdrRenderAutoForcedDarkAndForcedLight(t *testing.T) {
	palette := tokyoNight(t)
	auto := herdrRender(palette, ModeAuto, 7)
	for _, want := range []string{
		"# >>> pfm theme · tokyo-night · gen 7 >>>\n[theme]\n",
		"name = \"tokyo-night\"\nauto_switch = true\ndark_name = \"tokyo-night\"\nlight_name = \"tokyo-night-day\"\n",
		"[theme.custom]\naccent = \"#7aa2f7\"\npanel_bg = \"#1a1b26\"\n",
		"[theme.custom.light]\naccent = \"#2e7de9\"\npanel_bg = \"#e1e2e7\"\n",
		"red = \"#f7768e\"", "red = \"#f52a65\"",
	} {
		if !strings.Contains(auto, want) {
			t.Errorf("auto block lacks %q:\n%s", want, auto)
		}
	}
	if !strings.HasSuffix(auto, "# <<< pfm theme <<<\n") {
		t.Errorf("auto block does not end with the close marker:\n%s", auto)
	}
	dark := herdrRender(palette, ModeDark, 7)
	if !strings.Contains(dark, "name = \"tokyo-night\"\nauto_switch = false\n") || strings.Contains(dark, "light") {
		t.Errorf("forced dark block wrong:\n%s", dark)
	}
	light := herdrRender(palette, ModeLight, 7)
	if !strings.Contains(light, "name = \"tokyo-night-day\"\nauto_switch = false\n") ||
		!strings.Contains(
			light,
			"[theme.custom]\naccent = \"#2e7de9\"",
		) || strings.Contains(light, "[theme.custom.light]") {
		t.Errorf("forced light block wrong:\n%s", light)
	}
}

func TestHerdrRenderCoversEveryDocumentedCustomToken(t *testing.T) {
	block := herdrRender(tokyoNight(t), ModeAuto, 1)
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
	block := herdrRender(tokyoNight(t), ModeAuto, 2)
	updated, snapshot, err := herdrInstall(herdrWithTheme, block)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Span != "[theme]\nname = \"solarized\"\nauto_switch = false\n" || snapshot.Added != "" {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	if strings.Contains(updated, "name = \"solarized\"") ||
		!strings.Contains(updated, "# >>> pfm theme · tokyo-night · gen 2 >>>") {
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
	updated, snapshot, err := herdrInstall(original, herdrRender(tokyoNight(t), ModeAuto, 1))
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
		block := herdrRender(tokyoNight(t), ModeAuto, 1)
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
	first, snapshot, err := herdrInstall(herdrWithTheme, herdrRender(tokyoNight(t), ModeAuto, 1))
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(first, "new_tab = \"prefix+c\"", "new_tab = \"prefix+t\"", 1)
	second, replaced := herdrReplace(edited, herdrRender(tokyoNight(t), ModeAuto, 2))
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
			if _, _, err := herdrInstall(original, herdrRender(tokyoNight(t), ModeAuto, 1)); err == nil {
				t.Fatal("expected an error, got nil")
			}
		})
	}
}

func TestHerdrExtractReadsTheStampedGeneration(t *testing.T) {
	block := herdrRender(tokyoNight(t), ModeAuto, 9)
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
