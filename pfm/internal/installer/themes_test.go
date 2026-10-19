package installer

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/updatecheck"
)

// releaseThemeClient serves a release the way GitHub does: each profile as a
// raw file, and the directory listing through the contents API. Any other URL
// is a 404, so a fetch install should not make fails loud.
func releaseThemeClient(t *testing.T, ref string, profiles map[string]string, listing string) *http.Client {
	t.Helper()
	raw := releaseThemeURL(ref)
	served := map[string]string{
		"https://api.github.com/repos/o/r/contents/templates/themes?ref=" + ref: listing,
	}
	for file, body := range profiles {
		served[raw+file] = body
	}
	return &http.Client{Transport: themeRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		body, ok := served[request.URL.String()]
		status := http.StatusOK
		if !ok {
			status, body = http.StatusNotFound, "not found"
		}
		return &http.Response{
			StatusCode: status, Status: http.StatusText(status), Request: request,
			Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{},
		}, nil
	})}
}

// releaseThemeURL is the fixture release's templates/themes/ URL at ref.
func releaseThemeURL(ref string) string {
	return "https://raw.githubusercontent.com/o/r/" + ref + "/templates/themes/"
}

func TestProfileInstallsFromSourceCloneThenFromTheReleaseListing(t *testing.T) {
	t.Parallel()
	themeBody := []byte(`{"name":"Sonar Gold","base":"dark","overrides":{"claude":"#ffd60a"}}` + "\n")
	run := func(home, sourceRepo, releaseURL string, client *http.Client) (Report, string, error) {
		var output bytes.Buffer
		report, err := Run(context.Background(), Options{
			MCPConfigPath:   testConfigPath(t),
			Mode:            ModeApply,
			Home:            home,
			SourceRepo:      sourceRepo,
			ThemeReleaseURL: releaseURL,
			ThemeHTTPClient: client,
			Stdout:          &output,
			Runner:          &fakeRunner{nameSyncIdle: true},
			CodexHomes:      []string{},
			InstallThemes:   true,
		})
		return report, output.String(), err
	}

	// 1. the clone holds the profile beside its README: discovered, installed, idempotent.
	home := t.TempDir()
	sourceRepo := t.TempDir()
	recordFixtureSourceRepo(t, home, sourceRepo)
	writeFixture(t, filepath.Join(sourceRepo, "templates", "themes", "sonar-gold.json"), string(themeBody))
	writeFixture(t, filepath.Join(sourceRepo, "templates", "themes", "README.md"), "not a profile\n")
	target := filepath.Join(home, ".claude", "themes", "sonar-gold.json")
	first, firstOutput, err := run(home, sourceRepo, "", nil)
	if err != nil {
		t.Fatalf("discovered profile install: %v\n%s", err, firstOutput)
	}
	if got := []byte(readFixture(t, target)); !bytes.Equal(got, themeBody) {
		t.Fatalf("installed profile=%q, want %q", got, themeBody)
	}
	if first.Changed == 0 || !strings.Contains(firstOutput, "write theme sonar-gold -> "+target) {
		t.Fatalf("first report=%#v, want a named profile write\n%s", first, firstOutput)
	}
	if strings.Contains(firstOutput, "theme README") {
		t.Fatalf("a file beside the profiles was taken for one:\n%s", firstOutput)
	}
	second, secondOutput, err := run(home, sourceRepo, "", nil)
	if err != nil || second.Changed != 0 || !strings.Contains(secondOutput, "theme sonar-gold unchanged") {
		t.Fatalf("second run err=%v report=%#v, want changed=0 and an unchanged row\n%s", err, second, secondOutput)
	}

	// 2. no templates/themes/ in the clone: the release's listing names the profiles, each fetched from there.
	listing := `[{"name":"README.md","type":"file"},` +
		`{"name":"sonar-gold.json","type":"file"},{"name":"nested.json","type":"dir"}]`
	client := releaseThemeClient(t, "v1.0.0", map[string]string{"sonar-gold.json": string(themeBody)}, listing)
	releaseHome := t.TempDir()
	releaseURL := releaseThemeURL("v1.0.0")
	_, releaseOutput, err := run(releaseHome, t.TempDir(), releaseURL, client)
	if err != nil {
		t.Fatalf("release-listing profile install: %v\n%s", err, releaseOutput)
	}
	got := []byte(readFixture(t, filepath.Join(releaseHome, ".claude", "themes", "sonar-gold.json")))
	if !bytes.Equal(got, themeBody) || strings.Contains(releaseOutput, "theme nested") {
		t.Fatalf("release-listing profile=%q, want %q and no directory taken for a profile\n%s",
			got, themeBody, releaseOutput)
	}

	// 3. the listing is unreachable: the whole theme step names it, never an empty set of profiles.
	broken := releaseThemeClient(t, "v1.0.0", nil, "")
	_, brokenOutput, err := run(t.TempDir(), t.TempDir(), releaseURL, broken)
	if err != nil {
		t.Fatalf("broken listing aborted host install: %v\n%s", err, brokenOutput)
	}
	unavailable := "themes NOT installed: load " + themeProfilesRelative + ": local themes unavailable: "
	if !strings.Contains(brokenOutput, unavailable) ||
		!strings.Contains(brokenOutput,
			"; list release profiles: decode https://api.github.com/repos/o/r/contents/templates/themes?ref=v1.0.0") {
		t.Fatalf("an unreadable release listing was silent or vague:\n%s", brokenOutput)
	}
}

