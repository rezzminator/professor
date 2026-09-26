package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestMCPEnableRefusesUnresolvedConfigPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv(paths.EnvConfig, "")
	loaded, err := Load("", home, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, server := range []string{"chat", "harvester"} {
		if _, err := SetMCPServer(
			loaded,
			server,
			true,
		); err == nil ||
			!strings.Contains(err.Error(), "no config path") {
			t.Fatalf("enable %s without a path = %v", server, err)
		}
	}
}

func TestSetMCPServerIsIndependentAtomicAndIdempotent(t *testing.T) {
	registered := map[string]MCPServer{
		"chat":      {Enabled: false},
		"harvester": {Enabled: false},
	}
	home := filepath.Join(t.TempDir(), "home")
	path := filepath.Join(t.TempDir(), "deep", "machine.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{
  "version": 1,
  "claude": {"binary": "claude-custom"},
  "mcp": {"servers": {"chat": {"enabled": false}, "harvester": {"enabled": false}}}
}`), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadWithMCPServers(path, home, nil, registered)
	if err != nil {
		t.Fatalf("initial Load error = %v", err)
	}

	changed, err := SetMCPServer(loaded, "chat", true)
	if err != nil || !changed {
		t.Fatalf("enable chat = changed:%v err:%v, want changed true", changed, err)
	}
	afterChat, err := loadWithMCPServers(path, home, nil, registered)
	if err != nil {
		t.Fatalf("Load(after chat) error = %v", err)
	}
	if !afterChat.MCPServers["chat"].Enabled || afterChat.MCPServers["harvester"].Enabled {
		t.Fatalf("after chat toggle MCPServers = %#v, want chat true and harvester false", afterChat.MCPServers)
	}
	if afterChat.Claude.Binary != "claude-custom" {
		t.Fatal("SetMCPServer discarded unrelated configuration")
	}
	beforeNoop, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	changed, err = SetMCPServer(afterChat, "chat", true)
	if err != nil || changed {
		t.Fatalf("repeat enable chat = changed:%v err:%v, want no-op", changed, err)
	}
	afterNoop, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(afterNoop, beforeNoop) {
		t.Fatal("idempotent SetMCPServer rewrote the config")
	}

	changed, err = SetMCPServer(afterChat, "harvester", true)
	if err != nil || !changed {
		t.Fatalf("enable harvester = changed:%v err:%v, want changed true", changed, err)
	}
	afterHarvester, err := loadWithMCPServers(path, home, nil, registered)
	if err != nil {
		t.Fatalf("Load(after harvester) error = %v", err)
	}
	if !afterHarvester.MCPServers["chat"].Enabled || !afterHarvester.MCPServers["harvester"].Enabled {
		t.Fatalf("after harvester toggle MCPServers = %#v, want both enabled", afterHarvester.MCPServers)
	}

	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".config.json.tmp-") {
			t.Errorf("atomic scratch file remains: %s", entry.Name())
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("config mode = %o, want 600", info.Mode().Perm())
	}
}

func TestSetMCPServerRejectsUnknownServerWithoutWriting(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	path := filepath.Join(t.TempDir(), "config.json")
	loaded, err := Load(path, home, nil)
	if err != nil {
		t.Fatal(err)
	}
	if changed, err := SetMCPServer(loaded, "not-registered", true); err == nil || changed {
		t.Fatalf("unknown server = changed:%v err:%v, want error and no change", changed, err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unknown server created config: stat error = %v", err)
	}
}

func TestRegisteredMCPServersIsSorted(t *testing.T) {
	got := RegisteredMCPServers()
	if !sort.StringsAreSorted(got) || !reflect.DeepEqual(got, []string{"chat", "harvester"}) {
		t.Fatalf("RegisteredMCPServers() = %#v, want sorted production registry", got)
	}
}

func TestSetMCPServerWritesValidJSON(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	path := filepath.Join(t.TempDir(), "new", "config.json")
	loaded, err := Load(path, home, nil)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := SetMCPServer(loaded, "chat", true)
	if err != nil || !changed {
		t.Fatalf("SetMCPServer = changed:%v err:%v", changed, err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(content, &decoded); err != nil {
		t.Fatalf("written config is not JSON: %v", err)
	}
	if got, ok := decoded["version"].(float64); !ok || got != Version {
		t.Fatalf("written version = %#v, want %d", decoded["version"], Version)
	}
}

func TestRemoveLegacyMCPAuthTokenPreservesUnrelatedConfig(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, FileName)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	legacy := `{"version":2,"theme":"midnight","mcp":{"authToken":"retired-secret","http":{"port":8456},"servers":{"harvester":{"enabled":true}}}}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := Load(path, home, nil)
	if err != nil {
		t.Fatalf("legacy config must remain loadable for cleanup: %v", err)
	}
	changed, err := RemoveMCPAuthToken(config)
	if err != nil || !changed {
		t.Fatalf("RemoveMCPAuthToken changed=%t err=%v", changed, err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "authToken") || strings.Contains(string(body), "retired-secret") {
		t.Fatalf("legacy auth survived cleanup: %s", body)
	}
	for _, retained := range []string{`"theme": "midnight"`, `"port": 8456`, `"harvester"`} {
		if !strings.Contains(string(body), retained) {
			t.Fatalf("cleanup dropped unrelated config %q: %s", retained, body)
		}
	}
}
