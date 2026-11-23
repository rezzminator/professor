package herdr

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testwait"
)

// TestPluginLinked: the registry lives where Herdr 0.9.5 writes it, under
// XDG_CONFIG_HOME when set, else ~/.config, wherever HERDR_CONFIG_PATH puts
// config.toml; a registry that is not a regular file, a FIFO too, is a named
// error at once, never a hang.
func TestPluginLinked(t *testing.T) {
	for _, tc := range []struct {
		name, registry string
		// at is where the registry is written, env the environment read.
		at      string
		env     map[string]string
		fifo    bool
		want    bool
		wantErr string
	}{
		{name: "listed", registry: `[{"plugin_id":"a"},{"plugin_id":"herdr-shield"}]`, want: true},
		{name: "not listed", registry: `[{"plugin_id":"a"}]`},
		{name: "no registry"},
		{name: "malformed", registry: `{`, wantErr: "herdr: parse {path}: unexpected end of JSON input"},
		{
			name: "listed under XDG_CONFIG_HOME", registry: `[{"plugin_id":"herdr-shield"}]`,
			at: "{xdg}/herdr/plugins.json", env: map[string]string{"XDG_CONFIG_HOME": "{xdg}"}, want: true,
		},
		{
			name: "HERDR_CONFIG_PATH leaves the registry under the config home", registry: `[{"plugin_id":"herdr-shield"}]`,
			env: map[string]string{"HERDR_CONFIG_PATH": "{xdg}/elsewhere/config.toml"}, want: true,
		},
		{name: "a FIFO registry is an error", fifo: true, wantErr: "herdr: read {path}: not a regular file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, xdg := t.TempDir(), t.TempDir()
			fill := strings.NewReplacer("{home}", home, "{xdg}", xdg)
			env := &paths.MapEnv{Values: map[string]string{}}
			for name, value := range tc.env {
				env.Values[name] = fill.Replace(value)
			}
			path := filepath.Join(home, ".config", "herdr", "plugins.json")
			if tc.at != "" {
				path = fill.Replace(tc.at)
			}
			if got := paths.HerdrPluginRegistry(home, env); got != path {
				t.Fatalf("HerdrPluginRegistry = %s; want %s", got, path)
			}
			if tc.registry != "" || tc.fifo {
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if tc.registry != "" {
				if err := os.WriteFile(path, []byte(tc.registry), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if tc.fifo {
				if err := syscall.Mkfifo(path, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			type answer struct {
				linked bool
				err    error
			}
			answered := make(chan answer, 1)
			go func() {
				linked, err := PluginLinked(home, env, "herdr-shield")
				answered <- answer{linked, err}
			}()
			got, _ := testwait.Receive(t, "PluginLinked answers without blocking on "+path, answered)
			gotErr := ""
			if got.err != nil {
				gotErr = got.err.Error()
			}
			wantErr := strings.ReplaceAll(tc.wantErr, "{path}", path)
			if got.linked != tc.want || gotErr != wantErr {
				t.Fatalf("linked %t, error %q; want %t, %q", got.linked, gotErr, tc.want, wantErr)
			}
		})
	}
}

func TestServerNotRunning(t *testing.T) {
	for text, want := range map[string]bool{
		"error: Herdr server is NOT running": true,
		"plugin is locked":                   false,
		"":                                   false,
		"dial unix /run/herdr.sock: connect: no such file or directory":         true,
		"open /opt/plugins/herdr-shield/plugin.toml: no such file or directory": false,
	} {
		if got := ServerNotRunning(text); got != want {
			t.Fatalf("ServerNotRunning(%q) = %t; want %t", text, got, want)
		}
	}
}
