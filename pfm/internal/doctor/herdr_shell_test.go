package doctor

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
		{name: "absent", absent: true, want: "ok herdr absent"},
		{name: "user", config: "[terminal]\ndefault_shell = '/bin/fish'\n", want: `ok user-owned default_shell="/bin/fish"`},
		{name: "unwired", want: "WARN picker wiring missing", warnings: 1},
		{name: "broken config", config: "[terminal", want: "WARN parse Herdr config", warnings: 1},
		{name: "wired", config: "owned", wrapper: true, want: "ok wired"},
		{name: "missing wrapper", config: "owned", want: "WARN picker wiring missing", warnings: 1},
		{name: "non-executable wrapper", config: "owned", wrapper: true, nonExecutable: true, want: "WARN picker wiring missing", warnings: 1},
		{name: "unmarked missing wrapper", config: "unmarked", want: "WARN user-owned default_shell points to a missing", warnings: 1},
		{name: "unmarked non-executable wrapper", config: "unmarked", wrapper: true, nonExecutable: true, want: "WARN user-owned default_shell points to a missing", warnings: 1},
		{name: "unmarked executable wrapper", config: "unmarked", wrapper: true, want: "ok user-owned default_shell="},
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
			path := filepath.Join(home, ".config", "herdr", "config.toml")
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
				t.Fatal(err)
			}
			if tc.wrapper {
				if err := os.MkdirAll(filepath.Dir(paths.HerdrShellWrapper(home)), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := testjail.WriteExecutable(
					paths.HerdrShellWrapper(home),
					[]byte("#!/bin/sh\nexit 0\n"),
					0o755,
				); err != nil {
					t.Fatal(err)
				}
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
			if warnings != tc.warnings || !strings.Contains(out.String(), "doctor: herdr-shell: "+tc.want) {
				t.Fatalf("warnings=%d output=%s", warnings, &out)
			}
		})
	}
}
