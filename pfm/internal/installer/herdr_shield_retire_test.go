package installer

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// The door judges only a store whose one Herdr hook is herdr-shield's guarded
// form: it skips with the owner's steps while Herdr's default_shell still runs
// herdr-shield, clears the hook for unwrapping once it does not, and names an
// unreadable Herdr config.
func TestRetireHerdrShieldHook(t *testing.T) {
	const store = "/fixture/settings.json"
	guarded := `[ -n "${HERDR_SHIELD_INNER:-}" ] || sh '/fixture/hooks/herdr-agent-state.sh' session`
	for _, tc := range []struct {
		name, config string
		hooks        []string
		want         HerdrShieldRetirement
		// wantErr is the whole error of an unreadable Herdr config.
		wantErr string
	}{
		{
			name: "guarded hook while Herdr runs herdr-shield", config: "[terminal]\ndefault_shell = \"/opt/bin/herdr-shield -l\"\n",
			hooks: []string{guarded},
			want: HerdrShieldRetirement{Guarded: true, Skip: "herdr claude integration in /fixture/settings.json: " +
				"Herdr's default_shell in {config} still names herdr-shield, so its guarded hook stays — end the chats in Herdr," +
				` remove default_shell = "/opt/bin/herdr-shield -l" from {config}, restart Herdr, then run pfm install --yes again`},
		},
		{name: "guarded hook once Herdr runs another shell", config: "[terminal]\ndefault_shell = \"zsh\"\n", hooks: []string{guarded}, want: HerdrShieldRetirement{Guarded: true}},
		{name: "guarded hook with no Herdr config", hooks: []string{guarded}, want: HerdrShieldRetirement{Guarded: true}},
		{
			name: "guarded hook under an unreadable Herdr config", config: "[terminal\n", hooks: []string{guarded},
			want: HerdrShieldRetirement{Guarded: true},
			wantErr: "herdr claude integration in /fixture/settings.json: read Herdr's default_shell: parse Herdr config" +
				` {config}: toml: line 2: expected '.' or ']' to end table name, but got '\n' instead`,
		},
		{name: "plain hook", config: "[terminal]\ndefault_shell = \"herdr-shield\"\n", hooks: []string{"sh '/fixture/hooks/herdr-agent-state.sh' session"}},
		{name: "two hooks", config: "[terminal]\ndefault_shell = \"herdr-shield\"\n", hooks: []string{guarded, guarded}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := filepath.Join(t.TempDir(), "config.toml")
			if tc.config != "" {
				if err := os.WriteFile(config, []byte(tc.config), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			env := &paths.MapEnv{Values: map[string]string{"HERDR_CONFIG_PATH": config}}
			got := RetireHerdrShieldHook(herdr.ClaudeStore{Settings: store, Hooks: tc.hooks}, t.TempDir(), env)
			gotErr := ""
			if got.Err != nil {
				gotErr = got.Err.Error()
			}
			if wantErr := strings.ReplaceAll(tc.wantErr, "{config}", config); gotErr != wantErr {
				t.Fatalf("error %q; want %q", gotErr, wantErr)
			}
			got.Err = nil
			want := tc.want
			want.Skip = strings.ReplaceAll(want.Skip, "{config}", config)
			if got != want {
				t.Fatalf("verdict\n%+v\nwant\n%+v", got, want)
			}
		})
	}
}

// A registry listing herdr-shield gets exactly one unlink; a registry without
// it gets no command (beside a linked run proving the step ran); no herdr on
// PATH or no server up is a skip row; a failed unlink is an error naming the
// command and Herdr's text.
func TestRetireHerdrShieldPlugin(t *testing.T) {
	unlink := []string{fakeHerdrBinary, "plugin", "unlink", "herdr-shield"}
	for _, tc := range []struct {
		name, registry string
		noHerdr        bool
		result         deps.RunResult
		wantRows       string
		wantCalls      [][]string
		wantErr        string
	}{
		{
			name: "registry lists herdr-shield", registry: `[{"plugin_id":"professor.indicators"},{"plugin_id":"herdr-shield"}]`,
			wantRows: "  change  herdr plugin unlink herdr-shield\n", wantCalls: [][]string{unlink},
		},
		{name: "registry without herdr-shield", registry: `[{"plugin_id":"professor.indicators"}]`},
		{name: "no registry"},
		{
			name: "herdr off PATH", registry: `[{"plugin_id":"herdr-shield"}]`, noHerdr: true,
			wantRows: "  skip    herdr plugin herdr-shield stays registered until `herdr plugin unlink herdr-shield` runs: herdr is not on PATH\n",
		},
		{
			name: "no Herdr server up", registry: `[{"plugin_id":"herdr-shield"}]`,
			result:    deps.RunResult{ExitCode: 1, Stderr: []byte("error: herdr server is not running\n")},
			wantRows:  "  skip    herdr plugin herdr-shield stays registered until `herdr plugin unlink herdr-shield` runs: no Herdr server is up (herdr said: error: herdr server is not running)\n",
			wantCalls: [][]string{unlink},
		},
		{
			name: "unlink exits non-zero", registry: `[{"plugin_id":"herdr-shield"}]`,
			result:    deps.RunResult{ExitCode: 2, Stderr: []byte("plugin is locked\n")},
			wantRows:  "  FAIL    herdr plugin unlink herdr-shield exited 2: plugin is locked\n",
			wantCalls: [][]string{unlink},
			wantErr:   "herdr plugin unlink herdr-shield exited 2: plugin is locked",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			if tc.registry != "" {
				path := herdr.PluginRegistryPath(home)
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(tc.registry), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			runner := &deps.FakeRunner{}
			if tc.noHerdr {
				runner.ScriptLookPath("herdr", "", errors.New("not found"))
			} else {
				runner.ScriptLookPath("herdr", fakeHerdrBinary, nil)
			}
			runner.Script(unlink, tc.result, nil)
			var output bytes.Buffer
			installer := &engine{apply: true, options: Options{
				Mode: ModeApply, Home: home, Env: &paths.MapEnv{Values: map[string]string{}},
				ProcessRunner: runner, Stdout: &output,
			}}
			err := installer.retireHerdrShieldPlugin(context.Background())
			gotErr := ""
			if err != nil {
				gotErr = err.Error()
			}
			if gotErr != tc.wantErr {
				t.Fatalf("error %q; want %q", gotErr, tc.wantErr)
			}
			if output.String() != tc.wantRows {
				t.Fatalf("rows %q; want %q", output.String(), tc.wantRows)
			}
			var calls [][]string
			for _, call := range runner.Calls() {
				calls = append(calls, call.Argv)
			}
			if !reflect.DeepEqual(calls, tc.wantCalls) {
				t.Fatalf("ran %q; want %q", calls, tc.wantCalls)
			}
		})
	}
}