func TestThemeListingURLAcceptsOnlyARawReleaseThemeURL(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ release, want, refusal string }{
		{
			release: ThemeReleaseURL("1.2.3"),
			want:    "https://api.github.com/repos/" + updatecheck.ProfessorRepo + "/contents/templates/themes?ref=1.2.3",
		},
		{
			release: "https://raw.githubusercontent.com/o/r/release/v2/" + themeProfilesRelative,
			want:    "https://api.github.com/repos/o/r/contents/templates/themes?ref=release%2Fv2",
		},
		{release: "https://example.test/o/r/main/" + themeProfilesRelative + "/", refusal: "is not https://raw.githubusercontent.com/"},
		{release: "https://raw.githubusercontent.com/o/r/main/themes/", refusal: "is not https://raw.githubusercontent.com/"},
		{release: "https://raw.githubusercontent.com/" + themeProfilesRelative + "/", refusal: "is not https://raw.githubusercontent.com/"},
	} {
		got, err := themeListingURL(tc.release)
		if tc.refusal != "" {
			if err == nil || !strings.Contains(err.Error(), tc.refusal) || !strings.Contains(err.Error(), tc.release) {
				t.Errorf("themeListingURL(%s) = %q, %v; want a refusal naming it", tc.release, got, err)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("themeListingURL(%s) = %q, %v; want %q", tc.release, got, err, tc.want)
		}
	}
}

func TestOfflineThemesInstallTheCloneProfilesWithoutAFetch(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	sourceRepo := t.TempDir()
	recordFixtureSourceRepo(t, home, sourceRepo)
	palette := `{"name":"Local","base":"dark","overrides":{"claude":"#ffd60a"}}`
	writeFixture(t, filepath.Join(sourceRepo, "templates", "themes", "local.json"), palette)
	client := &http.Client{Transport: themeRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		t.Errorf("an offline install from the clone fetched %s", request.URL)
		return nil, fmt.Errorf("fixture transport refuses %s", request.URL)
	})}
	var output bytes.Buffer
	_, err := Run(context.Background(), Options{
		MCPConfigPath: testConfigPath(t), Mode: ModeApply, Home: home, SourceRepo: sourceRepo,
		Stdout: &output, Runner: &fakeRunner{nameSyncIdle: true}, CodexHomes: []string{},
		InstallThemes: true, ThemesOffline: true, ThemeHTTPClient: client,
	})
	if err != nil {
		t.Fatalf("offline theme install: %v\n%s", err, output.String())
	}
	if got := readFixture(t, filepath.Join(home, ".claude", "themes", "local.json")); got != palette {
		t.Fatalf("bundled palette = %q, want %q", got, palette)
	}
	if !strings.Contains(output.String(), "write theme local") {
		t.Fatalf("offline theme report lacks the profile write:\n%s", output.String())
	}
}

