package claudelaunch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// mcpPayload reads the --mcp-config word Render emitted: an absolute path to
// the launch's private MCP file, never inline JSON.
func mcpPayload(t *testing.T, word string) string {
	t.Helper()
	if !filepath.IsAbs(word) {
		t.Fatalf("--mcp-config = %q, want an absolute file path", word)
	}
	content, err := os.ReadFile(word)
	if err != nil {
		t.Fatalf("read --mcp-config file: %v", err)
	}
	return string(content)
}

// TestRenderKeepsMCPSecretsOffArgv pins that a third-party MCP entry's env
// value and Authorization header never reach argv, where /proc/{pid}/cmdline
// and ps show them to every local user: --mcp-config names a 0600 file in a
// 0700 directory under pfm's state, and the file carries the payload.
func TestRenderKeepsMCPSecretsOffArgv(t *testing.T) {
	const envValue, headerValue = "env-value-7f3a", "Bearer header-value-9c1e"
	for _, tc := range []struct {
		name      string
		purpose   Purpose
		professor bool
	}{
		{"third party only", PurposeInteractive, false},
		{"beside professor", PurposeInteractive, true},
		{"resume", PurposeResume, false},
		{"launcher", PurposeLauncher, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, machine := renderMachine(t)
			machine.MCPServers[pfmconfig.MCPServerChat] = pfmconfig.MCPServer{Enabled: tc.professor}
			machine.MCP.ThirdParty = map[string]json.RawMessage{
				"local": json.RawMessage(`{"command":"local-mcp","env":{"API_KEY":"` + envValue + `"}}`),
				"remote": json.RawMessage(
					`{"type":"http","url":"https://example.invalid/mcp","headers":{"Authorization":"` + headerValue + `"}}`,
				),
			}
			launch, parsed := renderParsed(t, Request{Purpose: tc.purpose, Home: home, Account: 1}, machine)
			for _, word := range append([]string{launch.Binary}, append(launch.Env, launch.Argv...)...) {
				if strings.Contains(word, envValue) || strings.Contains(word, "header-value") {
					t.Fatalf("a third-party secret reached the command line: %q", word)
				}
			}
			payload := mcpPayload(t, parsed.MCPConfig)
			if !strings.Contains(payload, envValue) || !strings.Contains(payload, headerValue) {
				t.Fatalf("mcp file lost the third-party entries: %s", payload)
			}
			info, err := os.Lstat(parsed.MCPConfig)
			if err != nil {
				t.Fatal(err)
			}
			if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
				t.Fatalf("mcp file mode = %v, want a regular 0600 file", info.Mode())
			}
			dir := filepath.Dir(parsed.MCPConfig)
			if dir != paths.ClaudeMCPConfigDir(home) {
				t.Fatalf("mcp file dir = %q, want pfm's state mcp-config", dir)
			}
			dirInfo, err := os.Lstat(dir)
			if err != nil {
				t.Fatal(err)
			}
			if !dirInfo.IsDir() || dirInfo.Mode().Perm() != 0o700 {
				t.Fatalf("mcp dir mode = %v, want a 0700 directory", dirInfo.Mode())
			}
		})
	}
}

// TestWriteMCPFilePrunesStaleLaunchFiles pins the cleanup: a launch MCP file
// past mcpFileMaxAge goes on the next write; a fresh one and any file pfm did
// not name stay.
func TestWriteMCPFilePrunesStaleLaunchFiles(t *testing.T) {
	home := t.TempDir()
	dir := paths.ClaudeMCPConfigDir(home)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-mcpFileMaxAge - time.Hour)
	files := map[string]bool{
		mcpFilePrefix + "stale" + mcpFileSuffix: false,
		mcpFilePrefix + "fresh" + mcpFileSuffix: true,
		"operator-notes.json":                   true,
	}
	for name := range files {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
		if name != mcpFilePrefix+"fresh"+mcpFileSuffix {
			if err := os.Chtimes(path, old, old); err != nil {
				t.Fatal(err)
			}
		}
	}
	path, err := writeMCPFile(home, []byte(`{"mcpServers":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(path) != dir {
		t.Fatalf("path = %q, want it in %q", path, dir)
	}
	for name, kept := range files {
		_, err := os.Stat(filepath.Join(dir, name))
		if exists := err == nil; exists != kept {
			t.Errorf("%s exists=%t, want %t (stat err %v)", name, exists, kept, err)
		}
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode = %v, want 0700 tightened from 0755", info.Mode().Perm())
	}
}

// TestWriteMCPFileRefusesASymlinkedDirectory pins that a planted symlink at
// the directory never redirects the payload elsewhere.
func TestWriteMCPFileRefusesASymlinkedDirectory(t *testing.T) {
	home := t.TempDir()
	elsewhere := t.TempDir()
	dir := paths.ClaudeMCPConfigDir(home)
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, dir); err != nil {
		t.Fatal(err)
	}
	_, err := writeMCPFile(home, []byte(`{}`))
	if err == nil || !strings.Contains(err.Error(), "is not a real directory") {
		t.Fatalf("err = %v, want a refused symlinked directory", err)
	}
	entries, err := os.ReadDir(elsewhere)
	if err != nil || len(entries) != 0 {
		t.Fatalf("symlink target holds %d entries (err %v), want none", len(entries), err)
	}
}
