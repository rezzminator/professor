package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestClaudeLaunchPreferencesMergeAndValidateByScope(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(t.TempDir(), FileName)
	content := `{"version":2,"claude":{"webSearchesPerSession":7,"autoCompactWindow":50000,"helperModel":"claude-haiku-4-5","tmuxTruecolor":false,"cleanupPeriodDays":30,"requireManagedCleanup":false},"accounts":[{"id":1,"configDir":"~/one","claude":{"webSearchesPerSession":9,"autoCompactWindow":250000,"helperModel":"claude-sonnet-5-5","tmuxTruecolor":true,"cleanupPeriodDays":14,"requireManagedCleanup":true}},{"id":2,"configDir":"~/two"},{"id":3,"configDir":"~/three","claude":{"helperModel":"claude-haiku-4-5"}}]}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path, home, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		id        int
		web       int64
		window    int64
		helper    string
		truecolor bool
		cleanup   int
		managed   bool
	}{
		{1, 9, 250000, "claude-sonnet-5-5", true, 14, true},
		{2, 7, 50000, "claude-haiku-4-5", false, 30, false},
		{3, 7, 50000, "claude-haiku-4-5", false, 30, false},
	} {
		prefs := loaded.EffectiveClaude(tc.id)
		if prefs.WebSearchesPerSession != tc.web || prefs.AutoCompactWindow != tc.window ||
			prefs.HelperModel != tc.helper || prefs.TmuxTruecolor != tc.truecolor ||
			prefs.CleanupPeriodDays != tc.cleanup || prefs.RequireManagedCleanup != tc.managed {
			t.Fatalf("account %d launch preferences = %+v", tc.id, prefs)
		}
	}
	if loaded.Source("claude.autoCompactWindow") != SourceFile ||
		loaded.Source("accounts[0].claude.autoCompactWindow") != SourceFile {
		t.Fatalf("auto compact window sources = %#v", loaded.Sources)
	}
	if loaded.Source("claude.helperModel") != SourceFile ||
		loaded.Source("accounts[0].claude.helperModel") != SourceFile {
		t.Fatalf("helper model sources = %#v", loaded.Sources)
	}
	encoded, err := Marshal(loaded, false)
	if err != nil || strings.Count(string(encoded), `"autoCompactWindow"`) != 3 ||
		strings.Count(string(encoded), `"helperModel"`) != 3 {
		t.Fatalf("marshal auto compact window and helper model: err=%v config=%s", err, encoded)
	}
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	reloaded, err := Load(path, home, nil)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Source("accounts[2].claude.helperModel") != SourceFile {
		t.Fatalf("explicit equal account helper model lost file provenance: %#v", reloaded.Sources)
	}
	// A later machine preference change must not retarget an explicit account preference.
	reloaded.Claude.HelperModel = "claude-sonnet-5-5"
	reloaded.Claude.PermissionMode = PermissionPrompt
	reloaded.Claude.Binary = "claude-new"
	if got := reloaded.EffectiveClaude(3); got.PermissionMode != PermissionPrompt || got.Binary != "claude-new" {
		t.Fatalf("round trip materialized inherited account posture: %+v", got)
	}
	if got := reloaded.EffectiveClaude(3).HelperModel; got != "claude-haiku-4-5" {
		t.Fatalf("machine edit changed explicit account helper model to %q", got)
	}
	for _, tc := range []struct{ content, want string }{
		{`{"version":2,"claude":{"webSearchesPerSession":0}}`, "claude.webSearchesPerSession"},
		{`{"version":2,"claude":{"autoCompactWindow":0}}`, "claude.autoCompactWindow must be at least 1"},
		{`{"version":2,"claude":{"cleanupPeriodDays":0}}`, "claude.cleanupPeriodDays"},
		{`{"version":2,"claude":{"helperModel":""}}`, "claude.helperModel must be a non-empty model name without whitespace"},
		{`{"version":2,"claude":{"helperModel":"  "}}`, "claude.helperModel must be a non-empty model name without whitespace"},
		{`{"version":2,"claude":{"helperModel":"claude haiku"}}`, "claude.helperModel must be a non-empty model name without whitespace"},
		{`{"version":2,"claude":{"helperModel":5}}`, "helperModel"},
		{`{"version":2,"accounts":[{"id":1,"configDir":"~/one","claude":{"webSearchesPerSession":0}}]}`, "accounts[0].webSearchesPerSession"},
		{`{"version":2,"accounts":[{"id":1,"configDir":"~/one","claude":{"autoCompactWindow":0}}]}`, "accounts[0].autoCompactWindow must be at least 1"},
		{`{"version":2,"accounts":[{"id":1,"configDir":"~/one","claude":{"cleanupPeriodDays":0}}]}`, "accounts[0].cleanupPeriodDays"},
		{`{"version":2,"accounts":[{"id":1,"configDir":"~/one","claude":{"helperModel":""}}]}`, "accounts[0].helperModel must be a non-empty model name without whitespace"},
	} {
		if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path, home, nil); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("invalid %s error = %v", tc.want, err)
		} else if (strings.Contains(tc.want, "autoCompactWindow") || strings.Contains(tc.want, "helperModel must be")) &&
			err.Error() != "config "+path+": "+tc.want {
			t.Fatalf("invalid %s error = %v, want exact scope and path", tc.want, err)
		}
	}
}

// An unset claude.helperModel is Haiku 5.5, for the top level, for an account
// that overrides other keys, and for an account the roster does not know.
func TestClaudeHelperModelDefaultsToHaiku(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(t.TempDir(), FileName)
	if err := os.WriteFile(
		path,
		[]byte(
			`{"version":2,"accounts":[{"id":1,"configDir":"~/one","claude":{"permissionMode":"prompt"}},{"id":2,"configDir":"~/two"}]}`,
		),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path, home, nil)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Source("claude.helperModel") != SourceDefault {
		t.Fatalf("default helper model source = %q", loaded.Source("claude.helperModel"))
	}
	for _, id := range []int{0, 1, 2, 99} {
		if got := loaded.EffectiveClaude(id).HelperModel; got != "claude-haiku-5-5" {
			t.Errorf("account %d helper model = %q, want claude-haiku-5-5", id, got)
		}
	}
	if got := Defaults(home, nil).Claude.HelperModel; got != "claude-haiku-5-5" {
		t.Errorf("Defaults helper model = %q, want claude-haiku-5-5", got)
	}
}

func TestClaudePluginCheckoutRoot(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(t.TempDir(), FileName)
	for _, tc := range []struct {
		content, want, err string
		source             Source
	}{
		{`{"version":2,"claude":{"pluginCheckoutRoot":"~/src/plugins"}}`, filepath.Join(home, "src", "plugins"), "", SourceFile},
		{`{"version":2,"claude":{"pluginCheckoutRoot":"/opt/plugins/"}}`, "/opt/plugins", "", SourceFile},
		{`{"version":2,"claude":{"pluginCheckoutRoot":null}}`, "", "", SourceDefault},
		{`{"version":2}`, "", "", SourceDefault},
		{`{"version":2,"claude":{"pluginCheckoutRoot":"src"}}`, "", "claude.pluginCheckoutRoot must be absolute or start with ~/ or $HOME/, got \"src\"", ""},
		{
			`{"version":2,"accounts":[{"id":1,"configDir":"~/one","claude":{"pluginCheckoutRoot":"/opt/plugins"}}]}`, "",
			"accounts[0].pluginCheckoutRoot is machine-wide: set claude.pluginCheckoutRoot", "",
		},
	} {
		if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
			t.Fatal(err)
		}
		loaded, err := Load(path, home, nil)
		if tc.err != "" {
			if err == nil || err.Error() != "config "+path+": "+tc.err {
				t.Fatalf("%s error = %v, want %q", tc.content, err, tc.err)
			}
			continue
		}
		if err != nil || loaded.Claude.PluginCheckoutRoot != tc.want ||
			loaded.Source(KeyClaudePluginCheckoutRoot) != tc.source {
			t.Fatalf("%s root=%q source=%q err=%v, want %q %q", tc.content, loaded.Claude.PluginCheckoutRoot,
				loaded.Source(KeyClaudePluginCheckoutRoot), err, tc.want, tc.source)
		}
		encoded, err := Marshal(loaded, false)
		if err != nil {
			t.Fatal(err)
		}
		var value struct {
			Claude map[string]any `json:"claude"`
		}
		if err := json.Unmarshal(encoded, &value); err != nil {
			t.Fatal(err)
		}
		// An unset root is left out of the file, never written as null or "".
		if got, found := value.Claude["pluginCheckoutRoot"]; (tc.want == "" && found) ||
			(tc.want != "" && got != tc.want) {
			t.Fatalf("%s marshals pluginCheckoutRoot=%v (found %v), want %q", tc.content, got, found, tc.want)
		}
	}
}

// claude.costLedger is the machine-wide switch of the per-call cost ledger: on by default, an
// account may not set it, and Marshal writes the value back when the file set it.
func TestClaudeCostLedgerSwitch(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(t.TempDir(), FileName)
	for _, tc := range []struct {
		content, err string
		want         bool
		source       Source
	}{
		{`{"version":2}`, "", true, SourceDefault},
		{`{"version":2,"claude":{"costLedger":false}}`, "", false, SourceFile},
		{`{"version":2,"claude":{"costLedger":true}}`, "", true, SourceFile},
		{
			`{"version":2,"accounts":[{"id":1,"configDir":"~/one","claude":{"costLedger":false}}]}`,
			"accounts[0].costLedger is machine-wide: set claude.costLedger", false, "",
		},
	} {
		if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
			t.Fatal(err)
		}
		loaded, err := Load(path, home, nil)
		if tc.err != "" {
			if err == nil || err.Error() != "config "+path+": "+tc.err {
				t.Fatalf("%s error = %v, want %q", tc.content, err, tc.err)
			}
			continue
		}
		if err != nil || loaded.Claude.CostLedger != tc.want || loaded.Source(KeyClaudeCostLedger) != tc.source {
			t.Fatalf("%s costLedger=%v source=%q err=%v, want %v %q", tc.content, loaded.Claude.CostLedger,
				loaded.Source(KeyClaudeCostLedger), err, tc.want, tc.source)
		}
		encoded, err := Marshal(loaded, false)
		if err != nil {
			t.Fatal(err)
		}
		var value struct {
			Claude map[string]any `json:"claude"`
		}
		if err := json.Unmarshal(encoded, &value); err != nil {
			t.Fatal(err)
		}
		// The default switch left unset stays out of the file; a value the file set is written.
		got, found := value.Claude["costLedger"]
		if wantFound := tc.source == SourceFile; found != wantFound || (found && got != tc.want) {
			t.Fatalf("%s marshals costLedger=%v (found %v), want %v (found %v)",
				tc.content, got, found, tc.want, wantFound)
		}
	}
	for _, def := range Keys() {
		if def.Key == KeyClaudeCostLedger && def.Default != true {
			t.Fatalf("Keys() lists %s default %v, want true", def.Key, def.Default)
		}
	}
}

// v079ClaudeKeys are the claude.* keys a pfm built at v0.79.0 decodes. It reads the machine file
// strictly (DisallowUnknownFields), so a key outside this set makes it refuse the whole file.
var v079ClaudeKeys = map[string]bool{
	"permissionMode": true, "webSearchesPerSession": true, "autoCompactWindow": true, "tmuxTruecolor": true,
	"cleanupPeriodDays": true, "requireManagedCleanup": true, "binary": true, "theme": true, "cache1h": true,
	"nativeCursor": true, "systemPrompt": true, "compactNudge": true, "maxSubagentSpawnDepth": true,
	"maxConcurrentSubagents": true,
}

// releaseClaudeKeys are the claude.* keys added after v0.79.0.
var releaseClaudeKeys = []string{"helperModel", "pluginCheckoutRoot", "plugins", "costLedger", "gptAgents"}

// pfm config init writes a file a pfm of the last release still decodes: a claude.* key added
// since stays out of the default file (machine block and account block alike) while it is unset or
// at its default, and a value the user set is written, and survives the round trip, even when it
// equals the default.
func TestMarshalClaudeKeysAddedSinceTheLastReleaseAreWrittenOnlyWhenSet(t *testing.T) {
	type claudeBlocks struct {
		Claude   map[string]any `json:"claude"`
		Accounts []struct {
			Claude map[string]any `json:"claude"`
		} `json:"accounts"`
	}
	home := filepath.Join(t.TempDir(), "home")
	t.Run("the default file", func(t *testing.T) {
		defaults := Defaults(home, nil)
		defaults.Accounts = []Account{{
			ID: 1, ConfigDir: filepath.Join(home, ".cc", "1"),
			Claude: &ClaudePrefs{HelperModel: DefaultHelperModel},
		}}
		for name, build := range map[string]func() ([]byte, error){
			"MarshalDefault":                  func() ([]byte, error) { return MarshalDefault(home, nil) },
			"an account block at the default": func() ([]byte, error) { return Marshal(defaults, false) },
		} {
			content, err := build()
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			var blocks claudeBlocks
			if err := json.Unmarshal(content, &blocks); err != nil {
				t.Fatal(err)
			}
			scopes := []map[string]any{blocks.Claude}
			for _, account := range blocks.Accounts {
				scopes = append(scopes, account.Claude)
			}
			for _, scope := range scopes {
				for key := range scope {
					if !v079ClaudeKeys[key] {
						t.Errorf("%s writes claude.%s, which a v0.79.0 pfm refuses: %s", name, key, content)
					}
				}
			}
		}
	})
	for _, tc := range []struct {
		name, content string
		want          map[string]any
	}{
		{
			"every key set to a non-default",
			`{"version":2,"claude":{"helperModel":"claude-sonnet-5-5","pluginCheckoutRoot":"/opt/plugins",` +
				`"plugins":["agent-effort"],"costLedger":false}}`,
			map[string]any{
				"helperModel": "claude-sonnet-5-5", "pluginCheckoutRoot": "/opt/plugins",
				"plugins": []any{"agent-effort"}, "costLedger": false,
			},
		},
		{
			"keys the user set to their defaults",
			`{"version":2,"claude":{"helperModel":"` + DefaultHelperModel + `","costLedger":true,` +
				`"plugins":["cache-live-control","sub-agent-compact","agent-effort","tool-visibility-controller"]}}`,
			map[string]any{
				"helperModel": DefaultHelperModel, "costLedger": true,
				"plugins": []any{"cache-live-control", "sub-agent-compact", "agent-effort", "tool-visibility-controller"},
			},
		},
		{"an explicitly empty list", `{"version":2,"claude":{"plugins":[]}}`, map[string]any{"plugins": []any{}}},
		{"nothing set", `{"version":2}`, map[string]any{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), FileName)
			if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
				t.Fatal(err)
			}
			loaded, err := Load(path, home, nil)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := Marshal(loaded, false)
			if err != nil {
				t.Fatal(err)
			}
			var blocks claudeBlocks
			if err := json.Unmarshal(encoded, &blocks); err != nil {
				t.Fatal(err)
			}
			for _, key := range releaseClaudeKeys {
				got, written := blocks.Claude[key]
				want, wantWritten := tc.want[key]
				if written != wantWritten || !reflect.DeepEqual(got, want) {
					t.Errorf("claude.%s = %#v (written %v), want %#v (written %v)\n%s",
						key, got, written, want, wantWritten, encoded)
				}
			}
			if err := os.WriteFile(path, encoded, 0o600); err != nil {
				t.Fatal(err)
			}
			reloaded, err := Load(path, home, nil)
			if err != nil {
				t.Fatalf("Load of the marshaled file: %v", err)
			}
			if !reflect.DeepEqual(loaded.Claude, reloaded.Claude) {
				t.Errorf("round trip changed the claude block:\n%#v\n%#v", loaded.Claude, reloaded.Claude)
			}
			for _, key := range []string{
				KeyClaudePlugins, KeyClaudePluginCheckoutRoot, KeyClaudeCostLedger, "claude.helperModel",
			} {
				if loaded.Source(key) != reloaded.Source(key) {
					t.Errorf(
						"round trip moved the source of %s: %s -> %s",
						key,
						loaded.Source(key),
						reloaded.Source(key),
					)
				}
			}
		})
	}
}

func TestGptAgentsMachineWideOptInAndDefaultOmission(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, FileName)
	defaults := Defaults(home, nil)
	body, err := Marshal(defaults, false)
	if err != nil || strings.Contains(string(body), `"gptAgents"`) {
		t.Fatalf("default gpt key emitted: %s %v", body, err)
	}
	for _, enabled := range []string{"true", "false"} {
		if err := os.WriteFile(path, []byte(`{"version":2,"claude":{"gptAgents":`+enabled+`}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		loaded, err := Load(path, home, nil)
		if err != nil {
			t.Fatalf("registered opt-in refused: %v", err)
		}
		if loaded.Source("claude.gptAgents") != SourceFile {
			t.Fatal("opt-in lost provenance")
		}
		body, err := Marshal(loaded, false)
		if err != nil || !strings.Contains(string(body), `"gptAgents": `+enabled) {
			t.Fatalf("explicit key lost: %s %v", body, err)
		}
	}
	if err := os.WriteFile(
		path,
		[]byte(`{"version":2,"accounts":[{"id":1,"configDir":"~/account","claude":{"gptAgents":true}}]}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path, home, nil); err == nil || !strings.Contains(err.Error(), "machine-wide") {
		t.Fatalf("account opt-in refusal=%v", err)
	}
}