func TestThemesDryRunOffline(t *testing.T) {
	for _, offline := range []bool{true, false} {
		t.Run(fmt.Sprint("offline=", offline), func(t *testing.T) {
			home, sourceRepo := t.TempDir(), t.TempDir()
			writeFixture(
				t,
				filepath.Join(sourceRepo, "templates", "themes", "local.json"),
				`{"name":"Local","base":"dark","overrides":{"claude":"#ffd60a"}}`,
			)
			var output bytes.Buffer
			installer := &engine{options: Options{
				Mode: ModeDryRun, Home: home, SourceRepo: sourceRepo, Stdout: &output,
				InstallThemes: true, ThemesOffline: offline,
			}, managedRoot: filepath.Join(home, "install")}
			installer.installThemes(context.Background())
			target := filepath.Join(home, ".claude", "themes", "local.json")
			want := "  change  read bundled theme local -> " + target + "\n"
			if output.String() != want || installer.report.Changed != 1 || installer.report.Skipped != 0 {
				t.Fatalf(
					"report = %+v, output = %q; want changed = 1, skipped = 0, output = %q",
					installer.report,
					output.String(),
					want,
				)
			}
		})
	}
}

func TestOfflineThemesWithoutCloneProfilesNameSkip(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	sourceRepo := t.TempDir()
	client := &http.Client{Transport: themeRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		t.Errorf("an offline install fetched %s", request.URL)
		return nil, fmt.Errorf("fixture transport refuses %s", request.URL)
	})}
	var output bytes.Buffer
	_, err := Run(context.Background(), Options{
		MCPConfigPath: testConfigPath(t), Mode: ModeApply, Home: home, SourceRepo: sourceRepo,
		ThemeReleaseURL: releaseThemeURL("v1.0.0"),
		Stdout:          &output, Runner: &fakeRunner{nameSyncIdle: true}, CodexHomes: []string{},
		InstallThemes: true, ThemesOffline: true, ThemeHTTPClient: client,
	})
	if err != nil {
		t.Fatalf("offline install without clone profiles: %v\n%s", err, output.String())
	}
	if !strings.Contains(output.String(), "themes NOT installed: load "+themeProfilesRelative) ||
		!strings.Contains(output.String(), "fetch skipped: PFM_THEMES_OFFLINE=1") {
		t.Fatalf("offline report lacks the named skip:\n%s", output.String())
	}
}

func TestThemeDiscoveryFailsClosedByName(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		profiles []string
		symlink  string
		want     string
	}{
		{"profile stem leaves the bare-name law", []string{"a:b.json"}, "", `profile a:b.json: name must be a bare profile name`},
		{"profile name collides with a ledger key", []string{"profile:x.json"}, "", `profile profile:x.json: name must be a bare profile name`},
		{"symlinked profile", nil, "linked.json", `profile linked.json in `},
		{"nothing to install", []string{"README.md"}, "", `no profile in `},
	} {
		sourceRepo := t.TempDir()
		dir := filepath.Join(sourceRepo, "templates", "themes")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, file := range tc.profiles {
			writeFixture(t, filepath.Join(dir, file), `{"name":"X","overrides":{}}`)
		}
		if tc.symlink != "" {
			writeFixture(t, filepath.Join(sourceRepo, "elsewhere.json"), `{"name":"X","overrides":{}}`)
			if err := os.Symlink(
				filepath.Join(sourceRepo, "elsewhere.json"),
				filepath.Join(dir, tc.symlink),
			); err != nil {
				t.Fatal(err)
			}
		}
		_, err := loadThemeProfiles(context.Background(), Options{SourceRepo: sourceRepo})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err=%v, want it to contain %q", tc.name, err, tc.want)
		}
	}

	home := t.TempDir()
	sourceRepo := t.TempDir()
	writeFixture(t, filepath.Join(sourceRepo, "templates", "themes", "x.json"), "not json\n")
	var output bytes.Buffer
	_, err := Run(context.Background(), Options{
		MCPConfigPath: testConfigPath(t),
		Mode:          ModeApply, Home: home, SourceRepo: sourceRepo, Stdout: &output,
		Runner: &fakeRunner{nameSyncIdle: true}, CodexHomes: []string{}, InstallThemes: true,
	})
	if err != nil {
		t.Fatalf("non-JSON profile aborted host install: %v\n%s", err, output.String())
	}
	if !strings.Contains(output.String(), "theme x read failed") ||
		!strings.Contains(output.String(), "is not valid JSON") {
		t.Fatalf("non-JSON profile was silent or vague:\n%s", output.String())
	}
	if _, statErr := os.Stat(filepath.Join(home, ".claude", "themes", "x.json")); !os.IsNotExist(statErr) {
		t.Fatalf("non-JSON profile was installed anyway: %v", statErr)
	}
}

