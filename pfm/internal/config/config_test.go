package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestConfigLoadsWithoutCodexLogin(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "pfm.config.json")
	if err := os.WriteFile(
		configPath,
		[]byte(`{"version":2,"codex":{"homes":[{"id":2,"home":"`+filepath.Join(home, "codex")+`"}]}}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(configPath, home, nil)
	if err != nil {
		t.Fatalf("Load() refused a configured Codex home without login: %v", err)
	}
	if len(loaded.CodexAccounts) != 1 || loaded.CodexAccounts[0].ID != 2 {
		t.Fatalf("Codex accounts = %#v, want id 2", loaded.CodexAccounts)
	}
}

func TestResolvePathUsesOverrideAndCloneMarker(t *testing.T) {
	home := t.TempDir()
	override := filepath.Join(t.TempDir(), "custom.json")
	t.Setenv("PFM_CONFIG", override)
	if got, err := ResolvePath(home); err != nil || got != override {
		t.Fatalf("override path = %q, %v; want %q", got, err, override)
	}
	t.Setenv("PFM_CONFIG", "relative.json")
	if _, err := ResolvePath(home); err == nil || !strings.Contains(err.Error(), "PFM_CONFIG") {
		t.Fatalf("relative override error = %v, want PFM_CONFIG", err)
	}
	t.Setenv("PFM_CONFIG", "")
	repo := t.TempDir()
	if err := paths.WriteSourceRepoMarker(home, repo); err != nil {
		t.Fatal(err)
	}
	if got, err := ResolvePath(home); err != nil || got != filepath.Join(repo, FileName) {
		t.Fatalf("clone path = %q, %v", got, err)
	}
}

func TestLoadWithoutMarkerIgnoresLegacyConfigAndHasNoWriterPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv(paths.EnvConfig, "")
	legacy := filepath.Join(home, ".config", "pfm", FileName)
	if err := os.MkdirAll(filepath.Dir(legacy), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte(`{"version":2,"theme":"tokyo-night"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load("", home, nil)
	if err != nil || loaded.Path != "" || loaded.Exists || loaded.Theme == "tokyo-night" {
		t.Fatalf(
			"legacy-only Load = path %q exists %t theme %q error %v",
			loaded.Path,
			loaded.Exists,
			loaded.Theme,
			err,
		)
	}
	if err := WriteDefault(
		"",
		home,
		nil,
		false,
	); err == nil ||
		!strings.Contains(err.Error(), "no source repo recorded") {
		t.Fatalf("WriteDefault without marker = %v", err)
	}
}

func TestUnusableMarkerIsALoadError(t *testing.T) {
	home := t.TempDir()
	t.Setenv(paths.EnvConfig, "")
	marker := paths.SourceRepoPath(home)
	if err := os.MkdirAll(filepath.Dir(marker), 0o700); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(home, "missing-clone")
	if err := os.WriteFile(marker, []byte(missing+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load("", home, nil)
	got := strings.ReplaceAll(fmt.Sprint(err), home, "{home}")
	if want := "no config path: inspect recorded source repository {home}/missing-clone: recorded source repository is unusable: stat {home}/missing-clone: no such file or directory — run pfm install from the clone, or set PFM_CONFIG"; loaded.Path != "" ||
		loaded.Exists ||
		!errors.Is(err, paths.ErrSourceRepoUnusable) ||
		got != want {
		t.Fatalf("unusable marker Load = %q, want %q (path %q exists %t)", got, want, loaded.Path, loaded.Exists)
	}
	written := strings.ReplaceAll(fmt.Sprint(WriteDefault("", home, nil, false)), home, "{home}")
	if want := "no config path: inspect recorded source repository {home}/missing-clone: recorded source repository is unusable: stat {home}/missing-clone: no such file or directory — run pfm install from the clone, or set PFM_CONFIG"; written != want {
		t.Fatalf("unusable marker writer error = %q, want %q", written, want)
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
			ID:        1,
			ConfigDir: filepath.Join(home, ".cc", "one"),

			Emoji: "🥇",
		},
		{
			ID:        2,
			ConfigDir: filepath.Join(home, ".cc", "two"),
			Emoji:     "🥈",
		},
	}
	if !reflect.DeepEqual(got.Accounts, wantAccounts) {
		t.Fatalf("Accounts = %#v, want %#v", got.Accounts, wantAccounts)
	}
	if !reflect.DeepEqual(got.Claude, Claude{
		Plugins:               DefaultClaudePlugins(),
		PermissionMode:        PermissionBypass,
		Binary:                "claude",
		WebSearchesPerSession: 9007199254740991,
		AutoCompactWindow:     100000,
		TmuxTruecolor:         true,
		CleanupPeriodDays:     36500,
		RequireManagedCleanup: true,
		HelperModel:           DefaultHelperModel,
		Cache1H:               true,
		RemoteControl:         true,
		CostLedger:            true,
		MaxSubagentSpawnDepth: DefaultSubagentSpawnDepth,
	}) {
		t.Fatalf("Claude = %#v, want bypass/claude defaults", got.Claude)
	}
	if got.Codex != (Codex{Yolo: true, Binary: "codex"}) {
		t.Fatalf("Codex = %#v, want yolo/codex defaults", got.Codex)
	}
	if !reflect.DeepEqual(got.MCPServers, map[string]MCPServer{
		"chat": {Enabled: false},
	}) {
		t.Fatalf("MCPServers = %#v, want chat disabled", got.MCPServers)
	}
	for _, key := range []string{
		"version", "accounts", "claude.permissionMode", "claude.binary", "claude.cache1h", "codex.yolo", "codex.binary", "mcp.servers.chat.enabled",
	} {
		if got.Source(key) != SourceDefault {
			t.Errorf("Source(%q) = %q, want %q", key, got.Source(key), SourceDefault)
		}
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
	for _, account := range []int{1, 2, 3, 4, 5} {
		configDir := filepath.Join(home, ".cc", strconv.Itoa(account))
		if err := os.MkdirAll(configDir, 0o700); err != nil {
			t.Fatal(err)
		}
		if account == 1 || account == 4 {
			continue
		}
		credentials := `{"claudeAiOauth":{"accessToken":"fixture","refreshToken":"fixture"}}`
		if account == 5 { // torn mid-rewrite: the account stays, its token unknown
			credentials = `{"claudeAiOauth":{"accessTo`
		}
		if err := os.WriteFile(filepath.Join(configDir, ".credentials.json"), []byte(credentials), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got := Defaults(home, nil)
	want := []Account{
		{
			ID:        1,
			ConfigDir: filepath.Join(home, ".cc", "1"),

			Emoji: "🥇",
		},
		{
			ID:        2,
			ConfigDir: filepath.Join(home, ".cc", "2"),
			Emoji:     "🥈",
		},
		{
			ID:        3,
			ConfigDir: filepath.Join(home, ".cc", "3"),
			Emoji:     "🥉",
		},
		{
			ID:        5,
			ConfigDir: filepath.Join(home, ".cc", "5"),
			Emoji:     DefaultEmoji(5),
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
			ID:        9,
			ConfigDir: filepath.Join(home, "account-nine"),
			Emoji:     "·",
		},
		{
			ID:        2,
			ConfigDir: filepath.Join(home, "account-two"),
			Emoji:     "🥈",
		},
		{
			ID:        17,
			ConfigDir: "/srv/claude/account-seventeen",
			Emoji:     "·",
		},
	}
	if !reflect.DeepEqual(got.Accounts, want) {
		t.Fatalf("Accounts = %#v, want %#v", got.Accounts, want)
	}
	if got.AccountIDs() == nil || !reflect.DeepEqual(got.AccountIDs(), []int{9, 2, 17}) {
		t.Fatalf("AccountIDs = %#v, want [9 2 17]", got.AccountIDs())
	}
	if !reflect.DeepEqual(got.Claude, Claude{
		Plugins:               DefaultClaudePlugins(),
		PermissionMode:        PermissionPrompt,
		Binary:                "claude-custom",
		WebSearchesPerSession: 9007199254740991,
		AutoCompactWindow:     100000,
		TmuxTruecolor:         true,
		CleanupPeriodDays:     36500,
		RequireManagedCleanup: true,
		HelperModel:           DefaultHelperModel,
		Cache1H:               true,
		RemoteControl:         true,
		CostLedger:            true,
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
	repo := filepath.Join(root, "clone")
	if err := os.MkdirAll(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := paths.WriteSourceRepoMarker(home, repo); err != nil {
		t.Fatal(err)
	}
	t.Setenv(paths.EnvConfig, "")
	path := filepath.Join(repo, FileName)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"version":2}`), 0o600); err != nil {
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

func TestLoadRefusesAnUnusableConfig(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	for _, tc := range []struct{ name, json, want string }{
		{"unknown top level", `{"version":1,"mystery":true}`, "parse config {path}: json: unknown field \"mystery\""},
		{"unknown claude key", `{"version":1,"claude":{"mystery":true}}`, "parse config {path}: json: unknown field \"mystery\""},
		{"unknown codex key", `{"version":1,"codex":{"mystery":true}}`, "parse config {path}: json: unknown field \"mystery\""},
		{"unknown account key", `{"version":1,"accounts":[{"id":1,"configDir":"/opt/fixture/cc","projectDir":"/opt/fixture/projects"}]}`, "parse config {path}: json: unknown field \"projectDir\""},
		{"unknown mcp key", `{"version":1,"mcp":{"mystery":true}}`, "parse config {path}: json: unknown field \"mystery\""},
		{"unknown server key", `{"version":1,"mcp":{"servers":{"chat":{"enabled":false,"mystery":true}}}}`, "config {path}: mcp.servers.chat: json: unknown field \"mystery\""},
		{"unknown ask engine", `{"version":1,"ask":{"bogus":{"model":"m"}}}`, "parse config {path}: ask.bogus: unknown engine \"bogus\" (want cc/claude, cx/codex, ox/opencode)"},
		{"unknown ask engine prefs key", `{"version":1,"ask":{"claude":{"mystery":true}}}`, "parse config {path}: ask.claude: json: unknown field \"mystery\""},
		{"syntax", `{"version":1`, "parse config {path} at byte 12: unexpected EOF"},
		{"wrong type", `{"version":"one"}`, "parse config {path} at byte 16: json: cannot unmarshal string into Go struct field rawConfig.version of type int"},
		{"non-positive account id", `{"version":1,"accounts":[{"id":0,"configDir":"/tmp/cc"}]}`, "config {path}: accounts: entry 1 id must be positive"},
		{"duplicate account id", `{"version":1,"accounts":[{"id":2,"configDir":"/tmp/a"},{"id":2,"configDir":"/tmp/b"}]}`, "config {path}: accounts: duplicate id 2"},
		{"relative account path", `{"version":1,"accounts":[{"id":1,"configDir":"relative"}]}`, "config {path}: accounts: entry 1 configDir: must be absolute or start with ~/ or $HOME/, got \"relative\""},
		{"nul account path", "{\"version\":1,\"accounts\":[{\"id\":1,\"configDir\":\"/tmp/a\\u0000b\"}]}", "config {path}: accounts: entry 1 configDir: must not contain NUL"},
		{"state db number", `{"version":2,"state":{"db":12}}`, "parse config {path} at byte 29: json: cannot unmarshal number into Go struct field rawConfig.state.db of type string"},
		{"state db empty", `{"version":2,"state":{"db":""}}`, "config {path}: state.db must be non-empty"},
		{"state db relative", `{"version":2,"state":{"db":"relative.db"}}`, "config {path}: state.db: must be absolute or start with ~/ or $HOME/, got \"relative.db\""},
		{"remoteControl string", `{"version":2,"claude":{"remoteControl":"on"}}`, "parse config {path} at byte 43: json: cannot unmarshal string into Go struct field rawConfig.claude.remoteControl of type bool"},
		{"account remoteControl number", `{"version":2,"accounts":[{"id":1,"configDir":"~/one","claude":{"remoteControl":1}}]}`, "parse config {path} at byte 80: json: cannot unmarshal number into Go struct field rawConfig.accounts.0.claude.remoteControl of type bool"},
		{"zero spawn depth", `{"version": 2, "claude": {"maxSubagentSpawnDepth": 0}}`, "config {path}: claude.maxSubagentSpawnDepth must be a positive integer, got 0"},
		{"negative spawn depth", `{"version": 2, "claude": {"maxSubagentSpawnDepth": -3}}`, "config {path}: claude.maxSubagentSpawnDepth must be a positive integer, got -3"},
		{"zero concurrency", `{"version": 2, "claude": {"maxConcurrentSubagents": 0}}`, "config {path}: claude.maxConcurrentSubagents must be a positive integer, got 0"},
		{"string spawn depth", `{"version": 2, "claude": {"maxSubagentSpawnDepth": "5"}}`, "parse config {path} at byte 54: json: cannot unmarshal string into Go struct field rawConfig.claude.maxSubagentSpawnDepth of type int"},
		{"fractional spawn depth", `{"version": 2, "claude": {"maxSubagentSpawnDepth": 5.5}}`, "parse config {path} at byte 54: json: cannot unmarshal number 5.5 into Go struct field rawConfig.claude.maxSubagentSpawnDepth of type int"},
		{"negative account concurrency", `{"version": 2, "accounts": [{"id": 1, "configDir": "~/one", "claude": {"maxConcurrentSubagents": -1}}]}`, "config {path}: accounts[0].maxConcurrentSubagents must be a positive integer, got -1"},
		{"empty theme", `{"version": 2, "claude": {"theme": ""}}`, "config {path}: claude.theme must be a non-empty string"},
		{"blank theme", `{"version": 2, "claude": {"theme": "   "}}`, "config {path}: claude.theme must be a non-empty string"},
		{"blank account theme", `{"version": 2, "accounts": [{"id": 3, "configDir": "~/three", "claude": {"theme": "  "}}]}`, "config {path}: accounts[0].theme must be a non-empty string"},
		{"unknown herdrTabStatus", `{"version":2,"chat":{"herdrTabStatus":"invalid"}}`, "config {path}: chat.herdrTabStatus must be emoji, text or off"},
		{"unknown systemPrompt", `{"version":1,"claude":{"systemPrompt":"vibes"}}`, "config {path}: claude.systemPrompt must be \"production\", \"lean\" or \"professor\", got \"vibes\""},
		{"log: {level: chatty}", `{"version": 2, "log": {"level": "chatty"}}`, "config {path}: log.level must be one of debug, info, warn, warning, error, off, got \"chatty\""},
		{"log: {components: {mcp: loud}}", `{"version": 2, "log": {"components": {"mcp": "loud"}}}`, "config {path}: log.components.mcp must be one of debug, info, warn, warning, error, off, got \"loud\""},
		{"log: {keepDays: -1}", `{"version": 2, "log": {"keepDays": -1}}`, "config {path}: log.keepDays must be 0..3650 (retired, no effect), got -1"},
		{"log: {keepDays: 4000}", `{"version": 2, "log": {"keepDays": 4000}}`, "config {path}: log.keepDays must be 0..3650 (retired, no effect), got 4000"},
		{"log: {keepFiles: 0}", `{"version": 2, "log": {"keepFiles": 0}}`, "config {path}: log.keepFiles must be 1..64 (segments kept whole per pfm home), got 0"},
		{"log: {keepFiles: 65}", `{"version": 2, "log": {"keepFiles": 65}}`, "config {path}: log.keepFiles must be 1..64 (segments kept whole per pfm home), got 65"},
		{"log: {maxMB: 0}", `{"version": 2, "log": {"maxMB": 0}}`, "config {path}: log.maxMB must be 1..1024 (megabytes per segment), got 0"},
		{"log: {maxMB: 4096}", `{"version": 2, "log": {"maxMB": 4096}}`, "config {path}: log.maxMB must be 1..1024 (megabytes per segment), got 4096"},
		{"log: {keep_files: 2}", `{"version": 2, "log": {"keep_files": 2}}`, "parse config {path}: json: unknown field \"keep_files\""},
		{"nameSync: {interval: 30s}", `{"version": 2, "nameSync": {"interval": "30s"}}`, "config {path}: nameSync.interval must be at least 1m0s, got 30s"},
		{"nameSync: {interval: 0s}", `{"version": 2, "nameSync": {"interval": "0s"}}`, "config {path}: nameSync.interval must be at least 1m0s, got 0s"},
		{"nameSync: {interval: -5m}", `{"version": 2, "nameSync": {"interval": "-5m"}}`, "config {path}: nameSync.interval must be at least 1m0s, got -5m0s"},
		{"nameSync: {interval: 15}", `{"version": 2, "nameSync": {"interval": "15"}}`, "config {path}: nameSync.interval must be a Go duration such as \"15m0s\", got \"15\""},
		{"nameSync: {interval: quarter hour}", `{"version": 2, "nameSync": {"interval": "quarter hour"}}`, "config {path}: nameSync.interval must be a Go duration such as \"15m0s\", got \"quarter hour\""},
	} {
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, []byte(tc.json), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := Load(path, home, nil)
		if got := strings.ReplaceAll(fmt.Sprint(err), path, "{path}"); got != tc.want {
			t.Errorf("%s: Load error = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestMarshalKeepsWhatLoadRead compares the named top-level keys of the re-marshalled config.
func TestMarshalKeepsWhatLoadRead(t *testing.T) {
	for _, tc := range []struct{ name, json, keys, want string }{
		{"retired herdrTabPriority dropped", `{"version":2,"chat":{"herdrTabPriority":true}}`, "chat", "{\"chat\":null}"},
		{"herdrTabStatus emoji", `{"version":2,"chat":{"herdrTabStatus":"emoji"}}`, "chat", "{\"chat\":{\"herdrTabStatus\":\"emoji\"}}"},
		{"herdrTabStatus text", `{"version":2,"chat":{"herdrTabStatus":"text"}}`, "chat", "{\"chat\":{\"herdrTabStatus\":\"text\"}}"},
		{"herdrTabStatus off", `{"version":2,"chat":{"herdrTabStatus":"off"}}`, "chat", "{\"chat\":{\"herdrTabStatus\":\"off\"}}"},
		{"nameSync interval", `{"version": 2, "nameSync": {"interval": "5m"}}`, "nameSync", "{\"nameSync\":{\"interval\":\"5m0s\"}}"},
		{"tmux titles", `{"version": 2, "tmux": {"titles": {"enabled": false}}}`, "tmux", "{\"tmux\":{\"titles\":{\"enabled\":false}}}"},
		{
			"claude theme top and per account",
			`{"version": 2, "accounts": [{"id": 3, "configDir": "~/three", "claude": {"theme": "light"}}], "claude": {"theme": "dark"}}`,
			"claude.theme accounts.0.claude.theme", "{\"accounts.0.claude.theme\":\"light\",\"claude.theme\":\"dark\"}",
		},
	} {
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, []byte(tc.json), 0o600); err != nil {
			t.Fatal(err)
		}
		loaded, err := Load(path, filepath.Join(t.TempDir(), "home"), nil)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		body, err := Marshal(loaded, false)
		if err != nil {
			t.Fatal(err)
		}
		var all any
		if err := json.Unmarshal(body, &all); err != nil {
			t.Fatal(err)
		}
		picked := map[string]any{}
		for _, key := range strings.Fields(tc.keys) {
			node := all
			for _, part := range strings.Split(key, ".") {
				switch typed := node.(type) {
				case map[string]any:
					node = typed[part]
				case []any:
					index, err := strconv.Atoi(part)
					if err != nil || index >= len(typed) {
						t.Fatalf("%s: no %s in %s", tc.name, key, body)
					}
					node = typed[index]
				}
			}
			picked[key] = node
		}
		got, err := json.Marshal(picked)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != tc.want {
			t.Errorf("%s: marshalled = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestLoadAcceptsRetiredKeysWithoutChangingOtherPreferences(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(t.TempDir(), "pfm.config.json")
	base := `{"version":2,"claude":{"permissionMode":"prompt","cache1h":false},` +
		`"accounts":[{"id":1,"configDir":"` + filepath.Join(home, "account") +
		`","claude":{"theme":"ocean"}}]}`
	if err := os.WriteFile(path, []byte(base), 0o600); err != nil {
		t.Fatal(err)
	}
	want, err := Load(path, home, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"compactNudge", "subagentStatuslineTrace"} {
		for _, value := range []string{`{"enabled":false,"start":0,"step":101}`, `false`, `true`, `null`, `[1,"old"]`} {
			retired := `{"version":2,"claude":{"permissionMode":"prompt","cache1h":false,"` + key + `":` + value + `},` +
				`"accounts":[{"id":1,"configDir":"` + filepath.Join(
				home,
				"account",
			) +
				`","claude":{"theme":"ocean","` + key + `":` + value + `}}]}`
			if err := os.WriteFile(path, []byte(retired), 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := Load(path, home, nil)
			if err != nil {
				t.Fatalf("retired %s value %s: %v", key, value, err)
			}
			if !reflect.DeepEqual(got.Claude, want.Claude) || !reflect.DeepEqual(got.Accounts, want.Accounts) ||
				!reflect.DeepEqual(got.Sources, want.Sources) {
				t.Fatalf("retired %s value %s changed effective preferences or sources", key, value)
			}
			encoded, err := Marshal(got, false)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(encoded), key) {
				t.Fatalf("serializer wrote retired key %s: %s", key, encoded)
			}
		}
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
	if !strings.Contains(string(content), "\"autoCompactWindow\": 100000") {
		t.Fatalf("default config carries no auto compact window: %s", content)
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

func TestDefaultsKeepPrimaryAccountWithoutCredentials(t *testing.T) {
	for _, roots := range []string{"empty", "default"} {
		for _, configured := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/configured=%t", roots, configured), func(t *testing.T) {
				home := filepath.Join(t.TempDir(), "home")
				var projects []string
				if roots == "default" {
					projects = []string{filepath.Join(home, ".claude", "projects")}
				}
				path := filepath.Join(t.TempDir(), "config.json")
				if configured {
					if err := os.WriteFile(path, []byte(`{"version":2}`), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				got, err := Load(path, home, projects)
				if err != nil {
					t.Fatal(err)
				}
				want := []Account{{ID: 1, ConfigDir: DefaultAccountDir(home, 1), Emoji: DefaultEmoji(1)}}
				if !reflect.DeepEqual(got.Accounts, want) || len(got.AccountSkips) != 0 {
					t.Fatalf("accounts=%#v skips=%#v, want %#v and no skips", got.Accounts, got.AccountSkips, want)
				}
			})
		}
	}
}

func TestDefaultsDiscoversSeatsWithDefaultRoots(t *testing.T) {
	home := t.TempDir()
	for _, id := range []int{1, 2, 3} {
		dir := DefaultAccountDir(home, id)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if id == 2 {
			if err := os.WriteFile(
				filepath.Join(dir, ".credentials.json"),
				[]byte(`{"claudeAiOauth":{"accessToken":"fixture"}}`),
				0o600,
			); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, roots := range [][]string{nil, {filepath.Join(home, ".claude", "projects")}} {
		got := Defaults(home, roots)
		want := []Account{
			{ID: 1, ConfigDir: DefaultAccountDir(home, 1), Emoji: DefaultEmoji(1)},
			{ID: 2, ConfigDir: DefaultAccountDir(home, 2), Emoji: DefaultEmoji(2)},
		}
		skips := []AccountSkip{{ID: 3, ConfigDir: DefaultAccountDir(home, 3), Reason: "no valid credentials"}}
		if !reflect.DeepEqual(got.Accounts, want) || !reflect.DeepEqual(got.AccountSkips, skips) {
			t.Fatalf(
				"roots=%v accounts=%#v skips=%#v, want %#v/%#v",
				roots,
				got.Accounts,
				got.AccountSkips,
				want,
				skips,
			)
		}
	}
}

func TestLoadRejectsDuplicateAccountDirs(t *testing.T) {
	home := t.TempDir()
	dir := DefaultAccountDir(home, 2)
	for _, tc := range []struct{ name, json, want string }{
		{"duplicate", fmt.Sprintf(`{"version":2,"accounts":[{"id":2,"configDir":%q},{"id":7,"configDir":%q}]}`, dir, dir+"/projects/.."), fmt.Sprintf("entry 2 configDir %s duplicates entry 1", dir)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(tc.json), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := Load(path, home, nil)
			want := "config " + path + ": accounts: " + tc.want
			if err == nil || err.Error() != want {
				t.Fatalf("Load error=%v, want %q", err, want)
			}
		})
	}
}

func TestLoadThirdParty(t *testing.T) {
	for _, tc := range []struct {
		name, mcp, wantError string
		want                 map[string]json.RawMessage
		file                 bool
	}{
		{"absent", `{}`, "", nil, false},
		{"empty", `{"thirdParty":{}}`, "", map[string]json.RawMessage{}, true},
		{"entries", `{"thirdParty":{"browser":{"type":"stdio","command":"x","args":["--flag"],"env":{"EXAMPLE":"value"},"custom":{"future":true}},"remote":{"type":"http","url":"https://example.invalid/mcp","headers":{"Authorization":"invented-token"}}}}`, "", map[string]json.RawMessage{
			"browser": json.RawMessage(`{"type":"stdio","command":"x","args":["--flag"],"env":{"EXAMPLE":"value"},"custom":{"future":true}}`),
			"remote":  json.RawMessage(`{"type":"http","url":"https://example.invalid/mcp","headers":{"Authorization":"invented-token"}}`),
		}, true},
		{"chat registration", `{"thirdParty":{"chat-professor":{"type":"stdio","command":"x"}}}`, "mcp.thirdParty.chat-professor: the name chat-professor is pfm's own server", nil, false},
		{"string", `{"thirdParty":{"x":"y"}}`, "mcp.thirdParty.x must be a JSON object (a Claude mcpServers entry)", nil, false},
		{"null", `{"thirdParty":{"x":null}}`, "mcp.thirdParty.x must be a JSON object (a Claude mcpServers entry)", nil, false},
		{"array", `{"thirdParty":{"x":[]}}`, "mcp.thirdParty.x must be a JSON object (a Claude mcpServers entry)", nil, false},
		{"number", `{"thirdParty":{"x":1}}`, "mcp.thirdParty.x must be a JSON object (a Claude mcpServers entry)", nil, false},
		{"boolean", `{"thirdParty":{"x":false}}`, "mcp.thirdParty.x must be a JSON object (a Claude mcpServers entry)", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			path := filepath.Join(home, FileName)
			if err := os.WriteFile(path, []byte(`{"version":2,"mcp":`+tc.mcp+`}`), 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := Load(path, home, nil)
			if tc.wantError != "" {
				want := "config " + path + ": " + tc.wantError
				if err == nil || err.Error() != want {
					t.Fatalf("Load error = %v, want %q", err, want)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.MCP.ThirdParty, tc.want) {
				t.Fatalf("thirdParty = %s, want %s", got.MCP.ThirdParty, tc.want)
			}
			wantSource := SourceDefault
			if tc.file {
				wantSource = SourceFile
			}
			if got.Source("mcp.thirdParty") != wantSource {
				t.Fatalf("source = %s, want %s", got.Source("mcp.thirdParty"), wantSource)
			}
		})
	}
}

func TestMarshalThirdParty(t *testing.T) {
	home := t.TempDir()
	machine := Defaults(home, nil)
	machine.MCP.ThirdParty = map[string]json.RawMessage{
		"browser": json.RawMessage(
			`{"type":"stdio","command":"x","args":["--flag"],"env":{"API_TOKEN":"invented-secret","PLAIN":"value"},"custom":{"future":true}}`,
		),
		"remote": json.RawMessage(
			`{"type":"http","url":"https://example.invalid/mcp","headers":{"X-Example":"value"}}`,
		),
	}
	for _, redact := range []bool{false, true} {
		t.Run(strconv.FormatBool(redact), func(t *testing.T) {
			content, err := Marshal(machine, redact)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), FileName)
			if err := os.WriteFile(path, content, 0o600); err != nil {
				t.Fatal(err)
			}
			loaded, err := Load(path, home, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(loaded.MCP.ThirdParty) != len(machine.MCP.ThirdParty) {
				t.Fatalf("round trip entries = %s", loaded.MCP.ThirdParty)
			}
			for name, raw := range machine.MCP.ThirdParty {
				var want, got any
				if redact {
					raw = RedactSecrets(raw)
				}
				if err := json.Unmarshal(raw, &want); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(loaded.MCP.ThirdParty[name], &got); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("round trip %s = %#v, want %#v", name, got, want)
				}
			}
		})
	}
}
