package installer

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

type registryWriteHook func([]byte) (int, error)

func (f registryWriteHook) Write(p []byte) (int, error) { return f(p) }

func TestMCPPreservesManualSecondaryCodexClient(t *testing.T) {
	home := t.TempDir()
	account := filepath.Join(home, "secondary-codex")
	path := filepath.Join(account, "config.toml")
	original := "model = \"personal\"\n[mcp_servers.harvester]\ncommand = \"manual-harvester\"\n"
	writeFixture(t, path, original)
	e := engine{
		options: Options{Home: home, CodexHomes: []string{account}, MCPPort: 8377, Stdout: io.Discard},
		apply:   true,
		stamp:   "fixture",
	}
	if err := e.writeMCPCodeConfig([]string{"harvester"}); err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if _, err := toml.Decode(readFixture(t, path), &document); err != nil {
		t.Fatalf("install corrupted manual secondary Codex config: %v", err)
	}
}

func TestMCPFailedRemovalKeepsOwnershipForRetry(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".claude.json")
	managed := filepath.Join(home, "managed")
	e := engine{
		options:     Options{Home: home, ConfigDir: filepath.Join(home, ".claude"), Stdout: io.Discard},
		managedRoot: managed,
		apply:       true,
		stamp:       "fixture",
	}
	if _, err := e.writeMCPClientJSON([]string{"chat"}); err != nil {
		t.Fatal(err)
	}
	original := readFixture(t, path)
	injected := false
	e.options.Stdout = registryWriteHook(func(p []byte) (int, error) {
		if !injected && strings.Contains(string(p), "change  rewrite "+physicalSettingsPath(path)+" ") {
			injected = true
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(path, 0o700); err != nil {
				t.Fatal(err)
			}
		}
		return len(p), nil
	})
	if _, err := e.writeMCPClientJSON(nil); err == nil {
		t.Fatal("failure injection not exercised")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, path, original)
	e.options.Stdout = io.Discard
	if _, err := e.writeMCPClientJSON(nil); err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(readFixture(t, path)), &doc); err != nil {
		t.Fatal(err)
	}
	if _, exists := doc["mcpServers"].(map[string]any)["chat"]; exists {
		t.Fatal("retry could not remove owned registration: receipt was deleted before failing registry mutation")
	}
}

func TestRetirementPreservesSecondaryPersonalAgentLink(t *testing.T) {
	home := t.TempDir()
	second := filepath.Join(home, "secondary")
	personal := filepath.Join(home, "personal-agent.md")
	writeFixture(t, personal, "---\nname: personal\n---\nPrivate agent\n")
	path := filepath.Join(second, "agents", "frr.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(personal, path); err != nil {
		t.Fatal(err)
	}
	e := engine{
		options: Options{Home: home, ConfigDirs: []string{second}, CodexHomes: []string{}, Stdout: io.Discard},
		apply:   true,
	}
	if err := e.retireRenamedGlobalAgents(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("unrelated secondary personal agent link removed: %v", err)
	}
}

func TestMCPConfigSymlinkSurvivesInstallAndRemoval(t *testing.T) {
	home := t.TempDir()
	config := filepath.Join(home, "secondary", "config.toml")
	target := filepath.Join(home, "personal.toml")
	writeFixture(t, target, "model = \"personal\"\n")
	if err := os.MkdirAll(filepath.Dir(config), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, config); err != nil {
		t.Fatal(err)
	}
	e := engine{
		options: Options{Home: home, CodexHomes: []string{filepath.Dir(config)}, MCPPort: 8377, Stdout: io.Discard},
		apply:   true,
		stamp:   "fixture",
	}
	if err := e.writeMCPCodeConfig([]string{"chat"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readFixture(t, target), "mcp_servers.chat") {
		t.Fatal("physical target was not wired")
	}
	if err := e.removeMCPCodeConfig(); err != nil {
		t.Fatal(err)
	}
	if got, err := os.Readlink(config); err != nil || got != target {
		t.Fatalf("link=%q err=%v", got, err)
	}
	if strings.Contains(readFixture(t, target), "mcp_servers.chat") {
		t.Fatal("owned registration survived removal")
	}
}

func TestMCPRefusesConcurrentNativeRegistryUpdate(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".claude.json")
	writeFixture(t, path, `{"oauthAccount":{"accountUuid":"original"}}`)
	latest := `{"oauthAccount":{"accountUuid":"latest-native-login"}}`
	e := engine{
		options:     Options{Home: home, ConfigDir: filepath.Join(home, ".claude"), Stdout: io.Discard},
		managedRoot: filepath.Join(home, "managed"),
		apply:       true,
		stamp:       "fixture",
	}
	injected := false
	e.options.Stdout = registryWriteHook(func(p []byte) (int, error) {
		if !injected && strings.Contains(string(p), "change  rewrite "+physicalSettingsPath(path)+" ") {
			injected = true
			writeFixture(t, path, latest)
		}
		return len(p), nil
	})
	if _, err := e.writeMCPClientJSON(
		[]string{"chat"},
	); err == nil ||
		!strings.Contains(err.Error(), "changed while planning") {
		t.Fatalf("concurrent update error=%v", err)
	}
	if got := readFixture(t, path); got != latest {
		t.Fatalf("native state overwritten: %s", got)
	}
	e.options.Stdout = io.Discard
	if _, err := e.writeMCPClientJSON([]string{"chat"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readFixture(t, path), "latest-native-login") {
		t.Fatal("retry lost native login")
	}
}
