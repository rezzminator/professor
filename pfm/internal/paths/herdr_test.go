package paths

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHerdrConfig(t *testing.T) {
	home := t.TempDir()
	for _, tc := range []struct {
		name          string
		values        map[string]string
		want, problem string
	}{
		{name: "default", want: filepath.Join(home, ".config", "herdr", "config.toml")},
		{name: "empty xdg", values: map[string]string{"XDG_CONFIG_HOME": ""}, want: filepath.Join(home, ".config", "herdr", "config.toml")},
		{name: "xdg", values: map[string]string{"XDG_CONFIG_HOME": home}, want: filepath.Join(home, "herdr", "config.toml")},
		{name: "override", values: map[string]string{"HERDR_CONFIG_PATH": filepath.Join(home, "custom.toml")}, want: filepath.Join(home, "custom.toml")},
		{name: "empty override", values: map[string]string{"HERDR_CONFIG_PATH": ""}, problem: "HERDR_CONFIG_PATH is empty"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path, err := HerdrConfig(home, &MapEnv{Values: tc.values})
			if tc.problem != "" {
				if err == nil || !strings.Contains(err.Error(), tc.problem) {
					t.Fatalf("path=%s err=%v", path, err)
				}
				return
			}
			if err != nil || path != tc.want {
				t.Fatalf("path=%s err=%v want=%s", path, err, tc.want)
			}
		})
	}
	debug := filepath.Join(home, ".config", "herdr-dev", "config.toml")
	if err := os.MkdirAll(filepath.Dir(debug), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(debug, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := HerdrConfig(home, &MapEnv{}); err == nil || !strings.Contains(err.Error(), "set HERDR_CONFIG_PATH") {
		t.Fatalf("debug ambiguity: %v", err)
	}
	if got := HerdrShellWrapper(
		home,
	); got != filepath.Join(
		home,
		".local",
		"share",
		"pfm",
		"install",
		"bin",
		"pfm-herdr-shell",
	) {
		t.Fatalf("wrapper=%s", got)
	}
}

func TestHerdrServerSocket(t *testing.T) {
	failure := errors.New("no home")
	for _, tc := range []struct {
		name      string
		values    map[string]string
		homeErr   error
		want, err string
	}{
		{name: "default session", want: "/u/.config/herdr/herdr.sock"},
		{name: "empty xdg", values: map[string]string{"XDG_CONFIG_HOME": ""}, want: "/u/.config/herdr/herdr.sock"},
		{name: "xdg", values: map[string]string{"XDG_CONFIG_HOME": "/x"}, want: "/x/herdr/herdr.sock"},
		{name: "named session", values: map[string]string{"HERDR_SESSION": "journey"}, want: "/u/.config/herdr/sessions/journey/herdr.sock"},
		{name: "explicit socket wins", values: map[string]string{"HERDR_SOCKET_PATH": "/s/h.sock", "HERDR_SESSION": "journey"}, want: "/s/h.sock"},
		{name: "explicit socket needs no home", values: map[string]string{"HERDR_SOCKET_PATH": "/s/h.sock"}, homeErr: failure, want: "/s/h.sock"},
		{name: "no home", homeErr: failure, err: "resolve the Herdr socket: home directory: no home"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := HerdrServerSocket(&MapEnv{Values: tc.values, HomeDir: "/u", HomeErr: tc.homeErr})
			gotErr := ""
			if err != nil {
				gotErr = err.Error()
			}
			if got != tc.want || gotErr != tc.err {
				t.Fatalf("HerdrServerSocket = %q, %q; want %q, %q", got, gotErr, tc.want, tc.err)
			}
		})
	}
}
