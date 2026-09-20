package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	pfmengine "hostops/pfm/internal/engine"
)

func TestResolvePathUsesAbsoluteXDGOrHomeConfig(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")

	t.Run("absolute XDG", func(t *testing.T) {
		xdg := filepath.Join(t.TempDir(), "xdg")
		t.Setenv("XDG_CONFIG_HOME", xdg)
		want := filepath.Join(xdg, "pfm", FileName)
		if got := ResolvePath(home); got != want {
			t.Fatalf("ResolvePath(%q) = %q, want %q", home, got, want)
		}
	})

	for _, tc := range []struct {
		name string
		xdg  string
	}{
		{name: "relative XDG", xdg: "relative-config"},
		{name: "empty XDG", xdg: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", tc.xdg)
			want := filepath.Join(home, ".config", "pfm", FileName)
			if got := ResolvePath(home); got != want {
				t.Fatalf("ResolvePath(%q) = %q, want %q", home, got, want)
			}
		})
	}
}

func TestDefaultsWithDiscoveryRoots(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	roots := []string{
		filepath.Join(home, ".cc", "one", "projects"),
		filepath.Join(home, ".cc", "two", "projects"),
	}

	got := Defaults(home, roots)
	if got.Version != Version {
		t.Fatalf("Version = %d, want %d", got.Version, Version)
	}
	wantAccounts := []Account{
		{
			ID:         1,
			ConfigDir:  filepath.Join(home, ".cc", "one"),
			ProjectDir: filepath.Join(home, ".cc", "one", "projects"),
			Implicit:   true,
			Emoji:      "🥇",
		},
		{
			ID:         2,
			ConfigDir:  filepath.Join(home, ".cc", "two"),
			ProjectDir: filepath.Join(home, ".cc", "two", "projects"),
			Emoji:      "🥈",
		},
	}
	if !reflect.DeepEqual(got.Accounts, wantAccounts) {
		t.Fatalf("Accounts = %#v, want %#v", got.Accounts, wantAccounts)
	}
	if got.Claude != (Claude{
		PermissionMode: PermissionBypass,
		Binary:         "claude",
		Cache1H:        true,
		CompactNudge:   DefaultCompactNudge(),

		MaxSubagentSpawnDepth: DefaultSubagentSpawnDepth,
	}) {
		t.Fatalf("Claude = %#v, want bypass/claude defaults", got.Claude)
	}
	if got.Codex != (Codex{Yolo: true, Binary: "codex"}) {
		t.Fatalf("Codex = %#v, want yolo/codex defaults", got.Codex)
	}
	if !reflect.DeepEqual(got.MCPServers, map[string]MCPServer{
		"chat": {Enabled: false}, "harvester": {Enabled: false},
	}) {
		t.Fatalf("MCPServers = %#v, want chat and harvester disabled", got.MCPServers)
	}
	for _, key := range []string{
		"version", "accounts", "claude.permissionMode", "claude.binary", "claude.cache1h", "codex.yolo", "codex.binary", "mcp.servers.chat.enabled", "mcp.servers.harvester.enabled",
	} {
		if got.Source(key) != SourceDefault {
			t.Errorf("Source(%q) = %q, want %q", key, got.Source(key), SourceDefault)
		}
	}
}

func TestDefaultsRegisterHarvesterDisabledByDefault(t *testing.T) {
	got := Defaults(filepath.Join(t.TempDir(), "home"), nil)
	server, ok := got.MCPServers["harvester"]
	if !ok {
		t.Fatal("harvester MCP server is not registered")
	}
	if server.Enabled {
		t.Fatal("harvester MCP server is enabled by default")
	}
	if got.Source("mcp.servers.harvester.enabled") != SourceDefault {
		t.Fatalf("harvester source = %q, want default", got.Source("mcp.servers.harvester.enabled"))
	}
}

func TestDefaultEmojiOwnsTheConventionalBadgeRoster(t *testing.T) {
	for _, testCase := range []struct {
		id   int
		want string
	}{
		{id: 1, want: "🥇"},
		{id: 2, want: "🥈"},
		{id: 3, want: "🥉"},
		{id: 4, want: "🍀"},
		{id: 99, want: "·"},
	} {
		if got := DefaultEmoji(testCase.id); got != testCase.want {
			t.Fatalf("DefaultEmoji(%d)=%q, want %q", testCase.id, got, testCase.want)
		}
	}
}

