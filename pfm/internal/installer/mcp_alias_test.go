package installer

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestMCPReconcilesOwnershipAfterRegistryMovesToSymlinkTarget(t *testing.T) {
	home := t.TempDir()
	logical := filepath.Join(home, ".claude.json")
	physical := filepath.Join(home, ".0-private", "claude.json")
	e := engine{
		options:     Options{Home: home, ConfigDir: filepath.Join(home, ".claude"), Stdout: io.Discard},
		managedRoot: filepath.Join(home, "managed"),
		apply:       true,
		stamp:       "fixture",
	}
	if _, err := e.writeMCPClientJSON([]string{"chat"}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(physical), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(logical, physical); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(physical, logical); err != nil {
		t.Fatal(err)
	}
	if _, err := e.writeMCPClientJSON([]string{"chat"}); err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal([]byte(readFixture(t, logical)), &document); err != nil {
		t.Fatal(err)
	}
	if _, ok := document["mcpServers"].(map[string]any)["chat"]; !ok {
		t.Fatal("successful reinstall removed the enabled chat registration after config became a symlink")
	}
}

func TestMCPLegacyLedgerFollowsRegistrySymlink(t *testing.T) {
	home := t.TempDir()
	logical := filepath.Join(home, ".mcp.json")
	physical := filepath.Join(home, ".claude.json")
	e := engine{
		options:     Options{Home: home, ConfigDir: filepath.Join(home, ".claude"), MCPPort: 8377, Stdout: io.Discard},
		managedRoot: filepath.Join(home, "managed"),
		apply:       true,
		stamp:       "fixture",
	}
	raw, err := json.Marshal(map[string]any{"mcpServers": map[string]any{"chat": e.mcpClientRegistration("chat")}})
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, physical, string(raw))
	if err := os.Symlink(physical, logical); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, e.mcpOwnershipPath(), `{"clients":["chat"]}`)
	if _, err := e.writeMCPClientJSON([]string{"chat"}); err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(readFixture(t, physical)), &doc); err != nil {
		t.Fatal(err)
	}
	if doc["mcpServers"].(map[string]any)["chat"] == nil {
		t.Fatal("migration removed enabled chat")
	}
	if _, err := e.writeMCPClientJSON(nil); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(readFixture(t, physical)), &doc); err != nil {
		t.Fatal(err)
	}
	if doc["mcpServers"].(map[string]any)["chat"] != nil {
		t.Fatal("uninstall lost migrated ownership")
	}
	if _, err := os.Readlink(logical); err != nil {
		t.Fatal(err)
	}
}