// TestThemeReleaseUnpublishedAlphaReturnsNamedRefusal is a REGRESSION test
// for the 2026-09-14 retro finding: `pfm install --yes` run outside the
// source checkout falls back to a release theme URL built from an -alpha
// VERSION, which pfm never publishes and which therefore 404s.
// loadThemeProfiles must recognise the -alpha reference in the URL and return
// a named refusal instead of surfacing a bare HTTP error — and must never even
// attempt the doomed fetch. FAILS on unfixed code because the URL is fetched
// unconditionally and the error is a bare "fetch failed"/network error, not
// the named refusal.
func TestThemeReleaseUnpublishedAlphaReturnsNamedRefusal(t *testing.T) {
	t.Parallel()
	_, err := loadThemeProfiles(context.Background(), Options{
		ThemeReleaseURL: "https://raw.githubusercontent.com/example/professor/0.78.0-alpha/templates/themes/",
	})
	if err == nil {
		t.Fatal("loadThemeProfiles() error = nil, want a named refusal for an unpublished -alpha release")
	}
	want := "release themes for an unpublished -alpha build; run pfm install from the source clone"
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("loadThemeProfiles() error = %v, want it to contain %q", err, want)
	}
}

// TestThemeSourceRepoWithoutProfilesRefusesUnpublishedAlpha covers the
// SourceRepo branch of the same refusal: a source repository with no
// templates/themes/ falls back to the release URL, and an -alpha URL there
// must return the named refusal carrying the local error — never a fetch of
// the URL pfm never publishes.
func TestThemeSourceRepoWithoutProfilesRefusesUnpublishedAlpha(t *testing.T) {
	t.Parallel()
	client := &http.Client{Transport: themeRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		t.Errorf("fetched the unpublished -alpha release %s", request.URL)
		return nil, fmt.Errorf("fixture transport refuses %s", request.URL)
	})}
	_, err := loadThemeProfiles(context.Background(), Options{
		SourceRepo:      t.TempDir(),
		ThemeReleaseURL: "https://raw.githubusercontent.com/example/professor/0.78.0-alpha/templates/themes/",
		ThemeHTTPClient: client,
	})
	if err == nil {
		t.Fatal("loadThemeProfiles() error = nil, want a named refusal for an unpublished -alpha release")
	}
	for _, want := range []string{
		"release themes for an unpublished -alpha build; run pfm install from the source clone",
		"local themes unavailable",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("loadThemeProfiles() error = %v, want it to contain %q", err, want)
		}
	}
}

type themeRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn themeRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

// TestThemePreviewLabelsBundledPaletteAsReadNotFetch is a REGRESSION test for
// the 2026-09-14 retro finding: the preview change line for a bundled
// palette read from the source clone said "fetch theme X -> target", the
// same verb used for a profile downloaded from the release. A bundled
// palette is read from disk (loadThemeContent, source.local != ""), so its
// preview line must say "read bundled theme X -> target". FAILS on unfixed
// code because the preview line always says "fetch theme X -> target".
func TestThemePreviewLabelsBundledPaletteAsReadNotFetch(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	sourceRepo := t.TempDir()
	writeFixture(
		t,
		filepath.Join(sourceRepo, "templates", "themes", "sonar-gold.json"),
		`{"name":"Sonar Gold","overrides":{"accent":"#fff"}}`+"\n",
	)
	var output bytes.Buffer
	_, err := Run(context.Background(), Options{
		MCPConfigPath: testConfigPath(t),
		Mode:          ModeDryRun, Home: home, SourceRepo: sourceRepo, Stdout: &output,
		Runner: &fakeRunner{nameSyncIdle: true}, CodexHomes: []string{}, InstallThemes: true,
	})
	if err != nil {
		t.Fatalf("bundled theme preview: %v\n%s", err, output.String())
	}
	target := filepath.Join(home, ".claude", "themes", "sonar-gold.json")
	want := "read bundled theme sonar-gold -> " + target
	if !strings.Contains(output.String(), want) {
		t.Fatalf("preview output = %q, want the read-bundled label %q", output.String(), want)
	}
	if strings.Contains(output.String(), "fetch theme sonar-gold") {
		t.Fatalf("preview output still labels the bundled palette as fetched:\n%s", output.String())
	}
}

