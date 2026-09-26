package installer

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

func TestMCPWiresCodexAndOpenCodeWithoutClaudeRegistry(t *testing.T) {
	home := t.TempDir()
	registry := filepath.Join(home, ".claude.json")
	original := `{"mcpServers":{"other":{"command":"operator"}}}`
	writeFixture(t, registry, original)
	codex := filepath.Join(home, ".codex")
	opencode := OpenCodeConfigPath(home)
	e := engine{
		options: Options{
			Home: home, CodexHomes: []string{codex}, OpenCodeConfigPath: opencode,
			MCPEnabled: map[string]bool{"chat": true, "harvester": true}, MCPPort: 8377, Stdout: io.Discard,
		},
		apply: true, managedRoot: managedRootForHome(home), stamp: "fixture",
	}
	if err := e.wireMCP(); err != nil {
		t.Fatal(err)
	}
	if got := readFixture(t, registry); got != original {
		t.Fatalf("Claude registry changed: %s", got)
	}
	for _, path := range []string{filepath.Join(codex, "config.toml"), opencode} {
		content, err := os.ReadFile(path)
		if err != nil || !strings.Contains(string(content), professorName) {
			t.Fatalf("%s lacks the professor MCP server: %s err=%v", path, content, err)
		}
	}
}

func TestClaudeUserRegistriesIncludeTheAmbientConfigDirTheLauncherPassesThrough(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	ambient := filepath.Join(home, ".cc", "1")
	accounts := []pfmconfig.Account{{ID: 1, ConfigDir: ambient, Implicit: true}}

	registries := ClaudeUserRegistries(home, accounts, ambient)

	if len(registries) != 2 {
		t.Fatalf("registries=%#v, want exactly 2 (implicit account + ambient)", registries)
	}
	implicitPath := filepath.Join(home, ".claude.json")
	if registries[0].Path != implicitPath {
		t.Fatalf("registries[0].Path=%s, want the implicit account's %s", registries[0].Path, implicitPath)
	}
	if registries[0].Reason != "account 1 (pfm spawns it without CLAUDE_CONFIG_DIR)" {
		t.Fatalf("registries[0].Reason=%q, want the implicit-account reason", registries[0].Reason)
	}
	ambientPath := filepath.Join(ambient, ".claude.json")
	if registries[1].Path != ambientPath {
		t.Fatalf("registries[1].Path=%s, want the ambient CLAUDE_CONFIG_DIR file %s", registries[1].Path, ambientPath)
	}
	wantReason := "ambient CLAUDE_CONFIG_DIR=" + ambient + " (the claude launcher passes it through — internal_launch.go)"
	if registries[1].Reason != wantReason {
		t.Fatalf("registries[1].Reason=%q, want %q", registries[1].Reason, wantReason)
	}
}
