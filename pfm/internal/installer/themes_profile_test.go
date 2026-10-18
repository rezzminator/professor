package installer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/themesync"
)

// pinnedProfile carries per-surface pins beside the palette; the pin is a
// VS Code terminal colour, the one every pin validator must accept.
const pinnedProfile = `{
  "name": "Pinned Night",
  "base": "dark",
  "overrides": {"claude": "#ff5fa2", "promptBorder": "#ff8fc0"},
  "surfaces": {"vscode": {"colors": {"terminal.background": "#101820"}}}
}
`

// plainProfile is deliberately not in the encoder's shape (key order, tabs,
// no trailing space rules): a profile without pins reaches Claude as it is.
const plainProfile = "{\n\t\"overrides\": {\"claude\": \"#40e0d0\"},\n\t\"base\": \"dark\",\n\t\"name\": \"Plain Lagoon\"\n}\n"

// profileHost is a temp home beside a source clone whose manifest lists the
// profiles it is seeded with.
type profileHost struct {
	t          *testing.T
	home, repo string
}

func newProfileHost(t *testing.T) *profileHost {
	t.Helper()
	host := &profileHost{t: t, home: t.TempDir(), repo: t.TempDir()}
	recordFixtureSourceRepo(t, host.home, host.repo)
	return host
}

// seed rewrites the clone: the manifest lists exactly these bundled profiles
// (name -> file body) and the clone holds exactly their files.
func (host *profileHost) seed(profiles map[string]string) {
	host.t.Helper()
	names := make([]string, 0, len(profiles))
	for name := range profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	entries := make([]string, 0, len(names))
	for _, name := range names {
		entries = append(entries, fmt.Sprintf(
			`%q: {"file": %q, "target": "~/.claude/themes/%s.json", "activate": "/theme", "requires": "fixture"}`,
			name, name+".json", name,
		))
		writeFixture(host.t, filepath.Join(host.repo, "templates", "themes", name+".json"), profiles[name])
	}
	writeFixture(
		host.t, filepath.Join(host.repo, themeManifestRelative),
		`{"bundled": {`+strings.Join(entries, ",")+`}}`,
	)
}

func (host *profileHost) run(mode Mode) (Report, string) {
	host.t.Helper()
	var output bytes.Buffer
	report, err := Run(context.Background(), Options{
		MCPConfigPath: testConfigPath(host.t), Mode: mode, Home: host.home, SourceRepo: host.repo,
		Stdout: &output, Runner: &fakeRunner{nameSyncIdle: true}, CodexHomes: []string{}, InstallThemes: true,
	})
	if err != nil {
		host.t.Fatalf("theme install (mode %v): %v\n%s", mode, err, output.String())
	}
	return report, output.String()
}

func (host *profileHost) claudeFile(name string) string {
	return filepath.Join(host.home, ".claude", "themes", name+".json")
}

func (host *profileHost) ledgerPath() string {
	return filepath.Join(managedRootForHome(host.home), themeOwnershipName)
}

func (host *profileHost) ledger() map[string]themeOwnershipRecord {
	host.t.Helper()
	records, err := readThemeOwnership(host.ledgerPath())
	if err != nil {
		host.t.Fatalf("read ownership ledger: %v", err)
	}
	return records
}

// own plants a file install wrote earlier: the bytes on disk and its ledger record.
func (host *profileHost) own(key, path, body string) {
	host.t.Helper()
	writeFixture(host.t, path, body)
	records := host.ledger()
	records[key] = themeOwnershipRecord{Path: path, SHA256: contentSHA256([]byte(body))}
	if err := writeThemeOwnership(host.ledgerPath(), records); err != nil {
		host.t.Fatalf("seed ownership ledger: %v", err)
	}
}

// contents is a file's text, or a marker when it is not there, so a missing
// file fails the assertion that compares it instead of ending the test.
func contents(t *testing.T, path string) string {
	t.Helper()
	if absent(t, path) {
		return "<absent: " + path + ">"
	}
	return readFixture(t, path)
}