// TestFetchThemeWritesAnHTTPOutRecordForConstructedAndInjectedClients pins
// the installer's http.out door (spec § Middleware): the theme fetch leaves
// one comp=http.out record whether it built its own client or was handed one,
// and the query string never reaches the log.
func TestFetchThemeWritesAnHTTPOutRecordForConstructedAndInjectedClients(t *testing.T) {
	_, recorder := obs.Test(t)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		if _, err := io.WriteString(response, `{"name":"Sonar Gold"}`); err != nil {
			t.Errorf("write theme: %v", err)
		}
	}))
	defer server.Close()
	for _, client := range []*http.Client{nil, server.Client()} {
		content, err := fetchTheme(
			context.Background(), client, server.URL+"/themes/sonar-gold.json?token=THEMESECRET",
		)
		if err != nil || string(content) != `{"name":"Sonar Gold"}` {
			t.Fatalf("fetchTheme = %q, %v", content, err)
		}
	}
	records := recorder.Records()
	if len(records) != 2 {
		t.Fatalf("records = %d, want one http.out record per fetch: %s", len(records), recorder.Raw())
	}
	for _, record := range records {
		for key, want := range map[string]any{
			obs.FieldComp: "http.out", "op": "request", "method": http.MethodGet, "path": "/themes/sonar-gold.json",
			"status": float64(http.StatusOK), "bytes": float64(len(`{"name":"Sonar Gold"}`)),
		} {
			if got, _ := record.Field(key); got != want {
				t.Fatalf("http.out record %s = %v, want %v: %v", key, got, want, record.Fields)
			}
		}
	}
	if strings.Contains(recorder.Raw(), "THEMESECRET") {
		t.Fatalf("the query string reached the activity log: %s", recorder.Raw())
	}
}

// TestThemeEditedAfterInstallIsLeftInPlace: a hibiscus-night.json whose SHA
// differs from the ownership record is the user's now — install, whose source
// changed, names it locally modified and never rewrites it. The theme is Claude
// Code's own file, which pfm theme only reads: nothing holds it back.
func TestThemeEditedAfterInstallIsLeftInPlace(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	sourceRepo := t.TempDir()
	recordFixtureSourceRepo(t, home, sourceRepo)
	sourceFile := filepath.Join(sourceRepo, "templates", "themes", "hibiscus-night.json")
	target := filepath.Join(home, ".claude", "themes", "hibiscus-night.json")
	first := `{"name":"Gold","base":"dark","overrides":{"claude":"#ffd60a"}}`
	edited := `{"name":"Gold","base":"dark","overrides":{"claude":"#7aa2f7"}}`
	second := `{"name":"Gold","base":"dark","overrides":{"claude":"#ffcc00"}}`
	run := func() string {
		var output bytes.Buffer
		if _, err := Run(context.Background(), Options{
			MCPConfigPath: testConfigPath(t), Mode: ModeApply, Home: home, SourceRepo: sourceRepo,
			Stdout: &output, Runner: &fakeRunner{nameSyncIdle: true}, CodexHomes: []string{},
			InstallThemes: true,
		}); err != nil {
			t.Fatalf("theme install: %v\n%s", err, output.String())
		}
		return output.String()
	}
	writeFixture(t, sourceFile, first)
	run()
	writeFixture(t, target, edited)
	writeFixture(t, sourceFile, second)

	output := run()
	if got := readFixture(t, target); got != edited {
		t.Fatalf("edited theme rewritten to %q, want it left at %q\n%s", got, edited, output)
	}
	if !strings.Contains(output, "theme hibiscus-night locally modified; left in place at "+target) {
		t.Fatalf("locally modified row missing:\n%s", output)
	}
}