func TestDefaultsWithoutDiscoveryRootsDiscoversCredentialedAccountsAndNamesSkips(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	for _, account := range []int{1, 2, 3, 4} {
		configDir := filepath.Join(home, ".cc", strconv.Itoa(account))
		if err := os.MkdirAll(configDir, 0o700); err != nil {
			t.Fatal(err)
		}
		if account == 4 {
			continue
		}
		credentials := `{"claudeAiOauth":{"accessToken":"fixture","refreshToken":"fixture"}}`
		if err := os.WriteFile(filepath.Join(configDir, ".credentials.json"), []byte(credentials), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got := Defaults(home, nil)
	want := []Account{
		{
			ID:         1,
			ConfigDir:  filepath.Join(home, ".cc", "1"),
			ProjectDir: filepath.Join(home, ".cc", "1", "projects"),
			Implicit:   true,
			Emoji:      "🥇",
		},
		{
			ID:         2,
			ConfigDir:  filepath.Join(home, ".cc", "2"),
			ProjectDir: filepath.Join(home, ".cc", "2", "projects"),
			Emoji:      "🥈",
		},
		{
			ID:         3,
			ConfigDir:  filepath.Join(home, ".cc", "3"),
			ProjectDir: filepath.Join(home, ".cc", "3", "projects"),
			Emoji:      "🥉",
		},
	}
	if !reflect.DeepEqual(got.Accounts, want) {
		t.Fatalf("Accounts = %#v, want %#v", got.Accounts, want)
	}
	wantSkips := []AccountSkip{{
		ID: 4, ConfigDir: filepath.Join(home, ".cc", "4"), Reason: "no valid credentials",
	}}
	if !reflect.DeepEqual(got.AccountSkips, wantSkips) {
		t.Fatalf("AccountSkips = %#v, want %#v", got.AccountSkips, wantSkips)
	}
}

func TestLoadAbsentReturnsDefaultsAndResolvedPath(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	path := filepath.Join(t.TempDir(), "nested", "machine.json")
	roots := []string{filepath.Join(home, ".cc", "1", "projects")}

	got, err := Load(path, home, roots)
	if err != nil {
		t.Fatalf("Load(absent) error = %v", err)
	}
	if got.Exists {
		t.Fatal("Load(absent) Exists = true, want false")
	}
	if got.Path != filepath.Clean(path) {
		t.Fatalf("Load(absent) Path = %q, want %q", got.Path, filepath.Clean(path))
	}
	if !reflect.DeepEqual(got.Accounts, Defaults(home, roots).Accounts) {
		t.Fatalf("Load(absent) accounts = %#v, want Defaults accounts", got.Accounts)
	}
}

func TestLoadConfiguredAccountsExpandHomeAndPreserveIDs(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	path := filepath.Join(t.TempDir(), "config.json")
	content := `{
  "version": 1,
  "accounts": [
    {"id": 9, "configDir": "~/account-nine"},
    {"id": 2, "configDir": "$HOME/account-two"},
    {"id": 17, "configDir": "/srv/claude/account-seventeen"}
  ],
  "claude": {"permissionMode": "prompt", "binary": "claude-custom"},
  "codex": {"yolo": false, "binary": "codex-custom"},
  "mcp": {"servers": {"chat": {"enabled": true}}}
}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := Load(path, home, nil)
	if err != nil {
		t.Fatalf("Load(configured) error = %v", err)
	}
	want := []Account{
		{
			ID:         9,
			ConfigDir:  filepath.Join(home, "account-nine"),
			ProjectDir: filepath.Join(home, "account-nine", "projects"),
			Emoji:      "·",
		},
		{
			ID:         2,
			ConfigDir:  filepath.Join(home, "account-two"),
			ProjectDir: filepath.Join(home, "account-two", "projects"),
			Emoji:      "🥈",
		},
		{
			ID:         17,
			ConfigDir:  "/srv/claude/account-seventeen",
			ProjectDir: "/srv/claude/account-seventeen/projects",
			Emoji:      "·",
		},
	}
	if !reflect.DeepEqual(got.Accounts, want) {
		t.Fatalf("Accounts = %#v, want %#v", got.Accounts, want)
	}
	if got.AccountIDs() == nil || !reflect.DeepEqual(got.AccountIDs(), []int{9, 2, 17}) {
		t.Fatalf("AccountIDs = %#v, want [9 2 17]", got.AccountIDs())
	}
	if got.Claude != (Claude{
		PermissionMode: PermissionPrompt,
		Binary:         "claude-custom",
		Cache1H:        true,
		CompactNudge:   DefaultCompactNudge(),

		MaxSubagentSpawnDepth: DefaultSubagentSpawnDepth,
	}) {
		t.Fatalf("Claude = %#v, want configured values", got.Claude)
	}
	if got.Codex != (Codex{Yolo: false, Binary: "codex-custom"}) {
		t.Fatalf("Codex = %#v, want configured values", got.Codex)
	}
	if !got.Exists || got.Source("accounts") != SourceFile || got.Source("mcp.servers.chat.enabled") != SourceFile {
		t.Fatalf(
			"configured sources/exists = exists:%v accounts:%q chat:%q",
			got.Exists,
			got.Source("accounts"),
			got.Source("mcp.servers.chat.enabled"),
		)
	}
	for _, key := range []string{"claude.permissionMode", "claude.binary", "codex.yolo", "codex.binary", "version"} {
		if got.Source(key) != SourceFile {
			t.Errorf("Source(%q) = %q, want %q", key, got.Source(key), SourceFile)
		}
	}
}

func TestLoadConfiguredAccountsDropUnregisteredDiscoverySkips(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	for _, account := range []int{1, 4} {
		configDir := DefaultAccountDir(home, account)
		if err := os.MkdirAll(configDir, 0o700); err != nil {
			t.Fatal(err)
		}
		if account == 1 {
			credentials := `{"claudeAiOauth":{"accessToken":"fixture"}}`
			if err := os.WriteFile(
				filepath.Join(configDir, ".credentials.json"),
				[]byte(credentials),
				0o600,
			); err != nil {
				t.Fatal(err)
			}
		}
	}

	path := filepath.Join(t.TempDir(), "config.json")
	content := fmt.Sprintf(`{"version":2,"accounts":[{"id":1,"configDir":%q}]}`, DefaultAccountDir(home, 1))
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := Load(path, home, nil)
	if err != nil {
		t.Fatalf("Load(configured) error = %v", err)
	}
	if len(got.AccountSkips) != 0 {
		t.Fatalf("configured roster leaked unregistered discovery skips: %#v", got.AccountSkips)
	}
}

func TestSkipsOutsideDirPreservesUnrelatedDiscoveryFailures(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	claudeRoot := filepath.Join(home, ".cc")
	codexFailure := AccountSkip{ConfigDir: filepath.Join(home, ".codex"), Reason: "codex discovery failed: fixture"}
	got := skipsOutsideDir([]AccountSkip{
		{ID: 4, ConfigDir: filepath.Join(claudeRoot, "4"), Reason: "no valid credentials"},
		codexFailure,
	}, claudeRoot)
	if !reflect.DeepEqual(got, []AccountSkip{codexFailure}) {
		t.Fatalf("skipsOutsideDir()=%#v, want only unrelated discovery failure", got)
	}
}

func TestLoadUsesResolvePathWhenPathIsEmpty(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	xdg := filepath.Join(root, "xdg")
	t.Setenv("XDG_CONFIG_HOME", xdg)
	path := filepath.Join(xdg, "pfm", "config.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"version":1}`), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := Load("", home, nil)
	if err != nil {
		t.Fatalf("Load(empty path) error = %v", err)
	}
	if got.Path != path || !got.Exists {
		t.Fatalf("Load(empty path) = path %q exists %v, want %q true", got.Path, got.Exists, path)
	}
}

func TestLoadRejectsUnknownKeysAtEveryConfigLevel(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	cases := []struct {
		name string
		json string
		want string
	}{
		{name: "top level", json: `{"version":1,"mystery":true}`, want: "mystery"},
		{name: "claude", json: `{"version":1,"claude":{"mystery":true}}`, want: "mystery"},
		{name: "codex", json: `{"version":1,"codex":{"mystery":true}}`, want: "mystery"},
		{
			name: "account",
			json: `{"version":1,"accounts":[{"id":1,"configDir":"/opt/fixture/cc","projectDir":"/opt/fixture/projects"}]}`,
			want: "projectDir",
		},
		{name: "mcp object", json: `{"version":1,"mcp":{"mystery":true}}`, want: "mystery"},
		{
			name: "server object",
			json: `{"version":1,"mcp":{"servers":{"chat":{"enabled":false,"mystery":true}}}}`,
			want: "mystery",
		},
		{
			name: "unregistered server",
			json: `{"version":1,"mcp":{"servers":{"not-registered":{"enabled":true}}}}`,
			want: "mcp.servers.not-registered",
		},
		{
			name: "ask engine key",
			json: `{"version":1,"ask":{"bogus":{"model":"m"}}}`,
			want: `unknown engine "bogus" (want cc/claude, cx/codex, ox/opencode)`,
		},
		{name: "ask engine prefs", json: `{"version":1,"ask":{"claude":{"mystery":true}}}`, want: "mystery"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(tc.json), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := Load(path, home, nil)
			if err == nil {
				t.Fatalf("Load(%s) succeeded, want unknown-key error", tc.json)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want it to name %q", err, tc.want)
			}
		})
	}
}

