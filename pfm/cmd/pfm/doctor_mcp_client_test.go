package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmconfig "hostops/pfm/internal/config"
)

// TestDoctorMCPClientRowNamesEachRegistryAndItsReason pins issue #24 finding
// 5's doctor half: on a host whose shell exports CLAUDE_CONFIG_DIR, the
// implicit account's ~/.claude.json is not the only file a pfm-launched
// `claude` can read — doctor must name BOTH registries with why each is one,
// and warn on the ambient one pfm never reached while the implicit one is
// healthy.
func TestDoctorMCPClientRowNamesEachRegistryAndItsReason(t *testing.T) {
	root := jailTest(t)
	home := filepath.Join(root, "home")
	ambient := filepath.Join(home, ".cc", "1")
	t.Setenv("CLAUDE_CONFIG_DIR", ambient)

	pfmBinary := filepath.Join(home, ".local", "bin", "pfm")
	registration := `{"mcpServers":{"harvester":{"type":"http","url":"http://127.0.0.1:18377/mcp/harvester"},"chat":{"type":"stdio","command":"` + pfmBinary + `","args":["mcp","chat","serve"]}}}`
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(registration), 0o600); err != nil {
		t.Fatal(err)
	}
	// The ambient CLAUDE_CONFIG_DIR's .claude.json deliberately does not exist.

	runtime, err := pfmconfig.LoadRuntime("")
	if err != nil {
		t.Fatal(err)
	}
	runtime.Config.Accounts = []pfmconfig.Account{{ID: 1, ConfigDir: filepath.Join(home, ".claude"), Implicit: true}}
	runtime.Config.MCP.HTTP.Port = 18377

	var stdout, stderr bytes.Buffer
	if code := runDoctor(nil, &stdout, &stderr, runtime); code != 1 {
		t.Fatalf(
			"doctor code=%d stdout=%q stderr=%q, want a warning for the absent ambient registry",
			code,
			stdout.String(),
			stderr.String(),
		)
	}
	out := stdout.String()
	implicitRow := "doctor: mcp client=claude registry=" + filepath.Join(home, ".claude.json") +
		" (account 1 (pfm spawns it without CLAUDE_CONFIG_DIR)) harvester=pfm chat=pfm"
	if !strings.Contains(out, implicitRow) {
		t.Fatalf("doctor output missing the healthy implicit-account row %q:\n%s", implicitRow, out)
	}
	ambientRow := "doctor: mcp client=claude registry=" + filepath.Join(ambient, ".claude.json") +
		" (ambient CLAUDE_CONFIG_DIR=" + ambient + " (the claude launcher passes it through — internal_launch.go)) harvester=absent chat=absent" +
		" remediation=run pfm install --yes (registers every registry a pfm-launched Claude reads)"
	if !strings.Contains(out, ambientRow) {
		t.Fatalf("doctor output missing the absent ambient-registry warning row %q:\n%s", ambientRow, out)
	}
}
