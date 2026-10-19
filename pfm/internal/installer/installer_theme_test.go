package installer

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/themesync"
)

func TestThemeInstallIsIdempotentVisibleOnDriftAndReversible(t *testing.T) {
	home := t.TempDir()
	sourceRepo := t.TempDir()
	recordFixtureSourceRepo(t, home, sourceRepo)
	themeBody := []byte(`{"name":"Sonar Gold","base":"dark","overrides":{"claude":"#ffd60a"}}` + "\n")
	writeFixture(t, filepath.Join(sourceRepo, "templates", "themes", "sonar-gold.json"), string(themeBody))

	run := func(mode Mode) (Report, string, error) {
		var output bytes.Buffer
		report, err := Run(context.Background(), Options{
			MCPConfigPath: testConfigPath(t),
			Mode:          mode, Home: home, SourceRepo: sourceRepo, Stdout: &output,
			Runner: &fakeRunner{nameSyncIdle: true}, CodexHomes: []string{}, InstallThemes: true,
		})
		return report, output.String(), err
	}
	target := filepath.Join(home, ".claude", "themes", "sonar-gold.json")
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
	if second.Changed != 0 || !strings.Contains(secondOutput, "theme sonar-gold") {
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
	if !strings.Contains(driftOutput, "theme sonar-gold locally modified; left in place") {
		t.Fatalf("local drift was silent:\n%s", driftOutput)
	}

	writeFixture(t, target, string(themeBody))
	_, uninstallOutput, err := run(ModeUninstall)
	if err != nil {
		t.Fatalf("theme uninstall: %v\n%s", err, uninstallOutput)
	}
	for _, path := range []string{target, themesync.ProfilePath(home, "sonar-gold")} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("uninstall retained installer-owned theme file %s: %v", path, err)
		}
	}
}

// TestReleaseProfileFetchFailureIsLoudAndNonFatal: with no profiles in the
// clone, a profile the release listing names but will not serve is a named
// skip, and the rest of the host install carries on.
func TestReleaseProfileFetchFailureIsLoudAndNonFatal(t *testing.T) {
	home := t.TempDir()
	sourceRepo := t.TempDir()
	recordFixtureSourceRepo(t, home, sourceRepo)
	client := releaseThemeClient(t, "v1.0.0", nil, `[{"name":"sonar-gold.json","type":"file"}]`)
	var output bytes.Buffer
	_, err := Run(context.Background(), Options{
		MCPConfigPath: testConfigPath(t),
		Mode:          ModeApply, Home: home, SourceRepo: sourceRepo, Stdout: &output,
		Runner: &fakeRunner{nameSyncIdle: true}, CodexHomes: []string{}, InstallThemes: true,
		ThemeReleaseURL: releaseThemeURL("v1.0.0"), ThemeHTTPClient: client,
	})
	if err != nil {
		t.Fatalf("cosmetic theme fetch aborted host install: %v\n%s", err, output.String())
	}
	if !strings.Contains(output.String(), "theme sonar-gold fetch failed: GET "+releaseThemeURL("v1.0.0")+
		"sonar-gold.json: HTTP Not Found") {
		t.Fatalf("theme fetch failure was silent or vague:\n%s", output.String())
	}
	if _, statErr := os.Stat(filepath.Join(home, ".zshrc")); statErr != nil {
		t.Fatalf("host install did not continue after theme failure: %v", statErr)
	}
}

func TestThemeProfilesFallBackToReleaseWhenTheCloneHoldsNone(t *testing.T) {
	listing := `[{"name":"README.md","type":"file"},{"name":"lagoon.json","type":"file"}]`
	client := releaseThemeClient(t, "v1.0.0", map[string]string{"lagoon.json": "{}"}, listing)

	profiles, err := loadThemeProfiles(context.Background(), Options{
		SourceRepo:      t.TempDir(),
		ThemeReleaseURL: releaseThemeURL("v1.0.0"),
		ThemeHTTPClient: client,
	})
	if err != nil {
		t.Fatalf("loadThemeProfiles() did not fall back to the release: %v", err)
	}
	if got := profileKeys(profiles); strings.Join(got, ",") != "lagoon" {
		t.Fatalf("release profiles = %v, want lagoon alone", got)
	}
	if got := profiles["lagoon"].raw; got != releaseThemeURL("v1.0.0")+"lagoon.json" {
		t.Fatalf("release profile lagoon raw=%q, want it in the release's templates/themes/", got)
	}
}
