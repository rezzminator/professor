package doctor

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestPrintThemeOwnershipDoctorWarnsPerUnownedShippedTheme(t *testing.T) {
	home := t.TempDir()
	filter := warningFilter{ignored: map[warningID]bool{}, configPath: "/cfg/pfm.config.json"}
	var out bytes.Buffer
	if warnings := printThemeOwnershipDoctor(&out, home, filter); warnings != 0 ||
		!bytes.HasPrefix(out.Bytes(), []byte("doctor: theme_ownership not checked — no source clone theme listing (")) {
		t.Fatalf("no clone: warnings=%d output=%q, want 0 and the not-checked row", warnings, out.String())
	}
	write := func(path string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(home, ".professor", "templates", "themes", "tokyo-night", "theme.json"))
	out.Reset()
	if warnings := printThemeOwnershipDoctor(&out, home, filter); warnings != 0 ||
		out.String() != "doctor: theme_ownership ok every shipped theme present is installer-owned\n" {
		t.Fatalf("nothing present: warnings=%d output=%q", warnings, out.String())
	}
	write(filepath.Join(home, ".claude", "themes", "tokyo-night.json"))
	out.Reset()
	want := "doctor: theme tokyo-night is not installer-owned: no VS Code colour theme is rendered for it" +
		" — move its file aside (pfm install names it), then run pfm install --yes" +
		" · don't want this? add \"theme-unowned\" to doctor.ignoreWarnings in /cfg/pfm.config.json\n"
	if warnings := printThemeOwnershipDoctor(&out, home, filter); warnings != 1 || out.String() != want {
		t.Fatalf("unowned: warnings=%d output=%q\nwant 1 %q", warnings, out.String(), want)
	}
}
