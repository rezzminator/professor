package paths

import (
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
