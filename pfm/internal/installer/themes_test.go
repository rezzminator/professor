package installer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

func TestBundledThemeInstallsFromSourceRepoThenReleaseAndReportsAMissingFile(t *testing.T) {
	t.Parallel()
	themeBody := []byte(`{"name":"Sonar Gold","base":"dark","overrides":{"claude":"#ffd60a"}}` + "\n")
	manifest := `{"bundled":{"sonar-gold":{"file":"sonar-gold.json","target":"~/.claude/themes/sonar-gold.json","activate":"/theme","requires":"fixture"}}}`
	run := func(home, sourceRepo, manifestURL string) (Report, string, error) {
		var output bytes.Buffer
		report, err := Run(context.Background(), Options{
			MCPConfigPath:    testConfigPath(t),
			Mode:             ModeApply,
			Home:             home,
			SourceRepo:       sourceRepo,
			ThemeManifestURL: manifestURL,
			Stdout:           &output,
			Runner:           &fakeRunner{nameSyncIdle: true},
			CodexHomes:       []string{},
			InstallThemes:    true,
		})
		return report, output.String(), err
	}

	// 1. source clone carries the manifest and the file: installed, owned, idempotent.
	home := t.TempDir()
	sourceRepo := t.TempDir()
	recordFixtureSourceRepo(t, home, sourceRepo)
	writeFixture(t, filepath.Join(sourceRepo, "templates", "themes", "sources.json"), manifest)
	writeFixture(t, filepath.Join(sourceRepo, "templates", "themes", "sonar-gold.json"), string(themeBody))
	target := filepath.Join(home, ".claude", "themes", "sonar-gold.json")
	first, firstOutput, err := run(home, sourceRepo, "")
	if err != nil {
		t.Fatalf("bundled theme install: %v\n%s", err, firstOutput)
	}
	if got := []byte(readFixture(t, target)); !bytes.Equal(got, themeBody) {
		t.Fatalf("installed bundled theme=%q, want %q", got, themeBody)
	}
	if first.Changed == 0 || !strings.Contains(firstOutput, "theme sonar-gold") {
		t.Fatalf("first report=%#v, want a named bundled theme write\n%s", first, firstOutput)
	}
	second, secondOutput, err := run(home, sourceRepo, "")
	if err != nil || second.Changed != 0 || !strings.Contains(secondOutput, "theme sonar-gold unchanged") {
		t.Fatalf("second run err=%v report=%#v, want changed=0 and an unchanged row\n%s", err, second, secondOutput)
	}

	// 2. the file is missing from the clone: a loud read failure, never an absence, and the install continues.
	if err := os.Remove(filepath.Join(sourceRepo, "templates", "themes", "sonar-gold.json")); err != nil {
		t.Fatal(err)
	}
	_, missingOutput, err := run(t.TempDir(), sourceRepo, "")
	if err != nil {
		t.Fatalf("missing bundled file aborted host install: %v\n%s", err, missingOutput)
	}
	if !strings.Contains(missingOutput, "theme sonar-gold read failed") ||
		!strings.Contains(missingOutput, "sonar-gold.json") {
		t.Fatalf("missing bundled file was silent or vague:\n%s", missingOutput)
	}

	// 3. no manifest in the discovered clone: the release manifest wins and the file comes from beside it.
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/templates/themes/sources.json":
			_, _ = io.WriteString(response, manifest)
		case "/templates/themes/sonar-gold.json":
			_, _ = response.Write(themeBody)
		default:
			http.NotFound(response, request)
		}
	}))
	t.Cleanup(server.Close)
	releaseHome := t.TempDir()
	_, releaseOutput, err := run(releaseHome, t.TempDir(), server.URL+"/templates/themes/sources.json")
	if err != nil {
		t.Fatalf("release-fallback bundled install: %v\n%s", err, releaseOutput)
	}
	if got := []byte(
		readFixture(t, filepath.Join(releaseHome, ".claude", "themes", "sonar-gold.json")),
	); !bytes.Equal(
		got,
		themeBody,
	) {
		t.Fatalf("release-fallback bundled theme=%q, want %q\n%s", got, themeBody, releaseOutput)
	}
}

