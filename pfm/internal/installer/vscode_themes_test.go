package installer

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/themesync"
)

// lightProfile is a light-based profile: its VS Code theme is a "vs" one.
const lightProfile = `{"name": "Paper Day · morning", "base": "light", "overrides": {"claude": "#b8336a"}}
`

type contributedTheme struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	UITheme string `json:"uiTheme"`
	Path    string `json:"path"`
}

func (host *profileHost) extensionDir() string {
	return filepath.Join(managedRootForHome(host.home), filepath.FromSlash(vscodeExtensionSource))
}

// contributed is the staged package.json's contributes.themes.
func (host *profileHost) contributed() []contributedTheme {
	host.t.Helper()
	var manifest struct {
		Contributes struct {
			Themes []contributedTheme `json:"themes"`
		} `json:"contributes"`
	}
	content := readFixture(host.t, filepath.Join(host.extensionDir(), "package.json"))
	if err := json.Unmarshal([]byte(content), &manifest); err != nil {
		host.t.Fatalf("staged package.json does not parse: %v\n%s", err, content)
	}
	return manifest.Contributes.Themes
}

func (host *profileHost) renderedThemes() []string {
	host.t.Helper()
	entries, err := os.ReadDir(filepath.Join(host.extensionDir(), themesync.VSCodeThemeDir))
	if err != nil && !os.IsNotExist(err) {
		host.t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

func TestInstallContributesOneVSCodeColorThemePerInstalledTheme(t *testing.T) {
	t.Parallel()
	host := newProfileHost(t)
	host.seed(map[string]string{"pinned-night": pinnedProfile, "paper-day": lightProfile})
	host.run(ModeApply)

	want := []contributedTheme{
		{"professor-paper-day", "Professor · Paper Day", "vs", "./themes/professor-paper-day.json"},
		{"professor-pinned-night", "Professor · Pinned Night", "vs-dark", "./themes/professor-pinned-night.json"},
	}
	got := host.contributed()
	if len(got) != len(want) {
		t.Fatalf("contributes.themes = %+v, want %+v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("contributes.themes[%d] = %+v, want %+v", index, got[index], want[index])
		}
		stem := strings.TrimPrefix(want[index].ID, vscodeThemeFilePrefix)
		rendered, err := themesync.RenderVSCodeTheme(stem, []byte(contents(t, themesync.ProfilePath(host.home, stem))))
		if err != nil {
			t.Fatal(err)
		}
		if on := contents(
			t,
			filepath.Join(host.extensionDir(), filepath.FromSlash(want[index].Path)),
		); on != string(
			rendered.Content,
		) {
			t.Fatalf("%s on disk is not the theme rendered from the installed profile copy", want[index].Path)
		}
	}
	var index struct {
		Themes []themesync.VSCodeThemeFile `json:"themes"`
	}
	indexPath := filepath.Join(host.extensionDir(), themesync.VSCodeThemeDir, themesync.VSCodeThemeIndexFile)
	if err := json.Unmarshal([]byte(contents(t, indexPath)), &index); err != nil {
		t.Fatalf("index.json: %v", err)
	}
	if len(index.Themes) != 2 || index.Themes[0].Value != "custom:paper-day" ||
		index.Themes[1].Value != "custom:pinned-night" {
		t.Fatalf("index.json must map each theme id to its Claude value: %+v", index.Themes)
	}

	_, again := host.run(ModeApply)
	for _, line := range strings.Split(again, "\n") {
		if strings.HasPrefix(line, "  change  ") && strings.Contains(
			line, filepath.Join(host.extensionDir(), themesync.VSCodeThemeDir)+string(filepath.Separator),
		) {
			t.Fatalf("a second install must change no VS Code theme: %q", line)
		}
	}

	host.seed(map[string]string{"pinned-night": pinnedProfile})
	host.run(ModeApply)
	if got := host.contributed(); len(got) != 1 || got[0].ID != "professor-pinned-night" {
		t.Fatalf("a removed profile must leave contributes.themes: %+v", got)
	}
	if names := host.renderedThemes(); strings.Join(names, ",") != "index.json,professor-pinned-night.json" {
		t.Fatalf("a removed profile's VS Code theme must be pruned, left %q", names)
	}

	host.run(ModeUninstall)
	if !absent(t, host.extensionDir()) {
		t.Fatalf(
			"uninstall must remove the rendered themes and the extension directory, left %q",
			host.renderedThemes(),
		)
	}
}

func TestInstallWithoutThemesStagesAnEmptyThemeList(t *testing.T) {
	t.Parallel()
	host := newProfileHost(t)
	host.seed(map[string]string{})
	_, output := host.run(ModeApply)
	if got := host.contributed(); len(got) != 0 {
		t.Fatalf("no installed theme must contribute no VS Code theme: %+v", got)
	}
	if names := host.renderedThemes(); strings.Join(names, ",") != "index.json" {
		t.Fatalf("an install with no theme writes the empty index alone, got %q\n%s", names, output)
	}
}

// TestUninstallKeepsAThemeDirectoryHoldingAStrayFileAndGoesOn: a file pfm
// never wrote in the rendered-theme directory keeps that directory, reported
// as a skip, and uninstall still removes what follows it.
func TestUninstallKeepsAThemeDirectoryHoldingAStrayFileAndGoesOn(t *testing.T) {
	t.Parallel()
	host := newProfileHost(t)
	host.seed(map[string]string{"pinned-night": pinnedProfile})
	host.run(ModeApply)
	stray := filepath.Join(host.extensionDir(), themesync.VSCodeThemeDir, "notes.txt")
	if err := os.WriteFile(stray, []byte("mine\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, output := host.run(ModeUninstall)
	if !strings.Contains(output, "leave non-empty VS Code theme directory") {
		t.Fatalf("uninstall must report the kept theme directory:\n%s", output)
	}
	if got := contents(t, stray); got != "mine\n" {
		t.Fatalf("uninstall must leave a file it never wrote, got %q", got)
	}
	if names := host.renderedThemes(); strings.Join(names, ",") != "notes.txt" {
		t.Fatalf("uninstall must still remove every rendered theme and the index, left %q", names)
	}
	if !absent(t, filepath.Join(host.extensionDir(), "package.json")) {
		t.Fatalf("uninstall stopped at the theme directory: the extension's package.json is still staged\n%s", output)
	}
}

func TestVSCodeThemeRenderSkipsAThemeThatWillNotRenderByNameAndPath(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	root := managedRootForHome(home)
	good := themesync.ProfilePath(home, "good")
	broken := themesync.ProfilePath(home, "broken")
	gone := themesync.ProfilePath(home, "gone")
	writeFixture(t, good, plainProfile)
	writeFixture(t, broken, "{not json")
	records := map[string]themeOwnershipRecord{}
	for name, path := range map[string]string{"good": good, "broken": broken, "gone": gone} {
		records[profileLedgerKey(name)] = themeOwnershipRecord{Path: path, SHA256: contentSHA256([]byte(name))}
		claude := filepath.Join(home, ".claude", "themes", name+".json")
		records[name] = themeOwnershipRecord{Path: claude, SHA256: contentSHA256([]byte(name))}
	}
	if err := writeThemeOwnership(filepath.Join(root, themeOwnershipName), records); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	installer := &engine{options: Options{Home: home, Stdout: &output}, managedRoot: root}
	themes := installer.renderVSCodeThemes()
	if len(themes) != 1 || themes[0].ID != "professor-good" {
		t.Fatalf("only the readable theme renders, got %+v", themes)
	}
	report := output.String()
	for _, want := range []string{"VS Code theme broken NOT rendered from " + broken, "VS Code theme gone NOT rendered: " + gone + " is missing"} {
		if !strings.Contains(report, want) {
			t.Fatalf("report must name %q:\n%s", want, report)
		}
	}
	if installer.report.Skipped != 2 {
		t.Fatalf("two skipped themes, report %+v", installer.report)
	}
}

func TestVSCodeExtensionManifestMarkerIsFilledExactlyOnce(t *testing.T) {
	t.Parallel()
	content, err := readAsset(vscodeExtensionSource + "/package.json")
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := renderVSCodeExtensionManifest(content, []themesync.VSCodeThemeFile{
		{ID: "professor-a", Label: `Professor · "A"`, UITheme: "vs", Path: "./themes/professor-a.json"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(rendered, &manifest); err != nil {
		t.Fatalf("rendered package.json does not parse: %v\n%s", err, rendered)
	}
	if _, err := renderVSCodeExtensionManifest([]byte(`{}`), nil); err == nil ||
		!strings.Contains(err.Error(), "occurs 0 times") {
		t.Fatalf("a manifest without the marker must be refused, got %v", err)
	}
}

// TestVSCodeListsTheProfilesAloneNotAKeptClaudeFile: the Color Theme picker
// holds the Professor themes alone. A ledger record with no profile copy —
// the Claude file a removed fetched theme left, kept because an account still
// selects it — is no Professor theme, and renders none.
func TestVSCodeListsTheProfilesAloneNotAKeptClaudeFile(t *testing.T) {
	t.Parallel()
	host := newProfileHost(t)
	host.seed(map[string]string{"plain-lagoon": plainProfile})
	host.own("legacy-fetched", host.claudeFile("legacy-fetched"), legacyPalette)
	writeFixture(t, filepath.Join(host.home, ".cc", "1", "settings.json"), `{"theme":"custom:legacy-fetched"}`+"\n")

	_, output := host.run(ModeApply)

	if absent(t, host.claudeFile("legacy-fetched")) {
		t.Fatalf("a theme an account selects was retired:\n%s", output)
	}
	for _, theme := range host.contributed() {
		if theme.ID == "professor-legacy-fetched" {
			t.Fatalf("a Claude file with no profile copy was contributed to VS Code: %+v", theme)
		}
	}
	if got := host.contributed(); len(got) != 1 || got[0].ID != "professor-plain-lagoon" {
		t.Fatalf("contributes.themes = %+v, want the one profile\n%s", got, output)
	}
	if names := host.renderedThemes(); strings.Join(names, ",") != "index.json,professor-plain-lagoon.json" {
		t.Fatalf("rendered VS Code themes = %q, want the one profile's", names)
	}
}