func absent(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Lstat(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("inspect %s: %v", path, err)
	}
	return err != nil
}

func profileKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func TestProfileWithSurfacesReachesClaudeAsThePaletteAloneAndKeepsTheWholeCopy(t *testing.T) {
	t.Parallel()
	host := newProfileHost(t)
	host.seed(map[string]string{"pinned-night": pinnedProfile})

	_, output := host.run(ModeApply)

	var claude map[string]json.RawMessage
	if err := json.Unmarshal([]byte(contents(t, host.claudeFile("pinned-night"))), &claude); err != nil {
		t.Fatalf("Claude theme file is not JSON: %v", err)
	}
	if got := profileKeys(claude); strings.Join(got, ",") != "base,name,overrides" {
		t.Fatalf("Claude theme file keys = %v, want only name, base and overrides\n%s", got, output)
	}
	if string(claude["name"]) != `"Pinned Night"` ||
		!strings.Contains(string(claude["overrides"]), `"promptBorder": "#ff8fc0"`) {
		t.Fatalf("Claude theme file lost palette content: %s", claude)
	}
	copyPath := themesync.ProfilePath(host.home, "pinned-night")
	if got := contents(t, copyPath); got != pinnedProfile {
		t.Fatalf("profile copy = %q, want the whole profile %q", got, pinnedProfile)
	}
	ledger := host.ledger()
	for key, path := range map[string]string{"pinned-night": host.claudeFile("pinned-night"), "profile:pinned-night": copyPath} {
		if record, owned := ledger[key]; !owned || record.Path != path ||
			record.SHA256 != contentSHA256([]byte(contents(t, path))) {
			t.Errorf("ledger[%s] = %+v (owned %v), want %s with its digest", key, record, owned, path)
		}
	}
	_, second := host.run(ModeApply)
	if !strings.Contains(second, "theme pinned-night unchanged at "+host.claudeFile("pinned-night")) {
		t.Fatalf("second run did not report the profile unchanged:\n%s", second)
	}
}

func TestProfileWithoutSurfacesIsWrittenVerbatim(t *testing.T) {
	t.Parallel()
	host := newProfileHost(t)
	host.seed(map[string]string{"plain-lagoon": plainProfile})

	host.run(ModeApply)

	if got := contents(t, host.claudeFile("plain-lagoon")); got != plainProfile {
		t.Fatalf("Claude theme file = %q, want the profile's own bytes %q", got, plainProfile)
	}
	if got := contents(t, themesync.ProfilePath(host.home, "plain-lagoon")); got != plainProfile {
		t.Fatalf("profile copy = %q, want %q", got, plainProfile)
	}
}

func TestProfileWithAnUnknownTopLevelKeyIsSkippedByNameAndTheRestInstalls(t *testing.T) {
	t.Parallel()
	host := newProfileHost(t)
	sloppy := `{"name":"Sloppy","base":"dark","overrides":{"claude":"#ffffff"},"extra":1}` + "\n"
	host.seed(map[string]string{"sloppy": sloppy, "plain-lagoon": plainProfile})

	_, output := host.run(ModeApply)

	if !strings.Contains(output, "skip    theme sloppy profile sloppy.json rejected:") ||
		!strings.Contains(output, `unknown field "extra"`) {
		t.Fatalf("a profile with an unknown key was not skipped by name:\n%s", output)
	}
	for _, path := range []string{host.claudeFile("sloppy"), themesync.ProfilePath(host.home, "sloppy")} {
		if !absent(t, path) {
			t.Errorf("rejected profile reached %s", path)
		}
	}
	if _, owned := host.ledger()["sloppy"]; owned {
		t.Errorf("rejected profile was recorded in the ledger")
	}
	if got := readFixture(t, host.claudeFile("plain-lagoon")); got != plainProfile {
		t.Fatalf("a good profile beside the rejected one was not installed: %q", got)
	}
}

