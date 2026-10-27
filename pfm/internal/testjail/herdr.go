package testjail

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

// StageHerdrShell supplies the config and wrapper of an installed Herdr host.
func StageHerdrShell(t *testing.T, home string) string {
	t.Helper()
	path := filepath.Join(home, ".config", "herdr", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	wrapper := paths.HerdrShellWrapper(home)
	if err := os.MkdirAll(filepath.Dir(wrapper), 0o700); err != nil {
		t.Fatal(err)
	}
	asset := filepath.Join(checkoutRoot(), "pfm", "internal", "installer", "assets", "shim", "pfm-herdr-shell.sh")
	raw, err := os.ReadFile(asset)
	if err != nil {
		t.Fatalf("read installed Herdr shell fixture asset: %v", err)
	}
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'" }
	contentWrapper := strings.NewReplacer("__PFM_REAL_SHELL__", quote("/bin/sh"), "__PFM_WRAPPER__", quote(wrapper)).
		Replace(string(raw))
	if err := WriteExecutable(wrapper, []byte(contentWrapper), 0o755); err != nil {
		t.Fatal(err)
	}
	content := fmt.Sprintf(
		"[terminal]\n# >>> pfm herdr shell >>>\ndefault_shell = %q\n# <<< pfm herdr shell <<<\n",
		wrapper,
	)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// HerdrPaneInfo returns the required Herdr 0.9.5 PaneInfo identity fields.
// Optional fields belong to the test exercising them, not this common fixture.
func HerdrPaneInfo(pane string) map[string]any {
	workspace, _, _ := strings.Cut(pane, ":")
	return map[string]any{
		"type": "pane_info",
		"pane": map[string]any{
			"pane_id": pane, "workspace_id": workspace,
			"terminal_id": "fixture-" + pane, "tab_id": workspace + ":t1", "focused": true,
		},
	}
}
