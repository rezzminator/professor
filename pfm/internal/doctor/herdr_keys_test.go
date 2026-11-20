package doctor

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

const herdrKeysFixtureBlock = "# >>> pfm herdr keys >>>\n[keys]\nprevious_workspace = \"prefix+i\"\nnext_workspace = \"prefix+m\"\n\n" +
	"[[keys.command]]\nkey = \"prefix+a\"\ntype = \"shell\"\ndescription = \"new pfm chat in this space\"\n" +
	"command = '\"${HERDR_BIN_PATH:-herdr}\" tab create --workspace \"${HERDR_ACTIVE_WORKSPACE_ID:-$HERDR_WORKSPACE_ID}\" --env PFM_AUTO_OPEN=pfm --focus'\n" +
	"# <<< pfm herdr keys <<<\n"

func TestHerdrKeysDoctorNamesEachMissingMapping(t *testing.T) {
	const config = "{root}/.config/herdr/config.toml"
	const allMissing = `previous_workspace = "prefix+i", next_workspace = "prefix+m", ` +
		`[[keys.command]] key = "prefix+a" (new pfm chat in this space)`
	for _, tc := range []struct {
		name, config, want string
		lookup             error
		noConfig           bool
		warnings           int
	}{
		{name: "herdr absent", lookup: exec.ErrNotFound, want: "doctor: herdr-keys: ok herdr absent\n"},
		{name: "herdr unusable", lookup: errors.New("relative PATH entry"), want: "doctor: herdr-keys: not checked — the herdr binary is unusable (see herdr-shell)\n"},
		{name: "owned", config: "[theme]\nname = 'x'\n\n" + herdrKeysFixtureBlock, want: "doctor: herdr-keys: ok wired " + config + "\n"},
		{name: "no config", noConfig: true, warnings: 1, want: "doctor: herdr-keys: WARN " + config + " lacks " + allMissing + " — run pfm install --yes\n"},
		{name: "no keys", config: "[theme]\nname = 'x'\n", warnings: 1, want: "doctor: herdr-keys: WARN " + config + " lacks " + allMissing + " — run pfm install --yes\n"},
		{
			name: "hand-written lacking one", warnings: 1,
			config: "[keys]\nprevious_workspace = [\"cmd+shift+up\", \"prefix+i\"]\nnext_workspace = \"prefix+m\"\n",
			want: "doctor: herdr-keys: WARN hand-written [keys] in " + config + ` lacks [[keys.command]] key = "prefix+a" (new pfm chat in this space)` +
				" — add them to that table (pfm install leaves a hand-written table alone)\n",
		},
		{
			name:   "hand-written binding all",
			config: "[keys]\nprevious_workspace = \"prefix+i\"\nnext_workspace = \"prefix+m\"\n\n[[keys.command]]\nkey = \"prefix+a\"\ntype = \"shell\"\ncommand = \"pfm\"\n",
			want:   "doctor: herdr-keys: ok hand-written [keys] in " + config + " binds every pfm mapping\n",
		},
		{
			name: "out-of-date block", warnings: 1,
			config: "# >>> pfm herdr keys >>>\n[keys]\nprevious_workspace = \"prefix+i\"\nnext_workspace = \"prefix+m\"\n\n[[keys.command]]\nkey = \"prefix+a\"\ntype = \"shell\"\ncommand = \"pfm\"\n# <<< pfm herdr keys <<<\n",
			want:   "doctor: herdr-keys: WARN pfm's keys block in " + config + " is out of date — run pfm install --yes\n",
		},
		{name: "broken config", config: "[keys", warnings: 1, want: "doctor: herdr-keys: WARN parse Herdr config " + config + ": toml: line 1: expected '.' or ']' to end table name, but got '\\x00' instead\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			if !tc.noConfig {
				path := filepath.Join(home, ".config", "herdr", "config.toml")
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(tc.config), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			runner := &deps.FakeRunner{}
			if tc.lookup != nil {
				runner.ScriptLookPath("herdr", "", tc.lookup)
			} else {
				runner.ScriptLookPath("herdr", "/fixture/herdr", nil)
			}
			var out bytes.Buffer
			warnings := printHerdrKeysDoctor(&out, home, Dependencies{Runner: runner, Env: &paths.MapEnv{}})
			if got := rooted(out.String(), home); warnings != tc.warnings || got != tc.want {
				t.Errorf("warnings=%d output = %q\nwant %d %q", warnings, got, tc.warnings, tc.want)
			}
		})
	}
}