func TestStaleOwnedThemeIsRetiredAndAnEditedOneStaysReported(t *testing.T) {
	t.Parallel()
	host := newProfileHost(t)
	host.seed(map[string]string{"plain-lagoon": plainProfile})
	host.run(ModeApply)
	copyOf := func(name string) string { return themesync.ProfilePath(host.home, name) }
	host.own("gone", host.claudeFile("gone"), "gone palette\n")
	host.own("profile:gone", copyOf("gone"), "gone profile\n")
	host.own("edited", host.claudeFile("edited"), "edited palette\n")
	host.own("profile:edited", copyOf("edited"), "edited profile\n")
	host.own("vanished", host.claudeFile("vanished"), "vanished palette\n")
	if err := os.Remove(host.claudeFile("vanished")); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, host.claudeFile("edited"), "the user's own edit\n")

	_, preview := host.run(ModeDryRun)
	if absent(t, host.claudeFile("gone")) || !strings.Contains(preview, "retire theme gone "+host.claudeFile("gone")) {
		t.Fatalf("a dry run must preview the retirement and remove nothing:\n%s", preview)
	}

	report, output := host.run(ModeApply)

	for _, path := range []string{host.claudeFile("gone"), copyOf("gone"), copyOf("edited")} {
		if !absent(t, path) {
			t.Errorf("retired unmodified file %s was kept", path)
		}
	}
	if !strings.Contains(output, "change  retire theme gone "+host.claudeFile("gone")) ||
		!strings.Contains(output, "change  retire theme gone profile copy "+copyOf("gone")) || report.Changed < 4 {
		t.Fatalf("retirement was not reported as changes (changed=%d):\n%s", report.Changed, output)
	}
	if got := readFixture(t, host.claudeFile("edited")); got != "the user's own edit\n" {
		t.Fatalf("an edited retired theme was removed or rewritten: %q", got)
	}
	if !strings.Contains(output, "skip    theme edited no longer in the manifest and locally modified; left in place"+
		" and retained recovery ownership at "+host.claudeFile("edited")) {
		t.Fatalf("an edited retired theme was not reported as a skip naming it:\n%s", output)
	}
	if got := readFixture(t, host.claudeFile("plain-lagoon")); got != plainProfile {
		t.Fatalf("a listed theme was touched: %q", got)
	}
	want := []string{"edited", "plain-lagoon", "profile:plain-lagoon"}
	if got := profileKeys(host.ledger()); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("ledger keys = %v, want %v (a missing file just drops its record)", got, want)
	}
}

func TestRetiringAThemeAnAccountStillSelectsKeepsItAndNamesTheAccount(t *testing.T) {
	t.Parallel()
	host := newProfileHost(t)
	host.seed(map[string]string{"plain-lagoon": plainProfile})
	host.run(ModeApply)
	copyOf := func(name string) string { return themesync.ProfilePath(host.home, name) }
	host.own("chosen", host.claudeFile("chosen"), "chosen palette\n")
	host.own("profile:chosen", copyOf("chosen"), "chosen profile\n")
	host.own("unchosen", host.claudeFile("unchosen"), "unchosen palette\n")
	settings := filepath.Join(host.home, ".cc", "1", "settings.json")
	writeFixture(t, filepath.Join(host.home, ".claude", "settings.json"), `{"theme":"dark"}`+"\n")
	writeFixture(t, settings, `{"theme":"custom:chosen"}`+"\n")
	settings, err := filepath.EvalSymlinks(settings)
	if err != nil {
		t.Fatal(err)
	}
	want := "skip    theme chosen is no longer in the manifest but " + settings + " still selects custom:chosen;" +
		" left in place: select another theme with `pfm theme use`, then install again to retire it"

	_, preview := host.run(ModeDryRun)
	if !strings.Contains(preview, want) || strings.Contains(preview, "retire theme chosen ") {
		t.Fatalf("the preview must name the selecting account and preview no retirement of chosen:\n%s", preview)
	}

	_, output := host.run(ModeApply)

	for _, path := range []string{host.claudeFile("chosen"), copyOf("chosen")} {
		if absent(t, path) {
			t.Errorf("a theme an account selects was retired: %s removed", path)
		}
	}
	if !strings.Contains(output, want) {
		t.Fatalf("the kept theme was not reported naming the selecting account:\n%s", output)
	}
	if !absent(t, host.claudeFile("unchosen")) {
		t.Errorf("an unselected stale theme was kept")
	}
	ledger := host.ledger()
	for _, key := range []string{"chosen", "profile:chosen"} {
		if _, owned := ledger[key]; !owned {
			t.Errorf("ledger dropped %s while its file stays", key)
		}
	}
}

