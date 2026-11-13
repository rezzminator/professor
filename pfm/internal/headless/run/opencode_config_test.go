package run

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigureOpenCodeProjectSourceDoesNotReuseNativeLocalConfig(t *testing.T) {
	runDir := t.TempDir()
	operatorHome := t.TempDir()
	cwd := t.TempDir()
	localDir := filepath.Join(cwd, ".opencode")
	if err := os.MkdirAll(localDir, 0o700); err != nil {
		t.Fatal(err)
	}
	projectConfig := filepath.Join(cwd, "opencode.jsonc")
	if err := os.WriteFile(projectConfig, []byte(`{"model":"project"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	values := map[string]string{
		"HOME":                            operatorHome,
		"XDG_CONFIG_HOME":                 "ambient-config",
		"OPENCODE_CONFIG_DIR":             "ambient-local",
		"OPENCODE_DISABLE_PROJECT_CONFIG": "ambient-disable",
	}
	if err := configureOpenCodeSources(values, runDir, operatorHome, cwd, "project"); err != nil {
		t.Fatalf("configureOpenCodeSources() error = %v", err)
	}
	if got := values["OPENCODE_CONFIG_DIR"]; got != "" {
		t.Fatalf("project-only local config dir = %q, want disabled", got)
	}
	if got := values["OPENCODE_CONFIG"]; got != projectConfig {
		t.Fatalf("project-only config source = %q, want nearest project file %q", got, projectConfig)
	}
	if got := values["OPENCODE_DISABLE_PROJECT_CONFIG"]; got != "1" {
		t.Fatalf("project-only native discovery control = %q, want 1", got)
	}
}

func TestConfigureOpenCodeUserSourceUsesOperatorConfigAndLocalStores(t *testing.T) {
	runDir := t.TempDir()
	operatorHome := t.TempDir()
	values := map[string]string{
		"HOME":                            t.TempDir(),
		"XDG_CONFIG_HOME":                 "ambient-config",
		"OPENCODE_CONFIG_DIR":             "ambient-local",
		"OPENCODE_DISABLE_PROJECT_CONFIG": "ambient-disable",
	}
	if err := configureOpenCodeSources(values, runDir, operatorHome, t.TempDir(), "user"); err != nil {
		t.Fatalf("configureOpenCodeSources() error = %v", err)
	}
	if got := values["HOME"]; got != operatorHome {
		t.Fatalf("user source HOME = %q, want operator home %q", got, operatorHome)
	}
	if got := values["XDG_CONFIG_HOME"]; got != "ambient-config" {
		t.Fatalf("user source XDG_CONFIG_HOME = %q, want preserved XDG config", got)
	}
	if got := values["OPENCODE_CONFIG_DIR"]; got != "" {
		t.Fatalf("user source local config dir = %q, want disabled project-local override", got)
	}
	if got := values["OPENCODE_DISABLE_PROJECT_CONFIG"]; got != "1" {
		t.Fatalf("user source native project discovery control = %q, want 1", got)
	}
}

func TestOpenCodeAutoPermission(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want bool
	}{
		{nil, false},
		{[]string{"--auto"}, true},
		{[]string{"--yolo=false"}, false},
		{[]string{"--auto", "false"}, false},
		{[]string{"--auto", "--no-auto"}, false},
		{[]string{"--auto=false", "--dangerously-skip-permissions"}, true},
		{[]string{"--", "--auto"}, false},
	} {
		if got := openCodeAutoPermission(tc.args); got != tc.want {
			t.Errorf("%q: got %v, want %v", tc.args, got, tc.want)
		}
	}
}
