package doctor

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

// TestHostOverlayDoctorNamesAnUnreadableSettingsFile pins the distinction an
// unreadable settings.json needs from "not configured": before this fix,
// installer.ReadStatusLineCommand folded a read failure into the same empty
// string a genuine absence returns, so printStatusLineOverlayDoctor reported
// a broken settings file as a quiet "nothing configured" instead of naming
// the read failure.
func TestHostOverlayDoctorNamesAnUnreadableSettingsFile(t *testing.T) {
	home := t.TempDir()
	managed := stageHostOverlayManagedCopies(t, home)
	wireHostOverlaySymlinks(t, home, managed)
	configDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// A directory sitting where the settings file belongs always fails
	// os.ReadFile (EISDIR) regardless of the test's own uid/gid — unlike a
	// permission bit, which a root-run suite ignores outright.
	settingsPath := filepath.Join(configDir, "settings.json")
	if err := os.MkdirAll(settingsPath, 0o700); err != nil {
		t.Fatal(err)
	}
	machine := pfmconfig.Config{Accounts: []pfmconfig.Account{{ID: 1, ConfigDir: configDir}}}
	var output bytes.Buffer
	if warnings, failures := printHostOverlayDoctor(&output, home, machine); warnings != 0 || failures != 1 {
		t.Fatalf("warnings=%d failures=%d, want 0/1\n%s", warnings, failures, output.String())
	}
	want := "doctor: host_overlay statusline claude[1] could not read " + settingsPath
	if !strings.Contains(output.String(), want) {
		t.Fatalf("output missing %q:\n%s", want, output.String())
	}
}