func TestRetireKeepsTheThemeWhenAnAccountCannotBeRead(t *testing.T) {
	t.Parallel()
	host := newProfileHost(t)
	host.seed(map[string]string{"plain-lagoon": plainProfile})
	host.run(ModeApply)
	host.own("stale", host.claudeFile("stale"), "stale palette\n")
	if err := os.MkdirAll(filepath.Join(host.home, ".cc", "9", "settings.json"), 0o700); err != nil {
		t.Fatal(err) // a settings.json that is a directory: no read of it can succeed
	}

	_, output := host.run(ModeApply)

	if absent(t, host.claudeFile("stale")) {
		t.Fatalf("a theme was retired although an account could not be read:\n%s", output)
	}
	if !strings.Contains(output, "skip    theme stale retire check failed, left in place: ") {
		t.Fatalf("the failed selection check was not reported:\n%s", output)
	}
}

func TestByteIdenticalUnownedThemeIsAdoptedAndADifferingOneIsKept(t *testing.T) {
	t.Parallel()
	host := newProfileHost(t)
	host.seed(map[string]string{"plain-lagoon": plainProfile, "pinned-night": pinnedProfile, "stranger": plainProfile})
	profile, err := themesync.ParseProfile([]byte(pinnedProfile))
	if err != nil {
		t.Fatal(err)
	}
	palette, err := profile.ClaudeFile([]byte(pinnedProfile))
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, host.claudeFile("plain-lagoon"), plainProfile)
	writeFixture(t, host.claudeFile("pinned-night"), string(palette))
	writeFixture(t, host.claudeFile("stranger"), "somebody else's theme\n")

	report, output := host.run(ModeApply)

	for _, name := range []string{"plain-lagoon", "pinned-night"} {
		if !strings.Contains(output, "ok      theme "+name+" adopted: "+host.claudeFile(name)) {
			t.Errorf("identical unowned %s was not adopted:\n%s", name, output)
		}
		if record, owned := host.ledger()[name]; !owned || record.Path != host.claudeFile(name) {
			t.Errorf("ledger[%s] = %+v (owned %v), want the adopted file", name, record, owned)
		}
		if absent(t, themesync.ProfilePath(host.home, name)) {
			t.Errorf("adopting %s did not write its profile copy", name)
		}
	}
	if got := readFixture(t, host.claudeFile("stranger")); got != "somebody else's theme\n" {
		t.Fatalf("a differing unowned file was overwritten: %q", got)
	}
	if !strings.Contains(output, "skip    theme stranger already exists but is not installer-owned; left in place at "+
		host.claudeFile("stranger")) {
		t.Fatalf("a differing unowned file was not skipped by name:\n%s", output)
	}
	if _, owned := host.ledger()["stranger"]; owned || report.Skipped == 0 {
		t.Fatalf("differing unowned file entered the ledger (owned %v) or no skip counted: %+v", owned, report)
	}
	if _, second := host.run(ModeApply); !strings.Contains(second, "theme plain-lagoon unchanged at") {
		t.Fatalf("an adopted theme is not unchanged on the next run:\n%s", second)
	}
}

