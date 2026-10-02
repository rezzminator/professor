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
	accountDir := pfmconfig.DefaultAccountDir(home, 1)
	accounts := []pfmconfig.Account{{ID: 1, ConfigDir: accountDir}}
	for _, ambient := range []string{accountDir, filepath.Join(home, "ambient")} {
		registries := ClaudeUserRegistries(home, accounts, ambient)
		count := 1
		if ambient != accountDir {
			count = 2
		}
		if len(registries) != count {
			t.Fatalf("registries=%#v, want %d", registries, count)
		}
		wantPath := filepath.Join(accountDir, ".claude.json")
		wantReason := "account 1 (CLAUDE_CONFIG_DIR=" + accountDir + " when pfm spawns it)"
		if registries[0].Path != wantPath || registries[0].Reason != wantReason || registries[0].Account != 1 {
			t.Fatalf("account registry=%#v, want path=%s reason=%q account=1", registries[0], wantPath, wantReason)
		}
		if ambient != accountDir {
			wantReason = "ambient CLAUDE_CONFIG_DIR=" + ambient + " (the claude launcher passes it through — internal_launch.go)"
			if registries[1].Path != filepath.Join(ambient, ".claude.json") || registries[1].Reason != wantReason ||
				registries[1].Account != 0 {
				t.Fatalf("ambient registry=%#v, want dir=%s reason=%q account=0", registries[1], ambient, wantReason)
			}
		}
	}
}
