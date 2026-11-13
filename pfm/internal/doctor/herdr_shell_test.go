package doctor

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func stageDoctorHerdrShell(t *testing.T, home string) {
	t.Helper()
	t.Setenv("HERDR_CONFIG_PATH", testjail.StageHerdrShell(t, home))
}

func TestHerdrShellDoctor(t *testing.T) {
	for _, tc := range []struct {
		name, config, want string
		absent, wrapper    bool
		warnings           int
		nonExecutable      bool
	}{
		{name: "absent", absent: true, want: "doctor: herdr-shell: ok herdr absent\n"},
		{name: "user", config: "[terminal]\ndefault_shell = '/bin/fish'\n", want: "doctor: herdr-shell: ok user-owned default_shell=\"/bin/fish\" in {root}/.config/herdr/config.toml (picker wiring skipped)\n"},
		{name: "unwired", want: "doctor: herdr-shell: WARN picker wiring missing or wrapper not executable in {root}/.config/herdr/config.toml — run pfm install --yes\n", warnings: 1},
		{name: "broken config", config: "[terminal", want: "doctor: herdr-shell: WARN parse Herdr config {root}/.config/herdr/config.toml: toml: line 1: expected '.' or ']' to end table name, but got '\\x00' instead\n", warnings: 1},
		{name: "wired", config: "owned", wrapper: true, want: "doctor: herdr-shell: ok wired {root}/.config/herdr/config.toml\n"},
		{name: "missing wrapper", config: "owned", want: "doctor: herdr-shell: WARN picker wiring missing or wrapper not executable in {root}/.config/herdr/config.toml — run pfm install --yes\n", warnings: 1},
		{name: "non-executable wrapper", config: "owned", wrapper: true, nonExecutable: true, want: "doctor: herdr-shell: WARN picker wiring missing or wrapper not executable in {root}/.config/herdr/config.toml — run pfm install --yes\n", warnings: 1},
		{name: "unmarked missing wrapper", config: "unmarked", want: "doctor: herdr-shell: WARN user-owned default_shell points to a missing or non-executable pfm wrapper in {root}/.config/herdr/config.toml — restore pfm's marker block or choose an executable shell\n", warnings: 1},
		{name: "unmarked non-executable wrapper", config: "unmarked", wrapper: true, nonExecutable: true, want: "doctor: herdr-shell: WARN user-owned default_shell points to a missing or non-executable pfm wrapper in {root}/.config/herdr/config.toml — restore pfm's marker block or choose an executable shell\n", warnings: 1},
		{name: "unmarked executable wrapper", config: "unmarked", wrapper: true, want: "doctor: herdr-shell: ok user-owned default_shell=\"{root}/.local/share/pfm/install/bin/pfm-herdr-shell\" in {root}/.config/herdr/config.toml (picker wiring skipped)\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			config := tc.config
			if config == "owned" {
				config = fmt.Sprintf(
					"[terminal]\n# >>> pfm herdr shell >>>\ndefault_shell = %q\n# <<< pfm herdr shell <<<\n",
					paths.HerdrShellWrapper(home),
				)
			}
			if config == "unmarked" {
				config = fmt.Sprintf("[terminal]\ndefault_shell = %q\n", paths.HerdrShellWrapper(home))
			}
			if tc.wrapper {
				testjail.StageHerdrShell(t, home)
			}
			path := filepath.Join(home, ".config", "herdr", "config.toml")
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
				t.Fatal(err)
			}
			if tc.nonExecutable {
				if err := os.Chmod(paths.HerdrShellWrapper(home), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			runner := &deps.FakeRunner{}
			if tc.absent {
				runner.ScriptLookPath("herdr", "", exec.ErrNotFound)
			} else {
				runner.ScriptLookPath("herdr", "/fixture/herdr", nil)
			}
			var out bytes.Buffer
			warnings := printHerdrShellDoctor(&out, home, Dependencies{Runner: runner, Env: &paths.MapEnv{}})
			if got := rooted(out.String(), home); warnings != tc.warnings || got != tc.want {
				t.Errorf("warnings=%d output = %q, want %q", warnings, got, tc.want)
			}
		})
	}
}
