package run

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmengine "hostops/pfm/internal/engine"
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

func TestRunOpenCodePersistenceUsesConfiguredDataHome(t *testing.T) {
	headlessJail(t)
	capture := t.TempDir()
	binary := writeOpenCodeStub(t, `printf '%s\n' "$XDG_DATA_HOME" > "$CAPTURE_DIR/data-home"
printf '%s\n' 'pfm-opencode-plugin-ready' > "$PFM_OPENCODE_PLUGIN_READY"
`+opencodeValidEvents())
	accountHome := opencodeHome(t)
	accountDataHome := filepath.Dir(accountHome)
	result, err := Run(context.Background(), Request{
		Config: opencodeMachine(binary, accountHome), Engine: pfmengine.OpenCode,
		TempDir: t.TempDir(), Prompt: "hello", Env: testEnv(capture, "CAPTURE_DIR="+capture),
	})
	if err != nil {
		t.Fatalf("persistent OpenCode Run() error = %v", err)
	}
	if result.Answer != "answer" {
		t.Fatalf("answer = %q, want answer", result.Answer)
	}
	if got := strings.TrimSpace(string(mustRead(t, filepath.Join(capture, "data-home")))); got != accountDataHome {
		t.Fatalf("persistent XDG_DATA_HOME = %q, want configured data home %q", got, accountDataHome)
	}
}

func TestRunOpenCodeSealedOwnsScratchAndClearsInheritedControls(t *testing.T) {
	headlessJail(t)
	base := t.TempDir()
	capture := t.TempDir()
	requestedCWD := t.TempDir()
	accountHome := opencodeHome(t)
	binary := writeOpenCodeStub(t, `pwd > "$CAPTURE_DIR/pwd"
printf '%s\n' "$XDG_DATA_HOME" > "$CAPTURE_DIR/data-home"
printf '%s\n' "$XDG_CONFIG_HOME" > "$CAPTURE_DIR/config-home"
printf '%s\n' "$OPENCODE_DISABLE_PROJECT_CONFIG" > "$CAPTURE_DIR/disable-project"
printf '%s\n' "$PFM_OPENCODE_STRICT_MCP" > "$CAPTURE_DIR/strict-mcp"
printf '%s\n' 'pfm-opencode-plugin-ready' > "$PFM_OPENCODE_PLUGIN_READY"
`+opencodeValidEvents())
	system := "sealed replacement"
	result, err := Run(context.Background(), Request{
		Config: opencodeMachine(binary, accountHome), Engine: pfmengine.OpenCode,
		Prompt: "hello", CWD: requestedCWD, TempDir: base, SystemPrompt: &system,
		Sealed: true, Env: testEnv(capture, "CAPTURE_DIR="+capture,
			"OPENCODE_CONFIG=ambient-config", "OPENCODE_CONFIG_DIR=ambient-dir"),
	})
	if err != nil {
		t.Fatalf("sealed OpenCode Run() error = %v", err)
	}
	if result.Answer != "answer" {
		t.Fatalf("answer = %q, want answer", result.Answer)
	}
	pwd := strings.TrimSpace(string(mustRead(t, filepath.Join(capture, "pwd"))))
	if filepath.Clean(pwd) == filepath.Clean(requestedCWD) || !insideTempBase(t, pwd, base) {
		t.Fatalf("sealed cwd = %q, want a temporary child of %q", pwd, base)
	}
	if _, err := os.Stat(pwd); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("sealed cwd was not removed: stat err=%v", err)
	}
	if got := strings.TrimSpace(
		string(mustRead(t, filepath.Join(capture, "data-home"))),
	); got == filepath.Dir(
		accountHome,
	) {
		t.Fatalf("sealed XDG_DATA_HOME reused configured data home %q", got)
	}
	if got := strings.TrimSpace(
		string(mustRead(t, filepath.Join(capture, "config-home"))),
	); got == "" ||
		got == "ambient-config" {
		t.Fatalf("sealed XDG_CONFIG_HOME = %q, want private config home", got)
	}
	if got := strings.TrimSpace(string(mustRead(t, filepath.Join(capture, "disable-project")))); got != "1" {
		t.Fatalf("sealed project config control = %q, want 1", got)
	}
	if got := strings.TrimSpace(string(mustRead(t, filepath.Join(capture, "strict-mcp")))); got != "1" {
		t.Fatalf("sealed strict MCP control = %q, want 1", got)
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