func TestPreviewNamesAnUnownedProfileCopyTheApplySkipsOver(t *testing.T) {
	t.Parallel()
	host := newProfileHost(t)
	host.seed(map[string]string{"plain-lagoon": plainProfile})
	copyPath := themesync.ProfilePath(host.home, "plain-lagoon")
	writeFixture(t, copyPath, "somebody else's profile\n")

	_, preview := host.run(ModeDryRun)

	if !strings.Contains(preview, "ok      theme plain-lagoon profile copy exists at "+copyPath+
		" and is not installer-owned; apply adopts it only when byte-identical to the install, else skips the theme") {
		t.Fatalf("the preview did not name the unowned profile copy the apply will skip over:\n%s", preview)
	}
	_, output := host.run(ModeApply)
	if !strings.Contains(output, "skip    theme plain-lagoon profile copy already exists but is not installer-owned;"+
		" left in place at "+copyPath) {
		t.Fatalf("the apply did not skip the theme its preview warned about:\n%s", output)
	}
	if got := readFixture(t, copyPath); got != "somebody else's profile\n" {
		t.Fatalf("the unowned profile copy was overwritten: %q", got)
	}
}

func TestPreviewSkipsAThemeWhoseOwnedProfileCopyWasEdited(t *testing.T) {
	t.Parallel()
	host := newProfileHost(t)
	host.seed(map[string]string{"plain-lagoon": plainProfile})
	host.run(ModeApply)
	copyPath := themesync.ProfilePath(host.home, "plain-lagoon")
	writeFixture(t, copyPath, "the user's own edit\n")
	want := "skip    theme plain-lagoon profile copy locally modified; left in place at " + copyPath

	_, preview := host.run(ModeDryRun)
	_, output := host.run(ModeApply)

	if !strings.Contains(preview, want) || !strings.Contains(output, want) {
		t.Fatalf("preview and apply must both skip the edited profile copy\npreview:\n%s\napply:\n%s", preview, output)
	}
}

