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
)

func TestThemeInstallIsIdempotentVisibleOnDriftAndReversible(t *testing.T) {
	home := t.TempDir()
	sourceRepo := t.TempDir()
	recordFixtureSourceRepo(t, home, sourceRepo)
	themeBody := []byte(`{"name":"Tokyo Night","fixture":true}` + "\n")
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/tokyo-night.json" {
			http.NotFound(response, request)
			return
		}
		if _, err := response.Write(themeBody); err != nil {
			t.Errorf("write theme fixture response: %v", err)
		}
	}))
	t.Cleanup(server.Close)
	manifest := fmt.Sprintf(`{
  "_comment": "fixture",
  "source_fetched": {
    "tokyo-night": {
      "repo": %q,
      "raw": %q,
      "target": "~/.claude/themes/tokyo-night.json",
      "activate": "/theme",
      "requires": "fixture"
    }
  }
}`, server.URL, server.URL+"/tokyo-night.json")
	writeFixture(t, filepath.Join(sourceRepo, "templates", "themes", "sources.json"), manifest)

	run := func(mode Mode) (Report, string, error) {
		var output bytes.Buffer
		report, err := Run(context.Background(), Options{
			MCPConfigPath: testConfigPath(t),
			Mode:          mode, Home: home, SourceRepo: sourceRepo, Stdout: &output,
			Runner: &fakeRunner{nameSyncIdle: true}, CodexHomes: []string{}, InstallThemes: true,
		})
		return report, output.String(), err
	}
	target := filepath.Join(home, ".claude", "themes", "tokyo-night.json")
	first, firstOutput, err := run(ModeApply)
	if err != nil {
		t.Fatalf("first theme install: %v\n%s", err, firstOutput)
	}
	if got := []byte(readFixture(t, target)); !bytes.Equal(got, themeBody) {
		t.Fatalf("installed theme=%q, want %q", got, themeBody)
	}
	second, secondOutput, err := run(ModeApply)
	if err != nil {
		t.Fatalf("second theme install: %v\n%s", err, secondOutput)
	}
	if second.Changed != 0 || !strings.Contains(secondOutput, "theme tokyo-night") {
		t.Fatalf("second report=%#v, want changed=0 with a named theme row\n%s", second, secondOutput)
	}
	if first.Changed == 0 {
		t.Fatalf("first report=%#v, want a theme write\n%s", first, firstOutput)
	}

	writeFixture(t, target, "operator modification\n")
	_, driftOutput, err := run(ModeApply)
	if err != nil {
		t.Fatalf("locally modified theme aborted install: %v\n%s", err, driftOutput)
	}
	if got := readFixture(t, target); got != "operator modification\n" {
		t.Fatalf("locally modified theme was overwritten: %q", got)
	}
	if !strings.Contains(driftOutput, "theme tokyo-night locally modified; left in place") {
		t.Fatalf("local drift was silent:\n%s", driftOutput)
	}

	writeFixture(t, target, string(themeBody))
	_, uninstallOutput, err := run(ModeUninstall)
	if err != nil {
		t.Fatalf("theme uninstall: %v\n%s", err, uninstallOutput)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("uninstall retained installer-owned theme: %v", err)
	}
}

func TestThemeFetchFailureIsLoudAndNonFatal(t *testing.T) {
	home := t.TempDir()
	sourceRepo := t.TempDir()
	recordFixtureSourceRepo(t, home, sourceRepo)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		http.Error(response, "blocked by fixture", http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)
	manifest := fmt.Sprintf(
		`{"source_fetched":{"tokyo-night":{"repo":%q,"raw":%q,"target":"~/.claude/themes/tokyo-night.json","activate":"/theme","requires":"fixture"}}}`,
		server.URL,
		server.URL+"/tokyo-night.json",
	)
	writeFixture(t, filepath.Join(sourceRepo, "templates", "themes", "sources.json"), manifest)
	var output bytes.Buffer
	_, err := Run(context.Background(), Options{
		MCPConfigPath: testConfigPath(t),
		Mode:          ModeApply, Home: home, SourceRepo: sourceRepo, Stdout: &output,
		Runner: &fakeRunner{nameSyncIdle: true}, CodexHomes: []string{}, InstallThemes: true,
	})
	if err != nil {
		t.Fatalf("cosmetic theme fetch aborted host install: %v\n%s", err, output.String())
	}
	if !strings.Contains(output.String(), "theme tokyo-night fetch failed") ||
		!strings.Contains(output.String(), "503") {
		t.Fatalf("theme fetch failure was silent or vague:\n%s", output.String())
	}
	if _, statErr := os.Stat(filepath.Join(home, ".zshrc")); statErr != nil {
		t.Fatalf("host install did not continue after theme failure: %v", statErr)
	}
}

func TestThemeManifestResolvesRegisteredOwnerPlaceholder(t *testing.T) {
	sourceRepo := t.TempDir()
	writeFixture(
		t,
		filepath.Join(sourceRepo, ".professor", "manifest.json"),
		`{"installed_from":{"repo":"fixture-owner/professor"}}`,
	)
	writeFixture(t, filepath.Join(sourceRepo, "templates", "themes", "sources.json"), `{
  "source_fetched": {
    "tokyo-night": {
      "repo": "https://github.com/{GH_USER}/claude-code-tokyo-night",
      "raw": "https://raw.githubusercontent.com/{GH_USER}/claude-code-tokyo-night/main/tokyo-night.json",
      "target": "~/.claude/themes/tokyo-night.json",
      "activate": "/theme",
      "requires": "fixture"
    }
  }
}`)
	sources, err := loadThemeSources(context.Background(), Options{SourceRepo: sourceRepo})
	if err != nil {
		t.Fatalf("loadThemeSources: %v", err)
	}
	if got := sources["tokyo-night"].Raw; got != "https://raw.githubusercontent.com/fixture-owner/claude-code-tokyo-night/main/tokyo-night.json" {
		t.Fatalf("resolved raw URL=%q", got)
	}
}

func TestThemeManifestFallsBackToReleaseWhenDiscoveredSourceLacksManifest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		if _, err := io.WriteString(
			response,
			`{"source_fetched":{"tokyo-night":{"repo":"https://example.test/theme","raw":"https://example.test/theme.json","target":"~/.claude/themes/tokyo-night.json","activate":"/theme","requires":"fixture"}}}`,
		); err != nil {
			t.Errorf("write release manifest fixture: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	sources, err := loadThemeSources(context.Background(), Options{
		SourceRepo:       t.TempDir(),
		ThemeManifestURL: server.URL,
	})
	if err != nil {
		t.Fatalf("loadThemeSources() did not fall back to release manifest: %v", err)
	}
	if _, found := sources["tokyo-night"]; !found {
		t.Fatalf("release manifest sources=%#v, want tokyo-night", sources)
	}
}