func TestBundledThemeManifestValidationAndNonJSONFileFailClosedByName(t *testing.T) {
	t.Parallel()
	load := func(manifest string) error {
		sourceRepo := t.TempDir()
		writeFixture(t, filepath.Join(sourceRepo, "templates", "themes", "sources.json"), manifest)
		_, err := loadThemeSources(context.Background(), Options{SourceRepo: sourceRepo})
		return err
	}
	for _, tc := range []struct{ name, manifest, want string }{
		{"path traversal", `{"bundled":{"x":{"file":"../secret.json","target":"~/.claude/themes/x.json"}}}`, `bundled theme "x" file "../secret.json" must be a bare file name beside the manifest`},
		{"name clash", `{"source_fetched":{"x":{"repo":"https://e.test","raw":"https://e.test/x.json","target":"~/.claude/themes/x.json"}},"bundled":{"x":{"file":"x.json","target":"~/.claude/themes/x.json"}}}`, `names theme "x" as both source_fetched and bundled`},
		{"missing file field", `{"bundled":{"x":{"target":"~/.claude/themes/x.json"}}}`, `bundled theme "x" is missing name, file, or target`},
	} {
		err := load(tc.manifest)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err=%v, want it to contain %q", tc.name, err, tc.want)
		}
	}

	home := t.TempDir()
	sourceRepo := t.TempDir()
	writeFixture(
		t,
		filepath.Join(sourceRepo, "templates", "themes", "sources.json"),
		`{"bundled":{"x":{"file":"x.json","target":"~/.claude/themes/x.json"}}}`,
	)
	writeFixture(t, filepath.Join(sourceRepo, "templates", "themes", "x.json"), "not json\n")
	var output bytes.Buffer
	_, err := Run(context.Background(), Options{
		MCPConfigPath: testConfigPath(t),
		Mode:          ModeApply, Home: home, SourceRepo: sourceRepo, Stdout: &output,
		Runner: &fakeRunner{nameSyncIdle: true}, CodexHomes: []string{}, InstallThemes: true,
	})
	if err != nil {
		t.Fatalf("non-JSON bundled file aborted host install: %v\n%s", err, output.String())
	}
	if !strings.Contains(output.String(), "theme x read failed") ||
		!strings.Contains(output.String(), "is not valid JSON") {
		t.Fatalf("non-JSON bundled file was silent or vague:\n%s", output.String())
	}
	if _, statErr := os.Stat(filepath.Join(home, ".claude", "themes", "x.json")); !os.IsNotExist(statErr) {
		t.Fatalf("non-JSON bundled file was installed anyway: %v", statErr)
	}
}

