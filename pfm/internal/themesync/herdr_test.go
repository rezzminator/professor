package themesync

import (
	"regexp"
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

const herdrSidebar = "[ui.sidebar.agents]\nrows = [\n  [\"$pfm_working\", \"agent\"],\n  [\"$pfm_blocked\"],\n" +
	"  [\"$pfm_done\", \"$pfm_unknown\"]\n]\n\n[ui.sidebar.spaces]\nrows = [[\"name\", \"$pfm_working\"]]\n"

// goldStyled is herdrSidebar after an apply under professor-gold: working is
// the theme's blue, blocked its yellow, done its success, and unknown its
// inactive #565f89 lifted to 3:1 on the sidebar backgrounds.
func goldStyled(t *testing.T) string {
	t.Helper()
	out := herdrSidebar
	for token, hue := range map[string]string{
		"pfm_working": "#7aa2f7", "pfm_blocked": "#e0af68", "pfm_done": "#9ece6a", "pfm_unknown": "#747ea8",
	} {
		out = strings.ReplaceAll(out, `"$`+token+`"`, `{ token = "$`+token+`", fg = "`+hue+`" }`)
	}
	return out
}

func herdrConfigWithSidebar(h *harness) string {
	config := fixtureHerdr + "\n" + herdrSidebar
	h.write(".config/herdr/config.toml", config)
	return config
}

func TestHerdrApplyStylesTheSidebarRowsAndOffRestoresThemBare(t *testing.T) {
	h := newHarness(t)
	before := herdrConfigWithSidebar(h)
	wantVerdict(t, h.on(), "herdr", VerdictInSync, "")
	got := h.read(".config/herdr/config.toml")
	if !strings.Contains(got, goldStyled(t)) {
		t.Fatalf("sidebar rows not styled with the gold hues:\n%s", got)
	}
	wantVerdict(t, h.status(), "herdr", VerdictInSync, "")

	h.write(".config/herdr/config.toml", strings.Replace(got, `fg = "#e0af68"`, `fg = "#111111"`, 1))
	wantVerdict(t, h.status(), "herdr", VerdictDrift, "sidebar row pfm token styles differ")
	wantVerdict(t, h.sync(false), "herdr", VerdictInSync, "")
	if !strings.Contains(h.read(".config/herdr/config.toml"), goldStyled(t)) {
		t.Fatalf("drifted fg not rewritten:\n%s", h.read(".config/herdr/config.toml"))
	}

	wantVerdict(t, offReport(t, h), "herdr", VerdictRestored, "")
	if after := h.read(".config/herdr/config.toml"); after != before {
		t.Fatalf("off left the rows styled:\n%s\nwant\n%s", after, before)
	}
}

func offReport(t *testing.T, h *harness) Report {
	t.Helper()
	report, err := Off(t.Context(), h.env)
	if err != nil {
		t.Fatal(err)
	}
	return report
}

func TestHerdrOffStripsStyledRowsEvenWhenThePfmBlockIsGone(t *testing.T) {
	h := newHarness(t)
	herdrConfigWithSidebar(h)
	h.on()
	styled := h.read(".config/herdr/config.toml")
	if !strings.Contains(styled, goldStyled(t)) {
		t.Fatalf("on did not style the sidebar rows:\n%s", styled)
	}
	block, _, found := herdrExtract(styled)
	if !found {
		t.Fatal("no pfm block after on")
	}
	h.write(".config/herdr/config.toml", strings.Replace(styled, block, "", 1))
	wantVerdict(t, offReport(t, h), "herdr", VerdictRestored, "pfm block already gone")
	if after := h.read(".config/herdr/config.toml"); strings.Contains(after, "fg =") {
		t.Fatalf("styled rows survived off:\n%s", after)
	}
}

func TestHerdrReadBackOfAStateWithoutTheUnknownHueIsDriftNotError(t *testing.T) {
	h := newHarness(t)
	herdrConfigWithSidebar(h)
	h.on()
	const stateRel = ".local/state/pfm/theme/state.json"
	// a state written before the unknown hue existed, read back while the
	// source cannot resolve (so Applied is not re-derived)
	stale := regexp.MustCompile(`"unknown":\s*"#[0-9a-f]{6}"`).ReplaceAllString(h.read(stateRel), `"unknown": ""`)
	if stale == h.read(stateRel) {
		t.Fatal("setup: the state carries no unknown hue to remove")
	}
	h.write(stateRel, stale)
	h.write(settingsRel, `{"theme":`)
	wantVerdict(t, h.status(), "herdr", VerdictDrift, "carries no hue")
}