func TestOverlayCarriesItsOwnSurfacesAndNeverTheBases(t *testing.T) {
	t.Parallel()
	base := `{"name":"Base","base":"dark","overrides":{"claude":"#111111"},"surfaces":{"tmux":{"status-style":"bg=#000000"}}}`
	pinned := `{"name":"Pinned Night","overrides":{"promptBorder":"#ff8fc0"},` +
		`"surfaces":{"vscode":{"colors":{"terminal.background":"#101820"}}}}`
	merged, err := mergeThemeOverlay([]byte(base), []byte(pinned))
	if err != nil {
		t.Fatal(err)
	}
	profile, err := themesync.ParseProfile(merged)
	if err != nil {
		t.Fatalf("merged overlay is not a profile: %v\n%s", err, merged)
	}
	if profile.Surfaces == nil || profile.Surfaces.VSCode == nil ||
		profile.Surfaces.VSCode.Colors["terminal.background"] != "#101820" || profile.Surfaces.Tmux != nil {
		t.Fatalf("merged surfaces = %+v, want the overlay's vscode pin and none of the base's", profile.Surfaces)
	}
	if profile.Overrides["claude"] != "#111111" || profile.Overrides["promptBorder"] != "#ff8fc0" {
		t.Fatalf("merged overrides = %v", profile.Overrides)
	}
	plain, err := mergeThemeOverlay([]byte(base), []byte(`{"overrides":{"promptBorder":"#ff8fc0"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(plain, []byte("surfaces")) {
		t.Fatalf("a base's pins leaked into an overlay without any:\n%s", plain)
	}
}

func TestUninstallRemovesTheProfileCopiesAndTheirDirectory(t *testing.T) {
	t.Parallel()
	host := newProfileHost(t)
	host.seed(map[string]string{"pinned-night": pinnedProfile, "plain-lagoon": plainProfile})
	host.run(ModeApply)
	copyOf := func(name string) string { return themesync.ProfilePath(host.home, name) }
	writeFixture(t, copyOf("plain-lagoon"), "the user's edit of the copy\n")

	_, output := host.run(ModeUninstall)

	if !absent(t, copyOf("pinned-night")) || !absent(t, host.claudeFile("pinned-night")) {
		t.Errorf("uninstall kept an owned, unmodified profile or its copy:\n%s", output)
	}
	if got := readFixture(t, copyOf("plain-lagoon")); got != "the user's edit of the copy\n" ||
		!strings.Contains(output, "skip    theme plain-lagoon profile copy locally modified; left in place") {
		t.Fatalf("an edited profile copy was removed or the skip is not named (%q):\n%s", got, output)
	}
}

// The real clone's manifest, read the way pfm install reads it: six complete
// bundled profiles that parse, and every file beside the manifest is listed.
func TestSourceCloneManifestLoadsAllSixProfiles(t *testing.T) {
	t.Parallel()
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test file")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(testFile), "..", "..", ".."))
	sources, err := loadThemeSources(context.Background(), Options{
		SourceRepo:       repoRoot,
		ThemeManifestURL: "https://raw.githubusercontent.com/example/professor/main/" + themeManifestRelative,
	})
	if err != nil {
		t.Fatalf("load the clone's theme manifest: %v", err)
	}
	profiles := []string{"hibiscus-night", "lagoon", "mirrorball", "neon-sunset", "ultrafunk", "velvet-groove"}
	var bundled []string
	for name, source := range sources {
		if source.local != "" { // read from the clone: a bundled profile
			bundled = append(bundled, name)
		}
	}
	sort.Strings(bundled)
	if strings.Join(bundled, ",") != strings.Join(profiles, ",") {
		t.Fatalf("bundled themes = %v, want %v", bundled, profiles)
	}
	if tokyo := sources["tokyo-night"]; tokyo.local != "" || tokyo.Raw == "" {
		t.Fatalf("tokyo-night = %+v, want it to stay a source-fetched theme", tokyo)
	}
	files, err := filepath.Glob(filepath.Join(repoRoot, "templates", "themes", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != len(profiles)+1 { // + sources.json
		t.Errorf(
			"templates/themes holds %d .json files, want the manifest and %d profiles: %v",
			len(files),
			len(profiles),
			files,
		)
	}
	home := t.TempDir()
	var output bytes.Buffer
	installer := &engine{
		options: Options{
			Mode:             ModeApply,
			Home:             home,
			SourceRepo:       repoRoot,
			Stdout:           &output,
			InstallThemes:    true,
			ThemesOffline:    true,
			ThemeManifestURL: "https://raw.githubusercontent.com/example/professor/main/" + themeManifestRelative,
		},
		apply: true, managedRoot: managedRootForHome(home),
	}
	installer.installThemes(context.Background())
	for _, name := range profiles {
		source := sources[name]
		if source.base != "" || filepath.Base(source.local) != name+".json" {
			t.Errorf("%s = %+v, want a complete palette in %s.json", name, source, name)
		}
		body := readFixture(t, source.local)
		if _, err := themesync.ParseProfile([]byte(body)); err != nil {
			t.Errorf("%s does not parse as a profile: %v", name, err)
		}
		if got := contents(t, filepath.Join(home, ".claude", "themes", name+".json")); got != body {
			t.Errorf("%s: installed Claude theme differs from the profile file", name)
		}
		if got := contents(t, themesync.ProfilePath(home, name)); got != body {
			t.Errorf("%s: installed profile copy differs from the profile file", name)
		}
	}
	if strings.Contains(output.String(), "professor-") {
		t.Errorf("a medal overlay is still named in the install report:\n%s", output.String())
	}
}
