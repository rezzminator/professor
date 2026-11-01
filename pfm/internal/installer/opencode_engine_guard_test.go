package installer

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestOpenCodeEngineGuardPluginRoundTrip stages the plugin where OpenCode
// scans its config dir, carrying this home's pfm binary and the guard's
// entry, leaves a current copy alone, and removes it on uninstall.
func TestOpenCodeEngineGuardPluginRoundTrip(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	plugin := filepath.Join(home, ".config", "opencode", "plugins", "pfm-engine-guard.js")
	var output bytes.Buffer
	e := &engine{apply: true, options: Options{
		Mode: ModeApply, Home: home, OpenCodeConfigPath: OpenCodeConfigPath(home), Stdout: &output,
	}}
	if err := e.wireOpenCodeEngineGuard(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(plugin)
	if err != nil {
		t.Fatalf("plugin not staged at %s: %v\noutput:\n%s", plugin, err, output.String())
	}
	lines := strings.Split(string(raw), "\n")
	for _, want := range []string{
		`const PFM_BINARY = "` + home + `/.local/bin/pfm"`,
		`const ENTRY = ["internal", "engine-guard", "--engine", "opencode"]`,
		`const TOOLS = new Set(["bash", "edit", "write", "apply_patch", "multiedit", "patch"])`,
	} {
		if !slices.Contains(lines, want) {
			t.Fatalf("staged plugin %s lacks the line\n%s\ngot:\n%s", plugin, want, raw)
		}
	}
	if got, want := output.String(), "  change  create "+plugin+"\n"; got != want {
		t.Fatalf("install output=%q, want %q", got, want)
	}

	output.Reset()
	if err := e.wireOpenCodeEngineGuard(); err != nil {
		t.Fatal(err)
	}
	if got, want := output.String(), "  ok      "+plugin+"\n"; got != want {
		t.Fatalf("second install output=%q, want %q", got, want)
	}

	output.Reset()
	e.options.Mode = ModeUninstall
	if err := e.wireOpenCodeEngineGuard(); err != nil {
		t.Fatal(err)
	}
	if got, want := output.String(), "  change  remove "+plugin+"\n"; got != want {
		t.Fatalf("uninstall output=%q, want %q", got, want)
	}
	if _, err := os.Stat(plugin); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("uninstall left %s: %v", plugin, err)
	}
}
