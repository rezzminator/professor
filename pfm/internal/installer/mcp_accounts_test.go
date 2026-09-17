package installer

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmconfig "hostops/pfm/internal/config"
)

func TestMCPWiresActualClaudeRegistriesAndHonorsEmptyCodex(t *testing.T) {
	home := t.TempDir()
	primary := filepath.Join(home, ".claude")
	secondary := filepath.Join(home, "account-two")
	paths := []string{filepath.Join(home, ".claude.json"), filepath.Join(secondary, ".claude.json")}
	for _, path := range paths {
		writeFixture(
			t,
			path,
			`{"oauthAccount":{"accountUuid":"private"},"mcpServers":{"foreign":{"command":"custom"}}}`,
		)
	}
	options := Options{
		Home:       home,
		ConfigDir:  primary,
		ConfigDirs: []string{primary, secondary},
		CodexHomes: []string{},
		Mode:       ModeApply,
		Runner:     &fakeRunner{},
		Stdout:     io.Discard,
		MCPEnabled: map[string]bool{"chat": true},
		MCPPort:    8377,
	}
	if _, err := Run(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		var doc map[string]any
		if err := json.Unmarshal([]byte(readFixture(t, path)), &doc); err != nil {
			t.Fatal(err)
		}
		servers := doc["mcpServers"].(map[string]any)
		if servers["chat"] == nil || servers["foreign"] == nil || doc["oauthAccount"] == nil {
			t.Errorf("registry %s lost wiring or private state: %#v", path, doc)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".codex")); !os.IsNotExist(err) {
		t.Errorf("empty Codex roster wrote .codex: %v", err)
	}
	// A user replacement after installation is preserved on uninstall.
	replacement := `{"oauthAccount":{"accountUuid":"private"},"mcpServers":{"chat":{"command":"manual"},"foreign":{"command":"custom"}}}`
	writeFixture(t, paths[1], replacement)
	options.Mode = ModeUninstall
	if _, err := Run(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(readFixture(t, paths[0]), `"chat"`) {
		t.Error("owned primary registration survived uninstall")
	}
	if got := readFixture(t, paths[1]); got != replacement {
		t.Errorf("manual replacement was changed: %s", got)
	}
}

// TestClaudeUserRegistriesIncludeTheAmbientConfigDirTheLauncherPassesThrough
// pins issue #24 finding 5: a `claude` typed into a shell that exports
// CLAUDE_CONFIG_DIR reads THAT directory's .claude.json, not the implicit
// account's $HOME/.claude.json a bare account-driven fanout would assume —
// the launcher shim passes the ambient var straight through
// (internal_launch.go), so the registry resolver must list both files, each
// naming why it is a registry pfm cares about.
func TestClaudeUserRegistriesIncludeTheAmbientConfigDirTheLauncherPassesThrough(t *testing.T) {
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