func TestOverlayThemeMergesOntoFetchedBaseAndNamesABaseFailure(t *testing.T) {
	t.Parallel()
	baseBody := `{"name":"Tokyo Night","base":"dark","overrides":{"claude":"#c95cff","promptBorder":"#7c4dff","promptBorderShimmer":"#aa8bff"}}`
	overlay := `{"name":"Professor Gold","overrides":{"promptBorder":"#ffd60a","promptBorderShimmer":"#fff7c2"}}`
	var baseStatus int
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/tokyo-night.json" {
			http.NotFound(response, request)
			return
		}
		if baseStatus != 0 {
			http.Error(response, "base down", baseStatus)
			return
		}
		_, _ = io.WriteString(response, baseBody)
	}))
	t.Cleanup(server.Close)
	sourceRepo := t.TempDir()
	writeFixture(t, filepath.Join(sourceRepo, "templates", "themes", "sources.json"), fmt.Sprintf(`{
  "source_fetched": {"tokyo-night": {"repo": %q, "raw": %q, "target": "~/.claude/themes/tokyo-night.json", "activate": "/theme", "requires": "fixture"}},
  "bundled": {"professor-gold": {"file": "professor-gold.json", "base": "tokyo-night", "target": "~/.claude/themes/professor-gold.json", "activate": "/theme", "requires": "fixture"}}
}`, server.URL, server.URL+"/tokyo-night.json"))
	writeFixture(t, filepath.Join(sourceRepo, "templates", "themes", "professor-gold.json"), overlay)
	run := func() (string, error) {
		var output bytes.Buffer
		_, err := Run(context.Background(), Options{
			MCPConfigPath: testConfigPath(t),
			Mode:          ModeApply, Home: t.TempDir(), SourceRepo: sourceRepo, Stdout: &output,
			Runner: &fakeRunner{nameSyncIdle: true}, CodexHomes: []string{}, InstallThemes: true,
		})
		return output.String(), err
	}
	home := t.TempDir()
	var output bytes.Buffer
	if _, err := Run(context.Background(), Options{
		MCPConfigPath: testConfigPath(t),
		Mode:          ModeApply, Home: home, SourceRepo: sourceRepo, Stdout: &output,
		Runner: &fakeRunner{nameSyncIdle: true}, CodexHomes: []string{}, InstallThemes: true,
	}); err != nil {
		t.Fatalf("overlay install: %v\n%s", err, output.String())
	}
	var merged struct {
		Name      string            `json:"name"`
		Base      string            `json:"base"`
		Overrides map[string]string `json:"overrides"`
	}
	if err := json.Unmarshal(
		[]byte(readFixture(t, filepath.Join(home, ".claude", "themes", "professor-gold.json"))),
		&merged,
	); err != nil {
		t.Fatalf("merged overlay is not JSON: %v", err)
	}
	if merged.Name != "Professor Gold" || merged.Base != "dark" || merged.Overrides["claude"] != "#c95cff" ||
		merged.Overrides["promptBorder"] != "#ffd60a" || merged.Overrides["promptBorderShimmer"] != "#fff7c2" {
		t.Fatalf("merged overlay=%#v, want the base palette with the overlay's name and two prompt-border keys", merged)
	}

	baseStatus = http.StatusServiceUnavailable
	failedOutput, err := run()
	if err != nil {
		t.Fatalf("base fetch failure aborted host install: %v\n%s", err, failedOutput)
	}
	if !strings.Contains(failedOutput, "theme professor-gold base tokyo-night fetch failed") ||
		!strings.Contains(failedOutput, "503") {
		t.Fatalf("base fetch failure was silent or vague:\n%s", failedOutput)
	}

	for _, tc := range []struct{ name, base, overlay, want string }{
		{"blank base", `{"name":"Tokyo Night","base":"dark"}`, overlay, "base palette carries no overrides"},
		{"blank overlay", baseBody, `{"name":"Professor Gold"}`, "overlay carries no overrides"},
		{"non-JSON base", `{"name":`, overlay, "decode base palette"},
	} {
		if _, err := mergeThemeOverlay(
			[]byte(tc.base),
			[]byte(tc.overlay),
		); err == nil ||
			!strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err=%v, want it to contain %q", tc.name, err, tc.want)
		}
	}

	writeFixture(
		t,
		filepath.Join(sourceRepo, "templates", "themes", "sources.json"),
		`{"bundled":{"professor-gold":{"file":"professor-gold.json","base":"nope","target":"~/.claude/themes/professor-gold.json"}}}`,
	)
	if _, err := loadThemeSources(
		context.Background(),
		Options{SourceRepo: sourceRepo},
	); err == nil ||
		!strings.Contains(err.Error(), `bundled theme "professor-gold" base "nope" is not a source_fetched theme`) {
		t.Fatalf("unknown base err=%v, want a named manifest refusal", err)
	}
}

// TestThemeManifestUnpublishedAlphaReleaseReturnsNamedRefusal is a
// REGRESSION test for the 2026-09-14 retro finding: `pfm install --yes` run
// outside the source checkout falls back to a release manifest URL built
// from an -alpha VERSION, which pfm never publishes and which therefore
// 404s. loadThemeSources must recognise the -alpha reference in the URL and
// return a named refusal instead of surfacing a bare HTTP error — and must
// never even attempt the doomed fetch. FAILS on unfixed code because the URL
// is fetched unconditionally and the error is a bare "fetch failed"/network
// error, not the named refusal.
func TestThemeManifestUnpublishedAlphaReleaseReturnsNamedRefusal(t *testing.T) {
	t.Parallel()
	_, err := loadThemeSources(context.Background(), Options{
		ThemeManifestURL: "https://raw.githubusercontent.com/example/professor/0.78.0-alpha/templates/themes/sources.json",
	})
	if err == nil {
		t.Fatal("loadThemeSources() error = nil, want a named refusal for an unpublished -alpha release manifest")
	}
	want := "release manifest for an unpublished -alpha build; run pfm install from the source clone"
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("loadThemeSources() error = %v, want it to contain %q", err, want)
	}
}