func TestLoadMalformedJSONNamesPathAndByte(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	cases := []struct {
		name    string
		content string
	}{
		{name: "syntax", content: `{"version":1`},
		{name: "wrong type", content: `{"version":"one"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "machine.json")
			if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := Load(path, home, nil)
			if err == nil {
				t.Fatal("Load malformed config succeeded")
			}
			message := err.Error()
			if !strings.Contains(message, "parse config "+path) {
				t.Errorf("error = %q, want path %q", message, path)
			}
			if !strings.Contains(message, "byte ") {
				t.Errorf("error = %q, want byte offset", message)
			}
		})
	}
}

func TestLoadRejectsInvalidAccountRoster(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	cases := []struct {
		name string
		json string
		want string
	}{
		{name: "non-positive id", json: `{"version":1,"accounts":[{"id":0,"configDir":"/tmp/cc"}]}`, want: "positive"},
		{
			name: "duplicate id",
			json: `{"version":1,"accounts":[{"id":2,"configDir":"/tmp/a"},{"id":2,"configDir":"/tmp/b"}]}`,
			want: "duplicate",
		},
		{
			name: "relative path",
			json: `{"version":1,"accounts":[{"id":1,"configDir":"relative"}]}`,
			want: "must be absolute",
		},
		{
			name: "nul path",
			json: "{\"version\":1,\"accounts\":[{\"id\":1,\"configDir\":\"/tmp/a\\u0000b\"}]}",
			want: "must not contain NUL",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(tc.json), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := Load(path, home, nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Load error = %v, want substring %q", err, tc.want)
			}
		})
	}
}

func TestLoadMCPServersHaveIndependentDefaultsAndSources(t *testing.T) {
	registered := map[string]MCPServer{
		"chat":      {Enabled: false},
		"harvester": {Enabled: false},
	}
	home := filepath.Join(t.TempDir(), "home")
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(
		path,
		[]byte(`{"version":1,"mcp":{"servers":{"harvester":{"enabled":true}}}}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	got, err := loadWithMCPServers(path, home, nil, registered)
	if err != nil {
		t.Fatalf("Load(mcp) error = %v", err)
	}
	if got.MCPServers["chat"].Enabled {
		t.Fatal("chat became enabled when only harvester was configured")
	}
	if !got.MCPServers["harvester"].Enabled {
		t.Fatal("harvester did not take its configured enabled value")
	}
	if got.Source("mcp.servers.chat.enabled") != SourceDefault {
		t.Fatalf("chat source = %q, want default", got.Source("mcp.servers.chat.enabled"))
	}
	// A pre-split file's mcp.servers.harvester is honored, but reported as
	// legacy so `pfm config show` / doctor point at the migration.
	if got.MCPServerSource("harvester") != SourceLegacy {
		t.Fatalf("harvester source = %q, want %q", got.MCPServerSource("harvester"), SourceLegacy)
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

func TestLoadRejectsMissingVersionAndWrongVersion(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	for _, tc := range []struct {
		name string
		json string
		want string
	}{
		{name: "missing", json: `{}`, want: "required key \"version\" is missing"},
		{name: "wrong", json: `{"version":99}`, want: "version must be 1 or 2, got 99"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(tc.json), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := Load(path, home, nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Load error = %v, want %q", err, tc.want)
			}
		})
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

func TestV2ConfigDefaultsAndPerAccountOverrides(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	path := filepath.Join(t.TempDir(), "config.json")
	content := `{
  "version": 2,
  "theme": "tokyo-night",
  "accounts": [
    {"id": 1, "configDir": "~/one", "emoji": "A", "claude": {"permissionMode": "prompted"}, "codex": {"yolo": false}},
    {"id": 2, "configDir": "~/two", "emoji": "B"}
  ],
  "claude": {"permissionMode": "bypass", "binary": "claude-x"},
  "codex": {"yolo": true, "binary": "codex-x"},
  "mcp": {"servers": {"chat": {"enabled": true}}, "http": {"port": 9393}},
  "ask": {"engine": "claude", "codex": {"model": "codex-model", "effort": "high"}, "claude": {"model": "claude-model", "effort": "low"}}
}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path, home, nil)
	if err != nil {
		t.Fatalf("Load(v2) error = %v", err)
	}
	if got.Theme != "tokyo-night" || got.MCP.HTTP.Port != 9393 || got.Ask.Engine != pfmengine.Claude {
		t.Fatalf("v2 fields = theme:%q mcp:%#v ask:%#v", got.Theme, got.MCP, got.Ask)
	}
	if got.EffectiveClaude(1).PermissionMode != "prompted" || got.EffectiveClaude(2).PermissionMode != "bypass" {
		t.Fatalf("effective Claude preferences = %#v / %#v", got.EffectiveClaude(1), got.EffectiveClaude(2))
	}
	if got.EffectiveCodex(1).Yolo || !got.EffectiveCodex(2).Yolo {
		t.Fatalf("effective Codex preferences = %#v / %#v", got.EffectiveCodex(1), got.EffectiveCodex(2))
	}
	if got.EmojiFor(1) != "A" || got.EmojiFor(2) != "B" || got.EmojiFor(999) != "·" {
		t.Fatalf("emoji lookup = %q %q %q", got.EmojiFor(1), got.EmojiFor(2), got.EmojiFor(999))
	}
}

func TestV1ConfigStillLoadsWithV2Defaults(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(
		path,
		[]byte(`{"version":1,"accounts":[{"id":4,"configDir":"~/four"}]}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path, home, nil)
	if err != nil {
		t.Fatalf("Load(v1) error = %v", err)
	}
	if got.Version != Version || got.Theme != "default" || got.MCP.HTTP.Port != DefaultMCPPort ||
		got.Ask.Engine != pfmengine.Codex {
		t.Fatalf(
			"v1 defaults = version:%d theme:%q port:%d engine:%q",
			got.Version,
			got.Theme,
			got.MCP.HTTP.Port,
			got.Ask.Engine,
		)
	}
	if got.EmojiFor(4) != "🍀" {
		t.Fatalf("v1 account 4 emoji = %q, want 🍀", got.EmojiFor(4))
	}
}

func TestLoadCache1HDefaultsTrueWhenAbsentFromFile(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	path := filepath.Join(t.TempDir(), "config.json")
	content := `{"version":2,"accounts":[{"id":1,"configDir":"~/one"}]}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := Load(path, home, nil)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !got.Claude.Cache1H {
		t.Fatalf("Claude.Cache1H = %v, want true when cache1h is absent everywhere", got.Claude.Cache1H)
	}
	if got.Source("claude.cache1h") != SourceDefault {
		t.Fatalf("Source(claude.cache1h) = %q, want %q", got.Source("claude.cache1h"), SourceDefault)
	}
	if !got.EffectiveClaude(1).Cache1H {
		t.Fatalf(
			"EffectiveClaude(1).Cache1H = %v, want true (account has no claude block at all)",
			got.EffectiveClaude(1).Cache1H,
		)
	}
}

func TestLoadCache1HExplicitFalseIsHonoredWithSource(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	path := filepath.Join(t.TempDir(), "config.json")
	content := `{"version":2,"claude":{"cache1h":false}}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := Load(path, home, nil)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.Claude.Cache1H {
		t.Fatal("Claude.Cache1H = true, want false: explicit top-level cache1h:false was not honored")
	}
	if got.Source("claude.cache1h") != SourceFile {
		t.Fatalf("Source(claude.cache1h) = %q, want %q", got.Source("claude.cache1h"), SourceFile)
	}
}

func TestLoadCache1HPerAccountOverridesTopLevel(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	path := filepath.Join(t.TempDir(), "config.json")
	content := `{
  "version": 2,
  "accounts": [{"id": 3, "configDir": "~/three", "claude": {"cache1h": false}}],
  "claude": {"cache1h": true}
}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := Load(path, home, nil)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !got.Claude.Cache1H {
		t.Fatal("top-level Claude.Cache1H changed by a per-account override; it must stay true")
	}
	if got.EffectiveClaude(3).Cache1H {
		t.Fatal("EffectiveClaude(3).Cache1H = true, want false: account-level cache1h:false was not applied")
	}
	if got.Source("accounts[0].claude.cache1h") != SourceFile {
		t.Fatalf(
			"Source(accounts[0].claude.cache1h) = %q, want %q",
			got.Source("accounts[0].claude.cache1h"),
			SourceFile,
		)
	}
}

func TestLoadCache1HPerAccountInheritsResolvedTopLevelWhenUnset(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	path := filepath.Join(t.TempDir(), "config.json")
	// The account's claude block sets ONLY binary; cache1h is absent there.
	// EffectiveClaude must inherit the resolved top-level true, not the
	// ClaudePrefs zero value (false), which is the false-zero trap this test
	// pins.
	content := `{
  "version": 2,
  "accounts": [{"id": 5, "configDir": "~/five", "claude": {"binary": "claude-five"}}],
  "claude": {"cache1h": true}
}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := Load(path, home, nil)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !got.EffectiveClaude(5).Cache1H {
		t.Fatal(
			"EffectiveClaude(5).Cache1H = false, want true: an account claude block touching only binary must inherit the resolved top-level cache1h, not the bool zero value",
		)
	}
	if got.Source("accounts[0].claude.cache1h") != SourceDefault {
		t.Fatalf(
			"Source(accounts[0].claude.cache1h) = %q, want %q (no account-level key was set)",
			got.Source("accounts[0].claude.cache1h"),
			SourceDefault,
		)
	}
}

func TestConfigInitJSONRoundTripsAndRedactsSecrets(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	path := filepath.Join(t.TempDir(), "config.json")
	content, err := MarshalDefault(home, nil)
	if err != nil {
		t.Fatalf("MarshalDefault() error = %v", err)
	}
	if strings.Contains(string(content), "//") {
		t.Fatalf("default config contains comments: %s", content)
	}
	if !strings.Contains(string(content), "\"level\": \"info\"") {
		t.Fatalf("default config carries no log.level default: %s", content)
	}
	if _, err := Load(path, home, nil); err != nil {
		// The strict loader check below needs the bytes on disk; this assertion
		// intentionally documents that MarshalDefault itself does not install.
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("Load(absent) error = %v", err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path, home, nil); err != nil {
		t.Fatalf("strict loader rejected init output: %v", err)
	}
	secretContent := []byte(`{"version":2,"mcp":{"authToken":"neutral-secret"}}`)
	if !strings.Contains(string(RedactSecrets(secretContent)), "<redacted>") {
		t.Fatal("RedactSecrets did not redact a secret-looking field")
	}
}

func TestRemoveLegacyMCPAuthTokenPreservesUnrelatedConfig(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".config", "pfm", "config.json")
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
