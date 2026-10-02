package doctor

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

func TestFullscreenDoctorNamesEachAccount(t *testing.T) {
	home := t.TempDir()
	dirs := map[int]string{}
	write := func(id int, name, content string) {
		dir := filepath.Join(home, ".cc", string(rune('0'+id)))
		dirs[id] = dir
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if content == "" {
			return
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	fullscreen := `{"tui":"fullscreen"}`
	write(1, "settings.json", fullscreen)
	write(1, ".claude.json", `{"fullscreenAutoDisabled":{"version":"2.1.284","at":1,"strikes":2}}`)
	write(2, "settings.json", fullscreen)
	write(2, ".claude.json", `{"fullscreenBootPending":{"9":1}}`)
	write(3, "settings.json", `{"tui":"default"}`)
	write(4, "settings.json", fullscreen)
	// A directory where the file belongs fails the read for any uid.
	if err := os.MkdirAll(filepath.Join(dirs[4], ".claude.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	write(5, "", "")
	write(6, "settings.json", `{"tui":`)
	machine := pfmconfig.Config{Accounts: []pfmconfig.Account{
		{ID: 1, ConfigDir: dirs[1]},
		{ID: 2, ConfigDir: dirs[2]},
		{ID: 3, ConfigDir: dirs[3]},
		{ID: 4, ConfigDir: dirs[4]},
		{ID: 5, ConfigDir: dirs[5]},
		{ID: 6, ConfigDir: dirs[6]},
	}}
	var output bytes.Buffer
	tally := &doctorTally{}
	printFullscreenDoctor(&output, home, machine, tally)
	if tally.warnings != 1 || tally.failures != 2 {
		t.Fatalf("warnings=%d failures=%d, want 1 and 2\n%s", tally.warnings, tally.failures, output.String())
	}
	for _, want := range []string{
		"doctor: fullscreen claude[1] fullscreen renderer auto-disabled by Claude's boot canary " +
			"(version 2.1.284, strikes 2) — run pfm install --yes\n",
		"doctor: fullscreen claude[2] ok\n",
		"doctor: fullscreen claude[3] ok (tui not fullscreen)\n",
		"doctor: fullscreen claude[4] could not read " + filepath.Join(dirs[4], ".claude.json"),
		"doctor: fullscreen claude[5] skipped: no settings.json at " + filepath.Join(dirs[5], "settings.json"),
		"doctor: fullscreen claude[6] could not read " + filepath.Join(dirs[6], "settings.json"),
	} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, output.String())
		}
	}
}