// TestThemeManifestSourceRepoWithoutLocalManifestRefusesUnpublishedAlpha
// covers the SourceRepo branch of the same refusal: a source repository with
// no local manifest falls back to the release URL, and an -alpha URL there
// must return the named refusal carrying the local-manifest error — never a
// fetch of the URL pfm never publishes.
func TestThemeManifestSourceRepoWithoutLocalManifestRefusesUnpublishedAlpha(t *testing.T) {
	t.Parallel()
	client := &http.Client{Transport: themeRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		t.Errorf("fetched the unpublished -alpha release manifest %s", request.URL)
		return nil, fmt.Errorf("fixture transport refuses %s", request.URL)
	})}
	_, err := loadThemeSources(context.Background(), Options{
		SourceRepo:       t.TempDir(),
		ThemeManifestURL: "https://raw.githubusercontent.com/example/professor/0.78.0-alpha/templates/themes/sources.json",
		ThemeHTTPClient:  client,
	})
	if err == nil {
		t.Fatal("loadThemeSources() error = nil, want a named refusal for an unpublished -alpha release manifest")
	}
	for _, want := range []string{
		"release manifest for an unpublished -alpha build; run pfm install from the source clone",
		"local theme manifest unavailable",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("loadThemeSources() error = %v, want it to contain %q", err, want)
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
// same verb used for a source-fetched theme downloaded over HTTP. A bundled
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
	manifest := `{"bundled":{"sonar-gold":{"file":"sonar-gold.json","target":"~/.claude/themes/sonar-gold.json","activate":"/theme","requires":"fixture"}}}`
	writeFixture(t, filepath.Join(sourceRepo, "templates", "themes", "sources.json"), manifest)
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

func TestThemeOverlayLoadsBaseOncePerInstall(t *testing.T) {
	for _, status := range []int{0, http.StatusServiceUnavailable, -1} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			ctx, recorder := obs.Test(t)
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests++
				if status == http.StatusServiceUnavailable {
					http.Error(w, "unavailable", status)
					return
				}
				_, _ = io.WriteString(w, `{"name":"Tokyo Night","base":"dark","overrides":{"claude":"#123456"}}`)
			}))
			url := server.URL + "/tokyo-night.json"
			if status == -1 {
				server.Close()
			} else {
				defer server.Close()
			}
			sourceRepo := t.TempDir()
			writeFixture(t, filepath.Join(sourceRepo, "templates", "themes", "sources.json"), fmt.Sprintf(`{
 "source_fetched":{"tokyo-night":{"repo":%q,"raw":%q,"target":"~/.claude/themes/tokyo-night.json"}},
 "bundled":{
  "first":{"file":"first.json","base":"tokyo-night","target":"~/.claude/themes/first.json"},
  "second":{"file":"second.json","base":"tokyo-night","target":"~/.claude/themes/second.json"}
 }
}`, server.URL, url))
			for _, name := range []string{"first", "second"} {
				writeFixture(
					t, filepath.Join(sourceRepo, "templates", "themes", name+".json"),
					fmt.Sprintf(`{"name":%q,"overrides":{"promptBorder":"#abcdef"}}`, name),
				)
			}
			home := t.TempDir()
			var output bytes.Buffer
			_, err := Run(ctx, Options{
				MCPConfigPath: testConfigPath(t), Mode: ModeApply, Home: home, SourceRepo: sourceRepo,
				Stdout: &output, Runner: &fakeRunner{nameSyncIdle: true}, CodexHomes: []string{},
				InstallThemes: true,
			})
			if err != nil {
				t.Fatalf("Run: %v\n%s", err, output.String())
			}
			records := []obs.Record{}
			for _, record := range recorder.Records() {
				if record.Message == "http.out.request" {
					records = append(records, record)
				}
			}
			if len(records) != 1 {
				t.Fatalf("records=%d, want 1: %s", len(records), recorder.Raw())
			}
			switch status {
			case -1:
				if records[0].Level != "WARN" {
					t.Fatalf("level=%s, want WARN", records[0].Level)
				}
				if got, _ := records[0].Field(obs.FieldErr); got == nil || got == "" {
					t.Fatalf("missing err: %v", records[0].Fields)
				}
				for _, name := range []string{"tokyo-night", "first", "second"} {
					if !strings.Contains(output.String(), "theme "+name+" ") ||
						!strings.Contains(output.String(), "fetch failed") {
						t.Fatalf("missing skip %s: %s", name, output.String())
					}
				}
			case http.StatusServiceUnavailable:
				if records[0].Level != "WARN" || !strings.Contains(output.String(), "503") {
					t.Fatalf("status result: %s %s", recorder.Raw(), output.String())
				}
			default:
				if requests != 1 {
					t.Fatalf("requests=%d, want 1", requests)
				}
				for _, name := range []string{"first", "second"} {
					if _, err := os.Stat(filepath.Join(home, ".claude", "themes", name+".json")); err != nil {
						t.Fatalf("overlay %s: %v", name, err)
					}
				}
			}
		})
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
