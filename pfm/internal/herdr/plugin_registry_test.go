package herdr

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPluginLinked(t *testing.T) {
	for _, tc := range []struct {
		name, registry string
		want           bool
		wantErr        string
	}{
		{name: "listed", registry: `[{"plugin_id":"a"},{"plugin_id":"herdr-shield"}]`, want: true},
		{name: "not listed", registry: `[{"plugin_id":"a"}]`},
		{name: "no registry"},
		{name: "malformed", registry: `{`, wantErr: "herdr: parse {path}: unexpected end of JSON input"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			path := PluginRegistryPath(home)
			if tc.registry != "" {
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(tc.registry), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			got, err := PluginLinked(home, "herdr-shield")
			gotErr := ""
			if err != nil {
				gotErr = err.Error()
			}
			wantErr := tc.wantErr
			if wantErr != "" {
				wantErr = "herdr: parse " + path + ": unexpected end of JSON input"
			}
			if got != tc.want || gotErr != wantErr {
				t.Fatalf("linked %t, error %q; want %t, %q", got, gotErr, tc.want, wantErr)
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
